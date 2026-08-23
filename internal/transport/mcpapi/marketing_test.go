package mcpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	marketingapp "github.com/tinfoyle/spyglass-engine/internal/application/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	marketingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

const (
	mcpMarketingCampaign   = "a1000000-0000-4000-8000-000000000001"
	mcpMarketingAsset      = "a2000000-0000-4000-8000-000000000002"
	mcpMarketingRevision   = "a3000000-0000-4000-8000-000000000003"
	mcpMarketingRelease    = "a4000000-0000-4000-8000-000000000004"
	mcpMarketingRun        = "a5000000-0000-4000-8000-000000000005"
	mcpMarketingInvocation = "a6000000-0000-4000-8000-000000000006"
)

type marketingMCPStub struct {
	now            time.Time
	campaignCreate marketingapp.CreateCampaignCommand
	assetUpload    marketingapp.UploadAssetRevisionCommand
	assetBody      []byte
	campaignQuery  marketingapp.CampaignListQuery
	approveError   error
}

func (stub *marketingMCPStub) campaign() marketingdomain.Campaign {
	return marketingdomain.Campaign{ID: mcpMarketingCampaign, AccountID: mcpAccount, Name: "Launch", Objective: "Governed release", Audience: "Operators", Channels: []marketingdomain.Channel{marketingdomain.ChannelWeb}, State: marketingdomain.CampaignDraft, Version: 1, CreatedBy: marketingdomain.Actor{Kind: marketingdomain.ActorUser, ID: mcpUser}, Provenance: marketingdomain.Provenance{Origin: marketingdomain.OriginHuman}, CreatedAt: stub.now, UpdatedAt: stub.now}
}
func (stub *marketingMCPStub) release() marketingdomain.ReleasePlan {
	return marketingdomain.ReleasePlan{ID: mcpMarketingRelease, AccountID: mcpAccount, CampaignID: mcpMarketingCampaign, CampaignVersion: 1, Name: "Release", Channels: []marketingdomain.Channel{marketingdomain.ChannelWeb}, AssetRevisionIDs: []ids.MarketingAssetRevisionID{mcpMarketingRevision}, State: marketingdomain.ReleaseDraft, Version: 1, CreatedBy: marketingdomain.Actor{Kind: marketingdomain.ActorUser, ID: mcpUser}, Provenance: marketingdomain.Provenance{Origin: marketingdomain.OriginHuman}, CreatedAt: stub.now, UpdatedAt: stub.now}
}
func (stub *marketingMCPStub) asset() marketingdomain.AssetRevision {
	digest := [32]byte{}
	for index := range digest {
		digest[index] = 0x11
	}
	return marketingdomain.AssetRevision{ID: mcpMarketingRevision, AccountID: mcpAccount, CampaignID: mcpMarketingCampaign, AssetID: mcpMarketingAsset, Revision: 1, Kind: marketingdomain.AssetImage, Title: "Hero", MediaType: "image/png", ContentReference: "objects/hero", ContentSHA256: digest, ContentBytes: 100, AlternativeText: "Hero", CreatedBy: marketingdomain.Actor{Kind: marketingdomain.ActorUser, ID: mcpUser}, Provenance: marketingdomain.Provenance{Origin: marketingdomain.OriginHuman}, CreatedAt: stub.now}
}
func (stub *marketingMCPStub) CreateCampaign(ctx context.Context, command marketingapp.CreateCampaignCommand) (marketingdomain.Campaign, bool, error) {
	claims, ok := routecontext.FromContext(ctx)
	if !ok || claims.Authority.PackageAccess.Code != string(catalog.PackageMarketing) {
		return marketingdomain.Campaign{}, false, errors.New("missing Marketing claims")
	}
	stub.campaignCreate = command
	return stub.campaign(), true, nil
}
func (stub *marketingMCPStub) GetCampaign(context.Context, access.Actor, ids.AccountID, ids.MarketingCampaignID) (marketingdomain.Campaign, error) {
	return stub.campaign(), nil
}
func (stub *marketingMCPStub) ListCampaigns(_ context.Context, _ access.Actor, _ ids.AccountID, query marketingapp.CampaignListQuery) (marketingapp.CampaignPage, error) {
	stub.campaignQuery = query
	value := stub.campaign()
	return marketingapp.CampaignPage{Items: []marketingdomain.Campaign{value}, NextCursor: &marketingapp.CampaignCursor{UpdatedAt: value.UpdatedAt, ID: value.ID}}, nil
}
func (stub *marketingMCPStub) ReviseCampaign(context.Context, marketingapp.ReviseCampaignCommand) (marketingdomain.Campaign, error) {
	return stub.campaign(), nil
}
func (stub *marketingMCPStub) UploadAssetRevision(_ context.Context, command marketingapp.UploadAssetRevisionCommand) (marketingdomain.AssetRevision, bool, error) {
	body, err := io.ReadAll(command.Body)
	if err != nil {
		return marketingdomain.AssetRevision{}, false, err
	}
	stub.assetUpload, stub.assetBody = command, body
	value := stub.asset()
	value.ContentSHA256, value.ContentBytes = sha256.Sum256(body), uint64(len(body))
	value.ContentReference, _ = marketingdomain.ContentReferenceForObjectVersion("mcp-test-version-1")
	return value, true, nil
}
func (stub *marketingMCPStub) ListAssetRevisions(context.Context, access.Actor, ids.AccountID, marketingapp.AssetRevisionListQuery) (marketingapp.AssetRevisionPage, error) {
	return marketingapp.AssetRevisionPage{Items: []marketingdomain.AssetRevision{stub.asset()}}, nil
}
func (stub *marketingMCPStub) CreateRelease(context.Context, marketingapp.CreateReleaseCommand) (marketingdomain.ReleasePlan, bool, error) {
	return stub.release(), true, nil
}
func (stub *marketingMCPStub) GetRelease(context.Context, access.Actor, ids.AccountID, ids.MarketingReleaseID) (marketingdomain.ReleasePlan, error) {
	return stub.release(), nil
}
func (stub *marketingMCPStub) ListReleases(context.Context, access.Actor, ids.AccountID, marketingapp.ReleaseListQuery) (marketingapp.ReleasePage, error) {
	return marketingapp.ReleasePage{Items: []marketingdomain.ReleasePlan{stub.release()}}, nil
}
func (stub *marketingMCPStub) SubmitRelease(context.Context, marketingapp.ReleaseTransitionCommand) (marketingdomain.ReleasePlan, error) {
	return stub.release(), nil
}
func (stub *marketingMCPStub) ApproveRelease(context.Context, marketingapp.ReleaseTransitionCommand) (marketingdomain.ReleasePlan, error) {
	if stub.approveError != nil {
		return marketingdomain.ReleasePlan{}, stub.approveError
	}
	return stub.release(), nil
}
func (stub *marketingMCPStub) CancelRelease(context.Context, marketingapp.ReleaseTransitionCommand) (marketingdomain.ReleasePlan, error) {
	return stub.release(), nil
}
func (stub *marketingMCPStub) ActivateCampaign(context.Context, marketingapp.CampaignTransitionCommand) (marketingdomain.Campaign, error) {
	return stub.campaign(), nil
}
func (stub *marketingMCPStub) PauseCampaign(context.Context, marketingapp.CampaignTransitionCommand) (marketingdomain.Campaign, error) {
	return stub.campaign(), nil
}
func (stub *marketingMCPStub) CompleteCampaign(context.Context, marketingapp.CampaignTransitionCommand) (marketingdomain.Campaign, error) {
	return stub.campaign(), nil
}
func (stub *marketingMCPStub) ArchiveCampaign(context.Context, marketingapp.CampaignTransitionCommand) (marketingdomain.Campaign, error) {
	return stub.campaign(), nil
}

func TestMarketingMCPPublishesCompleteTypedSurface(t *testing.T) {
	session, cleanup := connectMarketingMCP(t, &testAuthority{}, &marketingMCPStub{now: time.Date(2026, 8, 23, 5, 0, 0, 0, time.UTC)})
	defer cleanup()
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 31 {
		t.Fatalf("tool count=%d want=31", len(result.Tools))
	}
	names := make([]string, 0, len(result.Tools))
	count := 0
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
		if _, ok := ToolRequirement(tool.Name); !ok {
			t.Fatalf("tool %q has no global requirement", tool.Name)
		}
		if strings.HasPrefix(tool.Name, "spyglass_marketing_") {
			count++
			if tool.InputSchema == nil || tool.OutputSchema == nil || tool.Annotations == nil {
				t.Fatalf("incomplete Marketing tool: %+v", tool)
			}
			if tool.Name == "spyglass_marketing_asset_revision_create_draft" {
				schema, _ := json.Marshal(tool.InputSchema)
				if !bytes.Contains(schema, []byte(`"content_base64"`)) || bytes.Contains(schema, []byte(`"content_reference"`)) || bytes.Contains(schema, []byte(`"content_sha256"`)) || bytes.Contains(schema, []byte(`"content_bytes"`)) {
					t.Fatalf("unsafe Marketing asset input schema: %s", schema)
				}
			}
		}
	}
	if count != 16 || !slices.IsSorted(names) {
		t.Fatalf("marketing tools=%d sorted=%v names=%v", count, slices.IsSorted(names), names)
	}
}

func TestMarketingAssetBodyRequiresCanonicalBoundedBase64(t *testing.T) {
	want := []byte("launch copy")
	body, err := marketingAssetBody(base64.StdEncoding.EncodeToString(want))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(body)
	if !bytes.Equal(got, want) {
		t.Fatalf("body=%q want=%q", got, want)
	}
	for _, invalid := range []string{"", "not base64", base64.RawStdEncoding.EncodeToString(want), base64.StdEncoding.EncodeToString(make([]byte, maximumMCPMarketingAssetBytes+1))} {
		if _, err := marketingAssetBody(invalid); err == nil {
			t.Fatalf("accepted invalid body length=%d", len(invalid))
		}
	}
}

func TestMarketingMCPUsesCanonicalServiceCursorAndDigest(t *testing.T) {
	stub := &marketingMCPStub{now: time.Date(2026, 8, 23, 5, 0, 0, 0, time.UTC)}
	authority := &testAuthority{}
	session, cleanup := connectMarketingMCP(t, authority, stub)
	defer cleanup()
	created, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_marketing_campaign_create_draft", Arguments: map[string]any{"account_id": mcpAccount, "operation_id": mcpOperation, "name": "Launch", "objective": "Governed release", "audience": "Operators", "channels": []any{"web"}}})
	if err != nil || created.IsError || stub.campaignCreate.Provenance.Origin != marketingdomain.OriginHuman || stub.campaignCreate.RequestID != mcpOperation {
		t.Fatalf("create err=%v result=%+v command=%+v", err, created, stub.campaignCreate)
	}
	body := []byte("synthetic image bytes")
	asset, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_marketing_asset_revision_create_draft", Arguments: map[string]any{"account_id": mcpAccount, "operation_id": mcpOperation, "campaign_id": mcpMarketingCampaign, "asset_id": mcpMarketingAsset, "kind": "image", "title": "Hero", "media_type": "image/png", "content_base64": base64.StdEncoding.EncodeToString(body), "alternative_text": "Hero"}})
	wantDigest := sha256.Sum256(body)
	if err != nil || asset.IsError || !bytes.Equal(stub.assetBody, body) || stub.assetUpload.Provenance.Origin != marketingdomain.OriginHuman || !strings.Contains(asset.Content[0].(*mcp.TextContent).Text, `"content_sha256":"`+fmt.Sprintf("%x", wantDigest)) {
		t.Fatalf("asset err=%v result=%+v command=%+v body=%q", err, asset, stub.assetUpload, stub.assetBody)
	}
	listed, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_marketing_campaign_list", Arguments: map[string]any{"account_id": mcpAccount, "limit": 25}})
	if err != nil || listed.IsError || stub.campaignQuery.Limit != 25 {
		t.Fatalf("list err=%v result=%+v query=%+v", err, listed, stub.campaignQuery)
	}
	raw := listed.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(raw, `"next_cursor":"`) || strings.Contains(raw, `"date":`) {
		t.Fatalf("cursor is not opaque: %s", raw)
	}
	if len(authority.requirements) != 3 || authority.requirements[0].Package != catalog.PackageMarketing || !authority.requirements[0].Mutation || authority.requirements[2].Mutation {
		t.Fatalf("requirements=%+v", authority.requirements)
	}
}

func TestMarketingMCPAgentDraftProvenanceAndGovernanceFailures(t *testing.T) {
	stub := &marketingMCPStub{now: time.Date(2026, 8, 23, 5, 0, 0, 0, time.UTC), approveError: fmt.Errorf("database secret: %w", marketingapp.ErrConflict)}
	session, cleanup := connectMarketingMCP(t, &marketingAgentAuthority{}, stub)
	defer cleanup()
	draft, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_marketing_campaign_create_draft", Arguments: map[string]any{"account_id": mcpAccount, "operation_id": mcpOperation, "run_id": mcpMarketingRun, "name": "Launch", "objective": "Governed release", "audience": "Operators", "channels": []any{"web"}}})
	if err != nil || draft.IsError || stub.campaignCreate.Provenance.Origin != marketingdomain.OriginAgent || stub.campaignCreate.Provenance.RunID != mcpMarketingRun || stub.campaignCreate.Provenance.InvocationID != mcpMarketingInvocation {
		t.Fatalf("draft err=%v result=%+v command=%+v", err, draft, stub.campaignCreate)
	}
	missing, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_marketing_release_approve", Arguments: map[string]any{"account_id": mcpAccount, "operation_id": mcpOperation, "release_id": mcpMarketingRelease, "expected_version": 0, "approval_id": mcpOperation}})
	if err != nil || !missing.IsError || !strings.Contains(missing.Content[0].(*mcp.TextContent).Text, "marketing_version_required") {
		t.Fatalf("missing err=%v result=%+v", err, missing)
	}
	failed, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_marketing_release_approve", Arguments: map[string]any{"account_id": mcpAccount, "operation_id": mcpOperation, "release_id": mcpMarketingRelease, "expected_version": 1, "approval_id": mcpOperation}})
	if err != nil || !failed.IsError {
		t.Fatalf("failed err=%v result=%+v", err, failed)
	}
	raw := failed.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(raw, "marketing_version_conflict") || strings.Contains(raw, "database") || strings.Contains(raw, "secret") {
		t.Fatalf("unsafe error=%q", raw)
	}
}

type marketingAgentAuthority struct{}

func (*marketingAgentAuthority) Authenticate(_ context.Context, token string) (access.Actor, error) {
	if token != "reviewed-token" {
		return access.Actor{}, errors.New("invalid")
	}
	return access.Actor{WorkloadID: "runner-invocation:" + mcpMarketingInvocation}, nil
}
func (*marketingAgentAuthority) Authorize(_ context.Context, actor access.Actor, accountID ids.AccountID, requirement access.Requirement) (routecontext.Claims, error) {
	return routecontext.Claims{Authority: routecontext.Authority{AccountID: accountID, ActorKind: "workload", ActorID: actor.WorkloadID, Role: "", CellID: "cell-us-east-01", PlacementGeneration: 1, EntitlementVersion: 1, PackageAccess: &routecontext.PackageAccess{Code: string(requirement.Package), Version: 1, Mode: "enabled"}}}, nil
}

func connectMarketingMCP(t *testing.T, authority Authority, marketing MarketingService) (*mcp.ClientSession, func()) {
	t.Helper()
	server, err := New(authority, &attentionStub{}, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{Version: "0.3.0-test", MaxBody: DefaultMaxBody, TrustedOrigins: []string{"https://trusted.example"}, ResourceMetadataURL: "https://auth.infiniteocean.net/.well-known/oauth-protected-resource"}, WithMarketing(marketing))
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	transport := &mcp.StreamableClientTransport{Endpoint: httpServer.URL, DisableStandaloneSSE: true, HTTPClient: &http.Client{Transport: bearerRoundTripper{base: http.DefaultTransport}}}
	client := mcp.NewClient(&mcp.Implementation{Name: "spyglass-marketing-test", Version: "1.0.0"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}})
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		httpServer.Close()
		t.Fatal(err)
	}
	return session, func() { _ = session.Close(); httpServer.Close() }
}

var _ MarketingService = (*marketingMCPStub)(nil)
