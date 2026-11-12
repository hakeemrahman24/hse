package session

import (
	"errors"
	"strings"
	"testing"

	"entry-permit/internal/dates"
)

const secret = "unit-test-secret"

func TestRoundTrip(t *testing.T) {
	tok := Sign(secret, 42, "2026-11-06 18:00:00")
	id, err := Verify(secret, tok, "2026-11-06 08:00:00")
	if err != nil || id != 42 {
		t.Fatalf("%d %v", id, err)
	}
}

func TestExpired(t *testing.T) {
	tok := Sign(secret, 1, "2026-11-06 18:00:00")
	for _, now := range []string{"2026-11-06 18:00:00", "2026-11-07 00:00:00"} { // at and after expiry
		if _, err := Verify(secret, tok, dates.Moment(now)); !errors.Is(err, ErrExpired) {
			t.Errorf("%s: %v", now, err)
		}
	}
}

func TestTamperAndGarbage(t *testing.T) {
	tok := Sign(secret, 1, "2026-11-06 18:00:00")
	if _, err := Verify("other-secret", tok, "2026-11-06 08:00:00"); !errors.Is(err, ErrInvalid) {
		t.Errorf("wrong secret: %v", err)
	}
	// Swap in another user's payload but keep the old signature.
	forged := Sign(secret, 2, "2026-11-06 18:00:00")
	mixed := strings.Split(forged, ".")[0] + "." + strings.Split(tok, ".")[1]
	if _, err := Verify(secret, mixed, "2026-11-06 08:00:00"); !errors.Is(err, ErrInvalid) {
		t.Errorf("forged: %v", err)
	}
	for _, bad := range []string{"", "nodot", "!!!.???", "abc.", ".abc"} {
		if _, err := Verify(secret, bad, "2026-11-06 08:00:00"); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestMalformedPayloadWithValidSignature(t *testing.T) {
	// A correctly signed but malformed payload must still be refused.
	for _, payload := range []string{"nopipe", "x|2026-11-06 18:00:00", "7|not-a-moment"} {
		tok := enc.EncodeToString([]byte(payload)) + "." + enc.EncodeToString(mac(secret, payload))
		if _, err := Verify(secret, tok, "2026-11-06 08:00:00"); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", payload, err)
		}
	}
}
