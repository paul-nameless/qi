package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func sign(user string, secret []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(user))
	return hex.EncodeToString(m.Sum(nil))
}

func NewToken(user string, secret []byte) string {
	return user + "." + sign(user, secret)
}

// Verify returns the user name if the token is valid.
// TODO: reject expired auth tokens
func Verify(token string, secret []byte) (string, bool) {
	user, sig, ok := strings.Cut(token, ".")
	if !ok {
		return "", false
	}
	return user, hmac.Equal([]byte(sig), []byte(sign(user, secret)))
}
