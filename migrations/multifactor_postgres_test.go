package migrations_test

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/multifactor"
	"github.com/tinfoyle/spyglass-engine/internal/application/securityposture"
	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

type multifactorSender struct{ message multifactor.Message }

func (s *multifactorSender) SendMultifactor(_ context.Context, message multifactor.Message) error {
	s.message = message
	return nil
}

type sequenceIDs struct{ values []string }

func (g *sequenceIDs) New() string {
	value := g.values[0]
	g.values = g.values[1:]
	return value
}

func TestPostgresMultifactorEnrollmentAndStrongReauthentication(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := createDatabase(t, ctx, adminURL)
	defer cleanup()
	pool := openPool(t, ctx, databaseURL, nil)
	defer pool.Close()
	if _, err := migrations.Apply(ctx, pool, migrations.Global); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	userID := ids.UserID("10000000-0000-4000-8000-000000000001")
	sessionID := ids.SessionID("20000000-0000-4000-8000-000000000002")
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,primary_email,display_name,state,email_verified_at,security_version,created_at) VALUES ($1,'owner@example.com','Owner','active',$2,1,$2)`, userID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO sessions(id,user_id,token_hash,security_version,authenticated_at,last_seen_at,rotated_at,expires_at,reauthenticated_at,client_label,authentication_method,reauthentication_method) VALUES ($1,$2,$3,1,$4,$4,$4,$5,$4,'Test browser','password','password')`, sessionID, userID, bytes.Repeat([]byte{0x20}, 32), now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	sessionService, err := sessions.NewService(postgresadapter.NewSessionRepository(pool), &sequenceIDs{values: []string{"90000000-0000-4000-8000-000000000009"}}, fixedClock{now}, 24*time.Hour, time.Hour, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := multifactor.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	delivery := &multifactorSender{}
	service, err := multifactor.NewService(postgresadapter.NewMultifactorRepository(pool), delivery, sessionService, cipher, &sequenceIDs{values: []string{
		"30000000-0000-4000-8000-000000000003",
		"40000000-0000-4000-8000-000000000004",
		"50000000-0000-4000-8000-000000000005",
	}}, fixedClock{now}, true)
	if err != nil {
		t.Fatal(err)
	}
	session := sessions.Session{ID: sessionID, UserID: userID}
	started, err := service.BeginEnrollment(ctx, session, multifactor.KindSMS, "(202) 555-0199", multifactor.EnrollmentConsent{Accepted: true, Version: multifactor.SMSConsentVersion})
	if err != nil {
		t.Fatal(err)
	}
	var ciphertext, codeHash []byte
	if err := pool.QueryRow(ctx, `SELECT destination_ciphertext,code_hash FROM user_mfa_challenges WHERE id=$1`, started.ChallengeID).Scan(&ciphertext, &codeHash); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, []byte("+12025550199")) || bytes.Contains(codeHash, []byte(started.DevelopmentCode)) {
		t.Fatal("multifactor challenge persisted plaintext secret material")
	}
	var consentVersion, consentCopy, consentSource string
	var consentCopyHash, consentDestinationHash []byte
	var consentAcceptedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT consent_version,consent_copy,consent_copy_sha256,destination_fingerprint,source,accepted_at FROM sms_consent_receipts WHERE challenge_id=$1`, started.ChallengeID).Scan(&consentVersion, &consentCopy, &consentCopyHash, &consentDestinationHash, &consentSource, &consentAcceptedAt); err != nil {
		t.Fatal(err)
	}
	if consentVersion != multifactor.SMSConsentVersion || consentCopy != multifactor.SMSConsentText || consentSource != "setup_sms_enrollment" || len(consentCopyHash) != 32 || len(consentDestinationHash) != 32 || !consentAcceptedAt.Equal(now) || bytes.Contains(consentDestinationHash, []byte("+12025550199")) {
		t.Fatalf("unexpected SMS consent evidence version=%q source=%q copy=%x destination=%x accepted=%s", consentVersion, consentSource, consentCopyHash, consentDestinationHash, consentAcceptedAt)
	}
	method, err := service.Complete(ctx, session, started.ChallengeID, started.DevelopmentCode)
	if err != nil || method.Kind != multifactor.KindSMS {
		t.Fatalf("method=%+v err=%v", method, err)
	}
	postureService, _ := securityposture.NewService(postgresadapter.NewSecurityPostureRepository(pool))
	posture, err := postureService.Status(ctx, userID)
	if err != nil || !posture.OwnerReady || posture.MFAMethodCount != 1 {
		t.Fatalf("posture=%+v err=%v", posture, err)
	}

	started, err = service.BeginReauthentication(ctx, session, method.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Complete(ctx, session, started.ChallengeID, started.DevelopmentCode); err != nil {
		t.Fatal(err)
	}
	var reauthenticatedAt time.Time
	var reauthenticationMethod sessions.AuthenticationMethod
	if err := pool.QueryRow(ctx, `SELECT reauthenticated_at,reauthentication_method FROM sessions WHERE id=$1`, sessionID).Scan(&reauthenticatedAt, &reauthenticationMethod); err != nil {
		t.Fatal(err)
	}
	proof := sessions.Session{UserID: userID, ReauthenticatedAt: reauthenticatedAt, ReauthenticationMethod: reauthenticationMethod}
	if err := strongauth.Require(proof, userID, now); err != nil || reauthenticationMethod != sessions.AuthenticationMethodSMSOTP {
		t.Fatalf("proof=%+v err=%v", proof, err)
	}
	if _, err := service.Complete(ctx, session, started.ChallengeID, started.DevelopmentCode); err != multifactor.ErrInvalidChallenge {
		t.Fatalf("replayed challenge error=%v", err)
	}
}
