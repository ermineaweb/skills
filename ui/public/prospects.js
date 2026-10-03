"use strict";

// Panneau des prospects : affiche le dernier résultat du skill
// prospect-research accepté par enregistrer_prospects
// (GET /api/sessions/{id}/prospects), jamais la réponse écrite du modèle.
// Indépendant du chat : app.js appelle seulement refresh().

// load() renvoie {"resultat": {…} | null}.
function createProspectsPanel(load) {
  const $ = (id) => document.getElementById(id);
  const body = $("prospects-body");
  const errorBox = $("prospects-error");

  const STATUS = { complete: "Recherche complète", partial: "Recherche partielle", needs_input: "Informations manquantes" };
  const MATCH = { match: "Correspond", probable: "Probable" };
  const CONFIDENCE = { high: "Confiance élevée", medium: "Confiance moyenne", low: "Confiance faible" };
  const FACT_NOTE = { reported: "source unique", inferred: "déduit", conflicting: "sources contradictoires" };
  const SIGNAL = {
    hiring: "Recrutement", funding: "Levée de fonds", expansion: "Expansion", new_location: "Nouvelle implantation",
    product_launch: "Lancement produit", acquisition: "Acquisition", leadership_change: "Changement de direction",
    growth: "Croissance", technology_change: "Changement technologique", partnership: "Partenariat", other: "Autre",
  };

  function el(tag, className, text) {
    const e = document.createElement(tag);
    if (className) e.className = className;
    if (text !== undefined && text !== null) e.textContent = text;
    return e;
  }

  // Lien seulement pour une URL http(s) ; sinon texte brut.
  function link(url, text) {
    if (!/^https?:\/\//i.test(url || "")) return el("span", null, text || url);
    const a = el("a", null, text || url);
    a.href = url;
    a.target = "_blank";
    a.rel = "noopener noreferrer";
    return a;
  }

  function list(className, items, render) {
    const ul = el("ul", className);
    for (const item of items) ul.append(render(item));
    return ul;
  }

  function details(label, content) {
    const d = el("details");
    d.append(el("summary", null, label), content);
    return d;
  }

  // Valeur d'un fait (suivie de unit), avec la qualité de l'information si
  // elle n'est pas vérifiée.
  function fact(f, unit = "") {
    if (!f || f.status === "unknown" || (f.value == null && f.min == null && f.max == null)) {
      if (f && f.status === "conflicting" && f.alternatives) {
        return `contradictoire (${f.alternatives.map((a) => a.value).join(" / ")})`;
      }
      return null;
    }
    let v = f.value;
    if (v == null) v = f.min === f.max ? `${f.min}` : `${f.min ?? "?"}–${f.max ?? "?"}`;
    v += unit;
    const notes = [FACT_NOTE[f.status], f.as_of && `donnée ${f.as_of}`].filter(Boolean);
    return notes.length ? `${v} (${notes.join(", ")})` : v;
  }

  function badge(text, kind) {
    return el("span", `badge ${kind}`, text);
  }

  function renderProspect(p, criteria) {
    const c = p.company || {};
    const li = el("li", "prospect");

    const head = el("div", "prospect-head");
    const title = el("div");
    title.append(el("strong", null, c.name), el("span", "muted small", ` ${c.domain || p.id}`));
    const badges = el("div", "badges");
    badges.append(
      badge(MATCH[p.icp_match?.status] || p.icp_match?.status, `match-${p.icp_match?.status}`),
      badge(CONFIDENCE[p.confidence] || p.confidence, `conf-${p.confidence}`),
    );
    head.append(title, badges);
    li.append(head);

    const desc = fact(c.description);
    if (desc) li.append(el("p", "small", desc));

    const facts = [
      ["Secteur", fact(c.industry)],
      ["Modèle", fact(c.business_model)],
      ["Siège", [fact(c.headquarters), fact(c.country)].filter(Boolean).join(", ") || null],
      ["Effectif", fact(c.employees, " salariés") || "inconnu"],
      ["Société mère", fact(c.parent_company)],
    ].filter(([, v]) => v);
    const dl = el("dl", "facts small");
    for (const [k, v] of facts) dl.append(el("dt", null, k), el("dd", null, v));
    li.append(dl);

    const describe = (id) => criteria[id] || id;
    const unverified = p.icp_match?.unverified_criteria || [];
    const failed = p.icp_match?.failed_criteria || [];
    if (unverified.length) li.append(el("p", "small warn", `Non vérifié : ${unverified.map(describe).join(" ; ")}`));
    if (failed.length) li.append(el("p", "small muted", `Non satisfait (préférence) : ${failed.map(describe).join(" ; ")}`));

    if (p.signals?.length) {
      li.append(list("signals small", p.signals, (s) => {
        const item = el("li");
        item.append(el("span", "signal-type", SIGNAL[s.type] || s.type));
        if (s.date) item.append(el("span", "muted", ` ${s.date}`));
        item.append(el("span", null, ` — ${s.description}`));
        return item;
      }));
    } else {
      li.append(el("p", "small muted", "Aucun signal trouvé pendant cette recherche."));
    }

    if (p.to_verify?.length) li.append(el("p", "small muted", `À vérifier : ${p.to_verify.join(" ; ")}`));

    const sources = list("sources small", p.sources || [], (s) => {
      const item = el("li");
      item.append(link(s.url, s.title || s.url), el("span", "muted", ` · ${s.type === "primary" ? "primaire" : "secondaire"}`));
      return item;
    });
    li.append(details(`Sources (${(p.sources || []).length}) — ${p.confidence_reason || ""}`, sources));
    return li;
  }

  function render(r) {
    body.replaceChildren();
    if (!r) {
      body.append(el("p", "muted", "Aucun résultat pour l'instant. Demandez une recherche de prospects dans la conversation."));
      return;
    }
    const criteria = Object.fromEntries((r.request?.criteria || []).map((c) => [c.id, c.description]));
    const n = (r.prospects || []).length;
    const x = (r.excluded || []).length;
    body.append(el("p", "small", `${STATUS[r.status] || r.status} · ${n} prospect${n > 1 ? "s" : ""} · ${x} exclu${x > 1 ? "s" : ""}`));
    if (r.request?.summary) body.append(el("p", "small muted", r.request.summary));
    if (r.warnings?.length) body.append(list("small warn", r.warnings, (w) => el("li", null, w)));
    if (r.request?.assumptions?.length) {
      body.append(details("Hypothèses appliquées", list("small", r.request.assumptions, (a) => el("li", null, a))));
    }
    if (n) body.append(list("prospect-list", r.prospects, (p) => renderProspect(p, criteria)));
    if (x) {
      body.append(details(`Exclus (${x})`, list("small", r.excluded, (e) => {
        const item = el("li");
        item.append(el("strong", null, e.name), el("span", "muted", ` ${e.domain || ""} — ${e.reason}`));
        return item;
      })));
    }
    const log = r.search_log || {};
    if (log.queries?.length || log.unreachable_urls?.length) {
      const content = el("div", "small");
      if (log.queries?.length) content.append(el("p", null, `Requêtes : ${log.queries.join(" · ")}`));
      if (log.unreachable_urls?.length) content.append(el("p", "muted", `Pages inaccessibles : ${log.unreachable_urls.join(" · ")}`));
      body.append(details("Journal de recherche", content));
    }
  }

  async function refresh() {
    try {
      const data = await load();
      errorBox.hidden = true;
      render(data.resultat);
    } catch {
      errorBox.textContent = "Résultats momentanément indisponibles.";
      errorBox.hidden = false;
    }
  }

  return { refresh };
}
