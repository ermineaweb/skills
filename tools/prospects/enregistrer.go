// Package prospecttools implémente enregistrer_prospects, le tool par
// lequel le skill prospect-research remet son résultat à l'application.
//
// Le runtime valide la structure du résultat (JSON Schema de sortie du
// skill) avant l'appel. Le tool vérifie ensuite ce qu'un schéma ne peut pas
// exprimer : chaque URL a été renvoyée par web_search ou lue par web_fetch
// dans la session, chaque source_id désigne une source du prospect, et
// chaque prospect n'apparaît qu'une fois. Le résultat accepté est conservé
// dans la session (voir LastResult) : l'interface l'affiche à partir de là,
// jamais à partir du texte du modèle.
package prospecttools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	webtools "skills/tools/web"
	"skills/types"
)

// ToolEnregistrerProspects est le nom du tool exposé au modèle.
const ToolEnregistrerProspects = "enregistrer_prospects"

const resultKey = "prospecttools.result"

// LastResult renvoie le dernier résultat accepté dans la session.
func LastResult(st types.StateStore) (json.RawMessage, bool) {
	v, ok := st.Get(resultKey)
	if !ok {
		return nil, false
	}
	return v.(json.RawMessage), true
}

// result ne décode que les champs vérifiés ici ; le résultat est conservé
// tel que reçu.
type result struct {
	Prospects []struct {
		ID      string `json:"id"`
		Company struct {
			Website *string `json:"website"`
		} `json:"company"`
		Sources []struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		} `json:"sources"`
	} `json:"prospects"`
	Excluded []json.RawMessage `json:"excluded"`
}

// Output est le résultat de enregistrer_prospects.
type Output struct {
	Enregistre bool `json:"enregistre"`
	Prospects  int  `json:"prospects"`
	Exclus     int  `json:"exclus"`
}

// NewEnregistrerProspects renvoie le handler de enregistrer_prospects.
func NewEnregistrerProspects() types.ToolHandler {
	return types.ToolHandlerFunc(func(ctx context.Context, tc types.ToolContext, raw json.RawMessage) (any, *types.Error) {
		var args struct {
			Resultat json.RawMessage `json:"resultat"`
		}
		var r result
		var generic struct {
			Prospects []map[string]any `json:"prospects"`
		}
		if err := json.Unmarshal(raw, &args); err != nil || json.Unmarshal(args.Resultat, &r) != nil || json.Unmarshal(args.Resultat, &generic) != nil {
			return nil, types.Errorf(types.ErrInvalidRequest, "resultat doit être l'objet JSON décrit dans les instructions.")
		}

		var problems []string
		ids := map[string]bool{}
		for i, p := range r.Prospects {
			if ids[p.ID] {
				problems = append(problems, fmt.Sprintf("prospect %q présent deux fois : fusionne les doublons", p.ID))
			}
			ids[p.ID] = true
			if w := p.Company.Website; w != nil && *w != "" && !webtools.Seen(tc.State, *w) {
				problems = append(problems, fmt.Sprintf("%s : website %q n'a été renvoyé par aucun outil", p.ID, *w))
			}
			sourceIDs := map[string]bool{}
			for _, s := range p.Sources {
				sourceIDs[s.ID] = true
				if !webtools.Seen(tc.State, s.URL) {
					problems = append(problems, fmt.Sprintf("%s : la source %s (%s) n'a été ni renvoyée par web_search ni lue par web_fetch", p.ID, s.ID, s.URL))
				}
			}
			for _, ref := range sourceRefs(generic.Prospects[i]) {
				if !sourceIDs[ref] {
					problems = append(problems, fmt.Sprintf("%s : source_id %q absent de ses sources", p.ID, ref))
				}
			}
		}
		if len(problems) > 0 {
			slices.Sort(problems)
			problems = slices.Compact(problems)
			return nil, types.Errorf(types.ErrInvalidRequest,
				"Résultat refusé, corrige-le sans inventer de donnée puis rappelle %s : %s.",
				ToolEnregistrerProspects, strings.Join(problems, " ; "))
		}

		tc.State.Set(resultKey, append(json.RawMessage(nil), args.Resultat...))
		return Output{Enregistre: true, Prospects: len(r.Prospects), Exclus: len(r.Excluded)}, nil
	})
}

// sourceRefs renvoie tous les source_ids cités dans un prospect, à
// n'importe quelle profondeur (faits, technologies, signaux…).
func sourceRefs(prospect map[string]any) []string {
	var refs []string
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				if k != "source_ids" {
					walk(child)
					continue
				}
				ids, _ := child.([]any)
				for _, id := range ids {
					if str, ok := id.(string); ok {
						refs = append(refs, str)
					}
				}
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		}
	}
	walk(prospect)
	return refs
}
