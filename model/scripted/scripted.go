// Package scripted fournit un model.Adapter déterministe, piloté par un
// script d'étapes. Il sert aux tests et à la démonstration : il permet de
// vérifier le comportement du runtime, des tools et du calendrier sans
// appeler de LLM.
//
// Une étape peut lire l'historique (résultats de tools précédents) pour
// construire sa réponse : c'est ainsi qu'un test reproduit un modèle qui
// réutilise un slot_id renvoyé par le calendrier au lieu de l'inventer.
package scripted

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"skills/model"
)

// Step produit la réponse du modèle à partir de la requête reçue.
type Step func(req model.Request) (model.Message, error)

// ErrScriptExhausted est renvoyée quand le modèle est appelé plus de fois
// que prévu par le script.
var ErrScriptExhausted = errors.New("scripted: plus aucune étape dans le script")

// Model est un adapter scripté.
type Model struct {
	mu       sync.Mutex
	steps    []Step
	seq      int
	requests []model.Request
}

var _ model.Adapter = (*Model)(nil)

// New crée un modèle scripté.
func New(steps ...Step) *Model {
	return &Model{steps: steps}
}

// Push ajoute des étapes à la fin du script.
func (m *Model) Push(steps ...Step) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.steps = append(m.steps, steps...)
}

// Remaining renvoie le nombre d'étapes non consommées.
func (m *Model) Remaining() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.steps)
}

// Requests renvoie les requêtes reçues (pour inspection en test).
func (m *Model) Requests() []model.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]model.Request(nil), m.requests...)
}

// Generate implémente model.Adapter.
func (m *Model) Generate(_ context.Context, req model.Request) (model.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, req)
	if len(m.steps) == 0 {
		return model.Response{}, ErrScriptExhausted
	}
	step := m.steps[0]
	m.steps = m.steps[1:]
	msg, err := step(req)
	if err != nil {
		return model.Response{}, err
	}
	msg.Role = model.RoleAssistant
	for i := range msg.ToolCalls {
		if msg.ToolCalls[i].ID == "" {
			m.seq++
			msg.ToolCalls[i].ID = fmt.Sprintf("call_%d", m.seq)
		}
	}
	return model.Response{Message: msg}, nil
}

// Say répond par un texte final.
func Say(text string) Step {
	return func(model.Request) (model.Message, error) {
		return model.Message{Content: text}, nil
	}
}

// SayFn répond par un texte calculé à partir de l'historique.
func SayFn(fn func(req model.Request) (string, error)) Step {
	return func(req model.Request) (model.Message, error) {
		text, err := fn(req)
		return model.Message{Content: text}, err
	}
}

// Call appelle un tool avec des arguments fixes.
func Call(name string, args any) Step {
	return CallFn(name, func(model.Request) (any, error) { return args, nil })
}

// CallFn appelle un tool avec des arguments calculés à partir de l'historique.
func CallFn(name string, fn func(req model.Request) (any, error)) Step {
	return func(req model.Request) (model.Message, error) {
		args, err := fn(req)
		if err != nil {
			return model.Message{}, err
		}
		raw, err := json.Marshal(args)
		if err != nil {
			return model.Message{}, err
		}
		return model.Message{ToolCalls: []model.ToolCall{{Name: name, Arguments: raw}}}, nil
	}
}

// LastToolResult renvoie le dernier résultat (décodé) du tool nommé dans
// l'historique de la requête.
func LastToolResult(req model.Request, toolName string) (map[string]any, bool) {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if msg.Role == model.RoleTool && msg.ToolName == toolName {
			var out map[string]any
			if err := json.Unmarshal([]byte(msg.Content), &out); err != nil {
				return nil, false
			}
			return out, true
		}
	}
	return nil, false
}
