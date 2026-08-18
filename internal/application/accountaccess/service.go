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
	AccountID               ids.AccountID           `json:"account_id"`
	Slug                    string                  `json:"slug"`
	DisplayName             string                  `json:"display_name"`
	AccountType             accounts.AccountType    `json:"account_type"`
	AccountVersion          uint64                  `json:"account_version"`
	Role                    accounts.MembershipRole `json:"role"`
	CellID                  ids.CellID              `json:"cell_id"`
	PlacementGeneration     uint64                  `json:"placement_generation"`
	Entitlements            entitlements.Snapshot   `json:"entitlements"`
	OwnerEnrollmentRequired bool                    `json:"owner_enrollment_required"`
}

type Repository interface {
	Choices(context.Context, ids.UserID) ([]Choice, error)
}

type Service struct {
	repository    Repository
	authorizer    *access.Authorizer
	ownerSecurity access.OwnerSecurityPolicy
}

func NewService(repository Repository, authorizer *access.Authorizer, ownerSecurity access.OwnerSecurityPolicy) (*Service, error) {
	if repository == nil || authorizer == nil || ownerSecurity == nil {
		return nil, errors.New("account access dependencies are required")
	}
	return &Service{repository: repository, authorizer: authorizer, ownerSecurity: ownerSecurity}, nil
}

func (s *Service) List(ctx context.Context, userID ids.UserID) ([]Choice, error) {
	if userID == "" {
		return nil, &access.DeniedError{Code: access.DenialUnauthenticated}
	}
	choices, err := s.repository.Choices(ctx, userID)
	if err != nil {
		return nil, err
	}
	ready := true
	for _, choice := range choices {
		if choice.Role == accounts.RoleOwner {
			ready, err = s.ownerSecurity.Ready(ctx, userID)
			if err != nil {
				return nil, err
			}
			break
		}
	}
	for index := range choices {
		choices[index].OwnerEnrollmentRequired = choices[index].Role == accounts.RoleOwner && !ready
	}
	return choices, nil
}

func (s *Service) Select(ctx context.Context, userID ids.UserID, accountID ids.AccountID) (access.AccountContext, error) {
	return s.authorizer.Authorize(ctx, access.Actor{UserID: userID}, accountID, access.Requirement{})
}
