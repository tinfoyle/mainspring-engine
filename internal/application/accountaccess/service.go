package accountaccess

import (
	"context"
	"errors"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Choice struct {
	AccountID           ids.AccountID           `json:"account_id"`
	Slug                string                  `json:"slug"`
	DisplayName         string                  `json:"display_name"`
	AccountType         accounts.AccountType    `json:"account_type"`
	AccountVersion      uint64                  `json:"account_version"`
	Role                accounts.MembershipRole `json:"role"`
	CellID              ids.CellID              `json:"cell_id"`
	PlacementGeneration uint64                  `json:"placement_generation"`
	Entitlements        entitlements.Snapshot   `json:"entitlements"`
}

type Repository interface {
	Choices(context.Context, ids.UserID) ([]Choice, error)
}

type Service struct {
	repository Repository
	authorizer *access.Authorizer
}

func NewService(repository Repository, authorizer *access.Authorizer) (*Service, error) {
	if repository == nil || authorizer == nil {
		return nil, errors.New("account access dependencies are required")
	}
	return &Service{repository: repository, authorizer: authorizer}, nil
}

func (s *Service) List(ctx context.Context, userID ids.UserID) ([]Choice, error) {
	if userID == "" {
		return nil, &access.DeniedError{Code: access.DenialUnauthenticated}
	}
	return s.repository.Choices(ctx, userID)
}

func (s *Service) Select(ctx context.Context, userID ids.UserID, accountID ids.AccountID) (access.AccountContext, error) {
	return s.authorizer.Authorize(ctx, access.Actor{UserID: userID}, accountID, access.Requirement{})
}
