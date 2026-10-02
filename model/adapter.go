// Package model définit l'interface commune à tous les modèles d'IA.
//
// Le runtime et les skills ne manipulent que ces types. Les spécificités d'un
// fournisseur (format des messages, des tools, gestion du prompt système…)
// sont confinées dans son adapter (ex: model/openai).
package model

import (
	"context"
	"encoding/json"

	"skills/types"
)

// Role d'un message dans la conversation.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ToolCall est une demande d'exécution de tool émise par le modèle.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	// ProviderData : données opaques propres au fournisseur, que l'adapter
	// doit lui renvoyer telles quelles dans l'historique (ex: signature de
	// réflexion de Gemini). Le runtime ne les lit pas.
	ProviderData json.RawMessage `json:"provider_data,omitempty"`
}

// Message est un élément de l'historique, indépendant du fournisseur.
//
//   - RoleUser      : Content = texte de l'utilisateur
//   - RoleAssistant : Content = texte et/ou ToolCalls
//   - RoleTool      : Content = résultat JSON du tool, ToolCallID = appel associé
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolName   string     `json:"tool_name,omitempty"`
}

// Request est ce que le runtime envoie au modèle à chaque étape.
// Le prompt système est séparé des messages : certains fournisseurs le
// placent dans l'historique, d'autres dans un champ dédié.
type Request struct {
	System   string
	Messages []Message
	Tools    []types.ToolDefinition
}

// Response est la réponse du modèle : un message assistant contenant du
// texte final et/ou des appels de tools.
type Response struct {
	Message Message
}

// Adapter est l'unique point de contact entre le runtime et un modèle d'IA.
type Adapter interface {
	Generate(ctx context.Context, req Request) (Response, error)
}
