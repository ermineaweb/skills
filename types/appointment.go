package types

import "time"

// AppointmentStatus est l'état d'un rendez-vous côté calendrier.
type AppointmentStatus string

const (
	StatusConfirmed AppointmentStatus = "confirmed"
	StatusCancelled AppointmentStatus = "cancelled"
)

// Appointment est un rendez-vous tel qu'enregistré par le calendrier.
type Appointment struct {
	ID               string            `json:"id"`
	Start            time.Time         `json:"start"`
	End              time.Time         `json:"end"`
	ProfessionnelID  string            `json:"professionnel_id"`
	ProfessionnelNom string            `json:"professionnel_nom,omitempty"`
	TypeRendezVous   string            `json:"type_rendez_vous"`
	ClientID         string            `json:"client_id,omitempty"`
	ClientName       string            `json:"client_name"`
	ClientEmail      string            `json:"client_email,omitempty"`
	Status           AppointmentStatus `json:"status"`
}

// UserContext décrit l'utilisateur de la conversation, tel que connu par
// l'application hôte (authentification). Ces valeurs font autorité sur tout
// ce que le modèle pourrait proposer.
type UserContext struct {
	ClientID string `json:"client_id,omitempty"`
	Name     string `json:"name,omitempty"`
	Email    string `json:"email,omitempty"`
}
