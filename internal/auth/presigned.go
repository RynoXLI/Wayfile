// Package auth provides authentication and authorization utilities
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrInvalidToken is returned when a token is malformed
	ErrInvalidToken = errors.New("invalid token")
	// ErrTokenExpired is returned when a token has expired
	ErrTokenExpired = errors.New("token expired")
	// ErrInvalidSignature is returned when a token's signature is invalid
	ErrInvalidSignature = errors.New("invalid signature")
)

// Signer creates and verifies pre-signed tokens for URLs
type Signer struct {
	secret []byte
}

// NewSigner creates a new Signer with the given secret key
func NewSigner(secret string) *Signer {
	return &Signer{
		secret: []byte(secret),
	}
}

// GenerateToken creates a signed token for a resource with expiration
// Format: namespaceUUID.docID.expiresUnix.signature
// Uses namespace UUID to avoid delimiter conflicts and simplify token format
func (s *Signer) GenerateToken(namespaceUUID, docID string, ttl time.Duration) string {
	expiresAt := time.Now().Add(ttl).Unix()
	data := fmt.Sprintf("%s.%s.%d", namespaceUUID, docID, expiresAt)

	// Generate HMAC signature
	signature := s.sign(data)

	// Combine data and signature
	token := fmt.Sprintf("%s.%s", data, signature)
	return token
}

// VerifyToken validates a token and extracts namespace UUID and docID
// Returns namespaceUUID, docID, or error if invalid/expired
func (s *Signer) VerifyToken(token string) (namespaceUUID, docID string, err error) {
	// Split token into parts
	parts := strings.Split(token, ".")
	if len(parts) != 4 {
		return "", "", ErrInvalidToken
	}

	namespaceUUID = parts[0]
	docID = parts[1]
	expiresStr := parts[2]
	providedSig := parts[3]

	// Parse expiration
	expiresAt, err := strconv.ParseInt(expiresStr, 10, 64)
	if err != nil {
		return "", "", ErrInvalidToken
	}

	// Check expiration
	if time.Now().Unix() > expiresAt {
		return "", "", ErrTokenExpired
	}

	// Reconstruct data and verify signature
	data := fmt.Sprintf("%s.%s.%d", namespaceUUID, docID, expiresAt)
	expectedSig := s.sign(data)

	if !hmac.Equal([]byte(expectedSig), []byte(providedSig)) {
		return "", "", ErrInvalidSignature
	}

	return namespaceUUID, docID, nil
}

// sign creates an HMAC-SHA256 signature of the data
func (s *Signer) sign(data string) string {
	h := hmac.New(sha256.New, s.secret)
	h.Write([]byte(data))
	signature := h.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(signature)
}
