package privacyrightsadmin_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/privacyrightsadmin"
	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const requestID ids.PrivacyRightsRequestID = "10000000-0000-4000-8000-000000000001"

type fixedID struct{}

func (fixedID) New() string { return "20000000-0000-4000-8000-000000000002" }

type store struct {
	request  privacy.RightsRequest
	evidence privacyrightsadmin.ResolutionEvidence
	queue    []privacyrightsadmin.QueueItem
}

func (s *store) ListOpen(_ context.Context, _ time.Time, _ int, _ privacyrightsadmin.Change) ([]privacyrightsadmin.QueueItem, error) {
	return append([]privacyrightsadmin.QueueItem(nil), s.queue...), nil
}

func (s *store) Inspect(_ context.Context, _ ids.PrivacyRightsRequestID, _ privacyrightsadmin.Change) (privacy.RightsRequest, error) {
	return s.request, nil
}

func TestOpenQueueIsBoundedAndContainsNoCustomerIdentity(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	item := privacyrightsadmin.QueueItem{RequestID: requestID, Version: 2, Kind: privacy.RightsRestriction,
		Scope: privacy.RightsAnalytics, State: privacy.RightsInReview, RequestedAt: now,
		ResponseDueAt: now.AddDate(0, 1, 0), UpdatedAt: now.Add(time.Hour)}
	service, _ := privacyrightsadmin.New(&store{queue: []privacyrightsadmin.QueueItem{item}}, fixedID{})
	items, err := service.ListOpen(context.Background(), now.AddDate(0, 1, 0), 25,
		"privacy@example.test", "Prioritize the open deadline queue", "local")
	if err != nil || len(items) != 1 || items[0] != item {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	for name, run := range map[string]func() error{
		"deadline": func() error {
			_, err := service.ListOpen(context.Background(), time.Time{}, 25, "privacy@example.test", "Prioritize open requests", "local")
			return err
		},
		"zero limit": func() error {
			_, err := service.ListOpen(context.Background(), now, 0, "privacy@example.test", "Prioritize open requests", "local")
			return err
		},
		"large limit": func() error {
			_, err := service.ListOpen(context.Background(), now, 101, "privacy@example.test", "Prioritize open requests", "local")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(); !errors.Is(err, privacyrightsadmin.ErrInvalidChange) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func (s *store) StartReview(_ context.Context, _ ids.PrivacyRightsRequestID, expected uint64, _ privacyrightsadmin.Change) (privacy.RightsRequest, error) {
	if expected != s.request.Version || s.request.State != privacy.RightsSubmitted {
		return privacy.RightsRequest{}, privacyrightsadmin.ErrStateConflict
	}
	s.request.Version++
	s.request.State = privacy.RightsInReview
	return s.request, nil
}

func (s *store) Resolve(_ context.Context, _ ids.PrivacyRightsRequestID, expected uint64, state privacy.RightsState, evidence privacyrightsadmin.ResolutionEvidence, _ privacyrightsadmin.Change) (privacy.RightsRequest, error) {
	if expected != s.request.Version || s.request.State != privacy.RightsInReview {
		return privacy.RightsRequest{}, privacyrightsadmin.ErrStateConflict
	}
	s.evidence = evidence
	s.request.Version++
	s.request.State = state
	return s.request, nil
}

func TestReviewAndResolutionRequireExactVersionAndOpaqueEvidence(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	record, _ := privacy.NewRightsRequest(requestID, "30000000-0000-4000-8000-000000000003", privacy.RightsErasure, privacy.RightsAnalytics, now)
	repository := &store{request: record}
	service, _ := privacyrightsadmin.New(repository, fixedID{})
	reviewed, err := service.StartReview(context.Background(), requestID, 1, "privacy@example.test", "Verify and fulfill request", "local")
	if err != nil || reviewed.State != privacy.RightsInReview || reviewed.Version != 2 {
		t.Fatalf("reviewed=%+v err=%v", reviewed, err)
	}
	if _, err := service.Resolve(context.Background(), requestID, 1, privacy.RightsCompleted, privacyrightsadmin.ResolutionEvidence{ID: "40000000-0000-4000-8000-000000000004"}, "privacy@example.test", "Fulfillment evidence reviewed", "local"); !errors.Is(err, privacyrightsadmin.ErrStateConflict) {
		t.Fatalf("stale resolution error=%v", err)
	}
	evidence := privacyrightsadmin.ResolutionEvidence{ID: "40000000-0000-4000-8000-000000000004", SHA256: [32]byte{1, 2, 3}}
	resolved, err := service.Resolve(context.Background(), requestID, 2, privacy.RightsPartiallyCompleted, evidence, "privacy@example.test", "Fulfillment evidence reviewed", "local")
	if err != nil || resolved.State != privacy.RightsPartiallyCompleted || resolved.Version != 3 || repository.evidence != evidence {
		t.Fatalf("resolved=%+v evidence=%+v err=%v", resolved, repository.evidence, err)
	}
}

func TestOperatorInputRejectsMissingEvidenceAndUnsafeAuditFields(t *testing.T) {
	service, _ := privacyrightsadmin.New(&store{}, fixedID{})
	for name, run := range map[string]func() error{
		"request": func() error {
			_, err := service.Inspect(context.Background(), "bad", "privacy@example.test", "Inspect request", "local")
			return err
		},
		"actor": func() error {
			_, err := service.Inspect(context.Background(), requestID, "x", "Inspect request", "local")
			return err
		},
		"reason": func() error {
			_, err := service.Inspect(context.Background(), requestID, "privacy@example.test", "bad\nreason", "local")
			return err
		},
		"environment": func() error {
			_, err := service.Inspect(context.Background(), requestID, "privacy@example.test", "Inspect request", "Local!")
			return err
		},
		"evidence": func() error {
			_, err := service.Resolve(context.Background(), requestID, 1, privacy.RightsCompleted, privacyrightsadmin.ResolutionEvidence{}, "privacy@example.test", "Resolve request", "local")
			return err
		},
		"state": func() error {
			_, err := service.Resolve(context.Background(), requestID, 1, privacy.RightsCanceled, privacyrightsadmin.ResolutionEvidence{ID: "40000000-0000-4000-8000-000000000004"}, "privacy@example.test", "Resolve request", "local")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(); !errors.Is(err, privacyrightsadmin.ErrInvalidChange) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
