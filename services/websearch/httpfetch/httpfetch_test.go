package httpfetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"skills/services/websearch"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != DefaultUserAgent {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte("<html><head><title> Acme &amp;\n Cie </title></head><body>Café</body></html>"))
	})
	mux.HandleFunc("/ancienne", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/page", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/boucle", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/boucle", http.StatusFound)
	})
	mux.HandleFunc("/latin1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=iso-8859-1")
		w.Write([]byte("<title>Soci\xe9t\xe9</title>"))
	})
	mux.HandleFunc("/doc.pdf", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("%PDF-1.7"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFetch(t *testing.T) {
	srv := newServer(t)
	f := New(Config{AllowPrivate: true})
	ctx := context.Background()

	p, err := f.Fetch(ctx, srv.URL+"/ancienne")
	if err != nil {
		t.Fatal(err)
	}
	if p.URL != srv.URL+"/page" || p.RequestedURL != srv.URL+"/ancienne" || p.StatusCode != 200 {
		t.Fatalf("page = %+v", p)
	}
	if p.Title != "Acme & Cie" {
		t.Fatalf("titre = %q", p.Title)
	}

	p, err = f.Fetch(ctx, srv.URL+"/latin1")
	if err != nil || p.Title != "Société" {
		t.Fatalf("latin-1 : %q, %v", p.Title, err)
	}

	p, err = f.Fetch(ctx, srv.URL+"/absente")
	var se *websearch.StatusError
	if !errors.As(err, &se) || se.StatusCode != 404 || p.StatusCode != 404 {
		t.Fatalf("404 : %+v, %v", p, err)
	}

	for _, path := range []string{"/doc.pdf", "/boucle"} {
		if _, err := f.Fetch(ctx, srv.URL+path); !errors.Is(err, websearch.ErrUnreachable) {
			t.Errorf("%s : erreur = %v", path, err)
		}
	}
	if _, err := f.Fetch(ctx, "ftp://acme.test/"); !errors.Is(err, websearch.ErrInvalidURL) {
		t.Errorf("URL invalide : %v", err)
	}
}

func TestPrivateAddressRefused(t *testing.T) {
	srv := newServer(t)
	_, err := New(Config{}).Fetch(context.Background(), srv.URL+"/page")
	if !errors.Is(err, websearch.ErrUnreachable) || !errors.Is(err, errForbiddenAddr) {
		t.Fatalf("erreur = %v", err)
	}
}

func TestPublic(t *testing.T) {
	cases := map[string]bool{
		"93.184.215.14":      true,
		"2a00:1450:4007::64": true,
		"127.0.0.1":          false,
		"10.1.2.3":           false,
		"172.18.0.4":         false,
		"192.168.1.1":        false,
		"169.254.169.254":    false,
		"100.64.0.1":         false,
		"0.0.0.0":            false,
		"::1":                false,
		"fd00::1":            false,
		"::ffff:192.168.1.1": false,
		"fe80::1":            false,
		"224.0.0.1":          false,
	}
	for in, want := range cases {
		if got := public(netip.MustParseAddr(in)); got != want {
			t.Errorf("public(%s) = %v, attendu %v", in, got, want)
		}
	}
}

func TestDecodeTruncatedUTF8(t *testing.T) {
	if got := decode([]byte("Café\xc3")); got != "Café" {
		t.Fatalf("decode = %q", got)
	}
}
