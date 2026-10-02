package calendar

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"skills/types"
)

// Operation identifie une opération du Provider (pour l'injection de pannes).
type Operation string

const (
	OpSearch Operation = "search"
	OpBook   Operation = "book"
	OpUpdate Operation = "update"
	OpCancel Operation = "cancel"
	OpList   Operation = "list"
)

// DefaultDurationMinutes est la durée utilisée quand ni la requête ni le type
// de rendez-vous ne la précisent.
const DefaultDurationMinutes = 30

// MaxSearchRange limite l'amplitude d'une recherche.
const MaxSearchRange = 31 * 24 * time.Hour

type interval struct{ start, end time.Time }

type issuedSlot struct {
	slot     types.TimeSlot
	duration time.Duration
}

// MockProvider est un calendrier en mémoire, déterministe, avec injection de
// pannes. Les disponibilités sont définies par des plages d'ouverture
// (AddOpening), découpées en créneaux de la durée demandée, moins les
// rendez-vous existants.
type MockProvider struct {
	mu           sync.Mutex
	loc          *time.Location
	now          func() time.Time
	pros         map[string]types.Professional
	apptTypes    map[string]int
	openings     map[string][]interval
	appointments map[string]*types.Appointment
	issued       map[string]issuedSlot
	seq          int

	unavailable bool
	failNext    map[Operation]types.ErrorCode
	unconfirmed map[Operation]bool
	calls       map[Operation]int
}

var _ Provider = (*MockProvider)(nil)

// NewMockProvider crée un calendrier vide. loc est le fuseau dans lequel les
// créneaux sont exprimés ; il est obligatoire.
func NewMockProvider(loc *time.Location, now func() time.Time) (*MockProvider, error) {
	if loc == nil {
		return nil, errors.New("calendar: fuseau horaire obligatoire")
	}
	if now == nil {
		now = time.Now
	}
	return &MockProvider{
		loc:          loc,
		now:          now,
		pros:         map[string]types.Professional{},
		apptTypes:    map[string]int{},
		openings:     map[string][]interval{},
		appointments: map[string]*types.Appointment{},
		issued:       map[string]issuedSlot{},
		failNext:     map[Operation]types.ErrorCode{},
		unconfirmed:  map[Operation]bool{},
		calls:        map[Operation]int{},
	}, nil
}

// AddAppointmentType déclare un type de rendez-vous et sa durée par défaut.
func (m *MockProvider) AddAppointmentType(name string, minutes int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.apptTypes[name] = minutes
}

// AddProfessional déclare un professionnel et les types qu'il propose.
func (m *MockProvider) AddProfessional(id, nom string, apptTypes ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pros[id] = types.Professional{ID: id, Nom: nom, Types: apptTypes}
}

// AddOpening ajoute une plage d'ouverture pour un professionnel.
func (m *MockProvider) AddOpening(proID string, start, end time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.openings[proID] = append(m.openings[proID], interval{start.In(m.loc), end.In(m.loc)})
}

// SetUnavailable simule une panne technique (réseau, API indisponible…).
func (m *MockProvider) SetUnavailable(v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unavailable = v
}

// FailNext fait échouer le prochain appel de op avec le code donné.
func (m *MockProvider) FailNext(op Operation, code types.ErrorCode) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failNext[op] = code
}

// UnconfirmedNext fait renvoyer au prochain appel de op une réponse sans
// erreur mais non confirmée (Confirmed/Updated/Cancelled = false), pour
// vérifier que les tools ne la considèrent pas comme un succès.
func (m *MockProvider) UnconfirmedNext(op Operation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unconfirmed[op] = true
}

// OccupySlot simule la réservation du créneau par un tiers (concurrence).
func (m *MockProvider) OccupySlot(slotID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	is, ok := m.issued[slotID]
	if !ok {
		return fmt.Errorf("créneau %s inconnu", slotID)
	}
	m.seq++
	id := fmt.Sprintf("rdv_ext_%03d", m.seq)
	m.appointments[id] = &types.Appointment{
		ID: id, Start: is.slot.Start, End: is.slot.End, ProfessionnelID: is.slot.ProfessionnelID,
		ClientName: "tiers", Status: types.StatusConfirmed,
	}
	return nil
}

// DeleteAppointment simule la suppression d'un rendez-vous hors de l'agent
// (back-office, synchronisation…).
func (m *MockProvider) DeleteAppointment(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.appointments, id)
}

// Appointment renvoie l'état réel d'un rendez-vous (inspection en test).
func (m *MockProvider) Appointment(id string) (types.Appointment, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.appointments[id]
	if !ok {
		return types.Appointment{}, false
	}
	return *a, true
}

// Calls renvoie le nombre d'appels reçus pour une opération.
func (m *MockProvider) Calls(op Operation) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[op]
}

// begin est appelé (verrou tenu) au début de chaque opération.
func (m *MockProvider) begin(ctx context.Context, op Operation) error {
	m.calls[op]++
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.unavailable {
		return errors.New("mock: dial tcp 10.0.0.1:443: connect: connection refused")
	}
	if code, ok := m.failNext[op]; ok {
		delete(m.failNext, op)
		return types.NewError(code)
	}
	return nil
}

func (m *MockProvider) takeUnconfirmed(op Operation) bool {
	if m.unconfirmed[op] {
		delete(m.unconfirmed, op)
		return true
	}
	return false
}

func (m *MockProvider) SearchAvailability(ctx context.Context, req AvailabilityRequest) (AvailabilityResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.begin(ctx, OpSearch); err != nil {
		return AvailabilityResult{}, err
	}
	if !req.End.After(req.Start) {
		return AvailabilityResult{}, types.Errorf(types.ErrInvalidRequest, "date_fin doit être postérieure à date_debut.")
	}
	if req.End.Sub(req.Start) > MaxSearchRange {
		return AvailabilityResult{}, types.Errorf(types.ErrInvalidRequest, "La période de recherche ne peut pas dépasser 31 jours.")
	}
	if req.TypeRendezVous != "" {
		if _, ok := m.apptTypes[req.TypeRendezVous]; !ok {
			return AvailabilityResult{}, types.Errorf(types.ErrInvalidRequest,
				"Type de rendez-vous inconnu. Types existants : %s.", strings.Join(m.typeNames(), ", "))
		}
	}
	if req.ProfessionnelID != "" {
		if _, ok := m.pros[req.ProfessionnelID]; !ok {
			return AvailabilityResult{}, types.Errorf(types.ErrInvalidRequest,
				"Professionnel inconnu. Utilise lister_professionnels pour obtenir un identifiant valide.")
		}
	}

	minutes := req.DurationMinutes
	if minutes == 0 {
		minutes = m.apptTypes[req.TypeRendezVous]
	}
	if minutes == 0 {
		minutes = DefaultDurationMinutes
	}
	duration := time.Duration(minutes) * time.Minute
	now := m.now()

	var slots []types.TimeSlot
	for _, proID := range m.proIDs() {
		pro := m.pros[proID]
		if req.ProfessionnelID != "" && proID != req.ProfessionnelID {
			continue
		}
		if req.TypeRendezVous != "" && !slices.Contains(pro.Types, req.TypeRendezVous) {
			continue
		}
		for _, op := range m.openings[proID] {
			for start := op.start; !start.Add(duration).After(op.end); start = start.Add(duration) {
				end := start.Add(duration)
				if start.Before(req.Start) || end.After(req.End) || !start.After(now) {
					continue
				}
				if m.overlaps(proID, start, end, "") {
					continue
				}
				slot := types.TimeSlot{
					ID:               slotID(proID, start, minutes, req.TypeRendezVous),
					Start:            start.In(m.loc),
					End:              end.In(m.loc),
					ProfessionnelID:  proID,
					ProfessionnelNom: pro.Nom,
					TypeRendezVous:   req.TypeRendezVous,
				}
				m.issued[slot.ID] = issuedSlot{slot: slot, duration: duration}
				slots = append(slots, slot)
			}
		}
	}
	sort.SliceStable(slots, func(i, j int) bool { return slots[i].Start.Before(slots[j].Start) })
	return AvailabilityResult{Slots: slots}, nil
}

func (m *MockProvider) BookAppointment(ctx context.Context, req BookingRequest) (BookingResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.begin(ctx, OpBook); err != nil {
		return BookingResult{}, err
	}
	if strings.TrimSpace(req.ClientName) == "" {
		return BookingResult{}, &types.Error{Code: types.ErrMissingInformation,
			Message: types.DefaultHint(types.ErrMissingInformation), Missing: []string{"client_name"}}
	}
	is, err := m.freeIssuedSlot(req.SlotID, "")
	if err != nil {
		return BookingResult{}, err
	}
	if err := m.checkType(is, req.TypeRendezVous); err != nil {
		return BookingResult{}, err
	}
	if m.takeUnconfirmed(OpBook) {
		return BookingResult{Confirmed: false}, nil
	}
	m.seq++
	appt := &types.Appointment{
		ID:               fmt.Sprintf("rdv_%03d", m.seq),
		Start:            is.slot.Start,
		End:              is.slot.End,
		ProfessionnelID:  is.slot.ProfessionnelID,
		ProfessionnelNom: is.slot.ProfessionnelNom,
		TypeRendezVous:   req.TypeRendezVous,
		ClientID:         req.ClientID,
		ClientName:       req.ClientName,
		ClientEmail:      req.ClientEmail,
		Status:           types.StatusConfirmed,
	}
	m.appointments[appt.ID] = appt
	return BookingResult{Confirmed: true, Appointment: *appt}, nil
}

func (m *MockProvider) UpdateAppointment(ctx context.Context, req UpdateAppointmentRequest) (UpdateAppointmentResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.begin(ctx, OpUpdate); err != nil {
		return UpdateAppointmentResult{}, err
	}
	appt, err := m.ownedAppointment(req.AppointmentID, req.ClientID)
	if err != nil {
		return UpdateAppointmentResult{}, err
	}
	if appt.Status != types.StatusConfirmed {
		return UpdateAppointmentResult{}, types.Errorf(types.ErrUpdateFailed, "Ce rendez-vous est annulé ; il ne peut pas être déplacé. Propose une nouvelle réservation.")
	}
	is, err := m.freeIssuedSlot(req.NewSlotID, appt.ID)
	if err != nil {
		return UpdateAppointmentResult{}, err
	}
	if is.duration != appt.End.Sub(appt.Start) {
		return UpdateAppointmentResult{}, types.Errorf(types.ErrInvalidRequest,
			"La durée du nouveau créneau ne correspond pas à celle du rendez-vous (%d min). Relance une recherche avec cette durée.",
			int(appt.End.Sub(appt.Start).Minutes()))
	}
	if err := m.checkType(is, appt.TypeRendezVous); err != nil {
		return UpdateAppointmentResult{}, err
	}
	if m.takeUnconfirmed(OpUpdate) {
		return UpdateAppointmentResult{Updated: false, Appointment: *appt}, nil
	}
	appt.Start, appt.End = is.slot.Start, is.slot.End
	appt.ProfessionnelID, appt.ProfessionnelNom = is.slot.ProfessionnelID, is.slot.ProfessionnelNom
	return UpdateAppointmentResult{Updated: true, Appointment: *appt}, nil
}

func (m *MockProvider) CancelAppointment(ctx context.Context, req CancelAppointmentRequest) (CancelAppointmentResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.begin(ctx, OpCancel); err != nil {
		return CancelAppointmentResult{}, err
	}
	appt, err := m.ownedAppointment(req.AppointmentID, req.ClientID)
	if err != nil {
		return CancelAppointmentResult{}, err
	}
	if appt.Status == types.StatusCancelled {
		return CancelAppointmentResult{}, types.Errorf(types.ErrCancelFailed, "Ce rendez-vous est déjà annulé.")
	}
	if m.takeUnconfirmed(OpCancel) {
		return CancelAppointmentResult{Cancelled: false, Appointment: *appt}, nil
	}
	appt.Status = types.StatusCancelled
	return CancelAppointmentResult{Cancelled: true, Appointment: *appt}, nil
}

func (m *MockProvider) ListAppointments(ctx context.Context, req ListAppointmentsRequest) (ListAppointmentsResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.begin(ctx, OpList); err != nil {
		return ListAppointmentsResult{}, err
	}
	if req.ClientID == "" {
		return ListAppointmentsResult{}, &types.Error{Code: types.ErrMissingInformation,
			Message: "Client non identifié.", Missing: []string{"client_id"}}
	}
	var out []types.Appointment
	for _, a := range m.appointments {
		if a.ClientID == req.ClientID && a.Status == types.StatusConfirmed && a.End.After(req.From) {
			out = append(out, *a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return ListAppointmentsResult{Appointments: out}, nil
}

func (m *MockProvider) ListProfessionals(ctx context.Context) (ListProfessionalsResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.begin(ctx, OpList); err != nil {
		return ListProfessionalsResult{}, err
	}
	out := make([]types.Professional, 0, len(m.pros))
	for _, id := range m.proIDs() {
		out = append(out, m.pros[id])
	}
	return ListProfessionalsResult{Professionals: out}, nil
}

// freeIssuedSlot vérifie qu'un créneau a bien été émis par une recherche et
// qu'il est toujours libre (en ignorant éventuellement le rendez-vous ignoreID).
func (m *MockProvider) freeIssuedSlot(id, ignoreID string) (issuedSlot, error) {
	is, ok := m.issued[id]
	if !ok || !is.slot.Start.After(m.now()) || m.overlaps(is.slot.ProfessionnelID, is.slot.Start, is.slot.End, ignoreID) {
		return issuedSlot{}, types.NewError(types.ErrSlotNoLongerAvailable)
	}
	return is, nil
}

func (m *MockProvider) checkType(is issuedSlot, apptType string) error {
	if apptType == "" {
		return &types.Error{Code: types.ErrMissingInformation,
			Message: types.DefaultHint(types.ErrMissingInformation), Missing: []string{"type_rendez_vous"}}
	}
	if _, ok := m.apptTypes[apptType]; !ok {
		return types.Errorf(types.ErrInvalidRequest, "Type de rendez-vous inconnu. Types existants : %s.", strings.Join(m.typeNames(), ", "))
	}
	if is.slot.TypeRendezVous != "" && is.slot.TypeRendezVous != apptType {
		return types.Errorf(types.ErrInvalidRequest, "Ce créneau a été recherché pour le type %q.", is.slot.TypeRendezVous)
	}
	if want := time.Duration(m.apptTypes[apptType]) * time.Minute; is.slot.TypeRendezVous == "" && want != is.duration {
		return types.Errorf(types.ErrInvalidRequest,
			"Ce créneau ne dure pas %d min comme un rendez-vous %q. Relance la recherche en précisant type_rendez_vous.",
			m.apptTypes[apptType], apptType)
	}
	if !slices.Contains(m.pros[is.slot.ProfessionnelID].Types, apptType) {
		return types.Errorf(types.ErrInvalidRequest, "Ce professionnel ne propose pas ce type de rendez-vous.")
	}
	return nil
}

// ownedAppointment ne révèle pas l'existence d'un rendez-vous appartenant à
// un autre client : il est alors signalé comme introuvable.
func (m *MockProvider) ownedAppointment(id, clientID string) (*types.Appointment, error) {
	a, ok := m.appointments[id]
	if !ok || (a.ClientID != "" && a.ClientID != clientID) {
		return nil, types.NewError(types.ErrAppointmentNotFound)
	}
	return a, nil
}

func (m *MockProvider) overlaps(proID string, start, end time.Time, ignoreID string) bool {
	for id, a := range m.appointments {
		if id == ignoreID || a.ProfessionnelID != proID || a.Status != types.StatusConfirmed {
			continue
		}
		if start.Before(a.End) && a.Start.Before(end) {
			return true
		}
	}
	return false
}

func (m *MockProvider) proIDs() []string {
	ids := make([]string, 0, len(m.pros))
	for id := range m.pros {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (m *MockProvider) typeNames() []string {
	names := make([]string, 0, len(m.apptTypes))
	for n := range m.apptTypes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func slotID(proID string, start time.Time, minutes int, apptType string) string {
	id := fmt.Sprintf("slot_%s_%s_%d", proID, start.UTC().Format("20060102T1504Z"), minutes)
	if apptType != "" {
		id += "_" + apptType
	}
	return id
}
