// Package kokoro implémente tts.Engine pour Kokoro-FastAPI, serveur HTTP
// du modèle Kokoro (service kokoro de compose.yaml) :
//
//	POST {BaseURL}/v1/audio/speech
//	{"model", "input", "voice", "response_format", "speed", "stream", "lang_code"}
//
// C'est le seul package qui connaît Kokoro : ses codes de langue, ses
// formats, son API et ses erreurs.
package kokoro

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"skills/services/tts"
)

// Config de l'adaptateur.
type Config struct {
	// BaseURL du serveur (ex : http://kokoro:8880). Obligatoire.
	BaseURL string
	// Model : modèle demandé au serveur (défaut : kokoro).
	Model string
	// Stream : audio envoyé phrase par phrase, au fil de la génération.
	// Sinon, le serveur répond une fois l'audio complet généré.
	Stream bool
	// HTTPClient facultatif. Il ne doit pas avoir de Timeout global, qui
	// couperait un flux : la durée est bornée par le contexte.
	HTTPClient *http.Client
}

// Engine parle à un serveur Kokoro-FastAPI. Sûr pour un usage concurrent.
type Engine struct {
	endpoint string
	cfg      Config
}

var _ tts.Engine = (*Engine)(nil)

// New construit l'adaptateur.
func New(cfg Config) (*Engine, error) {
	u, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("kokoro: URL %q invalide (attendu http(s)://hôte[:port])", cfg.BaseURL)
	}
	if cfg.Model == "" {
		cfg.Model = "kokoro"
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	return &Engine{endpoint: strings.TrimSuffix(u.String(), "/") + "/v1/audio/speech", cfg: cfg}, nil
}

// Name implémente tts.Engine.
func (e *Engine) Name() string { return "kokoro/" + e.cfg.Model }

// Formats implémente tts.Engine.
func (e *Engine) Formats() []tts.Format {
	return []tts.Format{tts.FormatMP3, tts.FormatOpus, tts.FormatWAV}
}

// langCodes : langues de Kokoro (BCP 47 → lang_code). Une langue sans
// région désigne sa variante principale.
var langCodes = map[string]string{
	"en-us": "a", "en": "a",
	"en-gb": "b",
	"es-es": "e", "es": "e",
	"fr-fr": "f", "fr": "f",
	"hi-in": "h", "hi": "h",
	"it-it": "i", "it": "i",
	"ja-jp": "j", "ja": "j",
	"pt-br": "p", "pt": "p",
	"zh-cn": "z", "zh": "z",
}

func langCode(language string) string {
	return langCodes[strings.ToLower(strings.ReplaceAll(language, "_", "-"))]
}

// SupportsLanguage implémente tts.Engine.
func (e *Engine) SupportsLanguage(language string) bool { return langCode(language) != "" }

type speechRequest struct {
	Model          string  `json:"model"`
	Input          string  `json:"input"`
	Voice          string  `json:"voice"`
	ResponseFormat string  `json:"response_format"`
	Speed          float64 `json:"speed"`
	Stream         bool    `json:"stream"`
	LangCode       string  `json:"lang_code"`
}

// Stream implémente tts.Engine.
func (e *Engine) Stream(ctx context.Context, req tts.Request) (*tts.Audio, error) {
	code := langCode(req.Language)
	if code == "" {
		return nil, fmt.Errorf("%w : %s", tts.ErrUnsupportedLanguage, req.Language)
	}
	if req.Format.ContentType() == "" {
		return nil, fmt.Errorf("%w : %s", tts.ErrUnsupportedFormat, req.Format)
	}
	payload, err := json.Marshal(speechRequest{
		Model: e.cfg.Model, Input: req.Text, Voice: req.Voice, ResponseFormat: string(req.Format),
		Speed: req.Speed, Stream: e.cfg.Stream, LangCode: code,
	})
	if err != nil {
		return nil, fmt.Errorf("%w : %v", tts.ErrEngineError, err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%w : %v", tts.ErrEngineError, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := e.cfg.HTTPClient.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w : %v", tts.ErrEngineUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		detail := errorDetail(resp.Body)
		switch resp.StatusCode {
		case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return nil, fmt.Errorf("%w : HTTP %d %s", tts.ErrEngineUnavailable, resp.StatusCode, detail)
		default:
			return nil, fmt.Errorf("%w : HTTP %d %s", tts.ErrEngineError, resp.StatusCode, detail)
		}
	}
	// Type MIME du contrat (Kokoro sert l'Opus en audio/opus).
	return &tts.Audio{ContentType: req.Format.ContentType(), Body: resp.Body}, nil
}

// errorDetail extrait le message d'erreur de Kokoro-FastAPI :
// {"detail": {"message": "…"}} ou {"detail": "…"}, sinon le début du corps.
func errorDetail(r io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(r, 4<<10))
	msg := strings.TrimSpace(string(data))
	var body struct {
		Detail json.RawMessage `json:"detail"`
	}
	if json.Unmarshal(data, &body) == nil && len(body.Detail) > 0 {
		var d struct {
			Message string `json:"message"`
		}
		var s string
		if json.Unmarshal(body.Detail, &d) == nil && d.Message != "" {
			msg = d.Message
		} else if json.Unmarshal(body.Detail, &s) == nil {
			msg = s
		}
	}
	if r := []rune(msg); len(r) > 300 {
		msg = string(r[:300]) + "…"
	}
	return msg
}
