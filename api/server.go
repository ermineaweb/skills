// Package api expose le runtime d'agent via une petite API JSON. L'interface
// (dossier ui/) est servie séparément, par Caddy, qui relaie /api/* ici.
//
// Routes :
//
//	POST /api/sessions                    {"timezone": "Europe/Paris", "nom": "…"} → {"session_id": "…"}
//	POST /api/sessions/{id}/messages      {"message": "…"} → {"text", "steps", "effects"}
//	GET  /api/sessions/{id}/rendez-vous   → rendez-vous à venir lus dans le calendrier
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
	"skills/datetime"
	"skills/services/calendar"
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
	cal      calendar.Provider
	now      func() time.Time
	mu       sync.Mutex
	sessions map[string]*entry
	// activate : skills activés dès la création d'une session (décision de
	// l'application hôte), ce qui évite un appel au modèle par conversation.
	activate []string
}

// Option configure le Server.
type Option func(*Server)

// WithActivatedSkills active ces skills dans chaque nouvelle session.
func WithActivatedSkills(names ...string) Option {
	return func(s *Server) { s.activate = append(s.activate, names...) }
}

// New crée le serveur. cal est le même Provider que celui du skill : il sert
// à afficher l'état réel de l'agenda, indépendamment du texte du modèle.
func New(rt *agent.Runtime, cal calendar.Provider, now func() time.Time, opts ...Option) *Server {
	if now == nil {
		now = time.Now
	}
	s := &Server{rt: rt, cal: cal, now: now, sessions: map[string]*entry{}}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Handler renvoie le routeur HTTP.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/sessions", s.createSession)
	mux.HandleFunc("POST /api/sessions/{id}/messages", s.postMessage)
	mux.HandleFunc("GET /api/sessions/{id}/rendez-vous", s.listAppointments)
	return mux
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
		writeError(w, http.StatusInternalServerError, "erreur interne")
		return
	}
	name := strings.TrimSpace(body.Nom)
	if len([]rune(name)) > 100 {
		writeError(w, http.StatusBadRequest, "nom trop long")
		return
	}
	// Démo : chaque session web correspond à un client distinct. En
	// production, ClientID et Name viennent de l'authentification.
	user := types.UserContext{ClientID: "web-" + id[:12], Name: name}
	sess, err := agent.NewSession(id, body.Timezone, user)
	if err != nil {
		writeError(w, http.StatusBadRequest, "fuseau horaire manquant ou invalide")
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

	writeJSON(w, http.StatusCreated, map[string]string{"session_id": id, "timezone": sess.Location.String()})
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
		writeError(w, http.StatusBadRequest, "message vide ou trop long")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	reply, err := s.rt.Handle(ctx, sess, msg)
	if err != nil {
		slog.ErrorContext(ctx, "tour de conversation", "session", sess.ID, "error", err)
		// Les effets déjà confirmés sont renvoyés même en cas d'erreur.
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":   "L'assistant est momentanément indisponible. Réessayez dans un instant.",
			"steps":   nonNil(reply.Steps),
			"effects": nonNil(reply.Effects),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"text":    reply.Text,
		"steps":   nonNil(reply.Steps),
		"effects": nonNil(reply.Effects),
	})
}

type appointmentView struct {
	ID               string `json:"id"`
	Start            string `json:"start"`
	Libelle          string `json:"libelle"`
	ProfessionnelNom string `json:"professionnel_nom"`
	TypeRendezVous   string `json:"type_rendez_vous"`
}

func (s *Server) listAppointments(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.session(w, r)
	if !ok {
		return
	}
	res, err := s.cal.ListAppointments(r.Context(), calendar.ListAppointmentsRequest{
		ClientID: sess.User.ClientID, From: s.now(),
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "agenda indisponible")
		return
	}
	out := make([]appointmentView, 0, len(res.Appointments))
	for _, a := range res.Appointments {
		out = append(out, appointmentView{
			ID:               a.ID,
			Start:            a.Start.In(sess.Location).Format(time.RFC3339),
			Libelle:          datetime.FormatFR(a.Start, sess.Location),
			ProfessionnelNom: a.ProfessionnelNom,
			TypeRendezVous:   a.TypeRendezVous,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"rendez_vous": out})
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) (*agent.Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.sessions[r.PathValue("id")]
	if !ok || s.now().Sub(e.lastUsed) > sessionTTL {
		writeError(w, http.StatusNotFound, "session inconnue ou expirée")
		return nil, false
	}
	e.lastUsed = s.now()
	return e.session, true
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "JSON invalide")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
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
