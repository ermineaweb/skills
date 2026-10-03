# Guide d'architecture et d'extension

Ce guide explique comment les éléments du projet s'articulent, comment
utiliser le skill `prise-de-rendez-vous` et comment étendre le système
(nouveau calendrier, nouveau modèle d'IA, nouveau skill).

## 1. Séparation des responsabilités

```
Model  ≠  Agent Runtime  ≠  Skill  ≠  Tool  ≠  Business Service
```

| Couche | Package | Rôle | Ne connaît pas |
|---|---|---|---|
| **Model** | `model` (interface), `model/openai`, `model/scripted` | Traduit une `model.Request` neutre vers un fournisseur d'IA, et sa réponse en `model.Message` | les skills, le calendrier, la logique métier |
| **Agent Runtime** | `agent` | Boucle modèle → tools, activation des skills, validation JSON Schema, session (historique, fuseau, identité), effets confirmés | le fournisseur d'IA, le métier |
| **Skill** | `skills/prise-de-rendez-vous` | Assemble instructions (`instructions.md`), métadonnées (`SKILL.md`), schémas (`schemas/*.json`) et handlers | le modèle, l'agenda concret |
| **Tool** | `tools/calendar`, `tools/datetime` | Frontière contrôlée : revalide, vérifie la provenance des identifiants, impose l'identité, exige une confirmation, traduit les erreurs | le modèle, l'agenda concret |
| **Business Service** | `services/calendar` | Source de vérité : disponibilités, réservations, règles de conflit | l'IA sous toutes ses formes |
| **Assemblage** | `app` | Enregistre les skills choisis par la configuration (`SKILLS`, `ACTIVE_SKILLS`), construit leurs services et leurs routes d'API | le fournisseur d'IA, l'interface (HTTP, terminal) |
| **Interface** | `api` (+ `api/calendarapi`), `cmd/*` | HTTP ou terminal au-dessus du runtime ; `api` ne connaît aucun skill, les routes d'un domaine s'y branchent avec `api.WithSessionRoute` | le métier (sauf l'extension de domaine) |

Les dépendances vont dans un seul sens :

```
cmd/*  ──►  api ──► agent  ──►  model (interface)
  │                   │
  │                   └──►  types, jsonschema
  │
  ├──►  model/openai    (choisi uniquement par le point d'entrée)
  └──►  app ──► skills/prise-de-rendez-vous ──► tools/* ──► services/calendar ──► types
              └► api/calendarapi ──► api, services/calendar
```

Les points d'entrée ne connaissent aucun skill : `cmd/api` et `cmd/chat`
choisissent le modèle et appellent `app.New`. Seule la démo scriptée
(`cmd/demo`) cite le skill de rendez-vous, puisque son script en rejoue les
tools.

Aucun package sous `skills/`, `tools/` ou `services/` n'importe `model/…`.
Changer de modèle ne touche donc ni au skill, ni aux tools, ni au calendrier.

## 2. Déroulé d'un tour de conversation

```
Utilisateur : « Je voudrais un rendez-vous jeudi après-midi. »
   │
   ▼
Runtime.Handle ──► Model.Generate(system, messages, tools=[activer_skill])
   │                 └─► appel activer_skill("prise-de-rendez-vous")
   │   runtime : skill actif → instructions dans le prompt système, tools exposés
   ├──► Model.Generate(...)  └─► interpreter_date("jeudi après-midi")
   │   tool : 2026-10-01T12:00:00+02:00 → 2026-10-01T18:00:00+02:00
   ├──► Model.Generate(...)  └─► rechercher_disponibilites(date_debut, date_fin)
   │   runtime : validation JSON Schema → handler → CalendarProvider.SearchAvailability
   │   calendrier : 14:00, 15:00, 15:30, 17:00 (identifiants mémorisés dans la session)
   └──► Model.Generate(...)  └─► texte « J'ai trouvé ces disponibilités : … »

Utilisateur : « Je prends 15h30. »
   ├──► Model.Generate(...)  └─► reserver_creneau(slot_id du créneau de 15h30)
   │   tool : slot_id émis dans la session ? identité imposée ; Provider.BookAppointment
   │   calendrier : Confirmed = true, rdv_001
   │   runtime : Reply.Effects += reserver_creneau
   └──► Model.Generate(...)  └─► « Votre rendez-vous est confirmé : jeudi 1er octobre à 15h30. »
```

La démo scriptée (`go run ./cmd/demo`, voir le README) affiche exactement ce déroulé.

## 3. Charger le skill

Dans l'application, `app.New` fait cet assemblage d'après la configuration
(`SKILLS`, `ACTIVE_SKILLS`, `CALENDAR_TZ`, `AUTO_BOOKING`) :

```go
a, err := app.New(llm, app.ConfigFromEnv())
handler := api.New(a.Runtime, time.Now, a.APIOptions...).Handler()
```

Ce qu'il fait pour `prise-de-rendez-vous`, utilisable tel quel dans un autre
programme :

```go
cal, _ := calendar.NewDemoProvider(loc, time.Now)            // ou votre Provider réel
skill, err := priserdv.New(priserdv.Config{
    Provider:    cal,
    AutoBooking: false, // politique produit (désactivée par défaut)
})

rt, _ := agent.New(llm)                 // llm : n'importe quel model.Adapter
if err := rt.RegisterSkill(skill); err != nil { … }

session, err := agent.NewSession("conv-123", "Europe/Paris",
    types.UserContext{ClientID: "c-42", Name: "Camille Durand"}) // identité issue de VOTRE authentification

reply, err := rt.Handle(ctx, session, "Je voudrais un rendez-vous jeudi après-midi.")
// reply.Text    : réponse à afficher
// reply.Effects : opérations réellement confirmées par le calendrier
// reply.Steps   : trace des appels de tools (audit, debug)
```

Points importants :

- **Fuseau obligatoire** : `NewSession` refuse un fuseau vide. Il doit venir du
  profil utilisateur ou du navigateur, jamais d'une valeur par défaut implicite.
- **Identité** : `UserContext` provient de l'application hôte. Les tools
  l'imposent : le modèle ne peut ni réserver au nom d'un autre `client_id`, ni
  lister ou annuler les rendez-vous d'un autre client.
- **Confirmations côté application** : pour un e-mail ou un bandeau de
  confirmation, utilisez `reply.Effects`, jamais `reply.Text`.
- **Persistance de session** : `Session` vit en mémoire. Pour un service
  multi-instances, il faut la sérialiser (voir §9).

## 4. Enregistrer les tools

Un tool = une **définition** (nom, description, JSON Schema) + un **handler**
(`types.ToolHandler`) + un drapeau `Mutating`.

Dans ce skill :

- les définitions sont des fichiers JSON dans `skills/prise-de-rendez-vous/schemas/`,
  embarqués dans le binaire (`go:embed`) ;
- les handlers sont construits dans `tools/…` avec le `Provider` injecté ;
- `priserdv.New` associe chaque fichier à son handler et vérifie que les noms
  concordent.

`Runtime.RegisterSkill` compile ensuite tous les schémas (un schéma invalide
fait échouer le démarrage) et refuse les noms de tools en double entre skills.

À l'exécution, pour chaque appel du modèle, le runtime :

1. vérifie que le tool existe et que son skill est **actif** dans la session ;
2. valide les arguments contre le JSON Schema :
   champ obligatoire absent → `MISSING_INFORMATION` (avec `missing`), autre
   violation → `INVALID_REQUEST` ; le handler n'est alors pas appelé ;
3. appelle le handler avec un `ToolContext` de confiance (session, utilisateur,
   fuseau, instant, état) ;
4. renvoie au modèle `{"success": true, …}` ou `{"success": false, "error": {…}}` ;
5. ajoute le résultat aux `Effects` si le tool est `Mutating` et a réussi.

Une panique dans un handler est contenue (`INTERNAL_ERROR`).

## 5. Utiliser le `CalendarProvider`

```go
type Provider interface {
    SearchAvailability(ctx, AvailabilityRequest) (AvailabilityResult, error)
    BookAppointment(ctx, BookingRequest) (BookingResult, error)
    UpdateAppointment(ctx, UpdateAppointmentRequest) (UpdateAppointmentResult, error)
    CancelAppointment(ctx, CancelAppointmentRequest) (CancelAppointmentResult, error)
    ListAppointments(ctx, ListAppointmentsRequest) (ListAppointmentsResult, error)
    ListProfessionals(ctx) (ListProfessionalsResult, error)
}
```

`ListAppointments` et `ListProfessionals` complètent les quatre opérations
demandées : sans elles, « annule mon rendez-vous » dans une nouvelle
conversation ou « je veux voir Paul » obligeraient le modèle à deviner un
identifiant.

Contrat :

| Aspect | Règle |
|---|---|
| Erreur métier | renvoyer `*types.Error` avec un code (`SLOT_NO_LONGER_AVAILABLE`, `APPOINTMENT_NOT_FOUND`…) |
| Erreur technique | renvoyer une `error` quelconque : elle devient `CALENDAR_UNAVAILABLE`, son texte est journalisé et jamais transmis au modèle |
| Succès d'écriture | `err == nil` **et** `Confirmed` / `Updated` / `Cancelled == true` ; sinon le tool renvoie `BOOKING_FAILED` / `UPDATE_FAILED` / `CANCEL_FAILED` |
| Créneaux | le Provider est seul à fabriquer des `TimeSlot` et leurs identifiants ; `BookAppointment` doit revérifier que le créneau est libre |
| Propriété | `ClientID` des requêtes vient de la session ; un rendez-vous d'un autre client doit être signalé `APPOINTMENT_NOT_FOUND` |
| Fuseau | les `time.Time` portent leur fuseau ; les tools les convertissent dans le fuseau de la session |

`MockProvider` implémente ce contrat en mémoire, avec injection de pannes pour
les tests : `SetUnavailable`, `FailNext(op, code)`, `UnconfirmedNext(op)`,
`OccupySlot(slotID)` (concurrence) et `DeleteAppointment(id)` (back-office).

## 6. Brancher un nouveau fournisseur de calendrier

1. Créer `services/calendar/<fournisseur>.go` (ou un package dédié) qui
   implémente `calendar.Provider`.
2. **Identifiants de créneau** : un agenda externe ne connaît que des plages
   libres. Encodez de quoi retrouver le créneau dans l'identifiant (ex:
   `slot_<calendarId>_<startUTC>_<durée>`) ou conservez un cache serveur
   `slot_id → créneau` avec expiration. Ne faites jamais confiance au créneau
   sans revérifier la disponibilité au moment de la réservation.
3. **Réservation atomique** : utilisez le mécanisme de l'agenda (ETag,
   transaction, verrou) pour éviter les doubles réservations ; en cas de
   conflit renvoyez `SLOT_NO_LONGER_AVAILABLE`.
4. **Idempotence** : si l'API le permet, passez une clé d'idempotence (ex:
   hash de session + slot) pour qu'un réessai ne crée pas deux rendez-vous.
5. **Erreurs** : convertissez les erreurs connues en `*types.Error` ; laissez
   les erreurs réseau/timeout remonter telles quelles (→ `CALENDAR_UNAVAILABLE`).
6. **Confirmation** : ne mettez `Confirmed = true` qu'après la réponse positive
   de l'API, avec l'identifiant de l'événement créé.
7. Brancher : dans `app/rendezvous.go`, remplacer
   `calendar.NewDemoProvider(…)` par votre Provider (configuré par variables
   d'environnement). Aucune autre ligne ne change.
8. Tester : réutiliser les tests de `tools/calendar/tools_test.go` en les
   paramétrant par Provider (tests de contrat), plus des tests d'intégration
   contre un agenda de test.

Squelette :

```go
type GoogleProvider struct{ client *http.Client; calendarIDs map[string]string /* pro → agenda */ }

func (g *GoogleProvider) BookAppointment(ctx context.Context, req calendar.BookingRequest) (calendar.BookingResult, error) {
    slot, err := g.decodeSlot(req.SlotID)
    if err != nil {
        return calendar.BookingResult{}, types.NewError(types.ErrSlotNoLongerAvailable)
    }
    if busy, err := g.isBusy(ctx, slot); err != nil {
        return calendar.BookingResult{}, err                      // technique → CALENDAR_UNAVAILABLE
    } else if busy {
        return calendar.BookingResult{}, types.NewError(types.ErrSlotNoLongerAvailable)
    }
    ev, err := g.insertEvent(ctx, slot, req)                      // appel API réel
    if err != nil {
        return calendar.BookingResult{}, err
    }
    return calendar.BookingResult{Confirmed: true, Appointment: toAppointment(ev)}, nil
}
```

## 7. Brancher un nouveau modèle d'IA

Implémenter une seule interface :

```go
type Adapter interface {
    Generate(ctx context.Context, req model.Request) (model.Response, error)
}
```

L'adapter traduit :

| Concept neutre | À traduire vers le fournisseur |
|---|---|
| `Request.System` | message `system` (OpenAI) ou champ dédié (d'autres API) |
| `Message{Role: user}` | message utilisateur |
| `Message{Role: assistant, ToolCalls}` | message assistant avec appels de fonctions / blocs d'appel d'outil |
| `Message{Role: tool, ToolCallID, Content}` | résultat d'outil associé à l'appel |
| `types.ToolDefinition{Name, Description, Parameters}` | déclaration d'outil (le JSON Schema est transmis tel quel) |

Et en retour : texte et/ou appels d'outils → `model.Message{Role: assistant}`.

`model/openai` en est un exemple complet (HTTP brut, sans SDK). Il fonctionne
aussi avec toute API compatible OpenAI (Ollama, vLLM, LM Studio, Mistral…) via
`OPENAI_BASE_URL`.

Pour un autre fournisseur : créer `model/<fournisseur>/adapter.go`, un test
avec `httptest` sur la traduction requête/réponse (voir
`model/openai/adapter_test.go`), puis choisir l'adapter **dans le point
d'entrée** (`cmd/…`). Les skills, tools et services ne changent pas.

Les spécificités restent dans l'adapter : format des arguments (chaîne JSON
ou objet), prompt système séparé ou non, sous-ensemble de JSON Schema accepté
(un adapter peut simplifier le schéma si son API l'exige), limites de débit,
réessais.

## 8. Tester

```bash
# dans un conteneur Go jetable (voir la variable GO dans le README)
eval $GO go test ./...
eval $GO go test -v ./skills/...   # verbeux : conversations des scénarios
eval $GO go vet ./...
```

| Niveau | Fichier | Couvre |
|---|---|---|
| Validation | `jsonschema/jsonschema_test.go` | types, requis, bornes, `date-time` avec fuseau, propriétés inconnues |
| Dates | `datetime/resolver_test.go` | « jeudi après-midi », « la semaine prochaine », changement d'heure, dates passées, jour manquant |
| Service | `services/calendar/mock_test.go` | créneaux non émis, propriété, chevauchements, double annulation |
| Tools | `tools/calendar/tools_test.go` | recherche (date, plage, vide, professionnel, type), réservation (succès, créneau pris, pannes, non confirmée, données manquantes, id inventé), modification, annulation, listes |
| Runtime | `agent/runtime_test.go` | activation, validation avant exécution, effets, panique, étapes max, reprise après erreur |
| Adapter | `model/openai/adapter_test.go` | traduction requête/réponse, erreurs HTTP |
| Scénarios | `skills/prise-de-rendez-vous/conversation_test.go` | conversations complètes de bout en bout |

Les scénarios utilisent `model/scripted` : chaque étape joue le rôle du LLM et
lit l'historique pour construire ses appels (un « je prends le deuxième » prend
le 2ᵉ créneau du dernier résultat de recherche). Ils vérifient l'orchestration,
les garde-fous et l'état réel du calendrier, **pas** la qualité de
compréhension d'un vrai modèle. Pour cela, voir §10.

## 9. Ajouter un nouveau skill

Exemple : `facturation`.

```
skills/facturation/
├── SKILL.md            # en-tête name/description + documentation
├── instructions.md     # consignes injectées à l'activation
├── schemas/
│   └── consulter-facture.json   # {"name", "description", "parameters"}
├── skill.go            # implémente types.Skill
└── conversation_test.go
tools/billing/          # handlers (types.ToolHandler)
services/billing/       # interface métier + mock
```

1. Définir le service métier (`services/billing.Provider`) et son mock.
2. Écrire les handlers dans `tools/billing`, avec les mêmes règles :
   revalidation, identifiants vérifiés, identité issue de `ToolContext.User`,
   succès uniquement sur confirmation, erreurs traduites.
3. Écrire `SKILL.md` (l'en-tête `description` indique **quand** activer le
   skill), `instructions.md` et les schémas.
4. Implémenter `types.Skill` (voir `skills/prise-de-rendez-vous/skill.go`).
5. Dans `app/`, ajouter un fichier `facturation.go` qui construit le skill à
   partir de sa configuration (`Env.Getenv`) et, si l'interface en a besoin,
   ses routes d'API (`api.WithSessionRoute`), puis l'ajouter à `available`
   dans `app/app.go` (voir `app/rendezvous.go`).
6. Documenter ses variables et leurs valeurs par défaut dans `app.env`.
   L'activer : `SKILLS=prise-de-rendez-vous,facturation` (vide = tous), et
   éventuellement `ACTIVE_SKILLS`. Ni les points d'entrée, ni `compose.yaml`,
   ni `run.sh` ne changent.

Le runtime ne change pas : il liste les skills dans le prompt système, expose
`activer_skill` avec l'énumération des noms, et ne donne au modèle que les
tools des skills activés. Les noms de tools doivent être uniques entre skills
(vérifié à l'enregistrement). `interpreter_date` est générique : un autre skill
peut réutiliser `datetimetools.NewInterpreterDate()` sous un autre nom de tool.

## 10. Garde-fous et limites connues

| Risque | Garde-fou | Où |
|---|---|---|
| Créneau inventé | registre des `slot_id` émis dans la session + revérification par le Provider | `tools/calendar/common.go`, Provider |
| Rendez-vous inventé | registre des `appointment_id` émis | idem |
| Date en langage naturel envoyée au backend | JSON Schema `date-time` (RFC 3339, décalage obligatoire) + revalidation | schémas, tools |
| Fuseau supposé | session sans fuseau refusée ; `interpreter_date` utilise le fuseau de session | `agent/session.go` |
| Succès annoncé à tort | `Confirmed/Updated/Cancelled` exigés ; `Effects` seulement sur succès réel | tools, runtime |
| Usurpation de client | identité imposée par la session | tools, Provider |
| Fuite technique | erreurs techniques → `CALENDAR_UNAVAILABLE` sans détail | `providerError` |
| Tool hors contexte | tool refusé si son skill n'est pas actif | runtime |

Limites :

- Le texte généré par le modèle n'est pas contrôlé mot à mot : un modèle
  défaillant pourrait écrire « confirmé » après un échec. Les instructions
  l'interdisent, mais la garantie côté produit vient de `Reply.Effects`.
- Pas d'évaluation avec un vrai LLM dans la suite de tests : à ajouter sous
  forme d'évaluations hors CI (jeu de conversations + critères vérifiés sur
  la trace des tools).
- Sessions en mémoire, sans persistance ni expiration.
- Le résolveur de dates couvre les expressions courantes (jours, « demain »,
  « matin/après-midi/soir », « la semaine prochaine », dates explicites,
  heures). Les autres cas (« dans 15 jours », « fin octobre ») renvoient une
  erreur qui invite le modèle à demander une précision.
