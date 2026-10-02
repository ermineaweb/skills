// Package datetimetools expose la résolution d'expressions temporelles comme
// un tool, utilisable par n'importe quel skill.
package datetimetools

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"skills/datetime"
	"skills/types"
)

// ToolInterpreterDate est le nom du tool exposé au modèle.
const ToolInterpreterDate = "interpreter_date"

type interpreterArgs struct {
	Expression string `json:"expression"`
}

// InterpreterOutput est un intervalle ISO 8601 explicite.
type InterpreterOutput struct {
	DateDebut   string `json:"date_debut"`
	DateFin     string `json:"date_fin"`
	Fuseau      string `json:"fuseau"`
	Granularite string `json:"granularite"`
	Libelle     string `json:"libelle"`
	Note        string `json:"note,omitempty"`
}

// NewInterpreterDate renvoie le handler de interpreter_date. Il utilise
// l'instant et le fuseau de la session, jamais une valeur supposée.
func NewInterpreterDate() types.ToolHandler {
	return types.ToolHandlerFunc(func(ctx context.Context, tc types.ToolContext, raw json.RawMessage) (any, *types.Error) {
		var a interpreterArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, types.Errorf(types.ErrInvalidRequest, "Arguments JSON invalides.")
		}
		p, err := datetime.Resolve(a.Expression, tc.Now, tc.Location)
		switch {
		case errors.Is(err, datetime.ErrMissingDay):
			return nil, &types.Error{Code: types.ErrMissingInformation, Message: datetime.Describe(err), Missing: []string{"jour"}}
		case errors.Is(err, datetime.ErrNoLocation):
			return nil, types.Errorf(types.ErrInternal, "%s", datetime.Describe(err))
		case err != nil:
			return nil, types.Errorf(types.ErrInvalidRequest, "%s", datetime.Describe(err))
		}
		return InterpreterOutput{
			DateDebut:   p.Start.Format(time.RFC3339),
			DateFin:     p.End.Format(time.RFC3339),
			Fuseau:      tc.Location.String(),
			Granularite: string(p.Granularity),
			Libelle:     "du " + datetime.FormatFR(p.Start, tc.Location) + " au " + datetime.FormatFR(p.End, tc.Location),
			Note:        p.Note,
		}, nil
	})
}
