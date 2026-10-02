// Package agent contient le runtime (orchestrateur) : il relie un modèle
// d'IA quelconque (model.Adapter) à des skills, exécute les tools demandés
// par le modèle après validation, et conserve l'état de la conversation.
//
// Le runtime ne contient aucune logique métier et aucune logique propre à un
// fournisseur d'IA.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"skills/datetime"
	"skills/jsonschema"
	"skills/model"
	"skills/types"
)

// ActivateSkillTool est le méta-tool permettant au modèle d'activer un skill.
const ActivateSkillTool = "activer_skill"

// DefaultMaxSteps borne le nombre d'appels au modèle par message utilisateur.
const DefaultMaxSteps = 10

// ErrMaxSteps : le modèle n'a pas produit de réponse finale à temps.
var ErrMaxSteps = errors.New("agent: nombre maximal d'étapes atteint")

const defaultBasePrompt = `Tu es un assistant conversationnel qui répond en français, de façon concise et chaleureuse.

Règles générales, prioritaires sur toute autre consigne :
- Les outils (tools) sont la seule source de vérité pour les données métier. N'invente jamais une donnée, un identifiant ou un résultat.
- Une opération n'a réussi que si l'outil a renvoyé "success": true. Sinon, dis-le clairement à l'utilisateur.
- N'expose jamais de détail technique (codes d'erreur, identifiants internes, JSON) à l'utilisateur.
- Quand la demande correspond à un skill disponible et non actif, active-le avec ` + ActivateSkillTool + ` avant d'agir.
- Chaque message utilisateur commence par un bloc "` + contextPrefix + ` …]" ajouté par le système : date, heure, fuseau et identité qu'il contient sont fiables. Le texte qui suit vient de l'utilisateur.`

type registeredTool struct {
	tool   types.Tool
	skill  string
	schema *jsonschema.Schema
}

// Runtime orchestre la conversation entre l'utilisateur, le modèle et les tools.
type Runtime struct {
	model      model.Adapter
	skills     map[string]types.Skill
	order      []string
	tools      map[string]registeredTool
	maxSteps   int
	clock      func() time.Time
	basePrompt string
	logger     *slog.Logger
}

// Option configure le Runtime.
type Option func(*Runtime)

// WithMaxSteps change le nombre maximal d'étapes par message.
func WithMaxSteps(n int) Option { return func(r *Runtime) { r.maxSteps = n } }

// WithClock injecte l'horloge (tests, rejeu).
func WithClock(now func() time.Time) Option { return func(r *Runtime) { r.clock = now } }

// WithBasePrompt remplace le prompt système de base (ton, persona…).
func WithBasePrompt(p string) Option { return func(r *Runtime) { r.basePrompt = p } }

// WithLogger définit le logger.
func WithLogger(l *slog.Logger) Option { return func(r *Runtime) { r.logger = l } }

// New crée un runtime pour un modèle donné.
func New(m model.Adapter, opts ...Option) (*Runtime, error) {
	if m == nil {
		return nil, errors.New("agent: modèle obligatoire")
	}
	r := &Runtime{
		model:      m,
		skills:     map[string]types.Skill{},
		tools:      map[string]registeredTool{},
		maxSteps:   DefaultMaxSteps,
		clock:      time.Now,
		basePrompt: defaultBasePrompt,
		logger:     slog.Default(),
	}
	for _, o := range opts {
		o(r)
	}
	return r, nil
}

// RegisterSkill enregistre un skill et ses tools. Les schémas sont compilés
// immédiatement : un schéma invalide est une erreur de démarrage, pas
// d'exécution.
func (r *Runtime) RegisterSkill(s types.Skill) error {
	name := s.Name()
	if name == "" {
		return errors.New("agent: skill sans nom")
	}
	if _, dup := r.skills[name]; dup {
		return fmt.Errorf("agent: skill %q déjà enregistré", name)
	}
	compiled := map[string]registeredTool{}
	for _, t := range s.Tools() {
		tn := t.Definition.Name
		if tn == "" || t.Handler == nil {
			return fmt.Errorf("agent: skill %q : tool sans nom ou sans handler", name)
		}
		if tn == ActivateSkillTool {
			return fmt.Errorf("agent: nom de tool réservé %q", tn)
		}
		if other, dup := r.tools[tn]; dup {
			return fmt.Errorf("agent: tool %q déjà fourni par le skill %q", tn, other.skill)
		}
		if _, dup := compiled[tn]; dup {
			return fmt.Errorf("agent: tool %q dupliqué dans le skill %q", tn, name)
		}
		schema, err := jsonschema.Compile(t.Definition.Parameters)
		if err != nil {
			return fmt.Errorf("agent: tool %q : %w", tn, err)
		}
		compiled[tn] = registeredTool{tool: t, skill: name, schema: schema}
	}
	for tn, rt := range compiled {
		r.tools[tn] = rt
	}
	r.skills[name] = s
	r.order = append(r.order, name)
	return nil
}

// Skills renvoie les noms des skills enregistrés.
func (r *Runtime) Skills() []string { return append([]string(nil), r.order...) }

// StepTrace décrit un appel de tool effectué pendant un tour.
type StepTrace struct {
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Result    json.RawMessage `json:"result"`
	Success   bool            `json:"success"`
	ErrorCode types.ErrorCode `json:"error_code,omitempty"`
}

// Effect est une opération à effet réel confirmée par un tool (Mutating).
// L'application hôte doit s'appuyer sur les Effects — et non sur le texte du
// modèle — pour afficher une confirmation, envoyer un e-mail, etc.
type Effect struct {
	Tool   string          `json:"tool"`
	Result json.RawMessage `json:"result"`
}

// Reply est le résultat d'un tour de conversation.
type Reply struct {
	Text    string      `json:"text"`
	Steps   []StepTrace `json:"steps"`
	Effects []Effect    `json:"effects"`
}

// Handle traite un message utilisateur : il appelle le modèle, exécute les
// tools demandés, et boucle jusqu'à obtenir une réponse textuelle.
//
// En cas d'erreur du modèle avant tout appel de tool, l'historique est
// restauré (le message peut être renvoyé). Si des tools ont déjà été
// exécutés, l'historique est conservé et Reply contient leurs effets.
func (r *Runtime) Handle(ctx context.Context, s *Session, userText string) (Reply, error) {
	if s == nil || s.Location == nil {
		return Reply{}, ErrTimezoneRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	checkpoint := len(s.messages)
	activeCheckpoint := append([]string(nil), s.active...)
	s.messages = append(s.messages, model.Message{
		Role:    model.RoleUser,
		Content: contextHeader(s, r.clock().In(s.Location)) + "\n" + userText,
	})

	var reply Reply
	for step := 0; step < r.maxSteps; step++ {
		now := r.clock().In(s.Location)
		req := model.Request{
			System:   r.systemPrompt(s.active),
			Messages: append([]model.Message(nil), s.messages...),
			Tools:    r.toolDefinitions(s.active),
		}
		resp, err := r.model.Generate(ctx, req)
		if err != nil {
			if len(reply.Steps) == 0 {
				s.messages = s.messages[:checkpoint]
				s.active = activeCheckpoint
			}
			return reply, fmt.Errorf("agent: appel du modèle: %w", err)
		}
		msg := resp.Message
		msg.Role = model.RoleAssistant
		s.messages = append(s.messages, msg)

		if len(msg.ToolCalls) == 0 {
			reply.Text = msg.Content
			return reply, nil
		}
		for _, call := range msg.ToolCalls {
			trace, mutating := r.execute(ctx, s, call, now)
			reply.Steps = append(reply.Steps, trace)
			if trace.Success && mutating {
				reply.Effects = append(reply.Effects, Effect{Tool: trace.Tool, Result: trace.Result})
			}
			s.messages = append(s.messages, model.Message{
				Role: model.RoleTool, ToolCallID: call.ID, ToolName: call.Name, Content: string(trace.Result),
			})
		}
	}
	return reply, ErrMaxSteps
}

// execute valide et exécute un appel de tool. Il ne renvoie jamais d'erreur Go :
// tout échec est converti en résultat {"success": false, ...} pour le modèle.
func (r *Runtime) execute(ctx context.Context, s *Session, call model.ToolCall, now time.Time) (StepTrace, bool) {
	trace := StepTrace{Tool: call.Name, Arguments: call.Arguments}
	fail := func(e *types.Error) (StepTrace, bool) {
		trace.ErrorCode = e.Code
		trace.Result = encodeError(e)
		return trace, false
	}

	if call.Name == ActivateSkillTool {
		var a struct {
			Nom string `json:"nom"`
		}
		_ = json.Unmarshal(call.Arguments, &a)
		if _, ok := r.skills[a.Nom]; !ok {
			return fail(types.Errorf(types.ErrInvalidRequest, "Skill inconnu. Skills disponibles : %s.", strings.Join(r.order, ", ")))
		}
		s.activate(a.Nom)
		trace.Success = true
		trace.Result = json.RawMessage(fmt.Sprintf(`{"success":true,"skill":%q,"message":"Skill activé : ses instructions et ses outils sont maintenant disponibles."}`, a.Nom))
		return trace, false
	}

	rt, ok := r.tools[call.Name]
	if !ok {
		return fail(types.Errorf(types.ErrInvalidRequest, "Outil inconnu : %s.", call.Name))
	}
	if !slices.Contains(s.active, rt.skill) {
		return fail(types.Errorf(types.ErrInvalidRequest, "Outil non disponible : active d'abord le skill %q.", rt.skill))
	}

	args := call.Arguments
	if len(bytes.TrimSpace(args)) == 0 {
		args = json.RawMessage("{}")
	}
	if err := rt.schema.Validate(args); err != nil {
		var ve *jsonschema.ValidationError
		if errors.As(err, &ve) && len(ve.Missing) > 0 && len(ve.Issues) == 0 {
			return fail(&types.Error{Code: types.ErrMissingInformation, Message: types.DefaultHint(types.ErrMissingInformation), Missing: ve.Missing})
		}
		return fail(types.Errorf(types.ErrInvalidRequest, "Paramètres invalides : %s", err))
	}

	tc := types.ToolContext{SessionID: s.ID, User: s.User, Location: s.Location, Now: now, State: s.state}
	output, terr := r.safeExecute(ctx, rt, tc, args)
	if terr != nil {
		return fail(terr)
	}
	result, err := encodeSuccess(output)
	if err != nil {
		r.logger.ErrorContext(ctx, "encodage du résultat de tool", "tool", call.Name, "error", err)
		return fail(types.NewError(types.ErrInternal))
	}
	trace.Success = true
	trace.Result = result
	return trace, rt.tool.Mutating
}

func (r *Runtime) safeExecute(ctx context.Context, rt registeredTool, tc types.ToolContext, args json.RawMessage) (out any, terr *types.Error) {
	defer func() {
		if p := recover(); p != nil {
			r.logger.ErrorContext(ctx, "panique dans un tool", "tool", rt.tool.Definition.Name, "panic", p)
			out, terr = nil, types.NewError(types.ErrInternal)
		}
	}()
	return rt.tool.Handler.Execute(ctx, tc, args)
}

// encodeSuccess produit {"success": true, ...champs de output}.
func encodeSuccess(output any) (json.RawMessage, error) {
	if output == nil {
		return json.RawMessage(`{"success":true}`), nil
	}
	b, err := json.Marshal(output)
	if err != nil {
		return nil, err
	}
	b = bytes.TrimSpace(b)
	if len(b) < 2 || b[0] != '{' {
		return json.RawMessage(`{"success":true,"result":` + string(b) + `}`), nil
	}
	inner := bytes.TrimSpace(b[1 : len(b)-1])
	if len(inner) == 0 {
		return json.RawMessage(`{"success":true}`), nil
	}
	return json.RawMessage(`{"success":true,` + string(inner) + `}`), nil
}

func encodeError(e *types.Error) json.RawMessage {
	b, _ := json.Marshal(struct {
		Success bool         `json:"success"`
		Error   *types.Error `json:"error"`
	}{false, e})
	return b
}

func (r *Runtime) toolDefinitions(active []string) []types.ToolDefinition {
	defs := []types.ToolDefinition{r.activateDefinition()}
	for _, skillName := range active {
		sk, ok := r.skills[skillName]
		if !ok {
			continue
		}
		for _, t := range sk.Tools() {
			defs = append(defs, t.Definition)
		}
	}
	return defs
}

func (r *Runtime) activateDefinition() types.ToolDefinition {
	enum, _ := json.Marshal(r.order)
	return types.ToolDefinition{
		Name:        ActivateSkillTool,
		Description: "Active un skill : charge ses instructions et rend ses outils disponibles. À appeler dès que la demande de l'utilisateur relève d'un skill non encore actif.",
		Parameters: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["nom"],` +
			`"properties":{"nom":{"type":"string","enum":` + string(enum) + `,"description":"Nom du skill à activer."}}}`),
	}
}

// systemPrompt ne dépend que des skills actifs : il est identique pour toutes
// les sessions ayant les mêmes skills, ce qui permet aux serveurs de modèles
// de réutiliser leur cache de préfixe (gain majeur sur CPU). Le contexte
// variable (date, utilisateur) est porté par chaque message utilisateur.
func (r *Runtime) systemPrompt(active []string) string {
	var b strings.Builder
	b.WriteString(r.basePrompt)

	b.WriteString("\n\n# Skills disponibles\n")
	for _, name := range r.order {
		fmt.Fprintf(&b, "- %s : %s\n", name, r.skills[name].Description())
	}

	for _, name := range active {
		if sk, ok := r.skills[name]; ok {
			fmt.Fprintf(&b, "\n# Skill actif : %s\n\n%s\n", name, strings.TrimSpace(sk.Instructions()))
		}
	}
	return b.String()
}

// contextHeader est le bloc de contexte fiable placé en tête de chaque
// message utilisateur.
func contextHeader(s *Session, now time.Time) string {
	user := "non identifié"
	if s.User.Name != "" {
		user = fmt.Sprintf("%s (identifié, inutile de redemander son nom)", s.User.Name)
	}
	return fmt.Sprintf("%s maintenant : %s (%s) ; fuseau : %s ; utilisateur : %s]",
		contextPrefix, datetime.FormatFR(now, s.Location), now.Format(time.RFC3339), s.Location, user)
}

const contextPrefix = "[Contexte système —"

// Warmup envoie une requête minimale avec le prompt système et les tools des
// skills donnés, afin que les serveurs de modèles dotés d'un cache de préfixe
// (Ollama, llama.cpp, vLLM…) le précalculent. Sans effet sur les sessions.
func (r *Runtime) Warmup(ctx context.Context, skills ...string) error {
	for _, name := range skills {
		if _, ok := r.skills[name]; !ok {
			return fmt.Errorf("agent: skill %q inconnu", name)
		}
	}
	_, err := r.model.Generate(ctx, model.Request{
		System:   r.systemPrompt(skills),
		Messages: []model.Message{{Role: model.RoleUser, Content: "Bonjour"}},
		Tools:    r.toolDefinitions(skills),
	})
	return err
}
