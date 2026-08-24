package integrationsync

import (
	"context"
	"errors"

	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
)

type ProviderRoute struct {
	Kind               domain.ConnectorKind
	CredentialProvider string
	Provider           Provider
}

type providerRouteKey struct {
	kind     domain.ConnectorKind
	provider string
}

type ProviderRouter struct {
	routes map[providerRouteKey]Provider
}

func NewProviderRouter(routes []ProviderRoute) (*ProviderRouter, error) {
	if len(routes) == 0 {
		return nil, ErrInvalid
	}
	registered := make(map[providerRouteKey]Provider, len(routes))
	for _, route := range routes {
		if (route.Kind != domain.ConnectorEmail && route.Kind != domain.ConnectorGoogleDrive) ||
			!validProvider.MatchString(route.CredentialProvider) || route.Provider == nil {
			return nil, ErrInvalid
		}
		key := providerRouteKey{kind: route.Kind, provider: route.CredentialProvider}
		if _, exists := registered[key]; exists {
			return nil, ErrInvalid
		}
		registered[key] = route.Provider
	}
	return &ProviderRouter{routes: registered}, nil
}

func (router *ProviderRouter) Sync(ctx context.Context, request ProviderRequest) (ProviderPage, error) {
	if router == nil || ctx == nil || ctx.Err() != nil {
		return ProviderPage{}, ErrUnavailable
	}
	provider := router.routes[providerRouteKey{kind: request.SourceKind, provider: request.CredentialProvider}]
	if provider == nil {
		return ProviderPage{}, errors.Join(ErrUnavailable, errors.New("integration source provider route is unavailable"))
	}
	return provider.Sync(ctx, request)
}

var _ Provider = (*ProviderRouter)(nil)
