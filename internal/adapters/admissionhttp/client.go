// Package admissionhttp adapts the private global admission API to feature
// capacity ports without exposing a global database credential to a cell.
package admissionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	attentionapp "github.com/tinfoyle/spyglass-engine/internal/application/attention"
	"github.com/tinfoyle/spyglass-engine/internal/application/usageadmission"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

const maxResponseBody = int64(64 << 10)

type Client struct {
	origin *url.URL
	client *http.Client
	stats  transportCounters
}

type TransportStats struct {
	RetryAttempts  uint64 `json:"retry_attempts"`
	RetryRecovered uint64 `json:"retry_recovered"`
	RequestsFailed uint64 `json:"requests_failed"`
}

type transportCounters struct {
	retryAttempts  atomic.Uint64
	retryRecovered atomic.Uint64
	requestsFailed atomic.Uint64
}

func New(rawOrigin string, allowHTTP bool, transport http.RoundTripper) (*Client, error) {
	origin, err := url.Parse(strings.TrimSpace(rawOrigin))
	if err != nil || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") || (origin.Scheme != "https" && !(allowHTTP && origin.Scheme == "http")) {
		return nil, errors.New("admission origin must be an allowed absolute origin without a path")
	}
	origin.Path = ""
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &Client{origin: origin, client: &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("admission redirects are not allowed") }}}, nil
}

func (c *Client) TransportStats() TransportStats {
	return TransportStats{
		RetryAttempts:  c.stats.retryAttempts.Load(),
		RetryRecovered: c.stats.retryRecovered.Load(),
		RequestsFailed: c.stats.requestsFailed.Load(),
	}
}

func (c *Client) Reserve(ctx context.Context, command usageadmission.ReserveCommand) (usageadmission.Reservation, error) {
	if command.PackageCode != catalog.PackageWork || command.LimitCode != "active_items" || command.Amount != 1 {
		return usageadmission.Reservation{}, usageadmission.ErrInvalidRequest
	}
	return c.do(ctx, "reserve", command.AccountID, command.RequestID)
}

func (c *Client) Release(ctx context.Context, command usageadmission.ReleaseCommand) (usageadmission.Reservation, error) {
	return c.do(ctx, "release", command.AccountID, command.RequestID)
}

type capacityRequest struct {
	CellID       ids.CellID           `json:"cell_id"`
	RequestID    string               `json:"request_id"`
	RouteContext string               `json:"route_context"`
	Binding      routecontext.Binding `json:"binding"`
}

type capacityResponse struct {
	RequestID    string                          `json:"request_id"`
	State        usageadmission.ReservationState `json:"state"`
	Current      int64                           `json:"current"`
	Maximum      int64                           `json:"maximum"`
	ExpiresAt    *time.Time                      `json:"expires_at,omitempty"`
	NewlyCreated bool                            `json:"newly_created"`
}

type reviewerRequest struct {
	CellID       ids.CellID           `json:"cell_id"`
	UserID       ids.UserID           `json:"user_id"`
	RouteContext string               `json:"route_context"`
	Binding      routecontext.Binding `json:"binding"`
}

type reviewerResponse struct {
	UserID ids.UserID              `json:"user_id"`
	Active bool                    `json:"active"`
	Role   accounts.MembershipRole `json:"role,omitempty"`
}

func (c *Client) ActiveRole(ctx context.Context, accountID ids.AccountID, userID ids.UserID) (accounts.MembershipRole, bool, error) {
	claims, claimsOK := routecontext.FromContext(ctx)
	proof, proofOK := routecontext.ProofFromContext(ctx)
	if !claimsOK || !proofOK || claims.Authority.AccountID != accountID || ids.Validate(string(userID)) != nil {
		return "", false, attentionapp.ErrInvalidCommand
	}
	payload, err := json.Marshal(reviewerRequest{CellID: claims.Authority.CellID, UserID: userID, RouteContext: proof.Token, Binding: proof.Binding})
	if err != nil {
		return "", false, err
	}
	target := *c.origin
	target.Path = "/internal/v1/attention/reviewers:resolve"
	request, err := newRequest(ctx, target.String(), payload)
	if err != nil {
		return "", false, err
	}
	response, err := c.client.Do(request)
	if err != nil && response == nil && ctx.Err() == nil {
		c.stats.retryAttempts.Add(1)
		request, requestErr := newRequest(ctx, target.String(), payload)
		if requestErr != nil {
			return "", false, requestErr
		}
		response, err = c.client.Do(request)
		if err == nil {
			c.stats.retryRecovered.Add(1)
		}
	}
	if err != nil {
		closeResponse(response)
		c.stats.requestsFailed.Add(1)
		return "", false, errors.New("Attention reviewer directory is unavailable")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBody+1))
	if err != nil || int64(len(body)) > maxResponseBody || response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", false, errors.New("Attention reviewer directory returned an invalid response")
	}
	var value reviewerResponse
	if err := json.Unmarshal(body, &value); err != nil || value.UserID != userID || (value.Active && !validMembershipRole(value.Role)) || (!value.Active && value.Role != "") {
		return "", false, errors.New("Attention reviewer directory returned an invalid response")
	}
	return value.Role, value.Active, nil
}

func validMembershipRole(role accounts.MembershipRole) bool {
	switch role {
	case accounts.RoleOwner, accounts.RoleAdministrator, accounts.RoleBillingAdmin, accounts.RoleMember, accounts.RoleViewer:
		return true
	default:
		return false
	}
}

type problemResponse struct {
	Code    string `json:"code"`
	Current int64  `json:"current"`
	Maximum int64  `json:"maximum"`
}

func (c *Client) do(ctx context.Context, operation string, accountID ids.AccountID, requestID string) (usageadmission.Reservation, error) {
	claims, claimsOK := routecontext.FromContext(ctx)
	proof, proofOK := routecontext.ProofFromContext(ctx)
	if !claimsOK || !proofOK || claims.Authority.AccountID != accountID || ids.Validate(requestID) != nil || !capacityOperationAllowed(claims, requestID) {
		return usageadmission.Reservation{}, usageadmission.ErrInvalidRequest
	}
	payload, err := json.Marshal(capacityRequest{CellID: claims.Authority.CellID, RequestID: requestID, RouteContext: proof.Token, Binding: proof.Binding})
	if err != nil {
		return usageadmission.Reservation{}, err
	}
	target := *c.origin
	target.Path = "/internal/v1/work/capacity/" + operation
	request, err := newRequest(ctx, target.String(), payload)
	if err != nil {
		return usageadmission.Reservation{}, err
	}
	response, err := c.client.Do(request)
	if err != nil && response == nil && ctx.Err() == nil {
		c.stats.retryAttempts.Add(1)
		request, requestErr := newRequest(ctx, target.String(), payload)
		if requestErr != nil {
			return usageadmission.Reservation{}, requestErr
		}
		response, err = c.client.Do(request)
		if err == nil {
			c.stats.retryRecovered.Add(1)
		}
	}
	if err != nil {
		closeResponse(response)
		c.stats.requestsFailed.Add(1)
		return usageadmission.Reservation{}, errors.New("Work capacity admission is unavailable")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBody+1))
	if err != nil || int64(len(body)) > maxResponseBody {
		return usageadmission.Reservation{}, errors.New("Work capacity admission returned an invalid response")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return usageadmission.Reservation{}, admissionError(body)
	}
	var value capacityResponse
	// The broker may add receipt metadata during a rolling deployment. Keep the
	// reader additive while still rejecting malformed or trailing JSON.
	if err := json.Unmarshal(body, &value); err != nil || value.RequestID != requestID || (value.State != usageadmission.ReservationActive && value.State != usageadmission.ReservationReleased) {
		return usageadmission.Reservation{}, errors.New("Work capacity admission returned an invalid receipt")
	}
	return usageadmission.Reservation{AccountID: accountID, RequestID: value.RequestID, PackageCode: catalog.PackageWork, LimitCode: "active_items", Amount: 1, State: value.State, Current: value.Current, Maximum: value.Maximum, ExpiresAt: value.ExpiresAt, NewlyCreated: value.NewlyCreated}, nil
}

func capacityOperationAllowed(claims routecontext.Claims, requestID string) bool {
	if claims.Authority.OperationID == requestID {
		return true
	}
	if ids.Validate(claims.Authority.OperationID) != nil || claims.Binding.Method != http.MethodPost {
		return false
	}
	base := "/api/v1/accounts/" + string(claims.Authority.AccountID) + "/baseline-assessments/"
	remaining, found := strings.CutPrefix(claims.Binding.Target, base)
	if !found {
		return false
	}
	assessmentID, action, found := strings.Cut(remaining, "/")
	if !found || ids.Validate(assessmentID) != nil {
		return false
	}
	return action == "work-materializations" || action == "maintenance-work-materializations"
}

func newRequest(ctx context.Context, target string, payload []byte) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	return request, nil
}

func closeResponse(response *http.Response) {
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
}

func admissionError(body []byte) error {
	var problem problemResponse
	if json.Unmarshal(body, &problem) != nil || problem.Code == "" {
		return errors.New("Work capacity admission failed")
	}
	code := access.DenialCode(problem.Code)
	switch code {
	case access.DenialUnauthenticated, access.DenialMembership, access.DenialAccountUnavailable, access.DenialRole, access.DenialPackageNotEntitled, access.DenialPackageReadOnly, access.DenialLimitNotDefined, access.DenialLimitExceeded, access.DenialCorruptContext:
		return &access.DeniedError{Code: code, Package: catalog.PackageWork, Limit: "active_items", Current: problem.Current, Maximum: problem.Maximum}
	}
	switch problem.Code {
	case "invalid_usage_request", "invalid_admission_request", "invalid_route_proof", "admission_scope_denied":
		return usageadmission.ErrInvalidRequest
	case "reservation_conflict":
		return usageadmission.ErrReservationConflict
	case "reservation_closed":
		return usageadmission.ErrReservationClosed
	case "entitlement_changed":
		return usageadmission.ErrEntitlementChanged
	default:
		return errors.New("Work capacity admission is unavailable")
	}
}

var _ interface {
	Reserve(context.Context, usageadmission.ReserveCommand) (usageadmission.Reservation, error)
	Release(context.Context, usageadmission.ReleaseCommand) (usageadmission.Reservation, error)
} = (*Client)(nil)

var _ attentionapp.ReviewerDirectory = (*Client)(nil)
