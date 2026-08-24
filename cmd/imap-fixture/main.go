// Command imap-fixture serves a deterministic, local-only implicit-TLS IMAP
// mailbox. It is built only into the development fixture image.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

type config struct {
	address, username, password, certificateFile, keyFile, rootCAFile, serverName string
}

type literal struct {
	*bytes.Reader
	size int64
}

func (value literal) Size() int64 { return value.size }

func main() {
	configuration, err := configFromEnvironment()
	if err != nil {
		log.Fatal(err)
	}
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		if err := healthcheck(configuration); err != nil {
			log.Fatal("IMAP fixture is not ready")
		}
		return
	}
	if len(os.Args) != 1 {
		log.Fatal("imap-fixture accepts only the optional healthcheck command")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := serve(ctx, configuration); err != nil {
		log.Fatal(err)
	}
}

func configFromEnvironment() (config, error) {
	value := config{
		address:         envOr("IMAP_FIXTURE_ADDRESS", ":9993"),
		username:        strings.TrimSpace(os.Getenv("IMAP_FIXTURE_USERNAME")),
		password:        os.Getenv("IMAP_FIXTURE_PASSWORD"),
		certificateFile: envOr("IMAP_FIXTURE_CERTIFICATE_FILE", "/run/imap-fixture/tls.crt"),
		keyFile:         envOr("IMAP_FIXTURE_KEY_FILE", "/run/imap-fixture/tls.key"),
		rootCAFile:      envOr("IMAP_FIXTURE_ROOT_CA_FILE", "/run/imap-fixture/ca.crt"),
		serverName:      envOr("IMAP_FIXTURE_SERVER_NAME", "imap-fixture"),
	}
	if value.username == "" || value.password == "" || len(value.username) > 320 || len(value.password) > 4096 ||
		strings.TrimSpace(value.password) != value.password || strings.ContainsRune(value.username, '\x00') || strings.ContainsRune(value.password, '\x00') ||
		value.certificateFile == "" || value.keyFile == "" || value.rootCAFile == "" || value.serverName == "" {
		return config{}, errors.New("IMAP fixture configuration is invalid")
	}
	return value, nil
}

func serve(ctx context.Context, configuration config) error {
	certificate, err := tls.LoadX509KeyPair(configuration.certificateFile, configuration.keyFile)
	if err != nil {
		return fmt.Errorf("load IMAP fixture certificate: %w", err)
	}
	memory := imapmemserver.New()
	user := imapmemserver.NewUser(configuration.username, configuration.password)
	if err := user.Create("INBOX", nil); err != nil {
		return err
	}
	for index, message := range seedMessages() {
		reader := bytes.NewReader(message)
		if _, err := user.Append("INBOX", literal{Reader: reader, size: int64(len(message))}, &imap.AppendOptions{
			Time: time.Date(2026, 8, 24, 12+index, 0, 0, 0, time.UTC),
		}); err != nil {
			return fmt.Errorf("seed IMAP fixture: %w", err)
		}
	}
	memory.AddUser(user)
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	server := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return memory.NewSession(), nil, nil
		},
		TLSConfig: tlsConfig,
		Caps:      imap.CapSet{imap.CapIMAP4rev1: {}},
	})
	listener, err := tls.Listen("tcp", configuration.address, tlsConfig)
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		if err := server.Close(); err != nil {
			return err
		}
		return <-done
	case err := <-done:
		return err
	}
}

func healthcheck(configuration config) error {
	encoded, err := os.ReadFile(configuration.rootCAFile)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(encoded) {
		return errors.New("IMAP fixture root CA is invalid")
	}
	address := configuration.address
	if strings.HasPrefix(address, ":") {
		address = "127.0.0.1" + address
	}
	client, err := imapclient.DialTLS(address, &imapclient.Options{
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: configuration.serverName},
		Dialer:    &net.Dialer{Timeout: 3 * time.Second},
	})
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Login(configuration.username, configuration.password).Wait(); err != nil {
		return err
	}
	selected, err := client.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil || selected.NumMessages != uint32(len(seedMessages())) {
		return errors.New("IMAP fixture seed mailbox is unavailable")
	}
	return nil
}

func seedMessages() [][]byte {
	return [][]byte{
		[]byte("From: Client One <client.one@example.test>\r\nTo: Operations <operations@infiniteocean.test>\r\nSubject: Signed engagement brief\r\nDate: Mon, 24 Aug 2026 12:00:00 +0000\r\nMessage-ID: <brief-1@fixture.infiniteocean.test>\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nPlease review the attached engagement requirements before Thursday.\r\n"),
		[]byte("From: Client Two <client.two@example.test>\r\nTo: Operations <operations@infiniteocean.test>\r\nSubject: Follow-up with safe attachment\r\nDate: Mon, 24 Aug 2026 13:00:00 +0000\r\nMessage-ID: <follow-up-2@fixture.infiniteocean.test>\r\nIn-Reply-To: <brief-1@fixture.infiniteocean.test>\r\nReferences: <brief-1@fixture.infiniteocean.test>\r\nContent-Type: multipart/mixed; boundary=spyglass-fixture\r\n\r\n--spyglass-fixture\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nThe supporting notes are attached.\r\n--spyglass-fixture\r\nContent-Type: text/plain; name=\"supporting-notes.txt\"\r\nContent-Disposition: attachment; filename=\"supporting-notes.txt\"\r\n\r\nDeterministic local attachment content.\r\n--spyglass-fixture--\r\n"),
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
