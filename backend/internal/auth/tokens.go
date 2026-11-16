// Package auth issues and verifies JWTs and guards routes by capability.
package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the token payload. Perms is the resolved capability list, so a
// request never needs a lookup to be authorized.
type Claims struct {
	jwt.RegisteredClaims
	Username string   `json:"username"`
	Role     string   `json:"role"`
	Perms    []string `json:"perms"`
}

// ErrExpired is returned by Verify for an otherwise valid but expired token,
// so the API can answer "token_expired" and the UI can route back to login.
var ErrExpired = errors.New("token expired")

// Tokens signs and verifies HS256 tokens with a secret from the environment.
type Tokens struct {
	secret []byte
	TTL    time.Duration
}

// NewTokens requires a secret of at least 32 bytes.
func NewTokens(secret string, ttl time.Duration) (*Tokens, error) {
	if len(secret) < 32 {
		return nil, fmt.Errorf("JWT secret must be at least 32 characters (got %d)", len(secret))
	}
	return &Tokens{secret: []byte(secret), TTL: ttl}, nil
}

// Issue creates a signed token and returns it with its expiry.
func (t *Tokens) Issue(username, role string, perms []string) (string, time.Time, error) {
	now := time.Now()
	exp := now.Add(t.TTL)
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   username,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
		Username: username,
		Role:     role,
		Perms:    perms,
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
	return s, exp, err
}

// Verify checks signature, algorithm (HS256 only) and expiry.
func (t *Tokens) Verify(token string) (*Claims, error) {
	var c Claims
	_, err := jwt.ParseWithClaims(token, &c,
		func(*jwt.Token) (any, error) { return t.secret, nil },
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithExpirationRequired(),
	)
	if errors.Is(err, jwt.ErrTokenExpired) {
		return nil, ErrExpired
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}
