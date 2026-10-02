"use strict";

// Le fuseau horaire vient du navigateur : il n'est jamais supposé côté serveur.
const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone;
let sessionId = null;

const $ = (id) => document.getElementById(id);
const messages = $("messages");
const input = $("input");
const send = $("send");

$("tz").textContent = timezone ? `Fuseau : ${timezone}` : "Fuseau horaire inconnu";

// Libellés affichés à partir des effets confirmés par le calendrier
// (et non à partir du texte généré par le modèle).
const EFFECT_LABELS = {
  reserver_creneau: "Réservation enregistrée dans le calendrier",
  modifier_rendez_vous: "Déplacement enregistré dans le calendrier",
  annuler_rendez_vous: "Annulation enregistrée dans le calendrier",
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
    const libelle = e.result && e.result.appointment ? ` : ${e.result.appointment.libelle}` : "";
    addItem("effect", `✓ ${EFFECT_LABELS[e.tool] || e.tool}${libelle}`);
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
    refreshAgenda();
  }
}

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
    const data = await api("POST", "/api/sessions", { timezone, nom: $("nom").value });
    sessionId = data.session_id;
    $("start").hidden = true;
    $("app").hidden = false;
    addItem("msg bot", "Bonjour ! Je peux prendre, déplacer ou annuler un rendez-vous. Que puis-je faire pour vous ?");
    refreshAgenda();
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

$("show-tools").addEventListener("change", (event) => {
  document.body.classList.toggle("show-tools", event.target.checked);
});
