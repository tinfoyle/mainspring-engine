package trafficreport

import (
	"context"
	"github.com/tinfoyle/spyglass-engine/internal/modules/operations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"testing"
	"time"
)

type readerFake struct{ called bool }

func (r *readerFake) Read(context.Context, Query) (Report, error) {
	r.called = true
	return Report{}, nil
}

type authorizerFake struct {
	err    error
	called bool
}

func (a *authorizerFake) AuthorizeTrafficRead(context.Context, ids.UserID, Query, operations.AuditReason) error {
	a.called = true
	return a.err
}

func TestTrafficReadRequiresSuccessfulAuditAndAuthority(t *testing.T) {
	r := &readerFake{}
	a := &authorizerFake{err: operations.ErrStaffUnauthorized}
	s, _ := New(r, a)
	now := time.Now()
	q := Query{From: now.Add(-time.Hour), To: now}
	audit := operations.AuditReason{Ticket: "OPS-100", Reason: "Review current site traffic"}
	actor := ids.UserID("63000000-0000-4000-8000-000000000001")
	if _, err := s.Report(context.Background(), actor, q, audit); err != operations.ErrStaffUnauthorized || r.called {
		t.Fatal("unauthorized read reached logs")
	}
	a.err = nil
	if _, err := s.Report(context.Background(), actor, q, audit); err != nil || !r.called {
		t.Fatal("authorized report failed", err)
	}
	r.called = false
	a.called = false
	q.From = now.Add(-9 * 24 * time.Hour)
	if _, err := s.Report(context.Background(), actor, q, audit); err != ErrInvalidQuery || r.called || a.called {
		t.Fatal("invalid window was not rejected before access")
	}
}
