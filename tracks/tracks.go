package tracks

import (
	"context"
	"fmt"
	"time"

	librespot "github.com/elxgy/go-librespot"
	connectpb "github.com/elxgy/go-librespot/proto/spotify/connectstate"
	"github.com/elxgy/go-librespot/spclient"
	"golang.org/x/exp/rand"
)

type List struct {
	log librespot.Logger

	ctx *spclient.ContextResolver

	shuffled bool
	tracks   *pagedList[*connectpb.ContextTrack]

	// playbackOrder maps playback positions to context indices in the pagedList.
	// The pagedList is NEVER mutated by shuffle — it always holds tracks in
	// original context order. Only playbackOrder changes.
	playbackOrder []int // playback position → context index
	playbackPos   int   // current position in playback order (-1 = not started)

	playingQueue       bool
	queue              []*connectpb.ContextTrack
	maxTracksInContext int
}

func NewTrackListFromContext(ctx context.Context, log_ librespot.Logger, sp *spclient.Spclient, spotCtx *connectpb.Context, maxTracksInContext int) (_ *List, err error) {
	tl := &List{}
	tl.ctx, err = spclient.NewContextResolver(ctx, log_, sp, spotCtx)
	if err != nil {
		return nil, fmt.Errorf("failed initializing context resolver: %w", err)
	}

	tl.log = log_.WithField("uri", tl.ctx.Uri())
	tl.log.Debugf("resolved context of %s", tl.ctx.Type())

	tl.tracks = newPagedList[*connectpb.ContextTrack](tl.log, tl.ctx)
	tl.playbackPos = -1
	if maxTracksInContext <= 0 {
		maxTracksInContext = MaxTracksInContext
	}
	tl.maxTracksInContext = maxTracksInContext
	return tl, nil
}

func (tl *List) Metadata() map[string]string {
	return tl.ctx.Metadata()
}

// ShuffleStartPos returns playbackPos — the boundary between played and unplayed tracks.
// Tracks before this index in playbackOrder are "already played."
func (tl *List) ShuffleStartPos() int {
	if !tl.shuffled {
		return 0
	}
	return tl.playbackPos
}

// ensurePlaybackOrder loads all pages and builds the identity playback order if not yet built.
func (tl *List) ensurePlaybackOrder(ctx context.Context) error {
	if tl.playbackOrder != nil && tl.tracks.len() == len(tl.playbackOrder) {
		return nil
	}

	iter := tl.tracks.iterStart()
	for iter.next(ctx) {
		// consume all pages
	}
	if err := iter.error(); err != nil {
		return fmt.Errorf("failed fetching all tracks: %w", err)
	}

	tl.buildPlaybackOrder()
	return nil
}

// buildPlaybackOrder (re)builds the identity playback order for all currently loaded tracks.
func (tl *List) buildPlaybackOrder() {
	n := tl.tracks.len()
	tl.playbackOrder = make([]int, n)
	for i := 0; i < n; i++ {
		tl.playbackOrder[i] = i
	}
}

// extendPlaybackOrder extends the playback order for newly loaded pages without rebuilding.
func (tl *List) extendPlaybackOrder() {
	n := tl.tracks.len()
	if tl.playbackOrder == nil {
		tl.playbackOrder = make([]int, 0, n)
	}
	for i := len(tl.playbackOrder); i < n; i++ {
		tl.playbackOrder = append(tl.playbackOrder, i)
	}
}

// contextTrackAt returns the track at the given context index, fetching pages if needed.
func (tl *List) contextTrackAt(ctx context.Context, ctxIdx int) (*connectpb.ContextTrack, error) {
	for tl.tracks.len() <= ctxIdx {
		if _, err := tl.tracks.fetchNextPage(ctx); err != nil {
			return nil, err
		}
		tl.extendPlaybackOrder()
	}
	return tl.tracks.list[ctxIdx].item, nil
}

func (tl *List) TrySeek(ctx context.Context, f func(track *connectpb.ContextTrack) bool) error {
	if err := tl.Seek(ctx, f); err != nil {
		tl.log.WithError(err).Warnf("failed seeking to track in context %s", tl.ctx.Uri())

		tl.tracks.clear()
		tl.playbackOrder = nil
		tl.playbackPos = -1
	}

	return nil
}

func (tl *List) Seek(ctx context.Context, f func(*connectpb.ContextTrack) bool) error {
	if err := tl.ensurePlaybackOrder(ctx); err != nil {
		return fmt.Errorf("failed loading tracks for seek: %w", err)
	}

	for i, ctxIdx := range tl.playbackOrder {
		if f(tl.tracks.list[ctxIdx].item) {
			tl.playbackPos = i
			return nil
		}
	}

	return fmt.Errorf("could not find track")
}

func (tl *List) AllTracks(ctx context.Context) []*connectpb.ProvidedTrack {
	if err := tl.ensurePlaybackOrder(ctx); err != nil {
		tl.log.WithError(err).Error("failed loading all tracks")
		return nil
	}

	tracks := make([]*connectpb.ProvidedTrack, 0, len(tl.playbackOrder))
	for _, ctxIdx := range tl.playbackOrder {
		tracks = append(tracks, librespot.ContextTrackToProvidedTrack(tl.ctx.Type(), tl.tracks.list[ctxIdx].item))
	}
	return tracks
}

const MaxTracksInContext = 32

func (tl *List) maxTracks() int {
	if tl.maxTracksInContext <= 0 {
		return MaxTracksInContext
	}
	return tl.maxTracksInContext
}

// UpcomingTracks returns the next n tracks from the playback order after the current track.
// This is the authoritative "up next" queue — no derivation, no caching, no seen maps.
// Queue items (AddToQueue/SetQueue) are returned first, then playback order tracks.
func (tl *List) UpcomingTracks(ctx context.Context, n int) []*connectpb.ProvidedTrack {
	if n <= 0 {
		return nil
	}

	maxT := n
	tracks := make([]*connectpb.ProvidedTrack, 0, maxT)

	// Queue items first
	if len(tl.queue) > 0 {
		queue := tl.queue
		if tl.playingQueue {
			queue = queue[1:] // skip currently-playing queue item
		}
		for i := 0; i < len(queue) && len(tracks) < maxT; i++ {
			tracks = append(tracks, librespot.ContextTrackToProvidedTrack(tl.ctx.Type(), queue[i]))
		}
		if len(tracks) >= maxT {
			return tracks
		}
	}

	// Playback order tracks after current position
	if tl.playbackOrder != nil && tl.playbackPos >= 0 {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		for i := tl.playbackPos + 1; i < len(tl.playbackOrder) && len(tracks) < maxT; i++ {
			ctxIdx := tl.playbackOrder[i]
			track, err := tl.contextTrackAt(ctx, ctxIdx)
			if err != nil {
				break
			}
			tracks = append(tracks, librespot.ContextTrackToProvidedTrack(tl.ctx.Type(), track))
		}
	}

	return tracks
}

func (tl *List) PrevTracks() []*connectpb.ProvidedTrack {
	maxT := tl.maxTracks()

	if tl.playingQueue || tl.playbackPos < 0 || tl.playbackOrder == nil {
		return nil
	}

	count := min(tl.playbackPos, maxT)
	tracks := make([]*connectpb.ProvidedTrack, 0, count)

	for i := tl.playbackPos - count; i < tl.playbackPos; i++ {
		if i < 0 || i >= len(tl.playbackOrder) {
			continue
		}
		ctxIdx := tl.playbackOrder[i]
		if ctxIdx < tl.tracks.len() {
			tracks = append(tracks, librespot.ContextTrackToProvidedTrack(tl.ctx.Type(), tl.tracks.list[ctxIdx].item))
		}
	}

	return tracks
}

func (tl *List) NextTracks(ctx context.Context, nextHint []*connectpb.ContextTrack) []*connectpb.ProvidedTrack {
	maxT := tl.maxTracks()
	tracks := make([]*connectpb.ProvidedTrack, 0, maxT)

	if len(tl.queue) > 0 {
		queue := tl.queue
		if tl.playingQueue {
			queue = queue[1:]
		}

		for i := 0; i < len(queue) && len(tracks) < maxT; i++ {
			tracks = append(tracks, librespot.ContextTrackToProvidedTrack(tl.ctx.Type(), queue[i]))
		}
	}

	if nextHint != nil {
		queueLength := len(tl.queue)
		if tl.playingQueue {
			queueLength -= 1
		}
		for idx, curr := range nextHint {
			if idx < queueLength {
				continue
			}
			if !(len(tracks) < maxT) {
				break
			}

			delete(curr.Metadata, "is_queued")
			tracks = append(tracks, librespot.ContextTrackToProvidedTrack(tl.ctx.Type(), curr))
		}
	} else {
		// Use playback order to get next tracks
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		if tl.playbackOrder != nil && tl.playbackPos >= 0 {
			for i := tl.playbackPos + 1; i < len(tl.playbackOrder) && len(tracks) < maxT; i++ {
				ctxIdx := tl.playbackOrder[i]
				track, err := tl.contextTrackAt(ctx, ctxIdx)
				if err != nil {
					break
				}
				tracks = append(tracks, librespot.ContextTrackToProvidedTrack(tl.ctx.Type(), track))
			}
		} else {
			// Playback order not built yet — fall back to context-order iteration
			iter := tl.tracks.iterHere()
			for len(tracks) < maxT && iter.next(ctx) {
				curr := iter.get()
				tracks = append(tracks, librespot.ContextTrackToProvidedTrack(tl.ctx.Type(), curr.item))
			}
			if err := iter.error(); err != nil {
				tl.log.WithError(err).Error("failed fetching next tracks")
			}
		}
	}

	return tracks
}

func (tl *List) Index() *connectpb.ContextIndex {
	if tl.playingQueue {
		return &connectpb.ContextIndex{}
	}

	if tl.playbackPos < 0 || tl.playbackOrder == nil || tl.playbackPos >= len(tl.playbackOrder) {
		return &connectpb.ContextIndex{}
	}

	ctxIdx := tl.playbackOrder[tl.playbackPos]
	if ctxIdx >= 0 && ctxIdx < tl.tracks.len() {
		curr := tl.tracks.list[ctxIdx]
		return &connectpb.ContextIndex{Page: uint32(curr.pageIdx), Track: uint32(curr.itemIdx)}
	}
	return &connectpb.ContextIndex{}
}

func (tl *List) current() *connectpb.ContextTrack {
	if tl.playingQueue {
		return tl.queue[0]
	}

	if tl.playbackPos < 0 || tl.playbackOrder == nil || tl.playbackPos >= len(tl.playbackOrder) {
		return nil
	}

	ctxIdx := tl.playbackOrder[tl.playbackPos]
	if ctxIdx < tl.tracks.len() {
		return tl.tracks.list[ctxIdx].item
	}
	return nil
}

func (tl *List) CurrentTrack() *connectpb.ProvidedTrack {
	item := tl.current()
	if item == nil {
		return nil
	}
	return librespot.ContextTrackToProvidedTrack(tl.ctx.Type(), item)
}

func (tl *List) GoStart(ctx context.Context) bool {
	if err := tl.ensurePlaybackOrder(ctx); err != nil {
		tl.log.WithError(err).Error("failed building playback order for go start")
		return false
	}

	if len(tl.playbackOrder) == 0 {
		return false
	}

	tl.playbackPos = 0
	return true
}

func (tl *List) PeekNext(ctx context.Context) *connectpb.ContextTrack {
	if tl.playingQueue && len(tl.queue) > 1 {
		return tl.queue[1]
	} else if !tl.playingQueue && len(tl.queue) > 0 {
		return tl.queue[0]
	}

	// Look ahead in playback order
	if tl.playbackOrder != nil && tl.playbackPos >= 0 {
		nextPos := tl.playbackPos + 1
		if nextPos < len(tl.playbackOrder) {
			ctxIdx := tl.playbackOrder[nextPos]
			track, err := tl.contextTrackAt(ctx, ctxIdx)
			if err == nil {
				return track
			}
		}
		return nil
	}

	// Fallback: context order iteration
	iter := tl.tracks.iterHere()
	if iter.next(ctx) {
		return iter.get().item
	}

	return nil
}

func (tl *List) GoNext(ctx context.Context) bool {
	if tl.playingQueue {
		tl.queue = tl.queue[1:]
	}

	if len(tl.queue) > 0 {
		tl.playingQueue = true
		return true
	}

	tl.playingQueue = false

	// Extend playback order for any newly loaded pages
	tl.extendPlaybackOrder()

	tl.playbackPos++

	if tl.playbackOrder != nil && tl.playbackPos < len(tl.playbackOrder) {
		return true
	}

	// Need more tracks — try fetching next page
	if _, err := tl.tracks.fetchNextPage(ctx); err == nil {
		tl.extendPlaybackOrder()
		if tl.playbackPos < len(tl.playbackOrder) {
			return true
		}
	}

	// No more tracks
	tl.playbackPos = len(tl.playbackOrder) - 1
	return false
}

func (tl *List) GoPrev() bool {
	if tl.playingQueue {
		tl.playingQueue = false
		// Don't change playbackPos — it was never advanced during queue playback
		return tl.playbackPos >= 0
	}

	if tl.playbackPos > 0 {
		tl.playbackPos--
		return true
	}

	return false
}

func (tl *List) AddToQueue(track *connectpb.ContextTrack) {
	if track.Metadata == nil {
		track.Metadata = make(map[string]string)
	}

	track.Metadata["is_queued"] = "true"
	tl.queue = append(tl.queue, track)
}

func (tl *List) SetQueue(_ []*connectpb.ContextTrack, next []*connectpb.ContextTrack) {
	if tl.playingQueue {
		tl.queue = tl.queue[:1]
	} else {
		tl.queue = nil
	}

	for _, track := range next {
		if queued := track.Metadata["is_queued"]; queued != "true" {
			break
		}

		tl.queue = append(tl.queue, track)
	}
}

func (tl *List) SetPlayingQueue(val bool) {
	tl.playingQueue = len(tl.queue) > 0 && val
}

func (tl *List) ToggleShuffle(ctx context.Context, shuffle bool) error {
	if shuffle == tl.shuffled {
		return nil
	}

	if shuffle {
		// Load all pages so we have the full playback order
		if err := tl.ensurePlaybackOrder(ctx); err != nil {
			return fmt.Errorf("failed loading tracks for shuffle: %w", err)
		}

		seed := rand.Uint64() + 1
		rnd := rand.New(rand.NewSource(seed))

		if tl.playbackPos > 0 {
			// Partial shuffle: keep the current track at playbackPos fixed,
			// shuffle only the tracks AFTER it (upcoming portion).
			// playbackOrder[:playbackPos+1] stays as-is (already played + current)
			// playbackOrder[playbackPos+1:] gets shuffled
			upcoming := tl.playbackOrder[tl.playbackPos+1:]
			for i := len(upcoming) - 1; i > 0; i-- {
				j := rnd.Intn(i + 1)
				upcoming[i], upcoming[j] = upcoming[j], upcoming[i]
			}
		} else {
			// Full shuffle: shuffle everything. No track is pinned.
			// playbackPos stays 0 — the first track in the shuffled order is now current.
			for i := len(tl.playbackOrder) - 1; i > 0; i-- {
				j := rnd.Intn(i + 1)
				tl.playbackOrder[i], tl.playbackOrder[j] = tl.playbackOrder[j], tl.playbackOrder[i]
			}
		}

		tl.shuffled = true
		tl.log.Debugf("shuffled playback order (len: %d, pos: %d)", len(tl.playbackOrder), tl.playbackPos)
		return nil

	} else {
		// Deshuffle: reset playback order to identity (original context order)
		// The pagedList was NEVER mutated, so this is trivially correct.
		if tl.playbackOrder != nil {
			for i := range tl.playbackOrder {
				tl.playbackOrder[i] = i
			}
		}

		tl.shuffled = false
		tl.log.Debugf("unshuffled playback order (pos: %d)", tl.playbackPos)
		return nil
	}
}
