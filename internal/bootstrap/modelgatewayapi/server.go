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
	MaxRequestBody int64
}

type Server struct{ Handler http.Handler }

func New(config Config, logger *slog.Logger) (*Server, error) {
	if logger == nil || config.OpenAIAPIKey == "" {
		return nil, errors.New("model gateway configuration is required")
	}
	provider, err := openairesponses.New(openairesponses.Config{APIKey: config.OpenAIAPIKey, Origin: config.OpenAIOrigin, HTTPClient: config.OpenAIClient})
	if err != nil {
		return nil, err
	}
	pricing, err := modelgateway.ParsePricingJSON(config.OpenAIPricing)
	if err != nil {
		return nil, errors.New("model gateway pricing configuration is invalid")
	}
	service, err := modelgateway.New([]modelgateway.Definition{{Name: "openai", Timeout: modelgateway.MaximumProviderTimeout, Provider: provider, Pricing: pricing}})
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
