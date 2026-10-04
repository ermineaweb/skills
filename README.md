# skills : agent IA modulaire, indépendant du modèle

Socle Go pour des agents conversationnels composés de **skills** :

- [`prise-de-rendez-vous`](skills/prise-de-rendez-vous/SKILL.md) : rechercher
  des disponibilités, réserver, déplacer, annuler ;
- [`agenda`](skills/agenda/SKILL.md) : gérer l'agenda personnel de
  l'utilisateur (création, consultation, modification, suppression,
  récurrences, rappels), en langage naturel ;
- [`prospect-research`](skills/prospect-research/SKILL.md) : rechercher des
  entreprises correspondant à un profil de client (ICP), avec sources et
  niveau de confiance, sur le web réel (métamoteur SearXNG auto-hébergé) ;
- [`synthese-vocale`](skills/synthese-vocale/SKILL.md) : lire un texte à voix
  haute (moteur Kokoro local, interchangeable).

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
                                              ├──────────► searxng (recherche web)
                                              └──────────► kokoro (synthèse vocale)
```

| Service | Rôle |
|---|---|
| `ui` | Caddy : sert `ui/public/` et relaie `/api/*` vers `api` |
| `api` | API JSON Go : runtime + skills choisis par la configuration (non exposée sur l'hôte) |
| `searxng` | métamoteur de recherche auto-hébergé (API JSON), utilisé par `prospect-research` ; configuration : [searxng/settings.yml](searxng/settings.yml) (non exposé sur l'hôte) |
| `kokoro` | synthèse vocale locale (Kokoro-FastAPI, CPU), utilisée par `synthese-vocale` (non exposé sur l'hôte) |
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
sont acceptées), jamais dans la réponse écrite du modèle.

Recherche web : `web_search` interroge le service `searxng` (`SEARXNG_URL`,
voir [app.env](app.env)), qui agrège plusieurs moteurs publics sans clé
d'API. Ces moteurs limitent les requêtes automatisées : un moteur peut être
suspendu quelques minutes (`docker compose logs searxng`), les autres
prennent le relais. `web_fetch` lit les pages directement depuis l'API, en
HTTP, et refuse toute adresse non publique (réseau local, services de la
stack) ; seuls le HTML et le texte sont lus, sans exécuter de JavaScript (les
sites protégés par un défi anti-robots renvoient une page vide). Les tests
n'accèdent pas à Internet : ils utilisent le moteur simulé de
[websearchtest](services/websearch/websearchtest/).

Ce skill a des instructions longues et
enchaîne de nombreux appels : utiliser un modèle en ligne, et compter
plusieurs minutes avec un quota gratuit.

L'interface n'affiche que ce qui concerne les skills chargés
(`GET /api/skills`) : suggestions, agenda, panneau des prospects.

### Agenda personnel (`agenda`)

« Ajoute un rendez-vous demain à 14h avec Paul », « Qu'est-ce que j'ai cette
semaine ? », « Décale ma réunion de 10h à 15h », « Supprime le yoga de mardi ».
Quatre tools : `create_event`, `list_events`, `update_event`, `delete_event`.
Les dates sont dites en langage naturel et converties par le code (package
`datetime`) dans le fuseau du navigateur ; un événement ambigu n'est jamais
choisi à la place de l'utilisateur (`EVENT_AMBIGUOUS`). L'agenda est pour
l'instant en mémoire (`agenda.MockProvider`, vidé au redémarrage) : un
fournisseur réel (Google Calendar, CalDAV…) se branche en implémentant
`agenda.Provider` ([services/agenda](services/agenda/agenda.go)) dans
[app/agenda.go](app/agenda.go). Détails :
[skills/agenda/SKILL.md](skills/agenda/SKILL.md).

### Synthèse vocale (`synthese-vocale`)

« Lis-moi cette réponse à voix haute. » : le modèle appelle
`lire_a_voix_haute`, et l'interface affiche un lecteur audio (avec un bouton
« Arrêter »). L'audio est généré **en flux** par le service `kokoro` et ne
passe jamais par le modèle. Fonctionnement détaillé, paramètres et erreurs :
[skills/synthese-vocale/SKILL.md](skills/synthese-vocale/SKILL.md).

1. **Activer le skill** : il est enregistré par défaut (`SKILLS` vide). Pour
   le limiter : `SKILLS=synthese-vocale` dans `.env` ; pour l'activer dès
   l'ouverture : `ACTIVE_SKILLS=synthese-vocale`.
2. **Démarrer Kokoro** : `./run.sh` lance le service `kokoro` (image
   `kokoro-fastapi-cpu`, modèle inclus, 1 à 2 Go de RAM). Seul :
   `docker compose up -d kokoro`. Il n'est pas exposé sur l'hôte.
3. **URL** : `TTS_BASE_URL` (défaut `http://kokoro:8880`, nom du service sur
   le réseau Docker).
4. **Voix** : `TTS_VOICES=fr-FR=ff_siwis;en-US=af_heart,am_michael` ; la
   première voix d'une langue est sa voix par défaut. Liste des voix de
   Kokoro : `docker compose exec kokoro curl -s localhost:8880/v1/audio/voices`.
5. **Langue** : `TTS_DEFAULT_LANGUAGE=fr-FR` ; le modèle précise `langue`
   pour un texte dans une autre langue de `TTS_VOICES`.
6. **Tester une synthèse** sans passer par le modèle :

   ```bash
   docker compose exec kokoro curl -s localhost:8880/v1/audio/speech \
     -H 'Content-Type: application/json' \
     -d '{"input":"Bonjour, ceci est un test.","voice":"ff_siwis","response_format":"mp3","lang_code":"f"}' > test.mp3
   ```

   Par l'application : `POST /api/sessions/{id}/messages` avec « Lis à voix
   haute : … », puis `GET /api/sessions/{id}/audio/{audio_id}` (`audio_id`
   dans l'effet `lire_a_voix_haute`).
7. **Flux** : `TTS_STREAMING=true` (défaut). Kokoro découpe le texte en
   morceaux d'environ 200 tokens : sur CPU, un texte de 800 caractères
   commence à être lu après ~6 s au lieu de ~28 s ; un texte court arrive en
   un seul morceau. Arrêter le lecteur annule la requête ; Kokoro s'arrête à
   la fin du morceau en cours.
8. **Remplacer Kokoro** : écrire un adaptateur `tts.Engine`
   ([services/tts/tts.go](services/tts/tts.go)) dans `services/tts/<moteur>/`,
   l'ajouter à `ttsProviders` ([app/tts.go](app/tts.go)) avec sa
   configuration, ajouter son service dans `compose.yaml`, puis
   `TTS_PROVIDER=<moteur>`. Le skill, le tool, l'API et l'interface ne
   changent pas.

Autres réglages ([app.env](app.env)) : `TTS_DEFAULT_FORMAT` (mp3, opus,
wav), `TTS_DEFAULT_SPEED`, `TTS_MIN_SPEED`, `TTS_MAX_SPEED`,
`TTS_MAX_TEXT_LENGTH`, `TTS_TIMEOUT`, `TTS_MAX_CONCURRENT`.

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
│   ├── agenda/
│   │   ├── SKILL.md             # métadonnées + documentation (garde-fous, erreurs, limites)
│   │   ├── instructions.md      # consignes données au modèle à l'activation
│   │   ├── schemas/*.json       # create_event, list_events, update_event, delete_event
│   │   └── skill.go
│   ├── prospect-research/
│   │   ├── SKILL.md             # métadonnées + consignes du modèle (indépendantes de l'application)
│   │   ├── hote.md              # consignes propres à cette application (outils, remise du résultat)
│   │   ├── references/          # schéma de sortie, cas de test
│   │   ├── schemas/*.json       # web_search, web_fetch, enregistrer_prospects
│   │   └── skill.go
│   └── synthese-vocale/
│       ├── SKILL.md             # métadonnées + documentation (flux, paramètres, erreurs)
│       ├── instructions.md      # consignes du modèle (complétées par les voix configurées)
│       ├── schemas/*.json       # lire_a_voix_haute
│       └── skill.go
├── tools/
│   ├── calendar/                # handlers : recherche, réservation, modification, annulation, listes
│   ├── datetime/                # handler interpreter_date
│   ├── web/                     # handlers web_search, web_fetch + registre des URL vues
│   ├── agenda/                  # handlers create_event, list_events, update_event, delete_event
│   ├── prospects/               # handler enregistrer_prospects
│   └── tts/                     # handler lire_a_voix_haute
├── services/
│   ├── calendar/
│   │   ├── provider.go          # interface Provider (contrat métier)
│   │   ├── mock.go              # MockProvider en mémoire + injection de pannes
│   │   └── demo.go              # jeu de données de démonstration
│   ├── agenda/                  # contrat Provider de l'agenda personnel + MockProvider, récurrences (RRULE)
│   ├── tts/                     # contrat Engine + Service (validation, flux, journaux) ; skill synthese-vocale
│   │   ├── kokoro/              # Engine : Kokoro-FastAPI
│   │   └── ttstest/             # moteur factice des tests
│   └── websearch/               # contrats SearchEngine / WebFetcher (skill prospect-research)
│       ├── searxng/             # SearchEngine : API JSON de SearXNG
│       ├── httpfetch/           # WebFetcher : client HTTP limité aux adresses publiques
│       └── websearchtest/       # mocks déterministes + univers fictif (testdata/prospect-research)
├── types/                       # Appointment, TimeSlot, Tool, Skill, codes d'erreur
├── jsonschema/                  # validateur JSON Schema (sous-ensemble)
├── datetime/                    # expressions françaises → intervalles ISO 8601
├── app/                         # assemblage : skills choisis par la configuration (SKILLS, ACTIVE_SKILLS)
├── api/                         # API JSON (Go) au-dessus du runtime, sans notion de skill
│   ├── calendarapi/             # routes d'agenda de l'interface (rendez-vous, vue semaine)
│   ├── prospectsapi/            # route du dernier résultat de prospect-research
│   └── ttsapi/                  # route de lecture des audios (flux) de synthese-vocale
├── ui/
│   ├── Caddyfile                # fichiers statiques + reverse proxy /api/* → api:8080
│   └── public/                  # index.html, app.js, calendar.js (agenda), prospects.js, style.css
├── cmd/api, cmd/chat, cmd/demo  # points d'entrée
├── Dockerfile, compose.yaml     # image de l'API et stack
├── searxng/settings.yml         # configuration du métamoteur de recherche
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
