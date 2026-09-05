// Package trafficreport exposes bounded, audited operational HTTP traffic.
// This is separate from optional, consent-based product analytics.
package trafficreport

import (
	"context"
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/operations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var ErrUnavailable = errors.New("traffic logs are unavailable")
var ErrInvalidQuery = errors.New("traffic report query is invalid")

type Query struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

type Entry struct {
	Time       time.Time `json:"time"`
	IP         string    `json:"ip"`
	Host       string    `json:"host"`
	Method     string    `json:"method"`
	Status     int       `json:"status"`
	UserAgent  string    `json:"user_agent"`
	DurationMS float64   `json:"duration_ms"`
}

type IPCount struct {
	IP        string    `json:"ip"`
	Requests  uint64    `json:"requests"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

type Count struct {
	Label    string `json:"label"`
	Requests uint64 `json:"requests"`
}

type Report struct {
	From            time.Time  `json:"from"`
	To              time.Time  `json:"to"`
	GeneratedAt     time.Time  `json:"generated_at"`
	AvailableFrom   *time.Time `json:"available_from"`
	AvailableTo     *time.Time `json:"available_to"`
	Requests        uint64     `json:"requests"`
	UniqueIPs       int        `json:"unique_ips"`
	ServerErrors    uint64     `json:"server_errors"`
	FilesRead       int        `json:"files_read"`
	InvalidRecords  int        `json:"invalid_records"`
	Truncated       bool       `json:"truncated"`
	IPListTruncated bool       `json:"ip_list_truncated"`
	IPs             []IPCount  `json:"ips"`
	Hosts           []Count    `json:"hosts"`
	Statuses        []Count    `json:"statuses"`
	Days            []Count    `json:"days"`
	Logs            []Entry    `json:"logs"`
}

type Reader interface {
	Read(context.Context, Query) (Report, error)
}
type Authorizer interface {
	AuthorizeTrafficRead(context.Context, ids.UserID, Query, operations.AuditReason) error
}

type Service struct {
	reader     Reader
	authorizer Authorizer
}

func New(reader Reader, authorizer Authorizer) (*Service, error) {
	if reader == nil || authorizer == nil {
		return nil, ErrUnavailable
	}
	return &Service{reader: reader, authorizer: authorizer}, nil
}

func Validate(query Query, now time.Time) error {
	if query.From.IsZero() || query.To.IsZero() || !query.From.Before(query.To) || query.To.Sub(query.From) > 7*24*time.Hour || query.From.Before(now.Add(-8*24*time.Hour)) || query.To.After(now.Add(time.Minute)) {
		return ErrInvalidQuery
	}
	return nil
}

func (s *Service) Report(ctx context.Context, actor ids.UserID, query Query, audit operations.AuditReason) (Report, error) {
	if err := Validate(query, time.Now()); err != nil {
		return Report{}, err
	}
	if ids.Validate(string(actor)) != nil || audit.Validate() != nil {
		return Report{}, ErrInvalidQuery
	}
	// Audit and current administrator authority must succeed before touching logs.
	if err := s.authorizer.AuthorizeTrafficRead(ctx, actor, query, audit); err != nil {
		return Report{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.reader.Read(ctx, query)
}
