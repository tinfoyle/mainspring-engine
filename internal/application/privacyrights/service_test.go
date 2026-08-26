package privacyrights_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/privacyrights"
	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const testUserID ids.UserID = "10000000-0000-4000-8000-000000000001"

type rightsRepository struct{ requests []privacy.RightsRequest }

func (r *rightsRepository) Create(_ context.Context, request privacy.RightsRequest) error {
	for _, existing := range r.requests {
		if existing.UserID == request.UserID && existing.Kind == request.Kind && existing.Scope == request.Scope &&
			(existing.State == privacy.RightsSubmitted || existing.State == privacy.RightsInReview) {
			return privacyrights.ErrAlreadyOpen
		}
	}
	r.requests = append(r.requests, request)
	return nil
}
func (r *rightsRepository) List(_ context.Context, userID ids.UserID, _ int) ([]privacy.RightsRequest, error) {
	result := make([]privacy.RightsRequest, 0)
	for _, request := range r.requests {
		if request.UserID == userID {
			result = append(result, request)
		}
	}
	return result, nil
}
func (r *rightsRepository) Cancel(_ context.Context, requestID ids.PrivacyRightsRequestID, userID ids.UserID, now time.Time) (privacy.RightsRequest, error) {
	for index := range r.requests {
		if r.requests[index].ID == requestID && r.requests[index].UserID == userID {
			if r.requests[index].State != privacy.RightsSubmitted {
				return privacy.RightsRequest{}, privacyrights.ErrNotCancelable
			}
			r.requests[index].State, r.requests[index].UpdatedAt = privacy.RightsCanceled, now
			return r.requests[index], nil
		}
	}
	return privacy.RightsRequest{}, privacyrights.ErrNotFound
}

type fixedID struct{}

func (fixedID) New() string { return "20000000-0000-4000-8000-000000000001" }

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func verifiedSession(now time.Time) sessions.Session {
	return sessions.Session{UserID: testUserID, AuthenticationMethod: sessions.AuthenticationMethodPasskey,
		ReauthenticationMethod: sessions.AuthenticationMethodPasskey, ReauthenticatedAt: now}
}

func TestSubmitRequiresStrongAuthenticationAndTracksCalendarMonth(t *testing.T) {
	now := time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC)
	service, _ := privacyrights.New(&rightsRepository{}, fixedID{}, fixedClock{now})
	command := privacyrights.SubmitCommand{UserID: testUserID, Kind: privacy.RightsAccess, Scope: privacy.RightsIdentity}
	if _, err := service.Submit(context.Background(), command); !errors.Is(err, strongauth.ErrRequired) {
		t.Fatalf("weak session error=%v", err)
	}
	command.Session = verifiedSession(now)
	request, err := service.Submit(context.Background(), command)
	if err != nil || request.ResponseDueAt != time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC) {
		t.Fatalf("request=%+v err=%v", request, err)
	}
}

func TestDuplicateOpenRequestIsRejectedAndSubmittedRequestCanBeCanceled(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	repository := &rightsRepository{}
	service, _ := privacyrights.New(repository, fixedID{}, fixedClock{now})
	command := privacyrights.SubmitCommand{UserID: testUserID, Session: verifiedSession(now), Kind: privacy.RightsErasure, Scope: privacy.RightsAffiliate}
	request, err := service.Submit(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Submit(context.Background(), command); !errors.Is(err, privacyrights.ErrAlreadyOpen) {
		t.Fatalf("duplicate error=%v", err)
	}
	canceled, err := service.Cancel(context.Background(), privacyrights.CancelCommand{RequestID: request.ID, UserID: testUserID, Session: verifiedSession(now)})
	if err != nil || canceled.State != privacy.RightsCanceled {
		t.Fatalf("canceled=%+v err=%v", canceled, err)
	}
}
