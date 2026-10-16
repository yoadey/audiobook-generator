// Package auth signs users in with OpenID Connect (e.g. Zitadel) and keeps
// them signed in with an encrypted session cookie. Without an issuer
// configured, authentication is off and everyone is the anonymous user "".
package auth

import "context"

// User is a signed-in person.
type User struct {
	// Subject is the OIDC subject: the stable user ID.
	Subject string `json:"sub"`
	Name    string `json:"name"`
	Email   string `json:"email,omitempty"`
}

type userKey struct{}

// WithUser returns a context carrying the signed-in user.
func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, userKey{}, u)
}

// UserFrom returns the signed-in user (nil without authentication).
func UserFrom(ctx context.Context) *User {
	u, _ := ctx.Value(userKey{}).(*User)
	return u
}

// Subject returns the signed-in user's subject ("" without authentication):
// what per-user data (the listening position) is keyed by.
func Subject(ctx context.Context) string {
	if u := UserFrom(ctx); u != nil {
		return u.Subject
	}
	return ""
}
