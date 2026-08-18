// Package routecanary verifies candidate route-signing and workload TLS
// material against the real protected cell boundary before a rotation cutover.
package routecanary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

const (
	ActorID         = routecontext.RotationCanaryActorID
	DefaultTimeout  = 10 * time.Second
	MaximumResponse = int64(16 << 10)
)

var ErrRejected = errors.New("route rotation canary was rejected")

type Config struct {
	Origin              string
	CellID              ids.CellID
	AccountID           ids.AccountID
	PlacementGeneration uint64
	EntitlementVersion  uint64
	Issuer              string
	KeyID               string
	SigningKey          []byte
	Timeout             time.Duration
	Transport           http.RoundTripper
	Clock               routecontext.Clock
	IDs                 ids.Generator
}

type Result struct {
	CellID              ids.CellID
	KeyID               string
	PlacementGeneration uint64
}

func Probe(ctx context.Context, config Config) (Result, error) {
	origin, err := validate(config)
	if err != nil {
		return Result{}, err
	}
	if config.Timeout == 0 {
		config.Timeout = DefaultTimeout
	}
	target := "/api/v1/accounts/" + string(config.AccountID) + "/context"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin.String()+target, http.NoBody)
	if err != nil {
		return Result{}, errors.New("build route canary request failed")
	}
	binding, err := routecontext.BindRequest(request, nil)
	if err != nil {
		return Result{}, fmt.Errorf("bind route canary request: %w", err)
	}
	token, err := issue(config, binding)
	if err != nil {
		return Result{}, err
	}
	request.Header.Set(routecontext.HeaderName, token)
	request.Header.Set("Accept", "application/json")
	client := &http.Client{
		Transport: config.Transport,
		Timeout:   config.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("route canary redirects are not allowed")
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return Result{}, errors.New("route canary transport failed")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, MaximumResponse+1))
	if err != nil {
		return Result{}, errors.New("read route canary response failed")
	}
	if response.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("%w: status %d", ErrRejected, response.StatusCode)
	}
	if int64(len(body)) > MaximumResponse {
		return Result{}, errors.New("route canary response exceeds the safe bound")
	}
	var payload struct {
		AccountID           ids.AccountID `json:"account_id"`
		ActorKind           string        `json:"actor_kind"`
		CellID              ids.CellID    `json:"cell_id"`
		PlacementGeneration uint64        `json:"placement_generation"`
		EntitlementVersion  uint64        `json:"entitlement_version"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&payload); err != nil {
		return Result{}, errors.New("route canary response is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Result{}, errors.New("route canary response is invalid")
	}
	if payload.AccountID != config.AccountID || payload.ActorKind != "workload" || payload.CellID != config.CellID || payload.PlacementGeneration != config.PlacementGeneration || payload.EntitlementVersion != config.EntitlementVersion {
		return Result{}, errors.New("route canary response does not match the expected cell authority")
	}
	return Result{CellID: config.CellID, KeyID: strings.TrimSpace(config.KeyID), PlacementGeneration: config.PlacementGeneration}, nil
}

func ProbeAdmission(ctx context.Context, config Config) (Result, error) {
	origin, err := validate(config)
	if err != nil {
		return Result{}, err
	}
	if config.Timeout == 0 {
		config.Timeout = DefaultTimeout
	}
	target := "/api/v1/accounts/" + string(config.AccountID) + "/context"
	binding, err := routecontext.Bind(http.MethodGet, target, nil)
	if err != nil {
		return Result{}, fmt.Errorf("bind admission route canary: %w", err)
	}
	token, err := issue(config, binding)
	if err != nil {
		return Result{}, err
	}
	body, err := json.Marshal(map[string]any{"cell_id": config.CellID, "route_context": token, "binding": binding})
	if err != nil {
		return Result{}, errors.New("encode admission route canary")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, origin.String()+"/internal/v1/route-canary", bytes.NewReader(body))
	if err != nil {
		return Result{}, errors.New("build admission route canary request failed")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	client := &http.Client{Transport: config.Transport, Timeout: config.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("route canary redirects are not allowed")
	}}
	response, err := client.Do(request)
	if err != nil {
		return Result{}, errors.New("admission route canary transport failed")
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, MaximumResponse+1))
	if err != nil {
		return Result{}, errors.New("read admission route canary response failed")
	}
	if response.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("%w: status %d", ErrRejected, response.StatusCode)
	}
	if int64(len(responseBody)) > MaximumResponse {
		return Result{}, errors.New("admission route canary response exceeds the safe bound")
	}
	var payload struct {
		Status string     `json:"status"`
		CellID ids.CellID `json:"cell_id"`
		KeyID  string     `json:"key_id"`
	}
	if err := json.Unmarshal(responseBody, &payload); err != nil || payload.Status != "verified" || payload.CellID != config.CellID || payload.KeyID != strings.TrimSpace(config.KeyID) {
		return Result{}, errors.New("admission route canary response does not match the expected key authority")
	}
	return Result{CellID: config.CellID, KeyID: payload.KeyID, PlacementGeneration: config.PlacementGeneration}, nil
}

func issue(config Config, binding routecontext.Binding) (string, error) {
	signer, err := routecontext.NewSigner(config.Issuer, config.KeyID, config.SigningKey, routecontext.DefaultLifetime, config.Clock)
	if err != nil {
		return "", fmt.Errorf("configure route canary signer: %w", err)
	}
	authority := routecontext.Authority{
		RequestID: config.IDs.New(), AccountID: config.AccountID,
		ActorKind: "workload", ActorID: ActorID, CellID: config.CellID,
		PlacementGeneration: config.PlacementGeneration, EntitlementVersion: config.EntitlementVersion,
	}
	token, err := signer.Issue(routecontext.Audience(config.CellID), authority, binding)
	if err != nil {
		return "", fmt.Errorf("issue route canary proof: %w", err)
	}
	return token, nil
}

func validate(config Config) (url.URL, error) {
	if !routecontext.ValidCellID(config.CellID) || ids.Validate(string(config.AccountID)) != nil || config.PlacementGeneration == 0 || config.EntitlementVersion == 0 || strings.TrimSpace(config.Issuer) == "" || strings.TrimSpace(config.KeyID) == "" || len(config.SigningKey) != 32 || config.Transport == nil || config.Clock == nil || config.IDs == nil {
		return url.URL{}, errors.New("route canary configuration is invalid")
	}
	if config.Timeout < 0 || config.Timeout > 30*time.Second || (config.Timeout > 0 && config.Timeout < time.Second) {
		return url.URL{}, errors.New("route canary timeout must be between 1s and 30s")
	}
	origin, err := url.Parse(strings.TrimSpace(config.Origin))
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || (origin.Path != "" && origin.Path != "/") || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" {
		return url.URL{}, errors.New("route canary origin must be an exact HTTPS origin")
	}
	origin.Path = ""
	return *origin, nil
}
