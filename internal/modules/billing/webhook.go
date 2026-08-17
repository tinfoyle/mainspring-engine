package billing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidSignature = errors.New("invalid webhook signature")
	ErrStaleSignature   = errors.New("webhook signature timestamp is outside tolerance")
	ErrInvalidEvent     = errors.New("invalid billing event")
	ErrWrongMode        = errors.New("billing event mode does not match endpoint")
)

type Clock interface{ Now() time.Time }

type SignatureVerifier struct {
	secret    []byte
	tolerance time.Duration
	clock     Clock
}

func NewSignatureVerifier(secret string, tolerance time.Duration, clock Clock) (*SignatureVerifier, error) {
	if !strings.HasPrefix(secret, "whsec_") || len(secret) < 12 {
		return nil, errors.New("Stripe webhook secret must be a non-placeholder whsec_ value")
	}
	if tolerance <= 0 {
		return nil, errors.New("signature tolerance must be positive")
	}
	return &SignatureVerifier{secret: []byte(secret), tolerance: tolerance, clock: clock}, nil
}

func (v *SignatureVerifier) Verify(payload []byte, signatureHeader string) (time.Time, error) {
	parts := strings.Split(signatureHeader, ",")
	var timestamp int64
	signatures := make([]string, 0, 2)
	for _, part := range parts {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch key {
		case "t":
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return time.Time{}, ErrInvalidSignature
			}
			timestamp = parsed
		case "v1":
			signatures = append(signatures, value)
		}
	}
	if timestamp <= 0 || len(signatures) == 0 {
		return time.Time{}, ErrInvalidSignature
	}
	signedAt := time.Unix(timestamp, 0).UTC()
	age := v.clock.Now().UTC().Sub(signedAt)
	if age < -v.tolerance || age > v.tolerance {
		return time.Time{}, ErrStaleSignature
	}
	message := append([]byte(strconv.FormatInt(timestamp, 10)+"."), payload...)
	mac := hmac.New(sha256.New, v.secret)
	_, _ = mac.Write(message)
	expected := mac.Sum(nil)
	for _, encoded := range signatures {
		provided, err := hex.DecodeString(encoded)
		if err == nil && hmac.Equal(expected, provided) {
			return signedAt, nil
		}
	}
	return time.Time{}, ErrInvalidSignature
}

type EventEnvelope struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Created  int64           `json:"created"`
	LiveMode bool            `json:"livemode"`
	Data     json.RawMessage `json:"data"`
}

type InboxEntry struct {
	ProviderEventID     string
	EventType           string
	ProviderCreatedAt   time.Time
	ProviderObjectID    string
	Mode                string
	PayloadHash         [32]byte
	SignatureVerifiedAt time.Time
	ProcessingState     string
	AttemptCount        int
	CreatedAt           time.Time
}

type Inbox interface {
	Accept(context.Context, InboxEntry, []byte) (accepted bool, err error)
}

type IngestResult struct {
	EventID  string
	Accepted bool
	Mode     string
}

type WebhookService struct {
	verifier *SignatureVerifier
	inbox    Inbox
	mode     string
	clock    Clock
}

func NewWebhookService(verifier *SignatureVerifier, inbox Inbox, mode string, clock Clock) (*WebhookService, error) {
	if mode != "test" && mode != "live" {
		return nil, errors.New("billing webhook mode must be test or live")
	}
	if verifier == nil || inbox == nil || clock == nil {
		return nil, errors.New("billing webhook dependencies are required")
	}
	return &WebhookService{verifier: verifier, inbox: inbox, mode: mode, clock: clock}, nil
}

func (s *WebhookService) Ingest(ctx context.Context, payload []byte, signatureHeader string) (IngestResult, error) {
	verifiedAt, err := s.verifier.Verify(payload, signatureHeader)
	if err != nil {
		return IngestResult{}, err
	}
	var envelope EventEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return IngestResult{}, fmt.Errorf("%w: malformed JSON", ErrInvalidEvent)
	}
	if !strings.HasPrefix(envelope.ID, "evt_") || envelope.Type == "" || envelope.Created <= 0 {
		return IngestResult{}, fmt.Errorf("%w: missing event identity", ErrInvalidEvent)
	}
	eventMode := "test"
	if envelope.LiveMode {
		eventMode = "live"
	}
	if eventMode != s.mode {
		return IngestResult{}, ErrWrongMode
	}
	objectID := extractObjectID(envelope.Data)
	entry := InboxEntry{ProviderEventID: envelope.ID, EventType: envelope.Type, ProviderCreatedAt: time.Unix(envelope.Created, 0).UTC(), ProviderObjectID: objectID, Mode: eventMode, PayloadHash: sha256.Sum256(payload), SignatureVerifiedAt: verifiedAt, ProcessingState: "accepted", CreatedAt: s.clock.Now().UTC()}
	accepted, err := s.inbox.Accept(ctx, entry, payload)
	if err != nil {
		return IngestResult{}, err
	}
	return IngestResult{EventID: envelope.ID, Accepted: accepted, Mode: eventMode}, nil
}

func extractObjectID(data json.RawMessage) string {
	var wrapper struct {
		Object struct {
			ID string `json:"id"`
		} `json:"object"`
	}
	if json.Unmarshal(data, &wrapper) != nil {
		return ""
	}
	return wrapper.Object.ID
}
