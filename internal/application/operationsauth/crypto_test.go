package operationsauth

import (
	"bytes"
	"testing"
	"time"
)

func TestRFC6238SixDigitVectorsAndReplay(t *testing.T) {
	secret := []byte("12345678901234567890")
	for _, v := range []struct {
		seconds int64
		code    string
	}{{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"}, {1234567890, "005924"}, {2000000000, "279037"}, {20000000000, "353130"}} {
		now := time.Unix(v.seconds, 0)
		if got := Code(secret, v.seconds/30); got != v.code {
			t.Fatalf("vector %d: %s", v.seconds, got)
		}
		step, ok := Match(secret, v.code, now, -1)
		if !ok {
			t.Fatal("valid code denied")
		}
		if _, ok = Match(secret, v.code, now, step); ok {
			t.Fatal("replay accepted")
		}
		if _, ok = Match(secret, v.code, now.Add(2*time.Minute), -1); ok {
			t.Fatal("expired code accepted")
		}
	}
}
func TestHandoffBoundToBrowserAudienceAndLifetime(t *testing.T) {
	cipher, err := NewCipher(map[int][]byte{1: bytes.Repeat([]byte{1}, 32)}, 1)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := RandomToken()
	now := time.Now()
	ticket := Ticket{Identifier: "https://accounts.google.com\x1fsubject", State: state, Audience: "https://ops.test", ExpiresAt: now.Add(time.Minute)}
	raw, err := SealTicket(cipher, ticket)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = OpenTicket(cipher, raw, ticket.Audience, state, now); err != nil {
		t.Fatal(err)
	}
	other, _ := RandomToken()
	for _, v := range []struct {
		aud, state string
		now        time.Time
	}{{"https://evil.test", state, now}, {ticket.Audience, other, now}, {ticket.Audience, state, now.Add(time.Minute)}, {ticket.Audience, state, now.Add(-2 * time.Minute)}} {
		if _, err = OpenTicket(cipher, raw, v.aud, v.state, v.now); err == nil {
			t.Fatal("unbound ticket accepted")
		}
	}
	if _, err = OpenTicket(cipher, raw[:len(raw)-4]+"AAAA", ticket.Audience, state, now); err == nil {
		t.Fatal("tampered ticket accepted")
	}
}
