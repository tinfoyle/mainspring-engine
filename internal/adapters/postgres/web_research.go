package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	webresearchapp "github.com/tinfoyle/spyglass-engine/internal/application/webresearch"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func (repository *IntegrationsRepository) Resolve(ctx context.Context, accountID ids.AccountID, connectionID ids.IntegrationConnectionID, now time.Time) (webresearchapp.ConnectionAuthority, error) {
	if repository == nil || repository.cell == nil || ctx == nil || ctx.Err() != nil || ids.Validate(string(accountID)) != nil ||
		ids.Validate(string(connectionID)) != nil || now.IsZero() {
		return webresearchapp.ConnectionAuthority{}, webresearchapp.ErrInvalid
	}
	var authority webresearchapp.ConnectionAuthority
	var reference []byte
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT connection.account_id,connection.id,revision.id,revision.revision,revision.https_origin,revision.path_prefix,
			credential.id,credential.generation,credential.provider,credential.reference_sha256
			FROM spyglass.integration_connections connection
			JOIN spyglass.integration_connection_revisions revision ON revision.account_id=connection.account_id AND
				revision.connection_id=connection.id AND revision.revision=connection.current_revision
			JOIN spyglass.integration_credentials credential ON credential.account_id=connection.account_id AND
				credential.connection_id=connection.id AND credential.id=connection.credential_id AND credential.generation=connection.credential_generation
			WHERE connection.account_id=$1 AND connection.id=$2 AND connection.connector_kind='web_research' AND connection.state='active' AND
				revision.capabilities=ARRAY['web.research']::text[] AND credential.state='active' AND
				(credential.expires_at IS NULL OR credential.expires_at>$3)`, accountID, connectionID, now.UTC()).Scan(
			&authority.AccountID, &authority.ConnectionID, &authority.ConnectionRevisionID, &authority.ConnectionRevision,
			&authority.Scope.HTTPSOrigin, &authority.Scope.PathPrefix, &authority.CredentialID, &authority.CredentialGeneration,
			&authority.CredentialProvider, &reference)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return webresearchapp.ConnectionAuthority{}, webresearchapp.ErrNotFound
	}
	if err != nil || len(reference) != sha256.Size {
		return webresearchapp.ConnectionAuthority{}, errors.Join(webresearchapp.ErrUnavailable, err)
	}
	copy(authority.CredentialReferenceSHA256[:], reference)
	return authority, nil
}

func (repository *IntegrationsRepository) RecordCapture(ctx context.Context, capture webresearchapp.Capture) (webresearchapp.Capture, error) {
	if repository == nil || repository.cell == nil || !validWebResearchCapture(capture) || ctx == nil || ctx.Err() != nil {
		return webresearchapp.Capture{}, webresearchapp.ErrInvalid
	}
	result := capture
	err := repository.cell.WithAccountTx(ctx, capture.AccountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		existing, err := loadWebResearchCapture(ctx, tx, capture.AccountID, capture.ID)
		if err == nil {
			if !reflect.DeepEqual(existing, capture) {
				return webresearchapp.ErrConflict
			}
			result = existing
			return nil
		}
		if !errors.Is(err, webresearchapp.ErrNotFound) {
			return err
		}
		var available bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM spyglass.integration_connections connection
			JOIN spyglass.integration_credentials credential ON credential.account_id=connection.account_id AND credential.connection_id=connection.id AND
				credential.id=connection.credential_id AND credential.generation=connection.credential_generation
			WHERE connection.account_id=$1 AND connection.id=$2 AND connection.connector_kind='web_research' AND connection.state='active' AND
				connection.current_revision=$3 AND connection.credential_id=$4 AND connection.credential_generation=$5 AND credential.state='active' AND
				(credential.expires_at IS NULL OR credential.expires_at>$6))`, capture.AccountID, capture.ConnectionID, capture.ConnectionRevision,
			capture.CredentialID, capture.CredentialGeneration, capture.RetrievedAt.UTC()).Scan(&available); err != nil {
			return err
		}
		if !available {
			return webresearchapp.ErrConflict
		}
		actorKind, actorID := "workload", capture.CreatedBy.WorkloadID
		if capture.CreatedBy.UserID != "" {
			actorKind, actorID = "user", string(capture.CreatedBy.UserID)
		}
		_, err = tx.Exec(ctx, `INSERT INTO spyglass.integration_web_research_captures
			(account_id,id,connection_id,connection_revision_id,connection_revision,credential_id,credential_generation,requested_url_sha256,
			 canonical_url,title,excerpt,media_type,content_sha256,document_id,document_revision_id,retrieved_at,recorded_at,created_by_kind,created_by_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`, capture.AccountID, capture.ID,
			capture.ConnectionID, capture.ConnectionRevisionID, capture.ConnectionRevision, capture.CredentialID, capture.CredentialGeneration,
			capture.RequestedURLSHA256[:], capture.CanonicalURL, capture.Title, capture.Excerpt, capture.MediaType, capture.ContentSHA256[:], capture.DocumentID,
			capture.DocumentRevisionID, capture.RetrievedAt.UTC(), capture.RecordedAt.UTC(), actorKind, actorID)
		return err
	})
	if err == nil || errors.Is(err, webresearchapp.ErrConflict) || errors.Is(err, webresearchapp.ErrInvalid) {
		return result, err
	}
	return webresearchapp.Capture{}, errors.Join(webresearchapp.ErrUnavailable, err)
}

func (repository *IntegrationsRepository) GetCapture(ctx context.Context, accountID ids.AccountID, captureID ids.WebResearchCaptureID) (webresearchapp.Capture, error) {
	if repository == nil || repository.cell == nil || ctx == nil || ctx.Err() != nil || ids.Validate(string(accountID)) != nil || ids.Validate(string(captureID)) != nil {
		return webresearchapp.Capture{}, webresearchapp.ErrInvalid
	}
	var result webresearchapp.Capture
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		result, err = loadWebResearchCapture(ctx, tx, accountID, captureID)
		return err
	})
	return result, err
}

func loadWebResearchCapture(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, captureID ids.WebResearchCaptureID) (webresearchapp.Capture, error) {
	var capture webresearchapp.Capture
	var requested, content []byte
	var actorKind, actorID string
	err := tx.QueryRow(ctx, `SELECT id,account_id,connection_id,connection_revision_id,connection_revision,credential_id,credential_generation,
		requested_url_sha256,canonical_url,title,excerpt,media_type,content_sha256,document_id,document_revision_id,retrieved_at,recorded_at,created_by_kind,created_by_id
		FROM spyglass.integration_web_research_captures WHERE account_id=$1 AND id=$2`, accountID, captureID).Scan(&capture.ID,
		&capture.AccountID, &capture.ConnectionID, &capture.ConnectionRevisionID, &capture.ConnectionRevision, &capture.CredentialID,
		&capture.CredentialGeneration, &requested, &capture.CanonicalURL, &capture.Title, &capture.Excerpt, &capture.MediaType, &content, &capture.DocumentID,
		&capture.DocumentRevisionID, &capture.RetrievedAt, &capture.RecordedAt, &actorKind, &actorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return webresearchapp.Capture{}, webresearchapp.ErrNotFound
	}
	if err != nil || len(requested) != sha256.Size || len(content) != sha256.Size {
		return webresearchapp.Capture{}, errors.Join(webresearchapp.ErrUnavailable, err)
	}
	copy(capture.RequestedURLSHA256[:], requested)
	copy(capture.ContentSHA256[:], content)
	if actorKind == "user" {
		capture.CreatedBy = access.Actor{UserID: ids.UserID(actorID)}
	} else if actorKind == "workload" {
		capture.CreatedBy = access.Actor{WorkloadID: actorID}
	} else {
		return webresearchapp.Capture{}, webresearchapp.ErrUnavailable
	}
	return capture, nil
}

func validWebResearchCapture(capture webresearchapp.Capture) bool {
	actorValid := capture.CreatedBy.Valid() && ((capture.CreatedBy.UserID == "" && len(capture.CreatedBy.WorkloadID) <= 256 &&
		capture.CreatedBy.WorkloadID == strings.TrimSpace(capture.CreatedBy.WorkloadID)) || ids.Validate(string(capture.CreatedBy.UserID)) == nil)
	return ids.Validate(string(capture.ID)) == nil && ids.Validate(string(capture.AccountID)) == nil && ids.Validate(string(capture.ConnectionID)) == nil &&
		ids.Validate(string(capture.ConnectionRevisionID)) == nil && capture.ConnectionRevision > 0 && ids.Validate(string(capture.CredentialID)) == nil &&
		capture.CredentialGeneration > 0 && capture.RequestedURLSHA256 != ([sha256.Size]byte{}) && capture.ContentSHA256 != ([sha256.Size]byte{}) &&
		len(capture.CanonicalURL) > 0 && len(capture.CanonicalURL) <= webresearchapp.MaximumURLBytes && utf8.ValidString(capture.CanonicalURL) &&
		len(capture.Title) > 0 && len(capture.Title) <= 200 && capture.Title == strings.TrimSpace(capture.Title) && utf8.ValidString(capture.Title) &&
		len(capture.Excerpt) <= webresearchapp.MaximumExcerptBytes && capture.Excerpt == strings.TrimSpace(capture.Excerpt) && utf8.ValidString(capture.Excerpt) &&
		(capture.MediaType == "text/html" || capture.MediaType == "text/plain" || capture.MediaType == "application/pdf") &&
		ids.Validate(string(capture.DocumentID)) == nil && ids.Validate(string(capture.DocumentRevisionID)) == nil &&
		!capture.RetrievedAt.IsZero() && !capture.RecordedAt.Before(capture.RetrievedAt) && actorValid
}

var _ webresearchapp.Repository = (*IntegrationsRepository)(nil)
