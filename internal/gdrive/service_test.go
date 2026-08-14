package gdrive

import "testing"

func TestCanonicalIDs(t *testing.T) {
	values := canonicalIDs([]string{" folder-b ", "folder-a", "folder-a", "bad\nfolder"}, 10)
	if len(values) != 2 || values[0] != "folder-a" || values[1] != "folder-b" {
		t.Fatalf("canonicalIDs() = %#v", values)
	}
}

func TestDisabledService(t *testing.T) {
	service := NewService(nil, [16]byte{}, nil, "", "", "")
	if service.Enabled() {
		t.Fatal("service should be disabled without OAuth settings")
	}
	if _, err := service.AuthorizationURL("state"); err != ErrNotConfigured {
		t.Fatalf("AuthorizationURL error = %v", err)
	}
}
