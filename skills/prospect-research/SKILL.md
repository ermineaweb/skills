---
name: prospect-research
description: Rechercher et documenter des entreprises correspondant à un profil de client idéal (ICP), avec sources, niveau de confiance et signaux commerciaux récents. À activer quand l'utilisateur demande de trouver, lister ou identifier des prospects, des entreprises cibles ou des comptes correspondant à des critères (secteur, taille, zone, technologies, signaux d'achat).
---

# Skill `prospect-research`

Tu recherches des **entreprises** qui correspondent à un ICP (Ideal Customer
Profile) et tu les documentes de façon vérifiable. Tu ne qualifies pas en
profondeur, tu ne contactes personne et tu n'évalues pas la probabilité
d'achat : ton résultat sert d'entrée à d'autres skills (qualification,
prise de contact).

Le résultat est un objet JSON conforme à
[references/output-schema.json](references/output-schema.json).
Des cas de test sont dans [references/examples.md](references/examples.md).

## Règles non négociables

1. **Rien d'inventé.** Chaque valeur factuelle (taille, chiffre d'affaires,
   technologies, dirigeants, implantation, signaux…) provient d'une source que
   tu as réellement consultée pendant cette recherche. Sinon, elle est
   `unknown`.
2. **Inconnu ≠ négatif.** Une information introuvable vaut `null` avec le
   statut `unknown`, jamais `0`, `false` ou « non ». `signals: []` signifie
   « aucun signal trouvé pendant cette recherche », pas « aucun besoin ».
3. **Signal ≠ intention.** Tu décris des faits observés (« recrute trois
   développeurs data depuis juin 2026 ») et tu peux dire en quoi ils sont
   *potentiellement* pertinents. Tu n'écris jamais qu'une entreprise
   « cherche », « a besoin de » ou « va acheter » une solution.
4. **Aucune URL fabriquée, aucune source fictive.** Une URL figure dans le
   résultat seulement si elle vient d'un résultat d'outil. Une source n'est
   marquée consultée que si son contenu a été lu (page récupérée, ou extrait
   renvoyé par l'outil de recherche). Ne complète jamais une URL de mémoire.
5. **Ta mémoire n'est pas une source.** Tu peux t'en servir pour trouver des
   idées de requêtes ou de candidats, mais une entreprise n'entre dans le
   résultat qu'une fois confirmée par un outil.
6. **Pas de supposition silencieuse.** Toute valeur par défaut que tu
   appliques (nombre de résultats, fenêtre de récence, interprétation d'un
   critère ambigu) est inscrite dans `request.assumptions`.
7. **Uniquement des données professionnelles et publiques.** Voir
   [Éthique](#éthique).

## Outils

Ce skill utilise les capacités de recherche fournies par l'application hôte.
Les noms ci-dessous sont indicatifs : utilise les outils équivalents qui te
sont exposés.

| Capacité | Nom indicatif | Usage |
|---|---|---|
| Recherche web | `web_search` | découvrir des candidats, trouver le site officiel, une fiche de registre, des actualités, des offres d'emploi |
| Lecture de page | `web_fetch` | lire le site officiel, une fiche de registre, un communiqué : c'est ce qui permet de **vérifier** |

- Un extrait renvoyé par la recherche suffit pour repérer un candidat, pas
  pour vérifier un critère obligatoire : lis la page quand c'est possible.
- Si aucun outil de recherche n'est disponible, ne produis aucun prospect :
  renvoie `status: "needs_input"` avec un avertissement.
- Si l'hôte limite le nombre d'appels, arrête-toi proprement et renvoie
  `status: "partial"`.
- Si l'hôte fournit d'autres outils (base d'entreprises, registre, CRM en
  lecture), préfère-les pour les données structurées (identifiants, effectifs)
  et applique les mêmes règles de traçabilité.

## Entrées

- **ICP** : texte libre ou structuré. Seule entrée obligatoire.
- Optionnel : nombre de prospects souhaité, description de l'offre vendue,
  liste de domaines à exclure (clients ou prospects déjà connus), fenêtre de
  récence des signaux.

## Procédure

### 1. Analyser l'ICP

Transforme la demande en critères, chacun avec un identifiant stable
(`c1`, `c2`…) et une catégorie :

| Catégorie (`kind`) | Effet |
|---|---|
| `must_have` | non respecté de façon établie → l'entreprise est exclue |
| `exclusion` | constaté de façon établie → l'entreprise est exclue (ex. « pas d'ESN », « pas de clients existants ») |
| `preferred` | départage les prospects, n'exclut pas |
| `info` | information à collecter si elle est trouvée ; n'influence pas la correspondance |

Dimensions à considérer : secteur, sous-secteur, zone géographique, taille
(effectif), chiffre d'affaires, modèle économique, type de clientèle,
technologies, caractéristiques particulières, exclusions, signaux recherchés.

Classement :
- Ce que l'utilisateur formule comme une condition (« de 20 à 200 employés »,
  « françaises », « B2B ») est `must_have`.
- Ce qu'il formule comme une préférence (« idéalement », « de préférence »,
  « plutôt ») est `preferred`.
- Les signaux d'achat demandés sont `preferred`, sauf si l'utilisateur les
  exige (« uniquement des entreprises qui ont levé des fonds ») : `must_have`.
- En cas de doute entre `must_have` et `preferred`, choisis `preferred` et
  note-le dans `assumptions` : exclure à tort fait perdre un prospect, l'étape
  de qualification pourra resserrer.

Informations bloquantes : il faut au minimum **une activité ou un secteur**
et **une zone géographique**. S'il manque l'un des deux, ne lance pas de
recherche : renvoie `status: "needs_input"`, liste les manques dans
`missing_inputs` et pose une seule question courte. Tout le reste peut
manquer : applique alors ces valeurs par défaut, en les inscrivant dans
`assumptions` :

| Absent | Valeur par défaut |
|---|---|
| nombre de prospects | 10 (maximum 50 par exécution ; au-delà, procéder par lots) |
| fenêtre de récence des signaux | 12 mois avant la date du jour |
| signaux recherchés | aucun ; noter quand même les signaux rencontrés |

« Susceptibles d'avoir besoin de notre solution » sans description de l'offre
n'est pas un critère vérifiable : ne l'invente pas, signale-le dans
`assumptions` et, si l'offre est connue, déduis-en au plus des signaux à
rechercher (`preferred`).

### 2. Générer des candidats

Vise environ deux à trois fois le nombre de prospects demandé : une partie
sera éliminée. Combine plusieurs stratégies, et au moins deux sources de
nature différente :

- secteur × zone (« éditeur logiciel B2B Lyon », « SaaS RH France ») ;
- caractéristiques (technologie utilisée, type de clientèle, certification) ;
- signaux (levées de fonds récentes du secteur, offres d'emploi ciblées,
  ouvertures de bureaux) ;
- listes et répertoires (annuaires sectoriels, membres d'associations
  professionnelles, exposants de salons, palmarès, portefeuilles
  d'investisseurs) ;
- registres publics d'entreprises (ex. en France l'Annuaire des Entreprises /
  base SIRENE ; ailleurs le registre national équivalent).

Varie la langue des requêtes selon la zone. Écarte immédiatement les candidats
manifestement hors cible (grand groupe pour un ICP PME, mauvais pays,
activité sans rapport, domaine figurant dans la liste d'exclusion) sans les
vérifier davantage. Consigne les requêtes et stratégies utilisées dans
`search_log`.

Arrête la génération quand tu as assez de candidats plausibles, ou quand
deux stratégies supplémentaires n'apportent plus de nouvelle entreprise.

### 3. Vérifier chaque candidat retenu

Dans cet ordre, et en t'arrêtant dès qu'un critère `must_have` échoue de
façon établie :

1. **Identité** : trouve et lis le site officiel. Établis le nom, le domaine
   et, si possible, la raison sociale et un identifiant de registre. Vérifie
   qu'il s'agit bien de la même entreprise que celle trouvée à l'étape 2
   (activité, localisation).
2. **Critères `must_have` et `exclusion`** : cherche une preuve pour chacun.
3. **Critères `preferred` et `info`** : si le coût reste raisonnable.
4. **Signaux** (étape 4).

Hiérarchie des sources :

| Type (`type`) | Exemples (`kind`) | Valeur |
|---|---|---|
| `primary` | site officiel, communiqué de l'entreprise, registre public, document légal | fait l'autorité sur l'identité et les faits déclarés par l'entreprise |
| `secondary` | presse, annuaire, agrégateur de données d'entreprise, offre d'emploi sur un site tiers, réseau social professionnel | utile, à recouper ; préciser `kind` |

Une page d'entreprise sur un réseau social professionnel est `secondary` :
les effectifs qu'elle affiche sont déclaratifs et incluent souvent d'anciens
salariés.

Statut de chaque fait (`status`) :

| Statut | Condition |
|---|---|
| `verified` | une source primaire, ou deux sources secondaires indépendantes et concordantes |
| `reported` | une seule source secondaire |
| `inferred` | déduit d'éléments indirects (ex. « B2B » d'après les pages produits) ; expliquer dans `note` |
| `conflicting` | des sources crédibles divergent ; valeurs listées dans `alternatives` |
| `unknown` | rien trouvé ; `value: null` |

Indique `as_of` (date de la donnée) quand la source la donne ; une donnée
datée de plus de 24 mois mérite une `note`.

### 4. Détecter les signaux commerciaux

Cherche les signaux demandés dans l'ICP ; note aussi ceux que tu rencontres
en vérifiant le candidat. Types : `hiring`, `funding`, `expansion`,
`new_location`, `product_launch`, `acquisition`, `leadership_change`,
`growth`, `technology_change`, `partnership`, `other`.

Pour chaque signal :
- une description factuelle (quoi, combien, où) ;
- une date (`AAAA-MM-JJ`, `AAAA-MM` ou `AAAA` selon la précision disponible ;
  `null` si inconnue) — ne la déduis pas de la date de consultation ;
- au moins une source ;
- éventuellement `relevance` : pourquoi il est *potentiellement* pertinent
  pour l'ICP, au conditionnel.

Ne retiens pas un signal plus ancien que la fenêtre de récence, ni un signal
non daté présenté comme récent. Une offre d'emploi sans date de publication
n'est un signal que si la page indique qu'elle est toujours ouverte.

### 5. Dédupliquer

Avant de produire le résultat, fusionne les doublons. Deux entrées désignent
la même entreprise si l'un de ces éléments correspond :
- même identifiant de registre ;
- même domaine principal (après suppression de `www.`, du protocole, du
  chemin ; un domaine qui redirige vers un autre est un alias) ;
- même raison sociale et même localisation.

Le seul nom commercial ne suffit pas (homonymes fréquents). Lors d'une fusion,
conserve toutes les sources et rapproche les faits ; s'ils divergent, passe
le fait en `conflicting`. L'identifiant `id` du prospect est son domaine
normalisé, ou à défaut `registre:<identifiant>`.

### 6. Évaluer la correspondance et la confiance, puis répondre

Pour chaque critère, classe-le dans `matched_criteria`,
`unverified_criteria` ou `failed_criteria` :

- **met** : le fait établi satisfait le critère ;
- **failed** : le fait établi (`verified` ou `reported`) le contredit
  (pour une `exclusion` : le cas exclu est constaté) ;
- **unverified** : fait `unknown`, `inferred`, `conflicting`, ou intervalle
  qui chevauche la limite (effectif « 200-249 » pour « 20 à 200 »).

Les critères `info` ne sont pas classés.

| `icp_match.status` | Condition | Destination |
|---|---|---|
| `match` | tous les `must_have` et `exclusion` sont dans `matched_criteria` | `prospects` |
| `probable` | aucun `must_have`/`exclusion` en échec, au moins un non vérifié | `prospects` |
| `no_match` | au moins un `must_have` ou une `exclusion` en échec | `excluded` |

Un critère `preferred` en échec reste dans `failed_criteria` sans exclure.

`confidence` mesure la **qualité des informations**, jamais la probabilité
d'achat :

| Niveau | Condition |
|---|---|
| `high` | identité établie par le site officiel ; tous les `must_have` vérifiés (`verified`) ; aucun conflit sur un `must_have` |
| `medium` | identité établie ; au moins un `must_have` `reported`, `inferred` ou `unknown`, aucun en conflit |
| `low` | identité incertaine (site officiel non consulté, homonymie non levée), ou un `must_have` `conflicting`, ou sources uniquement peu fiables |

Accompagne-le d'une `confidence_reason` d'une phrase et liste dans
`to_verify` ce que l'étape de qualification devrait contrôler en priorité.

Trie les prospects : `match` avant `probable`, puis nombre de critères
`preferred` satisfaits, puis présence de signaux récents. Ce tri n'est pas
un score commercial.

`status` global : `complete` si le nombre demandé est atteint, `partial`
sinon (budget atteint, peu de candidats, pages inaccessibles) avec la raison
dans `warnings`. Ne complète jamais une liste trop courte avec des
entreprises non vérifiées ou hors cible.

## Cas difficiles

| Situation | Comportement |
|---|---|
| Sources contradictoires | ne choisis pas : `conflicting` + `alternatives`, et le critère reste non vérifié |
| Donnée ancienne | garde-la avec `as_of` ; si une source plus récente existe, elle prime, l'ancienne passe dans `note` |
| Page inaccessible | réessaie une fois, puis cherche une autre source ; ajoute l'URL dans `search_log.unreachable_urls` ; ne cite pas la page comme consultée |
| Information absente | `unknown` ; ne bloque pas le prospect sauf s'il s'agit d'un `must_have` (il reste alors `probable`) |
| Noms similaires | départage par domaine, identifiant de registre, localisation, activité ; si l'ambiguïté persiste, `confidence: low` et explication dans `to_verify` |
| Filiale | la décrire comme une entité distincte ; renseigner `parent_company` ; vérifier les critères au niveau de l'entité, pas du groupe (l'effectif du groupe n'est pas celui de la filiale) |
| Changement de nom | utiliser le nom actuel, les anciens dans `former_names` ; dédupliquer sur les deux |
| Source peu fiable (contenu généré, annuaire non daté, site sans auteur) | au plus `reported`, jamais seule base d'un `verified` |
| Impossible à vérifier | le dire : `unknown` ou `inferred` avec `note`, plutôt qu'une valeur plausible |
| Résultats insuffisants | `status: partial` ; suggérer dans `warnings` quel critère élargir, sans l'élargir toi-même |

## Éthique

- Tu recherches des **entreprises**, pas des individus. N'inclus aucune
  coordonnée personnelle (e-mail, téléphone, adresse personnelle) et ne
  cherche pas à en trouver.
- Un nom de dirigeant n'apparaît que s'il est le sujet d'une information
  professionnelle publique utile (ex. signal `leadership_change` tiré d'un
  communiqué), avec sa fonction et la source.
- N'infère et ne mentionne jamais : opinions politiques, religion, origine
  ethnique, orientation sexuelle, santé, situation familiale, ni aucune autre
  caractéristique personnelle sensible. Si la demande le réclame, refuse
  cette partie et poursuis le reste ; indique-le dans `warnings`.
- Ne contourne pas les restrictions d'accès (connexion, paywall, blocage) :
  une page non accessible est traitée comme inaccessible.

## Format de sortie

Renvoie un unique objet JSON conforme à
[references/output-schema.json](references/output-schema.json). Si l'hôte
attend aussi une réponse lisible, ajoute après un résumé court qui n'apporte
aucune information absente du JSON.

Structure (abrégée) :

```json
{
  "schema_version": "1.0",
  "status": "complete | partial | needs_input",
  "request": {
    "summary": "...",
    "requested_count": 10,
    "criteria": [{"id": "c1", "kind": "must_have", "dimension": "employees", "description": "20 à 200 salariés", "origin": "user"}],
    "signals_sought": ["funding", "hiring"],
    "signal_window_months": 12,
    "assumptions": []
  },
  "missing_inputs": [],
  "prospects": [{
    "id": "exemple.fr",
    "company": {
      "name": "...", "domain": "exemple.fr", "website": "https://...",
      "legal_name": {"value": "...", "status": "verified", "source_ids": ["s2"]},
      "registry_ids": [{"scheme": "SIREN", "value": "...", "source_ids": ["s2"]}],
      "country": {"value": "FR", "status": "verified", "source_ids": ["s2"]},
      "headquarters": {"value": "Lyon", "status": "verified", "source_ids": ["s1"]},
      "industry": {"value": "...", "status": "verified", "source_ids": ["s1"]},
      "business_model": {"value": "SaaS B2B", "status": "inferred", "source_ids": ["s1"], "note": "..."},
      "description": {"value": "...", "status": "verified", "source_ids": ["s1"]},
      "employees": {"min": 50, "max": 99, "status": "verified", "source_ids": ["s2"], "as_of": "2024"},
      "revenue": {"min": null, "max": null, "unit": "EUR", "status": "unknown", "source_ids": []},
      "technologies": [],
      "parent_company": {"value": null, "status": "unknown", "source_ids": []},
      "former_names": []
    },
    "icp_match": {"status": "match", "matched_criteria": ["c1"], "unverified_criteria": [], "failed_criteria": []},
    "signals": [{"type": "funding", "description": "...", "date": "2026-03", "source_ids": ["s3"], "relevance": "..."}],
    "sources": [{"id": "s1", "url": "https://...", "title": "...", "type": "primary", "kind": "official_site", "accessed_at": "2026-10-03", "published_at": null}],
    "confidence": "high",
    "confidence_reason": "...",
    "to_verify": []
  }],
  "excluded": [{"name": "...", "domain": "...", "reason": "...", "criterion_ids": ["c1"]}],
  "search_log": {"strategies": [], "queries": [], "unreachable_urls": []},
  "warnings": []
}
```

Conventions :
- Émets tous les champs de la structure, même inconnus : une valeur inconnue
  vaut `null` (ou `[]` pour une liste), jamais une chaîne vide ni
  `"unknown"` dans `value`. Seuls `as_of`, `note` et `alternatives` peuvent
  être omis quand ils n'apportent rien.
- Les `source_ids` renvoient aux `sources` du même prospect : chaque prospect
  se suffit à lui-même et peut être transmis seul au skill suivant.
- `accessed_at` est la date du jour fournie par l'hôte ; si tu ne la connais
  pas, `null`.
- `country` utilise le code ISO 3166-1 alpha-2 ; les dates suivent
  `AAAA-MM-JJ`, `AAAA-MM` ou `AAAA`.
- `excluded` ne contient que les candidats examinés à l'étape 3, pas chaque
  résultat de recherche écarté d'un coup d'œil.

## Vérification finale

Avant de répondre, contrôle :

- [ ] chaque URL vient d'un résultat d'outil, chaque source a été lue ;
- [ ] chaque valeur non nulle a au moins un `source_id` (sauf `name`) ;
- [ ] aucun `0`, `false` ou « non » ne remplace une information inconnue ;
- [ ] aucun signal n'est formulé comme une intention ou un besoin ;
- [ ] aucune entreprise n'apparaît deux fois (domaine, registre, raison sociale) ;
- [ ] aucun prospect n'a de `must_have` ou d'`exclusion` en échec ;
- [ ] chaque valeur par défaut appliquée figure dans `assumptions` ;
- [ ] aucune donnée personnelle ni caractéristique sensible.
