# skills : agent IA modulaire, indépendant du modèle

Socle Go pour des agents conversationnels composés de **skills** :

- [`prise-de-rendez-vous`](skills/prise-de-rendez-vous/SKILL.md) : rechercher
  des disponibilités, réserver, déplacer, annuler ;
- [`prospect-research`](skills/prospect-research/SKILL.md) : rechercher des
  entreprises correspondant à un profil de client (ICP), avec sources et
  niveau de confiance. Pour l'instant sur un moteur de recherche simulé
  (entreprises fictives, aucun accès à Internet).

- **Indépendant du modèle** : le runtime ne connaît que l'interface
  `model.Adapter`. Un adapter compatible OpenAI est fourni (OpenAI, Ollama,
  vLLM, Mistral…) ; d'autres fournisseurs se branchent sans toucher aux skills.
- **Le calendrier est la source de vérité** : le modèle ne peut ni inventer un
  créneau ou un identifiant, ni considérer une opération comme réussie sans
  confirmation. Ces règles sont **appliquées par le code**, pas seulement
  écrites dans le prompt.
- **Aucune dépendance externe** : bibliothèque standard Go uniquement.
- **Go via Docker** : aucune installation locale de Go n'est nécessaire.

## Démarrage

Prérequis : Docker avec Compose v2.24 ou plus récent.

```bash
./run.sh          # démarre la stack puis affiche l'URL (http://localhost:8080) et les skills chargés
./run.sh logs     # suit les logs
./run.sh stop     # arrête la stack (le modèle téléchargé est conservé)
```

La stack ([compose.yaml](compose.yaml)) :

```
navigateur ─► ui (Caddy) ─► /api/* ─► api (Go, skills) ─► modèle (API en ligne, ou Ollama local)
```

| Service | Rôle |
|---|---|
| `ui` | Caddy : sert `ui/public/` et relaie `/api/*` vers `api` |
| `api` | API JSON Go : runtime + skills choisis par la configuration (non exposée sur l'hôte) |
| `ollama` | serveur de modèles local, API compatible OpenAI (non exposé sur l'hôte) ; mode local uniquement |
| `ollama-pull` | télécharge le modèle au premier lancement, puis s'arrête ; mode local uniquement |

Modèle par défaut : **`qwen3:4b-instruct`** (Qwen3 4B Instruct 2507, environ
2,5 Go, exécutable sur CPU avec 8 Go de RAM). C'est la variante sans
« réflexion » : les modèles à réflexion génèrent des centaines de tokens avant
chaque réponse, ce qui est prohibitif sans GPU.
Sans GPU, compter une à deux minutes pour la première réponse (chargement du
modèle), puis quelques dizaines de secondes par message. Pour changer de
modèle ou de réglages : `cp .env.example .env` puis modifier `MODEL`, etc.

Configuration de l'application et des skills : [app.env](app.env) contient
les valeurs par défaut (skills enregistrés, skills actifs d'office, réglages
propres à chaque skill) ; `.env`, facultatif, les surcharge. Toutes ces
variables sont transmises à l'API : ni `compose.yaml` ni `run.sh` ne
connaissent les skills, et en ajouter un ne les modifie pas.

### Modèle en ligne gratuit (Gemini, Groq, OpenRouter…)

Pour éviter de faire tourner un modèle sur la machine, utiliser une API en
ligne compatible OpenAI. Exemple avec Gemini (clé gratuite sur
<https://aistudio.google.com/apikey>), dans `.env` :

```bash
MODEL_BASE_URL=https://generativelanguage.googleapis.com/v1beta/openai
MODEL_API_KEY=<votre clé>
MODEL=gemini-3.5-flash-lite
MODEL_WARMUP=false
MODEL_TIMEOUT=2m
```

Puis `./run.sh` : Ollama n'est alors ni téléchargé ni lancé. Les quotas
gratuits sont faibles (quelques requêtes par minute) et un message peut
déclencher plusieurs appels au modèle : en cas d'erreur HTTP 429 ou 503,
attendre ou changer de modèle (`gemini-3.8-flash`, plus capable). Google retire
régulièrement les anciens modèles (erreur 404 « no longer available »). Autres fournisseurs : voir
[.env.example](.env.example).

L'interface (HTML/CSS/JS sans framework) lit le fuseau horaire du navigateur.
Elle affiche à part les opérations confirmées par le calendrier et la liste
« Mes rendez-vous », lue dans l'agenda et non dans la réponse du modèle. Une
case permet d'afficher les appels d'outils.

Pour les démonstrations, un **agenda visuel** (vue semaine) montre à côté du
chat les plages d'ouverture de chaque professionnel, les créneaux réservés
(ceux des autres clients restent anonymes) et ceux de l'utilisateur. Il se met
à jour après chaque message et se place sur la semaine du dernier rendez-vous
réservé ou déplacé, qu'il met en évidence. Il est indépendant des skills : il
lit le calendrier via `calendar.Viewer` et la route
`GET /api/sessions/{id}/calendrier?date=AAAA-MM-JJ`, fournie par
[api/calendarapi](api/calendarapi/calendarapi.go) quand le skill
`prise-de-rendez-vous` est enregistré (sinon, l'agenda est simplement masqué). La case « Afficher l'agenda » le
masque.

Quand le skill `prospect-research` est enregistré, un panneau **Prospects**
affiche le dernier résultat de recherche : prospects, effectifs, signaux
datés, sources, exclusions et avertissements. Il est lu par
`GET /api/sessions/{id}/prospects` dans le résultat accepté par le tool
`enregistrer_prospects` (structure validée par le schéma de sortie, URL
vérifiées : seules celles renvoyées par `web_search` ou lues par `web_fetch`
sont acceptées), jamais dans la réponse écrite du modèle. Le scénario du
moteur simulé se choisit avec `WEBSEARCH_SCENARIO` (voir
[app.env](app.env)). Ce skill a des instructions longues et
enchaîne de nombreux appels : utiliser un modèle en ligne, et compter
plusieurs minutes avec un quota gratuit.

L'interface n'affiche que ce qui concerne les skills chargés
(`GET /api/skills`) : suggestions, agenda, panneau des prospects.

> Si Docker répond `permission denied` alors que vous êtes dans le groupe
> `docker`, `./run.sh` se relance automatiquement via `sg docker`.

## Développement

Les tests et outils ne font pas partie de la stack ; ils s'exécutent dans un
conteneur Go jetable :

```bash
GO='docker run --rm -it -v "$PWD":/src -w /src -u "$(id -u):$(id -g)" -e HOME=/tmp -e CGO_ENABLED=0 golang:1.25-alpine'
eval $GO go test ./...                    # tests
eval $GO go vet ./...                     # analyse statique
eval $GO gofmt -w .                       # formatage
eval $GO go run ./cmd/demo -tz Europe/Paris -now 2026-09-30T10:00:00+02:00   # démo scriptée, sans LLM
```

## Arborescence

```
.
├── agent/                       # Runtime : boucle modèle ↔ tools, sessions, activation des skills
│   ├── runtime.go
│   └── session.go
├── model/
│   ├── adapter.go               # interface Adapter + Message/ToolCall neutres
│   ├── openai/adapter.go        # adapter Chat Completions (HTTP, sans SDK)
│   └── scripted/scripted.go     # modèle déterministe pour tests et démo
├── skills/
│   ├── prise-de-rendez-vous/
│   │   ├── SKILL.md             # métadonnées (lues par le code) + documentation
│   │   ├── instructions.md      # consignes données au modèle à l'activation
│   │   ├── schemas/*.json       # définitions des 7 tools (JSON Schema)
│   │   └── skill.go             # assemblage instructions + schémas + handlers
│   └── prospect-research/
│       ├── SKILL.md             # métadonnées + consignes du modèle (indépendantes de l'application)
│       ├── hote.md              # consignes propres à cette application (outils, remise du résultat)
│       ├── references/          # schéma de sortie, cas de test
│       ├── schemas/*.json       # web_search, web_fetch, enregistrer_prospects
│       └── skill.go
├── tools/
│   ├── calendar/                # handlers : recherche, réservation, modification, annulation, listes
│   ├── datetime/                # handler interpreter_date
│   ├── web/                     # handlers web_search, web_fetch + registre des URL vues
│   └── prospects/               # handler enregistrer_prospects
├── services/
│   ├── calendar/
│   │   ├── provider.go          # interface Provider (contrat métier)
│   │   ├── mock.go              # MockProvider en mémoire + injection de pannes
│   │   └── demo.go              # jeu de données de démonstration
│   └── websearch/               # contrats SearchEngine / WebFetcher (skill prospect-research)
│       └── websearchtest/       # mocks déterministes + univers fictif (testdata/prospect-research)
├── types/                       # Appointment, TimeSlot, Tool, Skill, codes d'erreur
├── jsonschema/                  # validateur JSON Schema (sous-ensemble)
├── datetime/                    # expressions françaises → intervalles ISO 8601
├── app/                         # assemblage : skills choisis par la configuration (SKILLS, ACTIVE_SKILLS)
├── api/                         # API JSON (Go) au-dessus du runtime, sans notion de skill
│   ├── calendarapi/             # routes d'agenda de l'interface (rendez-vous, vue semaine)
│   └── prospectsapi/            # route du dernier résultat de prospect-research
├── ui/
│   ├── Caddyfile                # fichiers statiques + reverse proxy /api/* → api:8080
│   └── public/                  # index.html, app.js, calendar.js (agenda), prospects.js, style.css
├── cmd/api, cmd/chat, cmd/demo  # points d'entrée
├── Dockerfile, compose.yaml     # image de l'API et stack
├── run.sh                       # lancement de la stack
├── app.env                      # configuration par défaut de l'application et des skills
├── .env.example                 # surcharges facultatives (modèle, skills)
└── docs/GUIDE.md                # architecture, extension, tests
```

## Documentation

- [docs/GUIDE.md](docs/GUIDE.md) : séparation Model ≠ Runtime ≠ Skill ≠ Tool ≠
  Service, chargement du skill, enregistrement des tools, `CalendarProvider`,
  ajout d'un calendrier, d'un modèle ou d'un skill, tests, garde-fous.
- [skills/prise-de-rendez-vous/SKILL.md](skills/prise-de-rendez-vous/SKILL.md) :
  rôle, activation, informations nécessaires, règles métier, ambiguïtés,
  absence de disponibilité, confirmation, erreurs.
