//go:build test_unit

package login5

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	librespot "github.com/elxgy/go-librespot"
	pb "github.com/elxgy/go-librespot/proto/spotify/login5/v3"
	"google.golang.org/protobuf/proto"
)

func newExchangeTestLogin5(t *testing.T, handler http.HandlerFunc) (*Login5, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	serverURL, err := url.Parse(server.URL + "/")
	if err != nil {
		t.Fatalf("parsing test server URL: %v", err)
	}
	return &Login5{
		log:     &librespot.NullLogger{},
		baseUrl: serverURL,
		client:  server.Client(),
	}, &calls
}

func testLoginRequest(t *testing.T) []byte {
	t.Helper()
	body, err := proto.Marshal(&pb.LoginRequest{})
	if err != nil {
		t.Fatalf("marshalling test request: %v", err)
	}
	return body
}

func testLoginResponseBytes(t *testing.T) []byte {
	t.Helper()
	body, err := proto.Marshal(&pb.LoginResponse{Response: &pb.LoginResponse_Ok{Ok: &pb.LoginOk{Username: "user", AccessToken: "tok"}}})
	if err != nil {
		t.Fatalf("marshalling test response: %v", err)
	}
	return body
}

// An outage-style 500 with an HTML body must name the condition (status +
// body) instead of the bare historical "cannot parse invalid wire-format
// data", and must stop after the bounded attempts.
func TestRequestServerErrorNamesCondition(t *testing.T) {
	c, calls := newExchangeTestLogin5(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("<html>Service Unavailable</html>"))
	})

	_, err := c.request(context.Background(), &pb.LoginRequest{})
	if err == nil {
		t.Fatal("request against 500-HTML succeeded unexpectedly")
	}
	msg := err.Error()
	if !strings.Contains(msg, "500") || !strings.Contains(msg, "Service Unavailable") {
		t.Fatalf("error %q names neither the status nor the body", msg)
	}
	if got := calls.Load(); got != login5ExchangeAttempts {
		t.Fatalf("attempts = %d, want %d (bounded outage retry)", got, login5ExchangeAttempts)
	}
}

// A truncated proto body must carry the exchange evidence, not just the
// decoder complaint.
func TestRequestTruncatedBodyNamesExchange(t *testing.T) {
	full := testLoginResponseBytes(t)
	c, _ := newExchangeTestLogin5(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(full[:len(full)/2])
	})

	_, err := c.request(context.Background(), &pb.LoginRequest{})
	if err == nil {
		t.Fatal("request against truncated body succeeded unexpectedly")
	}
	msg := err.Error()
	if !strings.Contains(msg, "status=200") {
		t.Fatalf("error %q does not name the exchange status", msg)
	}
	if !strings.Contains(msg, "content-length=") {
		t.Fatalf("error %q does not name the body length", msg)
	}
}

// A gzip-encoded proto must decode through the shared default client path
// (pins that no custom transport breaks Go's automatic decompression — a
// gzip body at proto.Unmarshal is exactly the observed outage signature).
func TestRequestGzipEncodedProtoDecodes(t *testing.T) {
	full := testLoginResponseBytes(t)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(full); err != nil {
		t.Fatalf("gzipping test response: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("closing gzip writer: %v", err)
	}
	c, calls := newExchangeTestLogin5(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(buf.Bytes())
	})

	resp, err := c.request(context.Background(), &pb.LoginRequest{})
	if err != nil {
		t.Fatalf("request against gzip proto failed: %v", err)
	}
	if got := resp.GetOk().GetUsername(); got != "user" {
		t.Fatalf("decoded username = %q, want %q", got, "user")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("attempts = %d, want 1 (no retry on success)", got)
	}
}

// A 429 throttle is final: it must name the status and the retry window,
// and must not hammer the endpoint.
func TestRequestRateLimitIsFinal(t *testing.T) {
	c, calls := newExchangeTestLogin5(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "14886")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("slow down"))
	})

	_, err := c.request(context.Background(), &pb.LoginRequest{})
	if err == nil {
		t.Fatal("request against 429 succeeded unexpectedly")
	}
	msg := err.Error()
	if !strings.Contains(msg, "429") || !strings.Contains(msg, "14886") {
		t.Fatalf("error %q names neither the throttle nor its window", msg)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("attempts = %d, want 1 (a throttle must not be re-hit)", got)
	}
}

// A single blip then a healthy response must succeed — the retry heals a
// startup that would otherwise die on one bad answer.
func TestRequestRetriesOutageBlip(t *testing.T) {
	full := testLoginResponseBytes(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("blip"))
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(full)
	}))
	t.Cleanup(server.Close)
	serverURL, err := url.Parse(server.URL + "/")
	if err != nil {
		t.Fatalf("parsing test server URL: %v", err)
	}
	c := &Login5{log: &librespot.NullLogger{}, baseUrl: serverURL, client: server.Client()}

	resp, err := c.request(context.Background(), &pb.LoginRequest{})
	if err != nil {
		t.Fatalf("request after one blip failed: %v", err)
	}
	if got := resp.GetOk().GetUsername(); got != "user" {
		t.Fatalf("decoded username = %q, want %q", got, "user")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2 (one blip, one success)", got)
	}
}

// Cancellation during the exchange must surface promptly, not as a masked
// decoder error.
func TestRequestRespectsContext(t *testing.T) {
	c, _ := newExchangeTestLogin5(t, func(w http.ResponseWriter, r *http.Request) {
		// The client gives up at 100ms regardless of what the server does;
		// the short sleep (not the client's fate) only bounds Cleanup's
		// server.Close, which waits out outstanding handlers.
		select {
		case <-r.Context().Done():
		case <-time.After(300 * time.Millisecond):
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := c.request(ctx, &pb.LoginRequest{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline", err)
	}
}

// An empty 200 carries no proto but is not a decoder failure: it yields an
// empty response (the typed LoginError{0} surfaces one layer up in Login).
func TestRequestEmptyBodyYieldsEmptyResponse(t *testing.T) {
	c, _ := newExchangeTestLogin5(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	resp, err := c.request(context.Background(), &pb.LoginRequest{})
	if err != nil {
		t.Fatalf("request against empty 200 failed: %v", err)
	}
	if resp.GetOk() != nil || resp.GetError() != 0 {
		t.Fatalf("empty 200 yielded %+v, want empty response", resp)
	}
}
