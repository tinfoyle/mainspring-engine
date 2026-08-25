package affiliatesupport

import (
	"context"
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrAlreadyOpen        = errors.New("an equivalent Affiliate support request is already open")
	ErrNotFound           = errors.New("Affiliate support request was not found")
	ErrNotCancelable      = errors.New("Affiliate support request is not cancelable")
	ErrEnrollmentState    = errors.New("Affiliate enrollment state is not appealable")
	ErrCommissionNotOwned = errors.New("Affiliate commission entry was not found")
)

type Clock interface{ Now() time.Time }

type Repository interface {
	EnrollmentByUser(context.Context, ids.UserID) (affiliates.Enrollment, error)
	CommissionBelongs(context.Context, ids.AffiliateID, ids.CommissionEntryID) (bool, error)
	Create(context.Context, affiliates.SupportRequest, ids.AffiliateSupportEventID) error
	List(context.Context, ids.UserID) ([]affiliates.SupportRequest, error)
	Cancel(context.Context, ids.AffiliateSupportRequestID, ids.UserID, ids.AffiliateSupportEventID, time.Time) (affiliates.SupportRequest, error)
}

type Service struct {
	repository Repository
	ids        ids.Generator
	clock      Clock
}

func New(repository Repository, generator ids.Generator, clock Clock) (*Service, error) {
	if repository == nil || generator == nil || clock == nil {
		return nil, affiliates.ErrInvalidSupportRequest
	}
	return &Service{repository: repository, ids: generator, clock: clock}, nil
}

type SubmitCommand struct {
	UserID            ids.UserID
	Kind              affiliates.SupportKind
	CommissionEntryID ids.CommissionEntryID
}

func (s *Service) Submit(ctx context.Context, command SubmitCommand) (affiliates.SupportRequest, error) {
	if ids.Validate(string(command.UserID)) != nil {
		return affiliates.SupportRequest{}, affiliates.ErrInvalidSupportRequest
	}
	enrollment, err := s.repository.EnrollmentByUser(ctx, command.UserID)
	if err != nil {
		return affiliates.SupportRequest{}, err
	}
	switch command.Kind {
	case affiliates.SupportEnrollmentAppeal:
		if enrollment.State != affiliates.EnrollmentSuspended && enrollment.State != affiliates.EnrollmentClosed {
			return affiliates.SupportRequest{}, ErrEnrollmentState
		}
	case affiliates.SupportCommissionReview:
		owned, err := s.repository.CommissionBelongs(ctx, enrollment.ID, command.CommissionEntryID)
		if err != nil {
			return affiliates.SupportRequest{}, err
		}
		if !owned {
			return affiliates.SupportRequest{}, ErrCommissionNotOwned
		}
	default:
		return affiliates.SupportRequest{}, affiliates.ErrInvalidSupportRequest
	}
	request, err := affiliates.NewSupportRequest(ids.AffiliateSupportRequestID(s.ids.New()), enrollment,
		command.Kind, command.CommissionEntryID, s.clock.Now())
	if err != nil {
		return affiliates.SupportRequest{}, err
	}
	if err := s.repository.Create(ctx, request, ids.AffiliateSupportEventID(s.ids.New())); err != nil {
		return affiliates.SupportRequest{}, err
	}
	return request, nil
}

func (s *Service) List(ctx context.Context, userID ids.UserID) ([]affiliates.SupportRequest, error) {
	if ids.Validate(string(userID)) != nil {
		return nil, affiliates.ErrInvalidSupportRequest
	}
	return s.repository.List(ctx, userID)
}

func (s *Service) Cancel(ctx context.Context, requestID ids.AffiliateSupportRequestID, userID ids.UserID) (affiliates.SupportRequest, error) {
	if ids.Validate(string(requestID)) != nil || ids.Validate(string(userID)) != nil {
		return affiliates.SupportRequest{}, affiliates.ErrInvalidSupportRequest
	}
	return s.repository.Cancel(ctx, requestID, userID, ids.AffiliateSupportEventID(s.ids.New()), s.clock.Now())
}
