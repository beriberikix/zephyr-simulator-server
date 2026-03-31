package handlers

import (
	"context"
	"net/http"
	"regexp"
	"strings"
)

// contextKey is a private type to avoid collisions with other packages.
type contextKey int

const (
	identityCtxKey contextKey = iota
)

// OwnerType classifies the identity of a request caller.
type OwnerType string

const (
	OwnerTypeAnonymous OwnerType = "anonymous"
	OwnerTypeUser      OwnerType = "user"
)

// Identity carries resolved caller information injected into each request context.
type Identity struct {
	Type    OwnerType
	ID      string // anon UUID or PocketBase user record ID
	IsAdmin bool
}

// anonIDRe validates that a value is a UUID v4.
var anonIDRe = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// SetIdentity stores an Identity in the request context. Called by the
// PocketBase middleware in cmd/server after resolving the caller.
func SetIdentity(r *http.Request, id Identity) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), identityCtxKey, id))
}

// GetIdentity retrieves the Identity from a request context.
// Returns a zero-value Identity (anonymous, no ID) if none was set.
func GetIdentity(r *http.Request) Identity {
	id, _ := r.Context().Value(identityCtxKey).(Identity)
	return id
}

// IsValidAnonID reports whether s is a valid UUID v4 anonymous ID.
func IsValidAnonID(s string) bool {
	return anonIDRe.MatchString(strings.TrimSpace(s))
}
