package kokoro

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"skills/services/tts"
)

var req = tts.Request{Text: "Bonjour.", Language: "fr-FR", Voice: "ff_siwis", Speed: 1.2, Format: tts.FormatOpus}

func newEngine(t *testing.T, h http.HandlerFunc) *Engine {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	e, err := New(Config{BaseURL: srv.URL + "/", Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestStreamOK(t *testing.T) {
	var got speechRequest
	e := newEngine(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/audio/speech" {
			t.Errorf("requête = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "audio/opus")
		_, _ = w.Write([]byte("OggS..."))
	})
	a, err := e.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Body.Close()
	b, _ := io.ReadAll(a.Body)
	if string(b) != "OggS..." || a.ContentType != "audio/ogg" {
		t.Fatalf("audio = %q (%s)", b, a.ContentType)
	}
	want := speechRequest{Model: "kokoro", Input: "Bonjour.", Voice: "ff_siwis", ResponseFormat: "opus", Speed: 1.2, Stream: true, LangCode: "f"}
	if got != want {
		t.Fatalf("corps = %+v, attendu %+v", got, want)
	}
}

func TestStreamHTTPErrors(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   error
	}{
		{400, `{"detail":{"error":"validation_error","message":"Voice 'x' not found."}}`, tts.ErrEngineError},
		{500, `{"detail":"boom"}`, tts.ErrEngineError},
		{503, ``, tts.ErrEngineUnavailable},
	}
	for _, c := range cases {
		e := newEngine(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
			_, _ = io.WriteString(w, c.body)
		})
		_, err := e.Stream(context.Background(), req)
		if !errors.Is(err, c.want) {
			t.Errorf("HTTP %d : erreur = %v, attendu %v", c.status, err, c.want)
		}
	}
	e := newEngine(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = io.WriteString(w, `{"detail":{"message":"Voice 'x' not found."}}`)
	})
	if _, err := e.Stream(context.Background(), req); err == nil || !strings.Contains(err.Error(), "Voice 'x' not found.") {
		t.Errorf("détail du moteur absent de l'erreur (journaux) : %v", err)
	}
}

func TestStreamConnectionRefused(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	e, _ := New(Config{BaseURL: "http://" + addr})
	if _, err := e.Stream(context.Background(), req); !errors.Is(err, tts.ErrEngineUnavailable) {
		t.Fatalf("erreur = %v", err)
	}
}

func TestStreamContext(t *testing.T) {
	release := make(chan struct{})
	e := newEngine(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	// Enregistré après newEngine, donc exécuté avant srv.Close, qui attend
	// la fin du handler.
	t.Cleanup(func() { close(release) })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := e.Stream(ctx, req); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("délai : erreur = %v", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if _, err := e.Stream(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatalf("annulation : erreur = %v", err)
	}
}

// Le serveur coupe la connexion au milieu du flux.
func TestStreamInterrupted(t *testing.T) {
	e := newEngine(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("ID3 première phrase"))
		w.(http.Flusher).Flush()
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	})
	a, err := e.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Body.Close()
	if _, err := io.ReadAll(a.Body); err == nil {
		t.Fatal("flux interrompu lu sans erreur")
	}
}

func TestLanguages(t *testing.T) {
	e, _ := New(Config{BaseURL: "http://kokoro:8880"})
	for lang, want := range map[string]bool{"fr-FR": true, "fr": true, "EN_us": true, "en-GB": true, "de-DE": false, "": false} {
		if got := e.SupportsLanguage(lang); got != want {
			t.Errorf("SupportsLanguage(%q) = %v", lang, got)
		}
	}
	if _, err := e.Stream(context.Background(), tts.Request{Language: "de-DE", Format: tts.FormatMP3}); !errors.Is(err, tts.ErrUnsupportedLanguage) {
		t.Errorf("langue non prise en charge : %v", err)
	}
	if _, err := New(Config{BaseURL: "kokoro:8880"}); err == nil {
		t.Error("URL invalide acceptée")
	}
}
