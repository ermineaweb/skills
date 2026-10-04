package agendatools

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"skills/datetime"
	"skills/services/agenda"
	"skills/types"
)

type listArgs struct {
	Periode string `json:"periode"`
	Du      string `json:"du"`
	Au      string `json:"au"`
	Texte   string `json:"texte"`
}

// PeriodView est la période consultée.
type PeriodView struct {
	Du      string `json:"du"`
	Au      string `json:"au"`
	Libelle string `json:"libelle"`
}

// ListOutput est le résultat de list_events.
type ListOutput struct {
	Periode    PeriodView  `json:"periode"`
	Evenements []EventView `json:"evenements"`
	Nombre     int         `json:"nombre"`
	Tronque    bool        `json:"tronque,omitempty"`
	Note       string      `json:"note,omitempty"`
}

// NewListEvents renvoie le handler de list_events.
func NewListEvents(p agenda.Provider) types.ToolHandler {
	return types.ToolHandlerFunc(func(ctx context.Context, tc types.ToolContext, raw json.RawMessage) (any, *types.Error) {
		if err := checkContext(tc); err != nil {
			return nil, err
		}
		var a listArgs
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		from, to, note, err := listPeriod(a, tc)
		if err != nil {
			return nil, err
		}
		res, perr := p.ListEvents(ctx, agenda.ListRequest{OwnerID: tc.User.ClientID, From: from, To: to, Query: a.Texte})
		if perr != nil {
			return nil, providerError(ctx, "list", perr)
		}
		occ := res.Occurrences
		out := ListOutput{
			Periode: PeriodView{Du: from.Format(time.RFC3339), Au: to.Format(time.RFC3339), Libelle: periodLabel(from, to, tc.Location)},
			Nombre:  len(occ),
			Note:    note,
		}
		if len(occ) > MaxEventsReturned {
			occ, out.Tronque = occ[:MaxEventsReturned], true
		}
		out.Evenements = remember(tc, occ)
		if len(occ) == 0 {
			out.Note = strings.TrimSpace(out.Note + " Aucun événement sur cette période" + withText(a.Texte) + " : l'agenda est libre.")
		}
		return out, nil
	})
}

// listPeriod : période en langage naturel, ou du/au explicites.
func listPeriod(a listArgs, tc types.ToolContext) (time.Time, time.Time, string, *types.Error) {
	switch {
	case a.Periode != "" && (a.Du != "" || a.Au != ""):
		return time.Time{}, time.Time{}, "", types.Errorf(types.ErrInvalidRequest, "Donne soit periode, soit du et au, pas les deux.")
	case a.Periode != "":
		m, err := parseMoment(a.Periode, tc, false)
		if err != nil {
			return time.Time{}, time.Time{}, "", err
		}
		if m.isClock {
			return time.Time{}, time.Time{}, "", &types.Error{Code: types.ErrMissingInformation,
				Message: "Le jour n'est pas précisé : déduis-le de la conversation ou demande-le.", Missing: []string{"jour"}}
		}
		return m.period.Start, m.period.End, m.period.Note, nil
	case a.Du != "" && a.Au != "":
		from, err := boundary(a.Du, tc, false)
		if err != nil {
			return time.Time{}, time.Time{}, "", err
		}
		to, err := boundary(a.Au, tc, true)
		if err != nil {
			return time.Time{}, time.Time{}, "", err
		}
		if !to.After(from) {
			return time.Time{}, time.Time{}, "", types.Errorf(types.ErrInvalidRequest, "au doit suivre du.")
		}
		return from, to, "", nil
	default:
		return time.Time{}, time.Time{}, "", &types.Error{Code: types.ErrMissingInformation,
			Message: "Indique la période : periode (ex : « demain », « cette semaine ») ou du et au.", Missing: []string{"periode"}}
	}
}

// boundary lit une borne : un jour seul vaut son début (du) ou sa fin (au).
func boundary(s string, tc types.ToolContext, end bool) (time.Time, *types.Error) {
	m, err := parseMoment(s, tc, true)
	if err != nil {
		return time.Time{}, err
	}
	switch {
	case m.isClock:
		return time.Time{}, types.Errorf(types.ErrInvalidRequest, "%q : précise le jour.", s)
	case m.period.Granularity == datetime.GranularityHour:
		return m.period.Start, nil
	case end:
		return m.period.End, nil
	default:
		return m.period.Start, nil
	}
}

// periodLabel : « vendredi 9 octobre », « vendredi 9 octobre de 12h à
// 18h », « du lundi 5 octobre au dimanche 11 octobre ».
func periodLabel(from, to time.Time, loc *time.Location) string {
	from, to = from.In(loc), to.In(loc)
	midnight := func(t time.Time) bool { return t.Hour() == 0 && t.Minute() == 0 }
	if midnight(from) && midnight(to) {
		last := to.AddDate(0, 0, -1)
		if sameDay(from, last) {
			return datetime.FormatDayFR(from, nil)
		}
		return "du " + datetime.FormatDayFR(from, nil) + " au " + datetime.FormatDayFR(last, nil)
	}
	return label(from, to, false, loc)
}
