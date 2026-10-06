//go:build test_unit

package tracks

import (
	"context"
	"testing"

	librespot "github.com/elxgy/go-librespot"
	connectpb "github.com/elxgy/go-librespot/proto/spotify/connectstate"
	"github.com/elxgy/go-librespot/spclient"
)

// queue-only list: nothing playing from context, no pages fetched.
func newQueueOnlyList(t *testing.T) *List {
	t.Helper()
	ctx, err := spclient.NewContextResolver(context.Background(), &librespot.NullLogger{}, nil, &connectpb.Context{
		Uri: "spotify:playlist:test",
		Pages: []*connectpb.ContextPage{
			{Tracks: []*connectpb.ContextTrack{{Uri: "spotify:track:0000000000000000000000"}}},
		},
	})
	if err != nil {
		t.Fatalf("failed building context resolver: %v", err)
	}
	return &List{log: &librespot.NullLogger{}, ctx: ctx}
}

func qtracks(uris ...string) []*connectpb.ContextTrack {
	var out []*connectpb.ContextTrack
	for _, u := range uris {
		out = append(out, &connectpb.ContextTrack{Uri: u})
	}
	return out
}

func uriAt(tl *List, i int) string {
	if tl.playingQueue {
		return tl.queue[i+1].Uri
	}
	return tl.queue[i].Uri
}

func TestRemoveFromQueueNotPlaying(t *testing.T) {
	tl := newQueueOnlyList(t)
	tl.queue = qtracks("spotify:track:a", "spotify:track:b", "spotify:track:c")

	if !tl.RemoveFromQueue(1) {
		t.Fatal("expected removal to succeed")
	}
	if got := uriAt(tl, 1); got != "spotify:track:c" {
		t.Fatalf("after removing index 1, up-next[1] = %s, want c", got)
	}
	if !tl.RemoveFromQueue(1) || len(tl.queue) != 1 {
		t.Fatalf("unexpected queue state after second removal: %v", tl.queue)
	}
	if tl.RemoveFromQueue(1) {
		t.Fatal("out-of-range removal must fail")
	}
	if !tl.RemoveFromQueue(0) || len(tl.queue) != 0 {
		t.Fatalf("removing last entry should empty the queue")
	}
	if tl.playingQueue {
		t.Fatal("empty queue must clear playingQueue")
	}
}

func TestRemoveFromQueueWhilePlayingQueue(t *testing.T) {
	tl := newQueueOnlyList(t)
	tl.queue = qtracks("playing", "a", "b", "c")
	tl.playingQueue = true

	// visible up-next starts at queue[1]: remove "a" (index 0)
	if !tl.RemoveFromQueue(0) {
		t.Fatal("expected removal to succeed")
	}
	if got := uriAt(tl, 0); got != "b" {
		t.Fatalf("up-next[0] = %s, want b (playing entry untouched)", got)
	}
	if tl.queue[0].Uri != "playing" {
		t.Fatal("queue[0] (currently playing) must not be removable via visible index")
	}
	if tl.RemoveFromQueue(-1) {
		t.Fatal("negative index must fail")
	}
}

func TestRemoveFromQueueLastVisibleKeepsPlayingEntry(t *testing.T) {
	tl := newQueueOnlyList(t)
	tl.queue = qtracks("playing", "a")
	tl.playingQueue = true

	if !tl.RemoveFromQueue(0) {
		t.Fatal("expected removal to succeed")
	}
	// queue[0] is still the playing entry; nothing visible left but the
	// playing entry must survive.
	if len(tl.queue) != 1 || tl.queue[0].Uri != "playing" {
		t.Fatalf("playing entry must survive, queue: %v", tl.queue)
	}
	if !tl.playingQueue {
		t.Fatal("playingQueue stays true while queue[0] is the playing entry (GoNext pops it next)")
	}
}

func TestReorderQueue(t *testing.T) {
	tl := newQueueOnlyList(t)
	tl.queue = qtracks("a", "b", "c")

	if !tl.ReorderQueue(2, 0) {
		t.Fatal("expected reorder to succeed")
	}
	if got := uriAt(tl, 0); got != "c" {
		t.Fatalf("up-next[0] = %s, want c", got)
	}
	if got := uriAt(tl, 2); got != "b" {
		t.Fatalf("up-next[2] = %s, want b", got)
	}
	if tl.ReorderQueue(0, 5) || tl.ReorderQueue(-1, 0) {
		t.Fatal("out-of-range reorder must fail")
	}
}

func TestReorderQueuePlayingKeepsHead(t *testing.T) {
	tl := newQueueOnlyList(t)
	tl.queue = qtracks("playing", "a", "b", "c")
	tl.playingQueue = true

	if !tl.ReorderQueue(0, 2) {
		t.Fatal("expected reorder to succeed")
	}
	if got := uriAt(tl, 0); got != "b" {
		t.Fatalf("up-next[0] = %s, want b", got)
	}
	if got := uriAt(tl, 2); got != "a" {
		t.Fatalf("up-next[2] = %s, want a (item lands at the target position)", got)
	}
	if tl.queue[0].Uri != "playing" {
		t.Fatal("playing entry must not move")
	}
}

func TestGoToQueueEntry(t *testing.T) {
	tl := newQueueOnlyList(t)
	tl.queue = qtracks("playing", "a", "b", "c")
	tl.playingQueue = true

	if !tl.GoToQueueEntry(1) {
		t.Fatal("expected jump to succeed")
	}
	if !tl.playingQueue {
		t.Fatal("jump must set playingQueue")
	}
	if tl.queue[0].Uri != "b" {
		t.Fatalf("queue[0] = %s, want b (promoted to current)", tl.queue[0].Uri)
	}
	if len(tl.queue) != 2 {
		t.Fatalf("entries before the target must be discarded, got %d", len(tl.queue))
	}
	if got := uriAt(tl, 0); got != "c" {
		t.Fatalf("after promotion, up-next[0] = %s, want c", got)
	}
	if tl.GoToQueueEntry(5) {
		t.Fatal("out-of-range jump must fail")
	}
}

func TestGoToQueueEntryFromNonPlaying(t *testing.T) {
	tl := newQueueOnlyList(t)
	tl.queue = qtracks("a", "b", "c")

	if !tl.GoToQueueEntry(1) {
		t.Fatal("expected jump to succeed")
	}
	if tl.queue[0].Uri != "b" || !tl.playingQueue {
		t.Fatalf("queue[0] = %s playingQueue = %v", tl.queue[0].Uri, tl.playingQueue)
	}
}

func TestQueueEditEmptyUIDEntries(t *testing.T) {
	// entries without UIDs must not break positional math
	tl := newQueueOnlyList(t)
	tl.queue = []*connectpb.ContextTrack{
		{Uri: "a", Metadata: map[string]string{"is_queued": "true"}},
		{Uri: "b"},
		{Uri: "c"},
	}

	if !tl.RemoveFromQueue(0) || !tl.ReorderQueue(0, 1) || !tl.GoToQueueEntry(0) {
		t.Fatal("queue editing must work positionally on empty-UID entries")
	}
}

func newContextUpNextList(t *testing.T, uris []string) *List {
	t.Helper()
	var page []*connectpb.ContextTrack
	for _, uri := range uris {
		page = append(page, &connectpb.ContextTrack{Uri: uri})
	}
	spotCtx := &connectpb.Context{
		Uri:   "spotify:playlist:test",
		Pages: []*connectpb.ContextPage{{Tracks: page}},
	}
	tl, err := NewTrackListFromContext(context.Background(), &librespot.NullLogger{}, nil, spotCtx, 0)
	if err != nil {
		t.Fatalf("failed building track list: %v", err)
	}
	if err := tl.Seek(context.Background(), func(track *connectpb.ContextTrack) bool { return track.Uri == uris[0] }); err != nil {
		t.Fatalf("seek failed: %v", err)
	}
	return tl
}

func upNextURIs(tl *List, n int) []string {
	var out []string
	for _, tr := range tl.UpcomingTracksLoaded(n) {
		out = append(out, tr.Uri)
	}
	return out
}

func TestReorderUpNextContextMovesWithinPlaybackOrder(t *testing.T) {
	uris := []string{"t0", "t1", "t2", "t3", "t4"}
	tl := newContextUpNextList(t, uris)

	if !tl.ReorderUpNext(0, 2) {
		t.Fatal("expected context reorder to succeed")
	}
	got := upNextURIs(tl, 10)
	want := []string{"t2", "t3", "t1", "t4"}
	if len(got) != len(want) {
		t.Fatalf("up-next = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("up-next = %v, want %v", got, want)
		}
	}
	if cur := tl.CurrentTrack(); cur == nil || cur.Uri != "t0" {
		t.Fatalf("current track must not move, got %v", cur)
	}
}

func TestReorderUpNextCrossBoundaryRefused(t *testing.T) {
	uris := []string{"t0", "t1", "t2"}
	tl := newContextUpNextList(t, uris)
	tl.AddToQueue(&connectpb.ContextTrack{Uri: "q0"})

	if tl.ReorderUpNext(0, 1) {
		t.Fatal("manual-to-context reorder must fail")
	}
	if tl.ReorderUpNext(1, 0) {
		t.Fatal("context-to-manual reorder must fail")
	}
	got := upNextURIs(tl, 10)
	if len(got) != 3 || got[0] != "q0" || got[1] != "t1" || got[2] != "t2" {
		t.Fatalf("refused reorder must not mutate, up-next = %v", got)
	}
}

func TestReorderUpNextManualRoutesToQueue(t *testing.T) {
	uris := []string{"t0", "t1"}
	tl := newContextUpNextList(t, uris)
	tl.AddToQueue(&connectpb.ContextTrack{Uri: "q0"})
	tl.AddToQueue(&connectpb.ContextTrack{Uri: "q1"})

	if !tl.ReorderUpNext(0, 1) {
		t.Fatal("expected manual reorder to succeed")
	}
	got := upNextURIs(tl, 10)
	if len(got) != 3 || got[0] != "q1" || got[1] != "q0" || got[2] != "t1" {
		t.Fatalf("up-next = %v, want [q1 q0 t1]", got)
	}
}

func TestRemoveUpNextContextDropsFromPlaybackOrder(t *testing.T) {
	uris := []string{"t0", "t1", "t2"}
	tl := newContextUpNextList(t, uris)

	if !tl.RemoveUpNext(1) {
		t.Fatal("expected context remove to succeed")
	}
	got := upNextURIs(tl, 10)
	if len(got) != 1 || got[0] != "t1" {
		t.Fatalf("up-next = %v, want [t1]", got)
	}
	if cur := tl.CurrentTrack(); cur == nil || cur.Uri != "t0" {
		t.Fatalf("current track must not move, got %v", cur)
	}
}

func TestRemoveUpNextContextSurvivesRebuild(t *testing.T) {
	uris := []string{"t0", "t1", "t2", "t3"}
	tl := newContextUpNextList(t, uris)

	if !tl.ReorderUpNext(0, 2) {
		t.Fatal("expected reorder to succeed")
	}
	if !tl.RemoveUpNext(0) {
		t.Fatal("expected remove to succeed")
	}
	tl.buildPlaybackOrder()
	if err := tl.Seek(context.Background(), func(track *connectpb.ContextTrack) bool { return track.Uri == "t0" }); err != nil {
		t.Fatalf("re-seek failed: %v", err)
	}
	got := upNextURIs(tl, 10)
	want := []string{"t3", "t1"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("edits must survive a rebuild, up-next = %v, want %v", got, want)
	}
}

func TestRemoveUpNextOutOfRange(t *testing.T) {
	uris := []string{"t0", "t1"}
	tl := newContextUpNextList(t, uris)

	if tl.RemoveUpNext(5) || tl.RemoveUpNext(-1) {
		t.Fatal("out-of-range remove must fail")
	}
	if tl.ReorderUpNext(0, 5) {
		t.Fatal("out-of-range reorder must fail")
	}
}
