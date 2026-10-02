// Package calendartools implémente les tools de gestion de rendez-vous.
//
// Un tool est la frontière contrôlée entre l'agent et le service métier :
//   - il revalide les entrées (défense en profondeur, en plus du JSON Schema) ;
//   - il n'accepte que des identifiants qu'un tool a lui-même renvoyés dans la
//     conversation (le modèle ne peut pas inventer un slot_id/appointment_id) ;
//   - il impose l'identité du client issue de la session, pas du modèle ;
//   - il ne déclare un succès que si le calendrier l'a explicitement confirmé ;
//   - il traduit les erreurs en codes métier, sans détail technique.
//
// Ce package ne connaît aucun modèle d'IA et aucun agenda concret.
package calendartools

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"skills/datetime"
	"skills/types"
)

// Noms des tools exposés au modèle.
const (
	ToolRechercherDisponibilites = "rechercher_disponibilites"
	ToolReserverCreneau          = "reserver_creneau"
	ToolModifierRendezVous       = "modifier_rendez_vous"
	ToolAnnulerRendezVous        = "annuler_rendez_vous"
	ToolListerRendezVous         = "lister_rendez_vous"
	ToolListerProfessionnels     = "lister_professionnels"
)

// MaxSlotsReturned limite le nombre de créneaux renvoyés au modèle.
const MaxSlotsReturned = 10

// ---- Registre des identifiants émis (anti-hallucination) ----

const ledgerKey = "calendartools.ledger"

type ledger struct {
	slots        map[string]bool
	appointments map[string]bool
}

func getLedger(st types.StateStore) *ledger {
	if v, ok := st.Get(ledgerKey); ok {
		return v.(*ledger)
	}
	l := &ledger{slots: map[string]bool{}, appointments: map[string]bool{}}
	st.Set(ledgerKey, l)
	return l
}

func unknownID(field, source string) *types.Error {
	return types.Errorf(types.ErrInvalidRequest,
		"%s inconnu : n'utilise que des identifiants renvoyés par %s dans cette conversation, ne les invente jamais.",
		field, source)
}

// ---- Vues renvoyées au modèle ----

// SlotView est un créneau tel que présenté à l'agent.
type SlotView struct {
	types.TimeSlot
	Libelle string `json:"libelle"`
}

// AppointmentView est un rendez-vous tel que présenté à l'agent.
type AppointmentView struct {
	ID               string    `json:"id"`
	Start            time.Time `json:"start"`
	End              time.Time `json:"end"`
	ProfessionnelID  string    `json:"professionnel_id"`
	ProfessionnelNom string    `json:"professionnel_nom,omitempty"`
	TypeRendezVous   string    `json:"type_rendez_vous"`
	Status           string    `json:"status"`
	Libelle          string    `json:"libelle"`
}

func slotView(s types.TimeSlot, loc *time.Location) SlotView {
	s.Start, s.End = s.Start.In(loc), s.End.In(loc)
	return SlotView{TimeSlot: s, Libelle: datetime.FormatFR(s.Start, loc)}
}

func appointmentView(a types.Appointment, loc *time.Location) AppointmentView {
	return AppointmentView{
		ID: a.ID, Start: a.Start.In(loc), End: a.End.In(loc),
		ProfessionnelID: a.ProfessionnelID, ProfessionnelNom: a.ProfessionnelNom,
		TypeRendezVous: a.TypeRendezVous, Status: string(a.Status),
		Libelle: datetime.FormatFR(a.Start, loc),
	}
}

// ---- Utilitaires ----

func decode(args json.RawMessage, dst any) *types.Error {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	if err := json.Unmarshal(args, dst); err != nil {
		return types.Errorf(types.ErrInvalidRequest, "Arguments JSON invalides.")
	}
	return nil
}

func checkContext(tc types.ToolContext) *types.Error {
	if tc.Location == nil {
		return types.Errorf(types.ErrInternal, "Fuseau horaire de la session non défini.")
	}
	if tc.State == nil {
		return types.Errorf(types.ErrInternal, "État de session indisponible.")
	}
	return nil
}

// providerError convertit une erreur du fournisseur en erreur exploitable par
// l'agent. Les erreurs techniques sont journalisées mais jamais transmises.
func providerError(ctx context.Context, op string, err error) *types.Error {
	var te *types.Error
	if errors.As(err, &te) {
		out := *te
		if out.Message == "" {
			out.Message = types.DefaultHint(out.Code)
		}
		return &out
	}
	slog.WarnContext(ctx, "erreur technique du calendrier", "operation", op, "error", err)
	return types.NewError(types.ErrCalendarUnavailable)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
