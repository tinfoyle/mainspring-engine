// Package modelgatewayapi composes the provider-neutral model gateway with
// concrete provider adapters. It intentionally has no Account database or
// runner credentials.
package modelgatewayapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/openairesponses"
	"github.com/tinfoyle/spyglass-engine/internal/application/modelgateway"
	transport "github.com/tinfoyle/spyglass-engine/internal/transport/modelgatewayapi"
)

type Config struct {
	OpenAIAPIKey   string
	OpenAIOrigin   string
	OpenAIPricing  string
	OpenAIClient   *http.Client
	KimiAPIKey     string
	KimiOrigin     string
	KimiPricing    string
	KimiClient     *http.Client
	MaxRequestBody int64
}

type Server struct{ Handler http.Handler }

func New(config Config, logger *slog.Logger) (*Server, error) {
	if logger == nil || (config.OpenAIAPIKey == "" && config.KimiAPIKey == "") {
		return nil, errors.New("model gateway configuration is required")
	}
	definitions := make([]modelgateway.Definition, 0, 2)
	for _, target := range []struct {
		name, key, origin, pricing string
		client                     *http.Client
	}{
		{"openai", config.OpenAIAPIKey, config.OpenAIOrigin, config.OpenAIPricing, config.OpenAIClient},
		{"kimi", config.KimiAPIKey, config.KimiOrigin, config.KimiPricing, config.KimiClient},
	} {
		if target.key == "" {
			if target.pricing != "" {
				return nil, errors.New("model gateway provider credential is missing")
			}
			continue
		}
		if target.name == "kimi" && target.origin == "" {
			target.origin = "https://api.moonshot.ai"
		}
		provider, err := openairesponses.New(openairesponses.Config{APIKey: target.key, Origin: target.origin, HTTPClient: target.client, StructuredOutputViaTool: target.name == "kimi"})
		if err != nil {
			return nil, errors.New("model gateway provider configuration is invalid")
		}
		pricing, err := modelgateway.ParsePricingJSON(target.pricing)
		if err != nil {
			return nil, errors.New("model gateway pricing configuration is invalid")
		}
		definitions = append(definitions, modelgateway.Definition{Name: target.name, Timeout: modelgateway.MaximumProviderTimeout, Provider: provider, Pricing: pricing})
	}
	service, err := modelgateway.New(definitions)
	if err != nil {
		return nil, err
	}
	maxBody := config.MaxRequestBody
	if maxBody == 0 {
		maxBody = modelgateway.MaximumRequestBytes
	}
	api, err := transport.New(service, logger, maxBody)
	if err != nil {
		return nil, err
	}
	return &Server{Handler: api.Handler()}, nil
}
