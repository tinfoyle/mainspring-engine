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
	Surface       privacy.Surface
	AllowedFields map[string]struct{}
	AllowedValues map[string]map[string]struct{}
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

// HandoffReference is a short-lived bearer that lets the application record
// anonymous conversion milestones against a public acquisition subject. It
// never contains or persists a private subject, User, or Account identifier.
type HandoffReference struct {
	ReceiptEventID ids.AnalyticsEventID
	SubjectID      ids.ConsentSubjectID
	ExpiresAt      time.Time
}

func (r HandoffReference) Validate(now time.Time) error {
	if ids.Validate(string(r.ReceiptEventID)) != nil || ids.Validate(string(r.SubjectID)) != nil ||
		r.ExpiresAt.IsZero() || !now.UTC().Before(r.ExpiresAt.UTC()) || r.ExpiresAt.After(now.UTC().Add(24*time.Hour)) {
		return ErrInvalidEvent
	}
	return nil
}

var alwaysProhibited = map[string]struct{}{
	"account_id": {}, "user_id": {}, "email": {}, "name": {}, "ip": {}, "url": {}, "query": {}, "referrer": {},
	"stripe_id": {}, "prompt": {}, "answer": {}, "content": {}, "document": {}, "evidence": {}, "decision": {},
}

func LaunchRegistry() Registry {
	common := []string{"device_class", "locale", "route_name"}
	publicEvents := map[EventName]struct{}{
		LandingViewed: {}, PrimaryCTASelected: {}, FeatureViewed: {}, PricingViewed: {}, OfferSelected: {}, SignupHandoffStarted: {},
	}
	enums := map[EventName]map[string][]string{
		SecurityEnrollmentComplete: {"method": {"passkey_recovery_codes"}},
		CheckoutReviewed:           {"referral_present": {"false", "true"}},
		ReferralCodeAccepted:       {"entry_method": {"link", "manual"}},
		CheckoutRedirected:         {"referral_present": {"false", "true"}},
		CheckoutReturned:           {"result": {"cancelled", "returned"}},
		SubscriptionProjected:      {"result": {"active", "attention", "failed"}},
		ApplicationEntered:         {"entry_point": {"checkout", "deep_link", "your_turn"}},
		YourTurnOpened:             {"queue_state": {"empty", "open"}},
		YourTurnItemCompleted: {
			"task_category":   {"action", "approval", "information", "review"},
			"result":          {"completed"},
			"duration_bucket": {"under_1m", "1m_5m", "over_5m"},
		},
	}
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
		allowedValues := map[string]map[string]struct{}{
			"device_class": valueSet("desktop", "phone", "tablet"),
		}
		for field, values := range enums[item.name] {
			allowedValues[field] = valueSet(values...)
		}
		surface := privacy.SurfacePrivate
		if _, public := publicEvents[item.name]; public {
			surface = privacy.SurfacePublic
		}
		registry.definitions[item.name] = Definition{Name: item.name, Surface: surface, AllowedFields: allowed, AllowedValues: allowedValues}
	}
	return registry
}

func valueSet(values ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func (r Registry) Validate(envelope Envelope, decision privacy.Decision, now time.Time) error {
	definition, ok := r.definitions[envelope.Name]
	if !ok {
		return ErrUnknownEvent
	}
	if ids.Validate(string(envelope.ID)) != nil || ids.Validate(string(envelope.SubjectID)) != nil || envelope.SubjectID != decision.SubjectID || envelope.Surface != decision.Surface || envelope.Surface != definition.Surface || envelope.OccurredAt.IsZero() {
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
		if allowedValues, constrained := definition.AllowedValues[field]; constrained {
			if _, allowed := allowedValues[value]; !allowed {
				return ErrInvalidEvent
			}
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
