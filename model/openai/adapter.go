// Package openai implémente model.Adapter pour l'API Chat Completions
// d'OpenAI et les API compatibles (Ollama, vLLM, Mistral, LM Studio…),
// avec la bibliothèque standard uniquement.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"skills/model"
)

// Config de l'adapter.
type Config struct {
	APIKey  string
	BaseURL string // défaut : https://api.openai.com/v1
	Model   string
	// ExtraBody : paramètres ajoutés tels quels à chaque requête, pour les
	// options propres à un serveur ou à un modèle (ex: {"reasoning_effort":
	// "none"}). Ne peut pas remplacer model, messages ni tools.
	ExtraBody map[string]any
	// Timeout d'un appel au modèle (défaut : 60 s). Ignoré si HTTPClient est fourni.
	Timeout time.Duration
	// HTTPClient optionnel (proxy, tests).
	HTTPClient *http.Client
	// RateLimitWait : attente totale maximale, par appel, quand l'API répond
	// 429 (quota par minute des offres gratuites). L'appel est retenté après
	// le délai indiqué par l'API. Défaut : 2 min ; négatif : aucun nouvel
	// essai.
	RateLimitWait time.Duration
}

// DefaultRateLimitWait est la valeur par défaut de Config.RateLimitWait.
const DefaultRateLimitWait = 2 * time.Minute

// ConfigFromEnv lit la configuration dans l'environnement :
// OPENAI_API_KEY, OPENAI_BASE_URL, OPENAI_MODEL, OPENAI_EXTRA_BODY (objet
// JSON), OPENAI_TIMEOUT et OPENAI_RATE_LIMIT_WAIT (durées Go, ex: "5m").
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		APIKey:  os.Getenv("OPENAI_API_KEY"),
		BaseURL: os.Getenv("OPENAI_BASE_URL"),
		Model:   os.Getenv("OPENAI_MODEL"),
	}
	if raw := strings.TrimSpace(os.Getenv("OPENAI_EXTRA_BODY")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg.ExtraBody); err != nil {
			return Config{}, fmt.Errorf("openai: OPENAI_EXTRA_BODY doit être un objet JSON: %w", err)
		}
	}
	if raw := strings.TrimSpace(os.Getenv("OPENAI_TIMEOUT")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return Config{}, fmt.Errorf("openai: OPENAI_TIMEOUT invalide %q (ex: 5m)", raw)
		}
		cfg.Timeout = d
	}
	if raw := strings.TrimSpace(os.Getenv("OPENAI_RATE_LIMIT_WAIT")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("openai: OPENAI_RATE_LIMIT_WAIT invalide %q (ex: 2m, 0s pour ne pas réessayer)", raw)
		}
		if d == 0 {
			d = -1
		}
		cfg.RateLimitWait = d
	}
	return cfg, nil
}

// Adapter traduit model.Request au format Chat Completions.
type Adapter struct {
	cfg   Config
	sleep func(context.Context, time.Duration) error
}

var _ model.Adapter = (*Adapter)(nil)

// New crée un adapter. Model est obligatoire.
func New(cfg Config) (*Adapter, error) {
	if cfg.Model == "" {
		return nil, errors.New("openai: nom du modèle obligatoire")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.Timeout == 0 {
		cfg.Timeout = 60 * time.Second
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: cfg.Timeout}
	}
	if cfg.RateLimitWait == 0 {
		cfg.RateLimitWait = DefaultRateLimitWait
	}
	return &Adapter{cfg: cfg, sleep: sleep}, nil
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// ---- Format fil (wire format) spécifique au fournisseur ----

type wireFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // chaîne JSON, spécificité OpenAI
}

type wireToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function wireFunctionCall `json:"function"`
	// ExtraContent : extension non standard (Gemini y place la signature de
	// réflexion, à renvoyer au tour suivant sous peine d'erreur 400).
	ExtraContent json.RawMessage `json:"extra_content,omitempty"`
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    *string        `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type wireFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type wireTool struct {
	Type     string       `json:"type"`
	Function wireFunction `json:"function"`
}

type wireRequest struct {
	Model    string        `json:"model"`
	Messages []wireMessage `json:"messages"`
	Tools    []wireTool    `json:"tools,omitempty"`
}

type wireError struct {
	Message string `json:"message"`
}

type wireResponse struct {
	Choices []struct {
		Message wireMessage `json:"message"`
	} `json:"choices"`
	Error *wireError `json:"error"`
}

// Generate implémente model.Adapter.
func (a *Adapter) Generate(ctx context.Context, req model.Request) (model.Response, error) {
	body, err := a.encode(req)
	if err != nil {
		return model.Response{}, err
	}
	var waited time.Duration
	for attempt := 0; ; attempt++ {
		resp, data, err := a.post(ctx, body)
		if err != nil {
			return model.Response{}, err
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			d := retryDelay(resp.Header, data, attempt)
			if waited+d <= a.cfg.RateLimitWait {
				slog.InfoContext(ctx, "quota du modèle atteint, nouvel essai", "attente", d.Round(time.Second), "essai", attempt+1)
				if err := a.sleep(ctx, d); err != nil {
					return model.Response{}, fmt.Errorf("openai: attente du quota interrompue: %w", err)
				}
				waited += d
				continue
			}
		}
		return a.decode(resp, data)
	}
}

func (a *Adapter) post(ctx context.Context, body []byte) (*http.Response, []byte, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if a.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+a.cfg.APIKey)
	}
	resp, err := a.cfg.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, nil, fmt.Errorf("openai: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, nil, fmt.Errorf("openai: lecture de la réponse: %w", err)
	}
	return resp, data, nil
}

// reRetryIn lit le délai indiqué dans le message d'erreur : « Please retry
// in 10.84s » (Gemini), « Please try again in 1m2.5s » (Groq).
var reRetryIn = regexp.MustCompile(`(?i)(?:retry|try again) in ((?:\d+h)?(?:\d+m)?[\d.]+(?:ms|s))`)

// retryDelay renvoie l'attente avant un nouvel essai après une réponse 429 :
// le délai indiqué par l'API (en-tête Retry-After ou message), avec une
// seconde de marge, sinon 5 s, 10 s, 20 s… selon le nombre d'essais.
func retryDelay(h http.Header, data []byte, attempt int) time.Duration {
	if n, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After"))); err == nil && n >= 0 {
		return time.Duration(n)*time.Second + time.Second
	}
	if m := reRetryIn.FindSubmatch(data); m != nil {
		if d, err := time.ParseDuration(string(m[1])); err == nil {
			return d + time.Second
		}
	}
	return 5 * time.Second << min(attempt, 4)
}

func (a *Adapter) decode(resp *http.Response, data []byte) (model.Response, error) {
	if resp.StatusCode >= 300 {
		return model.Response{}, fmt.Errorf("openai: HTTP %d: %s", resp.StatusCode, errorMessage(data, resp.StatusCode))
	}
	var wr wireResponse
	if err := json.Unmarshal(data, &wr); err != nil {
		return model.Response{}, fmt.Errorf("openai: réponse invalide (HTTP %d)", resp.StatusCode)
	}
	if wr.Error != nil {
		return model.Response{}, fmt.Errorf("openai: %s", wr.Error.Message)
	}
	if len(wr.Choices) == 0 {
		return model.Response{}, errors.New("openai: réponse sans choix")
	}
	return model.Response{Message: fromWire(wr.Choices[0].Message)}, nil
}

// errorMessage extrait le message d'une réponse d'erreur : {"error":{…}}
// (OpenAI) ou [{"error":{…}}] (Gemini), sinon le début du corps brut.
func errorMessage(data []byte, status int) string {
	var one struct{ Error *wireError }
	if json.Unmarshal(data, &one) == nil && one.Error != nil && one.Error.Message != "" {
		return one.Error.Message
	}
	var many []struct{ Error *wireError }
	if json.Unmarshal(data, &many) == nil && len(many) > 0 && many[0].Error != nil && many[0].Error.Message != "" {
		return many[0].Error.Message
	}
	if body := strings.TrimSpace(string(data)); body != "" {
		if len(body) > 300 {
			body = body[:300] + "…"
		}
		return body
	}
	return http.StatusText(status)
}

// encode sérialise la requête et y ajoute ExtraBody.
func (a *Adapter) encode(req model.Request) ([]byte, error) {
	body, err := json.Marshal(a.toWire(req))
	if err != nil || len(a.cfg.ExtraBody) == 0 {
		return body, err
	}
	var merged map[string]any
	if err := json.Unmarshal(body, &merged); err != nil {
		return nil, err
	}
	for k, v := range a.cfg.ExtraBody {
		if _, core := merged[k]; core && (k == "model" || k == "messages" || k == "tools") {
			continue
		}
		merged[k] = v
	}
	return json.Marshal(merged)
}

func (a *Adapter) toWire(req model.Request) wireRequest {
	wr := wireRequest{Model: a.cfg.Model}
	if req.System != "" {
		sys := req.System
		wr.Messages = append(wr.Messages, wireMessage{Role: "system", Content: &sys})
	}
	for _, m := range req.Messages {
		content := m.Content
		wm := wireMessage{Role: string(m.Role), Content: &content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			args := string(tc.Arguments)
			if args == "" {
				args = "{}"
			}
			wm.ToolCalls = append(wm.ToolCalls, wireToolCall{
				ID: tc.ID, Type: "function", Function: wireFunctionCall{Name: tc.Name, Arguments: args},
				ExtraContent: tc.ProviderData,
			})
		}
		if m.Role == model.RoleAssistant && len(m.ToolCalls) > 0 && m.Content == "" {
			wm.Content = nil
		}
		wr.Messages = append(wr.Messages, wm)
	}
	for _, t := range req.Tools {
		wr.Tools = append(wr.Tools, wireTool{Type: "function", Function: wireFunction{
			Name: t.Name, Description: t.Description, Parameters: t.Parameters,
		}})
	}
	return wr
}

func fromWire(wm wireMessage) model.Message {
	msg := model.Message{Role: model.RoleAssistant}
	if wm.Content != nil {
		msg.Content = *wm.Content
	}
	for _, tc := range wm.ToolCalls {
		args := json.RawMessage(tc.Function.Arguments)
		if strings.TrimSpace(tc.Function.Arguments) == "" {
			args = json.RawMessage("{}")
		} else if !json.Valid(args) {
			// Transmis tel quel : la validation du runtime le rejettera
			// proprement et le modèle pourra se corriger.
			args, _ = json.Marshal(tc.Function.Arguments)
		}
		msg.ToolCalls = append(msg.ToolCalls, model.ToolCall{
			ID: tc.ID, Name: tc.Function.Name, Arguments: args, ProviderData: tc.ExtraContent,
		})
	}
	return msg
}
