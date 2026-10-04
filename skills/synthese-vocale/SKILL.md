---
name: synthese-vocale
description: Lire un texte à voix haute (synthèse vocale). À activer quand l'utilisateur demande de lire, dire, prononcer ou écouter un texte ou une réponse, ou parle d'audio, de voix ou de « à voix haute ».
---

# Skill `synthese-vocale`

> Les lignes `name` et `description` de l'en-tête sont lues par le code
> ([skill.go](skill.go)). Les consignes données au modèle sont dans
> [instructions.md](instructions.md), complétées à l'exécution par les
> langues, voix et limites configurées. Ce fichier documente le skill pour
> les développeurs.

## Rôle

Convertir en parole un texte désigné par l'utilisateur (« Lis-moi cette
réponse à voix haute. »), dans la langue, la voix, la vitesse et le format
demandés.

## Fonctionnement

```
modèle ─► lire_a_voix_haute ─► tts.Service.Prepare (validation)
                                  │  demande conservée dans la session
                                  ▼
          effet {audio_id} ─► interface ─► GET /api/sessions/{id}/audio/{audio_id}
                                                 │
                                    tts.Service.Stream ─► tts.Engine ─► serveur TTS (Kokoro…)
                                                 │
                         audio transmis au fil de la génération ◄─┘
```

- Le tool ne génère pas l'audio : il **valide** la demande et la conserve
  dans la session (20 au plus). L'audio ne passe jamais par le modèle.
- L'interface lit l'audio par [api/ttsapi](../../api/ttsapi/ttsapi.go), qui
  le **diffuse en flux** : la lecture commence dès la première phrase
  générée et l'audio n'est jamais conservé en entier en mémoire.
- **Annulation** : arrêter le lecteur, fermer la page ou dépasser
  `TTS_TIMEOUT` annule la requête HTTP, donc la génération en cours.
- Le skill ne connaît que le contrat [services/tts](../../services/tts/tts.go)
  (`tts.Engine`) ; le moteur est choisi par `TTS_PROVIDER` (voir
  [app/tts.go](../../app/tts.go)).

## Tool `lire_a_voix_haute`

| Paramètre | Obligatoire | Défaut | Validation |
|---|---|---|---|
| `texte` | oui | | non vide, au plus `TTS_MAX_TEXT_LENGTH` caractères |
| `langue` | non | `TTS_DEFAULT_LANGUAGE` | langue de `TTS_VOICES` (casse ignorée, `fr` accepté pour `fr-FR`) |
| `voix` | non | première voix de la langue | voix de cette langue dans `TTS_VOICES` |
| `vitesse` | non | `TTS_DEFAULT_SPEED` | entre `TTS_MIN_SPEED` et `TTS_MAX_SPEED` |
| `format` | non | `TTS_DEFAULT_FORMAT` | `mp3`, `opus` ou `wav`, s'il est produit par le moteur |

Exemple d'appel par le modèle :

```json
{"texte": "Bonjour, comment puis-je vous aider ?", "langue": "fr-FR", "voix": "ff_siwis", "vitesse": 1.0, "format": "opus"}
```

Résultat (effet du tour, lu par l'interface) :

```json
{"success": true, "audio_id": "4f1c…", "langue": "fr-FR", "voix": "ff_siwis", "vitesse": 1, "format": "opus", "caracteres": 37, "note": "…"}
```

Une demande invalide renvoie `INVALID_REQUEST` avec les valeurs acceptées
(ex : `voix non prise en charge : "x" pour fr-FR (voix : ff_siwis)`).

## Erreurs de synthèse

Elles surviennent à la lecture de l'audio (route d'API), pas dans le tool :

| Erreur (`services/tts`) | HTTP | Cause |
|---|---|---|
| `ErrEngineUnavailable` | 503 | moteur injoignable ou surchargé |
| `ErrTimeout` | 504 | `TTS_TIMEOUT` dépassé |
| `ErrEngineError` | 502 | requête refusée par le moteur, audio vide |
| `ErrCancelled` | — | client parti : rien n'est renvoyé |

Le détail du moteur n'est jamais renvoyé au client ; il est journalisé
(`tts: échec de synthèse`). Une erreur après le début du flux coupe la
connexion : le lecteur ne prend pas un audio tronqué pour un audio complet.

## Journaux

`tts: début de synthèse`, `tts: fin de synthèse`, `tts: synthèse annulée`,
`tts: échec de synthèse`, avec moteur, langue, voix, format, vitesse, nombre
de caractères, durée, délai du premier octet et taille de l'audio. Le texte
n'est jamais journalisé.
