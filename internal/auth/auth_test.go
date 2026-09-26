package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIKeyMatch(t *testing.T) {
	if !APIKeyMatch("secret", "secret") {
		t.Fatal("expected match")
	}
	if APIKeyMatch("secret", "other") {
		t.Fatal("expected mismatch")
	}
	if APIKeyMatch("secret", "") {
		t.Fatal("empty want must fail")
	}
}

func TestFeedAuth(t *testing.T) {
	c := FeedCreds{Username: "podcast", Password: "s3cret", Token: "tok"}

	t.Run("basic", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/podcast.xml", nil)
		r.SetBasicAuth("podcast", "s3cret")
		if !c.CheckFeed(r) {
			t.Fatal("basic auth should pass")
		}
	})

	t.Run("basic wrong", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/podcast.xml", nil)
		r.SetBasicAuth("podcast", "nope")
		if c.CheckFeed(r) {
			t.Fatal("wrong password should fail")
		}
	})

	t.Run("token", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/podcast.xml?token=tok", nil)
		if !c.CheckFeed(r) {
			t.Fatal("token should pass")
		}
	})

	t.Run("derived token", func(t *testing.T) {
		c2 := FeedCreds{Username: "podcast", Password: "s3cret"}
		r := httptest.NewRequest(http.MethodGet, "/podcast.xml?token="+c2.ExpectedToken(), nil)
		if !c2.CheckFeed(r) {
			t.Fatal("derived token should pass")
		}
	})
}

func TestBearer(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set("Authorization", "Bearer abc")
	if Bearer(r) != "abc" {
		t.Fatalf("got %q", Bearer(r))
	}
}
