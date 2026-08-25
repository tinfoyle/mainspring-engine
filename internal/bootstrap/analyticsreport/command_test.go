package analyticsreport

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	application "github.com/tinfoyle/spyglass-engine/internal/application/analyticsreport"
)

func TestRunRejectsInvalidConfigBeforeConnecting(t *testing.T) {
	query := application.Query{From: time.Now().Add(-time.Hour), To: time.Now(), Bucket: application.BucketDay, Dimension: "none", MinimumCohort: 5}
	if err := Run(context.Background(), Config{Query: query, Output: &bytes.Buffer{}}); err == nil {
		t.Fatal("missing database URL was accepted")
	}
	if err := Run(context.Background(), Config{DatabaseURL: "postgres://example", Query: application.Query{From: query.To, To: query.From, Bucket: application.BucketDay, Dimension: "none", MinimumCohort: 5}, Output: &bytes.Buffer{}}); err == nil {
		t.Fatal("invalid query was accepted")
	}
}

func TestReportJSONKeepsBothWindowBounds(t *testing.T) {
	report := application.Report{
		From:          time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		To:            time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC),
		Bucket:        application.BucketDay,
		Dimension:     "none",
		MinimumCohort: 5,
		Rows:          []application.Row{},
	}
	var output bytes.Buffer
	if err := writeReport(&output, report); err != nil {
		t.Fatal(err)
	}
	encoded := output.String()
	if !strings.Contains(encoded, `"from": "2026-08-01T00:00:00Z"`) || !strings.Contains(encoded, `"to": "2026-08-02T00:00:00Z"`) {
		t.Fatalf("report window missing from JSON: %s", encoded)
	}
}
