package memory

import (
	"context"
	"sync"

	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
)

type BillingInbox struct {
	mu       sync.Mutex
	entries  map[string]billing.InboxEntry
	payloads map[string][]byte
}

func NewBillingInbox() *BillingInbox {
	return &BillingInbox{entries: map[string]billing.InboxEntry{}, payloads: map[string][]byte{}}
}

func (i *BillingInbox) Accept(_ context.Context, entry billing.InboxEntry, payload []byte) (bool, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if _, exists := i.entries[entry.ProviderEventID]; exists {
		return false, nil
	}
	i.entries[entry.ProviderEventID] = entry
	i.payloads[entry.ProviderEventID] = append([]byte(nil), payload...)
	return true, nil
}

func (i *BillingInbox) Entry(eventID string) (billing.InboxEntry, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	entry, ok := i.entries[eventID]
	return entry, ok
}
