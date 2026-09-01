// Package multifactor implements verified email and SMS one-time-code factors.
// Passkeys remain a separate, phishing-resistant factor.
package multifactor

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Kind string
type Purpose string

const (
	KindSMS   Kind = "sms"
	KindEmail Kind = "email"

	PurposeEnrollment       Purpose = "enrollment"
	PurposeReauthentication Purpose = "reauthentication"

	SMSConsentVersion = "sms-security-v1-2026-09-01"
	SMSConsentText    = "By checking this box, you agree to receive one-time security codes from Infinite Ocean at this number. Message frequency varies. Standard message and data rates may apply. Reply STOP to opt out or HELP for help. Consent is not a condition of purchase. Your mobile information will not be sold or shared with third parties or affiliates for promotional or marketing purposes."

	codeTTL = 10 * time.Minute
)

var (
	ErrInvalidRequest   = errors.New("multifactor request is invalid")
	ErrInvalidChallenge = errors.New("multifactor code is invalid or expired")
	ErrRateLimited      = errors.New("too many multifactor codes were requested")
	phonePattern        = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)
	usPhonePattern      = regexp.MustCompile(`^\+1[2-9][0-9]{2}[2-9][0-9]{6}$`)
)

type Method struct {
	ID              string     `json:"id"`
	Kind            Kind       `json:"kind"`
	DestinationHint string     `json:"destination_hint"`
	CreatedAt       time.Time  `json:"created_at"`
	LastUsedAt      *time.Time `json:"last_used_at,omitempty"`
}

type Recipient struct {
	Email       string
	DisplayName string
}

type Envelope struct {
	Ciphertext []byte
	Nonce      []byte
	KeyVersion int
}

type Challenge struct {
	ID, ResultMethodID string
	MethodID           string
	UserID             ids.UserID
	SessionID          ids.SessionID
	Purpose            Purpose
	Kind               Kind
	Destination        Envelope
	DestinationHash    [32]byte
	DestinationHint    string
	CodeHash           [32]byte
	ExpiresAt          time.Time
	CreatedAt          time.Time
	SMSConsent         *SMSConsentEvidence
}

type EnrollmentConsent struct {
	Accepted bool
	Version  string
}

type SMSConsentEvidence struct {
	Version    string
	Text       string
	CopySHA256 [sha256.Size]byte
	Source     string
	AcceptedAt time.Time
}

type Store interface {
	Recipient(context.Context, ids.UserID) (Recipient, error)
	CreateChallenge(context.Context, Challenge) error
	CancelChallenge(context.Context, string, ids.UserID, ids.SessionID) error
	CompleteChallenge(context.Context, string, ids.UserID, ids.SessionID, [32]byte, time.Time) (Method, Purpose, Kind, bool, error)
	Methods(context.Context, ids.UserID) ([]Method, error)
	MethodDestination(context.Context, ids.UserID, string) (Method, Envelope, error)
}

type Message struct {
	ID, Destination, DisplayName, Code string
	Kind                               Kind
	ExpiresAt                          time.Time
}

type Sender interface {
	SendMultifactor(context.Context, Message) error
}
type Clock interface{ Now() time.Time }

type Cipher struct {
	aead cipher.AEAD
	key  []byte
}

func NewCipher(master []byte) (*Cipher, error) {
	if len(master) != 32 {
		return nil, errors.New("multifactor encryption requires a 32-byte master key")
	}
	derived := hmac.New(sha256.New, master)
	_, _ = derived.Write([]byte("spyglass/multifactor/destination/v1"))
	key := derived.Sum(nil)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead, key: key}, nil
}

func (c *Cipher) Seal(id string, kind Kind, destination string) (Envelope, error) {
	if c == nil || id == "" || kind != KindSMS {
		return Envelope{}, ErrInvalidRequest
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Envelope{}, err
	}
	return Envelope{Ciphertext: c.aead.Seal(nil, nonce, []byte(destination), []byte("mfa/"+id+"/"+string(kind))), Nonce: nonce, KeyVersion: 1}, nil
}

func (c *Cipher) Open(id string, kind Kind, envelope Envelope) (string, error) {
	if c == nil || id == "" || kind != KindSMS || envelope.KeyVersion != 1 || len(envelope.Nonce) != c.aead.NonceSize() {
		return "", ErrInvalidRequest
	}
	value, err := c.aead.Open(nil, envelope.Nonce, envelope.Ciphertext, []byte("mfa/"+id+"/"+string(kind)))
	if err != nil {
		return "", ErrInvalidRequest
	}
	return string(value), nil
}

func (c *Cipher) digest(label, value string) [32]byte {
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write([]byte(label + "\x00" + value))
	var result [32]byte
	copy(result[:], mac.Sum(nil))
	return result
}

type Service struct {
	store                 Store
	sender                Sender
	sessions              *sessions.Service
	cipher                *Cipher
	ids                   ids.Generator
	clock                 Clock
	exposeDevelopmentCode bool
}

func NewService(store Store, sender Sender, sessionService *sessions.Service, destinationCipher *Cipher, generator ids.Generator, clock Clock, exposeDevelopmentCode bool) (*Service, error) {
	if store == nil || sender == nil || sessionService == nil || destinationCipher == nil || generator == nil || clock == nil {
		return nil, ErrInvalidRequest
	}
	return &Service{store: store, sender: sender, sessions: sessionService, cipher: destinationCipher, ids: generator, clock: clock, exposeDevelopmentCode: exposeDevelopmentCode}, nil
}

type BeginResult struct {
	ChallengeID     string    `json:"challenge_id"`
	Kind            Kind      `json:"kind"`
	DestinationHint string    `json:"destination_hint"`
	ExpiresAt       time.Time `json:"expires_at"`
	DevelopmentCode string    `json:"development_code,omitempty"`
}

func (s *Service) BeginEnrollment(ctx context.Context, session sessions.Session, kind Kind, phone string, consent EnrollmentConsent) (BeginResult, error) {
	if session.UserID == "" || session.ID == "" || (kind != KindSMS && kind != KindEmail) {
		return BeginResult{}, ErrInvalidRequest
	}
	recipient, err := s.store.Recipient(ctx, session.UserID)
	if err != nil {
		return BeginResult{}, err
	}
	destination := recipient.Email
	var evidence *SMSConsentEvidence
	if kind == KindSMS {
		if !consent.Accepted || consent.Version != SMSConsentVersion {
			return BeginResult{}, ErrInvalidRequest
		}
		destination, err = NormalizePhone(phone)
		if err != nil {
			return BeginResult{}, err
		}
		if !usPhonePattern.MatchString(destination) {
			return BeginResult{}, ErrInvalidRequest
		}
		evidence = &SMSConsentEvidence{Version: SMSConsentVersion, Text: SMSConsentText, CopySHA256: sha256.Sum256([]byte(SMSConsentText)), Source: "setup_sms_enrollment"}
	} else if consent.Accepted || consent.Version != "" {
		return BeginResult{}, ErrInvalidRequest
	}
	return s.begin(ctx, session, kind, PurposeEnrollment, "", destination, recipient.DisplayName, evidence)
}

func (s *Service) BeginReauthentication(ctx context.Context, session sessions.Session, methodID string) (BeginResult, error) {
	if session.UserID == "" || session.ID == "" || ids.Validate(methodID) != nil {
		return BeginResult{}, ErrInvalidRequest
	}
	method, envelope, err := s.store.MethodDestination(ctx, session.UserID, methodID)
	if err != nil {
		return BeginResult{}, err
	}
	recipient, err := s.store.Recipient(ctx, session.UserID)
	if err != nil {
		return BeginResult{}, err
	}
	destination := recipient.Email
	if method.Kind == KindSMS {
		destination, err = s.cipher.Open(method.ID, method.Kind, envelope)
		if err != nil {
			return BeginResult{}, err
		}
	}
	return s.begin(ctx, session, method.Kind, PurposeReauthentication, method.ID, destination, recipient.DisplayName, nil)
}

func (s *Service) begin(ctx context.Context, session sessions.Session, kind Kind, purpose Purpose, methodID, destination, displayName string, consent *SMSConsentEvidence) (BeginResult, error) {
	challengeID := s.ids.New()
	methodResultID := ""
	if purpose == PurposeEnrollment {
		methodResultID = s.ids.New()
	}
	code, err := randomCode()
	if err != nil {
		return BeginResult{}, err
	}
	now := s.clock.Now().UTC()
	if consent != nil {
		consent.AcceptedAt = now
	}
	hint := destinationHint(kind, destination)
	envelope := Envelope{}
	if kind == KindSMS {
		envelope, err = s.cipher.Seal(methodResultIDOrMethod(methodResultID, methodID), kind, destination)
		if err != nil {
			return BeginResult{}, err
		}
	}
	challenge := Challenge{ID: challengeID, ResultMethodID: methodResultID, MethodID: methodID, UserID: session.UserID, SessionID: session.ID, Purpose: purpose, Kind: kind, Destination: envelope, DestinationHash: s.cipher.digest("destination/"+string(kind), destination), DestinationHint: hint, CodeHash: s.cipher.digest("code/"+challengeID, code), CreatedAt: now, ExpiresAt: now.Add(codeTTL), SMSConsent: consent}
	if err := s.store.CreateChallenge(ctx, challenge); err != nil {
		return BeginResult{}, err
	}
	if err := s.sender.SendMultifactor(ctx, Message{ID: challengeID, Destination: destination, DisplayName: displayName, Code: code, Kind: kind, ExpiresAt: challenge.ExpiresAt}); err != nil {
		_ = s.store.CancelChallenge(ctx, challengeID, session.UserID, session.ID)
		return BeginResult{}, err
	}
	result := BeginResult{ChallengeID: challengeID, Kind: kind, DestinationHint: hint, ExpiresAt: challenge.ExpiresAt}
	if s.exposeDevelopmentCode {
		result.DevelopmentCode = code
	}
	return result, nil
}

func (s *Service) Complete(ctx context.Context, session sessions.Session, challengeID, code string) (Method, error) {
	if session.UserID == "" || session.ID == "" || ids.Validate(challengeID) != nil || !validCode(code) {
		return Method{}, ErrInvalidChallenge
	}
	hash := s.cipher.digest("code/"+challengeID, code)
	method, purpose, kind, matched, err := s.store.CompleteChallenge(ctx, challengeID, session.UserID, session.ID, hash, s.clock.Now().UTC())
	if err != nil {
		return Method{}, err
	}
	if !matched {
		return Method{}, ErrInvalidChallenge
	}
	methodName := sessions.AuthenticationMethodEmailOTP
	if kind == KindSMS {
		methodName = sessions.AuthenticationMethodSMSOTP
	}
	if err := s.sessions.MarkReauthenticatedWithMethod(ctx, session.UserID, session.ID, methodName); err != nil {
		return Method{}, err
	}
	if purpose == PurposeReauthentication && method.ID == "" {
		return Method{}, ErrInvalidChallenge
	}
	return method, nil
}

func (s *Service) Methods(ctx context.Context, userID ids.UserID) ([]Method, error) {
	if userID == "" {
		return nil, ErrInvalidRequest
	}
	return s.store.Methods(ctx, userID)
}

func NormalizePhone(value string) (string, error) {
	value = strings.TrimSpace(value)
	var cleaned strings.Builder
	for index, current := range value {
		if current >= '0' && current <= '9' {
			cleaned.WriteRune(current)
			continue
		}
		if current == '+' && index == 0 {
			cleaned.WriteRune(current)
			continue
		}
		if strings.ContainsRune(" ()-.\t", current) {
			continue
		}
		return "", ErrInvalidRequest
	}
	phone := cleaned.String()
	if len(phone) == 10 && phone[0] != '+' {
		phone = "+1" + phone
	}
	if len(phone) == 11 && strings.HasPrefix(phone, "1") {
		phone = "+" + phone
	}
	if !phonePattern.MatchString(phone) {
		return "", ErrInvalidRequest
	}
	return phone, nil
}

func destinationHint(kind Kind, destination string) string {
	if kind == KindSMS {
		if len(destination) < 4 {
			return "phone"
		}
		return "phone ending in " + destination[len(destination)-4:]
	}
	parts := strings.Split(destination, "@")
	if len(parts) != 2 || parts[0] == "" {
		return "verified email"
	}
	return parts[0][:1] + "***@" + parts[1]
}

func methodResultIDOrMethod(result, method string) string {
	if result != "" {
		return result
	}
	return method
}
func validCode(value string) bool {
	if len(value) != 6 {
		return false
	}
	for _, current := range value {
		if current < '0' || current > '9' {
			return false
		}
	}
	return true
}

func randomCode() (string, error) {
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	value := (uint32(raw[0])<<24 | uint32(raw[1])<<16 | uint32(raw[2])<<8 | uint32(raw[3])) % 1000000
	return fmt.Sprintf("%06d", value), nil
}

func EqualHash(left, right [32]byte) bool { return subtle.ConstantTimeCompare(left[:], right[:]) == 1 }
