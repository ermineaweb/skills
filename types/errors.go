// Package types regroupe les types partagés entre le runtime, les skills,
// les tools et les services métier. Il ne dépend d'aucun autre package du projet.
package types

import "fmt"

// ErrorCode est un code d'erreur métier stable, exploitable par l'agent.
type ErrorCode string

const (
	ErrNoAvailability        ErrorCode = "NO_AVAILABILITY"
	ErrSlotNoLongerAvailable ErrorCode = "SLOT_NO_LONGER_AVAILABLE"
	ErrBookingFailed         ErrorCode = "BOOKING_FAILED"
	ErrAppointmentNotFound   ErrorCode = "APPOINTMENT_NOT_FOUND"
	ErrUpdateFailed          ErrorCode = "UPDATE_FAILED"
	ErrCancelFailed          ErrorCode = "CANCEL_FAILED"
	ErrInvalidRequest        ErrorCode = "INVALID_REQUEST"
	ErrMissingInformation    ErrorCode = "MISSING_INFORMATION"
	ErrCalendarUnavailable   ErrorCode = "CALENDAR_UNAVAILABLE"
	ErrSearchUnavailable     ErrorCode = "SEARCH_UNAVAILABLE"
	ErrPageUnavailable       ErrorCode = "PAGE_UNAVAILABLE"
	ErrEventNotFound         ErrorCode = "EVENT_NOT_FOUND"
	ErrEventAmbiguous        ErrorCode = "EVENT_AMBIGUOUS"
	ErrEventConflict         ErrorCode = "EVENT_CONFLICT"
	ErrEventNotSaved         ErrorCode = "EVENT_NOT_SAVED"
	ErrNotAuthenticated      ErrorCode = "NOT_AUTHENTICATED"
	ErrPermissionDenied      ErrorCode = "PERMISSION_DENIED"
	ErrInternal              ErrorCode = "INTERNAL_ERROR"
)

// Error est l'erreur renvoyée par un tool à l'agent.
//
// Message est une consigne destinée à l'agent (pas à l'utilisateur final) et
// ne contient jamais de détail technique du fournisseur sous-jacent.
type Error struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message,omitempty"`
	Missing   []string  `json:"missing,omitempty"`
	Retryable bool      `json:"retryable,omitempty"`
}

func (e *Error) Error() string {
	if e.Message == "" {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// NewError crée une erreur avec la consigne par défaut associée au code.
func NewError(code ErrorCode) *Error {
	return &Error{Code: code, Message: DefaultHint(code), Retryable: retryable(code)}
}

// Errorf crée une erreur avec une consigne spécifique.
func Errorf(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Retryable: retryable(code)}
}

// DefaultHint renvoie la consigne par défaut donnée à l'agent pour un code.
func DefaultHint(code ErrorCode) string {
	switch code {
	case ErrNoAvailability:
		return "Aucun créneau disponible sur cette période. Propose d'élargir la période, de changer de moment de la journée ou de professionnel."
	case ErrSlotNoLongerAvailable:
		return "Ce créneau n'est plus disponible. Informe l'utilisateur et propose de rechercher une autre disponibilité."
	case ErrBookingFailed:
		return "La réservation n'a pas été enregistrée. N'annonce aucune réservation ; propose de réessayer ou de choisir un autre créneau."
	case ErrAppointmentNotFound:
		return "Rendez-vous introuvable pour ce client. Propose de lister ses rendez-vous."
	case ErrUpdateFailed:
		return "La modification n'a pas été enregistrée ; le rendez-vous initial est inchangé."
	case ErrCancelFailed:
		return "L'annulation n'a pas été enregistrée ; le rendez-vous est toujours actif."
	case ErrInvalidRequest:
		return "Requête invalide. Corrige les paramètres sans inventer de valeur."
	case ErrMissingInformation:
		return "Information manquante. Demande uniquement les champs listés dans 'missing' à l'utilisateur."
	case ErrCalendarUnavailable:
		return "Le service d'agenda est momentanément indisponible. Informe l'utilisateur et propose de réessayer plus tard."
	case ErrSearchUnavailable:
		return "Le moteur de recherche n'a pas répondu. Réessaie au plus une fois, éventuellement avec une autre requête ; sinon signale-le dans les avertissements. N'invente aucun résultat."
	case ErrPageUnavailable:
		return "La page n'a pas pu être lue. Ne la cite pas comme source consultée et n'en déduis rien ; cherche l'information ailleurs."
	case ErrEventNotFound:
		return "Aucun événement ne correspond dans l'agenda. Dis-le à l'utilisateur et propose de consulter l'agenda sur la période."
	case ErrEventAmbiguous:
		return "Plusieurs événements correspondent. Ne choisis pas : présente-les à l'utilisateur et demande lequel il vise."
	case ErrEventConflict:
		return "Le moment choisi chevauche d'autres événements. Signale-les à l'utilisateur et demande s'il confirme."
	case ErrEventNotSaved:
		return "L'opération n'a pas été enregistrée dans l'agenda ; l'agenda est inchangé. Propose de réessayer."
	case ErrNotAuthenticated:
		return "L'utilisateur n'est pas identifié : l'agenda n'est pas accessible. Invite-le à se connecter."
	case ErrPermissionDenied:
		return "L'utilisateur n'a pas le droit d'effectuer cette opération sur cet agenda. Dis-le-lui sans réessayer."
	default:
		return "Erreur interne. Informe l'utilisateur que l'opération n'a pas pu être réalisée."
	}
}

func retryable(code ErrorCode) bool {
	return code == ErrCalendarUnavailable || code == ErrSearchUnavailable || code == ErrBookingFailed ||
		code == ErrUpdateFailed || code == ErrCancelFailed || code == ErrEventNotSaved
}
