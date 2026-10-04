// Package tts définit le contrat d'un moteur de synthèse vocale (Engine) et
// le service qui l'utilise (Service : validation, valeurs par défaut,
// délais, concurrence, journalisation, cache).
//
// Le skill synthese-vocale, ses tools et l'API ne dépendent que de ce
// package : le moteur concret (Kokoro, Piper, service cloud…) est un
// adaptateur dans un sous-package (ex. tts/kokoro), choisi par
// configuration (voir app/).
package tts

import (
	"context"
	"errors"
	"io"
	"strings"
)

// Engine est un moteur de synthèse vocale.
//
// Stream renvoie l'audio au fur et à mesure de sa génération quand le
// moteur le permet ; sinon, Body lit une réponse complète. L'appelant ferme
// toujours Body ; annuler ctx interrompt la génération.
//
// Erreurs de Stream (et de Body.Read) : une erreur qui enveloppe
// ErrEngineUnavailable (moteur injoignable ou surchargé, nouvel essai
// possible), ErrEngineError (requête refusée, réponse invalide) ou
// ErrUnsupportedLanguage / ErrUnsupportedFormat, ou l'erreur de ctx.
type Engine interface {
	// Name identifie le moteur dans les journaux et les clés de cache. Il
	// change quand un même texte ne donne plus le même audio (autre modèle).
	Name() string
	// Formats renvoie les formats que le moteur sait produire.
	Formats() []Format
	// SupportsLanguage indique si le moteur sait lire cette langue (BCP 47,
	// ex : fr-FR).
	SupportsLanguage(language string) bool
	Stream(ctx context.Context, req Request) (*Audio, error)
}

// Request est une demande de synthèse validée (voir Service.Prepare).
type Request struct {
	Text string
	// Language est une étiquette BCP 47 (ex : fr-FR).
	Language string
	// Voice est un identifiant propre au moteur (ex : ff_siwis pour Kokoro).
	Voice string
	// Speed : 1 = vitesse normale.
	Speed  float64
	Format Format
}

// Audio est un flux audio.
type Audio struct {
	ContentType string
	Body        io.ReadCloser
}

// Format est un format audio.
type Format string

// Formats connus.
const (
	FormatWAV  Format = "wav"
	FormatMP3  Format = "mp3"
	FormatOpus Format = "opus" // Opus dans un conteneur Ogg
)

var contentTypes = map[Format]string{
	FormatWAV:  "audio/wav",
	FormatMP3:  "audio/mpeg",
	FormatOpus: "audio/ogg",
}

// ParseFormat reconnaît un format connu (casse ignorée).
func ParseFormat(s string) (Format, bool) {
	f := Format(strings.ToLower(strings.TrimSpace(s)))
	_, ok := contentTypes[f]
	return f, ok
}

// ContentType renvoie le type MIME du format.
func (f Format) ContentType() string { return contentTypes[f] }

// Erreurs. Les erreurs de validation (Service.Prepare) peuvent être
// expliquées au modèle ; les erreurs du moteur ne doivent jamais être
// montrées telles quelles à l'utilisateur final.
var (
	ErrInvalidText         = errors.New("tts: texte vide")
	ErrTextTooLong         = errors.New("tts: texte trop long")
	ErrUnsupportedLanguage = errors.New("tts: langue non prise en charge")
	ErrUnsupportedVoice    = errors.New("tts: voix non prise en charge")
	ErrUnsupportedFormat   = errors.New("tts: format non pris en charge")
	ErrInvalidSpeed        = errors.New("tts: vitesse hors limites")
	ErrTimeout             = errors.New("tts: délai dépassé")
	ErrCancelled           = errors.New("tts: synthèse annulée")
	ErrEngineUnavailable   = errors.New("tts: moteur indisponible")
	ErrEngineError         = errors.New("tts: erreur du moteur")
)
