package auth

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var key = []byte("test-key")

func token(t *testing.T, method jwt.SigningMethod, signKey any, c jwt.MapClaims) string {
	s, err := jwt.NewWithClaims(method, c).SignedString(signKey)
	require.NoError(t, err)
	return s
}

func authenticate(j JWT, tok string) (Identity, error) {
	r := httptest.NewRequest("GET", "/", nil)
	if tok != "" {
		r.Header.Set("Cookie", CookieName+"="+tok)
	}
	return j.Authenticate(r)
}

func TestJWT(t *testing.T) {
	j := JWT{Key: key, Realm: "tasks"}
	future := time.Now().Add(time.Hour).Unix()
	good := jwt.MapClaims{"email": "sam@example.com", "name": "Sam Smith", "picture": "https://p", "realm": "tasks", "exp": future}

	id, err := authenticate(j, token(t, jwt.SigningMethodHS512, key, good))
	require.NoError(t, err)
	assert.Equal(t, Identity{Email: "sam@example.com", Name: "Sam Smith", Picture: "https://p"}, id)

	with := func(k string, v any) jwt.MapClaims {
		c := jwt.MapClaims{}
		for kk, vv := range good {
			c[kk] = vv
		}
		if v == nil {
			delete(c, k)
		} else {
			c[k] = v
		}
		return c
	}
	bad := map[string]string{
		"no cookie":     "",
		"expired":       token(t, jwt.SigningMethodHS512, key, with("exp", time.Now().Add(-time.Minute).Unix())),
		"no expiry":     token(t, jwt.SigningMethodHS512, key, with("exp", nil)),
		"no email":      token(t, jwt.SigningMethodHS512, key, with("email", nil)),
		"wrong realm":   token(t, jwt.SigningMethodHS512, key, with("realm", "other")),
		"wrong key":     token(t, jwt.SigningMethodHS512, []byte("nope"), good),
		"alg none":      token(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, good),
		"garbage token": "not.a.jwt",
	}
	for name, tok := range bad {
		_, err := authenticate(j, tok)
		assert.ErrorIs(t, err, ErrNotLoggedIn, name)
	}
}
