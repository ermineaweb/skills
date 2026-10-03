"use strict";

// Le fuseau horaire vient du navigateur : il n'est jamais supposé côté serveur.
const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone;
let sessionId = null;
let skills = []; // skills chargés par l'API (GET /api/skills)
let calendar = null; // agenda de démonstration (calendar.js)
let prospects = null; // panneau des prospects (prospects.js)

const $ = (id) => document.getElementById(id);
const messages = $("messages");
const input = $("input");
const send = $("send");

$("tz").textContent = timezone ? `Fuseau : ${timezone}` : "Fuseau horaire inconnu";

const RDV = "prise-de-rendez-vous";
const PROSPECTS = "prospect-research";

// Présentation de chaque skill : l'interface n'affiche que ceux que l'API a
// chargés.
const SKILL_UI = {
  [RDV]: {
    intro: "prendre, déplacer ou annuler un rendez-vous",
    detail: "Rendez-vous : agenda fictif de Paul Martin et Marie Dubois, jours ouvrés des deux prochaines semaines.",
    suggestions: [
      "Je voudrais un rendez-vous jeudi après-midi.",
      "Je voudrais voir Paul la semaine prochaine.",
      "Est-ce que vous avez quelque chose demain matin ?",
    ],
  },
  [PROSPECTS]: {
    intro: "rechercher des entreprises correspondant à un profil de client",
    detail: "Prospects : moteur de recherche simulé, entreprises fictives (aucun accès à Internet).",
    suggestions: [
      "Trouve-moi 5 entreprises françaises de 20 à 200 salariés dans le SaaS B2B.",
      "Trouve des éditeurs SaaS B2B français qui recrutent des commerciaux.",
      "Trouve des éditeurs SaaS B2B français basés à Lyon ou à Lille.",
    ],
  },
};

// Libellés affichés à partir des effets confirmés par les tools
// (et non à partir du texte généré par le modèle).
const EFFECT_LABELS = {
  reserver_creneau: "Réservation enregistrée dans le calendrier",
  modifier_rendez_vous: "Déplacement enregistré dans le calendrier",
  annuler_rendez_vous: "Annulation enregistrée dans le calendrier",
  enregistrer_prospects: "Résultat de recherche enregistré",
};

async function api(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: body ? { "Content-Type": "application/json" } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  });
  let data = {};
  try { data = await res.json(); } catch { /* réponse vide */ }
  if (!res.ok) {
    const err = new Error(data.error || `Erreur ${res.status}`);
    err.data = data;
    err.status = res.status;
    throw err;
  }
  return data;
}

const has = (name) => skills.includes(name);
const ui = () => skills.map((s) => SKILL_UI[s]).filter(Boolean);

function addItem(className, text) {
  const li = document.createElement("li");
  li.className = className;
  li.textContent = text; // textContent : aucun HTML injecté
  messages.append(li);
  messages.scrollTop = messages.scrollHeight;
  return li;
}

function addEffects(effects) {
  for (const e of effects || []) {
    const appt = e.result && e.result.appointment;
    addItem("effect", `✓ ${EFFECT_LABELS[e.tool] || e.tool}${appt ? ` : ${appt.libelle}` : ""}`);
    // L'agenda se place sur la semaine du rendez-vous concerné.
    // start est exprimé dans le fuseau de la session : sa date est locale.
    if (calendar && appt && appt.start) calendar.goTo(appt.start.slice(0, 10), appt.id);
  }
}

function addTools(steps) {
  if (!steps || steps.length === 0) return;
  const li = document.createElement("li");
  li.className = "tools";
  const details = document.createElement("details");
  const summary = document.createElement("summary");
  summary.textContent = `${steps.length} appel(s) d'outils`;
  const pre = document.createElement("pre");
  pre.textContent = steps
    .map((s) => `${s.success ? "✓" : "✗ " + s.error_code} ${s.tool} ${JSON.stringify(s.arguments)}`)
    .join("\n");
  details.append(summary, pre);
  li.append(details);
  messages.append(li);
}

async function refreshAgenda() {
  if (!has(RDV)) return;
  if (calendar) calendar.refresh();
  const list = $("agenda");
  try {
    const data = await api("GET", `/api/sessions/${sessionId}/rendez-vous`);
    list.replaceChildren();
    for (const a of data.rendez_vous) {
      const li = document.createElement("li");
      const when = document.createElement("strong");
      when.textContent = a.libelle;
      const who = document.createElement("span");
      who.className = "muted small";
      who.textContent = `${a.type_rendez_vous} avec ${a.professionnel_nom}`;
      li.append(when, who);
      list.append(li);
    }
    $("agenda-empty").hidden = data.rendez_vous.length > 0;
  } catch {
    $("agenda-empty").hidden = false;
    $("agenda-empty").textContent = "Agenda momentanément indisponible.";
  }
}

function refreshPanels() {
  refreshAgenda();
  if (prospects) prospects.refresh();
}

function setBusy(busy) {
  input.disabled = busy;
  send.disabled = busy;
  for (const b of $("suggestions").querySelectorAll("button")) b.disabled = busy;
}

async function sendMessage(text) {
  text = text.trim();
  if (!text || !sessionId) return;
  addItem("msg user", text);
  input.value = "";
  setBusy(true);
  const typing = addItem("msg bot typing", "…");
  try {
    const data = await api("POST", `/api/sessions/${sessionId}/messages`, { message: text });
    typing.remove();
    addTools(data.steps);
    addEffects(data.effects);
    addItem("msg bot", data.text || "(réponse vide)");
  } catch (err) {
    typing.remove();
    if (err.data) {
      addTools(err.data.steps);
      addEffects(err.data.effects);
    }
    if (err.status === 404) {
      addItem("msg error", "La session a expiré. Rechargez la page pour recommencer.");
    } else {
      addItem("msg error", err.message);
    }
  } finally {
    setBusy(false);
    input.focus();
    refreshPanels();
  }
}

function joinFr(parts) {
  return parts.length <= 1 ? parts.join("") : `${parts.slice(0, -1).join(", ")} ou ${parts[parts.length - 1]}`;
}

// Écran d'accueil : description des skills chargés.
async function loadSkills() {
  try {
    skills = (await api("GET", "/api/skills")).skills;
  } catch {
    skills = [RDV]; // API antérieure à /api/skills
  }
  const list = $("start-skills");
  list.replaceChildren();
  for (const s of ui()) {
    const li = document.createElement("li");
    li.textContent = s.detail;
    list.append(li);
  }
}
const skillsLoaded = loadSkills();

$("start-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const errorBox = $("start-error");
  errorBox.hidden = true;
  if (!timezone) {
    errorBox.textContent = "Impossible de déterminer votre fuseau horaire.";
    errorBox.hidden = false;
    return;
  }
  try {
    await skillsLoaded;
    const data = await api("POST", "/api/sessions", { timezone, nom: $("nom").value });
    sessionId = data.session_id;
    $("start").hidden = true;
    $("app").hidden = false;

    for (const text of ui().flatMap((s) => s.suggestions)) {
      const b = document.createElement("button");
      b.type = "button";
      b.textContent = text;
      $("suggestions").append(b);
    }
    if (has(RDV)) {
      $("agenda-card").hidden = false;
      $("show-calendar").parentElement.hidden = false;
      calendar = createDemoCalendar(
        (date) => api("GET", `/api/sessions/${sessionId}/calendrier${date ? `?date=${date}` : ""}`),
        () => { calendar = null; $("calendar").hidden = true; $("show-calendar").parentElement.hidden = true; updateLayout(); },
      );
    }
    if (has(PROSPECTS)) {
      $("prospects").hidden = false;
      prospects = createProspectsPanel(() => api("GET", `/api/sessions/${sessionId}/prospects`));
    }
    showCalendar(storedCalendarPref());

    const intros = ui().map((s) => s.intro);
    addItem("msg bot", intros.length
      ? `Bonjour ! Je peux ${joinFr(intros)}. Que puis-je faire pour vous ?`
      : "Bonjour ! Que puis-je faire pour vous ?");
    refreshPanels();
    input.focus();
  } catch (err) {
    errorBox.textContent = err.message;
    errorBox.hidden = false;
  }
});

$("chat-form").addEventListener("submit", (event) => {
  event.preventDefault();
  sendMessage(input.value);
});

$("suggestions").addEventListener("click", (event) => {
  if (event.target instanceof HTMLButtonElement) sendMessage(event.target.textContent);
});

// Colonne de droite élargie quand elle contient l'agenda ou les prospects.
function updateLayout() {
  const wide = !$("calendar").hidden || !$("prospects").hidden;
  $("app").classList.toggle("wide", wide);
}

// Affichage de l'agenda : préférence locale, sans incidence si le stockage
// est indisponible (navigation privée…).
function storedCalendarPref() {
  try { return localStorage.getItem("show-calendar") !== "0"; } catch { return true; }
}

function showCalendar(visible) {
  $("show-calendar").checked = visible;
  $("calendar").hidden = !visible || !calendar;
  updateLayout();
}

$("show-calendar").addEventListener("change", (event) => {
  showCalendar(event.target.checked);
  try { localStorage.setItem("show-calendar", event.target.checked ? "1" : "0"); } catch { /* ignoré */ }
});

$("show-tools").addEventListener("change", (event) => {
  document.body.classList.toggle("show-tools", event.target.checked);
});
