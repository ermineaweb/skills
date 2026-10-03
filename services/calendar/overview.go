package calendar

import (
	"context"
	"sort"
	"time"

	"skills/types"
)

// Viewer est implémenté par les calendriers capables de présenter tout
// l'agenda : plages d'ouverture et rendez-vous de tous les clients. Il sert
// uniquement à l'interface de démonstration ; il ne fait pas partie de
// Provider et les skills n'y ont pas accès.
type Viewer interface {
	Overview(ctx context.Context, from, to time.Time) (Overview, error)
}

// Overview est l'état de l'agenda sur une période.
type Overview struct {
	Professionals []types.Professional
	Openings      []Opening
	// Appointments : rendez-vous confirmés, tous clients confondus.
	Appointments []types.Appointment
}

// Opening est une plage d'ouverture d'un professionnel.
type Opening struct {
	ProfessionnelID string
	Start, End      time.Time
}

var _ Viewer = (*MockProvider)(nil)

// Overview renvoie les plages d'ouverture et les rendez-vous confirmés qui
// chevauchent [from, to[. Lecture seule : n'est pas compté dans Calls et
// ignore l'injection de pannes, qui simule l'agenda vu par l'agent.
func (m *MockProvider) Overview(ctx context.Context, from, to time.Time) (Overview, error) {
	if err := ctx.Err(); err != nil {
		return Overview{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out Overview
	for _, id := range m.proIDs() {
		out.Professionals = append(out.Professionals, m.pros[id])
		for _, op := range m.openings[id] {
			if op.start.Before(to) && op.end.After(from) {
				out.Openings = append(out.Openings, Opening{ProfessionnelID: id, Start: op.start, End: op.end})
			}
		}
	}
	for _, a := range m.appointments {
		if a.Status == types.StatusConfirmed && a.Start.Before(to) && a.End.After(from) {
			out.Appointments = append(out.Appointments, *a)
		}
	}
	sort.Slice(out.Openings, func(i, j int) bool { return out.Openings[i].Start.Before(out.Openings[j].Start) })
	sort.Slice(out.Appointments, func(i, j int) bool { return out.Appointments[i].Start.Before(out.Appointments[j].Start) })
	return out, nil
}
