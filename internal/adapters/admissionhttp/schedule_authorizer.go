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

	scheduleapp "github.com/tinfoyle/spyglass-engine/internal/application/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type ScheduleExecutionAuthorizer struct {
	origin *url.URL
	cellID ids.CellID
	client *http.Client
}

func NewScheduleExecutionAuthorizer(rawOrigin string, cellID ids.CellID, allowHTTP bool, transport http.RoundTripper) (*ScheduleExecutionAuthorizer, error) {
	origin, err := url.Parse(strings.TrimSpace(rawOrigin))
	if err != nil || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" ||
		(origin.Path != "" && origin.Path != "/") || (origin.Scheme != "https" && !(allowHTTP && origin.Scheme == "http")) || !routecontext.ValidCellID(cellID) {
		return nil, errors.New("Schedule admission origin and cell ID are invalid")
	}
	origin.Path = ""
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &ScheduleExecutionAuthorizer{origin: origin, cellID: cellID, client: &http.Client{Transport: transport, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("admission redirects are not allowed") }}}, nil
}

type scheduleExecutionAuthorizationRequest struct {
	CellID            ids.CellID     `json:"cell_id"`
	AccountID         ids.AccountID  `json:"account_id"`
	UserID            ids.UserID     `json:"user_id"`
	ScheduleID        ids.ScheduleID `json:"schedule_id"`
	RequiresWork      bool           `json:"requires_work"`
	RequiresKnowledge bool           `json:"requires_knowledge"`
}

type scheduleExecutionAuthorizationResponse struct {
	CellID                ids.CellID     `json:"cell_id"`
	AccountID             ids.AccountID  `json:"account_id"`
	UserID                ids.UserID     `json:"user_id"`
	ScheduleID            ids.ScheduleID `json:"schedule_id"`
	EntitlementVersion    uint64         `json:"entitlement_version"`
	MaximumConcurrentRuns int64          `json:"maximum_concurrent_runs"`
	CanReadRestricted     bool           `json:"can_read_restricted"`
}

func (authorizer *ScheduleExecutionAuthorizer) Authorize(ctx context.Context, snapshot scheduleapp.ExecutionSnapshot) (scheduleapp.ExecutionAuthorization, error) {
	if authorizer == nil || authorizer.origin == nil || !snapshot.ValidForAuthorization() {
		return scheduleapp.ExecutionAuthorization{}, scheduleapp.ErrExecutionAuthorization
	}
	schedule := snapshot.Schedule
	payload, err := json.Marshal(scheduleExecutionAuthorizationRequest{CellID: authorizer.cellID, AccountID: schedule.AccountID,
		UserID: schedule.CreatedBy, ScheduleID: schedule.ID, RequiresWork: len(schedule.Template.WorkItemIDs) != 0,
		RequiresKnowledge: len(schedule.Template.KnowledgeFactIDs)+len(schedule.Template.KnowledgeDocumentIDs)+len(schedule.Template.BaselineAssessmentIDs) != 0})
	if err != nil {
		return scheduleapp.ExecutionAuthorization{}, err
	}
	target := *authorizer.origin
	target.Path = "/internal/v1/agents/schedule-executions:authorize"
	request, err := newRequest(ctx, target.String(), payload)
	if err != nil {
		return scheduleapp.ExecutionAuthorization{}, err
	}
	response, err := authorizer.client.Do(request)
	if err != nil && response == nil && ctx.Err() == nil {
		request, requestErr := newRequest(ctx, target.String(), payload)
		if requestErr != nil {
			return scheduleapp.ExecutionAuthorization{}, requestErr
		}
		response, err = authorizer.client.Do(request)
	}
	if err != nil {
		closeResponse(response)
		return scheduleapp.ExecutionAuthorization{}, scheduleapp.ErrExecutionServiceUnavailable
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBody+1))
	if err != nil || int64(len(body)) > maxResponseBody {
		return scheduleapp.ExecutionAuthorization{}, scheduleapp.ErrExecutionServiceUnavailable
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var problem problemResponse
		if json.Unmarshal(body, &problem) == nil && problem.Code == "agent_authorization_stale" {
			return scheduleapp.ExecutionAuthorization{}, scheduleapp.ErrExecutionAuthorizationStale
		}
		if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusUnauthorized {
			return scheduleapp.ExecutionAuthorization{}, scheduleapp.ErrExecutionAuthorization
		}
		return scheduleapp.ExecutionAuthorization{}, scheduleapp.ErrExecutionServiceUnavailable
	}
	var value scheduleExecutionAuthorizationResponse
	if json.Unmarshal(body, &value) != nil || value.CellID != authorizer.cellID || value.AccountID != schedule.AccountID || value.UserID != schedule.CreatedBy ||
		value.ScheduleID != schedule.ID || value.EntitlementVersion == 0 || value.MaximumConcurrentRuns < 1 {
		return scheduleapp.ExecutionAuthorization{}, scheduleapp.ErrExecutionServiceUnavailable
	}
	return scheduleapp.ExecutionAuthorization{EntitlementVersion: value.EntitlementVersion, MaximumConcurrentRun: value.MaximumConcurrentRuns,
		CanReadRestricted: value.CanReadRestricted}, nil
}

var _ scheduleapp.ExecutionAuthorizer = (*ScheduleExecutionAuthorizer)(nil)
