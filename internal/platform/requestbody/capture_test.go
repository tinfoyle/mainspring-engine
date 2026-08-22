package requestbody

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"testing"
)

func TestCaptureReopensExactBodyAndRemovesFile(t *testing.T) {
	body := []byte("bounded routed document")
	capture, err := Read(bytes.NewReader(body), int64(len(body)))
	if err != nil || capture.Size() != int64(len(body)) || capture.SHA256() != sha256.Sum256(body) {
		t.Fatalf("capture=%+v err=%v", capture, err)
	}
	file, err := capture.Open()
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	actual, err := io.ReadAll(file)
	_ = file.Close()
	if err != nil || !bytes.Equal(actual, body) {
		t.Fatalf("actual=%q err=%v", actual, err)
	}
	if err := capture.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary body remains: %v", err)
	}
}

func TestCaptureRejectsBodyOverLimit(t *testing.T) {
	if _, err := Read(bytes.NewReader([]byte("oversized")), 4); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err=%v", err)
	}
}
