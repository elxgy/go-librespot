//go:build test_unit

package login5

import (
	"testing"
	"time"

	librespot "github.com/elxgy/go-librespot"
	pb "github.com/elxgy/go-librespot/proto/spotify/login5/v3"
)

func newTestLogin5() *Login5 {
	c := &Login5{log: &librespot.NullLogger{}}
	c.loginOk = &pb.LoginOk{AccessToken: "tok", Username: "user"}
	c.loginOkExp = time.Now().Add(time.Hour)
	return c
}

func TestCachedTokenValid(t *testing.T) {
	c := newTestLogin5()
	token, cached := c.cachedToken(false)
	if !cached || token != "tok" {
		t.Fatalf("cachedToken(false) = %q, %v; want tok, true", token, cached)
	}
}

func TestCachedTokenExpired(t *testing.T) {
	c := newTestLogin5()
	c.loginOkExp = time.Now().Add(-time.Minute)
	if _, cached := c.cachedToken(false); cached {
		t.Fatal("cachedToken returned a cached expired token")
	}
}

func TestCachedTokenForceCollapse(t *testing.T) {
	c := newTestLogin5()
	c.lastForcedRefresh = time.Now().Add(-100 * time.Millisecond)

	if _, cached := c.cachedToken(true); !cached {
		t.Fatal("forced refresh within minForcedRefreshInterval should reuse the token")
	}

	c.lastForcedRefresh = time.Now().Add(-minForcedRefreshInterval - time.Second)
	if _, cached := c.cachedToken(true); cached {
		t.Fatal("forced refresh outside minForcedRefreshInterval must not reuse the token")
	}
}
