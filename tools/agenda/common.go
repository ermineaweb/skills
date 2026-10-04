// Package agendatools implémente les tools du skill agenda : create_event,
// list_events, update_event, delete_event.
//
// Comme les tools de calendrier (tools/calendar), ce sont les frontières
// contrôlées entre l'agent et l'agenda :
//   - les dates en langage naturel (« demain à 14h », « dans deux heures »)
//     sont converties par le code (package datetime), dans le fuseau de la
//     session, jamais par le modèle ;
//   - un événement n'est désigné que par un identifiant qu'un tool a
//     lui-même renvoyé dans la conversation, ou par une cible (moment et
//     mots-clés) résolue par le code : s'il y a plusieurs candidats, rien
//     n'est fait (EVENT_AMBIGUOUS) et la liste est renvoyée au modèle ;
//   - l'identité de l'utilisateur vient de la session, pas du modèle ;
//   - une écriture n'est réussie que si l'agenda l'a explicitement
//     confirmée ; les erreurs techniques ne sont jamais détaillées.
//
// Ce package ne connaît aucun modèle d'IA et aucun agenda concret.
package agendatools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"skills/datetime"
	"skills/services/agenda"
	"skills/types"
)

// Noms des tools exposés au modèle.
const (
	ToolCreateEvent = "create_event"
	ToolListEvents  = "list_events"
	ToolUpdateEvent = "update_event"
	ToolDeleteEvent = "delete_event"
)

const (
	// DefaultDurationMinutes : durée d'un événement dont ni la fin ni la
	// durée ne sont données.
	DefaultDurationMinutes = 60
	// MaxEventsReturned limite le nombre d'occurrences renvoyées au modèle.
	MaxEventsReturned = 50
	// maxCandidates : candidats cités dans une erreur d'ambiguïté ou de
	// conflit.
	maxCandidates = 5
	// conflictOccurrences : occurrences d'une série vérifiées pour les
	// conflits.
	conflictOccurrences = 10
)

// Portées d'une modification ou d'une suppression dans une série.
const (
	ScopeOccurrence = "occurrence"
	ScopeSeries     = "serie"
)

// ---- Registre des occurrences présentées (anti-hallucination) ----

const ledgerKey = "agendatools.ledger"

// ledger associe chaque identifiant renvoyé au modèle à l'occurrence
// présentée. Accédé pendant un tour de conversation uniquement (sous le
// verrou de la session).
type ledger map[string]agenda.Occurrence

func getLedger(st types.StateStore) ledger {
	if v, ok := st.Get(ledgerKey); ok {
		return v.(ledger)
	}
	l := ledger{}
	st.Set(ledgerKey, l)
	return l
}

// viewID identifie une occurrence : l'identifiant de l'événement, suivi du
// début de l'occurrence pour une série.
func viewID(o agenda.Occurrence) string {
	if !o.Recurring() {
		return o.Event.ID
	}
	return o.Event.ID + "@" + o.Start.UTC().Format("20060102T150405Z")
}

// ---- Vues renvoyées au modèle ----

// EventView est une occurrence telle que présentée à l'agent. Les dates
// sont dans le fuseau de la session ; Libelle est calculé par le code.
type EventView struct {
	ID             string   `json:"id"`
	Titre          string   `json:"titre"`
	Debut          string   `json:"debut"`
	Fin            string   `json:"fin"`
	Libelle        string   `json:"libelle"`
	JourneeEntiere bool     `json:"journee_entiere,omitempty"`
	Lieu           string   `json:"lieu,omitempty"`
	Description    string   `json:"description,omitempty"`
	Participants   []string `json:"participants,omitempty"`
	RappelsMinutes []int    `json:"rappels_minutes,omitempty"`
	// Recurrence décrit la série dont fait partie l'occurrence.
	Recurrence string `json:"recurrence,omitempty"`
}

func view(o agenda.Occurrence, loc *time.Location) EventView {
	e := o.Event
	return EventView{
		ID: viewID(o), Titre: e.Title,
		Debut: o.Start.In(loc).Format(time.RFC3339), Fin: o.End.In(loc).Format(time.RFC3339),
		Libelle: label(o.Start, o.End, e.AllDay, loc), JourneeEntiere: e.AllDay,
		Lieu: e.Location, Description: e.Description, Participants: e.Attendees, RappelsMinutes: e.Reminders,
		Recurrence: describeRecurrence(e.Recurrence, loc),
	}
}

// remember enregistre les occurrences présentées et renvoie leurs vues.
func remember(tc types.ToolContext, occ []agenda.Occurrence) []EventView {
	l := getLedger(tc.State)
	out := make([]EventView, len(occ))
	for i, o := range occ {
		out[i] = view(o, tc.Location)
		l[out[i].ID] = o
	}
	return out
}

// label : « vendredi 9 octobre de 10h à 11h », « samedi 10 octobre (journée
// entière) », « du lundi 5 octobre à 22h au mardi 6 octobre à 2h ».
func label(start, end time.Time, allDay bool, loc *time.Location) string {
	start, end = start.In(loc), end.In(loc)
	if allDay {
		last := end.AddDate(0, 0, -1)
		if sameDay(start, last) {
			return datetime.FormatDayFR(start, nil) + " (journée entière)"
		}
		return "du " + datetime.FormatDayFR(start, nil) + " au " + datetime.FormatDayFR(last, nil) + " (journées entières)"
	}
	if sameDay(start, end) {
		return fmt.Sprintf("%s de %s à %s", datetime.FormatDayFR(start, nil), datetime.FormatClockFR(start, nil), datetime.FormatClockFR(end, nil))
	}
	return "du " + datetime.FormatFR(start, nil) + " au " + datetime.FormatFR(end, nil)
}

func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}

var frequencyLabels = map[agenda.Frequency][2]string{
	agenda.Daily:   {"tous les jours", "tous les %d jours"},
	agenda.Weekly:  {"toutes les semaines", "toutes les %d semaines"},
	agenda.Monthly: {"tous les mois", "tous les %d mois"},
	agenda.Yearly:  {"tous les ans", "tous les %d ans"},
}

var weekdayNames = [...]string{"dimanche", "lundi", "mardi", "mercredi", "jeudi", "vendredi", "samedi"}

var positionNames = map[int]string{1: "premier", 2: "deuxième", 3: "troisième", 4: "quatrième", 5: "cinquième", -1: "dernier"}

// describeRecurrence : « toutes les semaines le lundi, jusqu'au jeudi 31
// décembre », « tous les mois, le premier lundi, 6 fois ».
func describeRecurrence(r *agenda.Recurrence, loc *time.Location) string {
	if r == nil {
		return ""
	}
	labels := frequencyLabels[r.Frequency]
	s := labels[0]
	if r.Interval > 1 {
		s = fmt.Sprintf(labels[1], r.Interval)
	}
	if len(r.Weekdays) > 0 {
		days := make([]string, len(r.Weekdays))
		for i, d := range r.Weekdays {
			days[i] = weekdayNames[d]
		}
		if r.Position != 0 {
			s += fmt.Sprintf(", le %s %s", positionNames[r.Position], days[0])
		} else {
			s += " le " + strings.Join(days, ", le ")
		}
	}
	switch {
	case r.Until != nil:
		s += ", jusqu'au " + datetime.FormatDayFR(*r.Until, loc)
	case r.Count > 0:
		s += fmt.Sprintf(", %d fois", r.Count)
	}
	return s
}

// ---- Dates ----

var (
	reHours = regexp.MustCompile(`(?i)\b(\d{1,2})\s*heures?\b`)
	// « midi » seul (pas « après-midi ») désigne 12h quand on fixe une
	// heure : datetime y voit sinon la plage 12h-14h.
	reNoon   = regexp.MustCompile(`(?i)(^|[^-\p{L}])midi\b`)
	reNight  = regexp.MustCompile(`(?i)\bminuit\b`)
	reNoonPM = regexp.MustCompile(`(?i)apr[eè]s[\s-]*midi`)
)

// moment est un instant ou une période résolu.
type moment struct {
	period  datetime.Period
	isClock bool // heure seule (« 16h ») : le jour vient du contexte
	hour    int
	minute  int
}

// parseMoment lit une date ISO 8601 (avec ou sans fuseau, ou une date
// seule) ou une expression en français. pointInTime : « midi » désigne
// 12h (création, déplacement) et non la plage du déjeuner (consultation).
func parseMoment(s string, tc types.ToolContext, pointInTime bool) (moment, *types.Error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		t = t.In(tc.Location)
		return moment{period: datetime.Period{Start: t, End: t.Add(time.Hour), Granularity: datetime.GranularityHour}}, nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, s, tc.Location); err == nil {
			return moment{period: datetime.Period{Start: t, End: t.Add(time.Hour), Granularity: datetime.GranularityHour}}, nil
		}
	}
	if t, err := time.ParseInLocation(time.DateOnly, s, tc.Location); err == nil {
		return moment{period: datetime.Period{Start: t, End: t.AddDate(0, 0, 1), Granularity: datetime.GranularityDay}}, nil
	}
	if h, m, ok := datetime.ParseClock(s); ok {
		return moment{isClock: true, hour: h, minute: m}, nil
	}
	expr := reHours.ReplaceAllString(s, "${1}h")
	if pointInTime && !reNoonPM.MatchString(expr) {
		expr = reNoon.ReplaceAllString(expr, "${1}12h")
	}
	expr = reNight.ReplaceAllString(expr, "0h")
	p, err := datetime.ResolveIncludingPast(expr, tc.Now, tc.Location)
	switch {
	case errors.Is(err, datetime.ErrMissingDay):
		return moment{}, &types.Error{Code: types.ErrMissingInformation, Message: datetime.Describe(err), Missing: []string{"jour"}}
	case err != nil:
		return moment{}, types.Errorf(types.ErrInvalidRequest, "%q : %s", s, datetime.Describe(err))
	}
	return moment{period: p}, nil
}

// at place l'heure h:m le jour de day (fuseau de day).
func at(day time.Time, h, m int) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, day.Location())
}

// startOf renvoie l'instant précis d'un moment, ou demande l'heure.
func startOf(m moment, day time.Time) (time.Time, *types.Error) {
	switch {
	case m.isClock:
		return at(day, m.hour, m.minute), nil
	case m.period.Granularity == datetime.GranularityHour:
		return m.period.Start, nil
	case m.period.Granularity == datetime.GranularityDay || m.period.Granularity == datetime.GranularityDayPart:
		return time.Time{}, &types.Error{Code: types.ErrMissingInformation,
			Message: "L'heure n'est pas précisée : demande-la à l'utilisateur.", Missing: []string{"heure"}}
	default:
		return time.Time{}, types.Errorf(types.ErrInvalidRequest, "Précise le jour et l'heure (une période de plusieurs jours a été donnée).")
	}
}

// endOf calcule la fin d'un événement commençant à start : fin (heure seule
// le même jour, ou instant), sinon durée, sinon dur.
func endOf(start time.Time, fin string, dureeMinutes int, dur time.Duration, tc types.ToolContext) (time.Time, *types.Error) {
	switch {
	case fin != "" && dureeMinutes > 0:
		return time.Time{}, types.Errorf(types.ErrInvalidRequest, "Donne soit fin, soit duree_minutes, pas les deux.")
	case dureeMinutes > 0:
		return start.Add(time.Duration(dureeMinutes) * time.Minute), nil
	case fin == "":
		return start.Add(dur), nil
	}
	m, err := parseMoment(fin, tc, true)
	if err != nil {
		return time.Time{}, err
	}
	var end time.Time
	if m.isClock {
		end = at(start, m.hour, m.minute)
		if !end.After(start) { // « de 22h à 1h »
			end = end.AddDate(0, 0, 1)
		}
	} else if end, err = startOf(m, start); err != nil {
		return time.Time{}, err
	}
	if !end.After(start) {
		return time.Time{}, types.Errorf(types.ErrInvalidRequest, "La fin (%s) doit suivre le début (%s).",
			datetime.FormatFR(end, tc.Location), datetime.FormatFR(start, tc.Location))
	}
	return end, nil
}

// ---- Identification d'un événement ----

// Target désigne un événement : soit par identifiant (event_id), soit par
// une cible résolue par le code.
type Target struct {
	EventID string  `json:"event_id"`
	Cible   *Search `json:"cible"`
}

// Search : un moment (« demain à 14h », « vendredi ») et des mots-clés
// facultatifs (« Paul », « réunion »).
type Search struct {
	Quand string `json:"quand"`
	Texte string `json:"texte"`
}

// resolveTarget renvoie l'occurrence désignée, à jour. Plusieurs candidats :
// EVENT_AMBIGUOUS avec la liste, rien n'est choisi.
func resolveTarget(ctx context.Context, p agenda.Provider, tc types.ToolContext, t Target) (agenda.Occurrence, *types.Error) {
	switch {
	case t.EventID != "" && t.Cible != nil:
		return agenda.Occurrence{}, types.Errorf(types.ErrInvalidRequest, "Donne soit event_id, soit cible, pas les deux.")
	case t.EventID != "":
		snap, ok := getLedger(tc.State)[t.EventID]
		if !ok {
			return agenda.Occurrence{}, types.Errorf(types.ErrInvalidRequest,
				"event_id inconnu : n'utilise que des identifiants renvoyés par %s ou %s dans cette conversation, ne les invente jamais. Sinon, utilise cible.",
				ToolListEvents, ToolCreateEvent)
		}
		res, err := p.ListEvents(ctx, agenda.ListRequest{OwnerID: tc.User.ClientID, From: snap.Start, To: snap.End})
		if err != nil {
			return agenda.Occurrence{}, providerError(ctx, "list", err)
		}
		for _, o := range res.Occurrences {
			if o.Event.ID == snap.Event.ID && o.Start.Equal(snap.Start) {
				return o, nil
			}
		}
		return agenda.Occurrence{}, types.Errorf(types.ErrEventNotFound,
			"Cet événement n'est plus à cette place dans l'agenda (modifié ou supprimé entre-temps). Consulte de nouveau l'agenda avec %s.", ToolListEvents)
	case t.Cible != nil && strings.TrimSpace(t.Cible.Quand) != "":
		m, err := parseMoment(t.Cible.Quand, tc, true)
		if err != nil {
			return agenda.Occurrence{}, err
		}
		if m.isClock {
			return agenda.Occurrence{}, &types.Error{Code: types.ErrMissingInformation,
				Message: "Le jour de l'événement visé n'est pas précisé : déduis-le de la conversation ou demande-le.", Missing: []string{"jour"}}
		}
		res, perr := p.ListEvents(ctx, agenda.ListRequest{OwnerID: tc.User.ClientID, From: m.period.Start, To: m.period.End, Query: t.Cible.Texte})
		if perr != nil {
			return agenda.Occurrence{}, providerError(ctx, "list", perr)
		}
		var found []agenda.Occurrence
		for _, o := range res.Occurrences {
			// Une heure précise désigne l'événement qui commence à ce
			// moment-là, pas ceux qui sont simplement en cours.
			if m.period.Granularity == datetime.GranularityHour && o.Start.Before(m.period.Start) {
				continue
			}
			found = append(found, o)
		}
		switch len(found) {
		case 0:
			return agenda.Occurrence{}, types.Errorf(types.ErrEventNotFound,
				"Aucun événement ne correspond (%s%s). Dis-le à l'utilisateur ; propose de consulter l'agenda avec %s.",
				label(m.period.Start, m.period.End, false, tc.Location), withText(t.Cible.Texte), ToolListEvents)
		case 1:
			remember(tc, found)
			return found[0], nil
		default:
			return agenda.Occurrence{}, types.Errorf(types.ErrEventAmbiguous,
				"%d événements correspondent : %s. N'en choisis aucun : demande à l'utilisateur lequel il vise, puis rappelle avec son event_id.",
				len(found), list(remember(tc, found)))
		}
	default:
		return agenda.Occurrence{}, &types.Error{Code: types.ErrMissingInformation,
			Message: "Indique l'événement : event_id renvoyé par un outil, ou cible.quand (et cible.texte).", Missing: []string{"event_id"}}
	}
}

func withText(q string) string {
	if strings.TrimSpace(q) == "" {
		return ""
	}
	return fmt.Sprintf(", « %s »", q)
}

// list cite les premières vues : « [id] titre (libellé) ; … ».
func list(views []EventView) string {
	parts := make([]string, 0, maxCandidates+1)
	for i, v := range views {
		if i == maxCandidates {
			parts = append(parts, fmt.Sprintf("et %d autre(s)", len(views)-maxCandidates))
			break
		}
		parts = append(parts, fmt.Sprintf("[%s] %s (%s)", v.ID, v.Titre, v.Libelle))
	}
	return strings.Join(parts, " ; ")
}

// scope vérifie la portée demandée pour une occurrence : dans une série,
// elle est obligatoire (une occurrence ou toute la série ?).
func scope(o agenda.Occurrence, portee string) (string, *types.Error) {
	if !o.Recurring() {
		return ScopeOccurrence, nil
	}
	if portee == "" {
		return "", &types.Error{Code: types.ErrMissingInformation,
			Message: fmt.Sprintf("« %s » fait partie d'une série (%s). Demande à l'utilisateur s'il s'agit de cette seule occurrence ou de toute la série, puis précise portee.",
				o.Event.Title, describeRecurrence(o.Event.Recurrence, o.Start.Location())),
			Missing: []string{"portee"}}
	}
	return portee, nil
}

// ---- Conflits ----

// conflicts renvoie les occurrences (hors journées entières et hors
// l'événement ignoreID) qui chevauchent les occurrences de e.
func conflicts(ctx context.Context, p agenda.Provider, tc types.ToolContext, e agenda.Event, ignoreID string) ([]agenda.Occurrence, *types.Error) {
	if e.AllDay {
		return nil, nil
	}
	mine := agenda.Occurrences(e, e.Start, e.Start.AddDate(1, 0, 0), conflictOccurrences)
	if len(mine) == 0 {
		return nil, nil
	}
	res, err := p.ListEvents(ctx, agenda.ListRequest{OwnerID: tc.User.ClientID, From: mine[0].Start, To: mine[len(mine)-1].End})
	if err != nil {
		// Période trop longue pour l'agenda : on vérifie la première.
		if res, err = p.ListEvents(ctx, agenda.ListRequest{OwnerID: tc.User.ClientID, From: mine[0].Start, To: mine[0].End}); err != nil {
			return nil, providerError(ctx, "list", err)
		}
	}
	var out []agenda.Occurrence
	for _, o := range res.Occurrences {
		if o.Event.AllDay || o.Event.ID == ignoreID {
			continue
		}
		for _, m := range mine {
			if o.Start.Before(m.End) && m.Start.Before(o.End) {
				out = append(out, o)
				break
			}
		}
	}
	return out, nil
}

func conflictError(tc types.ToolContext, found []agenda.Occurrence) *types.Error {
	return types.Errorf(types.ErrEventConflict,
		"Ce moment chevauche : %s. Rien n'a été enregistré. Signale-le à l'utilisateur ; s'il confirme, rappelle avec ignorer_conflits: true.",
		list(remember(tc, found)))
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

// checkContext : fuseau, état de session et utilisateur authentifié.
func checkContext(tc types.ToolContext) *types.Error {
	if tc.Location == nil {
		return types.Errorf(types.ErrInternal, "Fuseau horaire de la session non défini.")
	}
	if tc.State == nil {
		return types.Errorf(types.ErrInternal, "État de session indisponible.")
	}
	if tc.User.ClientID == "" {
		return types.NewError(types.ErrNotAuthenticated)
	}
	return nil
}

// providerError convertit une erreur de l'agenda en erreur exploitable par
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
	slog.WarnContext(ctx, "erreur technique de l'agenda", "operation", op, "error", err)
	return types.NewError(types.ErrCalendarUnavailable)
}

// cleanList retire les éléments vides et les espaces.
func cleanList(in []string) []string {
	out := []string{}
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
