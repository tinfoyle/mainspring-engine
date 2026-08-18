package work

import (
	"context"
	"errors"
	"strings"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	workdomain "github.com/tinfoyle/spyglass-engine/internal/modules/work"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

// QueryService can be composed in a cell without any global capacity
// dependency. It is intentionally read-only; Work mutations use Service.
type QueryService struct {
	authorizer Authorizer
	repository Repository
}

func NewQueryService(authorizer Authorizer, repository Repository) (*QueryService, error) {
	if authorizer == nil || repository == nil {
		return nil, errors.New("work query dependencies are required")
	}
	return &QueryService{authorizer: authorizer, repository: repository}, nil
}

func (s *QueryService) Get(ctx context.Context, actor access.Actor, accountID ids.AccountID, itemID ids.WorkItemID) (workdomain.Item, error) {
	if _, err := s.authorizer.Authorize(ctx, actor, accountID, access.Requirement{Package: PackageCode}); err != nil {
		return workdomain.Item{}, err
	}
	if ids.Validate(string(itemID)) != nil {
		return workdomain.Item{}, ErrInvalidCommand
	}
	return s.repository.Get(ctx, accountID, itemID)
}

func (s *QueryService) List(ctx context.Context, actor access.Actor, accountID ids.AccountID, query ListQuery) (Page, error) {
	if _, err := s.authorizer.Authorize(ctx, actor, accountID, access.Requirement{Package: PackageCode}); err != nil {
		return Page{}, err
	}
	query.Search = strings.TrimSpace(query.Search)
	if query.Limit <= 0 {
		query.Limit = 50
	}
	if err := validateListQuery(query); err != nil {
		return Page{}, err
	}
	return s.repository.List(ctx, accountID, query)
}

func (s *QueryService) Children(ctx context.Context, actor access.Actor, accountID ids.AccountID, parentID ids.WorkItemID, limit int) ([]workdomain.Item, error) {
	if _, err := s.authorizer.Authorize(ctx, actor, accountID, access.Requirement{Package: PackageCode}); err != nil {
		return nil, err
	}
	if ids.Validate(string(parentID)) != nil || limit <= 0 || limit > MaxPageSize {
		return nil, ErrInvalidCommand
	}
	return s.repository.Children(ctx, accountID, parentID, limit)
}

func (s *QueryService) Summary(ctx context.Context, actor access.Actor, accountID ids.AccountID) (Summary, error) {
	if _, err := s.authorizer.Authorize(ctx, actor, accountID, access.Requirement{Package: PackageCode}); err != nil {
		return Summary{}, err
	}
	return s.repository.Summary(ctx, accountID)
}

func validateListQuery(query ListQuery) error {
	if query.Limit <= 0 || query.Limit > MaxPageSize || len(query.States) > 5 || len(query.Kinds) > 2 || len(query.Search) > 200 || (query.AfterUpdatedAt == nil) != (query.AfterID == "") {
		return ErrInvalidCommand
	}
	for _, state := range query.States {
		if !state.Valid() {
			return ErrInvalidCommand
		}
	}
	for _, kind := range query.Kinds {
		if !kind.Valid() {
			return ErrInvalidCommand
		}
	}
	if query.AfterUpdatedAt != nil && (query.AfterUpdatedAt.IsZero() || ids.Validate(string(query.AfterID)) != nil) {
		return ErrInvalidCommand
	}
	return nil
}
