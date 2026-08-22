package clamav

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
)

const (
	defaultDialTimeout      = 5 * time.Second
	defaultOperationTimeout = 2 * time.Minute
	defaultChunkBytes       = 64 << 10
	maximumChunkBytes       = 1 << 20
	maximumReplyBytes       = 8 << 10
)

var (
	ErrConfiguration = errors.New("ClamAV configuration is invalid")
	ErrUnavailable   = errors.New("ClamAV is unavailable")
	ErrProtocol      = errors.New("ClamAV protocol response is invalid")
	ErrInput         = errors.New("malware scan input is invalid")
	ErrScan          = errors.New("ClamAV could not scan the document")
)

type Config struct {
	Address          string
	DialTimeout      time.Duration
	OperationTimeout time.Duration
	MaximumBytes     int64
	ChunkBytes       int
}

type dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type Client struct {
	address          string
	dialer           dialer
	operationTimeout time.Duration
	maximumBytes     int64
	chunkBytes       int

	engineMu sync.RWMutex
	engine   string
}

func New(config Config) (*Client, error) {
	config.Address = strings.TrimSpace(config.Address)
	if config.DialTimeout == 0 {
		config.DialTimeout = defaultDialTimeout
	}
	if config.OperationTimeout == 0 {
		config.OperationTimeout = defaultOperationTimeout
	}
	if config.MaximumBytes == 0 {
		config.MaximumBytes = knowledgedomain.MaximumDocumentBytes
	}
	if config.ChunkBytes == 0 {
		config.ChunkBytes = defaultChunkBytes
	}
	if config.Address == "" || config.DialTimeout < 0 || config.OperationTimeout < 0 || config.MaximumBytes <= 0 || config.MaximumBytes > knowledgedomain.MaximumDocumentBytes || config.ChunkBytes <= 0 || config.ChunkBytes > maximumChunkBytes {
		return nil, ErrConfiguration
	}
	return &Client{address: config.Address, dialer: &net.Dialer{Timeout: config.DialTimeout}, operationTimeout: config.OperationTimeout, maximumBytes: config.MaximumBytes, chunkBytes: config.ChunkBytes}, nil
}

func (client *Client) Verify(ctx context.Context) error {
	engine, err := client.readVersion(ctx)
	if err != nil {
		return err
	}
	client.engineMu.Lock()
	client.engine = engine
	client.engineMu.Unlock()
	return nil
}

func (client *Client) Scan(ctx context.Context, request knowledgeapp.MalwareScanRequest) (knowledgeapp.MalwareScanResult, error) {
	if request.Body == nil || request.Size <= 0 || request.Size > client.maximumBytes {
		return knowledgeapp.MalwareScanResult{}, ErrInput
	}
	engine, err := client.engineIdentity(ctx)
	if err != nil {
		return knowledgeapp.MalwareScanResult{}, err
	}
	connection, finish, err := client.connect(ctx)
	if err != nil {
		return knowledgeapp.MalwareScanResult{}, err
	}
	defer finish()
	if err := writeFull(connection, []byte("zINSTREAM\x00")); err != nil {
		return knowledgeapp.MalwareScanResult{}, unavailable("write command", err)
	}
	buffer := make([]byte, client.chunkBytes)
	remaining := request.Size
	for remaining > 0 {
		length := int64(len(buffer))
		if remaining < length {
			length = remaining
		}
		chunk := buffer[:int(length)]
		if _, err := io.ReadFull(request.Body, chunk); err != nil {
			return knowledgeapp.MalwareScanResult{}, fmt.Errorf("%w: source ended before declared size: %v", ErrInput, err)
		}
		var prefix [4]byte
		binary.BigEndian.PutUint32(prefix[:], uint32(len(chunk)))
		if err := writeFull(connection, prefix[:]); err != nil {
			return knowledgeapp.MalwareScanResult{}, unavailable("write chunk length", err)
		}
		if err := writeFull(connection, chunk); err != nil {
			return knowledgeapp.MalwareScanResult{}, unavailable("write chunk", err)
		}
		remaining -= length
	}
	var extra [1]byte
	if count, readErr := io.ReadFull(request.Body, extra[:]); count != 0 || !errors.Is(readErr, io.EOF) {
		return knowledgeapp.MalwareScanResult{}, ErrInput
	}
	if err := writeFull(connection, []byte{0, 0, 0, 0}); err != nil {
		return knowledgeapp.MalwareScanResult{}, unavailable("finish stream", err)
	}
	reply, err := readRecord(connection)
	if err != nil {
		return knowledgeapp.MalwareScanResult{}, err
	}
	return parseScanReply(reply, engine)
}

func (client *Client) engineIdentity(ctx context.Context) (string, error) {
	client.engineMu.RLock()
	engine := client.engine
	client.engineMu.RUnlock()
	if engine != "" {
		return engine, nil
	}
	if err := client.Verify(ctx); err != nil {
		return "", err
	}
	client.engineMu.RLock()
	defer client.engineMu.RUnlock()
	return client.engine, nil
}

func (client *Client) readVersion(ctx context.Context) (string, error) {
	connection, finish, err := client.connect(ctx)
	if err != nil {
		return "", err
	}
	defer finish()
	if err := writeFull(connection, []byte("zVERSIONCOMMANDS\x00")); err != nil {
		return "", unavailable("write version command", err)
	}
	reply, err := readRecord(connection)
	if err != nil {
		return "", err
	}
	commandsAt := strings.Index(reply, "COMMANDS:")
	if commandsAt < 0 {
		return "", ErrProtocol
	}
	engine := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(reply[:commandsAt]), "|"))
	if !strings.HasPrefix(engine, "ClamAV ") || len(engine) > knowledgedomain.MaximumProcessorIdentity || strings.ContainsAny(engine, "\r\n\x00") || !commandListed(reply[commandsAt+len("COMMANDS:"):], "INSTREAM") {
		return "", ErrProtocol
	}
	return engine, nil
}

func (client *Client) connect(ctx context.Context) (net.Conn, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, unavailable("connect", err)
	}
	connection, err := client.dialer.DialContext(ctx, "tcp", client.address)
	if err != nil {
		return nil, nil, unavailable("connect", err)
	}
	deadline := time.Now().Add(client.operationTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		_ = connection.Close()
		return nil, nil, unavailable("set deadline", err)
	}
	stopCancellation := context.AfterFunc(ctx, func() { _ = connection.SetDeadline(time.Now()) })
	return connection, func() {
		stopCancellation()
		_ = connection.Close()
	}, nil
}

func readRecord(reader io.Reader) (string, error) {
	buffered := bufio.NewReaderSize(reader, 1024)
	result := make([]byte, 0, 256)
	for {
		fragment, err := buffered.ReadSlice(0)
		if len(result)+len(fragment) > maximumReplyBytes+1 {
			return "", ErrProtocol
		}
		result = append(result, fragment...)
		if err == nil {
			result = result[:len(result)-1]
			if len(result) == 0 || !utf8.Valid(result) {
				return "", ErrProtocol
			}
			return string(result), nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return "", unavailable("read response", err)
		}
	}
}

func parseScanReply(reply, engine string) (knowledgeapp.MalwareScanResult, error) {
	reply = strings.TrimSpace(reply)
	if reply == "stream: OK" {
		return knowledgeapp.MalwareScanResult{State: knowledgedomain.ScanClean, Engine: engine}, nil
	}
	const prefix, foundSuffix, errorSuffix = "stream: ", " FOUND", " ERROR"
	if strings.HasPrefix(reply, prefix) && strings.HasSuffix(reply, foundSuffix) {
		signature := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(reply, prefix), foundSuffix))
		if signature == "" || len(signature) > knowledgedomain.MaximumProcessorIdentity || strings.ContainsAny(signature, "\r\n\x00") {
			return knowledgeapp.MalwareScanResult{}, ErrProtocol
		}
		return knowledgeapp.MalwareScanResult{State: knowledgedomain.ScanInfected, Engine: engine, Signature: signature}, nil
	}
	if strings.HasPrefix(reply, prefix) && strings.HasSuffix(reply, errorSuffix) {
		return knowledgeapp.MalwareScanResult{}, fmt.Errorf("%w: %s", ErrScan, strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(reply, prefix), errorSuffix)))
	}
	return knowledgeapp.MalwareScanResult{}, ErrProtocol
}

func commandListed(reply, command string) bool {
	for _, candidate := range strings.Fields(reply) {
		if strings.Trim(candidate, ",|") == command {
			return true
		}
	}
	return false
}

func writeFull(writer io.Writer, value []byte) error {
	for len(value) > 0 {
		written, err := writer.Write(value)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrNoProgress
		}
		value = value[written:]
	}
	return nil
}

func unavailable(operation string, err error) error {
	return fmt.Errorf("%w: %s: %v", ErrUnavailable, operation, err)
}

var _ knowledgeapp.MalwareScanner = (*Client)(nil)
