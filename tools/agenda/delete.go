package agendatools

import (
	"context"
	"encoding/json"

	"skills/services/agenda"
	"skills/types"
)

type deleteArgs struct {
	Target
	Portee string `json:"portee"`
}

// NewDeleteEvent renvoie le handler de delete_event. L'événement est
// désigné sans ambiguïté (identifiant, ou cible à un seul candidat) ; dans
// une série, la portée (cette occurrence ou toute la série) est
// obligatoire.
func NewDeleteEvent(p agenda.Provider) types.ToolHandler {
	return types.ToolHandlerFunc(func(ctx context.Context, tc types.ToolContext, raw json.RawMessage) (any, *types.Error) {
		if err := checkContext(tc); err != nil {
			return nil, err
		}
		var a deleteArgs
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		occ, err := resolveTarget(ctx, p, tc, a.Target)
		if err != nil {
			return nil, err
		}
		portee, err := scope(occ, a.Portee)
		if err != nil {
			return nil, err
		}
		req := agenda.DeleteRequest{OwnerID: tc.User.ClientID, EventID: occ.Event.ID}
		if occ.Recurring() && portee == ScopeOccurrence {
			start := occ.Start
			req.Occurrence = &start
		}
		res, perr := p.DeleteEvent(ctx, req)
		if perr != nil {
			return nil, providerError(ctx, "delete", perr)
		}
		if !res.Deleted {
			return nil, types.Errorf(types.ErrEventNotSaved, "La suppression n'a pas été enregistrée ; l'événement est toujours dans l'agenda.")
		}
		delete(getLedger(tc.State), viewID(occ))
		out := EventOutput{Evenement: view(occ, tc.Location)}
		if occ.Recurring() {
			out.Portee = portee
		}
		return out, nil
	})
}
