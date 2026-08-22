package clamav

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
)

func TestClamAVStreamsCleanAndStandardTestSignature(t *testing.T) {
	address := os.Getenv("SPYGLASS_CLAMAV_TEST_ADDRESS")
	if address == "" {
		t.Skip("SPYGLASS_CLAMAV_TEST_ADDRESS is not configured")
	}
	client, err := New(Config{Address: address, OperationTimeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := client.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	clean := []byte("ordinary document text")
	result, err := client.Scan(ctx, knowledgeapp.MalwareScanRequest{Body: bytes.NewReader(clean), Size: int64(len(clean))})
	if err != nil || result.State != knowledgedomain.ScanClean || result.Engine == "" {
		t.Fatalf("clean result=%+v err=%v", result, err)
	}
	testSignature := []byte("X5O!P%@AP[4\\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*")
	result, err = client.Scan(ctx, knowledgeapp.MalwareScanRequest{Body: bytes.NewReader(testSignature), Size: int64(len(testSignature))})
	if err != nil || result.State != knowledgedomain.ScanInfected || result.Signature == "" {
		t.Fatalf("signature result=%+v err=%v", result, err)
	}
}
