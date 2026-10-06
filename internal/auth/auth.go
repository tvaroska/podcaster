package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
)

// Role identifies the publisher authorization tier.
type Role string

const (
	RoleAdmin            Role = "admin"
	RoleDefaultSubmitter Role = "default_submitter"
	RolePodcastSubmitter Role = "podcast_submitter"
)

// Principal describes the authenticated caller on control-plane endpoints.
type Principal struct {
	Role      Role
	PodcastID string
}

type principalCtxKey struct{}

// WithPrincipal attaches p to ctx.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalCtxKey{}, p)
}

// PrincipalFrom extracts the Principal stored in ctx, if any.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	if ctx == nil {
		return Principal{}, false
	}
	p, ok := ctx.Value(principalCtxKey{}).(Principal)
	return p, ok
}

// FeedCreds authenticates podcast clients on the RSS feed and enclosure URLs.
type FeedCreds struct {
	Username string
	Password string
	Token    string
}

func constantTimeEqual(got, want string) bool {
	if want == "" {
		return false
	}
	gotHash := sha256.Sum256([]byte(got))
	wantHash := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(gotHash[:], wantHash[:]) == 1
}

// APIKeyMatch reports whether the Authorization bearer token matches the agent key.
func APIKeyMatch(got, want string) bool {
	return constantTimeEqual(got, want)
}

// Bearer extracts a bearer token from Authorization, or empty string.
func Bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

// CheckFeed reports whether r presents valid podcast-subscriber credentials.
// Accepted forms:
//   - HTTP Basic (username/password)
//   - ?token= query parameter matching FeedToken (or sha256(user:pass) if Token is empty)
func (c FeedCreds) CheckFeed(r *http.Request) bool {
	if user, pass, ok := r.BasicAuth(); ok {
		userOK := c.match(user, c.Username)
		passOK := c.match(pass, c.Password)
		if userOK && passOK {
			return true
		}
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		return false
	}
	return c.match(token, c.ExpectedToken())
}

// ExpectedToken is the query-parameter token podcast apps can embed in the feed URL.
func (c FeedCreds) ExpectedToken() string {
	if c.Token != "" {
		return c.Token
	}
	sum := sha256.Sum256([]byte(c.Username + ":" + c.Password))
	return hex.EncodeToString(sum[:16])
}

func (c FeedCreds) match(got, want string) bool {
	return constantTimeEqual(got, want)
}

// UnauthorizedFeed writes a 401 that podcast clients understand.
func UnauthorizedFeed(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="Private Podcast"`)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}
