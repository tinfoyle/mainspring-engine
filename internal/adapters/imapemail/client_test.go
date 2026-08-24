package imapemail

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

type literalFixture struct {
	*bytes.Reader
	size int64
}

func (value literalFixture) Size() int64 { return value.size }

type recordingSession struct {
	imapserver.Session
	readOnly chan<- bool
}

func (value *recordingSession) Select(mailbox string, options *imap.SelectOptions) (*imap.SelectData, error) {
	value.readOnly <- options != nil && options.ReadOnly
	return value.Session.Select(mailbox, options)
}

func TestImplicitTLSClientCompletesReadOnlyBoundedProtocol(t *testing.T) {
	certificate, rootCAFile := testServerCertificate(t)
	memory := imapmemserver.New()
	user := imapmemserver.NewUser("reader@example.test", "secret")
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	raw := plainFixture("transport certified", "body over an authenticated implicit TLS connection")
	reader := bytes.NewReader(raw)
	if _, err := user.Append("INBOX", literalFixture{Reader: reader, size: int64(len(raw))}, &imap.AppendOptions{
		Time: time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	memory.AddUser(user)
	readOnly := make(chan bool, 2)
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	server := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return &recordingSession{Session: memory.NewSession(), readOnly: readOnly}, nil, nil
		},
		TLSConfig: tlsConfig,
		Caps:      imap.CapSet{imap.CapIMAP4rev1: {}},
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(tls.NewListener(listener, tlsConfig)) }()
	t.Cleanup(func() {
		_ = server.Close()
		if err := <-serveDone; err != nil {
			t.Errorf("serve: %v", err)
		}
	})

	config := normalizeConfig(Config{RootCAFile: rootCAFile, DialTimeout: 2 * time.Second})
	dialer, err := newIMAPDialer(config)
	if err != nil {
		t.Fatal(err)
	}
	mail, err := dialer.Dial(context.Background(), credentialDocument{
		Address: listener.Addr().String(), Username: "reader@example.test", Password: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer mail.Close()
	selected, err := mail.Select(context.Background(), "INBOX")
	if err != nil || selected.UIDValidity == 0 {
		t.Fatalf("selected=%+v err=%v", selected, err)
	}
	if value := <-readOnly; !value {
		t.Fatal("client selected the mailbox mutably")
	}
	uids, err := mail.Search(context.Background(), nil, nil)
	if err != nil || len(uids) != 1 || uids[0] != 1 {
		t.Fatalf("uids=%v err=%v", uids, err)
	}
	metadata, err := mail.Metadata(context.Background(), uids)
	if err != nil || len(metadata) != 1 || metadata[0].UID != 1 || metadata[0].Size != int64(len(raw)) ||
		metadata[0].Envelope.Subject != "transport certified" {
		t.Fatalf("metadata=%+v err=%v", metadata, err)
	}
	partial, err := mail.Raw(context.Background(), 1, 63)
	if err != nil || len(partial) != 64 || !bytes.Equal(partial, raw[:64]) {
		t.Fatalf("partial length=%d err=%v", len(partial), err)
	}

	untrusted, err := newIMAPDialer(normalizeConfig(Config{DialTimeout: time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := untrusted.Dial(context.Background(), credentialDocument{
		Address: listener.Addr().String(), Username: "reader@example.test", Password: "secret",
	}); err == nil {
		t.Fatal("self-signed server was trusted without its explicit root CA")
	}
}

func TestIMAPDialerRejectsInvalidRootCAFiles(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.pem")
	if _, err := newIMAPDialer(normalizeConfig(Config{RootCAFile: missing})); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("missing CA error=%v", err)
	}
	invalid := filepath.Join(t.TempDir(), "invalid.pem")
	if err := os.WriteFile(invalid, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newIMAPDialer(normalizeConfig(Config{RootCAFile: invalid})); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("invalid CA error=%v", err)
	}
}

func testServerCertificate(t *testing.T) (tls.Certificate, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Spyglass local IMAP fixture"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	encoded, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate := tls.Certificate{Certificate: [][]byte{encoded}, PrivateKey: key}
	filename := filepath.Join(t.TempDir(), "imap-root-ca.pem")
	if err := os.WriteFile(filename, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: encoded}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certificate, filename
}
