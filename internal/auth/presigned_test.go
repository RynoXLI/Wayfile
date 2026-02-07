package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestGenerateAndVerifyToken(t *testing.T) {
	signer := NewSigner("test-secret")
	namespaceUUID := uuid.New().String()
	docID := uuid.New().String()

	// Generate token
	token := signer.GenerateToken(namespaceUUID, docID, 1*time.Hour)

	// Verify token
	gotNs, gotID, err := signer.VerifyToken(token)
	if err != nil {
		t.Fatalf("VerifyToken failed: %v", err)
	}

	if gotNs != namespaceUUID {
		t.Errorf("expected namespace %q, got %q", namespaceUUID, gotNs)
	}

	if gotID != docID {
		t.Errorf("expected docID %q, got %q", docID, gotID)
	}
}

func TestTokenExpiration(t *testing.T) {
	signer := NewSigner("test-secret")
	namespaceUUID := uuid.New().String()
	docID := uuid.New().String()

	// Generate token that expires immediately
	token := signer.GenerateToken(namespaceUUID, docID, -1*time.Second)

	// Should fail due to expiration
	_, _, err := signer.VerifyToken(token)
	if err != ErrTokenExpired {
		t.Errorf("expected ErrTokenExpired, got %v", err)
	}
}

func TestInvalidSignature(t *testing.T) {
	signer := NewSigner("test-secret")
	namespaceUUID := uuid.New().String()
	docID := uuid.New().String()

	// Generate token
	token := signer.GenerateToken(namespaceUUID, docID, 1*time.Hour)

	// Tamper with the token
	parts := strings.Split(token, ".")
	parts[0] = "tampered"
	tamperedToken := strings.Join(parts, ".")

	// Should fail due to invalid signature
	_, _, err := signer.VerifyToken(tamperedToken)
	if err != ErrInvalidSignature {
		t.Errorf("expected ErrInvalidSignature, got %v", err)
	}
}

func TestInvalidTokenFormat(t *testing.T) {
	signer := NewSigner("test-secret")

	testCases := []string{
		"",
		"invalid",
		"only.two.parts",
		"too.many.parts.here.extra",
	}

	for _, tc := range testCases {
		_, _, err := signer.VerifyToken(tc)
		if err != ErrInvalidToken {
			t.Errorf("token %q: expected ErrInvalidToken, got %v", tc, err)
		}
	}
}

func TestDifferentSecrets(t *testing.T) {
	signer1 := NewSigner("secret-1")
	signer2 := NewSigner("secret-2")

	namespaceUUID := uuid.New().String()
	docID := uuid.New().String()

	// Generate token with signer1
	token := signer1.GenerateToken(namespaceUUID, docID, 1*time.Hour)

	// Try to verify with signer2 (different secret)
	_, _, err := signer2.VerifyToken(token)
	if err != ErrInvalidSignature {
		t.Errorf("expected ErrInvalidSignature when using different secret, got %v", err)
	}
}
