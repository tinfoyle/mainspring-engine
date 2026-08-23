package mcpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	marketingapp "github.com/tinfoyle/spyglass-engine/internal/application/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	marketingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type marketingCampaignTargetInput struct {
	AccountID  ids.AccountID           `json:"account_id"`
	CampaignID ids.MarketingCampaignID `json:"campaign_id"`
}

type marketingCampaignListInput struct {
	AccountID ids.AccountID                 `json:"account_id"`
	State     marketingdomain.CampaignState `json:"state,omitempty"`
	Cursor    string                        `json:"cursor,omitempty"`
	Limit     int                           `json:"limit,omitempty"`
}

type marketingCampaignCreateInput struct {
	AccountID   ids.AccountID             `json:"account_id"`
	OperationID string                    `json:"operation_id"`
	RunID       ids.RunID                 `json:"run_id,omitempty"`
	Name        string                    `json:"name"`
	Objective   string                    `json:"objective"`
	Audience    string                    `json:"audience"`
	Channels    []marketingdomain.Channel `json:"channels"`
}

type marketingCampaignReviseInput struct {
	AccountID       ids.AccountID             `json:"account_id"`
	OperationID     string                    `json:"operation_id"`
	CampaignID      ids.MarketingCampaignID   `json:"campaign_id"`
	ExpectedVersion uint64                    `json:"expected_version"`
	Name            string                    `json:"name"`
	Objective       string                    `json:"objective"`
	Audience        string                    `json:"audience"`
	Channels        []marketingdomain.Channel `json:"channels"`
}

type marketingCampaignTransitionInput struct {
	AccountID       ids.AccountID           `json:"account_id"`
	OperationID     string                  `json:"operation_id"`
	CampaignID      ids.MarketingCampaignID `json:"campaign_id"`
	ExpectedVersion uint64                  `json:"expected_version"`
}

type marketingCampaignActivationInput struct {
	AccountID       ids.AccountID           `json:"account_id"`
	OperationID     string                  `json:"operation_id"`
	CampaignID      ids.MarketingCampaignID `json:"campaign_id"`
	ReleaseID       ids.MarketingReleaseID  `json:"release_id"`
	ExpectedVersion uint64                  `json:"expected_version"`
}

type marketingAssetRevisionListInput struct {
	AccountID  ids.AccountID           `json:"account_id"`
	CampaignID ids.MarketingCampaignID `json:"campaign_id"`
	AssetID    ids.MarketingAssetID    `json:"asset_id,omitempty"`
	Cursor     string                  `json:"cursor,omitempty"`
	Limit      int                     `json:"limit,omitempty"`
}

type marketingAssetRevisionCreateInput struct {
	AccountID        ids.AccountID             `json:"account_id"`
	OperationID      string                    `json:"operation_id"`
	RunID            ids.RunID                 `json:"run_id,omitempty"`
	CampaignID       ids.MarketingCampaignID   `json:"campaign_id"`
	AssetID          ids.MarketingAssetID      `json:"asset_id"`
	Kind             marketingdomain.AssetKind `json:"kind"`
	Title            string                    `json:"title"`
	MediaType        string                    `json:"media_type"`
	ContentReference string                    `json:"content_reference"`
	ContentSHA256    string                    `json:"content_sha256"`
	ContentBytes     uint64                    `json:"content_bytes"`
	AlternativeText  string                    `json:"alternative_text,omitempty"`
}

type marketingReleaseTargetInput struct {
	AccountID ids.AccountID          `json:"account_id"`
	ReleaseID ids.MarketingReleaseID `json:"release_id"`
}

type marketingReleaseListInput struct {
	AccountID  ids.AccountID           `json:"account_id"`
	CampaignID ids.MarketingCampaignID `json:"campaign_id"`
	Cursor     string                  `json:"cursor,omitempty"`
	Limit      int                     `json:"limit,omitempty"`
}

type marketingReleaseCreateInput struct {
	AccountID        ids.AccountID                  `json:"account_id"`
	OperationID      string                         `json:"operation_id"`
	RunID            ids.RunID                      `json:"run_id,omitempty"`
	CampaignID       ids.MarketingCampaignID        `json:"campaign_id"`
	CampaignVersion  uint64                         `json:"campaign_version"`
	Name             string                         `json:"name"`
	Channels         []marketingdomain.Channel      `json:"channels"`
	AssetRevisionIDs []ids.MarketingAssetRevisionID `json:"asset_revision_ids"`
}

type marketingReleaseSubmitInput struct {
	AccountID       ids.AccountID          `json:"account_id"`
	OperationID     string                 `json:"operation_id"`
	ReleaseID       ids.MarketingReleaseID `json:"release_id"`
	ExpectedVersion uint64                 `json:"expected_version"`
	CampaignVersion uint64                 `json:"campaign_version"`
}

type marketingReleaseApprovalInput struct {
	AccountID       ids.AccountID               `json:"account_id"`
	OperationID     string                      `json:"operation_id"`
	ReleaseID       ids.MarketingReleaseID      `json:"release_id"`
	ExpectedVersion uint64                      `json:"expected_version"`
	ApprovalID      ids.ConsequentialApprovalID `json:"approval_id"`
}

type marketingReleaseTransitionInput struct {
	AccountID       ids.AccountID          `json:"account_id"`
	OperationID     string                 `json:"operation_id"`
	ReleaseID       ids.MarketingReleaseID `json:"release_id"`
	ExpectedVersion uint64                 `json:"expected_version"`
}

type marketingCampaignPageOutput struct {
	Items      []marketingdomain.Campaign `json:"items"`
	NextCursor string                     `json:"next_cursor,omitempty"`
}

type marketingAssetRevisionOutput struct {
	ID               ids.MarketingAssetRevisionID `json:"id"`
	AccountID        ids.AccountID                `json:"account_id"`
	CampaignID       ids.MarketingCampaignID      `json:"campaign_id"`
	AssetID          ids.MarketingAssetID         `json:"asset_id"`
	Revision         uint64                       `json:"revision"`
	Kind             marketingdomain.AssetKind    `json:"kind"`
	Title            string                       `json:"title"`
	MediaType        string                       `json:"media_type"`
	ContentReference string                       `json:"content_reference"`
	ContentSHA256    string                       `json:"content_sha256"`
	ContentBytes     uint64                       `json:"content_bytes"`
	AlternativeText  string                       `json:"alternative_text,omitempty"`
	CreatedBy        marketingdomain.Actor        `json:"created_by"`
	Provenance       marketingdomain.Provenance   `json:"provenance"`
	CreatedAt        time.Time                    `json:"created_at"`
}

type marketingAssetRevisionPageOutput struct {
	Items      []marketingAssetRevisionOutput `json:"items"`
	NextCursor string                         `json:"next_cursor,omitempty"`
}

type marketingReleasePageOutput struct {
	Items      []marketingdomain.ReleasePlan `json:"items"`
	NextCursor string                        `json:"next_cursor,omitempty"`
}

func (s *Server) registerMarketing(server *mcp.Server, actor access.Actor) {
	read := access.Requirement{Package: catalog.PackageMarketing}
	mutation := access.Requirement{Package: catalog.PackageMarketing, Mutation: true}

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_marketing_campaign_list", Title: "List Marketing campaigns", Description: "List a bounded stable page of Account-owned campaign intent.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input marketingCampaignListInput) (*mcp.CallToolResult, marketingCampaignPageOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, read)
		if err != nil {
			return nil, marketingCampaignPageOutput{}, err
		}
		limit, err := marketingLimit(input.Limit)
		if err != nil {
			return nil, marketingCampaignPageOutput{}, err
		}
		query := marketingapp.CampaignListQuery{State: input.State, Limit: limit}
		if input.Cursor != "" {
			cursor, err := decodeMarketingMCPCursor(input.Cursor, "campaign")
			if err != nil {
				return nil, marketingCampaignPageOutput{}, err
			}
			query.After = &marketingapp.CampaignCursor{UpdatedAt: cursor.Date, ID: ids.MarketingCampaignID(cursor.ID)}
		}
		page, err := s.marketing.ListCampaigns(ctx, actor, input.AccountID, query)
		if err != nil {
			return nil, marketingCampaignPageOutput{}, marketingError(err)
		}
		output := marketingCampaignPageOutput{Items: page.Items}
		if page.NextCursor != nil {
			output.NextCursor = encodeMarketingMCPCursor(marketingMCPCursor{Version: 1, Kind: "campaign", Date: page.NextCursor.UpdatedAt, ID: string(page.NextCursor.ID)})
		}
		return nil, output, nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_marketing_campaign_get", Title: "Get Marketing campaign", Description: "Get current versioned campaign intent and active release identity.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input marketingCampaignTargetInput) (*mcp.CallToolResult, marketingdomain.Campaign, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, read)
		if err != nil {
			return nil, marketingdomain.Campaign{}, err
		}
		value, err := s.marketing.GetCampaign(ctx, actor, input.AccountID, input.CampaignID)
		return nil, value, marketingError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_marketing_campaign_create_draft", Title: "Create Marketing campaign draft", Description: "Create campaign intent. Agent callers must supply the exact Run and can create drafts only.", Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input marketingCampaignCreateInput) (*mcp.CallToolResult, marketingdomain.Campaign, error) {
		ctx, op, provenance, err := s.marketingDraftContext(ctx, actor, input.AccountID, input.OperationID, input.RunID, mutation)
		if err != nil {
			return nil, marketingdomain.Campaign{}, err
		}
		value, _, err := s.marketing.CreateCampaign(ctx, marketingapp.CreateCampaignCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, Name: input.Name, Objective: input.Objective, Audience: input.Audience, Channels: input.Channels, Provenance: provenance})
		return nil, value, marketingError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_marketing_campaign_revise", Title: "Revise Marketing campaign", Description: "Revise draft or paused campaign intent using the current version. Human caller only.", Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input marketingCampaignReviseInput) (*mcp.CallToolResult, marketingdomain.Campaign, error) {
		ctx, op, err := s.marketingMutationContext(ctx, actor, input.AccountID, input.OperationID, mutation, input.ExpectedVersion)
		if err != nil {
			return nil, marketingdomain.Campaign{}, err
		}
		value, err := s.marketing.ReviseCampaign(ctx, marketingapp.ReviseCampaignCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, CampaignID: input.CampaignID, ExpectedVersion: input.ExpectedVersion, Name: input.Name, Objective: input.Objective, Audience: input.Audience, Channels: input.Channels})
		return nil, value, marketingError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_marketing_campaign_archive", Title: "Archive Marketing campaign", Description: "Archive a non-active campaign. Owner or Administrator human only.", Annotations: toolAnnotations(false, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, input marketingCampaignTransitionInput) (*mcp.CallToolResult, marketingdomain.Campaign, error) {
		return s.marketingCampaignTransition(ctx, actor, input, mutation, "archive")
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_marketing_asset_revision_list", Title: "List Marketing asset revisions", Description: "List immutable content-addressed revision metadata without content bytes.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input marketingAssetRevisionListInput) (*mcp.CallToolResult, marketingAssetRevisionPageOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, read)
		if err != nil {
			return nil, marketingAssetRevisionPageOutput{}, err
		}
		limit, err := marketingLimit(input.Limit)
		if err != nil {
			return nil, marketingAssetRevisionPageOutput{}, err
		}
		query := marketingapp.AssetRevisionListQuery{CampaignID: input.CampaignID, AssetID: input.AssetID, Limit: limit}
		if input.Cursor != "" {
			cursor, err := decodeMarketingMCPCursor(input.Cursor, "asset_revision")
			if err != nil {
				return nil, marketingAssetRevisionPageOutput{}, err
			}
			query.After = &marketingapp.AssetRevisionCursor{AssetID: ids.MarketingAssetID(cursor.AssetID), Revision: cursor.Revision}
		}
		page, err := s.marketing.ListAssetRevisions(ctx, actor, input.AccountID, query)
		if err != nil {
			return nil, marketingAssetRevisionPageOutput{}, marketingError(err)
		}
		output := marketingAssetRevisionPageOutput{Items: make([]marketingAssetRevisionOutput, len(page.Items))}
		for index, item := range page.Items {
			output.Items[index] = marketingAssetOutput(item)
		}
		if page.NextCursor != nil {
			output.NextCursor = encodeMarketingMCPCursor(marketingMCPCursor{Version: 1, Kind: "asset_revision", AssetID: string(page.NextCursor.AssetID), Revision: page.NextCursor.Revision})
		}
		return nil, output, nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_marketing_asset_revision_create_draft", Title: "Create Marketing asset revision draft", Description: "Append immutable asset metadata. Agent callers must supply the exact Run.", Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input marketingAssetRevisionCreateInput) (*mcp.CallToolResult, marketingAssetRevisionOutput, error) {
		ctx, op, provenance, err := s.marketingDraftContext(ctx, actor, input.AccountID, input.OperationID, input.RunID, mutation)
		if err != nil {
			return nil, marketingAssetRevisionOutput{}, err
		}
		digest, err := marketingDigest(input.ContentSHA256)
		if err != nil {
			return nil, marketingAssetRevisionOutput{}, err
		}
		value, _, err := s.marketing.CreateAssetRevision(ctx, marketingapp.CreateAssetRevisionCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, CampaignID: input.CampaignID, AssetID: input.AssetID, Kind: input.Kind, Title: input.Title, MediaType: input.MediaType, ContentReference: input.ContentReference, ContentSHA256: digest, ContentBytes: input.ContentBytes, AlternativeText: input.AlternativeText, Provenance: provenance})
		return nil, marketingAssetOutput(value), marketingError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_marketing_release_list", Title: "List Marketing releases", Description: "List exact governed release snapshots for one campaign.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input marketingReleaseListInput) (*mcp.CallToolResult, marketingReleasePageOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, read)
		if err != nil {
			return nil, marketingReleasePageOutput{}, err
		}
		limit, err := marketingLimit(input.Limit)
		if err != nil {
			return nil, marketingReleasePageOutput{}, err
		}
		query := marketingapp.ReleaseListQuery{CampaignID: input.CampaignID, Limit: limit}
		if input.Cursor != "" {
			cursor, err := decodeMarketingMCPCursor(input.Cursor, "release")
			if err != nil {
				return nil, marketingReleasePageOutput{}, err
			}
			query.After = &marketingapp.ReleaseCursor{CreatedAt: cursor.Date, ID: ids.MarketingReleaseID(cursor.ID)}
		}
		page, err := s.marketing.ListReleases(ctx, actor, input.AccountID, query)
		if err != nil {
			return nil, marketingReleasePageOutput{}, marketingError(err)
		}
		output := marketingReleasePageOutput{Items: page.Items}
		if page.NextCursor != nil {
			output.NextCursor = encodeMarketingMCPCursor(marketingMCPCursor{Version: 1, Kind: "release", Date: page.NextCursor.CreatedAt, ID: string(page.NextCursor.ID)})
		}
		return nil, output, nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_marketing_release_get", Title: "Get Marketing release", Description: "Get one exact versioned release snapshot and approval binding.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input marketingReleaseTargetInput) (*mcp.CallToolResult, marketingdomain.ReleasePlan, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, read)
		if err != nil {
			return nil, marketingdomain.ReleasePlan{}, err
		}
		value, err := s.marketing.GetRelease(ctx, actor, input.AccountID, input.ReleaseID)
		return nil, value, marketingError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_marketing_release_create_draft", Title: "Create Marketing release draft", Description: "Freeze one campaign version and exact asset revisions. Agent callers must supply the exact Run.", Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input marketingReleaseCreateInput) (*mcp.CallToolResult, marketingdomain.ReleasePlan, error) {
		ctx, op, provenance, err := s.marketingDraftContext(ctx, actor, input.AccountID, input.OperationID, input.RunID, mutation)
		if err != nil {
			return nil, marketingdomain.ReleasePlan{}, err
		}
		value, _, err := s.marketing.CreateRelease(ctx, marketingapp.CreateReleaseCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, CampaignID: input.CampaignID, CampaignVersion: input.CampaignVersion, Name: input.Name, Channels: input.Channels, AssetRevisionIDs: input.AssetRevisionIDs, Provenance: provenance})
		return nil, value, marketingError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_marketing_release_submit", Title: "Submit Marketing release", Description: "Submit a current draft release for human governance. Human caller only.", Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input marketingReleaseSubmitInput) (*mcp.CallToolResult, marketingdomain.ReleasePlan, error) {
		ctx, op, err := s.marketingMutationContext(ctx, actor, input.AccountID, input.OperationID, mutation, input.ExpectedVersion)
		if err != nil {
			return nil, marketingdomain.ReleasePlan{}, err
		}
		value, err := s.marketing.SubmitRelease(ctx, marketingapp.ReleaseTransitionCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, ReleaseID: input.ReleaseID, ExpectedVersion: input.ExpectedVersion, CampaignVersion: input.CampaignVersion})
		return nil, value, marketingError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_marketing_release_approve", Title: "Approve Marketing release", Description: "Bind an exact approved Attention decision. Owner or Administrator human only.", Annotations: toolAnnotations(false, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, input marketingReleaseApprovalInput) (*mcp.CallToolResult, marketingdomain.ReleasePlan, error) {
		ctx, op, err := s.marketingMutationContext(ctx, actor, input.AccountID, input.OperationID, mutation, input.ExpectedVersion)
		if err != nil {
			return nil, marketingdomain.ReleasePlan{}, err
		}
		value, err := s.marketing.ApproveRelease(ctx, marketingapp.ReleaseTransitionCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, ReleaseID: input.ReleaseID, ExpectedVersion: input.ExpectedVersion, ApprovalID: input.ApprovalID})
		return nil, value, marketingError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_marketing_release_cancel", Title: "Cancel Marketing release", Description: "Cancel a submitted or inactive approved release. Owner or Administrator human only.", Annotations: toolAnnotations(false, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, input marketingReleaseTransitionInput) (*mcp.CallToolResult, marketingdomain.ReleasePlan, error) {
		ctx, op, err := s.marketingMutationContext(ctx, actor, input.AccountID, input.OperationID, mutation, input.ExpectedVersion)
		if err != nil {
			return nil, marketingdomain.ReleasePlan{}, err
		}
		value, err := s.marketing.CancelRelease(ctx, marketingapp.ReleaseTransitionCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, ReleaseID: input.ReleaseID, ExpectedVersion: input.ExpectedVersion})
		return nil, value, marketingError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_marketing_campaign_activate", Title: "Activate Marketing campaign", Description: "Activate only the exact approved release snapshot. Owner or Administrator human only; no delivery occurs here.", Annotations: toolAnnotations(false, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, input marketingCampaignActivationInput) (*mcp.CallToolResult, marketingdomain.Campaign, error) {
		ctx, op, err := s.marketingMutationContext(ctx, actor, input.AccountID, input.OperationID, mutation, input.ExpectedVersion)
		if err != nil {
			return nil, marketingdomain.Campaign{}, err
		}
		value, err := s.marketing.ActivateCampaign(ctx, marketingapp.CampaignTransitionCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, CampaignID: input.CampaignID, ReleaseID: input.ReleaseID, ExpectedVersion: input.ExpectedVersion})
		return nil, value, marketingError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_marketing_campaign_pause", Title: "Pause Marketing campaign", Description: "Pause an active campaign. Owner or Administrator human only.", Annotations: toolAnnotations(false, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, input marketingCampaignTransitionInput) (*mcp.CallToolResult, marketingdomain.Campaign, error) {
		return s.marketingCampaignTransition(ctx, actor, input, mutation, "pause")
	})
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_marketing_campaign_complete", Title: "Complete Marketing campaign", Description: "Complete an active or paused campaign. Owner or Administrator human only.", Annotations: toolAnnotations(false, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, input marketingCampaignTransitionInput) (*mcp.CallToolResult, marketingdomain.Campaign, error) {
		return s.marketingCampaignTransition(ctx, actor, input, mutation, "complete")
	})
}

func (s *Server) marketingCampaignTransition(ctx context.Context, actor access.Actor, input marketingCampaignTransitionInput, requirement access.Requirement, kind string) (*mcp.CallToolResult, marketingdomain.Campaign, error) {
	ctx, op, err := s.marketingMutationContext(ctx, actor, input.AccountID, input.OperationID, requirement, input.ExpectedVersion)
	if err != nil {
		return nil, marketingdomain.Campaign{}, err
	}
	command := marketingapp.CampaignTransitionCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, CampaignID: input.CampaignID, ExpectedVersion: input.ExpectedVersion}
	var value marketingdomain.Campaign
	if kind == "archive" {
		value, err = s.marketing.ArchiveCampaign(ctx, command)
	} else if kind == "pause" {
		value, err = s.marketing.PauseCampaign(ctx, command)
	} else {
		value, err = s.marketing.CompleteCampaign(ctx, command)
	}
	return nil, value, marketingError(err)
}

func (s *Server) marketingMutationContext(ctx context.Context, actor access.Actor, accountID ids.AccountID, operation string, requirement access.Requirement, version uint64) (context.Context, string, error) {
	ctx, op, err := s.marketingCreateContext(ctx, actor, accountID, operation, requirement)
	if err != nil {
		return nil, "", err
	}
	if version == 0 {
		return nil, "", safeError("marketing_version_required")
	}
	return ctx, op, nil
}

func (s *Server) marketingCreateContext(ctx context.Context, actor access.Actor, accountID ids.AccountID, operation string, requirement access.Requirement) (context.Context, string, error) {
	ctx, err := s.toolContext(ctx, actor, accountID, requirement)
	if err != nil {
		return nil, "", err
	}
	op, err := operationID(operation)
	if err != nil {
		return nil, "", err
	}
	return ctx, op, nil
}

func (s *Server) marketingDraftContext(ctx context.Context, actor access.Actor, accountID ids.AccountID, operation string, runID ids.RunID, requirement access.Requirement) (context.Context, string, marketingdomain.Provenance, error) {
	ctx, op, err := s.marketingCreateContext(ctx, actor, accountID, operation, requirement)
	if err != nil {
		return nil, "", marketingdomain.Provenance{}, err
	}
	if actor.UserID != "" {
		if runID != "" {
			return nil, "", marketingdomain.Provenance{}, safeError("invalid_marketing_provenance")
		}
		return ctx, op, marketingdomain.Provenance{Origin: marketingdomain.OriginHuman}, nil
	}
	invocation := strings.TrimPrefix(actor.WorkloadID, "runner-invocation:")
	if invocation == actor.WorkloadID || ids.Validate(invocation) != nil || ids.Validate(string(runID)) != nil {
		return nil, "", marketingdomain.Provenance{}, safeError("invalid_marketing_provenance")
	}
	return ctx, op, marketingdomain.Provenance{Origin: marketingdomain.OriginAgent, RunID: runID, InvocationID: ids.AgentInvocationID(invocation)}, nil
}

func marketingLimit(value int) (int, error) {
	if value == 0 {
		return marketingapp.DefaultPageSize, nil
	}
	if value < 1 || value > marketingapp.MaximumPageSize {
		return 0, safeError("invalid_marketing_limit")
	}
	return value, nil
}

func marketingDigest(raw string) ([32]byte, error) {
	decoded, err := hex.DecodeString(raw)
	if err != nil || len(decoded) != 32 || raw != strings.ToLower(raw) {
		return [32]byte{}, safeError("invalid_marketing_digest")
	}
	value := [32]byte{}
	copy(value[:], decoded)
	return value, nil
}

func marketingAssetOutput(value marketingdomain.AssetRevision) marketingAssetRevisionOutput {
	return marketingAssetRevisionOutput{ID: value.ID, AccountID: value.AccountID, CampaignID: value.CampaignID, AssetID: value.AssetID, Revision: value.Revision, Kind: value.Kind, Title: value.Title, MediaType: value.MediaType, ContentReference: value.ContentReference, ContentSHA256: hex.EncodeToString(value.ContentSHA256[:]), ContentBytes: value.ContentBytes, AlternativeText: value.AlternativeText, CreatedBy: value.CreatedBy, Provenance: value.Provenance, CreatedAt: value.CreatedAt}
}

type marketingMCPCursor struct {
	Version  int       `json:"v"`
	Kind     string    `json:"kind"`
	Date     time.Time `json:"date,omitempty"`
	ID       string    `json:"id,omitempty"`
	AssetID  string    `json:"asset_id,omitempty"`
	Revision uint64    `json:"revision,omitempty"`
}

func encodeMarketingMCPCursor(value marketingMCPCursor) string {
	raw, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeMarketingMCPCursor(raw, kind string) (marketingMCPCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) > 1024 {
		return marketingMCPCursor{}, safeError("invalid_marketing_cursor")
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	var value marketingMCPCursor
	if decoder.Decode(&value) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) || value.Version != 1 || value.Kind != kind {
		return marketingMCPCursor{}, safeError("invalid_marketing_cursor")
	}
	if kind == "campaign" || kind == "release" {
		if value.Date.IsZero() || ids.Validate(value.ID) != nil || value.AssetID != "" || value.Revision != 0 {
			return marketingMCPCursor{}, safeError("invalid_marketing_cursor")
		}
	} else if kind == "asset_revision" {
		if ids.Validate(value.AssetID) != nil || value.Revision == 0 || !value.Date.IsZero() || value.ID != "" {
			return marketingMCPCursor{}, safeError("invalid_marketing_cursor")
		}
	} else {
		return marketingMCPCursor{}, safeError("invalid_marketing_cursor")
	}
	return value, nil
}

var _ MarketingService = (*marketingapp.Service)(nil)
