// Package agenda définit l'abstraction de l'agenda personnel d'un
// utilisateur (événements libres : réunions, déjeuners, rappels…), ainsi
// qu'une implémentation en mémoire (MockProvider).
//
// C'est un contrat distinct de calendar.Provider, qui gère des créneaux de
// professionnels à réserver. Un vrai fournisseur (Google Calendar, CalDAV,
// Microsoft Graph…) s'implémente derrière Provider : récurrences (RRULE),
// occurrences et rappels y sont natifs.
//
// Aucun type de ce package ne dépend d'un fournisseur d'IA.
package agenda

import (
	"context"
	"time"
)

// Provider est le contrat d'un agenda personnel.
//
// Conventions (identiques à calendar.Provider) :
//   - OwnerID vient toujours de la session authentifiée, jamais du modèle ;
//     un événement d'un autre propriétaire est introuvable
//     (types.ErrEventNotFound), sans révéler son existence ;
//   - une erreur métier est un *types.Error (code stable) ; toute autre
//     erreur est technique et présentée à l'agent comme
//     CALENDAR_UNAVAILABLE, sans détail ;
//   - une écriture n'est réussie que si l'erreur est nil ET que le résultat
//     porte Created/Updated/Deleted = true.
type Provider interface {
	// ListEvents renvoie les occurrences qui chevauchent [From, To[, triées
	// par début. Les événements récurrents sont développés.
	ListEvents(ctx context.Context, req ListRequest) (ListResult, error)
	CreateEvent(ctx context.Context, req CreateRequest) (CreateResult, error)
	UpdateEvent(ctx context.Context, req UpdateRequest) (UpdateResult, error)
	DeleteEvent(ctx context.Context, req DeleteRequest) (DeleteResult, error)
}

// Event est un événement, ou une série d'événements s'il a une Recurrence.
type Event struct {
	ID      string `json:"id"`
	OwnerID string `json:"owner_id"`
	Title   string `json:"title"`
	// Start et End sont exprimés dans TimeZone ; pour une série, ce sont
	// ceux de la première occurrence. End est exclu.
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	TimeZone string    `json:"timezone"`
	// AllDay : Start et End sont des minuits (End = lendemain du dernier
	// jour).
	AllDay      bool     `json:"all_day,omitempty"`
	Description string   `json:"description,omitempty"`
	Location    string   `json:"location,omitempty"`
	Attendees   []string `json:"attendees,omitempty"`
	// Reminders : minutes avant le début.
	Reminders  []int       `json:"reminders,omitempty"`
	Recurrence *Recurrence `json:"recurrence,omitempty"`
	// Exceptions : débuts des occurrences retirées d'une série (EXDATE).
	Exceptions []time.Time `json:"exceptions,omitempty"`
}

// Occurrence est une occurrence d'un événement : l'événement lui-même s'il
// n'est pas récurrent.
type Occurrence struct {
	Event Event
	// Start et End de cette occurrence.
	Start, End time.Time
}

// Recurring indique si l'occurrence fait partie d'une série.
func (o Occurrence) Recurring() bool { return o.Event.Recurrence != nil }

// ListRequest : occurrences de OwnerID chevauchant [From, To[, filtrées par
// Query (voir Matches) si non vide.
type ListRequest struct {
	OwnerID  string
	From, To time.Time
	Query    string
}

type ListResult struct {
	Occurrences []Occurrence
}

// CreateRequest : Event.ID et Event.OwnerID sont ignorés (fixés par le
// fournisseur et la session).
type CreateRequest struct {
	OwnerID string
	Event   Event
}

type CreateResult struct {
	Created bool
	Event   Event
}

// Changes : champs à modifier ; nil = inchangé.
type Changes struct {
	Title       *string
	Start, End  *time.Time
	AllDay      *bool
	Description *string
	Location    *string
	Attendees   *[]string
	Reminders   *[]int
}

// UpdateRequest modifie l'événement EventID. Si Occurrence est non nil
// (début d'une occurrence d'une série), seule cette occurrence est
// modifiée : elle est détachée de la série et devient un événement
// distinct, renvoyé dans le résultat.
type UpdateRequest struct {
	OwnerID    string
	EventID    string
	Occurrence *time.Time
	Changes    Changes
}

type UpdateResult struct {
	Updated bool
	Event   Event
}

// DeleteRequest supprime l'événement EventID (toute la série s'il est
// récurrent), ou la seule occurrence qui commence à Occurrence.
type DeleteRequest struct {
	OwnerID    string
	EventID    string
	Occurrence *time.Time
}

type DeleteResult struct {
	Deleted bool
	Event   Event
}
