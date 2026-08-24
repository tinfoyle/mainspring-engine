package imapemail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationhealth"
	"github.com/tinfoyle/spyglass-engine/internal/application/integrationsync"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
)

type fakeDialer struct {
	session session
	err     error
	calls   int
}

func (dialer *fakeDialer) Dial(context.Context, credentialDocument) (session, error) {
	dialer.calls++
	return dialer.session, dialer.err
}

type fakeSession struct {
	uidValidity uint32
	uids        []uint32
	messages    map[uint32]messageInfo
	raw         map[uint32][]byte
	selectErr   error
	searchErr   error
	metadataErr error
	rawErr      error
	selected    []string
	closed      int
}

func (value *fakeSession) Select(_ context.Context, mailbox string) (mailboxInfo, error) {
	value.selected = append(value.selected, mailbox)
	return mailboxInfo{UIDValidity: value.uidValidity}, value.selectErr
}

func (value *fakeSession) Search(context.Context, *time.Time, *time.Time) ([]uint32, error) {
	return append([]uint32(nil), value.uids...), value.searchErr
}

func (value *fakeSession) Metadata(_ context.Context, uids []uint32) ([]messageInfo, error) {
	if value.metadataErr != nil {
		return nil, value.metadataErr
	}
	result := make([]messageInfo, 0, len(uids))
	for _, uid := range uids {
		if message, exists := value.messages[uid]; exists {
			result = append(result, message)
		}
	}
	return result, nil
}

func (value *fakeSession) Raw(_ context.Context, uid uint32, maximum int64) ([]byte, error) {
	if value.rawErr != nil {
		return nil, value.rawErr
	}
	result := append([]byte(nil), value.raw[uid]...)
	if int64(len(result)) > maximum+1 {
		result = result[:maximum+1]
	}
	return result, nil
}

func (value *fakeSession) Close() error { value.closed++; return nil }

func TestProviderCapturesMessagePartsAndRestartIsIdempotent(t *testing.T) {
	raw := multipartFixture("quarterly report", "notes.txt", "supporting detail")
	mail := &fakeSession{uidValidity: 77, uids: []uint32{9}, messages: map[uint32]messageInfo{
		9: fixtureInfo(9, raw, time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)),
	}, raw: map[uint32][]byte{9: raw}}
	provider := testProvider(t, mail)
	page, err := provider.Sync(context.Background(), testRequest(nil, nil, nil))
	if err != nil || page.HasMore || len(page.Changes) != 2 || len(page.NextCursor) == 0 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if page.Changes[0].ObjectID != "mailbox-sha256:"+mailboxDigest("INBOX")+"/uidvalidity:77/uid:9/part:0" || page.Changes[0].Filename != "message.txt" ||
		!strings.Contains(string(page.Changes[0].Content), "Subject: quarterly report") ||
		!strings.Contains(string(page.Changes[0].Content), "Message-ID: <message-9@example.test>") ||
		page.Changes[1].Filename != "notes.txt" || string(page.Changes[1].Content) != "supporting detail" {
		t.Fatalf("changes=%+v", page.Changes)
	}
	replayed, err := provider.Sync(context.Background(), testRequest(page.NextCursor, nil, nil))
	if err != nil || replayed.HasMore || len(replayed.Changes) != 0 || len(replayed.NextCursor) == 0 {
		t.Fatalf("replayed=%+v err=%v", replayed, err)
	}
	if !bytes.Equal(page.NextCursor, replayed.NextCursor) {
		t.Fatal("stable mailbox state changed its cursor")
	}
}

func TestProviderEmitsExactDeletionsForExpunge(t *testing.T) {
	raw := multipartFixture("expunge me", "record.pdf", "%PDF-fixture")
	mail := &fakeSession{uidValidity: 12, uids: []uint32{4}, messages: map[uint32]messageInfo{
		4: fixtureInfo(4, raw, time.Now().UTC()),
	}, raw: map[uint32][]byte{4: raw}}
	provider := testProvider(t, mail)
	initial, err := provider.Sync(context.Background(), testRequest(nil, nil, nil))
	if err != nil || len(initial.Changes) != 2 {
		t.Fatalf("initial=%+v err=%v", initial, err)
	}
	mail.uids = nil
	deleted, err := provider.Sync(context.Background(), testRequest(initial.NextCursor, nil, nil))
	if err != nil || len(deleted.Changes) != 2 || deleted.HasMore {
		t.Fatalf("deleted=%+v err=%v", deleted, err)
	}
	for index, change := range deleted.Changes {
		if !change.Deleted || change.ObjectID != fmt.Sprintf("mailbox-sha256:%s/uidvalidity:12/uid:4/part:%d", mailboxDigest("INBOX"), index) ||
			change.FolderID != "INBOX" || change.Title != "" || change.Filename != "" || len(change.Content) != 0 {
			t.Fatalf("deletion %d=%+v", index, change)
		}
	}
}

func TestProviderSettlesUIDValidityResetBeforeReadmitting(t *testing.T) {
	raw := plainFixture("replacement", "new content")
	mail := &fakeSession{uidValidity: 41, uids: []uint32{5}, messages: map[uint32]messageInfo{
		5: fixtureInfo(5, raw, time.Now().UTC()),
	}, raw: map[uint32][]byte{5: raw}}
	provider := testProvider(t, mail)
	initial, err := provider.Sync(context.Background(), testRequest(nil, nil, nil))
	if err != nil || len(initial.Changes) != 1 {
		t.Fatalf("initial=%+v err=%v", initial, err)
	}
	mail.uidValidity = 42
	reset, err := provider.Sync(context.Background(), testRequest(initial.NextCursor, nil, nil))
	if err != nil || len(reset.Changes) != 1 || !reset.Changes[0].Deleted || !reset.HasMore ||
		!strings.Contains(reset.Changes[0].ObjectID, "uidvalidity:41") {
		t.Fatalf("reset=%+v err=%v", reset, err)
	}
	readmitted, err := provider.Sync(context.Background(), testRequest(reset.NextCursor, nil, nil))
	if err != nil || len(readmitted.Changes) != 1 || readmitted.Changes[0].Deleted || readmitted.HasMore ||
		!strings.Contains(readmitted.Changes[0].ObjectID, "uidvalidity:42") {
		t.Fatalf("readmitted=%+v err=%v", readmitted, err)
	}
}

func TestProviderAppliesExactInternalDateBounds(t *testing.T) {
	before := plainFixture("before", "old")
	inside := plainFixture("inside", "current")
	after := plainFixture("after", "future")
	mail := &fakeSession{uidValidity: 8, uids: []uint32{1, 2, 3}, messages: map[uint32]messageInfo{
		1: fixtureInfo(1, before, time.Date(2026, 8, 24, 9, 59, 59, 0, time.UTC)),
		2: fixtureInfo(2, inside, time.Date(2026, 8, 24, 10, 30, 0, 0, time.UTC)),
		3: fixtureInfo(3, after, time.Date(2026, 8, 24, 11, 0, 1, 0, time.UTC)),
	}, raw: map[uint32][]byte{1: before, 2: inside, 3: after}}
	provider := testProvider(t, mail)
	since := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	until := time.Date(2026, 8, 24, 11, 0, 0, 0, time.UTC)
	page, err := provider.Sync(context.Background(), testRequest(nil, &since, &until))
	if err != nil || len(page.Changes) != 1 || !strings.Contains(page.Changes[0].ObjectID, "/uid:2/") {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}

func TestProviderRejectsExecutableAttachmentWithoutPersistingItsBytes(t *testing.T) {
	raw := multipartFixture("unsafe attachment", "payload.exe", "EVIL-BINARY-BYTES")
	mail := &fakeSession{uidValidity: 9, uids: []uint32{6}, messages: map[uint32]messageInfo{
		6: fixtureInfo(6, raw, time.Now().UTC()),
	}, raw: map[uint32][]byte{6: raw}}
	page, err := testProvider(t, mail).Sync(context.Background(), testRequest(nil, nil, nil))
	if err != nil || len(page.Changes) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	manifest := string(page.Changes[0].Content)
	if !strings.Contains(manifest, "payload.exe was not admitted") || strings.Contains(manifest, "EVIL-BINARY-BYTES") {
		t.Fatalf("manifest=%q", manifest)
	}
}

func TestProviderReturnsNoCursorOrChangesOnProviderFailure(t *testing.T) {
	dialer := &fakeDialer{err: errors.New("offline")}
	provider, err := newProvider(Config{}, dialer)
	if err != nil {
		t.Fatal(err)
	}
	page, err := provider.Sync(context.Background(), testRequest(nil, nil, nil))
	if !errors.Is(err, ErrProvider) || len(page.Changes) != 0 || len(page.NextCursor) != 0 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}

func TestProviderRejectsCredentialAndCursorDriftBeforeDial(t *testing.T) {
	dialer := &fakeDialer{session: &fakeSession{uidValidity: 1}}
	provider, err := newProvider(Config{}, dialer)
	if err != nil {
		t.Fatal(err)
	}
	request := testRequest(nil, nil, nil)
	request.Credential = append(request.Credential, []byte(` {}`)...)
	if _, err := provider.Sync(context.Background(), request); !errors.Is(err, ErrCredential) || dialer.calls != 0 {
		t.Fatalf("credential err=%v calls=%d", err, dialer.calls)
	}
	request = testRequest([]byte("not-a-cursor"), nil, nil)
	if _, err := provider.Sync(context.Background(), request); !errors.Is(err, ErrCursor) || dialer.calls != 0 {
		t.Fatalf("cursor err=%v calls=%d", err, dialer.calls)
	}
}

func TestProviderHealthAuthenticatesWithoutSelectingOrFetching(t *testing.T) {
	mail := &fakeSession{uidValidity: 1}
	dialer := &fakeDialer{session: mail}
	provider, err := newProvider(Config{}, dialer)
	if err != nil {
		t.Fatal(err)
	}
	result := provider.Probe(context.Background(), integrationhealth.ProbeCall{Claim: integrationhealth.Claim{
		ConnectorKind: domain.ConnectorEmail, CredentialProvider: ProviderCode,
		Capabilities: []domain.Capability{domain.CapabilityEmailRead},
	}, Credential: credentialFixture()})
	if result.State != domain.HealthHealthy || result.ErrorCode != "" || dialer.calls != 1 || len(mail.selected) != 0 || mail.closed != 1 {
		t.Fatalf("result=%+v calls=%d selected=%v closed=%d", result, dialer.calls, mail.selected, mail.closed)
	}
}

func testProvider(t *testing.T, mail session) *Provider {
	t.Helper()
	provider, err := newProvider(Config{PageMessages: 4}, &fakeDialer{session: mail})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func testRequest(cursor []byte, sinceAt, untilAt *time.Time) integrationsync.ProviderRequest {
	return integrationsync.ProviderRequest{CredentialProvider: ProviderCode, SourceKind: domain.ConnectorEmail,
		FolderIDs: []string{"INBOX"}, SinceAt: sinceAt, UntilAt: untilAt, Cursor: cursor, Credential: credentialFixture()}
}

func credentialFixture() []byte {
	return []byte(`{"address":"mail.example.test:993","username":"reader@example.test","password":"secret"}`)
}

func fixtureInfo(uid uint32, raw []byte, at time.Time) messageInfo {
	return messageInfo{UID: uid, InternalDate: at, Size: int64(len(raw)), Envelope: messageEnvelope{Subject: "fallback"}}
}

func plainFixture(subject, body string) []byte {
	return []byte(fmt.Sprintf("From: Sender <sender@example.test>\r\nTo: Reader <reader@example.test>\r\nSubject: %s\r\nDate: Mon, 24 Aug 2026 12:00:00 +0000\r\nMessage-ID: <message-9@example.test>\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s", subject, body))
}

func multipartFixture(subject, filename, attachment string) []byte {
	mediaType := "text/plain"
	if strings.HasSuffix(filename, ".pdf") {
		mediaType = "application/pdf"
	} else if strings.HasSuffix(filename, ".exe") {
		mediaType = "application/octet-stream"
	}
	return []byte(fmt.Sprintf("From: Sender <sender@example.test>\r\nTo: Reader <reader@example.test>\r\nSubject: %s\r\nDate: Mon, 24 Aug 2026 12:00:00 +0000\r\nMessage-ID: <message-9@example.test>\r\nIn-Reply-To: <parent@example.test>\r\nReferences: <root@example.test> <parent@example.test>\r\nContent-Type: multipart/mixed; boundary=fixture-boundary\r\n\r\n--fixture-boundary\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nmessage body\r\n--fixture-boundary\r\nContent-Type: %s; name=\"%s\"\r\nContent-Disposition: attachment; filename=\"%s\"\r\n\r\n%s\r\n--fixture-boundary--\r\n", subject, mediaType, filename, filename, attachment))
}
