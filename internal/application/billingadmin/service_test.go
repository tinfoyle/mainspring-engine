package billingadmin

import (
	"context"
	"testing"
)

type storeStub struct {
	action, target string
	record         Record
}

func (s *storeStub) Inspect(context.Context, int, Change) ([]Record, error) {
	s.action = "inspect"
	if s.record.TargetID != "" {
		return []Record{s.record}, nil
	}
	return []Record{{Kind: "event", TargetID: "evt_1"}}, nil
}

func TestBillingAdminExplainsSafeFailureClasses(t *testing.T) {
	store := &storeStub{record: Record{Kind: "event", TargetID: "evt_1", LastErrorCode: "subscription_mapping_mismatch"}}
	service, _ := NewService(store, generator{})
	records, _, err := service.Inspect(context.Background(), 1, "operator@example.com", "Inspect mapping failure", "staging", "test")
	if err != nil || len(records) != 1 || records[0].ExplanationCode != "mapping_conflict" || records[0].Explanation == "" {
		t.Fatalf("records=%+v err=%v", records, err)
	}
}
func (s *storeStub) ReplayEvent(_ context.Context, target string, _ Change) (Record, error) {
	s.action, s.target = "replay-event", target
	return Record{Kind: "event", TargetID: target}, nil
}
func (s *storeStub) QueueRefresh(_ context.Context, target string, _ Change) (Record, error) {
	s.action, s.target = "refresh-subscription", target
	return Record{Kind: "reconciliation", TargetID: target}, nil
}

type generator struct{}

func (generator) New() string { return "10000000-0000-4000-8000-000000000001" }

func TestBillingAdminAcceptsOnlyBoundedProviderTargets(t *testing.T) {
	store := &storeStub{}
	service, _ := NewService(store, generator{})
	if _, _, err := service.ReplayEvent(context.Background(), "sub_wrong", "operator@example.com", "Replay verified event", "staging", "test"); err == nil {
		t.Fatal("accepted wrong event identity")
	}
	if _, _, err := service.ReplayEvent(context.Background(), "evt_123", "operator@example.com", "Replay verified event", "staging", "test"); err != nil || store.target != "evt_123" {
		t.Fatalf("replay target=%q err=%v", store.target, err)
	}
	if _, _, err := service.QueueRefresh(context.Background(), "sub_123", "operator@example.com", "Refresh current subscription", "staging", "live"); err != nil || store.action != "refresh-subscription" {
		t.Fatalf("refresh action=%q err=%v", store.action, err)
	}
	if _, _, err := service.Inspect(context.Background(), 101, "operator@example.com", "Inspect failures", "staging", "test"); err == nil {
		t.Fatal("accepted oversized inspection")
	}
}
