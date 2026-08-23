package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	marketingapp "github.com/tinfoyle/spyglass-engine/internal/application/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type MarketingRepository struct{ cell *database.CellPool }

func NewMarketingRepository(cell *database.CellPool) (*MarketingRepository, error) {
	if cell == nil {
		return nil, errors.New("Marketing cell pool is required")
	}
	return &MarketingRepository{cell: cell}, nil
}

func (repository *MarketingRepository) CreateCampaign(ctx context.Context, draft domain.CampaignDraftInput, role accounts.MembershipRole, mutation marketingapp.Mutation) (domain.Campaign, bool, error) {
	value, err := domain.NewCampaign(draft, role)
	if err != nil || !validMarketingMutation(mutation, "created", value.CreatedBy, value.CreatedAt) {
		return domain.Campaign{}, false, marketingapp.ErrInvalid
	}
	result, created := value, false
	err = repository.cell.WithAccountTx(ctx, value.AccountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		existing, loadErr := loadMarketingCampaign(ctx, tx, value.AccountID, value.ID, true)
		if loadErr == nil {
			matched, err := marketingEventMatches(ctx, tx, value.AccountID, mutation.EventID, "campaign", string(value.ID), "created", 0, 1)
			comparison := value
			comparison.CreatedAt, comparison.UpdatedAt = existing.CreatedAt, existing.UpdatedAt
			if err != nil || !matched || !reflect.DeepEqual(existing, comparison) {
				if err != nil {
					return err
				}
				return marketingapp.ErrConflict
			}
			result = existing
			return nil
		}
		if !errors.Is(loadErr, marketingapp.ErrNotFound) {
			return loadErr
		}
		if _, err := tx.Exec(ctx, `INSERT INTO spyglass.marketing_campaigns
			(account_id,id,name,objective,audience,state,version,created_by_kind,created_by_id,origin,run_id,invocation_id,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, value.AccountID, value.ID, value.Name, value.Objective, value.Audience,
			value.State, value.Version, value.CreatedBy.Kind, value.CreatedBy.ID, value.Provenance.Origin, nullableMarketingUUID(value.Provenance.RunID),
			nullableMarketingUUID(value.Provenance.InvocationID), value.CreatedAt, value.UpdatedAt); err != nil {
			return err
		}
		for _, channel := range value.Channels {
			if _, err := tx.Exec(ctx, `INSERT INTO spyglass.marketing_campaign_channels(account_id,campaign_id,channel) VALUES ($1,$2,$3)`, value.AccountID, value.ID, channel); err != nil {
				return err
			}
		}
		if err := insertMarketingEvent(ctx, tx, value.AccountID, "campaign", string(value.ID), 0, mutation, map[string]any{"state": value.State, "channel_count": len(value.Channels)}); err != nil {
			return err
		}
		created = true
		return nil
	})
	return result, created, classifyMarketing(err)
}

func (repository *MarketingRepository) GetCampaign(ctx context.Context, accountID ids.AccountID, campaignID ids.MarketingCampaignID) (domain.Campaign, error) {
	var result domain.Campaign
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		value, err := loadMarketingCampaign(ctx, tx, accountID, campaignID, false)
		result = value
		return err
	})
	return result, classifyMarketing(err)
}

func (repository *MarketingRepository) ListCampaigns(ctx context.Context, accountID ids.AccountID, query marketingapp.CampaignListQuery) (marketingapp.CampaignPage, error) {
	query, err := normalizeMarketingCampaignQuery(query)
	if err != nil {
		return marketingapp.CampaignPage{}, err
	}
	var page marketingapp.CampaignPage
	err = repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM spyglass.marketing_campaigns
			WHERE account_id=$1 AND ($2='' OR state=$2) AND ($3::timestamptz IS NULL OR updated_at<$3 OR (updated_at=$3 AND id>$4))
			ORDER BY updated_at DESC,id LIMIT $5`, accountID, query.State, marketingCampaignCursorTime(query.After), marketingCampaignCursorID(query.After), query.Limit+1)
		if err != nil {
			return err
		}
		var idsPage []ids.MarketingCampaignID
		for rows.Next() {
			var id ids.MarketingCampaignID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			idsPage = append(idsPage, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		hasMore := len(idsPage) > query.Limit
		if hasMore {
			idsPage = idsPage[:query.Limit]
		}
		for _, id := range idsPage {
			value, err := loadMarketingCampaign(ctx, tx, accountID, id, false)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, value)
		}
		if hasMore {
			last := page.Items[len(page.Items)-1]
			page.NextCursor = &marketingapp.CampaignCursor{UpdatedAt: last.UpdatedAt, ID: last.ID}
		}
		return nil
	})
	return page, classifyMarketing(err)
}

func (repository *MarketingRepository) ReviseCampaign(ctx context.Context, accountID ids.AccountID, campaignID ids.MarketingCampaignID, command domain.CampaignRevision, mutation marketingapp.Mutation) (domain.Campaign, error) {
	if !validMarketingMutation(mutation, "revised", command.Actor, command.At) {
		return domain.Campaign{}, marketingapp.ErrInvalid
	}
	var result domain.Campaign
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		current, err := loadMarketingCampaign(ctx, tx, accountID, campaignID, true)
		if err != nil {
			return err
		}
		if current.Version == command.ExpectedVersion+1 {
			matched, err := marketingEventMatches(ctx, tx, accountID, mutation.EventID, "campaign", string(campaignID), "revised", command.ExpectedVersion, current.Version)
			expectedValue := current
			expectedValue.Name, expectedValue.Objective, expectedValue.Audience = command.Name, command.Objective, command.Audience
			expectedValue.Channels = append([]domain.Channel(nil), command.Channels...)
			expectedValue, restoreErr := domain.RestoreCampaign(expectedValue)
			if err != nil || restoreErr != nil || !matched || current.Name != expectedValue.Name || current.Objective != expectedValue.Objective ||
				current.Audience != expectedValue.Audience || !reflect.DeepEqual(current.Channels, expectedValue.Channels) {
				if err != nil {
					return err
				}
				return marketingapp.ErrConflict
			}
			result = current
			return nil
		}
		if current.Version != command.ExpectedVersion {
			return marketingapp.ErrConflict
		}
		revised, err := current.Revise(command)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM spyglass.marketing_campaign_channels WHERE account_id=$1 AND campaign_id=$2`, accountID, campaignID); err != nil {
			return err
		}
		for _, channel := range revised.Channels {
			if _, err := tx.Exec(ctx, `INSERT INTO spyglass.marketing_campaign_channels(account_id,campaign_id,channel) VALUES ($1,$2,$3)`, accountID, campaignID, channel); err != nil {
				return err
			}
		}
		updated, err := tx.Exec(ctx, `UPDATE spyglass.marketing_campaigns SET name=$3,objective=$4,audience=$5,version=$6,updated_at=$7
			WHERE account_id=$1 AND id=$2 AND version=$8`, accountID, campaignID, revised.Name, revised.Objective, revised.Audience, revised.Version, revised.UpdatedAt, command.ExpectedVersion)
		if err != nil {
			return err
		}
		if updated.RowsAffected() != 1 {
			return marketingapp.ErrConflict
		}
		if err := insertMarketingEvent(ctx, tx, accountID, "campaign", string(campaignID), command.ExpectedVersion, mutation,
			map[string]any{"state": revised.State, "channel_count": len(revised.Channels)}); err != nil {
			return err
		}
		result = revised
		return nil
	})
	return result, classifyMarketing(err)
}

func (repository *MarketingRepository) CreateAssetRevision(ctx context.Context, input domain.AssetRevisionInput, role accounts.MembershipRole, mutation marketingapp.Mutation) (domain.AssetRevision, bool, error) {
	if !mutation.Valid() || mutation.Kind != "asset_revised" || mutation.Actor != input.CreatedBy || !mutation.At.UTC().Equal(input.CreatedAt.UTC()) {
		return domain.AssetRevision{}, false, marketingapp.ErrInvalid
	}
	var result domain.AssetRevision
	created := false
	err := repository.cell.WithAccountTx(ctx, input.AccountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		existing, loadErr := loadMarketingAssetRevision(ctx, tx, input.AccountID, input.ID)
		if loadErr == nil {
			matched, err := marketingEventMatches(ctx, tx, input.AccountID, mutation.EventID, "asset", string(existing.AssetID), "asset_revised", existing.Revision-1, existing.Revision)
			comparison := existing
			comparison.Kind, comparison.Title, comparison.MediaType, comparison.ContentReference, comparison.ContentSHA256, comparison.ContentBytes = input.Kind, input.Title, input.MediaType, input.ContentReference, input.ContentSHA256, input.ContentBytes
			comparison.AlternativeText, comparison.CreatedBy, comparison.Provenance = input.AlternativeText, input.CreatedBy, input.Provenance
			comparison, restoreErr := domain.RestoreAssetRevision(comparison)
			if err != nil || restoreErr != nil || !matched || existing.CampaignID != input.CampaignID || existing.AssetID != input.AssetID || !reflect.DeepEqual(existing, comparison) {
				if err != nil {
					return err
				}
				return marketingapp.ErrConflict
			}
			result = existing
			return nil
		}
		if !errors.Is(loadErr, marketingapp.ErrNotFound) {
			return loadErr
		}
		campaign, err := loadMarketingCampaign(ctx, tx, input.AccountID, input.CampaignID, true)
		if err != nil || (campaign.State != domain.CampaignDraft && campaign.State != domain.CampaignPaused) {
			if err != nil {
				return err
			}
			return marketingapp.ErrInvalid
		}
		previous, err := loadLatestMarketingAssetRevision(ctx, tx, input.AccountID, input.AssetID)
		if errors.Is(err, marketingapp.ErrNotFound) {
			previous = nil
			if _, err := tx.Exec(ctx, `INSERT INTO spyglass.marketing_assets(account_id,campaign_id,id,created_at) VALUES ($1,$2,$3,$4)`, input.AccountID, input.CampaignID, input.AssetID, input.CreatedAt.UTC()); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		value, err := domain.NewAssetRevision(input, previous, role)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO spyglass.marketing_asset_revisions
			(account_id,id,campaign_id,asset_id,revision,kind,title,media_type,content_reference,content_sha256,content_bytes,alternative_text,
			 created_by_kind,created_by_id,origin,run_id,invocation_id,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`, value.AccountID, value.ID, value.CampaignID, value.AssetID,
			value.Revision, value.Kind, value.Title, value.MediaType, value.ContentReference, value.ContentSHA256[:], value.ContentBytes, value.AlternativeText,
			value.CreatedBy.Kind, value.CreatedBy.ID, value.Provenance.Origin, nullableMarketingUUID(value.Provenance.RunID), nullableMarketingUUID(value.Provenance.InvocationID), value.CreatedAt); err != nil {
			return err
		}
		if err := insertMarketingEvent(ctx, tx, value.AccountID, "asset", string(value.AssetID), value.Revision-1, mutation,
			map[string]any{"revision": value.Revision, "kind": value.Kind, "content_bytes": value.ContentBytes}); err != nil {
			return err
		}
		result, created = value, true
		return nil
	})
	return result, created, classifyMarketing(err)
}

func (repository *MarketingRepository) ListAssetRevisions(ctx context.Context, accountID ids.AccountID, query marketingapp.AssetRevisionListQuery) (marketingapp.AssetRevisionPage, error) {
	query, err := normalizeMarketingAssetQuery(query)
	if err != nil {
		return marketingapp.AssetRevisionPage{}, err
	}
	var page marketingapp.AssetRevisionPage
	err = repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM spyglass.marketing_asset_revisions
			WHERE account_id=$1 AND campaign_id=$2 AND ($3::uuid IS NULL OR asset_id=$3) AND
			      ($4::uuid IS NULL OR asset_id>$4 OR (asset_id=$4 AND revision<$5::bigint))
			ORDER BY asset_id,revision DESC LIMIT $6`, accountID, query.CampaignID, nullableMarketingUUID(query.AssetID), marketingAssetCursorID(query.After), marketingAssetCursorRevision(query.After), query.Limit+1)
		if err != nil {
			return err
		}
		var revisionIDs []ids.MarketingAssetRevisionID
		for rows.Next() {
			var id ids.MarketingAssetRevisionID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			revisionIDs = append(revisionIDs, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		hasMore := len(revisionIDs) > query.Limit
		if hasMore {
			revisionIDs = revisionIDs[:query.Limit]
		}
		for _, id := range revisionIDs {
			value, err := loadMarketingAssetRevision(ctx, tx, accountID, id)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, value)
		}
		if hasMore {
			last := page.Items[len(page.Items)-1]
			page.NextCursor = &marketingapp.AssetRevisionCursor{AssetID: last.AssetID, Revision: last.Revision}
		}
		return nil
	})
	return page, classifyMarketing(err)
}

func (repository *MarketingRepository) CreateReleasePlan(ctx context.Context, input domain.ReleasePlanInput, role accounts.MembershipRole, mutation marketingapp.Mutation) (domain.ReleasePlan, bool, error) {
	value, err := domain.NewReleasePlan(input, role)
	if err != nil || !validMarketingMutation(mutation, "created", value.CreatedBy, value.CreatedAt) {
		return domain.ReleasePlan{}, false, marketingapp.ErrInvalid
	}
	result, created := value, false
	err = repository.cell.WithAccountTx(ctx, value.AccountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		existing, loadErr := loadMarketingRelease(ctx, tx, value.AccountID, value.ID, true)
		if loadErr == nil {
			matched, err := marketingEventMatches(ctx, tx, value.AccountID, mutation.EventID, "release", string(value.ID), "created", 0, 1)
			comparison := value
			comparison.CreatedAt, comparison.UpdatedAt = existing.CreatedAt, existing.UpdatedAt
			if err != nil || !matched || !reflect.DeepEqual(existing, comparison) {
				if err != nil {
					return err
				}
				return marketingapp.ErrConflict
			}
			result = existing
			return nil
		}
		if !errors.Is(loadErr, marketingapp.ErrNotFound) {
			return loadErr
		}
		campaign, err := loadMarketingCampaign(ctx, tx, value.AccountID, value.CampaignID, true)
		if err != nil || campaign.Version != value.CampaignVersion || (campaign.State != domain.CampaignDraft && campaign.State != domain.CampaignPaused) || !reflect.DeepEqual(campaign.Channels, value.Channels) {
			if err != nil {
				return err
			}
			return marketingapp.ErrInvalid
		}
		for _, revisionID := range value.AssetRevisionIDs {
			revision, err := loadMarketingAssetRevision(ctx, tx, value.AccountID, revisionID)
			if err != nil || revision.CampaignID != value.CampaignID {
				if err != nil {
					return err
				}
				return marketingapp.ErrInvalid
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO spyglass.marketing_release_plans
			(account_id,id,campaign_id,campaign_version,name,state,version,created_by_kind,created_by_id,origin,run_id,invocation_id,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, value.AccountID, value.ID, value.CampaignID, value.CampaignVersion, value.Name,
			value.State, value.Version, value.CreatedBy.Kind, value.CreatedBy.ID, value.Provenance.Origin, nullableMarketingUUID(value.Provenance.RunID),
			nullableMarketingUUID(value.Provenance.InvocationID), value.CreatedAt, value.UpdatedAt); err != nil {
			return err
		}
		for _, channel := range value.Channels {
			if _, err := tx.Exec(ctx, `INSERT INTO spyglass.marketing_release_channels(account_id,campaign_id,release_id,channel) VALUES ($1,$2,$3,$4)`, value.AccountID, value.CampaignID, value.ID, channel); err != nil {
				return err
			}
		}
		for _, revisionID := range value.AssetRevisionIDs {
			if _, err := tx.Exec(ctx, `INSERT INTO spyglass.marketing_release_assets(account_id,campaign_id,release_id,asset_revision_id) VALUES ($1,$2,$3,$4)`, value.AccountID, value.CampaignID, value.ID, revisionID); err != nil {
				return err
			}
		}
		if err := insertMarketingEvent(ctx, tx, value.AccountID, "release", string(value.ID), 0, mutation,
			map[string]any{"state": value.State, "channel_count": len(value.Channels), "asset_count": len(value.AssetRevisionIDs)}); err != nil {
			return err
		}
		created = true
		return nil
	})
	return result, created, classifyMarketing(err)
}

func (repository *MarketingRepository) GetReleasePlan(ctx context.Context, accountID ids.AccountID, releaseID ids.MarketingReleaseID) (domain.ReleasePlan, error) {
	var result domain.ReleasePlan
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		value, err := loadMarketingRelease(ctx, tx, accountID, releaseID, false)
		result = value
		return err
	})
	return result, classifyMarketing(err)
}

func (repository *MarketingRepository) ListReleasePlans(ctx context.Context, accountID ids.AccountID, query marketingapp.ReleaseListQuery) (marketingapp.ReleasePage, error) {
	query, err := normalizeMarketingReleaseQuery(query)
	if err != nil {
		return marketingapp.ReleasePage{}, err
	}
	var page marketingapp.ReleasePage
	err = repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM spyglass.marketing_release_plans
			WHERE account_id=$1 AND campaign_id=$2 AND ($3::timestamptz IS NULL OR created_at<$3 OR (created_at=$3 AND id>$4))
			ORDER BY created_at DESC,id LIMIT $5`, accountID, query.CampaignID, marketingReleaseCursorTime(query.After), marketingReleaseCursorID(query.After), query.Limit+1)
		if err != nil {
			return err
		}
		var idsPage []ids.MarketingReleaseID
		for rows.Next() {
			var id ids.MarketingReleaseID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			idsPage = append(idsPage, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		hasMore := len(idsPage) > query.Limit
		if hasMore {
			idsPage = idsPage[:query.Limit]
		}
		for _, id := range idsPage {
			value, err := loadMarketingRelease(ctx, tx, accountID, id, false)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, value)
		}
		if hasMore {
			last := page.Items[len(page.Items)-1]
			page.NextCursor = &marketingapp.ReleaseCursor{CreatedAt: last.CreatedAt, ID: last.ID}
		}
		return nil
	})
	return page, classifyMarketing(err)
}

func (repository *MarketingRepository) SubmitRelease(ctx context.Context, accountID ids.AccountID, releaseID ids.MarketingReleaseID, expected, campaignVersion uint64, actor domain.Actor, role accounts.MembershipRole, mutation marketingapp.Mutation) (domain.ReleasePlan, error) {
	return repository.releaseTransition(ctx, accountID, releaseID, expected, actor, mutation, "submitted", func(current domain.ReleasePlan) bool {
		return current.CampaignVersion == campaignVersion && current.SubmittedBy != nil && *current.SubmittedBy == actor
	}, func(current domain.ReleasePlan) (domain.ReleasePlan, error) {
		return current.Submit(expected, campaignVersion, actor, role, mutation.At)
	})
}

func (repository *MarketingRepository) ApproveRelease(ctx context.Context, accountID ids.AccountID, releaseID ids.MarketingReleaseID, expected uint64, approvalID ids.ConsequentialApprovalID, actor domain.Actor, role accounts.MembershipRole, mutation marketingapp.Mutation) (domain.ReleasePlan, error) {
	return repository.releaseTransition(ctx, accountID, releaseID, expected, actor, mutation, "approved", func(current domain.ReleasePlan) bool {
		return current.ApprovalID == approvalID && current.ApprovedBy != nil && *current.ApprovedBy == actor
	}, func(current domain.ReleasePlan) (domain.ReleasePlan, error) {
		return current.Approve(expected, approvalID, actor, role, mutation.At)
	})
}

func (repository *MarketingRepository) CancelRelease(ctx context.Context, accountID ids.AccountID, releaseID ids.MarketingReleaseID, expected uint64, actor domain.Actor, role accounts.MembershipRole, mutation marketingapp.Mutation) (domain.ReleasePlan, error) {
	return repository.releaseTransition(ctx, accountID, releaseID, expected, actor, mutation, "cancelled", func(domain.ReleasePlan) bool { return true }, func(current domain.ReleasePlan) (domain.ReleasePlan, error) {
		return current.Cancel(expected, actor, role, mutation.At)
	})
}

func (repository *MarketingRepository) releaseTransition(ctx context.Context, accountID ids.AccountID, releaseID ids.MarketingReleaseID, expected uint64, actor domain.Actor, mutation marketingapp.Mutation, target domain.ReleaseState, replayMatches func(domain.ReleasePlan) bool, transition func(domain.ReleasePlan) (domain.ReleasePlan, error)) (domain.ReleasePlan, error) {
	if !validMarketingMutation(mutation, string(target), actor, mutation.At) {
		return domain.ReleasePlan{}, marketingapp.ErrInvalid
	}
	var result domain.ReleasePlan
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		current, err := loadMarketingRelease(ctx, tx, accountID, releaseID, true)
		if err != nil {
			return err
		}
		if current.Version == expected+1 && current.State == target {
			matched, err := marketingEventMatches(ctx, tx, accountID, mutation.EventID, "release", string(releaseID), string(target), expected, current.Version)
			if err != nil || !matched || !replayMatches(current) {
				if err != nil {
					return err
				}
				return marketingapp.ErrConflict
			}
			result = current
			return nil
		}
		if current.Version != expected {
			return marketingapp.ErrConflict
		}
		next, err := transition(current)
		if err != nil {
			return err
		}
		updated, err := tx.Exec(ctx, `UPDATE spyglass.marketing_release_plans
			SET state=$3,approval_id=$4,submitted_by_user_id=$5,approved_by_user_id=$6,version=$7,updated_at=$8
			WHERE account_id=$1 AND id=$2 AND version=$9`, accountID, releaseID, next.State, nullableMarketingUUID(next.ApprovalID), nullableActorID(next.SubmittedBy),
			nullableActorID(next.ApprovedBy), next.Version, next.UpdatedAt, expected)
		if err != nil {
			return err
		}
		if updated.RowsAffected() != 1 {
			return marketingapp.ErrConflict
		}
		if err := insertMarketingEvent(ctx, tx, accountID, "release", string(releaseID), expected, mutation,
			map[string]any{"state": next.State, "channel_count": len(next.Channels), "asset_count": len(next.AssetRevisionIDs)}); err != nil {
			return err
		}
		result = next
		return nil
	})
	return result, classifyMarketing(err)
}

func (repository *MarketingRepository) ActivateCampaign(ctx context.Context, accountID ids.AccountID, campaignID ids.MarketingCampaignID, releaseID ids.MarketingReleaseID, expected uint64, actor domain.Actor, role accounts.MembershipRole, mutation marketingapp.Mutation) (domain.Campaign, error) {
	return repository.campaignTransition(ctx, accountID, campaignID, expected, actor, mutation, "activated", domain.CampaignActive, func(current domain.Campaign) bool {
		return current.ActiveReleaseID == releaseID
	}, func(tx pgx.Tx, current domain.Campaign) (domain.Campaign, error) {
		release, err := loadMarketingRelease(ctx, tx, accountID, releaseID, true)
		if err != nil {
			return domain.Campaign{}, err
		}
		return current.Activate(release, expected, actor, role, mutation.At)
	})
}

func (repository *MarketingRepository) PauseCampaign(ctx context.Context, accountID ids.AccountID, campaignID ids.MarketingCampaignID, expected uint64, actor domain.Actor, role accounts.MembershipRole, mutation marketingapp.Mutation) (domain.Campaign, error) {
	return repository.campaignTransition(ctx, accountID, campaignID, expected, actor, mutation, "paused", domain.CampaignPaused, func(domain.Campaign) bool { return true }, func(_ pgx.Tx, current domain.Campaign) (domain.Campaign, error) {
		return current.Pause(expected, actor, role, mutation.At)
	})
}

func (repository *MarketingRepository) CompleteCampaign(ctx context.Context, accountID ids.AccountID, campaignID ids.MarketingCampaignID, expected uint64, actor domain.Actor, role accounts.MembershipRole, mutation marketingapp.Mutation) (domain.Campaign, error) {
	return repository.campaignTransition(ctx, accountID, campaignID, expected, actor, mutation, "completed", domain.CampaignCompleted, func(domain.Campaign) bool { return true }, func(_ pgx.Tx, current domain.Campaign) (domain.Campaign, error) {
		return current.Complete(expected, actor, role, mutation.At)
	})
}

func (repository *MarketingRepository) ArchiveCampaign(ctx context.Context, accountID ids.AccountID, campaignID ids.MarketingCampaignID, expected uint64, actor domain.Actor, role accounts.MembershipRole, mutation marketingapp.Mutation) (domain.Campaign, error) {
	return repository.campaignTransition(ctx, accountID, campaignID, expected, actor, mutation, "archived", domain.CampaignArchived, func(domain.Campaign) bool { return true }, func(_ pgx.Tx, current domain.Campaign) (domain.Campaign, error) {
		return current.Archive(expected, actor, role, mutation.At)
	})
}

func (repository *MarketingRepository) campaignTransition(ctx context.Context, accountID ids.AccountID, campaignID ids.MarketingCampaignID, expected uint64, actor domain.Actor, mutation marketingapp.Mutation, kind string, target domain.CampaignState, replayMatches func(domain.Campaign) bool, transition func(pgx.Tx, domain.Campaign) (domain.Campaign, error)) (domain.Campaign, error) {
	if !validMarketingMutation(mutation, kind, actor, mutation.At) {
		return domain.Campaign{}, marketingapp.ErrInvalid
	}
	var result domain.Campaign
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		current, err := loadMarketingCampaign(ctx, tx, accountID, campaignID, true)
		if err != nil {
			return err
		}
		if current.Version == expected+1 && current.State == target {
			matched, err := marketingEventMatches(ctx, tx, accountID, mutation.EventID, "campaign", string(campaignID), mutation.Kind, expected, current.Version)
			if err != nil || !matched || !replayMatches(current) {
				if err != nil {
					return err
				}
				return marketingapp.ErrConflict
			}
			result = current
			return nil
		}
		if current.Version != expected {
			return marketingapp.ErrConflict
		}
		next, err := transition(tx, current)
		if err != nil {
			return err
		}
		updated, err := tx.Exec(ctx, `UPDATE spyglass.marketing_campaigns SET state=$3,active_release_id=$4,version=$5,updated_at=$6
			WHERE account_id=$1 AND id=$2 AND version=$7`, accountID, campaignID, next.State, nullableMarketingUUID(next.ActiveReleaseID), next.Version, next.UpdatedAt, expected)
		if err != nil {
			return err
		}
		if updated.RowsAffected() != 1 {
			return marketingapp.ErrConflict
		}
		if err := insertMarketingEvent(ctx, tx, accountID, "campaign", string(campaignID), expected, mutation,
			map[string]any{"state": next.State, "channel_count": len(next.Channels)}); err != nil {
			return err
		}
		result = next
		return nil
	})
	return result, classifyMarketing(err)
}

func loadMarketingCampaign(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, campaignID ids.MarketingCampaignID, lock bool) (domain.Campaign, error) {
	query := `SELECT id,account_id,name,objective,audience,state,COALESCE(active_release_id::text,''),version,created_by_kind,created_by_id,origin,
		COALESCE(run_id::text,''),COALESCE(invocation_id::text,''),created_at,updated_at FROM spyglass.marketing_campaigns WHERE account_id=$1 AND id=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	var value domain.Campaign
	err := tx.QueryRow(ctx, query, accountID, campaignID).Scan(&value.ID, &value.AccountID, &value.Name, &value.Objective, &value.Audience, &value.State,
		&value.ActiveReleaseID, &value.Version, &value.CreatedBy.Kind, &value.CreatedBy.ID, &value.Provenance.Origin, &value.Provenance.RunID,
		&value.Provenance.InvocationID, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Campaign{}, marketingapp.ErrNotFound
	}
	if err != nil {
		return domain.Campaign{}, err
	}
	rows, err := tx.Query(ctx, `SELECT channel FROM spyglass.marketing_campaign_channels WHERE account_id=$1 AND campaign_id=$2 ORDER BY channel`, accountID, campaignID)
	if err != nil {
		return domain.Campaign{}, err
	}
	for rows.Next() {
		var channel domain.Channel
		if err := rows.Scan(&channel); err != nil {
			rows.Close()
			return domain.Campaign{}, err
		}
		value.Channels = append(value.Channels, channel)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return domain.Campaign{}, err
	}
	value, err = domain.RestoreCampaign(value)
	if err != nil {
		return domain.Campaign{}, marketingapp.ErrRepository
	}
	return value, nil
}

func loadMarketingAssetRevision(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, revisionID ids.MarketingAssetRevisionID) (domain.AssetRevision, error) {
	var value domain.AssetRevision
	var digest []byte
	err := tx.QueryRow(ctx, `SELECT id,account_id,campaign_id,asset_id,revision,kind,title,media_type,content_reference,content_sha256,content_bytes,alternative_text,
		created_by_kind,created_by_id,origin,COALESCE(run_id::text,''),COALESCE(invocation_id::text,''),created_at
		FROM spyglass.marketing_asset_revisions WHERE account_id=$1 AND id=$2`, accountID, revisionID).Scan(&value.ID, &value.AccountID, &value.CampaignID,
		&value.AssetID, &value.Revision, &value.Kind, &value.Title, &value.MediaType, &value.ContentReference, &digest, &value.ContentBytes, &value.AlternativeText,
		&value.CreatedBy.Kind, &value.CreatedBy.ID, &value.Provenance.Origin, &value.Provenance.RunID, &value.Provenance.InvocationID, &value.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AssetRevision{}, marketingapp.ErrNotFound
	}
	if err != nil {
		return domain.AssetRevision{}, err
	}
	if len(digest) != 32 {
		return domain.AssetRevision{}, marketingapp.ErrRepository
	}
	copy(value.ContentSHA256[:], digest)
	value, err = domain.RestoreAssetRevision(value)
	if err != nil {
		return domain.AssetRevision{}, marketingapp.ErrRepository
	}
	return value, nil
}

func loadLatestMarketingAssetRevision(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, assetID ids.MarketingAssetID) (*domain.AssetRevision, error) {
	var revisionID ids.MarketingAssetRevisionID
	err := tx.QueryRow(ctx, `SELECT id FROM spyglass.marketing_asset_revisions WHERE account_id=$1 AND asset_id=$2 ORDER BY revision DESC LIMIT 1`, accountID, assetID).Scan(&revisionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, marketingapp.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	value, err := loadMarketingAssetRevision(ctx, tx, accountID, revisionID)
	return &value, err
}

func loadMarketingRelease(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, releaseID ids.MarketingReleaseID, lock bool) (domain.ReleasePlan, error) {
	query := `SELECT id,account_id,campaign_id,campaign_version,name,state,COALESCE(approval_id::text,''),version,created_by_kind,created_by_id,origin,
		COALESCE(run_id::text,''),COALESCE(invocation_id::text,''),submitted_by_user_id::text,approved_by_user_id::text,created_at,updated_at
		FROM spyglass.marketing_release_plans WHERE account_id=$1 AND id=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	var value domain.ReleasePlan
	var submittedBy, approvedBy *string
	err := tx.QueryRow(ctx, query, accountID, releaseID).Scan(&value.ID, &value.AccountID, &value.CampaignID, &value.CampaignVersion, &value.Name, &value.State,
		&value.ApprovalID, &value.Version, &value.CreatedBy.Kind, &value.CreatedBy.ID, &value.Provenance.Origin, &value.Provenance.RunID,
		&value.Provenance.InvocationID, &submittedBy, &approvedBy, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ReleasePlan{}, marketingapp.ErrNotFound
	}
	if err != nil {
		return domain.ReleasePlan{}, err
	}
	if submittedBy != nil {
		value.SubmittedBy = &domain.Actor{Kind: domain.ActorUser, ID: *submittedBy}
	}
	if approvedBy != nil {
		value.ApprovedBy = &domain.Actor{Kind: domain.ActorUser, ID: *approvedBy}
	}
	channelRows, err := tx.Query(ctx, `SELECT channel FROM spyglass.marketing_release_channels WHERE account_id=$1 AND release_id=$2 ORDER BY channel`, accountID, releaseID)
	if err != nil {
		return domain.ReleasePlan{}, err
	}
	for channelRows.Next() {
		var channel domain.Channel
		if err := channelRows.Scan(&channel); err != nil {
			channelRows.Close()
			return domain.ReleasePlan{}, err
		}
		value.Channels = append(value.Channels, channel)
	}
	err = channelRows.Err()
	channelRows.Close()
	if err != nil {
		return domain.ReleasePlan{}, err
	}
	assetRows, err := tx.Query(ctx, `SELECT asset_revision_id FROM spyglass.marketing_release_assets WHERE account_id=$1 AND release_id=$2 ORDER BY asset_revision_id`, accountID, releaseID)
	if err != nil {
		return domain.ReleasePlan{}, err
	}
	for assetRows.Next() {
		var revisionID ids.MarketingAssetRevisionID
		if err := assetRows.Scan(&revisionID); err != nil {
			assetRows.Close()
			return domain.ReleasePlan{}, err
		}
		value.AssetRevisionIDs = append(value.AssetRevisionIDs, revisionID)
	}
	err = assetRows.Err()
	assetRows.Close()
	if err != nil {
		return domain.ReleasePlan{}, err
	}
	value, err = domain.RestoreReleasePlan(value)
	if err != nil {
		return domain.ReleasePlan{}, marketingapp.ErrRepository
	}
	return value, nil
}

func insertMarketingEvent(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, aggregateKind, aggregateID string, from uint64, mutation marketingapp.Mutation, payload map[string]any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO spyglass.marketing_events
		(account_id,id,aggregate_kind,aggregate_id,event_type,from_version,to_version,actor_kind,actor_id,correlation_id,redacted_payload,occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, accountID, mutation.EventID, aggregateKind, aggregateID, mutation.Kind,
		from, from+1, mutation.Actor.Kind, mutation.Actor.ID, mutation.CorrelationID, encoded, mutation.At.UTC())
	return err
}

func marketingEventMatches(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, eventID, aggregateKind, aggregateID, kind string, from, to uint64) (bool, error) {
	var storedAggregateKind, storedAggregateID, storedKind string
	var storedFrom, storedTo uint64
	err := tx.QueryRow(ctx, `SELECT aggregate_kind,aggregate_id,event_type,from_version,to_version FROM spyglass.marketing_events WHERE account_id=$1 AND id=$2`, accountID, eventID).
		Scan(&storedAggregateKind, &storedAggregateID, &storedKind, &storedFrom, &storedTo)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil && storedAggregateKind == aggregateKind && storedAggregateID == aggregateID && storedKind == kind && storedFrom == from && storedTo == to, err
}

func validMarketingMutation(mutation marketingapp.Mutation, kind string, actor domain.Actor, at time.Time) bool {
	return mutation.Valid() && mutation.Kind == kind && mutation.Actor == actor && mutation.At.UTC().Equal(at.UTC())
}

func classifyMarketing(err error) error {
	if err == nil || errors.Is(err, marketingapp.ErrInvalid) || errors.Is(err, marketingapp.ErrNotFound) || errors.Is(err, marketingapp.ErrConflict) {
		return err
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		if postgresError.Code == "23505" || postgresError.Code == "40001" {
			return errors.Join(marketingapp.ErrConflict, err)
		}
		if postgresError.Code == "23503" || postgresError.Code == "23514" || postgresError.Code == "P0001" {
			return errors.Join(marketingapp.ErrInvalid, err)
		}
	}
	return marketingapp.ClassifyForAdapter(err)
}

func nullableMarketingUUID[T ~string](value T) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableActorID(actor *domain.Actor) any {
	if actor == nil {
		return nil
	}
	return actor.ID
}

func normalizeMarketingCampaignQuery(query marketingapp.CampaignListQuery) (marketingapp.CampaignListQuery, error) {
	if query.Limit == 0 {
		query.Limit = marketingapp.DefaultPageSize
	}
	if query.Limit < 1 || query.Limit > marketingapp.MaximumPageSize || (query.State != "" && query.State != domain.CampaignDraft && query.State != domain.CampaignActive &&
		query.State != domain.CampaignPaused && query.State != domain.CampaignCompleted && query.State != domain.CampaignArchived) {
		return marketingapp.CampaignListQuery{}, marketingapp.ErrInvalid
	}
	if query.After != nil && (query.After.UpdatedAt.IsZero() || ids.Validate(string(query.After.ID)) != nil) {
		return marketingapp.CampaignListQuery{}, marketingapp.ErrInvalid
	}
	return query, nil
}

func normalizeMarketingReleaseQuery(query marketingapp.ReleaseListQuery) (marketingapp.ReleaseListQuery, error) {
	if query.Limit == 0 {
		query.Limit = marketingapp.DefaultPageSize
	}
	if query.Limit < 1 || query.Limit > marketingapp.MaximumPageSize || ids.Validate(string(query.CampaignID)) != nil {
		return marketingapp.ReleaseListQuery{}, marketingapp.ErrInvalid
	}
	if query.After != nil && (query.After.CreatedAt.IsZero() || ids.Validate(string(query.After.ID)) != nil) {
		return marketingapp.ReleaseListQuery{}, marketingapp.ErrInvalid
	}
	return query, nil
}

func normalizeMarketingAssetQuery(query marketingapp.AssetRevisionListQuery) (marketingapp.AssetRevisionListQuery, error) {
	if query.Limit == 0 {
		query.Limit = marketingapp.DefaultPageSize
	}
	if query.Limit < 1 || query.Limit > marketingapp.MaximumPageSize || ids.Validate(string(query.CampaignID)) != nil ||
		(query.AssetID != "" && ids.Validate(string(query.AssetID)) != nil) {
		return marketingapp.AssetRevisionListQuery{}, marketingapp.ErrInvalid
	}
	if query.After != nil && (ids.Validate(string(query.After.AssetID)) != nil || query.After.Revision == 0 || (query.AssetID != "" && query.After.AssetID != query.AssetID)) {
		return marketingapp.AssetRevisionListQuery{}, marketingapp.ErrInvalid
	}
	return query, nil
}

func marketingCampaignCursorTime(cursor *marketingapp.CampaignCursor) any {
	if cursor == nil {
		return nil
	}
	return cursor.UpdatedAt.UTC()
}

func marketingCampaignCursorID(cursor *marketingapp.CampaignCursor) any {
	if cursor == nil {
		return nil
	}
	return cursor.ID
}

func marketingReleaseCursorTime(cursor *marketingapp.ReleaseCursor) any {
	if cursor == nil {
		return nil
	}
	return cursor.CreatedAt.UTC()
}

func marketingReleaseCursorID(cursor *marketingapp.ReleaseCursor) any {
	if cursor == nil {
		return nil
	}
	return cursor.ID
}

func marketingAssetCursorID(cursor *marketingapp.AssetRevisionCursor) any {
	if cursor == nil {
		return nil
	}
	return cursor.AssetID
}

func marketingAssetCursorRevision(cursor *marketingapp.AssetRevisionCursor) any {
	if cursor == nil {
		return nil
	}
	return cursor.Revision
}
