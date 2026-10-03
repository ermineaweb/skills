"use strict";

// Agenda de démonstration : vue semaine des plages d'ouverture et des
// rendez-vous, lue dans le calendrier (GET /api/sessions/{id}/calendrier).
// Indépendant du chat : app.js appelle seulement refresh() et goTo().

const PX_PER_HOUR = 44;
const MAX_PROS = 6; // couleurs définies dans style.css (.pro-0 … .pro-5)

// load(date) renvoie la semaine contenant date ("AAAA-MM-JJ", ou null pour
// aujourd'hui). onUnavailable() est appelé si la vue est désactivée côté API.
function createDemoCalendar(load, onUnavailable) {
  const $ = (id) => document.getElementById(id);
  const grid = $("cal-grid");
  const errorBox = $("cal-error");
  let date = null; // jour demandé, null = semaine courante
  let shown = null; // lundi de la semaine affichée
  let highlight = null; // rendez-vous à mettre en évidence (dernier effet)
  let pending = 0;

  // Dates "AAAA-MM-JJ" manipulées à midi UTC : aucun effet de fuseau.
  const toDate = (s) => new Date(`${s}T12:00:00Z`);
  const fmt = (s, opts) => toDate(s).toLocaleDateString("fr-FR", { timeZone: "UTC", ...opts });
  const addDays = (s, n) => {
    const d = toDate(s);
    d.setUTCDate(d.getUTCDate() + n);
    return d.toISOString().slice(0, 10);
  };
  const hhmm = (min) => `${Math.floor(min / 60)}h${min % 60 ? String(min % 60).padStart(2, "0") : ""}`;

  function el(tag, className, text) {
    const e = document.createElement(tag);
    if (className) e.className = className;
    if (text !== undefined) e.textContent = text;
    return e;
  }

  function place(e, b, hourMin) {
    e.style.top = `${((b.debut_min - hourMin * 60) / 60) * PX_PER_HOUR}px`;
    e.style.height = `${Math.max(((b.fin_min - b.debut_min) / 60) * PX_PER_HOUR, 4)}px`;
  }

  function render(week) {
    const proIndex = new Map(week.professionnels.map((p, i) => [p.id, i % MAX_PROS]));
    const proName = new Map(week.professionnels.map((p) => [p.id, p.nom]));
    const last = week.jours.length ? week.jours[week.jours.length - 1].date : addDays(week.debut, 4);

    $("cal-week").textContent =
      `Semaine du ${fmt(week.debut, { day: "numeric", month: "long" })} au ${fmt(last, { day: "numeric", month: "long", year: "numeric" })}`;

    const legend = $("cal-legend");
    legend.replaceChildren();
    for (const p of week.professionnels) {
      const li = el("li", `pro-${proIndex.get(p.id)}`);
      li.append(el("span", "swatch"), document.createTextNode(p.nom));
      legend.append(li);
    }
    const mine = el("li", "legend-mine");
    mine.append(el("span", "swatch"), document.createTextNode("Mes rendez-vous"));
    const open = el("li", "legend-open");
    open.append(el("span", "swatch"), document.createTextNode("Pâle : libre · vif : réservé"));
    legend.append(mine, open);

    const hours = week.heure_max - week.heure_min;
    grid.style.setProperty("--days", week.jours.length);
    grid.style.setProperty("--body-height", `${hours * PX_PER_HOUR}px`);
    grid.style.setProperty("--hour", `${PX_PER_HOUR}px`);
    grid.replaceChildren();

    // En-têtes : coin vide puis un jour par colonne.
    grid.append(el("div", "cal-corner"));
    for (const d of week.jours) {
      const head = el("div", `cal-day-head${d.aujourdhui ? " today" : ""}`);
      head.append(el("span", "dow", fmt(d.date, { weekday: "short" })), el("span", "dom", fmt(d.date, { day: "numeric" })));
      grid.append(head);
    }

    // Colonne des heures.
    const axis = el("div", "cal-axis");
    for (let h = week.heure_min; h < week.heure_max; h++) {
      const label = el("span", "cal-hour", `${h}h`);
      label.style.top = `${(h - week.heure_min) * PX_PER_HOUR}px`;
      axis.append(label);
    }
    grid.append(axis);

    for (const d of week.jours) {
      const col = el("div", `cal-day${d.aujourdhui ? " today" : ""}`);
      // Une voie par professionnel, côte à côte dans la journée.
      const lanes = new Map();
      for (const p of week.professionnels) {
        const lane = el("div", `cal-lane pro-${proIndex.get(p.id)}`);
        lanes.set(p.id, lane);
        col.append(lane);
      }
      for (const o of week.ouvertures.filter((x) => x.jour === d.date)) {
        const b = el("div", "cal-open");
        b.title = `${proName.get(o.professionnel_id)} · ouvert de ${hhmm(o.debut_min)} à ${hhmm(o.fin_min)}`;
        place(b, o, week.heure_min);
        lanes.get(o.professionnel_id)?.append(b);
      }
      for (const a of week.rendez_vous.filter((x) => x.jour === d.date)) {
        const b = el("div", `cal-appt${a.moi ? " mine" : ""}${a.id === highlight ? " flash" : ""}`);
        b.title = `${hhmm(a.debut_min)}–${hhmm(a.fin_min)} · ${proName.get(a.professionnel_id)} · ${a.moi ? a.type_rendez_vous : "autre client"}`;
        // Bloc court (30 min) : une seule ligne.
        if (a.fin_min - a.debut_min < 45) b.classList.add("short");
        b.append(el("span", "t", hhmm(a.debut_min)), el("span", "w", a.moi ? `Moi · ${a.type_rendez_vous}` : "Réservé"));
        place(b, a, week.heure_min);
        lanes.get(a.professionnel_id)?.append(b);
      }
      if (d.aujourdhui && week.maintenant.debut_min >= week.heure_min * 60 && week.maintenant.debut_min <= week.heure_max * 60) {
        const now = el("div", "cal-now");
        now.style.top = `${((week.maintenant.debut_min - week.heure_min * 60) / 60) * PX_PER_HOUR}px`;
        col.append(now);
      }
      grid.append(col);
    }
  }

  async function refresh() {
    const ticket = ++pending;
    try {
      const week = await load(date);
      if (ticket !== pending) return; // une requête plus récente a pris le relais
      errorBox.hidden = true;
      shown = week.debut;
      render(week);
      highlight = null; // une seule fois
    } catch (err) {
      if (err.status === 404 && !err.data?.error?.includes("session")) {
        onUnavailable();
        return;
      }
      errorBox.textContent = "Agenda momentanément indisponible.";
      errorBox.hidden = false;
    }
  }

  // goTo affiche la semaine contenant d (null = aujourd'hui) et met en
  // évidence le rendez-vous appointmentId s'il y figure.
  function goTo(d, appointmentId = null) {
    date = d;
    highlight = appointmentId;
    return refresh();
  }

  $("cal-prev").addEventListener("click", () => shown && goTo(addDays(shown, -7)));
  $("cal-next").addEventListener("click", () => shown && goTo(addDays(shown, 7)));
  $("cal-today").addEventListener("click", () => goTo(null));

  return { refresh, goTo };
}
