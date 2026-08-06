package auth

import "testing"

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("a sufficiently long password")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(hash, "a sufficiently long password") {
		t.Fatal("expected password to match")
	}
	if CheckPassword(hash, "the wrong password") {
		t.Fatal("expected wrong password not to match")
	}
}

func TestPasswordMinimumLength(t *testing.T) {
	if _, err := HashPassword("too short"); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestCSRFToken(t *testing.T) {
	secret := []byte("a secret with enough entropy for testing")
	token := CSRFToken("session-token", secret)
	if !CheckCSRF("session-token", token, secret) {
		t.Fatal("expected CSRF token to verify")
	}
	if CheckCSRF("another-session", token, secret) {
		t.Fatal("expected CSRF token to be session-bound")
	}
}
