package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	marketingapp "github.com/tinfoyle/spyglass-engine/internal/application/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/application/marketingaction"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	marketingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type MarketingActionStore struct {
	cell  *database.CellPool
	clock interface{ Now() time.Time }
}

func NewMarketingActionStore(cell *database.CellPool, clock interface{ Now() time.Time }) (*MarketingActionStore, error) {
	if cell == nil || clock == nil {
		return nil, errors.New("marketing action store dependencies are required")
	}
	return &MarketingActionStore{cell: cell, clock: clock}, nil
}

func (store *MarketingActionStore) ActivateRelease(ctx context.Context, accountID ids.AccountID, campaignID ids.MarketingCampaignID, campaignVersion uint64, releaseID ids.MarketingReleaseID, releaseVersion uint64, operationID string, approvedBy ids.UserID) error {
	releaseEventID, campaignEventID, err := marketingActivationEventIDs(operationID)
	if err != nil {
		return marketingaction.ErrInvalid
	}
	actor := marketingdomain.Actor{Kind: marketingdomain.ActorUser, ID: string(approvedBy)}
	at := store.clock.Now().UTC()
	err = store.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		approvalID, err := marketingApprovalForOperation(ctx, tx, accountID, operationID)
		if err != nil {
			return err
		}
		campaign, err := loadMarketingCampaign(ctx, tx, accountID, campaignID, true)
		if err != nil {
			return err
		}
		release, err := loadMarketingRelease(ctx, tx, accountID, releaseID, true)
		if err != nil {
			return err
		}
		if campaign.Version == campaignVersion+1 && campaign.State == marketingdomain.CampaignActive && campaign.ActiveReleaseID == releaseID &&
			release.Version == releaseVersion+1 && release.State == marketingdomain.ReleaseApproved && release.ApprovalID == approvalID {
			releaseMatched, matchErr := marketingEventMatches(ctx, tx, accountID, releaseEventID, "release", string(releaseID), "approved", releaseVersion, release.Version)
			if matchErr != nil || !releaseMatched {
				if matchErr != nil {
					return matchErr
				}
				return marketingapp.ErrConflict
			}
			campaignMatched, matchErr := marketingEventMatches(ctx, tx, accountID, campaignEventID, "campaign", string(campaignID), "activated", campaignVersion, campaign.Version)
			if matchErr != nil || !campaignMatched {
				if matchErr != nil {
					return matchErr
				}
				return marketingapp.ErrConflict
			}
			return nil
		}
		if campaign.Version != campaignVersion || release.Version != releaseVersion || release.CampaignID != campaignID {
			return marketingapp.ErrConflict
		}
		approved, err := release.Approve(releaseVersion, approvalID, actor, accounts.RoleAdministrator, at)
		if err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `UPDATE spyglass.marketing_release_plans
			SET state=$3,approval_id=$4,submitted_by_user_id=$5,approved_by_user_id=$6,version=$7,updated_at=$8
			WHERE account_id=$1 AND id=$2 AND version=$9`, accountID, releaseID, approved.State, approved.ApprovalID,
			nullableActorID(approved.SubmittedBy), nullableActorID(approved.ApprovedBy), approved.Version, approved.UpdatedAt, releaseVersion)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return marketingapp.ErrConflict
		}
		if err := insertMarketingEvent(ctx, tx, accountID, "release", string(releaseID), releaseVersion, marketingapp.Mutation{
			EventID: releaseEventID, Kind: "approved", Actor: actor, CorrelationID: operationID, At: at,
		}, map[string]any{"state": approved.State, "channel_count": len(approved.Channels), "asset_count": len(approved.AssetRevisionIDs)}); err != nil {
			return err
		}
		activated, err := campaign.Activate(approved, campaignVersion, actor, accounts.RoleAdministrator, at)
		if err != nil {
			return err
		}
		result, err = tx.Exec(ctx, `UPDATE spyglass.marketing_campaigns
			SET state=$3,active_release_id=$4,version=$5,updated_at=$6
			WHERE account_id=$1 AND id=$2 AND version=$7`, accountID, campaignID, activated.State, activated.ActiveReleaseID,
			activated.Version, activated.UpdatedAt, campaignVersion)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return marketingapp.ErrConflict
		}
		return insertMarketingEvent(ctx, tx, accountID, "campaign", string(campaignID), campaignVersion, marketingapp.Mutation{
			EventID: campaignEventID, Kind: "activated", Actor: actor, CorrelationID: operationID, At: at,
		}, map[string]any{"state": activated.State, "release_id": activated.ActiveReleaseID, "channel_count": len(activated.Channels)})
	})
	if err == nil {
		return nil
	}
	err = classifyMarketing(err)
	if errors.Is(err, marketingapp.ErrInvalid) || errors.Is(err, marketingapp.ErrNotFound) || errors.Is(err, marketingdomain.ErrApproval) || errors.Is(err, marketingdomain.ErrRole) || errors.Is(err, marketingdomain.ErrState) {
		return errors.Join(marketingaction.ErrInvalid, err)
	}
	if errors.Is(err, marketingapp.ErrConflict) || errors.Is(err, marketingdomain.ErrConflict) {
		return errors.Join(marketingaction.ErrConflict, err)
	}
	return errors.Join(marketingaction.ErrRepository, err)
}

func (store *MarketingActionStore) ReleaseActivatedByEvent(ctx context.Context, accountID ids.AccountID, campaignID ids.MarketingCampaignID, campaignVersion uint64, releaseID ids.MarketingReleaseID, releaseVersion uint64, operationID string) (bool, error) {
	releaseEventID, campaignEventID, err := marketingActivationEventIDs(operationID)
	if err != nil {
		return false, marketingaction.ErrInvalid
	}
	var releaseMatched, campaignMatched bool
	err = store.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		releaseMatched, err = marketingEventMatches(ctx, tx, accountID, releaseEventID, "release", string(releaseID), "approved", releaseVersion, releaseVersion+1)
		if err != nil || !releaseMatched {
			return err
		}
		campaignMatched, err = marketingEventMatches(ctx, tx, accountID, campaignEventID, "campaign", string(campaignID), "activated", campaignVersion, campaignVersion+1)
		return err
	})
	if err != nil {
		return false, errors.Join(marketingaction.ErrRepository, err)
	}
	return releaseMatched && campaignMatched, nil
}

func marketingApprovalForOperation(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, operationID string) (ids.ConsequentialApprovalID, error) {
	var approvalID ids.ConsequentialApprovalID
	err := tx.QueryRow(ctx, `SELECT approval_id FROM spyglass.runner_action_authorizations
		WHERE account_id=$1 AND operation_id=$2 AND capability=$3 AND state='approved'`,
		accountID, operationID, marketingaction.ReleaseActivateCapability).Scan(&approvalID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && ids.Validate(string(approvalID)) != nil) {
		return "", marketingaction.ErrInvalid
	}
	return approvalID, err
}

func marketingActivationEventIDs(operationID string) (string, string, error) {
	releaseEventID, err := ids.Derive(operationID, "marketing-release-approved")
	if err != nil {
		return "", "", err
	}
	campaignEventID, err := ids.Derive(operationID, "marketing-campaign-activated")
	return releaseEventID, campaignEventID, err
}

var _ marketingaction.Store = (*MarketingActionStore)(nil)
