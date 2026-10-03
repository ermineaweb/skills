// Package prospectsapi ajoute à l'API la lecture du dernier résultat du
// skill prospect-research :
//
//	GET /api/sessions/{id}/prospects   → {"resultat": {…} | null}
//
// Le résultat est celui accepté par enregistrer_prospects (structure
// validée, URL vérifiées), conservé dans la session : l'interface affiche
// les prospects à partir de là, jamais à partir du texte du modèle.
package prospectsapi

import (
	"encoding/json"
	"net/http"

	"skills/agent"
	"skills/api"
	prospecttools "skills/tools/prospects"
)

// Options renvoie la route à passer à api.New.
func Options() []api.Option {
	return []api.Option{api.WithSessionRoute("GET", "prospects", lastResult)}
}

func lastResult(w http.ResponseWriter, _ *http.Request, sess *agent.Session) {
	res, ok := prospecttools.LastResult(sess.State())
	if !ok {
		res = json.RawMessage("null")
	}
	api.WriteJSON(w, http.StatusOK, map[string]json.RawMessage{"resultat": res})
}
