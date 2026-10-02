package types

import "time"

// TimeSlot est un créneau libre retourné par le fournisseur de calendrier.
// Seul le fournisseur peut créer un TimeSlot : l'agent ne fait que le relayer.
type TimeSlot struct {
	ID               string    `json:"id"`
	Start            time.Time `json:"start"`
	End              time.Time `json:"end"`
	ProfessionnelID  string    `json:"professionnel_id"`
	ProfessionnelNom string    `json:"professionnel_nom,omitempty"`
	TypeRendezVous   string    `json:"type_rendez_vous,omitempty"`
}

// Professional décrit un professionnel pouvant recevoir des rendez-vous.
type Professional struct {
	ID    string   `json:"id"`
	Nom   string   `json:"nom"`
	Types []string `json:"types_rendez_vous"`
}
