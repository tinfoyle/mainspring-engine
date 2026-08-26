package privacy_test

import (
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestRightsRequestDeadlineIsOneClampedCalendarMonth(t *testing.T) {
	tests := []struct {
		name      string
		requested time.Time
		due       time.Time
	}{
		{"ordinary month", time.Date(2026, 8, 24, 12, 34, 56, 789, time.FixedZone("EDT", -4*60*60)), time.Date(2026, 9, 24, 16, 34, 56, 789, time.UTC)},
		{"non leap January end", time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC), time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC)},
		{"leap January end", time.Date(2028, 1, 31, 12, 0, 0, 0, time.UTC), time.Date(2028, 2, 29, 12, 0, 0, 0, time.UTC)},
		{"thirty first into thirty days", time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC), time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)},
		{"year boundary", time.Date(2026, 12, 31, 12, 0, 0, 0, time.UTC), time.Date(2027, 1, 31, 12, 0, 0, 0, time.UTC)},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := privacy.NewRightsRequest(
				ids.PrivacyRightsRequestID("10000000-0000-4000-8000-000000000001"),
				ids.UserID("20000000-0000-4000-8000-000000000002"),
				privacy.RightsAccess,
				privacy.RightsIdentity,
				test.requested,
			)
			if err != nil || !request.ResponseDueAt.Equal(test.due) {
				t.Fatalf("case=%d due=%s want=%s err=%v", index, request.ResponseDueAt, test.due, err)
			}
		})
	}
}
