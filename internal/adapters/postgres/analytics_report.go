package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tinfoyle/spyglass-engine/internal/application/analyticsreport"
)

type AnalyticsReportRepository struct{ pool *pgxpool.Pool }

func NewAnalyticsReportRepository(pool *pgxpool.Pool) *AnalyticsReportRepository {
	return &AnalyticsReportRepository{pool: pool}
}

func (r *AnalyticsReportRepository) Report(ctx context.Context, query analyticsreport.Query) ([]analyticsreport.Row, error) {
	rows, err := r.pool.Query(ctx, `SELECT bucket_start,event_name,surface,dimension_value,event_count,unique_subjects
		FROM spyglass_analytics_funnel_report($1,$2,$3,$4,$5)`, query.From, query.To, query.Bucket, query.Dimension, query.MinimumCohort)
	if err != nil {
		return nil, fmt.Errorf("query analytics funnel report: %w", err)
	}
	defer rows.Close()
	result := make([]analyticsreport.Row, 0)
	for rows.Next() {
		var row analyticsreport.Row
		if err := rows.Scan(&row.BucketStart, &row.EventName, &row.Surface, &row.Dimension, &row.EventCount, &row.UniqueSubjects); err != nil {
			return nil, fmt.Errorf("scan analytics funnel report: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read analytics funnel report: %w", err)
	}
	return result, nil
}

var _ analyticsreport.Repository = (*AnalyticsReportRepository)(nil)
