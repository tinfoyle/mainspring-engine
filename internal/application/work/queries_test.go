package work

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	workdomain "github.com/tinfoyle/spyglass-engine/internal/modules/work"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestQueryServiceAuthorizesAndBoundsFilters(t *testing.T) {
	repository := &fakeRepository{item: materialized(t, time.Now())}
	service, err := NewQueryService(fakeAuthorizer{role: accounts.RoleViewer}, repository)
	if err != nil {
		t.Fatal(err)
	}
	actor := access.Actor{UserID: ids.UserID(testUser)}
	if _, err := service.Get(context.Background(), actor, ids.AccountID(testAccount), repository.item.ID); err != nil {
		t.Fatal(err)
	}
	invalid := []ListQuery{
		{Limit: 101}, {Limit: 10, States: []workdomain.State{"unknown"}}, {Limit: 10, Kinds: []workdomain.Kind{"memo"}},
		{Limit: 10, AfterUpdatedAt: ptrTime(time.Now())}, {Limit: 10, AfterUpdatedAt: ptrTime(time.Time{}), AfterID: repository.item.ID},
	}
	for index, query := range invalid {
		if _, err := service.List(context.Background(), actor, ids.AccountID(testAccount), query); !errors.Is(err, ErrInvalidCommand) {
			t.Fatalf("case %d error=%v", index, err)
		}
	}
}

func ptrTime(value time.Time) *time.Time { return &value }
