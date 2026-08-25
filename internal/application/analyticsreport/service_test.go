package analyticsreport

import (
	"context"
	"testing"
	"time"
)

type memoryRepository struct{ query Query }

func (m *memoryRepository) Report(_ context.Context, query Query) ([]Row, error) {
	m.query = query
	return nil, nil
}

func TestReportValidatesBoundsAndNormalizesEmptyRows(t *testing.T) {
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.FixedZone("offset", -4*60*60))
	repository := &memoryRepository{}
	service, err := NewService(repository)
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Report(context.Background(), Query{From: from, To: from.Add(24 * time.Hour), Bucket: BucketDay, Dimension: "offer_code", MinimumCohort: 5})
	if err != nil {
		t.Fatal(err)
	}
	if report.Rows == nil || repository.query.From.Location() != time.UTC || report.MinimumCohort != 5 {
		t.Fatalf("unexpected report: %#v query=%#v", report, repository.query)
	}
	invalid := []Query{
		{From: from, To: from, Bucket: BucketDay, Dimension: "none", MinimumCohort: 5},
		{From: from, To: from.Add(32 * 24 * time.Hour), Bucket: BucketHour, Dimension: "none", MinimumCohort: 5},
		{From: from, To: from.Add(time.Hour), Bucket: BucketDay, Dimension: "email", MinimumCohort: 5},
		{From: from, To: from.Add(time.Hour), Bucket: BucketDay, Dimension: "none", MinimumCohort: 4},
	}
	for _, query := range invalid {
		if _, err := service.Report(context.Background(), query); err == nil {
			t.Fatalf("invalid query accepted: %#v", query)
		}
	}
}
