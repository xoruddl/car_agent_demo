package verifier

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"testing"
)

func TestVerify(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	hash := sha256.Sum256([]byte("firmware"))
	signature := ed25519.Sign(privateKey, hash[:])
	if err := Verify(publicKey, hex.EncodeToString(hash[:]), base64.StdEncoding.EncodeToString(signature)); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := Verify(publicKey, hex.EncodeToString(hash[:]), base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte("other")))); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("Verify() error = %v, want ErrSignatureInvalid", err)
	}
}
