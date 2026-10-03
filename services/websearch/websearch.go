// Package websearch définit les contrats de recherche web et de lecture de
// pages utilisés par le skill prospect-research.
//
// Le skill et ses tools ne dépendent que de SearchEngine et WebFetcher :
// l'implémentation réelle (API de recherche, client HTTP) et le mock
// déterministe des tests (package websearchtest) sont interchangeables.
package websearch

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// SearchEngine interroge un moteur de recherche.
//
// Une recherche sans résultat renvoie une liste vide et une erreur nil :
// l'absence de résultat n'est pas une panne.
type SearchEngine interface {
	Search(ctx context.Context, query string) ([]SearchResult, error)
}

// WebFetcher lit une page web.
//
// Une réponse HTTP d'erreur (4xx, 5xx) renvoie la Page reçue (StatusCode
// renseigné) et une *StatusError. Une page injoignable (hôte inconnu,
// délai dépassé) renvoie une erreur qui enveloppe ErrUnreachable.
type WebFetcher interface {
	Fetch(ctx context.Context, url string) (Page, error)
}

// SearchResult est un résultat tel que l'affiche un moteur de recherche.
// Le snippet est un extrait choisi par le moteur : il oriente la recherche
// mais ne vérifie rien, seule la page lue avec WebFetcher fait foi.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
	// Domain est l'hôte de URL sans « www. » (voir Domain), vide si l'URL
	// est invalide.
	Domain string `json:"domain"`
	// PublishedAt est la date affichée par le moteur (AAAA-MM-JJ), vide si
	// le moteur n'en affiche pas.
	PublishedAt string `json:"published_at,omitempty"`
}

// Page est une page lue.
type Page struct {
	// RequestedURL est l'URL demandée ; URL est l'URL finale, après
	// redirections. Elles diffèrent quand un domaine alias redirige vers le
	// domaine principal.
	RequestedURL string `json:"requested_url"`
	URL          string `json:"url"`
	StatusCode   int    `json:"status_code"`
	ContentType  string `json:"content_type"`
	// Title est le contenu de la balise <title>, vide si absente.
	Title string `json:"title"`
	// Content est le corps brut de la réponse (HTML le plus souvent).
	Content string `json:"content"`
}

var (
	// ErrSearchFailed : le moteur de recherche n'a pas pu répondre.
	ErrSearchFailed = errors.New("websearch: recherche impossible")
	// ErrUnreachable : la page n'a pas pu être lue (hôte inconnu, délai
	// dépassé, connexion refusée…).
	ErrUnreachable = errors.New("websearch: page injoignable")
	// ErrInvalidURL : l'URL n'est pas une URL http(s) absolue.
	ErrInvalidURL = errors.New("websearch: URL invalide")
)

// StatusError est renvoyée quand le serveur répond avec un code HTTP d'erreur.
type StatusError struct {
	URL        string
	StatusCode int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("websearch: %s a répondu %d", e.URL, e.StatusCode)
}

// Domain renvoie l'hôte d'une URL http(s), en minuscules et sans « www. »,
// ou "" si l'URL est invalide. C'est la clé de déduplication la plus simple
// entre résultats.
func Domain(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}
