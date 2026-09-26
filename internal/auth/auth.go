package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
)

// FeedCreds authenticates podcast clients on the RSS feed and enclosure URLs.
type FeedCreds struct {
	Username string
	Password string
	Token    string
}

// APIKeyMatch reports whether the Authorization bearer token matches the agent key.
func APIKeyMatch(got, want string) bool {
	if want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
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
		if c.match(user, c.Username) && c.match(pass, c.Password) {
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
	if want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// UnauthorizedFeed writes a 401 that podcast clients understand.
func UnauthorizedFeed(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="Private Podcast"`)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}
