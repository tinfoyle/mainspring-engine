package privacyconsent_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/privacyconsent"
	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type repository struct{ decisions []privacy.Decision }

func (r *repository) Append(_ context.Context, decision privacy.Decision) error {
	r.decisions = append(r.decisions, decision)
	return nil
}
func (r *repository) Current(_ context.Context, subject ids.ConsentSubjectID, surface privacy.Surface) (privacy.Decision, error) {
	for index := len(r.decisions) - 1; index >= 0; index-- {
		if r.decisions[index].SubjectID == subject && r.decisions[index].Surface == surface {
			return r.decisions[index], nil
		}
	}
	return privacy.Decision{}, privacyconsent.ErrNotFound
}

type generator struct{ next int }

func (g *generator) New() string {
	g.next++
	if g.next == 1 {
		return "10000000-0000-4000-8000-000000000001"
	}
	return "10000000-0000-4000-8000-000000000002"
}

type clock struct{ now time.Time }

func (c clock) Now() time.Time { return c.now }

func TestSetCreatesSubjectAndImmutableDecision(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	repository := &repository{}
	service, err := privacyconsent.New(repository, &generator{}, clock{now}, 4)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := service.Set(context.Background(), privacyconsent.SetCommand{Surface: privacy.SurfacePublic, Analytics: true})
	if err != nil || decision.SubjectID == "" || decision.ID == "" || decision.PolicyVersion != 4 || !decision.Analytics || decision.Marketing {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	current, err := service.Current(context.Background(), decision.SubjectID, privacy.SurfacePublic)
	if err != nil || current.ID != decision.ID {
		t.Fatalf("current=%+v err=%v", current, err)
	}
}

func TestCurrentRejectsUnverifiableSubject(t *testing.T) {
	service, _ := privacyconsent.New(&repository{}, &generator{}, clock{time.Now()}, 1)
	if _, err := service.Current(context.Background(), "bad", privacy.SurfacePublic); !errors.Is(err, privacy.ErrInvalidDecision) {
		t.Fatalf("invalid subject returned %v", err)
	}
}
