package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
)

type ctxKey struct{}

// ClaimsFrom returns the verified claims placed in the context by Guard.
func ClaimsFrom(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(ctxKey{}).(*Claims)
	return c, ok
}

func deny(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Bearer`)
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

// Guard authenticates the request and requires every listed capability.
// 401 = missing/invalid/expired token ("token_expired" lets the UI go back to
// login); 403 = valid token lacking a capability. With no capabilities listed,
// any valid token passes.
func Guard(t *Tokens, need ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || tok == "" {
				deny(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			claims, err := t.Verify(tok)
			if err != nil {
				if errors.Is(err, ErrExpired) {
					deny(w, http.StatusUnauthorized, "token_expired")
				} else {
					deny(w, http.StatusUnauthorized, "unauthorized")
				}
				return
			}
			for _, c := range need {
				if !slices.Contains(claims.Perms, c) {
					deny(w, http.StatusForbidden, "forbidden")
					return
				}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, claims)))
		})
	}
}
