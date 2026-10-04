// Package ttsapi ajoute à l'API la lecture des audios du skill
// synthese-vocale :
//
//	GET /api/sessions/{id}/audio/{audio_id}   → flux audio (audio/mpeg, audio/ogg, audio/wav)
//
// audio_id est renvoyé par le tool lire_a_voix_haute (effet du tour de
// conversation). L'audio est généré à la demande et transmis au fil de la
// génération : le client commence la lecture avant la fin de la synthèse.
// Quand le client arrête la lecture ou se déconnecte, la synthèse est
// annulée.
package ttsapi

import (
	"errors"
	"io"
	"net/http"

	"skills/agent"
	"skills/api"
	"skills/services/tts"
	ttstools "skills/tools/tts"
)

// Options renvoie la route à passer à api.New.
func Options(svc *tts.Service) []api.Option {
	return []api.Option{api.WithSessionRoute("GET", "audio/{audio}", func(w http.ResponseWriter, r *http.Request, sess *agent.Session) {
		stream(svc, w, r, sess)
	})}
}

func stream(svc *tts.Service, w http.ResponseWriter, r *http.Request, sess *agent.Session) {
	req, ok := ttstools.Lookup(sess.State(), r.PathValue("audio"))
	if !ok {
		api.WriteError(w, http.StatusNotFound, "audio inconnu")
		return
	}
	audio, err := svc.Stream(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	defer audio.Body.Close()

	// Premier fragment lu avant l'en-tête : une erreur à ce stade reçoit
	// encore un code HTTP.
	buf := make([]byte, 32<<10)
	n, err := io.ReadAtLeast(audio.Body, buf, 1)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", audio.ContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	for {
		if _, werr := w.Write(buf[:n]); werr != nil {
			return // client parti : Close annule la synthèse
		}
		_ = rc.Flush()
		n, err = audio.Body.Read(buf)
		if errors.Is(err, io.EOF) && n == 0 {
			return
		}
		if err != nil && !errors.Is(err, io.EOF) {
			// En-tête déjà envoyé : la connexion est coupée pour que le
			// client ne prenne pas un audio tronqué pour un audio complet.
			panic(http.ErrAbortHandler)
		}
	}
}

// writeError répond sans détail du moteur (journalisé par tts.Service).
func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, tts.ErrCancelled):
		// Client parti : personne ne lira la réponse.
	case errors.Is(err, tts.ErrTimeout):
		api.WriteError(w, http.StatusGatewayTimeout, "La synthèse vocale a pris trop de temps.")
	case errors.Is(err, tts.ErrEngineUnavailable):
		api.WriteError(w, http.StatusServiceUnavailable, "La synthèse vocale est momentanément indisponible.")
	default:
		api.WriteError(w, http.StatusBadGateway, "La synthèse vocale a échoué.")
	}
}
