package admissionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	workagent "github.com/tinfoyle/spyglass-engine/internal/application/workagentexecution"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

// WorkAgentAuthorizer is a workload client. It carries no browser token and
// relies on the admission transport's mTLS identity to request current global
// authorization for one frozen cell execution intent.
type WorkAgentAuthorizer struct {
	origin *url.URL
	cellID ids.CellID
	client *http.Client
}

func NewWorkAgentAuthorizer(rawOrigin string, cellID ids.CellID, allowHTTP bool, transport http.RoundTripper) (*WorkAgentAuthorizer, error) {
	origin, err := url.Parse(strings.TrimSpace(rawOrigin))
	if err != nil || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" ||
		(origin.Path != "" && origin.Path != "/") || (origin.Scheme != "https" && !(allowHTTP && origin.Scheme == "http")) || !routecontext.ValidCellID(cellID) {
		return nil, errors.New("Work Agent admission origin and cell ID are invalid")
	}
	origin.Path = ""
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &WorkAgentAuthorizer{origin: origin, cellID: cellID, client: &http.Client{Transport: transport, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("admission redirects are not allowed") }}}, nil
}

type workAgentAuthorizationRequest struct {
	CellID      ids.CellID    `json:"cell_id"`
	AccountID   ids.AccountID `json:"account_id"`
	UserID      ids.UserID    `json:"user_id"`
	ExecutionID string        `json:"execution_id"`
}

type workAgentAuthorizationResponse struct {
	CellID                ids.CellID    `json:"cell_id"`
	AccountID             ids.AccountID `json:"account_id"`
	UserID                ids.UserID    `json:"user_id"`
	ExecutionID           string        `json:"execution_id"`
	EntitlementVersion    uint64        `json:"entitlement_version"`
	MaximumConcurrentRuns int64         `json:"maximum_concurrent_runs"`
}

func (authorizer *WorkAgentAuthorizer) Authorize(ctx context.Context, snapshot workagent.Snapshot) (workagent.Authorization, error) {
	if authorizer == nil || authorizer.origin == nil || !snapshot.ValidForAuthorization() {
		return workagent.Authorization{}, workagent.ErrAuthorizationDenied
	}
	payload, err := json.Marshal(workAgentAuthorizationRequest{CellID: authorizer.cellID, AccountID: snapshot.AccountID,
		UserID: snapshot.UserID, ExecutionID: snapshot.ExecutionID})
	if err != nil {
		return workagent.Authorization{}, err
	}
	target := *authorizer.origin
	target.Path = "/internal/v1/agents/work-executions:authorize"
	request, err := newRequest(ctx, target.String(), payload)
	if err != nil {
		return workagent.Authorization{}, err
	}
	response, err := authorizer.client.Do(request)
	if err != nil && response == nil && ctx.Err() == nil {
		request, requestErr := newRequest(ctx, target.String(), payload)
		if requestErr != nil {
			return workagent.Authorization{}, requestErr
		}
		response, err = authorizer.client.Do(request)
	}
	if err != nil {
		closeResponse(response)
		return workagent.Authorization{}, workagent.ErrAuthorizationService
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBody+1))
	if err != nil || int64(len(body)) > maxResponseBody {
		return workagent.Authorization{}, workagent.ErrAuthorizationService
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var problem problemResponse
		if json.Unmarshal(body, &problem) == nil && problem.Code == "agent_authorization_stale" {
			return workagent.Authorization{}, workagent.ErrAuthorizationStale
		}
		if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusUnauthorized {
			return workagent.Authorization{}, workagent.ErrAuthorizationDenied
		}
		return workagent.Authorization{}, workagent.ErrAuthorizationService
	}
	var value workAgentAuthorizationResponse
	if json.Unmarshal(body, &value) != nil || value.CellID != authorizer.cellID || value.AccountID != snapshot.AccountID ||
		value.UserID != snapshot.UserID || value.ExecutionID != snapshot.ExecutionID || value.EntitlementVersion == 0 || value.MaximumConcurrentRuns < 1 {
		return workagent.Authorization{}, workagent.ErrAuthorizationService
	}
	return workagent.Authorization{EntitlementVersion: value.EntitlementVersion, MaximumConcurrentRun: value.MaximumConcurrentRuns}, nil
}

var _ workagent.Authorizer = (*WorkAgentAuthorizer)(nil)
