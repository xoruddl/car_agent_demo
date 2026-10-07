// Package verifier는 매니페스트의 Ed25519 서명을 검증한다.
package verifier

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
)

// ErrSignatureInvalid는 공개키와 서명이 해시를 검증하지 못할 때 반환한다.
var ErrSignatureInvalid = errors.New("manifest signature is invalid")

// Verify는 소문자 hex SHA-256 해시와 Base64 Ed25519 서명을 공개키로 검증한다.
func Verify(publicKey ed25519.PublicKey, sha256Hex, signatureBase64 string) error {
	hash, err := hex.DecodeString(sha256Hex)
	if err != nil {
		return fmt.Errorf("decode sha256: %w", err)
	}
	if len(hash) != 32 {
		return fmt.Errorf("sha256 must decode to 32 bytes, got %d", len(hash))
	}
	signature, err := base64.StdEncoding.DecodeString(signatureBase64)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	if !ed25519.Verify(publicKey, hash, signature) {
		return ErrSignatureInvalid
	}
	return nil
}
