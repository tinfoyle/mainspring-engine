package operationsapi

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/affiliateadmin"
	"github.com/tinfoyle/spyglass-engine/internal/application/analyticsreport"
	"github.com/tinfoyle/spyglass-engine/internal/application/billingadmin"
	"github.com/tinfoyle/spyglass-engine/internal/application/operationsconsole"
	"github.com/tinfoyle/spyglass-engine/internal/application/passkeys"
	"github.com/tinfoyle/spyglass-engine/internal/application/privacyrightsadmin"
	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
	"github.com/tinfoyle/spyglass-engine/internal/modules/operations"
	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/networkactor"
)

func (server *Server) beginPasskeyLogin(w http.ResponseWriter, request *http.Request) {
	if !server.validMutationOrigin(request) {
		writeProblem(w, http.StatusForbidden, "origin_denied", "request origin is not allowed")
		return
	}
	actor, _ := networkactor.FromContext(request.Context())
	result, err := server.passkeys.BeginLogin(request.Context(), actor)
	if err != nil {
		if errors.Is(err, passkeys.ErrInvalidCredential) {
			writeProblem(w, http.StatusTooManyRequests, "login_rate_limited", "try again later")
			return
		}
		server.logger.Error("operations passkey login begin failed", "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "passkey_login_unavailable", "passkey authentication could not be started")
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (server *Server) completePasskeyLogin(w http.ResponseWriter, request *http.Request) {
	if !server.validMutationOrigin(request) {
		writeProblem(w, http.StatusForbidden, "origin_denied", "request origin is not allowed")
		return
	}
	ceremonyID := request.PathValue("ceremonyID")
	if ids.Validate(ceremonyID) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_ceremony", "the passkey ceremony is invalid")
		return
	}
	var input struct {
		Credential  json.RawMessage `json:"credential"`
		ClientLabel string          `json:"client_label"`
	}
	if !server.decode(w, request, &input) {
		return
	}
	if len(input.Credential) == 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "a passkey credential is required")
		return
	}
	input.ClientLabel = strings.TrimSpace(input.ClientLabel)
	if input.ClientLabel == "" {
		input.ClientLabel = request.UserAgent()
	}
	issued, err := server.passkeys.CompleteLogin(request.Context(), passkeys.LoginCommand{CeremonyID: ceremonyID, Response: input.Credential, ClientLabel: input.ClientLabel})
	if err != nil {
		if errors.Is(err, passkeys.ErrInvalidCeremony) || errors.Is(err, passkeys.ErrInvalidCredential) || errors.Is(err, passkeys.ErrCredentialStateConflict) || errors.Is(err, sessions.ErrInvalidSession) {
			writeProblem(w, http.StatusUnauthorized, "passkey_login_failed", "the passkey could not establish an authorized staff session")
			return
		}
		server.logger.Error("operations passkey login completion failed", "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "passkey_login_unavailable", "passkey authentication could not be completed")
		return
	}
	staff, err := server.console.RecordAuthentication(request.Context(), issued.Session.UserID, issued.Session.ID)
	if err != nil {
		_, _ = server.sessions.RevokeOwned(request.Context(), issued.Session.UserID, issued.Session.ID)
		writeProblem(w, http.StatusForbidden, "staff_access_denied", "this passkey does not belong to an active staff identity")
		return
	}
	server.setCookie(w, issued.Token, issued.Session.ExpiresAt)
	writeJSON(w, http.StatusCreated, map[string]any{"staff": staff, "expires_at": issued.Session.ExpiresAt, "authentication_method": issued.Session.AuthenticationMethod})
}

func (server *Server) currentSession(w http.ResponseWriter, request *http.Request) {
	authenticated, staff, ok := server.authenticate(w, request)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"staff": staff, "expires_at": authenticated.Session.ExpiresAt, "authentication_method": authenticated.Session.AuthenticationMethod})
}

func (server *Server) logout(w http.ResponseWriter, request *http.Request) {
	if !server.validMutationOrigin(request) {
		writeProblem(w, http.StatusForbidden, "origin_denied", "request origin is not allowed")
		return
	}
	authenticated, _, ok := server.authenticate(w, request)
	if !ok {
		return
	}
	if err := server.console.RecordLogout(request.Context(), authenticated.Session.UserID, authenticated.Session.ID); err != nil {
		server.logger.Error("operations logout audit failed", "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "logout_unavailable", "logout could not be audited")
		return
	}
	if revoked, err := server.sessions.RevokeOwned(request.Context(), authenticated.Session.UserID, authenticated.Session.ID); err != nil || !revoked {
		server.logger.Error("operations session revocation failed", "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "logout_unavailable", "logout could not be completed")
		return
	}
	server.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (server *Server) lookup(w http.ResponseWriter, request *http.Request) {
	if !server.authorizeMutation(w, request) {
		return
	}
	_, staff, ok := server.authenticate(w, request)
	if !ok {
		return
	}
	var input struct {
		Kind   operations.LookupKind `json:"kind"`
		Value  string                `json:"value"`
		Ticket string                `json:"ticket"`
		Reason string                `json:"reason"`
	}
	if !server.decode(w, request, &input) {
		return
	}
	result, err := server.console.Lookup(request.Context(), staff.UserID, operations.LookupQuery{Kind: input.Kind, Value: input.Value, Audit: operations.AuditReason{Ticket: input.Ticket, Reason: input.Reason}})
	if err != nil {
		server.writeOperationsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": result})
}

func (server *Server) createGrant(w http.ResponseWriter, request *http.Request) {
	if !server.authorizeMutation(w, request) {
		return
	}
	_, staff, ok := server.authenticate(w, request)
	if !ok {
		return
	}
	var input struct {
		TargetUserID ids.UserID    `json:"target_user_id"`
		AccountID    ids.AccountID `json:"account_id"`
		Lifetime     uint64        `json:"lifetime_seconds"`
		Ticket       string        `json:"ticket"`
		Reason       string        `json:"reason"`
	}
	if !server.decode(w, request, &input) {
		return
	}
	grant, err := server.console.CreateGrant(request.Context(), operationsconsole.CreateGrantCommand{ActorUserID: staff.UserID,
		TargetUserID: input.TargetUserID, AccountID: input.AccountID, Lifetime: time.Duration(input.Lifetime) * time.Second,
		Audit: operations.AuditReason{Ticket: input.Ticket, Reason: input.Reason}})
	if err != nil {
		server.writeOperationsError(w, err)
		return
	}
	w.Header().Set("Location", "/api/operations/v1/support-grants/"+string(grant.ID))
	writeJSON(w, http.StatusCreated, map[string]any{"grant": grant})
}

func (server *Server) viewAccount(w http.ResponseWriter, request *http.Request) {
	if !server.authorizeMutation(w, request) {
		return
	}
	_, staff, ok := server.authenticate(w, request)
	if !ok {
		return
	}
	grantID := ids.OperationsSupportGrantID(request.PathValue("grantID"))
	var input operations.AuditReason
	if ids.Validate(string(grantID)) != nil || !server.decode(w, request, &input) {
		if ids.Validate(string(grantID)) != nil {
			writeProblem(w, http.StatusBadRequest, "invalid_grant", "the support grant is invalid")
		}
		return
	}
	view, err := server.console.ViewAccount(request.Context(), staff.UserID, grantID, input)
	if err != nil {
		server.writeOperationsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"mode": "read_only_support_view", "staff": staff, "view": view})
}

func (server *Server) revokeGrant(w http.ResponseWriter, request *http.Request) {
	if !server.authorizeMutation(w, request) {
		return
	}
	_, staff, ok := server.authenticate(w, request)
	if !ok {
		return
	}
	grantID := ids.OperationsSupportGrantID(request.PathValue("grantID"))
	var input struct {
		ExpectedVersion uint64 `json:"expected_version"`
		Ticket          string `json:"ticket"`
		Reason          string `json:"reason"`
	}
	if ids.Validate(string(grantID)) != nil || !server.decode(w, request, &input) {
		if ids.Validate(string(grantID)) != nil {
			writeProblem(w, http.StatusBadRequest, "invalid_grant", "the support grant is invalid")
		}
		return
	}
	grant, err := server.console.RevokeGrant(request.Context(), staff.UserID, grantID, input.ExpectedVersion, operations.AuditReason{Ticket: input.Ticket, Reason: input.Reason})
	if err != nil {
		server.writeOperationsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"grant": grant})
}

func (server *Server) analytics(w http.ResponseWriter, request *http.Request) {
	if !server.authorizeMutation(w, request) {
		return
	}
	_, staff, ok := server.authenticate(w, request)
	if !ok {
		return
	}
	var input struct {
		From          time.Time                 `json:"from"`
		To            time.Time                 `json:"to"`
		Bucket        analyticsreport.Bucket    `json:"bucket"`
		Dimension     analyticsreport.Dimension `json:"dimension"`
		MinimumCohort int                       `json:"minimum_cohort"`
		Ticket        string                    `json:"ticket"`
		Reason        string                    `json:"reason"`
	}
	if !server.decode(w, request, &input) {
		return
	}
	report, err := server.console.Analytics(request.Context(), staff.UserID, analyticsreport.Query{From: input.From, To: input.To, Bucket: input.Bucket, Dimension: input.Dimension, MinimumCohort: input.MinimumCohort}, operations.AuditReason{Ticket: input.Ticket, Reason: input.Reason})
	if err != nil {
		if analyticsreport.Validate(analyticsreport.Query{From: input.From, To: input.To, Bucket: input.Bucket, Dimension: input.Dimension, MinimumCohort: input.MinimumCohort}) != nil {
			writeProblem(w, http.StatusBadRequest, "invalid_analytics_query", "the analytics query is outside the allowed privacy bounds")
			return
		}
		server.writeOperationsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

type operatorAuditInput struct {
	Ticket string `json:"ticket"`
	Reason string `json:"reason"`
}

func (server *Server) billingFailures(w http.ResponseWriter, request *http.Request) {
	_, staff, ok := server.authorizedStaff(w, request, operations.RoleBilling)
	if !ok {
		return
	}
	var input struct {
		operatorAuditInput
		Limit int    `json:"limit"`
		Mode  string `json:"mode"`
	}
	if !server.decode(w, request, &input) {
		return
	}
	reason, ok := server.operatorReason(w, input.operatorAuditInput)
	if !ok {
		return
	}
	records, batchID, err := server.operators.Billing.Inspect(request.Context(), input.Limit, staffActor(staff), reason, server.environment, input.Mode)
	if err != nil {
		server.writeOperatorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"batch_id": batchID, "records": records})
}

func (server *Server) replayBillingEvent(w http.ResponseWriter, request *http.Request) {
	_, staff, ok := server.authorizedStaff(w, request, operations.RoleBilling)
	if !ok {
		return
	}
	var input struct {
		operatorAuditInput
		Mode string `json:"mode"`
	}
	if !server.decode(w, request, &input) {
		return
	}
	reason, ok := server.operatorReason(w, input.operatorAuditInput)
	if !ok {
		return
	}
	record, batchID, err := server.operators.Billing.ReplayEvent(request.Context(), request.PathValue("eventID"), staffActor(staff), reason, server.environment, input.Mode)
	if err != nil {
		server.writeOperatorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"batch_id": batchID, "record": record})
}

func (server *Server) refreshBillingSubscription(w http.ResponseWriter, request *http.Request) {
	_, staff, ok := server.authorizedStaff(w, request, operations.RoleBilling)
	if !ok {
		return
	}
	var input struct {
		operatorAuditInput
		Mode string `json:"mode"`
	}
	if !server.decode(w, request, &input) {
		return
	}
	reason, ok := server.operatorReason(w, input.operatorAuditInput)
	if !ok {
		return
	}
	record, batchID, err := server.operators.Billing.QueueRefresh(request.Context(), request.PathValue("subscriptionID"), staffActor(staff), reason, server.environment, input.Mode)
	if err != nil {
		server.writeOperatorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"batch_id": batchID, "record": record})
}

func (server *Server) openPrivacyRights(w http.ResponseWriter, request *http.Request) {
	_, staff, ok := server.authorizedStaff(w, request, operations.RolePrivacy)
	if !ok {
		return
	}
	var input struct {
		operatorAuditInput
		DueBefore time.Time `json:"due_before"`
		Limit     int       `json:"limit"`
	}
	if !server.decode(w, request, &input) {
		return
	}
	reason, ok := server.operatorReason(w, input.operatorAuditInput)
	if !ok {
		return
	}
	items, err := server.operators.Privacy.ListOpen(request.Context(), input.DueBefore, input.Limit, staffActor(staff), reason, server.environment)
	if err != nil {
		server.writeOperatorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (server *Server) inspectPrivacyRight(w http.ResponseWriter, request *http.Request) {
	_, staff, ok := server.authorizedStaff(w, request, operations.RolePrivacy)
	if !ok {
		return
	}
	var input operatorAuditInput
	if !server.decode(w, request, &input) {
		return
	}
	reason, ok := server.operatorReason(w, input)
	if !ok {
		return
	}
	value, err := server.operators.Privacy.Inspect(request.Context(), ids.PrivacyRightsRequestID(request.PathValue("requestID")), staffActor(staff), reason, server.environment)
	if err != nil {
		server.writeOperatorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"request": privacyOperatorView(value)})
}

func (server *Server) startPrivacyReview(w http.ResponseWriter, request *http.Request) {
	_, staff, ok := server.authorizedStaff(w, request, operations.RolePrivacy)
	if !ok {
		return
	}
	var input struct {
		operatorAuditInput
		ExpectedVersion uint64 `json:"expected_version"`
	}
	if !server.decode(w, request, &input) {
		return
	}
	reason, ok := server.operatorReason(w, input.operatorAuditInput)
	if !ok {
		return
	}
	value, err := server.operators.Privacy.StartReview(request.Context(), ids.PrivacyRightsRequestID(request.PathValue("requestID")), input.ExpectedVersion, staffActor(staff), reason, server.environment)
	if err != nil {
		server.writeOperatorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"request": privacyOperatorView(value)})
}

func (server *Server) resolvePrivacyRight(w http.ResponseWriter, request *http.Request) {
	_, staff, ok := server.authorizedStaff(w, request, operations.RolePrivacy)
	if !ok {
		return
	}
	var input struct {
		operatorAuditInput
		ExpectedVersion uint64              `json:"expected_version"`
		State           privacy.RightsState `json:"state"`
		EvidenceID      string              `json:"evidence_id"`
		EvidenceSHA256  string              `json:"evidence_sha256"`
	}
	if !server.decode(w, request, &input) {
		return
	}
	reason, ok := server.operatorReason(w, input.operatorAuditInput)
	if !ok {
		return
	}
	decoded, err := hex.DecodeString(input.EvidenceSHA256)
	if err != nil || len(decoded) != 32 || input.EvidenceSHA256 != strings.ToLower(input.EvidenceSHA256) {
		writeProblem(w, http.StatusBadRequest, "invalid_operations_request", "the evidence digest is invalid")
		return
	}
	var digest [32]byte
	copy(digest[:], decoded)
	value, err := server.operators.Privacy.Resolve(request.Context(), ids.PrivacyRightsRequestID(request.PathValue("requestID")), input.ExpectedVersion, input.State,
		privacyrightsadmin.ResolutionEvidence{ID: input.EvidenceID, SHA256: digest}, staffActor(staff), reason, server.environment)
	if err != nil {
		server.writeOperatorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"request": privacyOperatorView(value)})
}

func (server *Server) inspectAffiliate(w http.ResponseWriter, request *http.Request) {
	_, staff, ok := server.authorizedStaff(w, request, operations.RoleAffiliate)
	if !ok {
		return
	}
	var input operatorAuditInput
	if !server.decode(w, request, &input) {
		return
	}
	reason, ok := server.operatorReason(w, input)
	if !ok {
		return
	}
	value, err := server.operators.Affiliate.Inspect(request.Context(), ids.AffiliateID(request.PathValue("affiliateID")), staffActor(staff), reason, server.environment)
	if err != nil {
		server.writeOperatorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enrollment": value})
}

func (server *Server) inspectAffiliateRisk(w http.ResponseWriter, request *http.Request) {
	_, staff, ok := server.authorizedStaff(w, request, operations.RoleAffiliate)
	if !ok {
		return
	}
	var input operatorAuditInput
	if !server.decode(w, request, &input) {
		return
	}
	reason, ok := server.operatorReason(w, input)
	if !ok {
		return
	}
	value, err := server.operators.Affiliate.InspectRisk(request.Context(), ids.AffiliateID(request.PathValue("affiliateID")), staffActor(staff), reason, server.environment)
	if err != nil {
		server.writeOperatorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"risk": value, "flags": value.Flags()})
}

func (server *Server) transitionAffiliate(w http.ResponseWriter, request *http.Request) {
	_, staff, ok := server.authorizedStaff(w, request, operations.RoleAffiliate)
	if !ok {
		return
	}
	var input struct {
		operatorAuditInput
		ExpectedVersion uint64                     `json:"expected_version"`
		State           affiliates.EnrollmentState `json:"state"`
	}
	if !server.decode(w, request, &input) {
		return
	}
	reason, ok := server.operatorReason(w, input.operatorAuditInput)
	if !ok {
		return
	}
	value, err := server.operators.Affiliate.Transition(request.Context(), ids.AffiliateID(request.PathValue("affiliateID")), input.ExpectedVersion, input.State, staffActor(staff), reason, server.environment)
	if err != nil {
		server.writeOperatorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enrollment": value})
}

func (server *Server) authorizedStaff(w http.ResponseWriter, request *http.Request, role operations.StaffRole) (sessions.Authenticated, operations.Staff, bool) {
	if !server.authorizeMutation(w, request) {
		return sessions.Authenticated{}, operations.Staff{}, false
	}
	authenticated, staff, ok := server.authenticate(w, request)
	if !ok {
		return sessions.Authenticated{}, operations.Staff{}, false
	}
	if !staff.HasRole(role) {
		writeProblem(w, http.StatusForbidden, "staff_access_denied", "the assigned staff role does not permit this operation")
		return sessions.Authenticated{}, operations.Staff{}, false
	}
	return authenticated, staff, true
}

func (server *Server) operatorReason(w http.ResponseWriter, input operatorAuditInput) (string, bool) {
	audit := operations.AuditReason{Ticket: strings.TrimSpace(input.Ticket), Reason: strings.TrimSpace(input.Reason)}
	combined := audit.Ticket + ": " + audit.Reason
	if audit.Validate() != nil || len(combined) > 500 {
		writeProblem(w, http.StatusBadRequest, "invalid_operations_request", "a valid ticket and reason are required")
		return "", false
	}
	return combined, true
}

func staffActor(staff operations.Staff) string { return "operations/" + string(staff.UserID) }

func privacyOperatorView(value privacy.RightsRequest) map[string]any {
	return map[string]any{"request_id": value.ID, "version": value.Version, "kind": value.Kind, "scope": value.Scope,
		"state": value.State, "verified_at": value.VerifiedAt, "requested_at": value.RequestedAt,
		"response_due_at": value.ResponseDueAt, "updated_at": value.UpdatedAt}
}

func (server *Server) writeOperatorError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, billingadmin.ErrInvalidChange), errors.Is(err, privacyrightsadmin.ErrInvalidChange), errors.Is(err, affiliateadmin.ErrInvalidChange):
		writeProblem(w, http.StatusBadRequest, "invalid_operations_request", "the staff request is invalid")
	case errors.Is(err, billingadmin.ErrNotFound), errors.Is(err, privacyrightsadmin.ErrNotFound), errors.Is(err, affiliateadmin.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "operations_target_not_found", "the exact target was not found")
	case errors.Is(err, billingadmin.ErrStateConflict), errors.Is(err, privacyrightsadmin.ErrStateConflict), errors.Is(err, affiliateadmin.ErrStateConflict):
		writeProblem(w, http.StatusConflict, "operations_state_conflict", "the target changed; inspect it again before retrying")
	case errors.Is(err, affiliateadmin.ErrStrongAuth):
		writeProblem(w, http.StatusForbidden, "customer_strong_auth_required", "the customer must complete the required passkey confirmation")
	default:
		server.logger.Error("operations operator request failed", "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "operations_unavailable", "the operations request could not be completed")
	}
}

func (server *Server) authorizeMutation(w http.ResponseWriter, request *http.Request) bool {
	if !server.validMutationOrigin(request) {
		writeProblem(w, http.StatusForbidden, "origin_denied", "request origin is not allowed")
		return false
	}
	return true
}

func (server *Server) writeOperationsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, operations.ErrInvalidInput):
		writeProblem(w, http.StatusBadRequest, "invalid_operations_request", "the staff request is invalid")
	case errors.Is(err, operations.ErrStaffUnauthorized):
		writeProblem(w, http.StatusForbidden, "staff_access_denied", "the assigned staff role does not permit this operation")
	case errors.Is(err, operations.ErrGrantDenied):
		writeProblem(w, http.StatusForbidden, "support_grant_denied", "the exact support grant is unavailable, expired, or revoked")
	case errors.Is(err, operations.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "operations_target_not_found", "the exact target was not found")
	default:
		server.logger.Error("operations API request failed", "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "operations_unavailable", "the operations request could not be completed")
	}
}
