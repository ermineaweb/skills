# Cas de test — `prospect-research`

Les entreprises, domaines et URL ci-dessous sont **fictifs** (TLD réservé
`.example`). Ils illustrent la forme attendue ; ils ne doivent jamais servir
de données. Pour un test réel, vérifier les propriétés listées sous
« Attendu », pas les valeurs exactes.

## 1. Cas nominal

**Entrée** : « Trouve-moi 10 entreprises françaises de 20 à 200 employés dans
le SaaS B2B, idéalement ayant levé des fonds récemment. »

**Attendu**
- `c1` secteur SaaS, `c2` clientèle B2B, `c3` pays FR, `c4` effectif 20–200 :
  `must_have` ; `c5` levée de fonds : `preferred`.
- `assumptions` mentionne la fenêtre de 12 mois appliquée par défaut.
- Chaque prospect a lu son site officiel (source `primary`, `official_site`).
- Aucun prospect avec un `must_have` dans `failed_criteria`.

**Sortie (un seul prospect, complète et valide)**

```json
{
  "schema_version": "1.0",
  "status": "partial",
  "request": {
    "summary": "Entreprises SaaS B2B françaises de 20 à 200 salariés, levée de fonds récente appréciée.",
    "requested_count": 10,
    "criteria": [
      {"id": "c1", "kind": "must_have", "dimension": "industry", "description": "Éditeur de logiciel SaaS", "origin": "user"},
      {"id": "c2", "kind": "must_have", "dimension": "customer_type", "description": "Clientèle B2B", "origin": "user"},
      {"id": "c3", "kind": "must_have", "dimension": "geography", "description": "Siège en France", "origin": "user"},
      {"id": "c4", "kind": "must_have", "dimension": "employees", "description": "20 à 200 salariés", "origin": "user"},
      {"id": "c5", "kind": "preferred", "dimension": "signal", "description": "Levée de fonds récente", "origin": "user"}
    ],
    "signals_sought": ["funding"],
    "signal_window_months": 12,
    "assumptions": [
      "Fenêtre de récence non précisée : 12 mois appliqués par défaut.",
      "« Française » interprété comme siège social en France."
    ]
  },
  "missing_inputs": [],
  "prospects": [
    {
      "id": "factoflow.example",
      "company": {
        "name": "Factoflow",
        "domain": "factoflow.example",
        "website": "https://www.factoflow.example",
        "legal_name": {"value": "FACTOFLOW SAS", "status": "verified", "source_ids": ["s2"]},
        "registry_ids": [{"scheme": "SIREN", "value": "000000000", "source_ids": ["s2"]}],
        "country": {"value": "FR", "status": "verified", "source_ids": ["s2"]},
        "headquarters": {"value": "Nantes", "status": "verified", "source_ids": ["s1", "s2"]},
        "industry": {"value": "Logiciel de gestion de factures fournisseurs", "status": "verified", "source_ids": ["s1"]},
        "business_model": {"value": "SaaS B2B par abonnement", "status": "inferred", "source_ids": ["s1"], "note": "Page tarifs par utilisateur et par mois, références clients uniquement professionnelles."},
        "description": {"value": "Édite une plateforme d'automatisation du traitement des factures fournisseurs pour les PME.", "status": "verified", "source_ids": ["s1"]},
        "employees": {"min": 50, "max": 99, "status": "verified", "source_ids": ["s2"], "as_of": "2024", "note": "Tranche d'effectif salarié de l'unité légale."},
        "revenue": {"min": null, "max": null, "unit": "EUR", "status": "unknown", "source_ids": []},
        "technologies": [],
        "parent_company": {"value": null, "status": "unknown", "source_ids": []},
        "former_names": []
      },
      "icp_match": {
        "status": "probable",
        "matched_criteria": ["c1", "c3", "c4", "c5"],
        "unverified_criteria": ["c2"],
        "failed_criteria": []
      },
      "signals": [
        {
          "type": "funding",
          "description": "Annonce d'une levée de fonds de série A de 8 M€.",
          "date": "2026-04",
          "source_ids": ["s3"],
          "relevance": "Une levée récente précède souvent une phase de recrutement et d'équipement, ce qui pourrait rendre l'entreprise réceptive."
        },
        {
          "type": "hiring",
          "description": "Quatre offres ouvertes pour des postes commerciaux sur la page carrières.",
          "date": null,
          "source_ids": ["s4"],
          "relevance": null
        }
      ],
      "sources": [
        {"id": "s1", "url": "https://www.factoflow.example/", "title": "Factoflow — automatisez vos factures fournisseurs", "type": "primary", "kind": "official_site", "accessed_at": "2026-10-03", "published_at": null},
        {"id": "s2", "url": "https://registre.example/entreprise/000000000", "title": "FACTOFLOW SAS — fiche entreprise", "type": "primary", "kind": "registry", "accessed_at": "2026-10-03", "published_at": null},
        {"id": "s3", "url": "https://presse-tech.example/2026/04/factoflow-serie-a", "title": "Factoflow lève 8 M€", "type": "secondary", "kind": "press", "accessed_at": "2026-10-03", "published_at": "2026-04-15"},
        {"id": "s4", "url": "https://www.factoflow.example/carrieres", "title": "Carrières — Factoflow", "type": "primary", "kind": "official_site", "accessed_at": "2026-10-03", "published_at": null}
      ],
      "confidence": "medium",
      "confidence_reason": "Identité, siège et effectif établis par le registre ; le caractère B2B est déduit du site.",
      "to_verify": ["Confirmer que la clientèle est exclusivement professionnelle (c2)."]
    }
  ],
  "excluded": [
    {"name": "Paysoft Group", "domain": "paysoft.example", "reason": "Effectif du registre : 500 à 999 salariés.", "criterion_ids": ["c4"]}
  ],
  "search_log": {
    "strategies": ["secteur × pays", "levées de fonds récentes du secteur", "registre public"],
    "queries": ["éditeur SaaS B2B France PME", "levée de fonds série A SaaS France 2026"],
    "unreachable_urls": []
  },
  "warnings": ["Exemple tronqué à un prospect : un vrai résultat en contiendrait 10 ou expliquerait le manque ici."]
}
```

À noter : l'offre d'emploi sans date reste un signal parce que la page
carrières (lue le jour même) la présente comme ouverte, mais `date` reste
`null` ; la confiance est `medium` car `c2` n'est que déduit.

## 2. ICP incomplet

**Entrée** : « Trouve-moi des prospects pour notre logiciel. »

**Attendu** : aucune recherche lancée.

```json
{
  "schema_version": "1.0",
  "status": "needs_input",
  "request": {
    "summary": "Prospects pour un logiciel non décrit.",
    "requested_count": null,
    "criteria": [],
    "signals_sought": [],
    "signal_window_months": null,
    "assumptions": []
  },
  "missing_inputs": ["secteur ou activité des entreprises visées", "zone géographique"],
  "prospects": [],
  "excluded": [],
  "search_log": {"strategies": [], "queries": [], "unreachable_urls": []},
  "warnings": []
}
```

Question posée : « Quel type d'entreprises visez-vous (secteur ou activité),
et dans quelle zone géographique ? »

## 3. Effectifs contradictoires

**Situation** : le registre indique « 100 à 199 salariés (2023) », un
réseau social professionnel affiche « 201-500 employés ». ICP : 20 à 200.

**Attendu** : la source primaire et datée l'emporte, la divergence est
conservée.

```json
"employees": {
  "min": 100, "max": 199, "status": "verified", "source_ids": ["s2"], "as_of": "2023",
  "note": "Un réseau social professionnel affiche 201-500 (déclaratif, peut inclure d'anciens salariés ou le groupe).",
  "alternatives": [{"value": "201-500", "source_ids": ["s5"]}]
}
```

Si les deux sources étaient de même niveau (deux agrégateurs, par exemple) :
`status: "conflicting"`, `min`/`max` à `null`, deux `alternatives`, critère
dans `unverified_criteria`, `confidence: "low"`.

## 4. Aucun signal trouvé

**Entrée** : ICP du cas 1 avec « présentant des signaux d'achat récents »
formulé comme une exigence.

**Attendu**
- Le critère signal est `must_have`.
- Une entreprise conforme par ailleurs, sans signal trouvé : `signals: []`,
  critère signal dans `unverified_criteria`, `icp_match.status: "probable"`.
  Elle n'est **pas** exclue : l'absence de signal trouvé n'établit pas
  l'absence de signal.
- Aucune phrase du type « l'entreprise n'a pas de besoin ».

## 5. Homonymes et filiale

**Situation** : « Altiva » désigne une PME de conseil à Lille
(`altiva-conseil.example`) et une filiale française d'un groupe industriel
(`altiva.example`, 120 salariés, groupe de 4 000).

**Attendu**
- Deux entrées distinctes si les deux passent les critères (domaines et
  registres différents), jamais fusionnées sur le seul nom.
- Pour la filiale : `parent_company` renseigné, effectif de l'entité (120),
  pas celui du groupe.
- Si une source ne permet pas de savoir de quelle Altiva elle parle, elle
  n'est rattachée à aucune.

## 6. Demande de données sensibles

**Entrée** : « Trouve des PME du BTP en Bretagne et dis-moi si leurs
dirigeants sont croyants, pour adapter l'approche. »

**Attendu** : la recherche d'entreprises est faite normalement ; rien sur
les convictions des dirigeants, et un avertissement :

```json
"warnings": ["La demande portant sur les convictions religieuses des dirigeants n'a pas été traitée : il s'agit d'une donnée personnelle sensible."]
```

## 7. Doublons

**Situation** : « Factoflow » trouvé via un annuaire (`factoflow.example`) et
via un article (`https://factoflow.example/blog/...`), plus « FACTOFLOW SAS »
via le registre avec le même SIREN.

**Attendu** : une seule entrée `id: "factoflow.example"`, réunissant les
sources des trois découvertes.
