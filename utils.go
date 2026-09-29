package go_librespot

import (
	"fmt"
	"net/http"
	"strings"
)

// HTTPExchangeEvidence describes a non-proto HTTP answer compactly for error
// messages: status, wire content headers, body length, and a short escaped
// prefix of the body. Auth error pages carry no secrets, but the prefix is
// capped at 128 bytes and %q-escaped so nothing token-like can leak into
// logs unquoted.
func HTTPExchangeEvidence(resp *http.Response, respBody []byte) string {
	evidence := fmt.Sprintf("status=%d content-type=%q content-encoding=%q content-length=%d",
		resp.StatusCode,
		resp.Header.Get("Content-Type"),
		resp.Header.Get("Content-Encoding"),
		len(respBody))
	if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
		evidence += fmt.Sprintf(" retry-after=%q", retryAfter)
	}
	if len(respBody) > 0 {
		evidence += fmt.Sprintf(" body=%.128q", string(respBody))
	}
	return evidence
}

func ObfuscateUsername(username string) string {
	if strings.Contains(username, "@") {
		parts := strings.SplitN(username, "@", 2)
		if len(parts) == 2 {
			return ObfuscateUsername(parts[0]) + "@" + parts[1]
		}
	}

	if len(username) < 5 {
		return username
	}

	return username[:2] + strings.Repeat("*", len(username)-4) + username[len(username)-2:]
}
