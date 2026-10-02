---
name: prise-de-rendez-vous
description: Prendre, déplacer ou annuler un rendez-vous, et consulter les disponibilités d'un professionnel. À activer dès que l'utilisateur parle de rendez-vous, de créneau, de disponibilité, veut « voir » un professionnel, ou veut décaler/annuler un rendez-vous.
---

# Skill `prise-de-rendez-vous`

> Les lignes `name` et `description` de l'en-tête sont lues par le code
> ([skill.go](skill.go)) : `description` est ce que le modèle voit pour décider
> d'activer le skill. Les consignes données au modèle une fois le skill activé
> sont dans [instructions.md](instructions.md). Ce fichier documente le skill
> pour les développeurs.

## Rôle

Permettre à un agent conversationnel de :

1. comprendre une demande de rendez-vous formulée en langage naturel ;
2. rechercher des disponibilités **réelles** dans le calendrier ;
3. proposer ces créneaux à l'utilisateur ;
4. réserver le créneau choisi ;
5. déplacer ou annuler un rendez-vous existant ;
6. confirmer le résultat **uniquement** sur la base de la réponse du calendrier.

Le skill ne contient que des instructions et des tools standardisés. Il
n'a aucune dépendance à un modèle d'IA ni à un agenda concret (Google,
Outlook…) : le calendrier est injecté via l'interface `calendar.Provider`.

## Quand l'activer

Exemples de demandes qui doivent activer le skill :

| Demande | Intention |
|---|---|
| « Je voudrais un rendez-vous jeudi après-midi. » | réserver |
| « Je voudrais voir Paul la semaine prochaine. » | réserver avec un professionnel |
| « Est-ce que vous avez quelque chose demain matin ? » | consulter les disponibilités |
| « Finalement, décalez mon rendez-vous à vendredi. » | déplacer |
| « Annule mon rendez-vous. » | annuler |

## Informations nécessaires

| Information | Obligatoire | Source |
|---|---|---|
| Période souhaitée | oui | utilisateur, convertie par `interpreter_date` |
| Professionnel | non (tous par défaut) | utilisateur, identifiant via `lister_professionnels` |
| Type de rendez-vous | oui pour réserver | utilisateur ; déduit s'il n'existe qu'un type possible |
| Nom du client | oui pour réserver | session si l'utilisateur est identifié, sinon utilisateur |
| E-mail du client | non | session ou utilisateur |
| Créneau choisi | oui pour réserver | choix de l'utilisateur **parmi les résultats** de `rechercher_disponibilites` |
| Rendez-vous à modifier/annuler | oui | `lister_rendez_vous` ou réservation faite dans la conversation |

Règle : ne demander que ce qui manque réellement, et jamais ce qui est déjà
connu (session, messages précédents).

## Outils disponibles

| Tool | Effet | Rôle |
|---|---|---|
| `interpreter_date` | lecture | « jeudi après-midi » → `2026-10-01T12:00:00+02:00` / `2026-10-01T18:00:00+02:00`, dans le fuseau de la session |
| `lister_professionnels` | lecture | professionnels, identifiants, types de rendez-vous proposés |
| `rechercher_disponibilites` | lecture | créneaux libres ; **seule source de créneaux** |
| `reserver_creneau` | **écriture** | réserve un `slot_id` issu d'une recherche |
| `lister_rendez_vous` | lecture | rendez-vous à venir du client identifié |
| `modifier_rendez_vous` | **écriture** | déplace un rendez-vous vers un nouveau `slot_id` |
| `annuler_rendez_vous` | **écriture** | annule un rendez-vous |

Les schémas JSON des entrées sont dans [schemas/](schemas/). Tous les
résultats ont la forme `{"success": true, ...}` ou
`{"success": false, "error": {"code": "...", "message": "...", "missing": [...]}}`.

## Règles métier

Ces règles sont écrites dans les instructions du modèle **et** appliquées par le
code : le prompt ne suffit jamais à lui seul.

| Règle | Où elle est appliquée dans le code |
|---|---|
| Ne jamais inventer un créneau | seuls les `slot_id` renvoyés par `rechercher_disponibilites` **dans la session** sont acceptés (`tools/calendar/common.go`, registre d'identifiants) ; le calendrier revérifie ensuite |
| Ne jamais inventer un `appointment_id` | même registre : seuls les identifiants renvoyés par `reserver_creneau` / `lister_rendez_vous` sont acceptés |
| Réservation confirmée seulement si le calendrier confirme | `reserver_creneau` exige `Confirmed == true` **et** un identifiant ; sinon `BOOKING_FAILED` |
| Modification/annulation : état réel | `Updated` / `Cancelled` doivent être vrais ; sinon `UPDATE_FAILED` / `CANCEL_FAILED` |
| Dates ISO 8601 avec fuseau | JSON Schema `format: date-time` (RFC 3339 : décalage obligatoire) + revalidation dans le tool |
| Fuseau jamais supposé | `agent.NewSession` refuse une session sans fuseau |
| Identité du client | imposée par la session (`ToolContext.User`) ; un `client_id` différent est refusé |
| Pas de détail technique exposé | les erreurs techniques du fournisseur deviennent `CALENDAR_UNAVAILABLE`, sans message d'origine |

## Étapes d'une réservation

1. Activer le skill (`activer_skill`).
2. Convertir la période exprimée en intervalle ISO (`interpreter_date`).
3. Si un professionnel est nommé et que son identifiant est inconnu :
   `lister_professionnels`.
4. `rechercher_disponibilites` sur l'intervalle.
5. Présenter les créneaux (3 à 5 maximum, en utilisant le champ `libelle`).
6. Attendre le choix explicite de l'utilisateur.
7. Compléter uniquement les informations manquantes (nom, type…).
8. `reserver_creneau` avec le `slot_id` choisi.
9. Si `success: true` → confirmer avec la date et l'heure renvoyées par le
   calendrier. Sinon → expliquer simplement et proposer une alternative.

Déplacement : identifier le rendez-vous (`lister_rendez_vous` si besoin) →
chercher la nouvelle période → faire choisir → `modifier_rendez_vous`.

Annulation : identifier le rendez-vous → s'il y en a plusieurs, demander
lequel → `annuler_rendez_vous` → confirmer selon le résultat.

## Ambiguïtés

- Plusieurs moments possibles (« jeudi ») : chercher sur la journée ; si
  les créneaux sont trop nombreux, demander « Vous préférez le matin ou
  l'après-midi ? ».
- Référence relative (« le deuxième », « plutôt 16h ») : se rapporter à la
  **dernière liste de créneaux présentée**, sans recommencer la collecte.
- Nouvelle contrainte (« plutôt vendredi matin ») : conserver les autres
  critères déjà donnés (professionnel, type) et relancer la recherche.
- Plusieurs rendez-vous à venir pour « annule mon rendez-vous » : demander
  lequel, en les listant.
- Horaire sans jour (« à 16h ») sans contexte : demander le jour.

## Absence de disponibilité

`NO_AVAILABILITY` : ne rien inventer. Proposer d'élargir la période (autre
jour, autre moment de la journée) ou un autre professionnel, puis relancer une
recherche si l'utilisateur accepte.

## Confirmation

- Ne jamais annoncer « c'est réservé / confirmé / annulé / déplacé » sans un
  résultat `success: true` du tool correspondant **dans le tour courant**.
- La confirmation reprend la date/heure du résultat du tool (`libelle`).
- Pas de réservation automatique d'un créneau « qui semble convenir » : l'utilisateur
  choisit, sauf si la politique produit `AutoBooking` est activée
  (voir `Config.AutoBooking`), et uniquement quand la recherche renvoie un seul
  créneau correspondant exactement à la demande.
- L'application hôte doit baser ses propres confirmations (e-mail, UI) sur
  `agent.Reply.Effects`, jamais sur le texte généré.

## Erreurs

| Code | Réponse type à l'utilisateur |
|---|---|
| `NO_AVAILABILITY` | « Je n'ai rien trouvé sur cette période. Voulez-vous que je regarde un autre jour ? » |
| `SLOT_NO_LONGER_AVAILABLE` | « Désolé, ce créneau vient d'être pris. Je peux rechercher une autre disponibilité. » |
| `BOOKING_FAILED` | « La réservation n'a pas pu être enregistrée. Voulez-vous réessayer ? » |
| `APPOINTMENT_NOT_FOUND` | « Je ne retrouve pas ce rendez-vous. » |
| `UPDATE_FAILED` | « Le changement n'a pas pu être enregistré, votre rendez-vous initial est maintenu. » |
| `CANCEL_FAILED` | « L'annulation n'a pas pu être enregistrée, votre rendez-vous est toujours prévu. » |
| `INVALID_REQUEST` | (interne) le modèle corrige ses paramètres |
| `MISSING_INFORMATION` | demander uniquement les champs listés dans `missing` |
| `CALENDAR_UNAVAILABLE` | « L'agenda est momentanément indisponible, pouvez-vous réessayer dans quelques minutes ? » |
