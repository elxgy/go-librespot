package spotifyuser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const apiBase = "https://api.spotify.com"

func GetCurrentUserID(ctx context.Context, accessToken string) (string, error) {
	if accessToken == "" {
		return "", fmt.Errorf("access token is empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/v1/me", nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request /v1/me: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET /v1/me: %d", resp.StatusCode)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode /v1/me: %w", err)
	}
	if out.ID == "" {
		return "", fmt.Errorf("GET /v1/me: response missing id")
	}
	return out.ID, nil
}
