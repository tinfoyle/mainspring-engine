// Package imapemail implements bounded, read-only inbound email capture over
// implicit TLS IMAP. It uses UIDVALIDITY plus UID identities, never mutates a
// mailbox, and returns only customer content to the canonical Knowledge sink.
package imapemail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationhealth"
	"github.com/tinfoyle/spyglass-engine/internal/application/integrationsync"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
)

const (
	ProviderCode              = "imap"
	maximumCursorBytes        = integrationsync.MaximumCursorBytes
	maximumTrackedMessages    = 1000
	maximumPartsPerMessage    = 8
	defaultPageMessages       = 4
	maximumPageMessages       = 10
	defaultMaximumMessageSize = int64(20 << 20)
	defaultMaximumPartSize    = int64(10 << 20)
)

var (
	ErrConfiguration = errors.New("IMAP email configuration is invalid")
	ErrCredential    = errors.New("IMAP email credential is invalid")
	ErrProvider      = errors.New("IMAP email provider is unavailable")
	ErrCursor        = errors.New("IMAP email cursor is invalid")
	ErrMessage       = errors.New("IMAP email message is invalid")
	ErrLimit         = errors.New("IMAP email bounded limit was exceeded")
)

type Config struct {
	RootCAFile          string
	DialTimeout         time.Duration
	PageMessages        int
	MaximumMessageBytes int64
	MaximumPartBytes    int64
}

type credentialDocument struct {
	Address  string `json:"address"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type mailboxInfo struct{ UIDValidity uint32 }

type messageInfo struct {
	UID          uint32
	InternalDate time.Time
	Size         int64
	Envelope     messageEnvelope
}

type messageEnvelope struct {
	Subject, From, To, Date, MessageID, InReplyTo, References string
}

type session interface {
	Select(context.Context, string) (mailboxInfo, error)
	Search(context.Context, *time.Time, *time.Time) ([]uint32, error)
	Metadata(context.Context, []uint32) ([]messageInfo, error)
	Raw(context.Context, uint32, int64) ([]byte, error)
	Close() error
}

type sessionDialer interface {
	Dial(context.Context, credentialDocument) (session, error)
}

type Provider struct {
	config Config
	dialer sessionDialer
}

func New(config Config) (*Provider, error) {
	config = normalizeConfig(config)
	dialer, err := newIMAPDialer(config)
	if err != nil {
		return nil, err
	}
	return newProvider(config, dialer)
}

func newProvider(config Config, dialer sessionDialer) (*Provider, error) {
	config = normalizeConfig(config)
	if dialer == nil || config.DialTimeout < time.Second || config.DialTimeout > time.Minute || config.PageMessages < 1 ||
		config.PageMessages > maximumPageMessages || config.MaximumMessageBytes < 1024 ||
		config.MaximumMessageBytes > integrationsync.MaximumChangeBytes || config.MaximumPartBytes < 1024 ||
		config.MaximumPartBytes > config.MaximumMessageBytes {
		return nil, ErrConfiguration
	}
	return &Provider{config: config, dialer: dialer}, nil
}

func normalizeConfig(config Config) Config {
	if config.DialTimeout == 0 {
		config.DialTimeout = 15 * time.Second
	}
	if config.PageMessages == 0 {
		config.PageMessages = defaultPageMessages
	}
	if config.MaximumMessageBytes == 0 {
		config.MaximumMessageBytes = defaultMaximumMessageSize
	}
	if config.MaximumPartBytes == 0 {
		config.MaximumPartBytes = defaultMaximumPartSize
	}
	return config
}

func (provider *Provider) Sync(ctx context.Context, request integrationsync.ProviderRequest) (integrationsync.ProviderPage, error) {
	if provider == nil || provider.dialer == nil || ctx == nil || ctx.Err() != nil || request.SourceKind != domain.ConnectorEmail ||
		request.CredentialProvider != ProviderCode || len(request.FolderIDs) == 0 || len(request.FolderIDs) > 20 ||
		len(request.Cursor) > maximumCursorBytes || (request.SinceAt != nil && request.UntilAt != nil && request.SinceAt.After(*request.UntilAt)) {
		return integrationsync.ProviderPage{}, ErrProvider
	}
	for index, folder := range request.FolderIDs {
		if !validMailbox(folder) || (index > 0 && request.FolderIDs[index-1] >= folder) {
			return integrationsync.ProviderPage{}, ErrProvider
		}
	}
	credential, err := parseCredential(request.Credential)
	if err != nil {
		return integrationsync.ProviderPage{}, err
	}
	defer credential.wipe()
	cursor, err := decodeCursor(request.Cursor, request.FolderIDs, request.SinceAt, request.UntilAt)
	if err != nil {
		return integrationsync.ProviderPage{}, err
	}
	mail, err := provider.dialer.Dial(ctx, credential)
	if err != nil {
		return integrationsync.ProviderPage{}, errors.Join(ErrProvider, err)
	}
	defer mail.Close()
	for folderIndex, folder := range request.FolderIDs {
		page, worked, err := provider.syncMailbox(ctx, mail, folder, &cursor.Mailboxes[folderIndex], request.SinceAt, request.UntilAt)
		if err != nil {
			wipeChanges(page.Changes)
			return integrationsync.ProviderPage{}, err
		}
		if !worked {
			continue
		}
		encoded, err := encodeCursor(cursor)
		if err != nil {
			wipeChanges(page.Changes)
			return integrationsync.ProviderPage{}, err
		}
		page.NextCursor = encoded
		return page, nil
	}
	encoded, err := encodeCursor(cursor)
	if err != nil {
		return integrationsync.ProviderPage{}, err
	}
	return integrationsync.ProviderPage{NextCursor: encoded}, nil
}

func (provider *Provider) syncMailbox(ctx context.Context, mail session, folder string, state *mailboxCursor,
	sinceAt, untilAt *time.Time) (integrationsync.ProviderPage, bool, error) {
	selected, err := mail.Select(ctx, folder)
	if err != nil || selected.UIDValidity == 0 {
		return integrationsync.ProviderPage{}, false, errors.Join(ErrProvider, err)
	}
	uids, err := mail.Search(ctx, sinceAt, untilAt)
	if err != nil {
		return integrationsync.ProviderPage{}, false, errors.Join(ErrProvider, err)
	}
	uids, err = canonicalUIDs(uids)
	if err != nil || len(uids) > maximumTrackedMessages {
		return integrationsync.ProviderPage{}, false, ErrLimit
	}
	metadata := map[uint32]messageInfo{}
	if sinceAt != nil || untilAt != nil {
		metadata, uids, err = exactDateUIDs(ctx, mail, uids, sinceAt, untilAt)
		if err != nil {
			return integrationsync.ProviderPage{}, false, err
		}
	}
	if state.UIDValidity == 0 {
		state.UIDValidity = selected.UIDValidity
	}
	if state.UIDValidity != selected.UIDValidity {
		page, remaining := deleteTracked(folder, state, provider.config.PageMessages)
		if len(state.Messages) == 0 {
			state.UIDValidity = selected.UIDValidity
		}
		page.HasMore = remaining || len(uids) > 0
		return page, true, nil
	}
	current := make(map[uint32]struct{}, len(uids))
	for _, uid := range uids {
		current[uid] = struct{}{}
	}
	page := integrationsync.ProviderPage{}
	for index := 0; index < len(state.Messages) && len(page.Changes) < provider.config.PageMessages*maximumPartsPerMessage; {
		message := state.Messages[index]
		if _, exists := current[message.UID]; exists {
			index++
			continue
		}
		page.Changes = append(page.Changes, deletionChanges(folder, state.UIDValidity, message)...)
		state.Messages = removeMessage(state.Messages, index)
	}
	if len(page.Changes) > 0 {
		page.HasMore = hasMissing(state.Messages, current) || hasUntracked(uids, state.Messages)
		return page, true, nil
	}
	processedMessages := 0
	for _, uid := range uids {
		if _, exists := findMessage(state.Messages, uid); exists {
			continue
		}
		info, exists := metadata[uid]
		if !exists {
			values, fetchErr := mail.Metadata(ctx, []uint32{uid})
			if fetchErr != nil || len(values) != 1 || values[0].UID != uid {
				return integrationsync.ProviderPage{}, false, errors.Join(ErrProvider, fetchErr)
			}
			info = values[0]
		}
		parts, parseErr := provider.messageParts(ctx, mail, info)
		if parseErr != nil {
			return integrationsync.ProviderPage{}, false, parseErr
		}
		for partIndex := range parts {
			page.Changes = append(page.Changes, sourceChange(folder, state.UIDValidity, uid, partIndex, parts[partIndex]))
		}
		state.Messages, err = insertMessage(state.Messages, messageCursor{UID: uid, PartCount: uint8(len(parts))})
		if err != nil {
			wipeChanges(page.Changes)
			return integrationsync.ProviderPage{}, false, ErrCursor
		}
		processedMessages++
		if processedMessages >= provider.config.PageMessages {
			break
		}
	}
	if processedMessages > 0 {
		page.HasMore = hasUntracked(uids, state.Messages)
		return page, true, nil
	}
	return integrationsync.ProviderPage{}, false, nil
}

func (provider *Provider) messageParts(ctx context.Context, mail session, info messageInfo) ([]capturedPart, error) {
	if info.Size < 0 {
		return nil, ErrMessage
	}
	if info.Size > provider.config.MaximumMessageBytes {
		return []capturedPart{rejectionManifest(info.Envelope, "message exceeded the configured byte limit")}, nil
	}
	raw, err := mail.Raw(ctx, info.UID, provider.config.MaximumMessageBytes)
	if err != nil {
		return nil, errors.Join(ErrProvider, err)
	}
	defer wipe(raw)
	if int64(len(raw)) > provider.config.MaximumMessageBytes {
		return []capturedPart{rejectionManifest(info.Envelope, "message exceeded the configured byte limit")}, nil
	}
	parts, err := parseMessage(raw, info.Envelope, provider.config.MaximumPartBytes)
	if err != nil {
		return []capturedPart{rejectionManifest(info.Envelope, "message MIME structure was rejected")}, nil
	}
	return parts, nil
}

func (provider *Provider) Probe(ctx context.Context, call integrationhealth.ProbeCall) integrationhealth.ProbeResult {
	if provider == nil || call.Claim.ConnectorKind != domain.ConnectorEmail || !containsCapability(call.Claim.Capabilities, domain.CapabilityEmailRead) ||
		call.Claim.CredentialProvider != ProviderCode || len(call.Credential) == 0 {
		return integrationhealth.ProbeResult{State: domain.HealthUnavailable, ErrorCode: "imap_health_scope_invalid"}
	}
	credential, err := parseCredential(call.Credential)
	if err != nil {
		return integrationhealth.ProbeResult{State: domain.HealthUnavailable, ErrorCode: "imap_credential_invalid"}
	}
	defer credential.wipe()
	mail, err := provider.dialer.Dial(ctx, credential)
	if err != nil {
		return integrationhealth.ProbeResult{State: domain.HealthUnavailable, ErrorCode: "imap_unavailable"}
	}
	defer mail.Close()
	return integrationhealth.ProbeResult{State: domain.HealthHealthy}
}

func parseCredential(raw []byte) (credentialDocument, error) {
	var credential credentialDocument
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) == 0 || len(raw) > 64<<10 || decoder.Decode(&credential) != nil {
		credential.wipe()
		return credentialDocument{}, ErrCredential
	}
	var trailing struct{}
	if decoder.Decode(&trailing) != io.EOF || !validCredentialText(credential.Username, 320) || !validCredentialText(credential.Password, 4096) {
		credential.wipe()
		return credentialDocument{}, ErrCredential
	}
	host, port, err := net.SplitHostPort(credential.Address)
	if err != nil || host == "" || port == "" || strings.ContainsAny(host, `/\\@`) {
		credential.wipe()
		return credentialDocument{}, ErrCredential
	}
	return credential, nil
}

func (credential *credentialDocument) wipe() {
	credential.Address = strings.Repeat("\x00", len(credential.Address))
	credential.Username = strings.Repeat("\x00", len(credential.Username))
	credential.Password = strings.Repeat("\x00", len(credential.Password))
	*credential = credentialDocument{}
}

func sourceChange(folder string, validity, uid uint32, partIndex int, part capturedPart) integrationsync.ProviderChange {
	objectID := fmt.Sprintf("mailbox-sha256:%s/uidvalidity:%d/uid:%d/part:%d", mailboxDigest(folder), validity, uid, partIndex)
	digest := sha256.Sum256(part.Content)
	return integrationsync.ProviderChange{FolderID: folder, ObjectID: objectID, RevisionID: "sha256:" + hex.EncodeToString(digest[:]),
		Title: part.Title, Filename: part.Filename, MediaType: part.MediaType, Content: part.Content}
}

func deletionChanges(folder string, validity uint32, message messageCursor) []integrationsync.ProviderChange {
	changes := make([]integrationsync.ProviderChange, int(message.PartCount))
	for partIndex := range changes {
		changes[partIndex] = integrationsync.ProviderChange{FolderID: folder,
			ObjectID:   fmt.Sprintf("mailbox-sha256:%s/uidvalidity:%d/uid:%d/part:%d", mailboxDigest(folder), validity, message.UID, partIndex),
			RevisionID: fmt.Sprintf("deleted:uidvalidity:%d:uid:%d", validity, message.UID), Deleted: true}
	}
	return changes
}

func mailboxDigest(folder string) string {
	digest := sha256.Sum256([]byte(folder))
	return hex.EncodeToString(digest[:])
}

func deleteTracked(folder string, state *mailboxCursor, maximumMessages int) (integrationsync.ProviderPage, bool) {
	page := integrationsync.ProviderPage{}
	count := maximumMessages
	if len(state.Messages) < count {
		count = len(state.Messages)
	}
	for _, message := range state.Messages[:count] {
		page.Changes = append(page.Changes, deletionChanges(folder, state.UIDValidity, message)...)
	}
	state.Messages = append([]messageCursor(nil), state.Messages[count:]...)
	return page, len(state.Messages) > 0
}

func exactDateUIDs(ctx context.Context, mail session, uids []uint32, sinceAt, untilAt *time.Time) (map[uint32]messageInfo, []uint32, error) {
	if len(uids) == 0 {
		return map[uint32]messageInfo{}, nil, nil
	}
	values, err := mail.Metadata(ctx, uids)
	if err != nil || len(values) != len(uids) {
		return nil, nil, errors.Join(ErrProvider, err)
	}
	metadata := make(map[uint32]messageInfo, len(values))
	filtered := make([]uint32, 0, len(values))
	for _, value := range values {
		if value.UID == 0 || value.InternalDate.IsZero() {
			return nil, nil, ErrProvider
		}
		at := value.InternalDate.UTC()
		if (sinceAt != nil && at.Before(sinceAt.UTC())) || (untilAt != nil && at.After(untilAt.UTC())) {
			continue
		}
		metadata[value.UID] = value
		filtered = append(filtered, value.UID)
	}
	filtered, err = canonicalUIDs(filtered)
	return metadata, filtered, err
}

func canonicalUIDs(values []uint32) ([]uint32, error) {
	result := append([]uint32(nil), values...)
	sort.Slice(result, func(left, right int) bool { return result[left] < result[right] })
	for index, value := range result {
		if value == 0 || (index > 0 && result[index-1] == value) {
			return nil, ErrProvider
		}
	}
	return result, nil
}

func hasMissing(messages []messageCursor, current map[uint32]struct{}) bool {
	for _, message := range messages {
		if _, exists := current[message.UID]; !exists {
			return true
		}
	}
	return false
}

func hasUntracked(uids []uint32, messages []messageCursor) bool {
	for _, uid := range uids {
		if _, exists := findMessage(messages, uid); !exists {
			return true
		}
	}
	return false
}

func validMailbox(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= 512 && utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func validCredentialText(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= maximum && utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func containsCapability(values []domain.Capability, target domain.Capability) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func wipe(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func wipeChanges(values []integrationsync.ProviderChange) {
	for index := range values {
		wipe(values[index].Content)
	}
}

var (
	_ integrationsync.Provider = (*Provider)(nil)
	_ integrationhealth.Probe  = (*Provider)(nil)
)
