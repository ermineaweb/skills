package tts_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"skills/services/tts"
	"skills/services/tts/ttstest"
)

func newService(t *testing.T, e tts.Engine, edit func(*tts.Config)) *tts.Service {
	t.Helper()
	cfg := tts.Config{
		Engine:          e,
		Voices:          map[string][]string{"fr-FR": {"ff_siwis"}, "en-US": {"af_heart", "am_adam"}},
		DefaultLanguage: "fr-FR",
		MaxTextLength:   20,
	}
	if edit != nil {
		edit(&cfg)
	}
	s, err := tts.NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func speed(v float64) *float64 { return &v }

func TestPrepareDefaults(t *testing.T) {
	s := newService(t, &ttstest.Engine{}, nil)
	r, err := s.Prepare(tts.Params{Text: "  Bonjour  "})
	if err != nil {
		t.Fatal(err)
	}
	want := tts.Request{Text: "Bonjour", Language: "fr-FR", Voice: "ff_siwis", Speed: 1, Format: tts.FormatMP3}
	if r != want {
		t.Fatalf("requête = %+v, attendu %+v", r, want)
	}

	r, err = s.Prepare(tts.Params{Text: "Hello", Language: "EN", Voice: "am_adam", Speed: speed(1.5), Format: "OPUS"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Language != "en-US" || r.Voice != "am_adam" || r.Speed != 1.5 || r.Format != tts.FormatOpus {
		t.Fatalf("requête = %+v", r)
	}
}

func TestPrepareValidation(t *testing.T) {
	s := newService(t, &ttstest.Engine{}, nil)
	cases := []struct {
		name string
		p    tts.Params
		want error
	}{
		{"texte vide", tts.Params{Text: "  \n "}, tts.ErrInvalidText},
		{"texte trop long", tts.Params{Text: strings.Repeat("é", 21)}, tts.ErrTextTooLong},
		{"langue inconnue", tts.Params{Text: "Hallo", Language: "de-DE"}, tts.ErrUnsupportedLanguage},
		{"voix inconnue", tts.Params{Text: "Bonjour", Voice: "ff_inconnue"}, tts.ErrUnsupportedVoice},
		{"voix d'une autre langue", tts.Params{Text: "Bonjour", Voice: "af_heart"}, tts.ErrUnsupportedVoice},
		{"vitesse trop faible", tts.Params{Text: "Bonjour", Speed: speed(0.1)}, tts.ErrInvalidSpeed},
		{"vitesse trop forte", tts.Params{Text: "Bonjour", Speed: speed(2.5)}, tts.ErrInvalidSpeed},
		{"format inconnu", tts.Params{Text: "Bonjour", Format: "aiff"}, tts.ErrUnsupportedFormat},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := s.Prepare(c.p); !errors.Is(err, c.want) {
				t.Fatalf("erreur = %v, attendu %v", err, c.want)
			}
		})
	}
	// Les valeurs acceptées sont indiquées.
	_, err := s.Prepare(tts.Params{Text: "Bonjour", Voice: "x"})
	if !strings.Contains(err.Error(), "ff_siwis") {
		t.Errorf("message sans les voix acceptées : %v", err)
	}
}

// mp3Only ne produit que du mp3.
type mp3Only struct{ ttstest.Engine }

func (*mp3Only) Formats() []tts.Format { return []tts.Format{tts.FormatMP3} }

func TestFormatNotProducedByEngine(t *testing.T) {
	s := newService(t, &mp3Only{}, nil)
	if _, err := s.Prepare(tts.Params{Text: "Bonjour", Format: "wav"}); !errors.Is(err, tts.ErrUnsupportedFormat) {
		t.Fatalf("erreur = %v", err)
	}
	if _, err := tts.NewService(tts.Config{Engine: &mp3Only{}, Voices: map[string][]string{"fr-FR": {"v"}}, DefaultLanguage: "fr-FR", DefaultFormat: tts.FormatOpus}); err == nil {
		t.Fatal("format par défaut non produit accepté")
	}
}

func TestStream(t *testing.T) {
	e := &ttstest.Engine{}
	s := newService(t, e, nil)
	r, _ := s.Prepare(tts.Params{Text: "Bonjour"})
	a, err := s.Stream(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Body.Close()
	b, err := io.ReadAll(a.Body)
	if err != nil || string(b) != "AUDIO:Bonjour" || a.ContentType != "audio/mpeg" {
		t.Fatalf("audio = %q (%s), %v", b, a.ContentType, err)
	}
}

func TestStreamEmptyAudio(t *testing.T) {
	empty := ""
	s := newService(t, &ttstest.Engine{Audio: &empty}, nil)
	r, _ := s.Prepare(tts.Params{Text: "..."})
	a, err := s.Stream(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(a.Body); !errors.Is(err, tts.ErrEngineError) {
		t.Fatalf("erreur = %v", err)
	}
}

func TestStreamEngineErrors(t *testing.T) {
	cases := []struct {
		err  error
		want error
	}{
		{tts.ErrEngineUnavailable, tts.ErrEngineUnavailable},
		{errors.New("panne inattendue"), tts.ErrEngineError},
	}
	for _, c := range cases {
		s := newService(t, &ttstest.Engine{Err: c.err}, nil)
		r, _ := s.Prepare(tts.Params{Text: "Bonjour"})
		if _, err := s.Stream(context.Background(), r); !errors.Is(err, c.want) {
			t.Errorf("%v : erreur = %v, attendu %v", c.err, err, c.want)
		}
	}
}

// blocking est un moteur dont le flux ne finit jamais avant l'annulation.
type blocking struct {
	ttstest.Engine
	started chan struct{}
}

func (b *blocking) Stream(ctx context.Context, _ tts.Request) (*tts.Audio, error) {
	if b.started != nil {
		b.started <- struct{}{}
	}
	pr, pw := io.Pipe()
	go func() {
		<-ctx.Done()
		pw.CloseWithError(ctx.Err())
	}()
	return &tts.Audio{Body: pr}, nil
}

func TestStreamTimeoutAndCancel(t *testing.T) {
	s := newService(t, &blocking{}, func(c *tts.Config) { c.Timeout = 20 * time.Millisecond })
	r, _ := s.Prepare(tts.Params{Text: "Bonjour"})
	a, err := s.Stream(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(a.Body); !errors.Is(err, tts.ErrTimeout) {
		t.Fatalf("délai : erreur = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s = newService(t, &blocking{}, nil)
	a, err = s.Stream(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := io.ReadAll(a.Body); !errors.Is(err, tts.ErrCancelled) {
		t.Fatalf("annulation : erreur = %v", err)
	}
}

func TestConcurrencyLimit(t *testing.T) {
	e := &blocking{started: make(chan struct{}, 2)}
	s := newService(t, e, func(c *tts.Config) { c.MaxConcurrent = 1 })
	r, _ := s.Prepare(tts.Params{Text: "Bonjour"})
	first, err := s.Stream(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	<-e.started

	// La deuxième synthèse attend son tour : son contexte expire avant.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.Stream(ctx, r); !errors.Is(err, tts.ErrTimeout) {
		t.Fatalf("erreur = %v", err)
	}
	// Fermer le premier flux libère la place.
	first.Body.Close()
	second, err := s.Stream(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	second.Body.Close()
}

type mapCache struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (c *mapCache) Get(k string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.m[k]
	return b, ok
}

func (c *mapCache) Set(k string, b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[k] = append([]byte(nil), b...)
}

func TestCache(t *testing.T) {
	e := &ttstest.Engine{}
	s := newService(t, e, func(c *tts.Config) { c.Cache = &mapCache{m: map[string][]byte{}} })
	r, _ := s.Prepare(tts.Params{Text: "Bonjour"})
	for i := 0; i < 2; i++ {
		a, err := s.Stream(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(a.Body)
		a.Body.Close()
		if string(b) != "AUDIO:Bonjour" {
			t.Fatalf("audio %d = %q", i, b)
		}
	}
	if n := len(e.Requests()); n != 1 {
		t.Fatalf("appels au moteur = %d, attendu 1", n)
	}
	other := r
	other.Voice = "autre"
	if tts.CacheKey("fake", r) == tts.CacheKey("fake", other) || tts.CacheKey("a", r) == tts.CacheKey("b", r) {
		t.Fatal("clé de cache indépendante de la voix ou du moteur")
	}
}
