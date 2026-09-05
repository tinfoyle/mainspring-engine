package scheduledreports

import (
	"context"
	"errors"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"testing"
	"time"
)

type reportFixture struct {
	sent                      int
	state, code               string
	recipientError, sendError error
}

func (f *reportFixture) Now() time.Time { return time.Now() }
func (f *reportFixture) Claim(context.Context, string, time.Time) (Claim, bool, error) {
	return Claim{ID: "test"}, true, nil
}
func (f *reportFixture) Resolve(context.Context, Claim) (string, error) {
	return "owner@example.com", f.recipientError
}
func (f *reportFixture) Begin(context.Context, Claim, time.Time) (Message, bool, error) {
	return Message{Body: "report", To: "untrusted@example.com"}, true, nil
}
func (f *reportFixture) Finish(_ context.Context, _ Claim, state, code string, _ time.Time) error {
	f.state, f.code = state, code
	return nil
}
func (f *reportFixture) SendReport(_ context.Context, m Message) error {
	if m.To != "owner@example.com" {
		panic("recipient override")
	}
	f.sent++
	return f.sendError
}
func TestDeliveryPermissionAndUncertainOutcomes(t *testing.T) {
	for _, v := range []struct {
		name            string
		recipient, send error
		state           string
		count           int
	}{
		{"success", nil, nil, "sent", 1}, {"revoked", ErrDenied, nil, "cancelled", 0},
		{"recipient unavailable", errors.New("db unavailable"), nil, "queued", 0},
		{"pre DATA failure", nil, errors.New("connection failed"), "queued", 1},
		{"ambiguous", nil, ErrUnknown, "unknown", 1},
	} {
		t.Run(v.name, func(t *testing.T) {
			f := &reportFixture{recipientError: v.recipient, sendError: v.send}
			p, err := New(f, f, f, ids.RandomGenerator{}, f)
			if err != nil {
				t.Fatal(err)
			}
			worked, err := p.ProcessOne(context.Background())
			if err != nil || !worked || f.state != v.state || f.sent != v.count {
				t.Fatalf("worked=%v state=%s count=%d err=%v", worked, f.state, f.sent, err)
			}
		})
	}
}
