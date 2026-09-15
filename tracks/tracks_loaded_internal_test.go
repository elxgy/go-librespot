//go:build test_unit

package tracks

import (
	"context"
	"testing"

	librespot "github.com/elxgy/go-librespot"
	connectpb "github.com/elxgy/go-librespot/proto/spotify/connectstate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// The List is used with a mock page resolver so that any unexpected Page()
// call (i.e. a fetch triggered by the loaded-only read methods) panics the
// mock. The single expected Page(0) covers the setup fetch.
func newTestLoadedList(t *testing.T, uris []string) (*List, *librespot.MockPageResolver[*connectpb.ContextTrack]) {
	t.Helper()

	spotCtx := &connectpb.Context{
		Uri: "spotify:playlist:test",
		Pages: []*connectpb.ContextPage{
			{Tracks: []*connectpb.ContextTrack{{Uri: "spotify:track:0000000000000000000000"}}},
		},
	}
	tl, err := NewTrackListFromContext(context.Background(), &librespot.NullLogger{}, nil, spotCtx, 0)
	if err != nil {
		t.Fatalf("failed building track list: %v", err)
	}

	resolver := librespot.NewMockPageResolver[*connectpb.ContextTrack](t)
	var page []*connectpb.ContextTrack
	for _, uri := range uris {
		page = append(page, &connectpb.ContextTrack{Uri: uri})
	}
	resolver.EXPECT().Page(mock.Anything, 0).Return(page, nil).Once()
	tl.tracks = newPagedList[*connectpb.ContextTrack](tl.log, resolver)
	if _, err := tl.tracks.fetchNextPage(context.Background()); err != nil {
		t.Fatalf("failed loading page: %v", err)
	}

	tl.playbackPos = -1
	tl.buildPlaybackOrder()
	return tl, resolver
}

func TestUpcomingTracksLoadedDoesNotFetch(t *testing.T) {
	uris := []string{"spotify:track:1111111111111111111111", "spotify:track:2222222222222222222222", "spotify:track:3333333333333333333333"}
	tl, _ := newTestLoadedList(t, uris)

	if err := tl.Seek(context.Background(), func(track *connectpb.ContextTrack) bool { return track.Uri == uris[0] }); err != nil {
		t.Fatalf("seek failed: %v", err)
	}

	got := tl.UpcomingTracksLoaded(2)
	assert.Len(t, got, 2)
	assert.Equal(t, uris[1], got[0].Uri)
	assert.Equal(t, uris[2], got[1].Uri)

	got = tl.UpcomingTracksLoaded(5)
	assert.Len(t, got, 2, "must stop at the loaded boundary")
}

func TestNextTracksLoadedDoesNotFetch(t *testing.T) {
	uris := []string{"spotify:track:1111111111111111111111", "spotify:track:2222222222222222222222"}
	tl, _ := newTestLoadedList(t, uris)

	if err := tl.Seek(context.Background(), func(track *connectpb.ContextTrack) bool { return track.Uri == uris[0] }); err != nil {
		t.Fatalf("seek failed: %v", err)
	}

	got := tl.NextTracksLoaded(nil)
	assert.Len(t, got, 1)
	assert.Equal(t, uris[1], got[0].Uri)
}

func TestNextTracksLoadedWithHint(t *testing.T) {
	uris := []string{"spotify:track:1111111111111111111111"}
	tl, _ := newTestLoadedList(t, uris)

	if err := tl.Seek(context.Background(), func(track *connectpb.ContextTrack) bool { return track.Uri == uris[0] }); err != nil {
		t.Fatalf("seek failed: %v", err)
	}

	hint := []*connectpb.ContextTrack{{Uri: "spotify:track:4444444444444444444444"}}
	got := tl.NextTracksLoaded(hint)
	assert.Len(t, got, 1)
	assert.Equal(t, hint[0].Uri, got[0].Uri)
}

func TestUpcomingTracksLoadedWithQueue(t *testing.T) {
	uris := []string{"spotify:track:1111111111111111111111", "spotify:track:2222222222222222222222"}
	tl, _ := newTestLoadedList(t, uris)

	if err := tl.Seek(context.Background(), func(track *connectpb.ContextTrack) bool { return track.Uri == uris[0] }); err != nil {
		t.Fatalf("seek failed: %v", err)
	}

	tl.AddToQueue(&connectpb.ContextTrack{Uri: "spotify:track:5555555555555555555555", Metadata: map[string]string{}})

	got := tl.UpcomingTracksLoaded(10)
	assert.Len(t, got, 2)
	assert.Equal(t, "spotify:track:5555555555555555555555", got[0].Uri)
	assert.Equal(t, uris[1], got[1].Uri)
}
