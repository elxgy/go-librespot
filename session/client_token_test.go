//go:build test_unit

package session

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// retrieveClientToken hardcodes its endpoint; the test client rewrites every
// request to the test server so production keeps a fixed, non-injectable URL.
func newExchangeTestClient(t *testing.T, handler http.HandlerFunc) *http.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parsing test server URL: %v", err)
	}
	client := server.Client()
	transport := client.Transport
	client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = serverURL.Scheme
		req.URL.Host = serverURL.Host
		return transport.RoundTrip(req)
	})
	return client
}

// An outage-style 503 with a text body must name the condition (status +
// body) instead of a bare status code.
func TestRetrieveClientTokenServerErrorNamesCondition(t *testing.T) {
	client := newExchangeTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("no healthy upstream"))
	})

	_, err := retrieveClientToken(client, "device", "client")
	if err == nil {
		t.Fatal("clienttoken request against 503 succeeded unexpectedly")
	}
	msg := err.Error()
	if !strings.Contains(msg, "503") || !strings.Contains(msg, "no healthy upstream") {
		t.Fatalf("error %q names neither the status nor the body", msg)
	}
}

// A 200 with a non-proto body must carry the exchange evidence, not just
// the decoder complaint.
func TestRetrieveClientTokenGarbageBodyNamesExchange(t *testing.T) {
	client := newExchangeTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>oops</html>"))
	})

	_, err := retrieveClientToken(client, "device", "client")
	if err == nil {
		t.Fatal("clienttoken request against garbage body succeeded unexpectedly")
	}
	msg := err.Error()
	if !strings.Contains(msg, "status=200") || !strings.Contains(msg, "<html>oops</html>") {
		t.Fatalf("error %q does not name the exchange: %q", msg, msg)
	}
}
