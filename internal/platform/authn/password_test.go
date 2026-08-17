package authn

import "testing"

func TestPasswordsRoundTripAndRejectsInvalidInput(t *testing.T) {
	passwords := Passwords{}
	encoded, err := passwords.Hash("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !passwords.Verify(encoded, "correct horse battery staple") {
		t.Fatal("valid password was rejected")
	}
	if passwords.Verify(encoded, "incorrect horse battery staple") {
		t.Fatal("invalid password was accepted")
	}
	if _, err := passwords.Hash("too-short"); err == nil {
		t.Fatal("expected password policy error")
	}
	if passwords.Verify("$argon2id$malformed", "anything") {
		t.Fatal("malformed hash was accepted")
	}
}
