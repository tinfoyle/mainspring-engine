package imapemail

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/mail"
	"os"
	"strings"
	"sync"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

const maximumRootCABytes = int64(1 << 20)

type imapDialer struct {
	config Config
	roots  *x509.CertPool
}

type imapSession struct {
	client    *imapclient.Client
	closed    chan struct{}
	closeOnce sync.Once
}

func newIMAPDialer(config Config) (sessionDialer, error) {
	var roots *x509.CertPool
	if config.RootCAFile != "" {
		info, err := os.Lstat(config.RootCAFile)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > maximumRootCABytes {
			return nil, ErrConfiguration
		}
		encoded, err := os.ReadFile(config.RootCAFile)
		if err != nil {
			return nil, ErrConfiguration
		}
		defer wipe(encoded)
		roots, err = x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(encoded) {
			return nil, ErrConfiguration
		}
	}
	return &imapDialer{config: config, roots: roots}, nil
}

func (dialer *imapDialer) Dial(ctx context.Context, credential credentialDocument) (session, error) {
	if dialer == nil || ctx == nil || ctx.Err() != nil {
		return nil, ErrProvider
	}
	host, _, err := net.SplitHostPort(credential.Address)
	if err != nil || host == "" {
		return nil, ErrCredential
	}
	client, err := imapclient.DialTLS(credential.Address, &imapclient.Options{
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs:    dialer.roots,
			ServerName: host,
		},
		Dialer: &net.Dialer{Timeout: dialer.config.DialTimeout},
	})
	if err != nil {
		return nil, err
	}
	if err := client.Login(credential.Username, credential.Password).Wait(); err != nil {
		_ = client.Close()
		return nil, err
	}
	value := &imapSession{client: client, closed: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			_ = value.Close()
		case <-value.closed:
		}
	}()
	return value, nil
}

func (value *imapSession) Select(ctx context.Context, mailbox string) (mailboxInfo, error) {
	if err := value.ready(ctx); err != nil {
		return mailboxInfo{}, err
	}
	selected, err := value.client.Select(mailbox, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return mailboxInfo{}, err
	}
	return mailboxInfo{UIDValidity: selected.UIDValidity}, nil
}

func (value *imapSession) Search(ctx context.Context, sinceAt, untilAt *time.Time) ([]uint32, error) {
	if err := value.ready(ctx); err != nil {
		return nil, err
	}
	criteria := &imap.SearchCriteria{}
	if sinceAt != nil {
		criteria.Since = utcDay(*sinceAt)
	}
	if untilAt != nil {
		criteria.Before = utcDay(*untilAt).AddDate(0, 0, 1)
	}
	result, err := value.client.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, err
	}
	uids := result.AllUIDs()
	output := make([]uint32, len(uids))
	for index, uid := range uids {
		output[index] = uint32(uid)
	}
	return output, nil
}

func (value *imapSession) Metadata(ctx context.Context, uids []uint32) ([]messageInfo, error) {
	if err := value.ready(ctx); err != nil {
		return nil, err
	}
	set := make([]imap.UID, len(uids))
	for index, uid := range uids {
		if uid == 0 {
			return nil, ErrProvider
		}
		set[index] = imap.UID(uid)
	}
	if len(set) == 0 {
		return nil, nil
	}
	messages, err := value.client.Fetch(imap.UIDSetNum(set...), &imap.FetchOptions{
		UID: true, Envelope: true, InternalDate: true, RFC822Size: true,
	}).Collect()
	if err != nil {
		return nil, err
	}
	output := make([]messageInfo, 0, len(messages))
	for _, message := range messages {
		if message == nil || message.UID == 0 || message.Envelope == nil {
			return nil, ErrProvider
		}
		output = append(output, messageInfo{
			UID:          uint32(message.UID),
			InternalDate: message.InternalDate,
			Size:         message.RFC822Size,
			Envelope:     convertEnvelope(message.Envelope),
		})
	}
	return output, nil
}

func (value *imapSession) Raw(ctx context.Context, uid uint32, maximum int64) ([]byte, error) {
	if err := value.ready(ctx); err != nil || uid == 0 || maximum < 1 {
		return nil, ErrProvider
	}
	section := &imap.FetchItemBodySection{
		Partial: &imap.SectionPartial{Offset: 0, Size: maximum + 1},
		Peek:    true,
	}
	command := value.client.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{
		UID: true, BodySection: []*imap.FetchItemBodySection{section},
	})
	var output []byte
	matched := false
	for message := command.Next(); message != nil; message = command.Next() {
		messageUID := uint32(0)
		var body []byte
		bodySeen := false
		for item := message.Next(); item != nil; item = message.Next() {
			switch item := item.(type) {
			case imapclient.FetchItemDataUID:
				messageUID = uint32(item.UID)
			case imapclient.FetchItemDataBodySection:
				if bodySeen || item.Literal == nil || !item.MatchCommand(section) {
					continue
				}
				bodySeen = true
				var err error
				body, err = io.ReadAll(io.LimitReader(item.Literal, maximum+1))
				if err != nil {
					wipe(body)
					_ = command.Close()
					return nil, err
				}
			}
		}
		if messageUID == uid && bodySeen && !matched {
			output = body
			matched = true
		} else {
			wipe(body)
		}
	}
	if err := command.Close(); err != nil {
		wipe(output)
		return nil, err
	}
	if !matched {
		return nil, ErrProvider
	}
	return output, nil
}

func (value *imapSession) Close() error {
	if value == nil {
		return nil
	}
	var err error
	value.closeOnce.Do(func() {
		close(value.closed)
		if value.client != nil {
			err = value.client.Close()
		}
	})
	return err
}

func (value *imapSession) ready(ctx context.Context) error {
	if value == nil || value.client == nil || ctx == nil {
		return ErrProvider
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-value.closed:
		return ErrProvider
	default:
		return nil
	}
}

func utcDay(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func convertEnvelope(value *imap.Envelope) messageEnvelope {
	if value == nil {
		return messageEnvelope{}
	}
	date := ""
	if !value.Date.IsZero() {
		date = value.Date.Format(time.RFC1123Z)
	}
	return messageEnvelope{
		Subject:   value.Subject,
		From:      formatAddresses(value.From),
		To:        formatAddresses(value.To),
		Date:      date,
		MessageID: bracketMessageID(value.MessageID),
		InReplyTo: formatMessageIDs(value.InReplyTo),
	}
}

func formatAddresses(values []imap.Address) string {
	formatted := make([]string, 0, len(values))
	for index := range values {
		address := values[index].Addr()
		if address == "" {
			continue
		}
		formatted = append(formatted, (&mail.Address{Name: values[index].Name, Address: address}).String())
	}
	return strings.Join(formatted, ", ")
}

func formatMessageIDs(values []string) string {
	formatted := make([]string, 0, len(values))
	for _, value := range values {
		if value = bracketMessageID(value); value != "" {
			formatted = append(formatted, value)
		}
	}
	return strings.Join(formatted, " ")
}

func bracketMessageID(value string) string {
	value = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "<"), ">"))
	if value == "" || strings.ContainsAny(value, "<>\r\n\x00") {
		return ""
	}
	return fmt.Sprintf("<%s>", value)
}

var (
	_ sessionDialer = (*imapDialer)(nil)
	_ session       = (*imapSession)(nil)
)
