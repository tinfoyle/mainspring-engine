// Package scheduledreports delivers completed reports under email-to-self permission.
package scheduledreports

import (
	"context"
	"errors"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"time"
)

var ErrDenied = errors.New("report recipient is no longer authorized")
var ErrUnknown = errors.New("report delivery outcome is unknown")

type Claim struct {
	AccountID ids.AccountID
	ID        string
	UserID    ids.UserID
	LeaseID   string
}
type Message struct {
	ID             string
	To             string
	Subject        string
	Body           string
	BoardroomID    ids.BoardroomID
	ConversationID ids.ConversationID
	ScheduleID     ids.ScheduleID
}
type Queue interface {
	Claim(context.Context, string, time.Time) (Claim, bool, error)
	Begin(context.Context, Claim, time.Time) (Message, bool, error)
	Finish(context.Context, Claim, string, string, time.Time) error
}
type Recipient interface {
	Resolve(context.Context, Claim) (string, error)
}
type Sender interface {
	SendReport(context.Context, Message) error
}
type Clock interface{ Now() time.Time }
type Processor struct {
	queue     Queue
	recipient Recipient
	sender    Sender
	ids       ids.Generator
	clock     Clock
}

func New(q Queue, r Recipient, s Sender, g ids.Generator, c Clock) (*Processor, error) {
	if q == nil || r == nil || s == nil || g == nil || c == nil {
		return nil, errors.New("report delivery dependencies required")
	}
	return &Processor{q, r, s, g, c}, nil
}
func (p *Processor) ProcessOne(ctx context.Context) (bool, error) {
	claim, found, err := p.queue.Claim(ctx, p.ids.New(), p.clock.Now().UTC())
	if err != nil || !found {
		return found, err
	}
	email, err := p.recipient.Resolve(ctx, claim)
	if err != nil {
		state, code := "queued", "recipient_unavailable"
		if errors.Is(err, ErrDenied) {
			state, code = "cancelled", "recipient_access_revoked"
		}
		return true, p.queue.Finish(ctx, claim, state, code, p.clock.Now().UTC())
	}
	message, ready, err := p.queue.Begin(ctx, claim, p.clock.Now().UTC())
	if err != nil || !ready {
		return true, err
	}
	message.To = email
	state, code := "sent", ""
	if err := p.sender.SendReport(ctx, message); err != nil {
		state, code = "queued", "delivery_failed"
		if errors.Is(err, ErrUnknown) {
			state, code = "unknown", "delivery_outcome_unknown"
		}
	}
	return true, p.queue.Finish(ctx, claim, state, code, p.clock.Now().UTC())
}
