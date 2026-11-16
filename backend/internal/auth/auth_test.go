package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const secret = "0123456789abcdef0123456789abcdef"

func TestTokenRoundTrip(t *testing.T) {
	tk, err := NewTokens(secret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	perms, _ := PermsForRole(RoleOperator)
	s, _, err := tk.Issue("alice", RoleOperator, perms)
	if err != nil {
		t.Fatal(err)
	}
	c, err := tk.Verify(s)
	if err != nil {
		t.Fatal(err)
	}
	if c.Username != "alice" || c.Role != RoleOperator || len(c.Perms) != len(perms) {
		t.Fatalf("claims = %+v", c)
	}
}

func TestShortSecretRejected(t *testing.T) {
	if _, err := NewTokens("short", time.Minute); err == nil {
		t.Fatal("short secret must be rejected")
	}
}

func TestExpiredToken(t *testing.T) {
	tk, _ := NewTokens(secret, -time.Minute) // already expired when issued
	s, _, _ := tk.Issue("alice", RoleViewer, nil)
	if _, err := tk.Verify(s); !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v, want ErrExpired", err)
	}
}

func TestWrongSecretAndNoneAlgRejected(t *testing.T) {
	a, _ := NewTokens(secret, time.Minute)
	b, _ := NewTokens("ffffffffffffffffffffffffffffffff", time.Minute)
	s, _, _ := a.Issue("alice", RoleViewer, nil)
	if _, err := b.Verify(s); err == nil {
		t.Fatal("token signed with another secret must fail")
	}

	none, err := jwt.NewWithClaims(jwt.SigningMethodNone, Claims{
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
		Username:         "mallory", Role: RoleOperator, Perms: []string{WriteRelay},
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Verify(none); err == nil {
		t.Fatal("alg=none token must be rejected")
	}
}

func TestUsers(t *testing.T) {
	u, err := NewUsers()
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Add("bob", "short", RoleViewer); err == nil {
		t.Fatal("short password must be rejected")
	}
	if err := u.Add("bob", "correct-horse", "root"); err == nil {
		t.Fatal("unknown role must be rejected")
	}
	if err := u.Add("bob", "correct-horse", RoleViewer); err != nil {
		t.Fatal(err)
	}
	if _, ok := u.Authenticate("bob", "correct-horse"); !ok {
		t.Fatal("valid credentials rejected")
	}
	if _, ok := u.Authenticate("bob", "wrong-password"); ok {
		t.Fatal("wrong password accepted")
	}
	if _, ok := u.Authenticate("nobody", "correct-horse"); ok {
		t.Fatal("unknown user accepted")
	}
}
