# Fixtures `prospect-research`

Univers **fictif** servi par `MockSearchEngine` et `MockWebFetcher`
(package `websearchtest`). Tous les domaines sont en `.test` (TLD réservé) :
aucune entreprise, URL ou donnée réelle. Rien ne dépend de la date du jour ;
les dates écrites dans les pages sont fixes (fin septembre 2026).

```
search/      pages de résultats, choisies par mots-clés
pages/       index.json (URL → fichier, redirection ou panne) + pages HTML
scenarios/   scénarios : recherches utilisées, surcharges, attendus
```

## Entreprises

| Domaine | Profil | Cas testé |
|---|---|---|
| `acme-saas.test` | SaaS B2B, Lyon, 85 pers., recrute 3 commerciaux | prospect idéal, recrutement, plusieurs noms (ACME Software, ACME SAS, alias `acmesoftware.test`) |
| `nuvio.test` | SaaS B2B, Paris, 140 pers. | plusieurs signaux : recrutement, expansion, lancement, partenariat, levée |
| `sendara.test` | SaaS B2B, Lille, 35 pers., recrute | recrutement |
| `calmeo.test` | SaaS B2B, Grenoble, 60 pers. | conforme, aucun signal |
| `ombrelle.test` | SaaS B2B, Nantes | effectif inconnu |
| `kalista.test` | SaaS B2B, Toulouse | contradiction : 120 (site) / 250 (annuaire) |
| `logitrame.test` | SaaS B2B, Rennes | informations anciennes (2018-2019) |
| `zenith-analytics.test` | SaaS B2B d'après le snippet | site en erreur 500, carrières en 404 |
| `ferrolux.test` | industrie, 150 pers. | mauvais secteur |
| `tinyform.test` | SaaS B2B, 5 pers. | trop petite |
| `hexacorp.test` | SaaS B2B, 5 000 pers. | trop grande |
| `datenwerk.test` | SaaS B2B, Munich, 100 pers. | hors zone |
| `cloudnova.test` | ESN (le snippet dit « plateforme SaaS B2B ») | snippet trompeur |
| `fitloop.test` | SaaS B2C, Montpellier, 70 pers. | faux positif : échoue seulement sur B2B |
| `brightpay-fr.test` | filiale française (110 pers.) de `brightpay.test` (3 000) | filiale |
| `altiva.test` / `altiva-conseil.test` | SaaS / cabinet de conseil, Lille | homonymes |

Sources secondaires : `annuaire-pro.test` (annuaire déclaratif),
`registre-entreprises.test` (registre), `tech-news.test` (presse).

## Sélection des résultats de recherche

Une entrée (`search/*.json` ou champ `search` d'un scénario) :

```json
{"keywords": ["saas", "france|francaise"], "priority": 0, "results": [...]}
```

1. Requête et mots-clés sont comparés en minuscules, sans accents, mot par
   mot. `a|b` : l'une des variantes suffit.
2. Une entrée correspond si **tous** ses mots-clés sont dans la requête.
3. Parmi les entrées qui correspondent : `priority` la plus haute, puis le
   plus de mots-clés, puis la première chargée (entrées du scénario, puis
   `search_files` dans l'ordre).
4. Aucune entrée ne correspond : liste vide, sans erreur.

Priorités utilisées : 0 pour les listes sectorielles, 1 pour les signaux et
les noms d'entreprise, 5 et plus pour les surcharges des scénarios.

`"error": "…"` simule une panne du moteur ; `"results": []` une recherche
sans résultat ; `"malformed": true` sur un résultat le renvoie tel quel
malgré une URL ou un titre invalides.

## Pages

`pages/index.json` liste chaque URL avec **un** comportement :

```json
{"url": "https://acme-saas.test/", "file": "acme-home.html"}
{"url": "https://acmesoftware.test/", "redirect_to": "https://acme-saas.test/"}
{"url": "https://zenith-analytics.test/", "status": 500}
{"url": "https://nuvio.test/", "error": "connection timed out (simulated)"}
```

Une URL non déclarée répond 404 si son domaine existe, sinon elle est
injoignable. Schéma, `www.`, casse de l'hôte, fragment et `/` final sont
ignorés.

## Ajouter un scénario

Créer `scenarios/<nom>.json` :

```json
{
  "name": "<nom>",
  "description": "…",
  "icp": "demande envoyée à l'agent",
  "search_files": ["companies.json", "saas-france.json", "signals.json"],
  "search": [],
  "pages": [],
  "expect": {"prospects": [], "excluded": [], "checks": []}
}
```

`pages` remplace les pages de même URL de l'index pour ce scénario
seulement. `expect` n'est pas utilisé par les mocks : il décrit ce que le
skill doit produire. Les champs inconnus, fichiers absents, URL invalides et
attendus sur un domaine inexistant font échouer le chargement.
