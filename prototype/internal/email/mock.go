package email

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type MockConnector struct{}

func NewMockConnector() *MockConnector                         { return &MockConnector{} }
func (*MockConnector) Test(context.Context, Integration) error { return nil }
func (*MockConnector) Inbox(_ context.Context, _ Integration, limit int) ([]InboxMessage, error) {
	items := []InboxMessage{
		{UID: 103, From: "Sam Carter <sam@example.test>", To: []string{"office@example.test"}, Subject: "Friday service appointment", Date: time.Now().UTC().Add(-35 * time.Minute), Unread: true, MessageID: "<demo-103@example.test>"},
		{UID: 102, From: "Northside Supply <orders@example.test>", To: []string{"office@example.test"}, Subject: "Order 4481 is ready", Date: time.Now().UTC().Add(-4 * time.Hour), MessageID: "<demo-102@example.test>"},
		{UID: 101, From: "Alex Rivera <alex@example.test>", To: []string{"office@example.test"}, Subject: "Question about estimate 221", Date: time.Now().UTC().Add(-26 * time.Hour), Unread: true, MessageID: "<demo-101@example.test>"},
	}
	if limit > 0 && limit < len(items) {
		items = items[:limit]
	}
	return items, nil
}
func (c *MockConnector) Message(ctx context.Context, integration Integration, uid uint32) (Message, error) {
	items, _ := c.Inbox(ctx, integration, 50)
	for _, item := range items {
		if item.UID == uid {
			return Message{InboxMessage: item, Body: "Hello,\n\nThis is a simulated inbox message for the Mainspring development environment. It lets us test inbox access safely before connecting a real mailbox.\n\nThanks"}, nil
		}
	}
	return Message{}, ErrMessageNotFound
}
func (*MockConnector) Send(_ context.Context, integration Integration, message OutgoingMessage) (SendResult, error) {
	if len(message.To) == 0 {
		return SendResult{}, fmt.Errorf("at least one recipient is required")
	}
	return SendResult{MessageID: "<" + uuid.NewString() + "@" + senderDomain(integration.EmailAddress) + ">", SentAt: time.Now().UTC()}, nil
}
