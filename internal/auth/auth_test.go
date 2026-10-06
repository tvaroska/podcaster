package auth

import (
	"context"
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
	if APIKeyMatch("s", "secret-with-longer-length") {
		t.Fatal("different lengths must fail")
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

	t.Run("basic wrong password", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/podcast.xml", nil)
		r.SetBasicAuth("podcast", "nope")
		if c.CheckFeed(r) {
			t.Fatal("wrong password should fail")
		}
	})

	t.Run("basic wrong username", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/podcast.xml", nil)
		r.SetBasicAuth("wrong-user", "s3cret")
		if c.CheckFeed(r) {
			t.Fatal("wrong username should fail")
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
	r2 := httptest.NewRequest(http.MethodPost, "/", nil)
	if Bearer(r2) != "" {
		t.Fatalf("expected empty bearer, got %q", Bearer(r2))
	}
}

func TestUnauthorizedFeed(t *testing.T) {
	rr := httptest.NewRecorder()
	UnauthorizedFeed(rr)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rr.Code)
	}
	if got := rr.Header().Get("WWW-Authenticate"); got == "" {
		t.Fatal("expected WWW-Authenticate header")
	}
}

func TestPrincipalContext(t *testing.T) {
	if _, ok := PrincipalFrom(context.Background()); ok {
		t.Fatal("expected no principal on empty context")
	}
	if _, ok := PrincipalFrom(nil); ok {
		t.Fatal("expected no principal on nil context")
	}

	want := Principal{Role: RolePodcastSubmitter, PodcastID: "alice"}
	ctx := WithPrincipal(context.Background(), want)
	got, ok := PrincipalFrom(ctx)
	if !ok {
		t.Fatal("expected principal in context")
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
