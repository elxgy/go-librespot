package session

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	webApi429MaxRetries   = 2
	webApi429DefaultWait  = 5 * time.Second
	webApi429MinRemaining = 2 * time.Second
)

func (s *Session) WebApiWith429Retry(ctx context.Context, method, path string, query url.Values, header http.Header, body []byte) (*http.Response, error) {
	var lastResp *http.Response
	for attempt := 0; attempt <= webApi429MaxRetries; attempt++ {
		if lastResp != nil {
			_ = lastResp.Body.Close()
		}
		resp, err := s.WebApi(ctx, method, path, query, header, body)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != 429 {
			return resp, nil
		}
		lastResp = resp
		wait := webApi429DefaultWait
		if h := resp.Header.Get("Retry-After"); h != "" {
			if sec, err := strconv.Atoi(h); err == nil && sec > 0 && sec <= 60 {
				wait = time.Duration(sec) * time.Second
			}
		}
		if deadline, ok := ctx.Deadline(); ok {
			remaining := time.Until(deadline)
			if remaining <= webApi429MinRemaining {
				_ = lastResp.Body.Close()
				return nil, fmt.Errorf("rate limited (429); not enough time to wait (Retry-After %v)", wait)
			}
			if wait > remaining-webApi429MinRemaining {
				wait = remaining - webApi429MinRemaining
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	if lastResp != nil {
		_ = lastResp.Body.Close()
	}
	return nil, fmt.Errorf("webapi rate limit (429) after %d retries", webApi429MaxRetries+1)
}

func (s *Session) WebApiWith429RetryAndReadBody(ctx context.Context, method, path string, query url.Values, body []byte) ([]byte, int, error) {
	resp, err := s.WebApiWith429Retry(ctx, method, path, query, nil, body)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return payload, resp.StatusCode, nil
}
