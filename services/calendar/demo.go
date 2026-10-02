package calendar

import "time"

// NewDemoProvider crée un calendrier mock pré-rempli sur les 14 jours
// suivant now (jours ouvrés) :
//
//   - types : "consultation" (30 min), "bilan" (60 min)
//   - "paul"  (Paul Martin)  : consultation, bilan —
//     09:00-09:30, 10:30-12:00, 14:00-14:30, 15:30-16:00, 17:00-17:30
//   - "marie" (Marie Dubois) : consultation —
//     09:30-10:00, 11:00-11:30, 15:00-15:30
func NewDemoProvider(loc *time.Location, now func() time.Time) (*MockProvider, error) {
	m, err := NewMockProvider(loc, now)
	if err != nil {
		return nil, err
	}
	m.AddAppointmentType("consultation", 30)
	m.AddAppointmentType("bilan", 60)
	m.AddProfessional("paul", "Paul Martin", "consultation", "bilan")
	m.AddProfessional("marie", "Marie Dubois", "consultation")

	type hm struct{ h, m int }
	schedule := map[string][][2]hm{
		"paul":  {{{9, 0}, {9, 30}}, {{10, 30}, {12, 0}}, {{14, 0}, {14, 30}}, {{15, 30}, {16, 0}}, {{17, 0}, {17, 30}}},
		"marie": {{{9, 30}, {10, 0}}, {{11, 0}, {11, 30}}, {{15, 0}, {15, 30}}},
	}
	start := m.now().In(loc)
	for i := 0; i < 14; i++ {
		d := time.Date(start.Year(), start.Month(), start.Day()+i, 0, 0, 0, 0, loc)
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		for pro, ranges := range schedule {
			for _, r := range ranges {
				m.AddOpening(pro,
					time.Date(d.Year(), d.Month(), d.Day(), r[0].h, r[0].m, 0, 0, loc),
					time.Date(d.Year(), d.Month(), d.Day(), r[1].h, r[1].m, 0, 0, loc))
			}
		}
	}
	return m, nil
}
