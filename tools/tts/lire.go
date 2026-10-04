// Package ttstools implémente lire_a_voix_haute, le tool du skill
// synthese-vocale.
//
// Le tool ne génère pas l'audio : il valide la demande (tts.Service.Prepare)
// et la conserve dans la session sous un identifiant. L'interface lit
// ensuite l'audio par la route d'API (api/ttsapi), qui le génère en flux :
// l'audio ne passe jamais par le modèle et n'est jamais conservé en entier
// en mémoire.
package ttstools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"unicode/utf8"

	"skills/services/tts"
	"skills/types"
)

// ToolLireAVoixHaute est le nom du tool exposé au modèle.
const ToolLireAVoixHaute = "lire_a_voix_haute"

// maxAudios : demandes conservées par session (les plus anciennes sont
// oubliées).
const maxAudios = 20

const stateKey = "ttstools.audios"

// pending : demandes de la session. Le tool écrit pendant un tour de
// conversation, l'API lit en parallèle : accès protégé par mu.
type pending struct {
	mu    sync.Mutex
	order []string
	byID  map[string]tts.Request
}

func pendingOf(st types.StateStore) *pending {
	if v, ok := st.Get(stateKey); ok {
		return v.(*pending)
	}
	p := &pending{byID: map[string]tts.Request{}}
	st.Set(stateKey, p)
	return p
}

// Lookup renvoie la demande de synthèse audioID de la session.
func Lookup(st types.StateStore, audioID string) (tts.Request, bool) {
	v, ok := st.Get(stateKey)
	if !ok {
		return tts.Request{}, false
	}
	p := v.(*pending)
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.byID[audioID]
	return r, ok
}

type args struct {
	Texte   string   `json:"texte"`
	Langue  string   `json:"langue"`
	Voix    string   `json:"voix"`
	Vitesse *float64 `json:"vitesse"`
	Format  string   `json:"format"`
}

// Output est le résultat de lire_a_voix_haute.
type Output struct {
	AudioID    string     `json:"audio_id"`
	Langue     string     `json:"langue"`
	Voix       string     `json:"voix"`
	Vitesse    float64    `json:"vitesse"`
	Format     tts.Format `json:"format"`
	Caracteres int        `json:"caracteres"`
	Note       string     `json:"note"`
}

// NewLireAVoixHaute renvoie le handler de lire_a_voix_haute.
func NewLireAVoixHaute(svc *tts.Service) types.ToolHandler {
	return types.ToolHandlerFunc(func(_ context.Context, tc types.ToolContext, raw json.RawMessage) (any, *types.Error) {
		var a args
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, types.Errorf(types.ErrInvalidRequest, "Arguments invalides.")
		}
		req, err := svc.Prepare(tts.Params{Text: a.Texte, Language: a.Langue, Voice: a.Voix, Speed: a.Vitesse, Format: a.Format})
		if err != nil {
			// Erreur de validation : elle indique les valeurs acceptées.
			return nil, types.Errorf(types.ErrInvalidRequest, "%s. Corrige la demande sans inventer de valeur.",
				strings.TrimPrefix(err.Error(), "tts: "))
		}
		id, err := randomID()
		if err != nil {
			return nil, types.NewError(types.ErrInternal)
		}
		p := pendingOf(tc.State)
		p.mu.Lock()
		p.byID[id] = req
		p.order = append(p.order, id)
		if len(p.order) > maxAudios {
			delete(p.byID, p.order[0])
			p.order = p.order[1:]
		}
		p.mu.Unlock()

		return Output{
			AudioID: id, Langue: req.Language, Voix: req.Voice, Vitesse: req.Speed, Format: req.Format,
			Caracteres: utf8.RuneCountInString(req.Text),
			Note:       "L'interface lit l'audio à l'utilisateur. N'affirme pas qu'il a été entendu et ne recopie pas le texte.",
		}, nil
	})
}

func randomID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
