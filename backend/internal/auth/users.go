package auth

import (
	"crypto/rand"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// MinPasswordLen is enforced when a user is added.
const MinPasswordLen = 8

// User is an authenticated account; only the bcrypt hash is kept.
type User struct {
	Username string
	Role     string
	hash     []byte
}

// Users is an in-memory account table, populated at startup from the environment.
type Users struct {
	byName map[string]*User
	dummy  []byte // compared against for unknown users, to keep timing uniform
}

// NewUsers creates an empty table.
func NewUsers() (*Users, error) {
	pw := make([]byte, 16)
	if _, err := rand.Read(pw); err != nil {
		return nil, err
	}
	dummy, err := bcrypt.GenerateFromPassword(pw, bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	return &Users{byName: map[string]*User{}, dummy: dummy}, nil
}

// Add registers a user, hashing the password with bcrypt.
func (u *Users) Add(username, password, role string) error {
	if username == "" {
		return errors.New("empty username")
	}
	if len(password) < MinPasswordLen {
		return fmt.Errorf("password for %q must be at least %d characters", username, MinPasswordLen)
	}
	if _, ok := PermsForRole(role); !ok {
		return fmt.Errorf("unknown role %q", role)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	u.byName[username] = &User{Username: username, Role: role, hash: h}
	return nil
}

// Authenticate verifies credentials. Unknown users cost the same as wrong passwords.
func (u *Users) Authenticate(username, password string) (*User, bool) {
	usr, ok := u.byName[username]
	if !ok {
		_ = bcrypt.CompareHashAndPassword(u.dummy, []byte(password))
		return nil, false
	}
	if bcrypt.CompareHashAndPassword(usr.hash, []byte(password)) != nil {
		return nil, false
	}
	return usr, true
}
