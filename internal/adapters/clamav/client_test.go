package clamav

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
)

const testVersion = "ClamAV 1.4.3/27700/Thu Aug 21 12:00:00 2026"

func TestClientScansExactStreamAsClean(t *testing.T) {
	payload := []byte("bounded document")
	address, wait := serve(t,
		versionHandler(testVersion+"\nCOMMANDS: VERSIONCOMMANDS INSTREAM\x00"),
		func(connection net.Conn) error {
			command, err := readTestRecord(connection)
			if err != nil || command != "zINSTREAM" {
				return fmt.Errorf("command = %q: %v", command, err)
			}
			stream, lengths, err := readTestStream(connection)
			if err != nil {
				return err
			}
			if !bytes.Equal(stream, payload) {
				return fmt.Errorf("stream = %q", stream)
			}
			expected := []uint32{3, 3, 3, 3, 3, 1}
			if fmt.Sprint(lengths) != fmt.Sprint(expected) {
				return fmt.Errorf("chunk lengths = %v", lengths)
			}
			_, err = connection.Write([]byte("stream: OK\x00"))
			return err
		},
	)
	client := newTestClient(t, address, Config{ChunkBytes: 3})
	if err := client.Verify(context.Background()); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	result, err := client.Scan(context.Background(), knowledgeapp.MalwareScanRequest{Body: bytes.NewReader(payload), Size: int64(len(payload))})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.State != knowledgedomain.ScanClean || result.Engine != testVersion || result.Signature != "" {
		t.Fatalf("result = %+v", result)
	}
	wait()
}

func TestClientParsesInfectedAndFailsClosed(t *testing.T) {
	tests := []struct {
		name      string
		reply     string
		state     knowledgedomain.ScanState
		signature string
		wantErr   error
	}{
		{name: "infected", reply: "stream: Eicar-Signature FOUND\x00", state: knowledgedomain.ScanInfected, signature: "Eicar-Signature"},
		{name: "daemon error", reply: "stream: INSTREAM size limit exceeded ERROR\x00", wantErr: ErrScan},
		{name: "unknown response", reply: "stream: MAYBE\x00", wantErr: ErrProtocol},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			address, wait := serve(t,
				versionHandler(testVersion+"\nCOMMANDS: INSTREAM VERSIONCOMMANDS\x00"),
				streamReplyHandler(test.reply),
			)
			client := newTestClient(t, address, Config{})
			result, err := client.Scan(context.Background(), knowledgeapp.MalwareScanRequest{Body: bytes.NewReader([]byte("x")), Size: 1})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
			if test.wantErr == nil && (result.State != test.state || result.Signature != test.signature || result.Engine != testVersion) {
				t.Fatalf("result = %+v", result)
			}
			wait()
		})
	}
}

func TestClientRejectsDeclaredSizeMismatch(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		size int64
	}{
		{name: "short", body: []byte("abc"), size: 4},
		{name: "long", body: []byte("abcde"), size: 4},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			address, wait := serve(t,
				versionHandler(testVersion+"\nCOMMANDS: INSTREAM VERSIONCOMMANDS\x00"),
				func(connection net.Conn) error {
					_, err := io.Copy(io.Discard, connection)
					return err
				},
			)
			client := newTestClient(t, address, Config{})
			_, err := client.Scan(context.Background(), knowledgeapp.MalwareScanRequest{Body: bytes.NewReader(test.body), Size: test.size})
			if !errors.Is(err, ErrInput) {
				t.Fatalf("error = %v", err)
			}
			wait()
		})
	}
}

func TestClientVerifyRejectsUnsupportedAndTimedOutDaemon(t *testing.T) {
	t.Run("missing instream", func(t *testing.T) {
		address, wait := serve(t, versionHandler(testVersion+"\nCOMMANDS: VERSIONCOMMANDS\x00"))
		client := newTestClient(t, address, Config{})
		if err := client.Verify(context.Background()); !errors.Is(err, ErrProtocol) {
			t.Fatalf("error = %v", err)
		}
		wait()
	})
	t.Run("deadline", func(t *testing.T) {
		address, wait := serve(t, func(connection net.Conn) error {
			if _, err := readTestRecord(connection); err != nil {
				return err
			}
			time.Sleep(75 * time.Millisecond)
			return nil
		})
		client := newTestClient(t, address, Config{OperationTimeout: 10 * time.Millisecond})
		if err := client.Verify(context.Background()); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("error = %v", err)
		}
		wait()
	})
}

func TestNewRejectsUnsafeConfiguration(t *testing.T) {
	tests := []Config{
		{},
		{Address: "clamav:3310", DialTimeout: -1},
		{Address: "clamav:3310", OperationTimeout: -1},
		{Address: "clamav:3310", MaximumBytes: knowledgedomain.MaximumDocumentBytes + 1},
		{Address: "clamav:3310", ChunkBytes: maximumChunkBytes + 1},
	}
	for index, config := range tests {
		if _, err := New(config); !errors.Is(err, ErrConfiguration) {
			t.Fatalf("case %d: error = %v", index, err)
		}
	}
}

func newTestClient(t *testing.T, address string, overrides Config) *Client {
	t.Helper()
	overrides.Address = address
	client, err := New(overrides)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

func versionHandler(reply string) func(net.Conn) error {
	return func(connection net.Conn) error {
		command, err := readTestRecord(connection)
		if err != nil {
			return err
		}
		if command != "zVERSIONCOMMANDS" {
			return fmt.Errorf("command = %q", command)
		}
		_, err = connection.Write([]byte(reply))
		return err
	}
}

func streamReplyHandler(reply string) func(net.Conn) error {
	return func(connection net.Conn) error {
		command, err := readTestRecord(connection)
		if err != nil || command != "zINSTREAM" {
			return fmt.Errorf("command = %q: %v", command, err)
		}
		if _, _, err := readTestStream(connection); err != nil {
			return err
		}
		_, err = connection.Write([]byte(reply))
		return err
	}
}

func readTestRecord(connection net.Conn) (string, error) {
	var result []byte
	for {
		var value [1]byte
		if _, err := io.ReadFull(connection, value[:]); err != nil {
			return "", err
		}
		if value[0] == 0 {
			return string(result), nil
		}
		result = append(result, value[0])
	}
}

func readTestStream(connection net.Conn) ([]byte, []uint32, error) {
	var result []byte
	var lengths []uint32
	for {
		var prefix [4]byte
		if _, err := io.ReadFull(connection, prefix[:]); err != nil {
			return nil, nil, err
		}
		length := binary.BigEndian.Uint32(prefix[:])
		if length == 0 {
			return result, lengths, nil
		}
		chunk := make([]byte, length)
		if _, err := io.ReadFull(connection, chunk); err != nil {
			return nil, nil, err
		}
		lengths = append(lengths, length)
		result = append(result, chunk...)
	}
}

func serve(t *testing.T, handlers ...func(net.Conn) error) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		defer listener.Close()
		for _, handler := range handlers {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				done <- acceptErr
				return
			}
			handlerErr := handler(connection)
			_ = connection.Close()
			if handlerErr != nil {
				done <- handlerErr
				return
			}
		}
		done <- nil
	}()
	t.Cleanup(func() { _ = listener.Close() })
	return listener.Addr().String(), func() {
		t.Helper()
		if serveErr := <-done; serveErr != nil {
			t.Fatalf("server: %v", serveErr)
		}
	}
}
