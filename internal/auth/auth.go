// Package auth identifies the user behind a request. In production Caddy
// (caddy-security) handles Google login and sets a signed JWT in the
// access_token cookie; see docs/design.md §6.1.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"

	"github.com/augustoroman/taskmaster/internal/store"
)

// Identity is who the identity provider says the user is.
type Identity struct {
	Email   string
	Name    string
	Picture string
}

// ErrNotLoggedIn means the request carries no valid credentials.
var ErrNotLoggedIn = errors.New("not logged in")

type Authenticator interface {
	Authenticate(r *http.Request) (Identity, error)
}

// CookieName is caddy-security's default token cookie.
const CookieName = "access_token"

// JWT verifies caddy-security tokens signed with a shared HMAC key.
type JWT struct {
	Key []byte
	// Realm, if set, must match the token's realm claim.
	Realm string
}

type claims struct {
	Email      string `json:"email"`
	Name       string `json:"name"`
	GivenName  string `json:"given_name"`
	Picture    string `json:"picture"`
	TokenRealm string `json:"realm"`
	jwt.RegisteredClaims
}

func (j JWT) Authenticate(r *http.Request) (Identity, error) {
	c, err := r.Cookie(CookieName)
	if err != nil || strings.TrimSpace(c.Value) == "" {
		return Identity{}, ErrNotLoggedIn
	}
	var cl claims
	_, err = jwt.ParseWithClaims(c.Value, &cl, func(*jwt.Token) (any, error) { return j.Key, nil },
		// Only HMAC: rejects "none" and algorithm-confusion attacks.
		jwt.WithValidMethods([]string{"HS256", "HS384", "HS512"}),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %v", ErrNotLoggedIn, err)
	}
	if cl.Email == "" {
		return Identity{}, fmt.Errorf("%w: token has no email", ErrNotLoggedIn)
	}
	if j.Realm != "" && cl.TokenRealm != j.Realm {
		return Identity{}, fmt.Errorf("%w: token is for realm %q", ErrNotLoggedIn, cl.TokenRealm)
	}
	return Identity{Email: cl.Email, Name: cl.Name, Picture: cl.Picture}, nil
}

// DevUser authenticates every request as one email. For local development
// only; main refuses to use it on a non-loopback address.
type DevUser struct{ Email string }

func (d DevUser) Authenticate(*http.Request) (Identity, error) {
	return Identity{Email: d.Email, Name: strings.Split(d.Email, "@")[0]}, nil
}

type ctxKey struct{}

func WithUser(ctx context.Context, u *store.User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

// UserFrom returns the logged-in user, or nil.
func UserFrom(ctx context.Context) *store.User {
	u, _ := ctx.Value(ctxKey{}).(*store.User)
	return u
}
