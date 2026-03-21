package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/elxgy/go-librespot/session"
)

func GetMe(ctx context.Context, sess *session.Session) (string, error) {
	resp, err := sess.WebApiWith429Retry(ctx, "GET", "v1/me", nil, nil, nil)
	if err != nil {
		return "", fmt.Errorf("webapi me: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("webapi me: %d %s", resp.StatusCode, string(body))
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode me: %w", err)
	}
	return out.ID, nil
}

func GetUserPlaylistsPage(ctx context.Context, sess *session.Session, offset, limit int) (*Page, error) {
	if offset < 0 {
		return nil, fmt.Errorf("playlist offset must be >= 0")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 50 {
		limit = 50
	}
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(offset))
	resp, err := sess.WebApiWith429Retry(ctx, "GET", "v1/me/playlists", q, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("webapi playlists: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("webapi playlists: %d %s", resp.StatusCode, string(body))
	}
	var raw struct {
		Items []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			URI           string `json:"uri"`
			Collaborative bool   `json:"collaborative"`
			Owner         struct {
				ID          string `json:"id"`
				DisplayName string `json:"display_name"`
			} `json:"owner"`
			Images []struct {
				URL string `json:"url"`
			} `json:"images"`
			Tracks struct {
				Total int `json:"total"`
			} `json:"tracks"`
		} `json:"items"`
		Next *string `json:"next"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode playlists: %w", err)
	}
	out := &Page{Offset: offset, Limit: limit}
	if len(raw.Items) == 0 {
		out.NextOffset = offset
		return out, nil
	}
	out.Items = make([]Summary, 0, len(raw.Items))
	for _, pl := range raw.Items {
		imageURL := ""
		if len(pl.Images) > 0 {
			imageURL = pl.Images[0].URL
		}
		out.Items = append(out.Items, Summary{
			ID:            pl.ID,
			Name:          pl.Name,
			URI:           pl.URI,
			Kind:          ContextKindPlaylist,
			Owner:         pl.Owner.DisplayName,
			OwnerID:       pl.Owner.ID,
			Collaborative: pl.Collaborative,
			TrackCount:    pl.Tracks.Total,
			ImageURL:      imageURL,
		})
	}
	out.NextOffset = offset + len(out.Items)
	out.HasMore = raw.Next != nil && *raw.Next != ""
	return out, nil
}

func GetSavedAlbumsPage(ctx context.Context, sess *session.Session, offset, limit int) (*Page, error) {
	if offset < 0 {
		return nil, fmt.Errorf("album offset must be >= 0")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 50 {
		limit = 50
	}
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(offset))
	resp, err := sess.WebApiWith429Retry(ctx, "GET", "v1/me/albums", q, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("webapi albums: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("webapi albums: %d %s", resp.StatusCode, string(body))
	}
	var raw struct {
		Items []struct {
			Album struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				URI         string `json:"uri"`
				TotalTracks int    `json:"total_tracks"`
				Artists     []struct {
					Name string `json:"name"`
				} `json:"artists"`
				Images []struct {
					URL string `json:"url"`
				} `json:"images"`
			} `json:"album"`
		} `json:"items"`
		Next *string `json:"next"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode albums: %w", err)
	}
	out := &Page{Offset: offset, Limit: limit}
	if len(raw.Items) == 0 {
		out.NextOffset = offset
		return out, nil
	}
	out.Items = make([]Summary, 0, len(raw.Items))
	for _, item := range raw.Items {
		album := item.Album
		if album.ID == "" || album.URI == "" {
			continue
		}
		imageURL := ""
		if len(album.Images) > 0 {
			imageURL = album.Images[0].URL
		}
		artists := make([]string, 0, len(album.Artists))
		for _, a := range album.Artists {
			if name := strings.TrimSpace(a.Name); name != "" {
				artists = append(artists, name)
			}
		}
		owner := strings.Join(artists, ", ")
		if owner == "" {
			owner = "Unknown artist"
		}
		out.Items = append(out.Items, Summary{
			ID:         album.ID,
			Name:       album.Name,
			URI:        album.URI,
			Kind:       ContextKindAlbum,
			Owner:      owner,
			TrackCount: album.TotalTracks,
			ImageURL:   imageURL,
		})
	}
	out.NextOffset = offset + len(out.Items)
	out.HasMore = raw.Next != nil && *raw.Next != ""
	return out, nil
}

func GetPlaylistTracksPage(ctx context.Context, sess *session.Session, playlistID string, offset, limit int) (*TrackPage, error) {
	if playlistID == "" {
		return nil, fmt.Errorf("playlist ID must not be empty")
	}
	if offset < 0 {
		return nil, fmt.Errorf("playlist offset must be >= 0")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 100 {
		limit = 100
	}
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(offset))
	path := "v1/playlists/" + url.PathEscape(playlistID) + "/tracks"
	resp, err := sess.WebApiWith429Retry(ctx, "GET", path, q, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("webapi playlist tracks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("webapi playlist tracks: %d %s", resp.StatusCode, string(body))
	}
	var raw struct {
		Items []struct {
			Track *struct {
				ID       string `json:"id"`
				URI      string `json:"uri"`
				Name     string `json:"name"`
				Duration int    `json:"duration_ms"`
				Artists  []struct {
					Name string `json:"name"`
				} `json:"artists"`
			} `json:"track"`
		} `json:"items"`
		Next *string `json:"next"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode playlist tracks: %w", err)
	}
	out := &TrackPage{Offset: offset, Limit: limit}
	if len(raw.Items) == 0 {
		out.NextOffset = offset
		return out, nil
	}
	out.TrackIDs = make([]string, 0, len(raw.Items))
	out.TrackInfos = make([]TrackInfo, 0, len(raw.Items))
	for _, item := range raw.Items {
		if item.Track == nil || item.Track.ID == "" {
			continue
		}
		out.TrackIDs = append(out.TrackIDs, item.Track.ID)
		artist := ""
		if len(item.Track.Artists) > 0 {
			artist = item.Track.Artists[0].Name
		}
		out.TrackInfos = append(out.TrackInfos, TrackInfo{
			ID:         item.Track.ID,
			Name:       item.Track.Name,
			Artist:     artist,
			DurationMS: item.Track.Duration,
		})
	}
	out.NextOffset = offset + len(out.TrackIDs)
	out.HasMore = raw.Next != nil && *raw.Next != ""
	return out, nil
}
