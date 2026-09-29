package login5

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	librespot "github.com/elxgy/go-librespot"
	pb "github.com/elxgy/go-librespot/proto/spotify/login5/v3"
	credentialspb "github.com/elxgy/go-librespot/proto/spotify/login5/v3/credentials"
	"google.golang.org/protobuf/proto"
)

type LoginError struct {
	Code pb.LoginError
}

func (e *LoginError) Error() string {
	return fmt.Sprintf("failed authenticating with login5: %v", e.Code)
}

type Login5 struct {
	log     librespot.Logger
	baseUrl *url.URL
	client  *http.Client

	deviceId    string
	clientToken string
	clientId    string

	loginOk           *pb.LoginOk
	loginOkExp        time.Time
	loginOkLock       sync.RWMutex
	refreshMu         sync.Mutex
	lastForcedRefresh time.Time
}

func NewLogin5(log librespot.Logger, client *http.Client, deviceId, clientToken, clientId string) *Login5 {
	baseUrl, err := url.Parse("https://login5.spotify.com/")
	if err != nil {
		panic("invalid login5 base URL")
	}
	if clientId == "" {
		clientId = librespot.ClientIdHex
	}
	return &Login5{
		log:         log,
		baseUrl:     baseUrl,
		client:      client,
		deviceId:    deviceId,
		clientToken: clientToken,
		clientId:    clientId,
	}
}

func (c *Login5) request(ctx context.Context, req *pb.LoginRequest) (*pb.LoginResponse, error) {
	body, err := proto.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed marshalling LoginRequest: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= login5ExchangeAttempts; attempt++ {
		resp, retryable, err := c.exchangeOnce(ctx, body)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !retryable || attempt == login5ExchangeAttempts {
			return nil, err
		}
		backoff := time.NewTimer(login5RetryBackoff(attempt))
		select {
		case <-ctx.Done():
			backoff.Stop()
			return nil, fmt.Errorf("login5 exchange aborted: %w", ctx.Err())
		case <-backoff.C:
		}
	}
	return nil, lastErr
}

// login5ExchangeAttempts bounds the retry loop for transient exchange
// failures (unreachable endpoint, 5xx, or an unparseable body — all of
// which an outage produces). Client rejections (4xx, including 429) are
// final: retrying a rejection — or hammering a throttle — cannot heal it.
const login5ExchangeAttempts = 3

// login5RetryBackoff spaces exchange retries. Bounded and short: this runs
// inside the single-flight token refresh, so it must not park callers.
func login5RetryBackoff(attempt int) time.Duration {
	return time.Duration(attempt) * 500 * time.Millisecond
}

func (c *Login5) exchangeOnce(ctx context.Context, body []byte) (*pb.LoginResponse, bool, error) {
	httpReq := &http.Request{
		Method: "POST",
		URL:    c.baseUrl.JoinPath("/v3/login"),
		Header: http.Header{
			"Accept":       []string{"application/x-protobuf"},
			"User-Agent":   []string{librespot.UserAgent()},
			"Client-Token": []string{c.clientToken},
		},
		Body: io.NopCloser(bytes.NewReader(body)),
	}

	resp, err := c.client.Do(httpReq.WithContext(ctx))
	if err != nil {
		return nil, true, fmt.Errorf("failed requesting login5: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, fmt.Errorf("failed reading login5 response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		retryable := resp.StatusCode >= 500 && resp.StatusCode < 600
		return nil, retryable, fmt.Errorf("login5 exchange rejected: %s", responseEvidence(resp, respBody))
	}

	var protoResp pb.LoginResponse
	if err := proto.Unmarshal(respBody, &protoResp); err != nil {
		return nil, true, fmt.Errorf("failed decoding login5 response: %s: %w", responseEvidence(resp, respBody), err)
	}

	return &protoResp, false, nil
}

// responseEvidence describes a non-proto HTTP answer compactly for error
// messages: status, wire content headers, body length, and a short escaped
// prefix of the body. Auth error pages carry no secrets, but the prefix is
// capped at 128 bytes and %q-escaped so nothing token-like can leak into
// logs unquoted.
func responseEvidence(resp *http.Response, respBody []byte) string {
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

func (c *Login5) Login(ctx context.Context, credentials proto.Message) error {
	c.loginOkLock.Lock()
	defer c.loginOkLock.Unlock()

	req := &pb.LoginRequest{
		ClientInfo: &pb.ClientInfo{
			ClientId: c.clientId,
			DeviceId: c.deviceId,
		},
	}

	switch lm := credentials.(type) {
	case *credentialspb.StoredCredential:
		req.LoginMethod = &pb.LoginRequest_StoredCredential{StoredCredential: lm}
	case *credentialspb.FacebookAccessToken:
		req.LoginMethod = &pb.LoginRequest_FacebookAccessToken{FacebookAccessToken: lm}
	case *credentialspb.OneTimeToken:
		req.LoginMethod = &pb.LoginRequest_OneTimeToken{OneTimeToken: lm}
	case *credentialspb.ParentChildCredential:
		req.LoginMethod = &pb.LoginRequest_ParentChildCredential{ParentChildCredential: lm}
	case *credentialspb.AppleSignInCredential:
		req.LoginMethod = &pb.LoginRequest_AppleSignInCredential{AppleSignInCredential: lm}
	case *credentialspb.SamsungSignInCredential:
		req.LoginMethod = &pb.LoginRequest_SamsungSignInCredential{SamsungSignInCredential: lm}
	case *credentialspb.GoogleSignInCredential:
		req.LoginMethod = &pb.LoginRequest_GoogleSignInCredential{GoogleSignInCredential: lm}
	default:
		return fmt.Errorf("invalid credentials: %v", lm)
	}

	resp, err := c.request(ctx, req)
	if err != nil {
		return fmt.Errorf("failed requesting login5 endpoint: %w", err)
	}

	if ch := resp.GetChallenges(); ch != nil && len(ch.Challenges) > 0 {
		req.LoginContext = resp.LoginContext
		req.ChallengeSolutions = &pb.ChallengeSolutions{}

		// solve challenges
		for _, c := range ch.Challenges {
			switch cc := c.Challenge.(type) {
			case *pb.Challenge_Hashcash:
				sol := solveHashcash(req.LoginContext, cc.Hashcash)
				req.ChallengeSolutions.Solutions = append(req.ChallengeSolutions.Solutions, &pb.ChallengeSolution{
					Solution: &pb.ChallengeSolution_Hashcash{Hashcash: sol},
				})
			case *pb.Challenge_Code:
				return fmt.Errorf("login5 code challenge not supported")
			}
		}

		resp, err = c.request(ctx, req)
		if err != nil {
			return fmt.Errorf("failed requesting login5 endpoint with challenge solutions: %w", err)
		}
	}

	if ok := resp.GetOk(); ok != nil {
		c.loginOk = ok
		c.loginOkExp = time.Now().Add(time.Duration(c.loginOk.AccessTokenExpiresIn) * time.Second)
		c.log.WithField("username", librespot.ObfuscateUsername(c.loginOk.Username)).
			Infof("authenticated Login5")
		return nil
	} else {
		return &LoginError{Code: resp.GetError()}
	}
}

func (c *Login5) Username() string {
	c.loginOkLock.RLock()
	defer c.loginOkLock.RUnlock()

	if c.loginOk == nil {
		c.log.Warn("login5 not authenticated")
		return ""
	}

	return c.loginOk.Username
}

func (c *Login5) StoredCredential() []byte {
	c.loginOkLock.RLock()
	defer c.loginOkLock.RUnlock()

	if c.loginOk == nil {
		c.log.Warn("login5 not authenticated")
		return nil
	}

	return c.loginOk.StoredCredential
}

// minForcedRefreshInterval collapses bursts of 401-triggered forced refreshes
// (spclient retries concurrent requests with force=true) into a single login.
const minForcedRefreshInterval = 2 * time.Second

func (c *Login5) AccessToken() librespot.GetLogin5TokenFunc {
	return func(ctx context.Context, force bool) (string, error) {
		cachedToken, cached := c.cachedToken(force)
		if cached {
			return cachedToken, nil
		}

		// Single-flight: serialize refreshes; double-check after acquiring in
		// case another caller refreshed while we waited.
		c.refreshMu.Lock()
		defer c.refreshMu.Unlock()

		if cachedToken, cached := c.cachedToken(force); cached {
			return cachedToken, nil
		}

		c.loginOkLock.RLock()
		if c.loginOk == nil {
			c.loginOkLock.RUnlock()
			return "", errors.New("login5 not authenticated")
		}
		username, storedCred := c.loginOk.Username, c.loginOk.StoredCredential
		c.loginOkLock.RUnlock()

		c.log.Debug("renewing login5 access token")
		if err := c.Login(ctx, &credentialspb.StoredCredential{
			Username: username,
			Data:     storedCred,
		}); err != nil {
			return "", fmt.Errorf("failed renewing login5 access token: %w", err)
		}

		c.loginOkLock.Lock()
		c.lastForcedRefresh = time.Now()
		token := c.loginOk.AccessToken
		c.loginOkLock.Unlock()
		return token, nil
	}
}

func (c *Login5) cachedToken(force bool) (string, bool) {
	now := time.Now()

	c.loginOkLock.Lock()
	defer c.loginOkLock.Unlock()

	if c.loginOk == nil {
		return "", false
	}

	if force && now.Sub(c.lastForcedRefresh) < minForcedRefreshInterval {
		// A forced refresh just happened; the 401 burst that triggered it
		// is already covered by this token.
		force = false
	}
	if force {
		return "", false
	}
	if c.loginOkExp.After(now) {
		return c.loginOk.AccessToken, true
	}
	return "", false
}
