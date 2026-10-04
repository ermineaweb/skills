// Package httpfetch implémente websearch.WebFetcher avec un client HTTP.
//
// Les URL lues sont choisies par le modèle : le lecteur refuse par défaut
// toute adresse non publique (boucle locale, réseaux privés, services de la
// stack Docker, métadonnées cloud…), vérifiée à la connexion, après
// résolution DNS et à chaque redirection.
package httpfetch

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"skills/services/websearch"
)

// Valeurs par défaut de Config.
const (
	DefaultTimeout   = 15 * time.Second
	DefaultMaxBytes  = 2 << 20
	DefaultUserAgent = "Mozilla/5.0 (compatible; skills-prospect-research/1.0)"
	maxRedirects     = 5
)

// Config du lecteur. La valeur zéro convient.
type Config struct {
	// Timeout borne la lecture d'une page, redirections comprises.
	Timeout time.Duration
	// MaxBytes borne le corps lu ; au-delà, il est tronqué.
	MaxBytes  int64
	UserAgent string
	// AllowPrivate autorise les adresses non publiques (tests uniquement).
	AllowPrivate bool
}

// Fetcher lit des pages web. Sûr pour un usage concurrent.
type Fetcher struct {
	client    *http.Client
	maxBytes  int64
	userAgent string
}

var _ websearch.WebFetcher = (*Fetcher)(nil)

var errForbiddenAddr = errors.New("adresse non publique refusée")

// New construit le lecteur.
func New(cfg Config) *Fetcher {
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = DefaultMaxBytes
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = DefaultUserAgent
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if !cfg.AllowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil || !public(ap.Addr()) {
				return fmt.Errorf("%w : %s", errForbiddenAddr, address)
			}
			return nil
		}
	}
	transport := &http.Transport{
		Proxy:                 nil, // un proxy contournerait le contrôle des adresses
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: cfg.Timeout,
		MaxIdleConns:          20,
		IdleConnTimeout:       60 * time.Second,
	}
	client := &http.Client{
		Timeout:   cfg.Timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.New("trop de redirections")
			}
			if websearch.Domain(req.URL.String()) == "" {
				return fmt.Errorf("redirection vers une URL invalide : %s", req.URL)
			}
			return nil
		},
	}
	return &Fetcher{client: client, maxBytes: cfg.MaxBytes, userAgent: cfg.UserAgent}
}

// cgnat (100.64.0.0/10) n'est pas couvert par netip.Addr.IsPrivate.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

func public(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsGlobalUnicast() && !a.IsPrivate() && !cgnat.Contains(a)
}

// Fetch lit rawURL en suivant les redirections. Seules les pages HTML et
// texte sont lues ; un autre type de contenu (PDF, image…) est une page
// illisible (ErrUnreachable).
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (websearch.Page, error) {
	if websearch.Domain(rawURL) == "" {
		return websearch.Page{}, fmt.Errorf("%w : %q", websearch.ErrInvalidURL, rawURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return websearch.Page{}, fmt.Errorf("%w : %v", websearch.ErrInvalidURL, err)
	}
	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.1")
	req.Header.Set("Accept-Language", "fr-FR,fr;q=0.9,en;q=0.7")
	resp, err := f.client.Do(req)
	if err != nil {
		return websearch.Page{}, fmt.Errorf("%w : %w", websearch.ErrUnreachable, err)
	}
	defer resp.Body.Close()

	page := websearch.Page{
		RequestedURL: rawURL,
		URL:          resp.Request.URL.String(),
		StatusCode:   resp.StatusCode,
		ContentType:  resp.Header.Get("Content-Type"),
	}
	if resp.StatusCode >= 400 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return page, &websearch.StatusError{URL: page.URL, StatusCode: resp.StatusCode}
	}
	if !readable(page.ContentType) {
		return websearch.Page{}, fmt.Errorf("%w : contenu %q non lisible (%s)", websearch.ErrUnreachable, page.ContentType, page.URL)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBytes))
	if err != nil {
		return websearch.Page{}, fmt.Errorf("%w : %v", websearch.ErrUnreachable, err)
	}
	page.Content = decode(body)
	page.Title = title(page.Content)
	return page, nil
}

func readable(contentType string) bool {
	if contentType == "" {
		return true // le plus souvent du HTML mal servi
	}
	mt, _, err := mime.ParseMediaType(contentType)
	return err == nil && (mt == "text/html" || mt == "application/xhtml+xml" || mt == "text/plain")
}

// decode renvoie le corps en UTF-8. Un corps qui n'est pas de l'UTF-8
// valide est lu comme du Latin-1, encodage historique le plus courant des
// sites français. Un caractère coupé par la troncature (MaxBytes) est
// ignoré.
func decode(b []byte) string {
	for cut := 0; cut < utf8.UTFMax && cut <= len(b); cut++ {
		if utf8.Valid(b[:len(b)-cut]) {
			return string(b[:len(b)-cut])
		}
	}
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}

var (
	reTitle  = regexp.MustCompile(`(?is)<title\b[^>]*>(.*?)</title>`)
	reSpaces = regexp.MustCompile(`\s+`)
)

func title(content string) string {
	m := reTitle.FindStringSubmatch(content)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(reSpaces.ReplaceAllString(html.UnescapeString(m[1]), " "))
}
