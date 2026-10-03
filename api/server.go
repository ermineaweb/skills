// Package api expose le runtime d'agent via une petite API JSON. L'interface
// (dossier ui/) est servie séparément, par Caddy, qui relaie /api/* ici.
//
// Routes :
//
//	POST /api/sessions                    {"timezone": "Europe/Paris", "nom": "…"} → {"session_id": "…"}
//	POST /api/sessions/{id}/messages      {"message": "…"} → {"text", "steps", "effects"}
//	GET  /api/skills                      → {"skills": […]} skills enregistrés
//
// Le package ne connaît aucun skill ni service métier : les routes propres à
// un domaine (ex. l'agenda, voir api/calendarapi) s'ajoutent sous
// /api/sessions/{id}/ avec WithSessionRoute.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"skills/agent"
	"skills/types"
)

const (
	maxBodyBytes  = 64 << 10
	maxMessageLen = 2000
	sessionTTL    = time.Hour
	// Un tour peut enchaîner plusieurs appels au modèle ; sur CPU, chacun
	// peut prendre plus d'une minute.
	requestTimeout = 15 * time.Minute
)

type entry struct {
	session  *agent.Session
	lastUsed time.Time
}

// Server relie les requêtes HTTP au runtime. Les sessions sont en mémoire.
type Server struct {
	rt       *agent.Runtime
	now      func() time.Time
	mu       sync.Mutex
	sessions map[string]*entry
	// activate : skills activés dès la création d'une session (décision de
	// l'application hôte), ce qui évite un appel au modèle par conversation.
	activate []string
	routes   []sessionRoute
}

// SessionHandler traite une requête portant sur une session existante.
type SessionHandler func(w http.ResponseWriter, r *http.Request, sess *agent.Session)

type sessionRoute struct {
	method, path string
	handler      SessionHandler
}

// Option configure le Server.
type Option func(*Server)

// WithActivatedSkills active ces skills dans chaque nouvelle session.
func WithActivatedSkills(names ...string) Option {
	return func(s *Server) { s.activate = append(s.activate, names...) }
}

// WithSessionRoute ajoute la route « method /api/sessions/{id}/path ». Le
// serveur résout la session (404 si inconnue ou expirée) avant d'appeler h.
func WithSessionRoute(method, path string, h SessionHandler) Option {
	return func(s *Server) {
		s.routes = append(s.routes, sessionRoute{method: method, path: path, handler: h})
	}
}

// New crée le serveur.
func New(rt *agent.Runtime, now func() time.Time, opts ...Option) *Server {
	if now == nil {
		now = time.Now
	}
	s := &Server{rt: rt, now: now, sessions: map[string]*entry{}}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Handler renvoie le routeur HTTP.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/skills", s.listSkills)
	mux.HandleFunc("POST /api/sessions", s.createSession)
	mux.HandleFunc("POST /api/sessions/{id}/messages", s.postMessage)
	for _, rt := range s.routes {
		h := rt.handler
		mux.HandleFunc(rt.method+" /api/sessions/{id}/"+rt.path, func(w http.ResponseWriter, r *http.Request) {
			if sess, ok := s.session(w, r); ok {
				h(w, r, sess)
			}
		})
	}
	return mux
}

// listSkills permet à l'interface de n'afficher que ce qui concerne les
// skills chargés (suggestions, panneaux).
func (s *Server) listSkills(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, map[string][]string{"skills": nonNil(s.rt.Skills())})
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Timezone string `json:"timezone"`
		Nom      string `json:"nom"`
	}
	if !decode(w, r, &body) {
		return
	}
	id, err := randomID()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "erreur interne")
		return
	}
	name := strings.TrimSpace(body.Nom)
	if len([]rune(name)) > 100 {
		WriteError(w, http.StatusBadRequest, "nom trop long")
		return
	}
	// Démo : chaque session web correspond à un client distinct. En
	// production, ClientID et Name viennent de l'authentification.
	user := types.UserContext{ClientID: "web-" + id[:12], Name: name}
	sess, err := agent.NewSession(id, body.Timezone, user)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "fuseau horaire manquant ou invalide")
		return
	}
	for _, name := range s.activate {
		sess.ActivateSkill(name)
	}

	s.mu.Lock()
	now := s.now()
	for k, e := range s.sessions {
		if now.Sub(e.lastUsed) > sessionTTL {
			delete(s.sessions, k)
		}
	}
	s.sessions[id] = &entry{session: sess, lastUsed: now}
	s.mu.Unlock()

	WriteJSON(w, http.StatusCreated, map[string]string{"session_id": id, "timezone": sess.Location.String()})
}

func (s *Server) postMessage(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.session(w, r)
	if !ok {
		return
	}
	var body struct {
		Message string `json:"message"`
	}
	if !decode(w, r, &body) {
		return
	}
	msg := strings.TrimSpace(body.Message)
	if msg == "" || len([]rune(msg)) > maxMessageLen {
		WriteError(w, http.StatusBadRequest, "message vide ou trop long")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	reply, err := s.rt.Handle(ctx, sess, msg)
	if err != nil {
		slog.ErrorContext(ctx, "tour de conversation", "session", sess.ID, "error", err)
		// Les effets déjà confirmés sont renvoyés même en cas d'erreur.
		WriteJSON(w, http.StatusBadGateway, map[string]any{
			"error":   "L'assistant est momentanément indisponible. Réessayez dans un instant.",
			"steps":   nonNil(reply.Steps),
			"effects": nonNil(reply.Effects),
		})
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"text":    reply.Text,
		"steps":   nonNil(reply.Steps),
		"effects": nonNil(reply.Effects),
	})
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) (*agent.Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.sessions[r.PathValue("id")]
	if !ok || s.now().Sub(e.lastUsed) > sessionTTL {
		WriteError(w, http.StatusNotFound, "session inconnue ou expirée")
		return nil, false
	}
	e.lastUsed = s.now()
	return e.session, true
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		WriteError(w, http.StatusBadRequest, "JSON invalide")
		return false
	}
	return true
}

// WriteJSON écrit v en JSON avec le code status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError écrit {"error": msg} avec le code status.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", errors.New("aléa indisponible")
	}
	return hex.EncodeToString(b), nil
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
