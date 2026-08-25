package affiliatesupport_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/affiliatesupport"
	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	supportUserID      = "30000000-0000-4000-8000-000000000001"
	supportAffiliateID = "30000000-0000-4000-8000-000000000002"
	supportCommission  = "30000000-0000-4000-8000-000000000003"
)

type supportRepository struct {
	enrollment affiliates.Enrollment
	belongs    bool
	created    affiliates.SupportRequest
	requests   []affiliates.SupportRequest
}

func (r *supportRepository) EnrollmentByUser(context.Context, ids.UserID) (affiliates.Enrollment, error) {
	return r.enrollment, nil
}
func (r *supportRepository) CommissionBelongs(context.Context, ids.AffiliateID, ids.CommissionEntryID) (bool, error) {
	return r.belongs, nil
}
func (r *supportRepository) Create(_ context.Context, request affiliates.SupportRequest, _ ids.AffiliateSupportEventID) error {
	r.created = request
	return nil
}
func (r *supportRepository) List(context.Context, ids.UserID) ([]affiliates.SupportRequest, error) {
	return r.requests, nil
}
func (r *supportRepository) Cancel(_ context.Context, requestID ids.AffiliateSupportRequestID, userID ids.UserID, _ ids.AffiliateSupportEventID, now time.Time) (affiliates.SupportRequest, error) {
	return affiliates.SupportRequest{ID: requestID, AffiliateID: supportAffiliateID, UserID: userID,
		Kind: affiliates.SupportEnrollmentAppeal, State: affiliates.SupportCanceled, Version: 2,
		CreatedAt: now.Add(-time.Minute), UpdatedAt: now}, nil
}

type fixedSupportIDs struct{ values []string }

func (g *fixedSupportIDs) New() string { value := g.values[0]; g.values = g.values[1:]; return value }

type fixedSupportClock struct{ now time.Time }

func (c fixedSupportClock) Now() time.Time { return c.now }

func testEnrollment(state affiliates.EnrollmentState) affiliates.Enrollment {
	return affiliates.Enrollment{ID: supportAffiliateID, UserID: supportUserID, PublicCode: "IO-REVIEW1",
		TermsVersion: 1, RuleVersion: 1, State: state, Version: 2,
		CreatedAt: time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)}
}

func TestSubmitEnrollmentAppealRequiresSuspendedOrClosed(t *testing.T) {
	repository := &supportRepository{enrollment: testEnrollment(affiliates.EnrollmentActive)}
	service, _ := affiliatesupport.New(repository, &fixedSupportIDs{}, fixedSupportClock{time.Now()})
	if _, err := service.Submit(context.Background(), affiliatesupport.SubmitCommand{UserID: supportUserID, Kind: affiliates.SupportEnrollmentAppeal}); !errors.Is(err, affiliatesupport.ErrEnrollmentState) {
		t.Fatalf("error=%v", err)
	}
}

func TestSubmitCommissionReviewRequiresOwnedEntry(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	repository := &supportRepository{enrollment: testEnrollment(affiliates.EnrollmentSuspended), belongs: true}
	generator := &fixedSupportIDs{values: []string{"30000000-0000-4000-8000-000000000004", "30000000-0000-4000-8000-000000000005"}}
	service, _ := affiliatesupport.New(repository, generator, fixedSupportClock{now})
	request, err := service.Submit(context.Background(), affiliatesupport.SubmitCommand{UserID: supportUserID,
		Kind: affiliates.SupportCommissionReview, CommissionEntryID: supportCommission})
	if err != nil || request.CommissionEntryID != supportCommission || repository.created.ID != request.ID {
		t.Fatalf("request=%+v created=%+v err=%v", request, repository.created, err)
	}
}

func TestSupportRequestJSONNeverExposesUserIdentity(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	request := affiliates.SupportRequest{ID: "30000000-0000-4000-8000-000000000004", AffiliateID: supportAffiliateID,
		UserID: supportUserID, Kind: affiliates.SupportEnrollmentAppeal, State: affiliates.SupportSubmitted,
		Version: 1, CreatedAt: now, UpdatedAt: now}
	encoded, err := json.Marshal(request)
	if err != nil || strings.Contains(string(encoded), supportUserID) || strings.Contains(string(encoded), "user_id") {
		t.Fatalf("json=%s err=%v", encoded, err)
	}
}
