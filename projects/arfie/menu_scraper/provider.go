package main

import (
	"fmt"
	"net/http"

	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/grafana/ai-sdk/provider"
	"github.com/grafana/ai-sdk/providers/anthropic"
	"github.com/grafana/ai-sdk/providers/openai"
	openaicompatible "github.com/grafana/ai-sdk/providers/openai-compatible"
	"github.com/openai/openai-go/v3/option"
)

type providerConfig struct {
	name, model, keyEnv string
}

func resolveProvider(name, model string) (providerConfig, error) {
	c := providerConfig{name: name}
	switch name {
	case "anthropic":
		c.model, c.keyEnv = "claude-haiku-4-5-20251001", "ANTHROPIC_API_KEY"
	case "openai":
		c.model, c.keyEnv = "gpt-5.6-luna", "OPENAI_API_KEY"
	case "openrouter":
		c.model, c.keyEnv = "qwen/qwen3.8-flash", "OPENROUTER_API_KEY"
	default:
		return c, fmt.Errorf("unknown provider %q: choose anthropic, openai, or openrouter", name)
	}
	if model != "" {
		c.model = model
	}
	return c, nil
}

func (c providerConfig) newModel(key string, client *http.Client) provider.LanguageModel {
	switch c.name {
	case "anthropic":
		return anthropic.New(key, c.model, anthropic.WithRequestOptions(anthropicoption.WithHTTPClient(client), anthropicoption.WithMaxRetries(0)))
	case "openrouter":
		return openaicompatible.New(c.model,
			openaicompatible.WithProviderName("openrouter"),
			openaicompatible.WithBaseURL("https://openrouter.ai/api/v1"),
			openaicompatible.WithAPIKey(key),
			openaicompatible.WithHTTPClient(client),
			openaicompatible.WithStructuredOutputs(true))
	default: // resolveProvider validates the name before construction.
		return openai.NewResponses(key, c.model, openai.WithRequestOptions(option.WithHTTPClient(client), option.WithMaxRetries(0)))
	}
}

func (s *Scraper) providerOptions(research bool) []provider.ProviderOption {
	if s.model.Provider() != "openai" {
		return nil
	}
	opts := openai.OpenAIResponsesOptions{Store: new(false)}
	if research {
		opts.ParallelToolCalls = new(false)
	}
	return []provider.ProviderOption{opts}
}
