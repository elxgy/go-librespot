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
