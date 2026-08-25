// Package analyticsreport exposes privacy-bounded aggregate launch-funnel
// evidence. Reports never return a consent subject or customer identifier.
package analyticsreport

import (
	"context"
	"errors"
	"time"
)

type Bucket string
type Dimension string

const (
	BucketHour Bucket = "hour"
	BucketDay  Bucket = "day"
)

var allowedDimensions = map[Dimension]struct{}{
	"none": {}, "device_class": {}, "locale": {}, "route_name": {}, "cta_code": {},
	"feature_code": {}, "package_code": {}, "offer_code": {}, "campaign_code": {},
	"method": {}, "referral_present": {}, "entry_method": {}, "result": {},
	"entry_point": {}, "queue_state": {}, "task_category": {}, "duration_bucket": {},
}

type Query struct {
	From, To      time.Time
	Bucket        Bucket
	Dimension     Dimension
	MinimumCohort int
}

type Row struct {
	BucketStart    time.Time `json:"bucket_start"`
	EventName      string    `json:"event_name"`
	Surface        string    `json:"surface"`
	Dimension      string    `json:"dimension_value"`
	EventCount     uint64    `json:"event_count"`
	UniqueSubjects uint64    `json:"unique_subjects"`
}

type Report struct {
	From          time.Time `json:"from"`
	To            time.Time `json:"to"`
	Bucket        Bucket    `json:"bucket"`
	Dimension     Dimension `json:"dimension"`
	MinimumCohort int       `json:"minimum_cohort"`
	Rows          []Row     `json:"rows"`
}

type Repository interface {
	Report(context.Context, Query) ([]Row, error)
}

type Service struct{ repository Repository }

func NewService(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, errors.New("analytics report repository is required")
	}
	return &Service{repository: repository}, nil
}

func (s *Service) Report(ctx context.Context, query Query) (Report, error) {
	if err := Validate(query); err != nil {
		return Report{}, err
	}
	query.From = query.From.UTC()
	query.To = query.To.UTC()
	rows, err := s.repository.Report(ctx, query)
	if err != nil {
		return Report{}, err
	}
	if rows == nil {
		rows = []Row{}
	}
	return Report{From: query.From, To: query.To, Bucket: query.Bucket, Dimension: query.Dimension, MinimumCohort: query.MinimumCohort, Rows: rows}, nil
}

func Validate(query Query) error {
	if query.From.IsZero() || query.To.IsZero() || !query.From.Before(query.To) || query.To.Sub(query.From) > 395*24*time.Hour {
		return errors.New("analytics report window must be positive and no longer than 395 days")
	}
	if query.Bucket != BucketHour && query.Bucket != BucketDay {
		return errors.New("analytics report bucket must be hour or day")
	}
	if query.Bucket == BucketHour && query.To.Sub(query.From) > 31*24*time.Hour {
		return errors.New("hourly analytics reports cannot exceed 31 days")
	}
	if _, ok := allowedDimensions[query.Dimension]; !ok {
		return errors.New("analytics report dimension is not allowed")
	}
	if query.MinimumCohort < 5 || query.MinimumCohort > 100 {
		return errors.New("analytics report minimum cohort must be between 5 and 100")
	}
	return nil
}
