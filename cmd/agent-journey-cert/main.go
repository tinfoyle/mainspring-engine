package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	protocolVersion = "2026-07-28"
	localClientID   = "https://agent-client.infiniteocean.localhost:8444/oauth/client-metadata.json"
	localRedirect   = "https://agent-client.infiniteocean.localhost:8444/callback"
)

type config struct {
	appOrigin, mcpOrigin, edgeAddress, mailpitURL, databaseURL, providerFixture, output string
	stripeFixtureURL, stripeFixtureToken                                                string
	commercialOnly                                                                      bool
}

type check struct {
	Name    string `json:"name"`
	Outcome string `json:"outcome"`
	Detail  string `json:"detail,omitempty"`
}

type toolCoverage struct {
	Name    string `json:"name"`
	Outcome string `json:"outcome"`
	Detail  string `json:"detail"`
}

type report struct {
	SchemaVersion string         `json:"schema_version"`
	ExecutedAt    time.Time      `json:"executed_at"`
	AccountID     string         `json:"account_id"`
	UserID        string         `json:"user_id"`
	ToolCount     int            `json:"tool_count"`
	Tools         []string       `json:"tools"`
	ToolCoverage  []toolCoverage `json:"tool_coverage"`
	Checks        []check        `json:"checks"`
}

type journey struct {
	config                      config
	client                      *http.Client
	report                      report
	covered                     map[string]toolCoverage
	workID, runID, invocationID string
	workVersion                 uint64
	policyVersion               uint64
}

func main() {
	var c config
	flag.StringVar(&c.appOrigin, "app-origin", envOr("SPYGLASS_AGENT_JOURNEY_APP_ORIGIN", "https://app.infiniteocean.localhost:8444"), "canonical local app origin")
	flag.StringVar(&c.mcpOrigin, "mcp-origin", envOr("SPYGLASS_AGENT_JOURNEY_MCP_ORIGIN", "https://mcp.infiniteocean.localhost:8444"), "canonical local MCP origin")
	flag.StringVar(&c.edgeAddress, "edge-address", envOr("SPYGLASS_AGENT_JOURNEY_EDGE_ADDRESS", "127.0.0.1:8444"), "local edge dial address")
	flag.StringVar(&c.mailpitURL, "mailpit-url", envOr("SPYGLASS_AGENT_JOURNEY_MAILPIT_URL", "http://127.0.0.1:8025"), "local Mailpit API URL")
	flag.StringVar(&c.databaseURL, "database-url", os.Getenv("SPYGLASS_AGENT_JOURNEY_DATABASE_URL"), "local fixture database URL")
	flag.StringVar(&c.providerFixture, "provider-fixture", envOr("SPYGLASS_AGENT_JOURNEY_PROVIDER_FIXTURE", "deterministic-fail-once"), "required deterministic local provider behavior")
	flag.StringVar(&c.stripeFixtureURL, "stripe-fixture-url", os.Getenv("SPYGLASS_AGENT_JOURNEY_STRIPE_FIXTURE_URL"), "local Stripe fixture origin")
	flag.StringVar(&c.stripeFixtureToken, "stripe-fixture-token", os.Getenv("SPYGLASS_AGENT_JOURNEY_STRIPE_FIXTURE_TOKEN"), "local Stripe fixture completion token")
	flag.BoolVar(&c.commercialOnly, "commercial-only", strings.EqualFold(os.Getenv("SPYGLASS_AGENT_JOURNEY_COMMERCIAL_ONLY"), "true"), "stop after the real local commercial journey")
	flag.StringVar(&c.output, "out", "", "optional content-free JSON report path")
	flag.Parse()
	if err := run(context.Background(), c); err != nil {
		fmt.Fprintln(os.Stderr, "agent journey certification failed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, c config) error {
	if c.appOrigin != "https://app.infiniteocean.localhost:8444" || c.mcpOrigin != "https://mcp.infiniteocean.localhost:8444" {
		return errors.New("agent journey certificate only accepts the canonical local origins")
	}
	if !c.commercialOnly && c.providerFixture != "deterministic-fail-once" {
		return errors.New("agent journey certificate requires the deterministic fail-once provider fixture")
	}
	if err := validateLocalAgentJourneyTargets(c); err != nil {
		return err
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		Proxy:           nil,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}, // #nosec G402 -- exact local origins are enforced above and routed to an explicit local edge address.
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, c.edgeAddress)
		},
	}
	schemaVersion := "spyglass-agent-journey/v1"
	if c.commercialOnly {
		schemaVersion = "spyglass-commercial-journey/v1"
	}
	j := &journey{config: c, client: &http.Client{Jar: jar, Transport: transport, Timeout: 15 * time.Second}, report: report{SchemaVersion: schemaVersion, ExecutedAt: time.Now().UTC(), Tools: []string{}, ToolCoverage: []toolCoverage{}}, covered: map[string]toolCoverage{}}
	defer transport.CloseIdleConnections()

	stamp := time.Now().UTC().Format("20060102t150405.000000000")
	email := "agent-cert-" + stamp + "@example.test"
	password := "Local-agent-certification-" + stamp + "!"
	if err := j.register(ctx, email, password); err != nil {
		return err
	}
	if c.stripeFixtureURL != "" {
		if err := j.selectAccount(ctx); err != nil {
			return err
		}
		if err := j.purchaseLocalSubscription(ctx); err != nil {
			return err
		}
		if c.commercialOnly {
			if err := j.exerciseBaselineJourney(ctx); err != nil {
				return err
			}
			return j.writeReport()
		}
	} else {
		if err := provisionLocalCommercialFixture(ctx, c.databaseURL, j.report.AccountID); err != nil {
			return fmt.Errorf("provision local commercial fixture: %w", err)
		}
		j.pass("local commercial fixture", "all packages enabled and deterministic AI Tokens granted")
		if err := j.selectAccount(ctx); err != nil {
			return err
		}
	}
	if err := j.exerciseBaselineJourney(ctx); err != nil {
		return err
	}
	if err := j.exerciseHTTPPlatform(ctx); err != nil {
		return err
	}
	accessToken, err := j.authorizeMCP(ctx)
	if err != nil {
		return err
	}
	if err := j.listTools(ctx, accessToken); err != nil {
		return err
	}
	if err := j.exerciseMCPPlatform(ctx, accessToken); err != nil {
		return err
	}
	if err := j.exerciseMCPBoundaries(ctx, accessToken); err != nil {
		return err
	}
	if err := j.exerciseEveryMCPTool(ctx, accessToken); err != nil {
		return err
	}
	if len(j.report.Tools) < 90 {
		return fmt.Errorf("MCP surface unexpectedly contains only %d tools", len(j.report.Tools))
	}
	return j.writeReport()
}

type baselineJourneyAssessment struct {
	ID           string `json:"id"`
	State        string `json:"state"`
	Version      uint64 `json:"version"`
	Requirements []struct {
		ID          string `json:"id"`
		Disposition string `json:"disposition"`
	} `json:"requirements"`
	Plan *struct {
		ID                string `json:"id"`
		ContentSHA256     string `json:"content_sha256"`
		AssessmentVersion uint64 `json:"assessment_version"`
		ProposedWorkCount uint64 `json:"proposed_work_count"`
	} `json:"plan"`
	ReassessAt *time.Time `json:"reassess_at"`
}

func (j *journey) exerciseBaselineJourney(ctx context.Context) error {
	base := j.config.appOrigin + "/api/v1/accounts/" + j.report.AccountID
	retries, err := j.awaitAccountCell(ctx, base)
	if err != nil {
		return err
	}
	if retries > 0 {
		j.pass("fresh Account HTTP retry", fmt.Sprintf("recovered after %d fail-closed route probes while cell provisioning completed", retries))
	}
	baselineBase := base + "/baseline-assessments"
	status, body, err := j.jsonRequest(ctx, http.MethodPost, baselineBase, map[string]any{}, true)
	if err != nil || status != http.StatusCreated {
		return fmt.Errorf("start Baseline: status=%d body=%s err=%w", status, body, err)
	}
	assessment, err := decodeBaselineJourneyAssessment(body)
	if err != nil || assessment.State != "interview" || assessment.Version == 0 {
		return fmt.Errorf("decode started Baseline: assessment=%+v err=%w", assessment, err)
	}
	assessmentPath := baselineBase + "/" + assessment.ID
	legalNameFactID, legalNameFactRevision, err := j.captureBaselineOwnerFact(ctx, base)
	if err != nil {
		return err
	}
	assessment, err = j.baselineCommand(ctx, assessmentPath+"/answers", assessment.Version, map[string]any{
		"question_key": "organization.legal_name",
		"kind":         "fact",
		"fact": map[string]any{
			"fact_id":  legalNameFactID,
			"revision": legalNameFactRevision,
		},
	})
	if err != nil {
		return fmt.Errorf("bind owner-confirmed legal name to Baseline: %w", err)
	}
	requiredQuestions := []string{
		"organization.industry",
		"organization.primary_location",
		"organization.services",
		"organization.team_size",
		"baseline.immediate_concern",
	}
	for _, question := range requiredQuestions {
		assessment, err = j.baselineCommand(ctx, assessmentPath+"/answers", assessment.Version, map[string]any{
			"question_key": question,
			"kind":         "unknown",
			"reason":       "The owner will confirm this during the certified Baseline review.",
		})
		if err != nil {
			return fmt.Errorf("answer Baseline question %s: %w", question, err)
		}
	}
	assessment, err = j.baselineCommand(ctx, assessmentPath+"/inventory-starts", assessment.Version, map[string]any{})
	if err != nil || assessment.State != "inventory" {
		return fmt.Errorf("begin Baseline inventory: state=%q err=%w", assessment.State, err)
	}
	assessment, err = j.baselineCommand(ctx, assessmentPath+"/inventories", assessment.Version, map[string]any{})
	if err != nil || assessment.State != "gap_review" || len(assessment.Requirements) < 2 {
		return fmt.Errorf("build Baseline inventory: state=%q requirements=%d err=%w", assessment.State, len(assessment.Requirements), err)
	}
	gapID := assessment.Requirements[0].ID
	for index, requirement := range assessment.Requirements {
		disposition, reason := "not_applicable", "The owner reviewed this requirement and confirmed it does not apply to the certification fixture."
		if index == 0 {
			disposition, reason = "gap", "The certification fixture needs reviewed formation evidence."
		}
		assessment, err = j.baselineCommand(ctx, assessmentPath+"/dispositions", assessment.Version, map[string]any{
			"requirement_id": requirement.ID,
			"disposition":    disposition,
			"reason":         reason,
		})
		if err != nil {
			return fmt.Errorf("review Baseline requirement %s: %w", requirement.ID, err)
		}
	}
	assessment, err = j.baselineCommand(ctx, assessmentPath+"/plans", assessment.Version, map[string]any{})
	if err != nil || assessment.State != "plan_approval" || assessment.Plan == nil || assessment.Plan.ProposedWorkCount != 1 {
		return fmt.Errorf("freeze Baseline plan: state=%q plan=%+v err=%w", assessment.State, assessment.Plan, err)
	}
	plan := *assessment.Plan
	changedDigest := strings.Repeat("0", sha256.Size*2)
	status, rejected, requestErr := j.jsonRequestWithHeaders(ctx, http.MethodPost, assessmentPath+"/plan-approvals", map[string]any{
		"plan_id": plan.ID, "content_sha256": changedDigest, "assessment_version": plan.AssessmentVersion,
	}, true, map[string]string{"If-Match": fmt.Sprintf(`W/"%d"`, assessment.Version)})
	if requestErr != nil || status != http.StatusUnprocessableEntity {
		return fmt.Errorf("changed Baseline plan did not fail closed: status=%d body=%s err=%w", status, rejected, requestErr)
	}
	assessment, err = j.baselineCommand(ctx, assessmentPath+"/plan-approvals", assessment.Version, map[string]any{
		"plan_id": plan.ID, "content_sha256": plan.ContentSHA256, "assessment_version": plan.AssessmentVersion,
	})
	if err != nil || assessment.State != "active" {
		return fmt.Errorf("approve exact Baseline plan: state=%q err=%w", assessment.State, err)
	}
	status, body, err = j.jsonRequestWithHeaders(ctx, http.MethodPost, assessmentPath+"/work-materializations", map[string]any{
		"plan_id": plan.ID, "content_sha256": plan.ContentSHA256, "assessment_version": plan.AssessmentVersion,
	}, true, map[string]string{"If-Match": fmt.Sprintf(`W/"%d"`, assessment.Version)})
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("materialize Baseline Work: status=%d body=%s err=%w", status, body, err)
	}
	var materialized struct {
		Items []struct {
			ID         string `json:"id"`
			State      string `json:"state"`
			Version    uint64 `json:"version"`
			Provenance struct {
				Source                string `json:"source"`
				BaselineRequirementID string `json:"baseline_requirement_id"`
			} `json:"provenance"`
		} `json:"items"`
	}
	if json.Unmarshal(body, &materialized) != nil || len(materialized.Items) != 1 {
		return fmt.Errorf("materialized Baseline plan did not return exactly one Work item: %s", body)
	}
	work := materialized.Items[0]
	if work.State != "open" || work.Provenance.Source != "baseline" || work.Provenance.BaselineRequirementID != gapID {
		return fmt.Errorf("materialized Work lost Baseline provenance: %+v", work)
	}
	status, body, err = j.jsonRequestWithHeaders(ctx, http.MethodPost, base+"/work-items/"+work.ID+"/transitions", map[string]any{
		"to": "in_progress", "reason": "Begin the certified Baseline evidence task.",
	}, true, map[string]string{"If-Match": fmt.Sprintf(`W/"%d"`, work.Version)})
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("start Baseline Work: status=%d body=%s err=%w", status, body, err)
	}
	_, work.Version, err = objectIdentity(body)
	if err != nil {
		return fmt.Errorf("decode started Baseline Work: %w", err)
	}
	status, body, err = j.jsonRequestWithHeaders(ctx, http.MethodPost, base+"/work-items/"+work.ID+"/transitions", map[string]any{
		"to": "done", "reason": "Reviewed owner evidence is ready to bind to the Baseline.",
	}, true, map[string]string{"If-Match": fmt.Sprintf(`W/"%d"`, work.Version)})
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("complete Baseline Work: status=%d body=%s err=%w", status, body, err)
	}
	statement := []byte("Synthetic owner-reviewed formation evidence for the Baseline journey.")
	digest := sha256.Sum256(statement)
	status, body, err = j.jsonRequest(ctx, http.MethodPost, base+"/knowledge/evidence", map[string]any{
		"source_kind": "owner_statement", "source_reference": "baseline-journey/formation-evidence",
		"source_revision": "certified-v1", "content_sha256": hex.EncodeToString(digest[:]), "captured_at": time.Now().UTC(),
	}, true)
	if err != nil || status != http.StatusCreated {
		return fmt.Errorf("register Baseline evidence: status=%d body=%s err=%w", status, body, err)
	}
	evidenceID, _, err := objectIdentity(body)
	if err != nil {
		return fmt.Errorf("decode Baseline evidence: %w", err)
	}
	assessment, err = j.baselineCommand(ctx, assessmentPath+"/work-evidence-confirmations", assessment.Version, map[string]any{
		"requirement_id": gapID, "work_item_id": work.ID, "evidence_id": evidenceID,
		"reason": "The owner reviewed the completed Work and exact formation evidence.",
	})
	disposition, found := baselineRequirementDisposition(assessment, gapID)
	if err != nil || !found || disposition != "satisfied" {
		return fmt.Errorf("confirm Baseline Work evidence: requirement=%s disposition=%q found=%t err=%w", gapID, disposition, found, err)
	}
	assessment, err = j.baselineCommand(ctx, assessmentPath+"/readiness", assessment.Version, map[string]any{})
	if err != nil || assessment.State != "ready" || assessment.ReassessAt == nil {
		return fmt.Errorf("mark Baseline ready: state=%q reassess_at=%v err=%w", assessment.State, assessment.ReassessAt, err)
	}
	status, body, err = j.jsonRequest(ctx, http.MethodGet, baselineBase+"/current", nil, false)
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("reload current Baseline: status=%d body=%s err=%w", status, body, err)
	}
	reloaded, decodeErr := decodeBaselineJourneyAssessment(body)
	if decodeErr != nil || reloaded.ID != assessment.ID || reloaded.State != "ready" || reloaded.Version != assessment.Version {
		return fmt.Errorf("current Baseline lost durable ready state: assessment=%+v err=%w", reloaded, decodeErr)
	}
	j.pass("Business Baseline end-to-end", "started a real assessment, reviewed a generated inventory, rejected a changed frozen plan, approved the exact plan, generated and completed accountable Work, bound owner-reviewed evidence, and reloaded the durable ready Baseline")
	return nil
}

func (j *journey) captureBaselineOwnerFact(ctx context.Context, base string) (string, uint64, error) {
	statement := "Synthetic Wrench Works"
	canonical, err := json.Marshal(statement)
	if err != nil {
		return "", 0, err
	}
	digest := sha256.Sum256(canonical)
	status, body, err := j.jsonRequest(ctx, http.MethodPost, base+"/knowledge/evidence", map[string]any{
		"source_kind":      "owner_statement",
		"source_reference": "baseline-journey/organization.legal_name",
		"source_revision":  "owner-confirmed-v1",
		"content_sha256":   hex.EncodeToString(digest[:]),
		// Deliberately simulate a browser clock ahead of the Cell. This keeps the
		// real failure that motivated the certificate in permanent coverage.
		"captured_at": time.Now().UTC().Add(30 * time.Second),
	}, true)
	if err != nil || status != http.StatusCreated {
		return "", 0, fmt.Errorf("register owner-confirmed Baseline evidence: status=%d body=%s err=%w", status, body, err)
	}
	evidenceID, _, err := objectIdentity(body)
	if err != nil {
		return "", 0, fmt.Errorf("decode owner-confirmed Baseline evidence: %w", err)
	}

	status, body, err = j.jsonRequest(ctx, http.MethodPost, base+"/knowledge/claims", map[string]any{
		"scope":       map[string]any{"kind": "account"},
		"key":         "organization.legal_name",
		"value":       statement,
		"confidence":  1000,
		"sensitivity": "internal",
		"citations": []map[string]any{{
			"evidence_id": evidenceID, "evidence_kind": "owner_statement", "relation": "supports",
			"locator": "Owner-confirmed Business Baseline answer",
		}},
	}, true)
	if err != nil || status != http.StatusCreated {
		return "", 0, fmt.Errorf("propose owner-confirmed Baseline claim: status=%d body=%s err=%w", status, body, err)
	}
	claimID, claimVersion, err := objectIdentity(body)
	if err != nil {
		return "", 0, fmt.Errorf("decode owner-confirmed Baseline claim: %w", err)
	}

	status, body, err = j.jsonRequestWithHeaders(ctx, http.MethodPost, base+"/knowledge/claims/"+claimID+"/decisions", map[string]any{
		"accept": true,
		"reason": "Owner confirmed during Business Baseline onboarding",
	}, true, map[string]string{"If-Match": fmt.Sprintf(`W/"%d"`, claimVersion)})
	if err != nil || status != http.StatusOK {
		return "", 0, fmt.Errorf("accept owner-confirmed Baseline claim: status=%d body=%s err=%w", status, body, err)
	}
	var decision struct {
		Fact *struct {
			ID       string `json:"id"`
			Revision uint64 `json:"revision"`
		} `json:"fact"`
	}
	if err := json.Unmarshal(body, &decision); err != nil {
		return "", 0, fmt.Errorf("decode accepted owner-confirmed Baseline fact: %w", err)
	}
	if decision.Fact == nil || ids.Validate(decision.Fact.ID) != nil || decision.Fact.Revision == 0 {
		return "", 0, errors.New("accepted owner-confirmed Baseline claim omitted a valid Fact identity or revision")
	}
	j.pass("Baseline owner Knowledge capture", "a browser-skewed owner statement became attributable evidence, an accepted claim, and a durable Fact before the interview answer was saved")
	return decision.Fact.ID, decision.Fact.Revision, nil
}

func baselineRequirementDisposition(assessment baselineJourneyAssessment, requirementID string) (string, bool) {
	for _, requirement := range assessment.Requirements {
		if requirement.ID == requirementID {
			return requirement.Disposition, true
		}
	}
	return "", false
}

func (j *journey) baselineCommand(ctx context.Context, target string, version uint64, input map[string]any) (baselineJourneyAssessment, error) {
	status, body, err := j.jsonRequestWithHeaders(ctx, http.MethodPost, target, input, true, map[string]string{"If-Match": fmt.Sprintf(`W/"%d"`, version)})
	if err != nil {
		return baselineJourneyAssessment{}, err
	}
	if status != http.StatusOK {
		return baselineJourneyAssessment{}, fmt.Errorf("status=%d body=%s", status, body)
	}
	return decodeBaselineJourneyAssessment(body)
}

func decodeBaselineJourneyAssessment(body []byte) (baselineJourneyAssessment, error) {
	var assessment baselineJourneyAssessment
	if err := json.Unmarshal(body, &assessment); err != nil {
		return baselineJourneyAssessment{}, err
	}
	if ids.Validate(assessment.ID) != nil || assessment.Version == 0 {
		return baselineJourneyAssessment{}, errors.New("Baseline response omitted a valid identity or version")
	}
	return assessment, nil
}

func (j *journey) awaitAccountCell(ctx context.Context, base string) (int, error) {
	for retries := 0; retries < 20; retries++ {
		status, body, err := j.jsonRequest(ctx, http.MethodGet, base+"/work-items/summary", nil, false)
		if err != nil {
			return retries, fmt.Errorf("probe fresh Account route: %w", err)
		}
		if status == http.StatusOK {
			return retries, nil
		}
		if status != http.StatusServiceUnavailable || !bytes.Contains(body, []byte(`"code":"account_unavailable"`)) {
			return retries, fmt.Errorf("probe fresh Account route: status=%d body=%s", status, body)
		}
		select {
		case <-ctx.Done():
			return retries, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return 20, errors.New("fresh Account cell projection did not become available")
}

func (j *journey) exerciseHTTPPlatform(ctx context.Context) error {
	base := j.config.appOrigin + "/api/v1/accounts/" + j.report.AccountID
	retries, err := j.awaitAccountCell(ctx, base)
	if err != nil {
		return err
	}
	if retries > 0 {
		j.pass("fresh Account HTTP retry", fmt.Sprintf("recovered after %d fail-closed route probes while cell provisioning completed", retries))
	}
	status, body, err := j.jsonRequest(ctx, http.MethodPost, base+"/work-items", map[string]any{
		"kind": "todo", "title": "Confirm Friday parts order", "description": "Check the supplier list before the afternoon cutoff.",
		"priority": "high", "assignment": map[string]any{"responsibility": "user", "user_id": j.report.UserID}, "reason": "external agent certification",
	}, true)
	if err != nil || status != http.StatusCreated {
		return fmt.Errorf("create Work item: status=%d body=%s err=%w", status, body, err)
	}
	workID, workVersion, err := objectIdentity(body)
	if err != nil {
		return fmt.Errorf("decode Work item: %w", err)
	}
	j.workID = workID
	status, body, err = j.jsonRequestWithHeaders(ctx, http.MethodPost, base+"/work-items/"+workID+"/transitions", map[string]any{"to": "in_progress", "reason": "certify lifecycle"}, true, map[string]string{"If-Match": fmt.Sprintf(`W/"%d"`, workVersion)})
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("transition Work item: status=%d body=%s err=%w", status, body, err)
	}
	_, j.workVersion, err = objectIdentity(body)
	if err != nil {
		return fmt.Errorf("decode transitioned Work item: %w", err)
	}
	status, body, err = j.jsonRequest(ctx, http.MethodGet, base+"/work-items/summary", nil, false)
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("read Your Turn summary: status=%d body=%s err=%w", status, body, err)
	}
	j.pass("Work and Your Turn", "created, transitioned and summarized accountable owner Work through HTTP")

	status, body, err = j.jsonRequest(ctx, http.MethodPost, base+"/agent-boardrooms", map[string]any{"name": "Shop planning", "purpose": "Keep customer promises and the parts schedule aligned."}, true)
	if err != nil || status != http.StatusCreated {
		return fmt.Errorf("create Boardroom: status=%d body=%s err=%w", status, body, err)
	}
	boardroomID, boardroomVersion, err := objectIdentity(body)
	if err != nil {
		return fmt.Errorf("decode Boardroom: %w", err)
	}
	personaID := ids.RandomGenerator{}.New()
	persona := map[string]any{
		"persona_id": personaID, "expected_latest_version": 0, "name": "Service coordinator", "role": "Schedule and parts coordinator",
		"description": "Keeps promised dates realistic.", "system_instructions": "Review the supplied work and point out schedule conflicts. Do not take consequential actions.",
		"policy": map[string]any{"complexity": "simple", "maximum_input_tokens": 2000, "maximum_output_tokens": 500, "maximum_cost_micros": 100000, "maximum_tool_steps": 0, "citation_policy": "best_effort", "action_policy": "none", "tools": []any{}},
	}
	status, body, err = j.jsonRequest(ctx, http.MethodPost, base+"/agent-boardrooms/"+boardroomID+"/personas", persona, true)
	if err != nil || status != http.StatusCreated {
		return fmt.Errorf("publish persona: status=%d body=%s err=%w", status, body, err)
	}
	secondPersonaID := ids.RandomGenerator{}.New()
	secondPersona := map[string]any{
		"persona_id": secondPersonaID, "expected_latest_version": 0, "name": "Parts checker", "role": "Parts availability checker",
		"description": "Checks whether planned work has the required parts.", "system_instructions": "Review supplied work for parts dependencies. Report uncertainty and do not take consequential actions.",
		"policy": map[string]any{"complexity": "simple", "maximum_input_tokens": 2000, "maximum_output_tokens": 500, "maximum_cost_micros": 100000, "maximum_tool_steps": 0, "citation_policy": "best_effort", "action_policy": "none", "tools": []any{}},
	}
	status, body, err = j.jsonRequest(ctx, http.MethodPost, base+"/agent-boardrooms/"+boardroomID+"/personas", secondPersona, true)
	if err != nil || status != http.StatusCreated {
		return fmt.Errorf("publish second persona: status=%d body=%s err=%w", status, body, err)
	}
	status, body, err = j.jsonRequest(ctx, http.MethodPut, base+"/agent-boardrooms/"+boardroomID+"/manager", map[string]any{"manager_persona_id": personaID, "expected_version": boardroomVersion}, true)
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("configure Boardroom manager: status=%d body=%s err=%w", status, body, err)
	}
	_, managerVersion, err := objectIdentity(body)
	if err != nil {
		return fmt.Errorf("decode revised Boardroom: %w", err)
	}
	status, staleBody, staleErr := j.jsonRequest(ctx, http.MethodPut, base+"/agent-boardrooms/"+boardroomID+"/manager", map[string]any{"manager_persona_id": secondPersonaID, "expected_version": boardroomVersion}, true)
	if staleErr != nil || (status != http.StatusConflict && status != http.StatusPreconditionFailed) {
		return fmt.Errorf("stale Boardroom revision did not fail closed: status=%d body=%s err=%w", status, staleBody, staleErr)
	}
	status, body, err = j.jsonRequest(ctx, http.MethodPut, base+"/agent-boardrooms/"+boardroomID+"/manager", map[string]any{"manager_persona_id": secondPersonaID, "expected_version": managerVersion}, true)
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("recover Boardroom manager revision: status=%d body=%s err=%w", status, body, err)
	}
	j.pass("stale-version recovery", fmt.Sprintf("stale Boardroom version %d was rejected; current version %d remained authoritative", boardroomVersion, managerVersion))

	status, body, err = j.jsonRequest(ctx, http.MethodPost, base+"/agent-boardrooms/"+boardroomID+"/runs", map[string]any{
		"subject": "Friday readiness", "prompt": "Review the parts-order Work item and identify schedule risk.", "mode": "selected", "persona_ids": []string{personaID}, "context": map[string]any{"work_item_ids": []string{workID}},
	}, true)
	if err != nil || (status != http.StatusCreated && status != http.StatusAccepted) {
		return fmt.Errorf("start Agent run: status=%d body=%s err=%w", status, body, err)
	}
	runID, _, err := objectIdentity(body)
	if err != nil {
		return fmt.Errorf("decode Agent run: %w", err)
	}
	j.runID = runID
	status, body, err = j.jsonRequest(ctx, http.MethodGet, base+"/agent-runs/"+runID, nil, false)
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("read Agent run: status=%d body=%s err=%w", status, body, err)
	}
	var run struct {
		InvocationIDs []string `json:"invocation_ids"`
		PolicyVersion uint64   `json:"policy_version"`
	}
	if json.Unmarshal(body, &run) != nil || len(run.InvocationIDs) == 0 || run.PolicyVersion == 0 {
		return errors.New("Agent run omitted invocation authority")
	}
	j.invocationID = run.InvocationIDs[0]
	j.policyVersion = run.PolicyVersion
	if j.config.providerFixture == "deterministic-fail-once" {
		failed, err := j.waitForAgentRun(ctx, base, runID, "failed")
		if err != nil {
			return fmt.Errorf("wait for deterministic provider failure: %w", err)
		}
		if len(failed.InvocationIDs) != 1 {
			return errors.New("failed Agent run lost its invocation identity")
		}
		status, body, err = j.jsonRequest(ctx, http.MethodPost, base+"/agent-runs/"+runID+"/resolutions", map[string]any{
			"action": "retry_failed", "note": "Retry the deterministic transient provider failure.",
		}, true)
		if err != nil || (status != http.StatusOK && status != http.StatusCreated) {
			return fmt.Errorf("retry failed Agent run: status=%d body=%s err=%w", status, body, err)
		}
		retryRunID := nestedString(body, "retry_run_id")
		if ids.Validate(retryRunID) != nil {
			return fmt.Errorf("retry resolution omitted retry run: %s", body)
		}
		retryRun, err := j.waitForAgentRun(ctx, base, retryRunID, "succeeded")
		if err != nil {
			return fmt.Errorf("wait for retried Agent run: %w", err)
		}
		if len(retryRun.InvocationIDs) != 1 || retryRun.PolicyVersion == 0 || retryRun.Invocations[0].Status != "succeeded" || retryRun.Invocations[0].Usage.TotalTokens == 0 {
			return fmt.Errorf("retried Agent run omitted successful usage evidence: %#v", retryRun)
		}
		j.runID, j.invocationID, j.policyVersion = retryRunID, retryRun.InvocationIDs[0], retryRun.PolicyVersion
		j.pass("Agent execution retry", "a deterministic provider failure reached failed, the documented human retry boundary created a new Run, and the local model completed it with metered usage")

		if err := exhaustLocalAITokens(ctx, j.config.databaseURL, j.report.AccountID); err != nil {
			return fmt.Errorf("exhaust local AI Tokens: %w", err)
		}
		status, body, err = j.jsonRequest(ctx, http.MethodPost, base+"/agent-boardrooms/"+boardroomID+"/runs", map[string]any{
			"subject": "Token boundary", "prompt": "Prove that an Agent cannot run without available AI Tokens.", "mode": "selected", "persona_ids": []string{personaID}, "context": map[string]any{},
		}, true)
		if err != nil || (status != http.StatusCreated && status != http.StatusAccepted) {
			return fmt.Errorf("start exhausted-token Agent run: status=%d body=%s err=%w", status, body, err)
		}
		exhaustedRunID, _, decodeErr := objectIdentity(body)
		if decodeErr != nil {
			return fmt.Errorf("decode exhausted-token Agent run: %w", decodeErr)
		}
		exhaustedRun, waitErr := j.waitForAgentRun(ctx, base, exhaustedRunID, "failed")
		if waitErr != nil {
			return fmt.Errorf("wait for exhausted-token denial: %w", waitErr)
		}
		if len(exhaustedRun.Invocations) != 1 || exhaustedRun.Invocations[0].Status != "failed" || exhaustedRun.Invocations[0].Usage.TotalTokens != 0 {
			return fmt.Errorf("exhausted-token Run did not fail before model use: %#v", exhaustedRun)
		}
		j.pass("insufficient AI Tokens", "an exhausted Account grant failed the queued invocation before any model usage")
	}
	j.pass("Agents and Boardroom", "published bounded Personas, configured a manager and exercised an Account-scoped run")

	recurrence := map[string]any{"frequency": "daily", "local_hour": 7, "local_minute": 30, "weekdays": nil, "gap_policy": "next_valid", "overlap_policy": "first"}
	template := map[string]any{"boardroom_id": boardroomID, "mode": "selected", "persona_ids": []string{personaID}, "subject": "Morning schedule check", "prompt": "Review open work for schedule risks.", "work_item_ids": []string{workID}, "knowledge_fact_ids": []string{}, "knowledge_document_ids": []string{}, "baseline_assessment_ids": []string{}}
	status, body, err = j.jsonRequest(ctx, http.MethodPost, base+"/schedules", map[string]any{"name": "Morning schedule check", "timezone": "America/New_York", "recurrence": recurrence, "missed_run_policy": "catch_up_one", "template": template, "reason": "external agent certification"}, true)
	if err != nil || status != http.StatusCreated {
		return fmt.Errorf("create Schedule: status=%d body=%s err=%w", status, body, err)
	}
	scheduleID, scheduleVersion, err := objectIdentity(body)
	if err != nil {
		return fmt.Errorf("decode Schedule: %w", err)
	}
	status, body, err = j.jsonRequest(ctx, http.MethodPost, base+"/schedules/"+scheduleID+"/pauses", map[string]any{"expected_version": scheduleVersion, "reason": "certify pause"}, true)
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("pause Schedule: status=%d body=%s err=%w", status, body, err)
	}
	_, pausedVersion, err := objectIdentity(body)
	if err != nil {
		return err
	}
	status, staleBody, staleErr = j.jsonRequest(ctx, http.MethodPost, base+"/schedules/"+scheduleID+"/resumptions", map[string]any{"expected_version": scheduleVersion, "reason": "stale retry"}, true)
	if staleErr != nil || status != http.StatusConflict {
		return fmt.Errorf("stale Schedule version did not fail closed: status=%d body=%s err=%w", status, staleBody, staleErr)
	}
	status, body, err = j.jsonRequest(ctx, http.MethodPost, base+"/schedules/"+scheduleID+"/resumptions", map[string]any{"expected_version": pausedVersion, "reason": "recover with current version"}, true)
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("resume Schedule: status=%d body=%s err=%w", status, body, err)
	}
	_, resumedVersion, err := objectIdentity(body)
	if err != nil {
		return err
	}
	status, body, err = j.jsonRequest(ctx, http.MethodPost, base+"/schedules/"+scheduleID+"/triggers", map[string]any{"expected_version": resumedVersion, "reason": "certify manual trigger"}, true)
	if err != nil || (status != http.StatusOK && status != http.StatusAccepted && status != http.StatusCreated) {
		return fmt.Errorf("trigger Schedule: status=%d body=%s err=%w", status, body, err)
	}
	j.pass("Scheduling", "created, paused, stale-retried, resumed and manually triggered an Agent schedule")
	return nil
}

type agentRunView struct {
	State         string   `json:"state"`
	InvocationIDs []string `json:"invocation_ids"`
	PolicyVersion uint64   `json:"policy_version"`
	Invocations   []struct {
		Status string `json:"status"`
		Usage  struct {
			TotalTokens int64 `json:"total_tokens"`
		} `json:"usage"`
	} `json:"invocations"`
}

func (j *journey) waitForAgentRun(ctx context.Context, base, runID string, wanted ...string) (agentRunView, error) {
	deadline := time.Now().Add(90 * time.Second)
	for {
		status, body, err := j.jsonRequest(ctx, http.MethodGet, base+"/agent-runs/"+runID, nil, false)
		if err != nil || status != http.StatusOK {
			return agentRunView{}, fmt.Errorf("read run: status=%d body=%s err=%w", status, body, err)
		}
		var run agentRunView
		if err := json.Unmarshal(body, &run); err != nil {
			return agentRunView{}, err
		}
		if slices.Contains(wanted, run.State) {
			return run, nil
		}
		if slices.Contains([]string{"succeeded", "partially_failed", "failed", "canceled"}, run.State) {
			return agentRunView{}, fmt.Errorf("run reached unexpected terminal state %q", run.State)
		}
		if time.Now().After(deadline) {
			return agentRunView{}, fmt.Errorf("run remained %q past timeout", run.State)
		}
		select {
		case <-ctx.Done():
			return agentRunView{}, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func nestedString(raw []byte, key string) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	var visit func(any) string
	visit = func(candidate any) string {
		switch typed := candidate.(type) {
		case map[string]any:
			if found, ok := typed[key].(string); ok {
				return found
			}
			for _, nested := range typed {
				if found := visit(nested); found != "" {
					return found
				}
			}
		case []any:
			for _, nested := range typed {
				if found := visit(nested); found != "" {
					return found
				}
			}
		}
		return ""
	}
	return visit(value)
}

func objectIdentity(raw []byte) (string, uint64, error) {
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", 0, err
	}
	if nested, ok := value["item"].(map[string]any); ok {
		value = nested
	}
	if nested, ok := value["boardroom"].(map[string]any); ok {
		value = nested
	}
	if nested, ok := value["run"].(map[string]any); ok {
		value = nested
	}
	if nested, ok := value["schedule"].(map[string]any); ok {
		value = nested
	}
	id, _ := value["id"].(string)
	versionNumber, _ := value["version"].(float64)
	if id == "" {
		return "", 0, errors.New("response omitted object ID")
	}
	return id, uint64(versionNumber), nil
}

func (j *journey) register(ctx context.Context, email, password string) error {
	status, _, err := j.jsonRequest(ctx, http.MethodPost, j.config.appOrigin+"/api/v1/registrations", map[string]any{
		"email": email, "display_name": "External Agent Certification", "account_name": "Synthetic Wrench Works " + email, "region": "us-east",
	}, false)
	if err != nil || status != http.StatusAccepted {
		return fmt.Errorf("begin registration: status=%d err=%w", status, err)
	}
	j.pass("registration discovery", "fresh synthetic identity accepted through documented HTTP")
	token, err := waitForVerificationToken(ctx, j.config.mailpitURL, email)
	if err != nil {
		return err
	}
	status, body, err := j.jsonRequest(ctx, http.MethodPost, j.config.appOrigin+"/api/v1/registrations/verify", map[string]any{"token": token, "password": password}, false)
	if err != nil || status != http.StatusCreated {
		return fmt.Errorf("complete registration: status=%d body=%s err=%w", status, body, err)
	}
	var provisioned struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
	}
	if err := json.Unmarshal(body, &provisioned); err != nil || provisioned.User.ID == "" || provisioned.Account.ID == "" {
		return fmt.Errorf("decode provisioned account: %w", err)
	}
	j.report.UserID, j.report.AccountID = provisioned.User.ID, provisioned.Account.ID
	j.pass("email verification", "verification completed through local notification provider")

	status, body, err = j.jsonRequest(ctx, http.MethodPost, j.config.appOrigin+"/api/v1/sessions", map[string]any{"email": email, "password": password}, false)
	if err != nil || status != http.StatusCreated {
		return fmt.Errorf("password login: status=%d body=%s err=%w", status, body, err)
	}
	j.pass("password login", "session established without customer browser UI")

	status, body, err = j.jsonRequest(ctx, http.MethodPost, j.config.appOrigin+"/api/v1/session/account", map[string]any{"account_id": j.report.AccountID}, true)
	if err != nil || status != http.StatusForbidden || !bytes.Contains(body, []byte("owner_security_enrollment_required")) {
		return fmt.Errorf("unenrolled owner boundary: status=%d body=%s err=%w", status, body, err)
	}
	j.pass("strong-auth denial", "Account selection failed closed before passkey and recovery enrollment")

	if err := j.enrollPasskey(ctx); err != nil {
		return err
	}
	status, body, err = j.jsonRequest(ctx, http.MethodPost, j.config.appOrigin+"/api/v1/recovery-codes", map[string]any{}, true)
	if err != nil || status != http.StatusCreated {
		return fmt.Errorf("rotate recovery codes: status=%d body=%s err=%w", status, body, err)
	}
	var recovery struct {
		Codes []string `json:"codes"`
	}
	if json.Unmarshal(body, &recovery) != nil || len(recovery.Codes) != 10 {
		return errors.New("recovery code rotation did not return ten one-time codes")
	}
	j.pass("owner recovery enrollment", "ten one-time recovery codes issued; values omitted from certificate")
	return nil
}

func (j *journey) enrollPasskey(ctx context.Context) error {
	status, body, err := j.jsonRequest(ctx, http.MethodPost, j.config.appOrigin+"/api/v1/passkey-registrations", map[string]any{}, true)
	if err != nil || status != http.StatusCreated {
		return fmt.Errorf("begin passkey registration: status=%d body=%s err=%w", status, body, err)
	}
	var ceremony struct {
		ID        string `json:"ceremony_id"`
		PublicKey struct {
			PublicKey struct {
				Challenge string `json:"challenge"`
			} `json:"publicKey"`
		} `json:"public_key"`
	}
	if json.Unmarshal(body, &ceremony) != nil || ceremony.ID == "" || ceremony.PublicKey.PublicKey.Challenge == "" {
		return errors.New("invalid passkey registration ceremony")
	}
	credential, err := registrationCredential(ceremony.PublicKey.PublicKey.Challenge, j.config.appOrigin, "infiniteocean.localhost")
	if err != nil {
		return err
	}
	status, body, err = j.jsonRequest(ctx, http.MethodPost, j.config.appOrigin+"/api/v1/passkey-registrations/"+ceremony.ID+"/complete", map[string]any{"name": "External agent certification passkey", "credential": credential}, true)
	if err != nil || status != http.StatusCreated {
		return fmt.Errorf("complete passkey registration: status=%d body=%s err=%w", status, body, err)
	}
	j.pass("software passkey enrollment", "ES256 user-verifying credential enrolled through WebAuthn HTTP contract")
	return nil
}

func (j *journey) selectAccount(ctx context.Context) error {
	status, body, err := j.jsonRequest(ctx, http.MethodPost, j.config.appOrigin+"/api/v1/session/account", map[string]any{"account_id": j.report.AccountID}, true)
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("select account: status=%d body=%s err=%w", status, body, err)
	}
	j.pass("Account selection", "owner security posture and placement accepted")
	return nil
}

func (j *journey) purchaseLocalSubscription(ctx context.Context) error {
	base := j.config.appOrigin + "/api/v1/accounts/" + j.report.AccountID
	status, body, err := j.jsonRequest(ctx, http.MethodPost, base+"/checkout-sessions", map[string]any{
		"offer_code": "team-monthly-v2", "include_commissioning": false,
	}, true)
	if err != nil || status != http.StatusCreated {
		return fmt.Errorf("create Stripe Checkout session: status=%d body=%s err=%w", status, body, err)
	}
	var checkout struct {
		SessionID string `json:"session_id"`
		URL       string `json:"url"`
	}
	if json.Unmarshal(body, &checkout) != nil || checkout.SessionID == "" || checkout.URL == "" {
		return errors.New("Stripe Checkout response omitted its session or hosted URL")
	}
	if !strings.HasPrefix(checkout.URL, "https://stripe.infiniteocean.localhost:8444/checkout/") {
		return fmt.Errorf("Stripe Checkout returned an unexpected hosted URL: %s", checkout.URL)
	}
	j.pass("Stripe Checkout creation", "the real Account billing API created a hosted $50 monthly subscription session")

	fixtureClient := &http.Client{Timeout: 10 * time.Second}
	hostedURL := strings.TrimRight(j.config.stripeFixtureURL, "/") + "/checkout/" + url.PathEscape(checkout.SessionID)
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, hostedURL, nil)
	response, err := fixtureClient.Do(request)
	if err != nil {
		return fmt.Errorf("open local hosted checkout: %w", err)
	}
	hostedBody, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Contains(hostedBody, []byte("$50.00 USD per month")) {
		return fmt.Errorf("local hosted checkout did not show the expected subscription: status=%d", response.StatusCode)
	}
	j.pass("hosted checkout contract", "the deterministic provider page displayed $50 USD per month and no commissioning charge")

	completeURL := strings.TrimRight(j.config.stripeFixtureURL, "/") + "/test/checkout/" + url.PathEscape(checkout.SessionID) + "/complete"
	request, _ = http.NewRequestWithContext(ctx, http.MethodPost, completeURL, nil)
	request.Header.Set("X-Stripe-Fixture-Token", j.config.stripeFixtureToken)
	response, err = fixtureClient.Do(request)
	if err != nil {
		return fmt.Errorf("complete local hosted checkout: %w", err)
	}
	completionBody, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Contains(completionBody, []byte(`"events_delivered":6`)) {
		return fmt.Errorf("complete local hosted checkout: status=%d body=%s", response.StatusCode, completionBody)
	}
	appURL, _ := url.Parse(j.config.appOrigin)
	if !slices.ContainsFunc(j.client.Jar.Cookies(appURL), func(cookie *http.Cookie) bool { return cookie.Name == "__Host-spyglass_session" && cookie.Value != "" }) {
		return errors.New("the authenticated session cookie disappeared while the provider completed checkout")
	}
	j.pass("signed Stripe delivery", "current-shape checkout, subscription and invoice events were signed and delivered, including an exact duplicate")

	deadline := time.Now().Add(30 * time.Second)
	var lastDetail string
	for time.Now().Before(deadline) {
		ready, detail, err := j.commercialProjectionReady(ctx, base)
		if err != nil {
			lastDetail = err.Error()
		} else if ready {
			j.pass("billing projection", "the worker projected one active team subscription after out-of-order provider events")
			j.pass("provider replay idempotency", "six deliveries produced five unique processed inbox events and one subscription")
			j.pass("package access", "every package in the purchased team plan became enabled through subscription grants")
			j.pass("included AI Tokens", "exactly one configurable monthly included-token grant became available")
			return nil
		} else {
			lastDetail = detail
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return fmt.Errorf("commercial projection did not converge: %s", lastDetail)
}

func (j *journey) commercialProjectionReady(ctx context.Context, base string) (bool, string, error) {
	status, body, err := j.jsonRequest(ctx, http.MethodGet, base+"/billing", nil, false)
	if err != nil || status != http.StatusOK {
		return false, fmt.Sprintf("billing status=%d body=%s", status, body), err
	}
	var billingStatus struct {
		Subscriptions []struct {
			State     string `json:"state"`
			OfferCode string `json:"offer_code"`
		} `json:"subscriptions"`
	}
	if err := json.Unmarshal(body, &billingStatus); err != nil {
		return false, "invalid billing status", err
	}
	if len(billingStatus.Subscriptions) != 1 || billingStatus.Subscriptions[0].State != "active" || billingStatus.Subscriptions[0].OfferCode != "team-monthly-v2" {
		return false, "active team subscription is not projected yet", nil
	}
	status, body, err = j.jsonRequest(ctx, http.MethodGet, base+"/ai-tokens", nil, false)
	if err != nil || status != http.StatusOK {
		return false, fmt.Sprintf("AI Token status=%d body=%s", status, body), err
	}
	var balance struct {
		Available, Included int64
	}
	if err := json.Unmarshal(body, &balance); err != nil {
		return false, "invalid AI Token balance", err
	}

	pool, err := pgxpool.New(ctx, j.config.databaseURL)
	if err != nil {
		return false, "connect to projection database", err
	}
	defer pool.Close()
	var publicationRaw []byte
	if err := pool.QueryRow(ctx, `SELECT content FROM catalog_publications WHERE state='published' ORDER BY version DESC LIMIT 1`).Scan(&publicationRaw); err != nil {
		return false, "read published Catalog", err
	}
	var publication catalog.PublishedCatalog
	if err := json.Unmarshal(publicationRaw, &publication); err != nil {
		return false, "decode published Catalog", err
	}
	var expectedPackages int
	for _, plan := range publication.Plans {
		if plan.Code == "team" {
			expectedPackages = len(plan.Packages)
		}
	}
	if expectedPackages == 0 || publication.AITokenRenewalGrant == nil {
		return false, "published commercial Catalog is incomplete", nil
	}
	if balance.Available != publication.AITokenRenewalGrant.Quantity || balance.Included != publication.AITokenRenewalGrant.Quantity {
		return false, "included AI Token grant is not projected yet", nil
	}
	var uniqueEvents, processedEvents, subscriptions, grants, tokenGrants int
	err = pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM billing_event_inbox WHERE account_id=$1),
			(SELECT count(*) FROM billing_event_inbox WHERE account_id=$1 AND processing_state='processed'),
			(SELECT count(*) FROM subscriptions WHERE account_id=$1 AND state='active' AND offer_code='team-monthly-v2'),
			(SELECT count(*) FROM entitlement_grants WHERE account_id=$1 AND source='subscription'),
			(SELECT count(*) FROM ai_token_grants WHERE account_id=$1 AND origin='included' AND state='active')`, j.report.AccountID).
		Scan(&uniqueEvents, &processedEvents, &subscriptions, &grants, &tokenGrants)
	if err != nil {
		return false, "read commercial projections", err
	}
	if uniqueEvents != 5 || processedEvents != 5 || subscriptions != 1 || grants != expectedPackages || tokenGrants != 1 {
		return false, fmt.Sprintf("events=%d/%d subscriptions=%d grants=%d/%d token_grants=%d", processedEvents, uniqueEvents, subscriptions, grants, expectedPackages, tokenGrants), nil
	}
	return true, "ready", nil
}

func (j *journey) authorizeMCP(ctx context.Context) (string, error) {
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return "", err
	}
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	state := hex.EncodeToString(verifierBytes[:12])
	query := url.Values{
		"response_type": {"code"}, "client_id": {localClientID}, "redirect_uri": {localRedirect},
		"resource": {j.config.mcpOrigin}, "scope": {"spyglass:mcp"}, "state": {state},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"},
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, j.config.appOrigin+"/oauth/authorize?"+query.Encode(), nil)
	response, err := j.client.Do(request)
	if err != nil {
		return "", err
	}
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("authorization request: status=%d body=%s", response.StatusCode, body)
	}
	pending := regexp.MustCompile(`name="pending_id" value="([0-9a-f-]{36})"`).FindSubmatch(body)
	if len(pending) != 2 {
		return "", errors.New("OAuth approval page omitted pending authorization ID")
	}
	decision := url.Values{"pending_id": {string(pending[1])}, "decision": {"approve"}}
	request, _ = http.NewRequestWithContext(ctx, http.MethodPost, j.config.appOrigin+"/oauth/authorize", strings.NewReader(decision.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", j.config.appOrigin)
	redirectClient := *j.client
	redirectClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err = redirectClient.Do(request)
	if err != nil {
		return "", err
	}
	response.Body.Close()
	location, err := url.Parse(response.Header.Get("Location"))
	if err != nil || response.StatusCode != http.StatusSeeOther || location.Query().Get("state") != state || location.Query().Get("code") == "" {
		return "", fmt.Errorf("OAuth approval redirect invalid: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {location.Query().Get("code")}, "client_id": {localClientID},
		"redirect_uri": {localRedirect}, "resource": {j.config.mcpOrigin}, "code_verifier": {verifier},
	}
	request, _ = http.NewRequestWithContext(ctx, http.MethodPost, j.config.appOrigin+"/oauth/token", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err = j.client.Do(request)
	if err != nil {
		return "", err
	}
	body, _ = io.ReadAll(io.LimitReader(response.Body, 1<<20))
	response.Body.Close()
	var tokens struct {
		AccessToken string `json:"access_token"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &tokens) != nil || tokens.AccessToken == "" {
		return "", fmt.Errorf("OAuth token exchange: status=%d body=%s", response.StatusCode, body)
	}
	j.pass("MCP OAuth authorization", "human-authorized exact client/resource/scope grant exchanged with S256 PKCE")
	return tokens.AccessToken, nil
}

func (j *journey) listTools(ctx context.Context, accessToken string) error {
	payload := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": map[string]any{"_meta": map[string]any{
		"io.modelcontextprotocol/protocolVersion":    protocolVersion,
		"io.modelcontextprotocol/clientCapabilities": map[string]any{},
		"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "spyglass-agent-journey-cert", "version": "1"},
	}}}
	raw, _ := json.Marshal(payload)
	var response *http.Response
	var body []byte
	retries := 0
	for attempt := 0; attempt < 20; attempt++ {
		request, _ := http.NewRequestWithContext(ctx, http.MethodPost, j.config.mcpOrigin+"/mcp/v1/accounts/"+j.report.AccountID, bytes.NewReader(raw))
		request.Header.Set("Authorization", "Bearer "+accessToken)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		request.Header.Set("MCP-Protocol-Version", protocolVersion)
		request.Header.Set("Mcp-Method", "tools/list")
		var err error
		response, err = j.client.Do(request)
		if err != nil {
			return err
		}
		body, _ = io.ReadAll(io.LimitReader(response.Body, 4<<20))
		response.Body.Close()
		if response.StatusCode != http.StatusServiceUnavailable || !bytes.Contains(body, []byte(`"code":"account_unavailable"`)) {
			break
		}
		retries++
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	if retries > 0 {
		j.pass("fresh Account route retry", fmt.Sprintf("recovered from %d fail-closed responses while the durable cell projection completed", retries))
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("MCP tools/list: status=%d body=%s", response.StatusCode, body)
	}
	var envelope struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return err
	}
	for _, tool := range envelope.Result.Tools {
		j.report.Tools = append(j.report.Tools, tool.Name)
		j.covered[tool.Name] = toolCoverage{Name: tool.Name, Outcome: "discovered", Detail: "published by authenticated tools/list"}
	}
	slices.Sort(j.report.Tools)
	j.report.ToolCount = len(j.report.Tools)
	j.pass("MCP discovery", fmt.Sprintf("%d Account-scoped tools discovered through authenticated external transport", len(j.report.Tools)))
	return nil
}

type mcpToolResult struct {
	IsError    bool
	Text       string
	Structured map[string]any
}

func (j *journey) callTool(ctx context.Context, accessToken, name string, arguments map[string]any) (mcpToolResult, error) {
	payload := map[string]any{"jsonrpc": "2.0", "id": ids.RandomGenerator{}.New(), "method": "tools/call", "params": map[string]any{
		"name": name, "arguments": arguments, "_meta": map[string]any{
			"io.modelcontextprotocol/protocolVersion":    protocolVersion,
			"io.modelcontextprotocol/clientCapabilities": map[string]any{},
			"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "spyglass-agent-journey-cert", "version": "1"},
		},
	}}
	status, body, err := j.mcpRequest(ctx, j.report.AccountID, accessToken, "tools/call", name, payload)
	if err != nil {
		return mcpToolResult{}, err
	}
	if status != http.StatusOK {
		return mcpToolResult{}, fmt.Errorf("%s returned HTTP %d: %s", name, status, body)
	}
	var envelope struct {
		Result struct {
			IsError           bool           `json:"isError"`
			StructuredContent map[string]any `json:"structuredContent"`
			Content           []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return mcpToolResult{}, err
	}
	if envelope.Error != nil {
		return mcpToolResult{}, fmt.Errorf("%s JSON-RPC error %d: %s", name, envelope.Error.Code, envelope.Error.Message)
	}
	result := mcpToolResult{IsError: envelope.Result.IsError, Structured: envelope.Result.StructuredContent}
	for _, content := range envelope.Result.Content {
		if content.Type == "text" {
			result.Text += content.Text
		}
	}
	outcome := "executed"
	detail := "tool returned a structured success response"
	if result.IsError {
		outcome = "safe_rejection"
		detail = "tool reached its Account-scoped handler and rejected incomplete or inapplicable input"
	}
	j.covered[name] = toolCoverage{Name: name, Outcome: outcome, Detail: detail}
	return result, nil
}

func (j *journey) requireTool(ctx context.Context, accessToken, name string, arguments map[string]any) (map[string]any, error) {
	result, err := j.callTool(ctx, accessToken, name, arguments)
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, fmt.Errorf("%s rejected valid certification input: %s", name, result.Text)
	}
	return result.Structured, nil
}

func objectFromStructured(value map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if nested, ok := value[key].(map[string]any); ok {
			return nested
		}
	}
	return value
}

func structuredIdentity(value map[string]any, keys ...string) (string, uint64, error) {
	value = objectFromStructured(value, keys...)
	id, _ := value["id"].(string)
	version, _ := value["version"].(float64)
	if id == "" {
		return "", 0, errors.New("structured tool result omitted object ID")
	}
	return id, uint64(version), nil
}

func newOperation() string { return ids.RandomGenerator{}.New() }

func (j *journey) exerciseMCPPlatform(ctx context.Context, accessToken string) error {
	account := j.report.AccountID

	assessment, err := j.requireTool(ctx, accessToken, "spyglass_baseline_start", map[string]any{"account_id": account, "operation_id": newOperation()})
	if err != nil {
		return err
	}
	assessmentID, _, err := structuredIdentity(assessment)
	if err != nil {
		return err
	}
	if _, err := j.requireTool(ctx, accessToken, "spyglass_baseline_get", map[string]any{"account_id": account, "assessment_id": assessmentID}); err != nil {
		return err
	}
	if _, err := j.requireTool(ctx, accessToken, "spyglass_baseline_source_list", map[string]any{"account_id": account, "assessment_id": assessmentID, "limit": 25}); err != nil {
		return err
	}
	j.pass("Baseline MCP", "started and retrieved a version-frozen assessment and its bounded source-grant view")

	evidence, err := j.requireTool(ctx, accessToken, "spyglass_knowledge_evidence_register", map[string]any{
		"account_id": account, "operation_id": newOperation(), "source_kind": "owner_statement", "source_reference": "external-agent-certification",
		"source_revision": "1", "content_sha256": strings.Repeat("11", 32), "captured_at": time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano),
	})
	if err != nil {
		return err
	}
	evidenceID, _, err := structuredIdentity(evidence)
	if err != nil {
		return err
	}
	claim, err := j.requireTool(ctx, accessToken, "spyglass_knowledge_claim_propose", map[string]any{
		"account_id": account, "operation_id": newOperation(), "scope": map[string]any{"kind": "account"}, "key": "operations.certification_note",
		"value": map[string]any{"text": "Synthetic evidence is not production truth."}, "confidence": 700, "sensitivity": "internal",
		"citations": []any{map[string]any{"evidence_id": evidenceID, "evidence_kind": "owner_statement", "relation": "supports", "locator": "statement:1"}},
	})
	if err != nil {
		return err
	}
	claimID, claimVersion, err := structuredIdentity(claim)
	if err != nil {
		return err
	}
	if _, err := j.requireTool(ctx, accessToken, "spyglass_knowledge_claim_get", map[string]any{"account_id": account, "claim_id": claimID}); err != nil {
		return err
	}
	if _, err := j.requireTool(ctx, accessToken, "spyglass_knowledge_claim_list", map[string]any{"account_id": account, "limit": 25}); err != nil {
		return err
	}
	if _, err := j.requireTool(ctx, accessToken, "spyglass_knowledge_claim_decide", map[string]any{"account_id": account, "operation_id": newOperation(), "claim_id": claimID, "expected_version": claimVersion, "accept": false, "reason": "Synthetic certification evidence must not become Account truth."}); err != nil {
		return err
	}
	if _, err := j.requireTool(ctx, accessToken, "spyglass_knowledge_fact_list", map[string]any{"account_id": account, "scope": map[string]any{"kind": "account"}, "limit": 25}); err != nil {
		return err
	}
	if _, err := j.requireTool(ctx, accessToken, "spyglass_knowledge_document_retrieve", map[string]any{"account_id": account, "query": "schedule risk", "limit": 3}); err != nil {
		return err
	}
	j.pass("Knowledge MCP", "registered immutable evidence, proposed/read/listed/rejected a claim, and queried fact and document projections")

	ledger, err := j.requireTool(ctx, accessToken, "spyglass_finance_ledger_create", map[string]any{"account_id": account, "operation_id": newOperation(), "name": "Certification ledger", "code": "CERT", "description": "Synthetic local-only ledger", "currency": "USD"})
	if err != nil {
		return err
	}
	ledgerID, ledgerVersion, err := structuredIdentity(ledger)
	if err != nil {
		return err
	}
	ledger, err = j.requireTool(ctx, accessToken, "spyglass_finance_ledger_revise", map[string]any{"account_id": account, "operation_id": newOperation(), "ledger_id": ledgerID, "expected_version": ledgerVersion, "name": "Certification operations ledger", "code": "CERT", "description": "Synthetic local-only operating ledger"})
	if err != nil {
		return err
	}
	_, ledgerVersion, _ = structuredIdentity(ledger)
	if _, err := j.requireTool(ctx, accessToken, "spyglass_finance_ledger_get", map[string]any{"account_id": account, "ledger_id": ledgerID}); err != nil {
		return err
	}
	if _, err := j.requireTool(ctx, accessToken, "spyglass_finance_ledger_list", map[string]any{"account_id": account, "limit": 25}); err != nil {
		return err
	}
	cash, err := j.requireTool(ctx, accessToken, "spyglass_finance_posting_account_create", map[string]any{"account_id": account, "operation_id": newOperation(), "ledger_id": ledgerID, "code": "1000", "name": "Certification cash", "description": "Synthetic cash", "type": "asset", "allow_posting": true})
	if err != nil {
		return err
	}
	cashID, cashVersion, err := structuredIdentity(cash)
	if err != nil {
		return err
	}
	cash, err = j.requireTool(ctx, accessToken, "spyglass_finance_posting_account_revise", map[string]any{"account_id": account, "operation_id": newOperation(), "posting_account_id": cashID, "expected_version": cashVersion, "code": "1000", "name": "Certification checking", "description": "Synthetic checking balance", "allow_posting": true})
	if err != nil {
		return err
	}
	_, cashVersion, _ = structuredIdentity(cash)
	revenue, err := j.requireTool(ctx, accessToken, "spyglass_finance_posting_account_create", map[string]any{"account_id": account, "operation_id": newOperation(), "ledger_id": ledgerID, "code": "4000", "name": "Certification revenue", "description": "Synthetic revenue", "type": "income", "allow_posting": true})
	if err != nil {
		return err
	}
	revenueID, _, err := structuredIdentity(revenue)
	if err != nil {
		return err
	}
	if _, err := j.requireTool(ctx, accessToken, "spyglass_finance_posting_account_get", map[string]any{"account_id": account, "posting_account_id": cashID}); err != nil {
		return err
	}
	if _, err := j.requireTool(ctx, accessToken, "spyglass_finance_posting_account_list", map[string]any{"account_id": account, "ledger_id": ledgerID, "limit": 25}); err != nil {
		return err
	}
	entry, err := j.requireTool(ctx, accessToken, "spyglass_finance_entry_create_draft", map[string]any{
		"account_id": account, "operation_id": newOperation(), "ledger_id": ledgerID, "entry_date": time.Now().UTC().Format(time.RFC3339),
		"description": "Synthetic service receipt", "reference": "CERT-1", "currency": "USD", "evidence": []any{evidenceID},
		"lines": []any{map[string]any{"account_id": cashID, "memo": "cash received", "debit_minor": 5000, "credit_minor": 0}, map[string]any{"account_id": revenueID, "memo": "service revenue", "debit_minor": 0, "credit_minor": 5000}},
	})
	if err != nil {
		return err
	}
	entryID, entryVersion, err := structuredIdentity(entry)
	if err != nil {
		return err
	}
	entry, err = j.requireTool(ctx, accessToken, "spyglass_finance_entry_revise_draft", map[string]any{
		"account_id": account, "operation_id": newOperation(), "entry_id": entryID, "expected_version": entryVersion, "entry_date": time.Now().UTC().Format(time.RFC3339),
		"description": "Synthetic service receipt revised", "reference": "CERT-1R", "evidence": []any{evidenceID},
		"lines": []any{map[string]any{"account_id": cashID, "memo": "cash received", "debit_minor": 5000, "credit_minor": 0}, map[string]any{"account_id": revenueID, "memo": "service revenue", "debit_minor": 0, "credit_minor": 5000}},
	})
	if err != nil {
		return err
	}
	_, entryVersion, _ = structuredIdentity(entry)
	entry, err = j.requireTool(ctx, accessToken, "spyglass_finance_entry_post", map[string]any{"account_id": account, "operation_id": newOperation(), "entry_id": entryID, "expected_version": entryVersion})
	if err != nil {
		return err
	}
	_, entryVersion, _ = structuredIdentity(entry)
	if _, err := j.requireTool(ctx, accessToken, "spyglass_finance_entry_get", map[string]any{"account_id": account, "entry_id": entryID}); err != nil {
		return err
	}
	if _, err := j.requireTool(ctx, accessToken, "spyglass_finance_entry_list", map[string]any{"account_id": account, "ledger_id": ledgerID, "limit": 25}); err != nil {
		return err
	}
	if _, err := j.requireTool(ctx, accessToken, "spyglass_finance_entry_reverse", map[string]any{"account_id": account, "operation_id": newOperation(), "entry_id": entryID, "expected_version": entryVersion, "entry_date": time.Now().UTC().Format(time.RFC3339), "description": "Reverse synthetic certification receipt", "reference": "CERT-REV", "evidence": []any{evidenceID}}); err != nil {
		return err
	}
	_ = ledgerVersion
	_ = cashVersion
	j.pass("Finance MCP", "created/revised/read a ledger and chart, then drafted, revised, posted, listed and reversed a balanced evidence-backed entry")

	campaign, err := j.requireTool(ctx, accessToken, "spyglass_marketing_campaign_create_draft", map[string]any{"account_id": account, "operation_id": newOperation(), "name": "Certification campaign", "objective": "Prove governed campaign drafting", "audience": "Synthetic local operators", "channels": []any{"web"}})
	if err != nil {
		return err
	}
	campaignID, campaignVersion, err := structuredIdentity(campaign)
	if err != nil {
		return err
	}
	campaign, err = j.requireTool(ctx, accessToken, "spyglass_marketing_campaign_revise", map[string]any{"account_id": account, "operation_id": newOperation(), "campaign_id": campaignID, "expected_version": campaignVersion, "name": "Certification campaign revised", "objective": "Prove governed campaign revision", "audience": "Synthetic local operators", "channels": []any{"web"}})
	if err != nil {
		return err
	}
	_, campaignVersion, _ = structuredIdentity(campaign)
	if _, err := j.requireTool(ctx, accessToken, "spyglass_marketing_campaign_get", map[string]any{"account_id": account, "campaign_id": campaignID}); err != nil {
		return err
	}
	if _, err := j.requireTool(ctx, accessToken, "spyglass_marketing_campaign_list", map[string]any{"account_id": account, "limit": 25}); err != nil {
		return err
	}
	if _, err := j.requireTool(ctx, accessToken, "spyglass_marketing_campaign_archive", map[string]any{"account_id": account, "operation_id": newOperation(), "campaign_id": campaignID, "expected_version": campaignVersion}); err != nil {
		return err
	}
	j.pass("Marketing MCP", "created, revised, retrieved, listed and archived a local campaign without publishing externally")

	connection, err := j.requireTool(ctx, accessToken, "spyglass_integrations_connection_create", map[string]any{"account_id": account, "operation_id": newOperation(), "name": "Certification email", "kind": "email", "capabilities": []any{"email.send"}, "scope": map[string]any{"email_address": "certification@example.test", "audience_reference": "audience:synthetic"}})
	if err != nil {
		return err
	}
	connectionID, connectionVersion, err := structuredIdentity(connection)
	if err != nil {
		return err
	}
	connection, err = j.requireTool(ctx, accessToken, "spyglass_integrations_connection_revise", map[string]any{"account_id": account, "operation_id": newOperation(), "connection_id": connectionID, "expected_version": connectionVersion, "name": "Certification email revised", "capabilities": []any{"email.send"}, "scope": map[string]any{"email_address": "certification@example.test", "audience_reference": "audience:synthetic-v2"}})
	if err != nil {
		return err
	}
	_, connectionVersion, _ = structuredIdentity(connection)
	if _, err := j.requireTool(ctx, accessToken, "spyglass_integrations_connection_get", map[string]any{"account_id": account, "connection_id": connectionID}); err != nil {
		return err
	}
	if _, err := j.requireTool(ctx, accessToken, "spyglass_integrations_connection_list", map[string]any{"account_id": account, "limit": 25}); err != nil {
		return err
	}
	if _, err := j.requireTool(ctx, accessToken, "spyglass_integrations_connection_revoke", map[string]any{"account_id": account, "operation_id": newOperation(), "connection_id": connectionID, "expected_version": connectionVersion}); err != nil {
		return err
	}
	j.pass("Integrations MCP", "created, revised, read, listed and revoked a non-secret pending connector without contacting a provider")

	if err := j.exerciseAttentionAndExport(ctx, accessToken); err != nil {
		return err
	}
	return nil
}

func (j *journey) exerciseAttentionAndExport(ctx context.Context, accessToken string) error {
	account := j.report.AccountID
	information, err := j.requireTool(ctx, accessToken, "spyglass_attention_information_create", map[string]any{"account_id": account, "operation_id": newOperation(), "parent_work_item_id": j.workID, "requirement": map[string]any{"key": "operations.parts_vendor", "scope": "account"}, "question": "Which vendor should supply the synthetic certification part?"})
	if err != nil {
		return err
	}
	requestID, requestVersion, err := structuredIdentity(information)
	if err != nil {
		return err
	}
	if _, err := j.requireTool(ctx, accessToken, "spyglass_attention_information_get", map[string]any{"account_id": account, "request_id": requestID}); err != nil {
		return err
	}
	if _, err := j.requireTool(ctx, accessToken, "spyglass_attention_information_list", map[string]any{"account_id": account, "limit": 25}); err != nil {
		return err
	}
	if _, err := j.requireTool(ctx, accessToken, "spyglass_attention_information_cancel", map[string]any{"account_id": account, "operation_id": newOperation(), "request_id": requestID, "expected_version": requestVersion, "reason": "Synthetic request completed by certification cleanup."}); err != nil {
		return err
	}

	approval, err := j.requireTool(ctx, accessToken, "spyglass_attention_approval_create", map[string]any{
		"account_id": account, "idempotency_key": newOperation(), "operation_id": newOperation(), "invocation_id": j.invocationID, "work_item_id": j.workID,
		"capability": "finance.payment.create", "payload": map[string]any{"amount_minor": 5000, "currency": "USD"}, "evidence_sha256": strings.Repeat("22", 32),
		"policy_version": j.policyVersion, "require_independent_review": true, "expires_at": time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339Nano),
	})
	if err != nil {
		return err
	}
	approvalID, approvalVersion, err := structuredIdentity(approval)
	if err != nil {
		return err
	}
	denied, err := j.callTool(ctx, accessToken, "spyglass_attention_approval_decide", map[string]any{"account_id": account, "operation_id": newOperation(), "approval_id": approvalID, "expected_version": approvalVersion, "decision": "approved", "reason": "Self approval must be rejected."})
	if err != nil || !denied.IsError || !strings.Contains(denied.Text, "attention_command_rejected") {
		return fmt.Errorf("independent approval boundary did not fail closed: result=%+v err=%w", denied, err)
	}
	if _, err := j.requireTool(ctx, accessToken, "spyglass_attention_approval_get", map[string]any{"account_id": account, "approval_id": approvalID}); err != nil {
		return err
	}
	if _, err := j.requireTool(ctx, accessToken, "spyglass_attention_approval_list", map[string]any{"account_id": account, "limit": 25}); err != nil {
		return err
	}
	if _, err := j.requireTool(ctx, accessToken, "spyglass_attention_approval_cancel", map[string]any{"account_id": account, "operation_id": newOperation(), "approval_id": approvalID, "expected_version": approvalVersion, "reason": "Clean up synthetic consequential proposal."}); err != nil {
		return err
	}
	j.pass("consequential-action boundary", "created an exact invocation-bound proposal, rejected self-approval under independent review, then canceled it without execution")

	export, err := j.requireTool(ctx, accessToken, "spyglass_account_export_request", map[string]any{"account_id": account})
	if err != nil {
		return err
	}
	exportID, exportVersion, err := structuredIdentity(export)
	if err != nil {
		return err
	}
	if _, err := j.requireTool(ctx, accessToken, "spyglass_account_export_get", map[string]any{"account_id": account, "export_id": exportID}); err != nil {
		return err
	}
	if _, err := j.requireTool(ctx, accessToken, "spyglass_account_export_list", map[string]any{"account_id": account, "limit": 25}); err != nil {
		return err
	}
	if _, err := j.requireTool(ctx, accessToken, "spyglass_account_export_cancel", map[string]any{"account_id": account, "export_id": exportID, "expected_version": exportVersion}); err != nil {
		return err
	}
	j.pass("Account export MCP", "requested, retrieved, listed and canceled a strongly authorized portability export")
	return nil
}

func (j *journey) exerciseEveryMCPTool(ctx context.Context, accessToken string) error {
	for _, name := range j.report.Tools {
		if coverage := j.covered[name]; coverage.Outcome == "executed" || coverage.Outcome == "safe_rejection" {
			continue
		}
		if _, err := j.callTool(ctx, accessToken, name, map[string]any{"account_id": j.report.AccountID}); err != nil {
			return fmt.Errorf("invoke published tool %s: %w", name, err)
		}
	}
	j.report.ToolCoverage = make([]toolCoverage, 0, len(j.report.Tools))
	for _, name := range j.report.Tools {
		coverage, ok := j.covered[name]
		if !ok || coverage.Outcome == "discovered" {
			return fmt.Errorf("published tool was not exercised: %s", name)
		}
		j.report.ToolCoverage = append(j.report.ToolCoverage, coverage)
	}
	j.pass("tool-by-tool MCP coverage", fmt.Sprintf("all %d published tools reached their authenticated Account handler; applicable workflows succeeded and the remainder failed safely on incomplete synthetic input", len(j.report.ToolCoverage)))
	return nil
}

func (j *journey) exerciseMCPBoundaries(ctx context.Context, accessToken string) error {
	payload := map[string]any{"jsonrpc": "2.0", "id": 9001, "method": "tools/list", "params": map[string]any{"_meta": map[string]any{
		"io.modelcontextprotocol/protocolVersion":    protocolVersion,
		"io.modelcontextprotocol/clientCapabilities": map[string]any{},
		"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "spyglass-agent-journey-cert", "version": "1"},
	}}}
	status, body, err := j.mcpRequest(ctx, j.report.AccountID, "", "tools/list", "", payload)
	if err != nil || status != http.StatusUnauthorized {
		return fmt.Errorf("MCP missing-token boundary: status=%d body=%s err=%w", status, body, err)
	}
	j.pass("MCP authentication denial", "missing Bearer authority failed closed")

	pool, err := pgxpool.New(ctx, j.config.databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	var otherAccount string
	if err := pool.QueryRow(ctx, `SELECT id FROM accounts WHERE id<>$1 AND state='active' ORDER BY created_at LIMIT 1`, j.report.AccountID).Scan(&otherAccount); err != nil {
		return fmt.Errorf("find isolation control Account: %w", err)
	}
	status, body, err = j.mcpRequest(ctx, otherAccount, accessToken, "tools/list", "", payload)
	if err != nil || status != http.StatusForbidden {
		return fmt.Errorf("MCP Account isolation boundary: status=%d body=%s err=%w", status, body, err)
	}
	j.pass("Account isolation denial", "valid OAuth authority could not enumerate another Account")
	return nil
}

func (j *journey) mcpRequest(ctx context.Context, accountID, accessToken, method, tool string, payload any) (int, []byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, j.config.mcpOrigin+"/mcp/v1/accounts/"+accountID, bytes.NewReader(raw))
	if err != nil {
		return 0, nil, err
	}
	if accessToken != "" {
		request.Header.Set("Authorization", "Bearer "+accessToken)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", protocolVersion)
	request.Header.Set("Mcp-Method", method)
	if tool != "" {
		request.Header.Set("Mcp-Name", tool)
	}
	response, err := j.client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	return response.StatusCode, body, err
}

func (j *journey) jsonRequest(ctx context.Context, method, target string, value any, origin bool) (int, []byte, error) {
	return j.jsonRequestWithHeaders(ctx, method, target, value, origin, nil)
}

func (j *journey) jsonRequestWithHeaders(ctx context.Context, method, target string, value any, origin bool, headers map[string]string) (int, []byte, error) {
	var bodyReader io.Reader
	if value != nil {
		raw, err := json.Marshal(value)
		if err != nil {
			return 0, nil, err
		}
		bodyReader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, bodyReader)
	if err != nil {
		return 0, nil, err
	}
	if value != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	if origin {
		request.Header.Set("Origin", j.config.appOrigin)
	}
	if method != http.MethodGet && method != http.MethodHead && strings.Contains(request.URL.Path, "/api/v1/accounts/") {
		request.Header.Set("Idempotency-Key", ids.RandomGenerator{}.New())
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := j.client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	return response.StatusCode, body, err
}

func (j *journey) pass(name, detail string) {
	j.report.Checks = append(j.report.Checks, check{Name: name, Outcome: "passed", Detail: detail})
}

func (j *journey) writeReport() error {
	raw, err := json.MarshalIndent(j.report, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if j.config.output != "" {
		return os.WriteFile(j.config.output, raw, 0o600)
	}
	_, err = os.Stdout.Write(raw)
	return err
}

func waitForVerificationToken(ctx context.Context, mailpitURL, email string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(20 * time.Second)
	defer timeout.Stop()
	tokenPattern := regexp.MustCompile(`token=([A-Za-z0-9_-]{43})`)
	for {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(mailpitURL, "/")+"/api/v1/messages", nil)
		response, err := client.Do(request)
		if err == nil {
			raw, _ := io.ReadAll(io.LimitReader(response.Body, 4<<20))
			response.Body.Close()
			if bytes.Contains(bytes.ToLower(raw), bytes.ToLower([]byte(email))) {
				var listing struct {
					Messages []struct {
						ID string `json:"ID"`
					} `json:"messages"`
				}
				if json.Unmarshal(raw, &listing) == nil {
					for _, message := range listing.Messages {
						request, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(mailpitURL, "/")+"/api/v1/message/"+url.PathEscape(message.ID), nil)
						response, err := client.Do(request)
						if err != nil {
							continue
						}
						detail, _ := io.ReadAll(io.LimitReader(response.Body, 4<<20))
						response.Body.Close()
						if !bytes.Contains(bytes.ToLower(detail), bytes.ToLower([]byte(email))) {
							continue
						}
						match := tokenPattern.FindSubmatch(detail)
						if len(match) == 2 {
							return string(match[1]), nil
						}
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timeout.C:
			return "", errors.New("timed out waiting for local verification email")
		case <-ticker.C:
		}
	}
}

func registrationCredential(challenge, origin, relyingPartyID string) (map[string]any, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	publicKey, err := webauthncbor.Marshal(webauthncose.EC2PublicKeyData{
		PublicKeyData: webauthncose.PublicKeyData{KeyType: int64(webauthncose.EllipticKey), Algorithm: int64(webauthncose.AlgES256)},
		Curve:         int64(webauthncose.P256), XCoord: privateKey.X.FillBytes(make([]byte, 32)), YCoord: privateKey.Y.FillBytes(make([]byte, 32)),
	})
	if err != nil {
		return nil, err
	}
	credentialHash := sha256.Sum256(append([]byte(challenge), privateKey.D.Bytes()...))
	credentialID := append([]byte(nil), credentialHash[:20]...)
	clientData, _ := json.Marshal(map[string]string{"type": "webauthn.create", "challenge": challenge, "origin": origin})
	rpHash := sha256.Sum256([]byte(relyingPartyID))
	authenticatorData := append([]byte(nil), rpHash[:]...)
	authenticatorData = append(authenticatorData, 0x45, 0, 0, 0, 0)
	authenticatorData = append(authenticatorData, make([]byte, 16)...)
	credentialLength := make([]byte, 2)
	binary.BigEndian.PutUint16(credentialLength, uint16(len(credentialID)))
	authenticatorData = append(authenticatorData, credentialLength...)
	authenticatorData = append(authenticatorData, credentialID...)
	authenticatorData = append(authenticatorData, publicKey...)
	attestation, err := webauthncbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": authenticatorData})
	if err != nil {
		return nil, err
	}
	encodedID := base64.RawURLEncoding.EncodeToString(credentialID)
	return map[string]any{
		"id": encodedID, "rawId": encodedID, "type": "public-key",
		"response": map[string]any{"clientDataJSON": base64.RawURLEncoding.EncodeToString(clientData), "attestationObject": base64.RawURLEncoding.EncodeToString(attestation), "transports": []string{"internal"}},
	}, nil
}

func provisionLocalCommercialFixture(ctx context.Context, databaseURL, accountID string) error {
	if err := requireLocalFixtureURL(databaseURL, []string{"postgres", "postgresql"}, "global-db"); err != nil {
		return fmt.Errorf("commercial fixture database must be local: %w", err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var publicationRaw []byte
	var publishedAt time.Time
	if err := tx.QueryRow(ctx, `SELECT content,published_at FROM catalog_publications WHERE state='published' ORDER BY version DESC LIMIT 1`).Scan(&publicationRaw, &publishedAt); err != nil {
		return err
	}
	var publication catalog.PublishedCatalog
	if err := json.Unmarshal(publicationRaw, &publication); err != nil {
		return err
	}
	publication.PublishedAt = publishedAt.UTC()
	if err := publication.Validate(); err != nil {
		return err
	}
	var account ids.AccountID
	var currentVersion uint64
	if err := tx.QueryRow(ctx, `SELECT id,entitlement_version FROM accounts WHERE id=$1 FOR UPDATE`, accountID).Scan(&account, &currentVersion); err != nil {
		return err
	}
	definitions := make(map[catalog.PackageCode]catalog.FeaturePackage, len(publication.Packages))
	for _, definition := range publication.Packages {
		definitions[definition.Code] = definition
	}
	var plan catalog.Plan
	for _, candidate := range publication.Plans {
		if candidate.Code == "team" {
			plan = candidate
		}
	}
	if plan.Code == "" {
		return errors.New("published Catalog has no team plan")
	}
	now := time.Now().UTC()
	grants := make([]entitlements.Grant, 0, len(plan.Packages))
	for code, mode := range plan.Packages {
		definition := definitions[code]
		limits := make(map[catalog.LimitCode]int64, len(definition.DefaultLimits))
		for limit, value := range definition.DefaultLimits {
			limits[limit] = value
		}
		grant := entitlements.Grant{ID: ids.GrantID(ids.RandomGenerator{}.New()), AccountID: account, PackageCode: code, PackageVersion: definition.Version, Mode: mode, Source: entitlements.SourceSubscription, SourceReference: "local-agent-certification", Limits: limits, StartsAt: now, Priority: 100, Reason: "deterministic local external-agent certification"}
		limitsRaw, _ := json.Marshal(limits)
		if _, err := tx.Exec(ctx, `INSERT INTO entitlement_grants (id,account_id,package_code,package_version,mode,source,source_reference,limits,starts_at,priority,reason,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$9)`, grant.ID, grant.AccountID, grant.PackageCode, grant.PackageVersion, grant.Mode, grant.Source, grant.SourceReference, limitsRaw, grant.StartsAt, grant.Priority, grant.Reason); err != nil {
			return err
		}
		grants = append(grants, grant)
	}
	snapshot, err := entitlements.Evaluate(account, currentVersion+1, publication, grants, now)
	if err != nil {
		return err
	}
	packages, _ := json.Marshal(snapshot.Packages)
	sourceHash := sha256.Sum256(packages)
	if _, err := tx.Exec(ctx, `INSERT INTO entitlement_snapshots (account_id,version,catalog_version,evaluated_at,source_hash,effective_packages) VALUES ($1,$2,$3,$4,$5,$6)`, account, snapshot.Version, snapshot.CatalogVersion, snapshot.EvaluatedAt, sourceHash[:], packages); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE accounts SET account_type='paid',entitlement_version=$2,last_catalog_reconciled_version=$3 WHERE id=$1`, account, snapshot.Version, publication.Version); err != nil {
		return err
	}
	definition := publication.AITokenRenewalGrant
	if definition == nil {
		return errors.New("published Catalog has no AI Token renewal grant")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ai_token_grants (id,account_id,origin,definition_code,catalog_version,source_reference,quantity,available,reserved,consumed,state,expires_at,created_at) VALUES ($1,$2,'included',$3,$4,$5,$6,$6,0,0,'active',$7,$8)`, ids.RandomGenerator{}.New(), account, definition.Code, publication.Version, "local-agent-certification-period", definition.Quantity, now.Add(31*24*time.Hour), now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func exhaustLocalAITokens(ctx context.Context, databaseURL, accountID string) error {
	if err := requireLocalFixtureURL(databaseURL, []string{"postgres", "postgresql"}, "global-db"); err != nil {
		return fmt.Errorf("AI Token fixture database must be local: %w", err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	command, err := pool.Exec(ctx, `UPDATE ai_token_grants SET available=0 WHERE account_id=$1 AND state='active'`, accountID)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return errors.New("Account had no active AI Token grant to exhaust")
	}
	return nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func validateLocalAgentJourneyTargets(c config) error {
	host, _, err := net.SplitHostPort(c.edgeAddress)
	if err != nil || !localFixtureHost(host, "edge") {
		return errors.New("agent journey edge address must resolve to the local edge fixture")
	}
	if err := requireLocalFixtureURL(c.mailpitURL, []string{"http"}, "mailpit"); err != nil {
		return fmt.Errorf("agent journey notification endpoint must be local: %w", err)
	}
	if err := requireLocalFixtureURL(c.databaseURL, []string{"postgres", "postgresql"}, "global-db"); err != nil {
		return fmt.Errorf("agent journey database must be local: %w", err)
	}
	if c.commercialOnly || c.stripeFixtureURL != "" {
		if err := requireLocalFixtureURL(c.stripeFixtureURL, []string{"http"}, "stripe-fixture"); err != nil {
			return fmt.Errorf("commercial journey Stripe endpoint must be local: %w", err)
		}
		if len(c.stripeFixtureToken) < 24 {
			return errors.New("commercial journey Stripe completion token is missing")
		}
	}
	return nil
}

func requireLocalFixtureURL(raw string, schemes []string, hosts ...string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return errors.New("URL is invalid")
	}
	if !slices.Contains(schemes, parsed.Scheme) || !localFixtureHost(parsed.Hostname(), hosts...) {
		return errors.New("URL does not name an allowed local fixture")
	}
	return nil
}

func localFixtureHost(host string, names ...string) bool {
	if host == "localhost" || slices.Contains(names, host) {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
