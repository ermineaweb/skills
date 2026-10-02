package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"skills/model"
	"skills/types"
)

func TestGenerateTranslatesRequestAndResponse(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("chemin = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("Authorization manquant")
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":null,
			"tool_calls":[{"id":"call_1","type":"function","function":{"name":"rechercher_disponibilites","arguments":"{\"date_debut\":\"2026-10-01T12:00:00+02:00\"}"}}]}}]}`)
	}))
	defer srv.Close()

	a, err := New(Config{APIKey: "sk-test", BaseURL: srv.URL + "/v1/", Model: "gpt-test"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := a.Generate(context.Background(), model.Request{
		System: "sys",
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "Bonjour"},
			{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "c0", Name: "activer_skill", Arguments: json.RawMessage(`{"nom":"x"}`)}}},
			{Role: model.RoleTool, ToolCallID: "c0", Content: `{"success":true}`},
		},
		Tools: []types.ToolDefinition{{Name: "t", Description: "d", Parameters: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}

	msgs := got["messages"].([]any)
	if len(msgs) != 4 || msgs[0].(map[string]any)["role"] != "system" {
		t.Fatalf("messages mal traduits: %v", msgs)
	}
	assistant := msgs[2].(map[string]any)
	call := assistant["tool_calls"].([]any)[0].(map[string]any)["function"].(map[string]any)
	if call["arguments"] != `{"nom":"x"}` {
		t.Fatalf("arguments doivent être une chaîne JSON, obtenu %v", call["arguments"])
	}
	if msgs[3].(map[string]any)["tool_call_id"] != "c0" {
		t.Fatalf("tool_call_id manquant")
	}
	if got["tools"].([]any)[0].(map[string]any)["type"] != "function" {
		t.Fatalf("tools mal traduits")
	}

	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Name != "rechercher_disponibilites" {
		t.Fatalf("tool call non traduit: %+v", resp.Message)
	}
	if !strings.Contains(string(resp.Message.ToolCalls[0].Arguments), "2026-10-01T12:00:00+02:00") {
		t.Fatalf("arguments perdus: %s", resp.Message.ToolCalls[0].Arguments)
	}
}

func TestGenerateHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid key"}}`)
	}))
	defer srv.Close()
	a, _ := New(Config{BaseURL: srv.URL, Model: "m"})
	if _, err := a.Generate(context.Background(), model.Request{}); err == nil || !strings.Contains(err.Error(), "invalid key") {
		t.Fatalf("erreur attendue, obtenu %v", err)
	}
}

func TestExtraBodyIsMergedWithoutOverridingCoreFields(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer srv.Close()
	a, _ := New(Config{BaseURL: srv.URL, Model: "m", ExtraBody: map[string]any{"reasoning_effort": "none", "model": "pirate"}})
	if _, err := a.Generate(context.Background(), model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "x"}}}); err != nil {
		t.Fatal(err)
	}
	if got["reasoning_effort"] != "none" || got["model"] != "m" {
		t.Fatalf("corps = %v", got)
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("OPENAI_MODEL", "qwen3:4b")
	t.Setenv("OPENAI_EXTRA_BODY", `{"reasoning_effort":"none"}`)
	t.Setenv("OPENAI_TIMEOUT", "5m")
	cfg, err := ConfigFromEnv()
	if err != nil || cfg.Model != "qwen3:4b" || cfg.ExtraBody["reasoning_effort"] != "none" || cfg.Timeout.Minutes() != 5 {
		t.Fatalf("cfg = %+v, err = %v", cfg, err)
	}
	t.Setenv("OPENAI_EXTRA_BODY", `[1]`)
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("OPENAI_EXTRA_BODY invalide accepté")
	}
}

func TestGeminiErrorArrayIsReadable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `[{"error":{"code":404,"message":"model is no longer available"}}]`)
	}))
	defer srv.Close()
	a, _ := New(Config{BaseURL: srv.URL, Model: "m"})
	if _, err := a.Generate(context.Background(), model.Request{}); err == nil || !strings.Contains(err.Error(), "no longer available") {
		t.Fatalf("message d'erreur Gemini attendu, obtenu %v", err)
	}
}

func TestToolCallExtraContentRoundTrip(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":null,
			"tool_calls":[{"id":"call_1","type":"function","function":{"name":"t","arguments":"{}"},
			"extra_content":{"google":{"thought_signature":"sig"}}}]}}]}`)
	}))
	defer srv.Close()
	a, _ := New(Config{BaseURL: srv.URL, Model: "m"})
	resp, err := a.Generate(context.Background(), model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	call := resp.Message.ToolCalls[0]
	if !strings.Contains(string(call.ProviderData), "sig") {
		t.Fatalf("signature perdue: %+v", call)
	}

	// Renvoyée telle quelle au tour suivant.
	if _, err := a.Generate(context.Background(), model.Request{Messages: []model.Message{
		{Role: model.RoleUser, Content: "x"},
		resp.Message,
		{Role: model.RoleTool, ToolCallID: call.ID, Content: "{}"},
	}}); err != nil {
		t.Fatal(err)
	}
	sent := got["messages"].([]any)[1].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	extra, _ := json.Marshal(sent["extra_content"])
	if string(extra) != `{"google":{"thought_signature":"sig"}}` {
		t.Fatalf("extra_content non renvoyé: %s", extra)
	}
}
