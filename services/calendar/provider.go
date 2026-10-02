// Package calendar définit l'abstraction du service de calendrier (logique
// métier et source de vérité des disponibilités et réservations), ainsi
// qu'une implémentation en mémoire pour les tests.
//
// Aucun type de ce package ne dépend d'un fournisseur d'IA ni d'un agenda
// particulier (Google, Outlook…) : ceux-ci s'implémentent derrière Provider.
package calendar

import (
	"context"
	"time"

	"skills/types"
)

// Provider est le contrat qu'un fournisseur de calendrier doit respecter.
//
// Conventions d'erreur :
//   - une erreur métier est renvoyée sous forme de *types.Error (code stable) ;
//   - toute autre erreur est considérée comme technique et sera présentée à
//     l'agent comme CALENDAR_UNAVAILABLE, sans détail.
//
// Une opération d'écriture n'est réussie que si l'erreur est nil ET que le
// résultat porte explicitement Confirmed/Updated/Cancelled = true.
type Provider interface {
	SearchAvailability(ctx context.Context, req AvailabilityRequest) (AvailabilityResult, error)
	BookAppointment(ctx context.Context, req BookingRequest) (BookingResult, error)
	UpdateAppointment(ctx context.Context, req UpdateAppointmentRequest) (UpdateAppointmentResult, error)
	CancelAppointment(ctx context.Context, req CancelAppointmentRequest) (CancelAppointmentResult, error)
	ListAppointments(ctx context.Context, req ListAppointmentsRequest) (ListAppointmentsResult, error)
	ListProfessionals(ctx context.Context) (ListProfessionalsResult, error)
}

// AvailabilityRequest : recherche de créneaux libres dans [Start, End].
type AvailabilityRequest struct {
	ProfessionnelID string // vide = tous les professionnels
	TypeRendezVous  string // vide = tous types
	Start           time.Time
	End             time.Time
	DurationMinutes int // 0 = durée par défaut du type de rendez-vous
}

type AvailabilityResult struct {
	Slots []types.TimeSlot
}

// BookingRequest : réservation d'un créneau précédemment retourné par
// SearchAvailability.
type BookingRequest struct {
	SlotID         string
	TypeRendezVous string
	ClientID       string
	ClientName     string
	ClientEmail    string
}

type BookingResult struct {
	Confirmed   bool
	Appointment types.Appointment
}

// UpdateAppointmentRequest : déplacement d'un rendez-vous vers un nouveau
// créneau. ClientID provient de la session authentifiée.
type UpdateAppointmentRequest struct {
	AppointmentID string
	NewSlotID     string
	ClientID      string
}

type UpdateAppointmentResult struct {
	Updated     bool
	Appointment types.Appointment
}

type CancelAppointmentRequest struct {
	AppointmentID string
	ClientID      string
}

type CancelAppointmentResult struct {
	Cancelled   bool
	Appointment types.Appointment
}

// ListAppointmentsRequest : rendez-vous actifs d'un client, se terminant
// après From.
type ListAppointmentsRequest struct {
	ClientID string
	From     time.Time
}

type ListAppointmentsResult struct {
	Appointments []types.Appointment
}

type ListProfessionalsResult struct {
	Professionals []types.Professional
}
