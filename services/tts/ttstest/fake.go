// Package ttstest fournit un moteur de synthèse vocale factice pour les
// tests : aucun appel réseau, audio déterministe.
package ttstest

import (
	"context"
	"io"
	"strings"
	"sync"

	"skills/services/tts"
)

// Engine implémente tts.Engine. Sûr pour un usage concurrent.
type Engine struct {
	// Err, si non nil, est renvoyée par Stream.
	Err error
	// Audio : contenu renvoyé (défaut : "AUDIO:" suivi du texte).
	Audio *string

	mu       sync.Mutex
	requests []tts.Request
}

var _ tts.Engine = (*Engine)(nil)

// Name implémente tts.Engine.
func (e *Engine) Name() string { return "fake" }

// Formats implémente tts.Engine.
func (e *Engine) Formats() []tts.Format {
	return []tts.Format{tts.FormatMP3, tts.FormatOpus, tts.FormatWAV}
}

// SupportsLanguage implémente tts.Engine : français et anglais.
func (e *Engine) SupportsLanguage(language string) bool {
	l := strings.ToLower(language)
	return strings.HasPrefix(l, "fr") || strings.HasPrefix(l, "en")
}

// Stream implémente tts.Engine.
func (e *Engine) Stream(ctx context.Context, req tts.Request) (*tts.Audio, error) {
	e.mu.Lock()
	e.requests = append(e.requests, req)
	e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.Err != nil {
		return nil, e.Err
	}
	audio := "AUDIO:" + req.Text
	if e.Audio != nil {
		audio = *e.Audio
	}
	return &tts.Audio{ContentType: req.Format.ContentType(), Body: io.NopCloser(strings.NewReader(audio))}, nil
}

// Requests renvoie les demandes reçues.
func (e *Engine) Requests() []tts.Request {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]tts.Request(nil), e.requests...)
}
