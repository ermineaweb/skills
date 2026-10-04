---
name: agenda
description: Gérer l'agenda personnel de l'utilisateur : ajouter, consulter, déplacer ou supprimer ses propres événements (réunions, déjeuners, rendez-vous qu'il note lui-même, événements récurrents, rappels), et dire s'il est disponible. À activer pour « ajoute / mets dans mon agenda », « qu'est-ce que j'ai demain ? », « suis-je libre vendredi ? », « décale / supprime ma réunion ». Pas pour réserver un créneau chez un professionnel (prise-de-rendez-vous), ni pour un simple conseil d'organisation.
---

# Skill `agenda`

> Les lignes `name` et `description` de l'en-tête sont lues par le code
> ([skill.go](skill.go)) : `description` est ce que le modèle voit pour décider
> d'activer le skill. Les consignes données au modèle une fois le skill activé
> sont dans [instructions.md](instructions.md). Ce fichier documente le skill
> pour les développeurs.

## Rôle

Gérer les événements de l'utilisateur dans son agenda personnel : création
(avec durée, lieu, participants, rappels, récurrence), consultation d'une
période, modification et suppression.

Distinct de `prise-de-rendez-vous`, qui réserve des créneaux chez des
professionnels (`calendar.Provider`). L'agenda est injecté via
`agenda.Provider` ([services/agenda](../../services/agenda/agenda.go)) :
l'application utilise pour l'instant `agenda.MockProvider` (en mémoire, perdu
au redémarrage). **À connecter** : un fournisseur réel (Google Calendar,
CalDAV, Microsoft Graph) qui implémente `agenda.Provider` — la récurrence
s'exporte en RRULE (`Recurrence.RRULE`), les rappels et les occurrences y sont
natifs.

## Tools

| Tool | Rôle | Effet |
|---|---|---|
| `create_event` | ajoute un événement ou une série | oui |
| `list_events` | occurrences d'une période (séries développées), triées | non |
| `update_event` | horaire, titre, lieu, participants, notes, rappels | oui |
| `delete_event` | supprime un événement, une occurrence ou une série | oui |

Exemple : « Mets dans mon agenda une réunion avec l'équipe vendredi de 10h à
11h, rappelle-moi 15 minutes avant. »

```json
{"titre": "Réunion avec l'équipe", "debut": "vendredi 10h", "fin": "11h", "rappels_minutes": [15]}
```

Résultat (dates dans le fuseau de la session, libellé calculé par le code) :

```json
{"success": true, "evenement": {"id": "evt-1", "titre": "Réunion avec l'équipe",
 "debut": "2026-10-09T10:00:00+02:00", "fin": "2026-10-09T11:00:00+02:00",
 "libelle": "vendredi 9 octobre de 10h à 11h", "rappels_minutes": [15]}}
```

## Garde-fous appliqués par le code

- **Dates** : les tools reçoivent les expressions de l'utilisateur (« demain à
  14h », « dans deux heures », « ce week-end ») et les convertissent avec le
  package `datetime`, dans le fuseau de la session (celui du navigateur),
  jamais en UTC supposé. Une date ISO 8601 est aussi acceptée. Sans heure,
  `create_event` renvoie `MISSING_INFORMATION` (`heure`) ; sans fin ni
  durée, l'événement dure 1 heure.
- **Identification** : `event_id` doit avoir été renvoyé par un tool dans la
  conversation ; sinon `cible` (`quand` + `texte`) est résolue par le code.
  Plusieurs candidats → `EVENT_AMBIGUOUS` avec leur liste, rien n'est fait.
  Dans une série, `portee` (`occurrence` / `serie`) est obligatoire.
- **Conflits** : un chevauchement (hors journées entières) renvoie
  `EVENT_CONFLICT` ; l'opération n'est faite qu'avec `ignorer_conflits: true`,
  après confirmation de l'utilisateur.
- **Propriété** : le propriétaire est `UserContext.ClientID` de la session,
  jamais un paramètre du modèle. Un événement d'un autre utilisateur est
  introuvable (`EVENT_NOT_FOUND`), sans révéler son existence ; sans
  utilisateur identifié : `NOT_AUTHENTICATED`.
- **Confirmation** : une écriture n'est un succès que si l'agenda l'a
  confirmée (`Created` / `Updated` / `Deleted`) ; sinon `EVENT_NOT_SAVED`.

## Récurrence

Sous-ensemble de la RRULE (RFC 5545) : quotidienne, hebdomadaire (jours),
mensuelle (même jour du mois, jours de la semaine, ou « le premier / dernier
lundi »), annuelle ; intervalle ; fin par date (`jusqu_au`) ou par nombre.
Les occurrences sont développées dans le fuseau de l'événement (9h reste 9h
après le changement d'heure). Modifier une seule occurrence la détache de la
série (nouvel événement) ; supprimer une occurrence l'exclut (EXDATE).

## Erreurs

| Code | Situation |
|---|---|
| `MISSING_INFORMATION` | titre, jour, heure ou portée manquants (`missing`) |
| `INVALID_REQUEST` | date passée ou inexistante, fin avant début, identifiant inventé, récurrence incohérente |
| `EVENT_NOT_FOUND` | aucun événement ne correspond, ou il appartient à un autre utilisateur |
| `EVENT_AMBIGUOUS` | plusieurs événements correspondent (liste dans `message`) |
| `EVENT_CONFLICT` | chevauchement (liste dans `message`) |
| `EVENT_NOT_SAVED` | écriture non confirmée par l'agenda |
| `CALENDAR_UNAVAILABLE` | panne technique de l'agenda (détail journalisé, jamais transmis) |
| `NOT_AUTHENTICATED`, `PERMISSION_DENIED` | utilisateur non identifié, droits insuffisants |

## Limites

- Les rappels sont enregistrés mais pas notifiés par `MockProvider` : c'est le
  rôle du fournisseur réel.
- La récurrence d'une série existante ne se modifie pas (supprimer puis
  recréer) ; pour toute la série, seul l'horaire peut changer.
- Les invitations aux participants ne sont pas envoyées.
