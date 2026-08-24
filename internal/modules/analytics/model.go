package analytics

import (
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type EventName string

const (
	LandingViewed              EventName = "landing_viewed"
	PrimaryCTASelected         EventName = "primary_cta_selected"
	FeatureViewed              EventName = "feature_viewed"
	PricingViewed              EventName = "pricing_viewed"
	OfferSelected              EventName = "offer_selected"
	SignupHandoffStarted       EventName = "signup_handoff_started"
	RegistrationStarted        EventName = "registration_started"
	VerificationCompleted      EventName = "verification_completed"
	AccountCreated             EventName = "account_created"
	SecurityEnrollmentComplete EventName = "security_enrollment_completed"
	CheckoutReviewed           EventName = "checkout_reviewed"
	ReferralCodeAccepted       EventName = "referral_code_accepted"
	CheckoutRedirected         EventName = "checkout_redirected"
	CheckoutReturned           EventName = "checkout_returned"
	SubscriptionProjected      EventName = "subscription_projected"
	ApplicationEntered         EventName = "application_entered"
	YourTurnOpened             EventName = "your_turn_opened"
	YourTurnItemCompleted      EventName = "your_turn_item_completed"
)

var (
	ErrUnknownEvent     = errors.New("analytics event is not registered")
	ErrInvalidEvent     = errors.New("analytics event is invalid")
	ErrConsentRequired  = errors.New("analytics consent is required")
	ErrProhibitedField  = errors.New("analytics event contains a prohibited field")
	ErrUnsupportedField = errors.New("analytics event contains an unsupported field")
)

type Definition struct {
	Name          EventName
	AllowedFields map[string]struct{}
}

type Registry struct{ definitions map[EventName]Definition }

type Envelope struct {
	ID         ids.AnalyticsEventID `json:"event_id"`
	SubjectID  ids.ConsentSubjectID `json:"subject_id"`
	Name       EventName            `json:"name"`
	Surface    privacy.Surface      `json:"surface"`
	OccurredAt time.Time            `json:"occurred_at"`
	Fields     map[string]string    `json:"fields,omitempty"`
}

var alwaysProhibited = map[string]struct{}{
	"account_id": {}, "user_id": {}, "email": {}, "name": {}, "ip": {}, "url": {}, "query": {}, "referrer": {},
	"stripe_id": {}, "prompt": {}, "answer": {}, "content": {}, "document": {}, "evidence": {}, "decision": {},
}

func LaunchRegistry() Registry {
	common := []string{"device_class", "locale", "route_name"}
	definitions := []struct {
		name   EventName
		fields []string
	}{
		{LandingViewed, common},
		{PrimaryCTASelected, append(common, "cta_code")},
		{FeatureViewed, append(common, "feature_code", "package_code")},
		{PricingViewed, common},
		{OfferSelected, append(common, "offer_code")},
		{SignupHandoffStarted, append(common, "offer_code", "campaign_code")},
		{RegistrationStarted, append(common, "offer_code")},
		{VerificationCompleted, common},
		{AccountCreated, common},
		{SecurityEnrollmentComplete, append(common, "method")},
		{CheckoutReviewed, append(common, "offer_code", "referral_present")},
		{ReferralCodeAccepted, append(common, "offer_code", "entry_method")},
		{CheckoutRedirected, append(common, "offer_code", "referral_present")},
		{CheckoutReturned, append(common, "offer_code", "result")},
		{SubscriptionProjected, append(common, "offer_code", "result")},
		{ApplicationEntered, append(common, "entry_point")},
		{YourTurnOpened, append(common, "queue_state")},
		{YourTurnItemCompleted, append(common, "task_category", "result", "duration_bucket")},
	}
	registry := Registry{definitions: make(map[EventName]Definition, len(definitions))}
	for _, item := range definitions {
		allowed := make(map[string]struct{}, len(item.fields))
		for _, field := range item.fields {
			allowed[field] = struct{}{}
		}
		registry.definitions[item.name] = Definition{Name: item.name, AllowedFields: allowed}
	}
	return registry
}

func (r Registry) Validate(envelope Envelope, decision privacy.Decision, now time.Time) error {
	definition, ok := r.definitions[envelope.Name]
	if !ok {
		return ErrUnknownEvent
	}
	if ids.Validate(string(envelope.ID)) != nil || ids.Validate(string(envelope.SubjectID)) != nil || envelope.SubjectID != decision.SubjectID || envelope.Surface != decision.Surface || envelope.OccurredAt.IsZero() {
		return ErrInvalidEvent
	}
	if !decision.Allows(privacy.CategoryAnalytics) || decision.EffectiveAt.After(envelope.OccurredAt) {
		return ErrConsentRequired
	}
	if envelope.OccurredAt.After(now.UTC().Add(5*time.Minute)) || envelope.OccurredAt.Before(now.UTC().Add(-24*time.Hour)) {
		return ErrInvalidEvent
	}
	if len(envelope.Fields) > 8 {
		return ErrInvalidEvent
	}
	for field, value := range envelope.Fields {
		if _, prohibited := alwaysProhibited[field]; prohibited {
			return ErrProhibitedField
		}
		if _, allowed := definition.AllowedFields[field]; !allowed {
			return ErrUnsupportedField
		}
		if !validDimension(value) {
			return ErrInvalidEvent
		}
	}
	return nil
}

func validDimension(value string) bool {
	if value == "" || len(value) > 80 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsLetter(character) || unicode.IsDigit(character) || character == '_' || character == '-' || character == '.' || character == '/' {
			continue
		}
		return false
	}
	return true
}
