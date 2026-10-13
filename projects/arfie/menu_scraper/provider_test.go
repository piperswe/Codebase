package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/grafana/ai-sdk/provider"
)

func TestProviderSelection(t *testing.T) {
	for _, tc := range []struct{ name, model, key string }{
		{"anthropic", "claude-haiku-4-5-20251001", "ANTHROPIC_API_KEY"},
		{"openai", "gpt-5.6-luna", "OPENAI_API_KEY"},
		{"openrouter", "qwen/qwen3.8-flash", "OPENROUTER_API_KEY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, err := resolveProvider(tc.name, "")
			if err != nil || config.model != tc.model || config.keyEnv != tc.key {
				t.Fatalf("config=%+v err=%v", config, err)
			}
			override, err := resolveProvider(tc.name, "custom-model")
			if err != nil || override.newModel("test", http.DefaultClient).ModelID() != "custom-model" {
				t.Fatal("model override ignored")
			}
			s := &Scraper{model: config.newModel("test", http.DefaultClient)}
			if (len(s.providerOptions(true)) > 0) != (tc.name == "openai") {
				t.Fatal("provider options leaked")
			}
			var stdout, stderr bytes.Buffer
			code := runCLI(context.Background(), []string{"-provider", tc.name, "Cafe"}, &stdout, &stderr, func(key string) string {
				if key != "KAGI_API_KEY" && key != tc.key {
					t.Errorf("read another provider's key: %s", key)
				}
				return ""
			})
			if code != 1 || !strings.Contains(stderr.String(), tc.key) || !json.Valid(stdout.Bytes()) {
				t.Fatalf("exit=%d stderr=%s", code, &stderr)
			}
		})
	}
	for _, args := range [][]string{{"-provider", "invalid", "Cafe"}, {"-model", " ", "Cafe"}} {
		if code := runCLI(context.Background(), args, io.Discard, io.Discard, func(string) string { t.Fatal("credentials read for invalid flags"); return "" }); code != 2 {
			t.Fatalf("exit=%d", code)
		}
	}
}

// Exercise the actual Grafana adapters, while intercepting transport before any network I/O.
func TestProviderMediaRequests(t *testing.T) {
	for _, name := range []string{"anthropic", "openrouter"} {
		t.Run(name, func(t *testing.T) {
			config, _ := resolveProvider(name, "")
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				data, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				var body map[string]any
				if err := json.Unmarshal(data, &body); err != nil {
					t.Fatal(err)
				}
				if body["model"] != config.model {
					t.Errorf("model=%v", body["model"])
				}
				for _, content := range []string{base64.StdEncoding.EncodeToString([]byte("test-image")), base64.StdEncoding.EncodeToString([]byte("%PDF-test"))} {
					if !bytes.Contains(data, []byte(content)) {
						t.Errorf("missing media: %s", data)
					}
				}
				if body["store"] != nil {
					t.Error("OpenAI options leaked")
				}
				if name == "anthropic" {
					if r.URL.Host != "api.anthropic.com" || r.URL.Path != "/v1/messages" || r.Header.Get("X-Api-Key") != "test-key" {
						t.Errorf("request %s", r.URL)
					}
					if body["output_config"] == nil {
						t.Errorf("missing schema: %s", data)
					}
					return response(r, 200, "application/json", []byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-haiku-4-5-20251001","content":[{"type":"text","text":"{}"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)), nil
				}
				if r.URL.String() != "https://openrouter.ai/api/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
					t.Errorf("request %s", r.URL)
				}
				if body["response_format"] == nil || !bytes.Contains(data, []byte("file_data")) || !bytes.Contains(data, []byte("image_url")) {
					t.Errorf("missing schema or media fields: %s", data)
				}
				return response(r, 200, "application/json", []byte(`{"id":"chat_1","model":"qwen/qwen3.8-flash","choices":[{"message":{"role":"assistant","content":"{}"},"finish_reason":"stop"}]}`)), nil
			})}
			_, err := config.newModel("test-key", client).DoGenerate(context.Background(), provider.CallOptions{
				MaxOutputTokens: new(100),
				Prompt:          []provider.Message{provider.NewUserMessage(provider.TextPart("Extract menu"), provider.FilePart("image/png", provider.DataContent{Bytes: []byte("test-image")}), provider.FilePart("application/pdf", provider.DataContent{Bytes: []byte("%PDF-test")}))},
				ResponseFormat:  &provider.ResponseFormat{Type: provider.ResponseFormatJSON, Schema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
			})
			if err != nil || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}
