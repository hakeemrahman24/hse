// Package session makes and checks the login cookie value.
//
// WHY stateless and signed: the cookie carries "userID|expiry" and an HMAC of
// it, so nothing needs storing on the server and a changed cookie is
// rejected. The expiry is a Jakarta moment string like every other time here.
package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"

	"entry-permit/internal/dates"
)

// Errors from Verify. Callers treat both as "not logged in".
var (
	ErrInvalid = errors.New("invalid session")
	ErrExpired = errors.New("session expired")
)

var enc = base64.RawURLEncoding

func mac(secret, payload string) []byte {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(payload))
	return h.Sum(nil)
}

// Sign returns the cookie value for a user until expires.
func Sign(secret string, userID int64, expires dates.Moment) string {
	payload := strconv.FormatInt(userID, 10) + "|" + string(expires)
	return enc.EncodeToString([]byte(payload)) + "." + enc.EncodeToString(mac(secret, payload))
}

// Verify returns the user id if the token is genuine and not expired at now.
func Verify(secret, token string, now dates.Moment) (int64, error) {
	encPayload, encMac, ok := strings.Cut(token, ".")
	if !ok {
		return 0, ErrInvalid
	}
	payloadBytes, err := enc.DecodeString(encPayload)
	if err != nil {
		return 0, ErrInvalid
	}
	gotMac, err := enc.DecodeString(encMac)
	if err != nil {
		return 0, ErrInvalid
	}
	payload := string(payloadBytes)
	if !hmac.Equal(gotMac, mac(secret, payload)) {
		return 0, ErrInvalid
	}
	idText, expText, ok := strings.Cut(payload, "|")
	if !ok {
		return 0, ErrInvalid
	}
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil {
		return 0, ErrInvalid
	}
	expires, err := dates.ParseMoment(expText)
	if err != nil {
		return 0, ErrInvalid
	}
	if expires <= now {
		return 0, ErrExpired
	}
	return id, nil
}
