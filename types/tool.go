package types

import (
	"context"
	"encoding/json"
	"time"
)

// ToolDefinition est la description d'un tool, indépendante de tout
// fournisseur d'IA. Parameters est un JSON Schema (objet).
// Chaque ModelAdapter la traduit dans le format de son fournisseur.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// StateStore est un espace clé/valeur propre à une session de conversation.
// Les tools l'utilisent pour mémoriser, par exemple, les identifiants qu'ils
// ont eux-mêmes renvoyés.
type StateStore interface {
	Get(key string) (any, bool)
	Set(key string, value any)
}

// ToolContext est fourni par le runtime à chaque exécution de tool.
// Il contient des informations de confiance (non issues du modèle).
type ToolContext struct {
	SessionID string
	User      UserContext
	Location  *time.Location
	Now       time.Time
	State     StateStore
}

// ToolHandler exécute un tool. args a déjà été validé contre le JSON Schema
// de la définition par le runtime.
//
// En cas de succès, output est sérialisé en objet JSON auquel le runtime
// ajoute "success": true. En cas d'échec, le runtime renvoie
// {"success": false, "error": {...}} au modèle.
type ToolHandler interface {
	Execute(ctx context.Context, tc ToolContext, args json.RawMessage) (output any, err *Error)
}

// ToolHandlerFunc adapte une fonction en ToolHandler.
type ToolHandlerFunc func(ctx context.Context, tc ToolContext, args json.RawMessage) (any, *Error)

func (f ToolHandlerFunc) Execute(ctx context.Context, tc ToolContext, args json.RawMessage) (any, *Error) {
	return f(ctx, tc, args)
}

// Tool associe une définition et son implémentation.
type Tool struct {
	Definition ToolDefinition
	Handler    ToolHandler
	// Mutating indique que le tool a un effet réel (réservation, annulation…).
	// Ses succès sont exposés à l'application hôte comme "effets" confirmés.
	Mutating bool
}

// Skill est un ensemble cohérent d'instructions et de tools, sans aucune
// connaissance du modèle d'IA qui l'utilisera.
type Skill interface {
	// Name est l'identifiant unique du skill (ex: "prise-de-rendez-vous").
	Name() string
	// Description indique au modèle quand activer le skill.
	Description() string
	// Instructions sont injectées dans le contexte une fois le skill activé.
	Instructions() string
	// Tools sont exposés au modèle une fois le skill activé.
	Tools() []Tool
}
