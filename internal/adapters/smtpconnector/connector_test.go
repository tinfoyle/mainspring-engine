package smtpconnector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	stdsmtp "net/smtp"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationexecution"
	"github.com/tinfoyle/spyglass-engine/internal/application/integrationhealth"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	marketingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
)

type fakeSession struct {
	auth, mail, recipient, data, noop error
	writer                            *fakeWriter
	from                              string
	recipients                        []string
}

func (session *fakeSession) Auth(stdsmtp.Auth) error { return session.auth }
func (session *fakeSession) Mail(value string) error { session.from = value; return session.mail }
func (session *fakeSession) Rcpt(value string) error {
	session.recipients = append(session.recipients, value)
	return session.recipient
}
func (session *fakeSession) Data() (io.WriteCloser, error) {
	if session.writer == nil {
		session.writer = &fakeWriter{}
	}
	return session.writer, session.data
}
func (session *fakeSession) Noop() error { return session.noop }
func (*fakeSession) Quit() error         { return nil }
func (*fakeSession) Close() error        { return nil }

type fakeWriter struct {
	bytes.Buffer
	writeErr, closeErr error
}

func (writer *fakeWriter) Write(value []byte) (int, error) {
	if writer.writeErr != nil {
		return 0, writer.writeErr
	}
	return writer.Buffer.Write(value)
}
func (writer *fakeWriter) Close() error { return writer.closeErr }

func TestExecuteBindsScopeAndSendsDeterministicMessage(t *testing.T) {
	smtpSession := &fakeSession{}
	connector := &Connector{dial: func(context.Context, credential) (session, error) { return smtpSession, nil }}
	call := smtpCall(t)
	result := connector.Execute(context.Background(), call)
	if result.Outcome != domain.AttemptSucceeded || result.ErrorCode != "" {
		t.Fatalf("result=%+v", result)
	}
	message := smtpSession.writer.String()
	for _, expected := range []string{
		"From: \"Launch Team\" <launch@example.com>",
		"To: customer-a@example.com, customer-b@example.com",
		"Subject: Launch announcement",
		"Message-ID: <spyglass-e1500000-0000-4000-8000-000000000005@example.com>",
		"approved launch copy",
	} {
		if !strings.Contains(message, expected) {
			t.Fatalf("message missing %q: %s", expected, message)
		}
	}
	if smtpSession.from != "launch@example.com" || len(smtpSession.recipients) != 2 || smtpSession.recipients[0] != "customer-a@example.com" {
		t.Fatalf("from=%q recipients=%v", smtpSession.from, smtpSession.recipients)
	}
}

func TestExecuteClassifiesPreDataFailureAndPostDataUncertainty(t *testing.T) {
	tests := []struct {
		name     string
		session  *fakeSession
		want     domain.AttemptOutcome
		wantCode string
	}{
		{name: "recipient rejected", session: &fakeSession{recipient: errors.New("rejected")}, want: domain.AttemptFailed, wantCode: "smtp_recipient_rejected"},
		{name: "write uncertain", session: &fakeSession{writer: &fakeWriter{writeErr: errors.New("connection reset")}}, want: domain.AttemptUnknown, wantCode: "smtp_delivery_uncertain"},
		{name: "finish uncertain", session: &fakeSession{writer: &fakeWriter{closeErr: errors.New("connection reset")}}, want: domain.AttemptUnknown, wantCode: "smtp_delivery_uncertain"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			connector := &Connector{dial: func(context.Context, credential) (session, error) { return testCase.session, nil }}
			result := connector.Execute(context.Background(), smtpCall(t))
			if result.Outcome != testCase.want || result.ErrorCode != testCase.wantCode {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestExecuteRejectsCredentialPayloadAndScopeBeforeDial(t *testing.T) {
	call := smtpCall(t)
	called := false
	connector := &Connector{dial: func(context.Context, credential) (session, error) { called = true; return &fakeSession{}, nil }}
	call.Credential = append(call.Credential, []byte(` {}`)...)
	if result := connector.Execute(context.Background(), call); result.Outcome != domain.AttemptFailed || result.ErrorCode != "smtp_configuration_invalid" || called {
		t.Fatalf("invalid credential result=%+v called=%v", result, called)
	}
	call = smtpCall(t)
	var envelope integrationexecution.ProviderEnvelope
	if err := json.Unmarshal(call.Payload.ProviderPayload, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.Scope.EmailAddress = "other@example.com"
	call.Payload.ProviderPayload, _ = json.Marshal(envelope)
	if result := connector.Execute(context.Background(), call); result.Outcome != domain.AttemptFailed || result.ErrorCode != "smtp_scope_mismatch" || called {
		t.Fatalf("scope mismatch result=%+v called=%v", result, called)
	}
}

func TestReconcileNeverAuthorizesAutomaticSMTPRetry(t *testing.T) {
	result := New().Reconcile(context.Background(), smtpCall(t))
	if result.Outcome != domain.AttemptUnknown || result.ErrorCode != "smtp_reconciliation_unavailable" || result.RetryAt != nil {
		t.Fatalf("result=%+v", result)
	}
}

func TestProbeAuthenticatesWithoutStartingAMessage(t *testing.T) {
	smtpSession := &fakeSession{}
	connector := &Connector{dial: func(context.Context, credential) (session, error) { return smtpSession, nil }}
	call := smtpCall(t)
	result := connector.Probe(context.Background(), integrationhealth.ProbeCall{Claim: integrationhealth.Claim{
		ConnectorKind: domain.ConnectorEmail, CredentialProvider: ProviderCode,
		Scope: domain.ConnectionScope{EmailAddress: "launch@example.com", AudienceReference: "audience:customers-v1"},
	}, Credential: call.Credential})
	if result.State != domain.HealthHealthy || smtpSession.from != "" || len(smtpSession.recipients) != 0 || smtpSession.writer != nil {
		t.Fatalf("result=%+v from=%q recipients=%v writer=%v", result, smtpSession.from, smtpSession.recipients, smtpSession.writer)
	}
}

func smtpCall(t *testing.T) integrationexecution.ConnectorCall {
	t.Helper()
	claim := integrationexecution.Claim{ExecutionID: "e1500000-0000-4000-8000-000000000005", IdempotencyKey: "e1500000-0000-4000-8000-000000000005",
		Capability: domain.CapabilityEmailSend, CredentialProvider: ProviderCode}
	envelope := integrationexecution.ProviderEnvelope{Version: 1, Capability: domain.CapabilityEmailSend, IdempotencyKey: string(claim.IdempotencyKey),
		Scope: domain.ConnectionScope{EmailAddress: "launch@example.com", AudienceReference: "audience:customers-v1"},
		Assets: []integrationexecution.ProviderAsset{{ID: "e1700000-0000-4000-8000-000000000007", Kind: marketingdomain.AssetCopy,
			Title: "Launch announcement", MediaType: "text/plain", Content: []byte("approved launch copy")}}}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	credential := []byte(`{"version":1,"address":"smtp.example.com:465","server_name":"smtp.example.com","username":"mailer","password":"secret","from_address":"launch@example.com","from_name":"Launch Team","audience_reference":"audience:customers-v1","recipients":["customer-b@example.com","customer-a@example.com"]}`)
	return integrationexecution.ConnectorCall{Claim: claim, Payload: integrationexecution.Payload{ProviderPayload: payload}, Credential: credential,
		At: time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)}
}
