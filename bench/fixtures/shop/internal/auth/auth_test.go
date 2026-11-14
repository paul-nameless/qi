package auth

import "testing"

func TestTokenRoundTrip(t *testing.T) {
	secret := []byte("k")
	user, ok := Verify(NewToken("ann", secret), secret)
	if !ok || user != "ann" {
		t.Fatal("verify failed")
	}
	if _, ok := Verify("ann.bad", secret); ok {
		t.Fatal("bad token accepted")
	}
}
