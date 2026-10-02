# skills : agent IA modulaire, indépendant du modèle

Socle Go pour des agents conversationnels composés de **skills**. Premier
skill : [`prise-de-rendez-vous`](skills/prise-de-rendez-vous/SKILL.md)
(rechercher des disponibilités, réserver, déplacer, annuler).

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

Prérequis : Docker avec Compose v2.20 ou plus récent.

```bash
./run.sh          # démarre la stack puis affiche l'URL (http://localhost:8080)
./run.sh logs     # suit les logs
./run.sh stop     # arrête la stack (le modèle téléchargé est conservé)
```

La stack ([compose.yaml](compose.yaml)) :

```
navigateur ─► ui (Caddy) ─► /api/* ─► api (Go) ─► modèle (API en ligne, ou Ollama local)
```

| Service | Rôle |
|---|---|
| `ui` | Caddy : sert `ui/public/` et relaie `/api/*` vers `api` |
| `api` | API JSON Go : runtime + skill + calendrier mock (non exposée sur l'hôte) |
| `ollama` | serveur de modèles local, API compatible OpenAI (non exposé sur l'hôte) ; mode local uniquement |
| `ollama-pull` | télécharge le modèle au premier lancement, puis s'arrête ; mode local uniquement |

Modèle par défaut : **`qwen3:4b-instruct`** (Qwen3 4B Instruct 2507, environ
2,5 Go, exécutable sur CPU avec 8 Go de RAM). C'est la variante sans
« réflexion » : les modèles à réflexion génèrent des centaines de tokens avant
chaque réponse, ce qui est prohibitif sans GPU.
Sans GPU, compter une à deux minutes pour la première réponse (chargement du
modèle), puis quelques dizaines de secondes par message. Pour changer de
modèle ou de réglages : `cp .env.example .env` puis modifier `MODEL`, etc.

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
│   └── prise-de-rendez-vous/
│       ├── SKILL.md             # métadonnées (lues par le code) + documentation
│       ├── instructions.md      # consignes données au modèle à l'activation
│       ├── schemas/*.json       # définitions des 7 tools (JSON Schema)
│       └── skill.go             # assemblage instructions + schémas + handlers
├── tools/
│   ├── calendar/                # handlers : recherche, réservation, modification, annulation, listes
│   └── datetime/                # handler interpreter_date
├── services/
│   └── calendar/
│       ├── provider.go          # interface Provider (contrat métier)
│       ├── mock.go              # MockProvider en mémoire + injection de pannes
│       └── demo.go              # jeu de données de démonstration
├── types/                       # Appointment, TimeSlot, Tool, Skill, codes d'erreur
├── jsonschema/                  # validateur JSON Schema (sous-ensemble)
├── datetime/                    # expressions françaises → intervalles ISO 8601
├── api/                         # API JSON (Go) au-dessus du runtime
├── ui/
│   ├── Caddyfile                # fichiers statiques + reverse proxy /api/* → api:8080
│   └── public/                  # index.html, app.js, style.css
├── cmd/api, cmd/chat, cmd/demo  # points d'entrée
├── Dockerfile, compose.yaml     # image de l'API et stack
├── run.sh                       # lancement de la stack
├── .env.example                 # configuration facultative
└── docs/GUIDE.md                # architecture, extension, tests
```

## Documentation

- [docs/GUIDE.md](docs/GUIDE.md) : séparation Model ≠ Runtime ≠ Skill ≠ Tool ≠
  Service, chargement du skill, enregistrement des tools, `CalendarProvider`,
  ajout d'un calendrier, d'un modèle ou d'un skill, tests, garde-fous.
- [skills/prise-de-rendez-vous/SKILL.md](skills/prise-de-rendez-vous/SKILL.md) :
  rôle, activation, informations nécessaires, règles métier, ambiguïtés,
  absence de disponibilité, confirmation, erreurs.
