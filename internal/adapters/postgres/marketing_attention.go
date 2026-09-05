package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	attentionapp "github.com/tinfoyle/spyglass-engine/internal/application/attention"
	marketingapp "github.com/tinfoyle/spyglass-engine/internal/application/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	attention "github.com/tinfoyle/spyglass-engine/internal/modules/attention"
	marketing "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type humanMarketingPayload struct {
	CampaignID      ids.MarketingCampaignID `json:"campaign_id"`
	CampaignVersion uint64                  `json:"campaign_version"`
	ReleaseID       ids.MarketingReleaseID  `json:"release_id"`
	ReleaseVersion  uint64                  `json:"release_version"`
}

func marketingReleaseEvidence(release marketing.ReleasePlan) [sha256.Size]byte {
	raw, _ := json.Marshal(struct {
		CampaignID      ids.MarketingCampaignID
		CampaignVersion uint64
		ReleaseID       ids.MarketingReleaseID
		ReleaseVersion  uint64
		Channels        []marketing.Channel
		Assets          []ids.MarketingAssetRevisionID
	}{release.CampaignID, release.CampaignVersion, release.ID, release.Version, release.Channels, release.AssetRevisionIDs})
	return sha256.Sum256(raw)
}

func createHumanMarketingApproval(ctx context.Context, tx pgx.Tx, release marketing.ReleasePlan, actor marketing.Actor, mutation marketingapp.Mutation) error {
	if actor.Kind != marketing.ActorUser || release.State != marketing.ReleaseSubmitted {
		return marketingapp.ErrInvalid
	}
	// Locking the release serializes concurrent requests. Reuse an open request
	// for the same version, while allowing a fresh request after expiry/rejection.
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM spyglass.attention_consequential_approvals
		WHERE account_id=$1 AND invocation_id IS NULL AND capability='marketing.release.activate'
		AND state='open' AND expires_at>$4 AND convert_from(canonical_payload,'UTF8')::jsonb->>'release_id'=$2
		AND (convert_from(canonical_payload,'UTF8')::jsonb->>'release_version')::bigint=$3)`, release.AccountID, string(release.ID), release.Version, mutation.At).Scan(&exists)
	if err != nil || exists {
		return err
	}
	approvalID, err := ids.Derive(mutation.EventID, "marketing-release-approval")
	if err != nil {
		return err
	}
	operationID, err := ids.Derive(mutation.EventID, "marketing-release-activation")
	if err != nil {
		return err
	}
	if previous, found, err := loadApproval(ctx, tx, release.AccountID, ids.ConsequentialApprovalID(approvalID)); err != nil {
		return err
	} else if found {
		if previous.OperationID != operationID {
			return marketingapp.ErrConflict
		}
		return nil
	}
	payload, err := json.Marshal(humanMarketingPayload{release.CampaignID, release.CampaignVersion, release.ID, release.Version})
	if err != nil {
		return err
	}
	at := mutation.At.UTC().Truncate(time.Microsecond)
	item, err := attention.NewConsequentialApproval(attention.ConsequentialApprovalDraft{
		ID: ids.ConsequentialApprovalID(approvalID), AccountID: release.AccountID, OperationID: operationID,
		Capability: "marketing.release.activate", CanonicalPayload: payload, EvidenceSHA256: marketingReleaseEvidence(release),
		Proposer: attention.Actor{Kind: attention.ActorUser, ID: actor.ID}, PolicyVersion: 1, ExpiresAt: at.Add(24 * time.Hour),
	}, at)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO spyglass.attention_consequential_approvals
		(account_id,id,operation_id,invocation_id,capability,canonical_payload,input_sha256,hash_version,evidence_sha256,
		proposer_kind,proposer_id,policy_version,require_independent_review,expires_at,state,version,created_at,updated_at)
		VALUES ($1,$2,$3,NULL,$4,$5,$6,1,$7,'user',$8,1,false,$9,'open',1,$10,$10)`,
		item.AccountID, item.ID, item.OperationID, item.Capability, []byte(item.CanonicalPayload), item.InputSHA256[:], item.EvidenceSHA256[:], actor.ID, item.ExpiresAt, at)
	if err != nil {
		return err
	}
	eventID, _ := ids.Derive(approvalID, "requested")
	_, err = tx.Exec(ctx, `INSERT INTO spyglass.attention_events
		(account_id,id,aggregate_kind,consequential_approval_id,event_type,from_version,to_version,actor_kind,actor_id,reason,correlation_id,redacted_payload,occurred_at)
		VALUES ($1,$2,'consequential_approval',$3,'approval_requested',0,1,'user',$4,'marketing_release_submitted',$5,'{"state":"open","capability":"marketing.release.activate","hash_version":1,"policy_version":1}', $6)`,
		item.AccountID, eventID, item.ID, actor.ID, mutation.CorrelationID, at)
	return err
}

// Human approval applies the exact internal campaign activation atomically with
// the Attention decision. Delivery remains a separately authorized Integration.
func activateHumanMarketingApproval(ctx context.Context, tx pgx.Tx, approval attention.ConsequentialApproval) error {
	if approval.InvocationID != "" || approval.Capability != "marketing.release.activate" || approval.Proposer.Kind != attention.ActorUser || approval.Decision == nil {
		return attentionapp.ErrCorrupt
	}
	var payload humanMarketingPayload
	if err := json.Unmarshal(approval.CanonicalPayload, &payload); err != nil {
		return attentionapp.ErrCorrupt
	}
	campaign, err := loadMarketingCampaign(ctx, tx, approval.AccountID, payload.CampaignID, true)
	if err != nil {
		return attentionapp.ErrConflict
	}
	release, err := loadMarketingRelease(ctx, tx, approval.AccountID, payload.ReleaseID, true)
	if err != nil {
		return attentionapp.ErrConflict
	}
	if campaign.Version != payload.CampaignVersion || release.Version != payload.ReleaseVersion || release.CampaignID != campaign.ID || release.State != marketing.ReleaseSubmitted || marketingReleaseEvidence(release) != approval.EvidenceSHA256 {
		return attentionapp.ErrConflict
	}
	actor := marketing.Actor{Kind: marketing.ActorUser, ID: string(approval.Decision.DecidedBy)}
	at := approval.Decision.DecidedAt
	approved, err := release.Approve(release.Version, approval.ID, actor, accounts.RoleAdministrator, at)
	if err != nil {
		return attentionapp.ErrConflict
	}
	activated, err := campaign.Activate(approved, campaign.Version, actor, accounts.RoleAdministrator, at)
	if err != nil {
		return attentionapp.ErrConflict
	}
	result, err := tx.Exec(ctx, `UPDATE spyglass.marketing_release_plans SET state='approved',approval_id=$3,approved_by_user_id=$4,version=$5,updated_at=$6 WHERE account_id=$1 AND id=$2 AND version=$7`, approval.AccountID, release.ID, approval.ID, actor.ID, approved.Version, at, release.Version)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return attentionapp.ErrConflict
	}
	result, err = tx.Exec(ctx, `UPDATE spyglass.marketing_campaigns SET state='active',active_release_id=$3,version=$4,updated_at=$5 WHERE account_id=$1 AND id=$2 AND version=$6`, approval.AccountID, campaign.ID, release.ID, activated.Version, at, campaign.Version)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return attentionapp.ErrConflict
	}
	for _, event := range []struct {
		kind, id, action string
		before           uint64
	}{{"release", string(release.ID), "approved", release.Version}, {"campaign", string(campaign.ID), "activated", campaign.Version}} {
		eventID, err := ids.Derive(approval.OperationID, fmt.Sprintf("marketing-%s-%s", event.kind, event.action))
		if err != nil {
			return err
		}
		if err := insertMarketingEvent(ctx, tx, approval.AccountID, event.kind, event.id, event.before, marketingapp.Mutation{EventID: eventID, Kind: event.action, Actor: actor, CorrelationID: approval.OperationID, At: at}, map[string]any{"state": map[string]string{"release": "approved", "campaign": "active"}[event.kind]}); err != nil {
			return err
		}
	}
	return nil
}
