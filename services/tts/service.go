package tts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Valeurs par défaut de Config.
const (
	DefaultMaxTextLength = 2000
	DefaultTimeout       = 2 * time.Minute
	DefaultMaxConcurrent = 2
	DefaultMinSpeed      = 0.5
	DefaultMaxSpeed      = 2.0
	// maxCachedBytes : un audio plus long n'est pas mis en cache.
	maxCachedBytes = 4 << 20
)

// Cache conserve des audios déjà générés (voir CacheKey). Facultatif.
type Cache interface {
	Get(key string) ([]byte, bool)
	Set(key string, audio []byte)
}

// CacheKey identifie un audio : moteur et paramètres complets.
func CacheKey(engine string, r Request) string {
	h := sha256.New()
	for _, part := range []string{engine, r.Language, r.Voice, strconv.FormatFloat(r.Speed, 'f', -1, 64), string(r.Format), r.Text} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Config du service.
type Config struct {
	Engine Engine
	// Voices associe chaque langue proposée (BCP 47) à ses voix ; la
	// première est la voix par défaut de la langue. Obligatoire.
	Voices map[string][]string
	// DefaultLanguage doit figurer dans Voices.
	DefaultLanguage string
	// DefaultFormat doit être produit par le moteur. Défaut : mp3.
	DefaultFormat Format
	// Vitesse par défaut (défaut 1) et bornes acceptées (défaut 0,5 à 2).
	DefaultSpeed, MinSpeed, MaxSpeed float64
	// MaxTextLength : nombre maximal de caractères (défaut 2000).
	MaxTextLength int
	// Timeout borne une synthèse, lecture du flux comprise (défaut 2 min).
	Timeout time.Duration
	// MaxConcurrent : synthèses simultanées (défaut 2) ; les suivantes
	// attendent leur tour.
	MaxConcurrent int
	// Cache facultatif.
	Cache Cache
}

// Service valide les demandes et les transmet au moteur. Sûr pour un usage
// concurrent.
type Service struct {
	cfg     Config
	formats []Format
	sem     chan struct{}
}

// NewService vérifie la configuration au regard des capacités du moteur.
func NewService(cfg Config) (*Service, error) {
	if cfg.Engine == nil {
		return nil, errors.New("tts: moteur obligatoire")
	}
	if cfg.DefaultFormat == "" {
		cfg.DefaultFormat = FormatMP3
	}
	if cfg.DefaultSpeed == 0 {
		cfg.DefaultSpeed = 1
	}
	if cfg.MinSpeed == 0 {
		cfg.MinSpeed = DefaultMinSpeed
	}
	if cfg.MaxSpeed == 0 {
		cfg.MaxSpeed = DefaultMaxSpeed
	}
	if cfg.MaxTextLength <= 0 {
		cfg.MaxTextLength = DefaultMaxTextLength
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = DefaultMaxConcurrent
	}
	if len(cfg.Voices) == 0 {
		return nil, errors.New("tts: aucune voix configurée")
	}
	for lang, voices := range cfg.Voices {
		if len(voices) == 0 {
			return nil, fmt.Errorf("tts: aucune voix pour la langue %s", lang)
		}
		if !cfg.Engine.SupportsLanguage(lang) {
			return nil, fmt.Errorf("tts: le moteur %s ne lit pas la langue %s", cfg.Engine.Name(), lang)
		}
	}
	if _, ok := cfg.Voices[cfg.DefaultLanguage]; !ok {
		return nil, fmt.Errorf("tts: langue par défaut %q absente des voix configurées", cfg.DefaultLanguage)
	}
	s := &Service{cfg: cfg, sem: make(chan struct{}, cfg.MaxConcurrent)}
	for _, f := range cfg.Engine.Formats() {
		if f.ContentType() != "" {
			s.formats = append(s.formats, f)
		}
	}
	if !slices.Contains(s.formats, cfg.DefaultFormat) {
		return nil, fmt.Errorf("tts: le moteur %s ne produit pas le format %q", cfg.Engine.Name(), cfg.DefaultFormat)
	}
	if !(cfg.MinSpeed > 0 && cfg.MinSpeed <= cfg.DefaultSpeed && cfg.DefaultSpeed <= cfg.MaxSpeed) {
		return nil, fmt.Errorf("tts: vitesses incohérentes (min %v, défaut %v, max %v)", cfg.MinSpeed, cfg.DefaultSpeed, cfg.MaxSpeed)
	}
	return s, nil
}

// EngineName renvoie le nom du moteur.
func (s *Service) EngineName() string { return s.cfg.Engine.Name() }

// Catalog décrit ce que le service accepte, pour l'expliquer au modèle.
type Catalog struct {
	DefaultLanguage    string
	Voices             map[string][]string
	Formats            []Format
	DefaultFormat      Format
	MinSpeed, MaxSpeed float64
	MaxTextLength      int
}

// Catalog renvoie les langues, voix, formats et limites acceptés.
func (s *Service) Catalog() Catalog {
	return Catalog{
		DefaultLanguage: s.cfg.DefaultLanguage,
		Voices:          s.cfg.Voices,
		Formats:         s.formats,
		DefaultFormat:   s.cfg.DefaultFormat,
		MinSpeed:        s.cfg.MinSpeed,
		MaxSpeed:        s.cfg.MaxSpeed,
		MaxTextLength:   s.cfg.MaxTextLength,
	}
}

// Params est une demande brute ; les champs vides prennent la valeur par
// défaut.
type Params struct {
	Text     string
	Language string
	Voice    string
	Speed    *float64
	Format   string
}

// Prepare valide p et complète les valeurs par défaut. Les erreurs
// enveloppent ErrInvalidText, ErrTextTooLong, ErrUnsupportedLanguage,
// ErrUnsupportedVoice, ErrInvalidSpeed ou ErrUnsupportedFormat, avec un
// message qui indique les valeurs acceptées.
func (s *Service) Prepare(p Params) (Request, error) {
	text := strings.TrimSpace(p.Text)
	if text == "" {
		return Request{}, ErrInvalidText
	}
	if n := utf8.RuneCountInString(text); n > s.cfg.MaxTextLength {
		return Request{}, fmt.Errorf("%w : %d caractères, au plus %d", ErrTextTooLong, n, s.cfg.MaxTextLength)
	}
	r := Request{Text: text, Language: s.cfg.DefaultLanguage, Speed: s.cfg.DefaultSpeed, Format: s.cfg.DefaultFormat}

	if p.Language != "" {
		lang, ok := s.language(p.Language)
		if !ok {
			return Request{}, fmt.Errorf("%w : %q (langues : %s)", ErrUnsupportedLanguage, p.Language, strings.Join(s.languages(), ", "))
		}
		r.Language = lang
	}
	voices := s.cfg.Voices[r.Language]
	r.Voice = voices[0]
	if p.Voice != "" {
		if !slices.Contains(voices, p.Voice) {
			return Request{}, fmt.Errorf("%w : %q pour %s (voix : %s)", ErrUnsupportedVoice, p.Voice, r.Language, strings.Join(voices, ", "))
		}
		r.Voice = p.Voice
	}
	if p.Speed != nil {
		v := *p.Speed
		if math.IsNaN(v) || v < s.cfg.MinSpeed || v > s.cfg.MaxSpeed {
			return Request{}, fmt.Errorf("%w : %v (de %v à %v)", ErrInvalidSpeed, v, s.cfg.MinSpeed, s.cfg.MaxSpeed)
		}
		r.Speed = v
	}
	if p.Format != "" {
		f, ok := ParseFormat(p.Format)
		if !ok || !slices.Contains(s.formats, f) {
			names := make([]string, len(s.formats))
			for i, f := range s.formats {
				names[i] = string(f)
			}
			return Request{}, fmt.Errorf("%w : %q (formats : %s)", ErrUnsupportedFormat, p.Format, strings.Join(names, ", "))
		}
		r.Format = f
	}
	return r, nil
}

// language reconnaît une langue configurée, sans tenir compte de la casse ;
// une langue sans région (« fr ») désigne la première langue configurée de
// même langue principale, par ordre alphabétique.
func (s *Service) language(raw string) (string, bool) {
	raw = strings.ReplaceAll(strings.TrimSpace(raw), "_", "-")
	for _, l := range s.languages() {
		if strings.EqualFold(l, raw) {
			return l, true
		}
	}
	if !strings.Contains(raw, "-") {
		for _, l := range s.languages() {
			if primary, _, _ := strings.Cut(l, "-"); strings.EqualFold(primary, raw) {
				return l, true
			}
		}
	}
	return "", false
}

func (s *Service) languages() []string {
	langs := make([]string, 0, len(s.cfg.Voices))
	for l := range s.cfg.Voices {
		langs = append(langs, l)
	}
	slices.Sort(langs)
	return langs
}

// Stream lance la synthèse de r (validée par Prepare). Body doit être
// fermé ; il se ferme aussi de lui-même en cas d'erreur de lecture.
//
// Les erreurs (de Stream comme de Body.Read) enveloppent ErrTimeout,
// ErrCancelled, ErrEngineUnavailable ou ErrEngineError. Un audio vide est
// une erreur du moteur.
func (s *Service) Stream(ctx context.Context, r Request) (*Audio, error) {
	log := slog.With("moteur", s.EngineName(), "langue", r.Language, "voix", r.Voice,
		"format", r.Format, "vitesse", r.Speed, "caracteres", utf8.RuneCountInString(r.Text))
	var key string
	if s.cfg.Cache != nil {
		key = CacheKey(s.EngineName(), r)
		if b, ok := s.cfg.Cache.Get(key); ok {
			log.InfoContext(ctx, "tts: audio servi depuis le cache", "octets", len(b))
			return &Audio{ContentType: r.Format.ContentType(), Body: io.NopCloser(bytes.NewReader(b))}, nil
		}
	}

	select {
	case s.sem <- struct{}{}:
	case <-ctx.Done():
		err := classify(ctx, ctx.Err())
		log.InfoContext(ctx, "tts: synthèse abandonnée avant son début", "error", err)
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	start := time.Now()
	log.InfoContext(ctx, "tts: début de synthèse")

	audio, err := s.cfg.Engine.Stream(ctx, r)
	if err != nil {
		err = classify(ctx, err)
		cancel()
		<-s.sem
		logEnd(ctx, log, err, start, 0, 0)
		return nil, err
	}
	b := &body{ctx: ctx, rc: audio.Body, log: log, start: start}
	b.release = func() { cancel(); <-s.sem }
	if s.cfg.Cache != nil {
		b.cache, b.key = s.cfg.Cache, key
	}
	ct := audio.ContentType
	if ct == "" {
		ct = r.Format.ContentType()
	}
	return &Audio{ContentType: ct, Body: b}, nil
}

// classify convertit une erreur du moteur ou du contexte en erreur du
// contrat.
func classify(ctx context.Context, err error) error {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Errorf("%w : %w", ErrTimeout, err)
	case ctx.Err() != nil:
		return fmt.Errorf("%w : %w", ErrCancelled, err)
	case errors.Is(err, ErrEngineUnavailable), errors.Is(err, ErrEngineError),
		errors.Is(err, ErrUnsupportedLanguage), errors.Is(err, ErrUnsupportedFormat):
		return err
	default:
		return fmt.Errorf("%w : %w", ErrEngineError, err)
	}
}

// body suit la lecture du flux : taille, durée, fin (complète, en erreur
// ou interrompue par le lecteur), et libère les ressources à la fermeture.
type body struct {
	ctx     context.Context
	rc      io.ReadCloser
	log     *slog.Logger
	start   time.Time
	release func()

	n         int64
	firstByte time.Duration
	done      bool  // fin du flux atteinte (EOF ou erreur)
	err       error // erreur de lecture, nil si EOF

	cache Cache
	key   string
	buf   *bytes.Buffer

	closeOnce sync.Once
}

func (b *body) Read(p []byte) (int, error) {
	if b.done {
		if b.err != nil {
			return 0, b.err
		}
		return 0, io.EOF
	}
	n, err := b.rc.Read(p)
	if n > 0 {
		if b.n == 0 {
			b.firstByte = time.Since(b.start)
		}
		b.n += int64(n)
		if b.cache != nil {
			if b.buf == nil {
				b.buf = &bytes.Buffer{}
			}
			if b.buf.Len()+n <= maxCachedBytes {
				b.buf.Write(p[:n])
			} else {
				b.cache = nil
			}
		}
	}
	switch {
	case err == io.EOF && b.n == 0:
		err = fmt.Errorf("%w : audio vide", ErrEngineError)
	case err == io.EOF:
		if b.cache != nil {
			b.cache.Set(b.key, b.buf.Bytes())
		}
	case err != nil:
		err = classify(b.ctx, err)
	}
	if err != nil {
		b.done = true
		if err != io.EOF {
			b.err = err
		}
		b.Close()
	}
	return n, err
}

func (b *body) Close() error {
	b.closeOnce.Do(func() {
		err := b.err
		if !b.done {
			err = fmt.Errorf("%w : lecture interrompue", ErrCancelled)
		}
		b.rc.Close()
		b.release()
		logEnd(b.ctx, b.log, err, b.start, b.n, b.firstByte)
	})
	return nil
}

func logEnd(ctx context.Context, log *slog.Logger, err error, start time.Time, n int64, firstByte time.Duration) {
	attrs := []any{"duree", time.Since(start).Round(time.Millisecond), "octets", n}
	if firstByte > 0 {
		attrs = append(attrs, "premier_octet", firstByte.Round(time.Millisecond))
	}
	switch {
	case err == nil:
		log.InfoContext(ctx, "tts: fin de synthèse", attrs...)
	case errors.Is(err, ErrCancelled):
		log.InfoContext(ctx, "tts: synthèse annulée", append(attrs, "error", err)...)
	default:
		log.ErrorContext(ctx, "tts: échec de synthèse", append(attrs, "error", err)...)
	}
}
