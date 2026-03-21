package tracks

import (
	"context"
	"fmt"
	"slices"
	"time"

	librespot "github.com/elxgy/go-librespot"
	connectpb "github.com/elxgy/go-librespot/proto/spotify/connectstate"
	"github.com/elxgy/go-librespot/spclient"
	"golang.org/x/exp/rand"
)

type List struct {
	log librespot.Logger

	ctx *spclient.ContextResolver

	shuffled        bool
	shuffleSeed     uint64
	shuffleLen      int
	shuffleKeep     int
	shuffleStartPos int // index where shuffle begins; tracks before this are "already played"
	tracks          *pagedList[*connectpb.ContextTrack]

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
	if maxTracksInContext <= 0 {
		maxTracksInContext = MaxTracksInContext
	}
	tl.maxTracksInContext = maxTracksInContext
	return tl, nil
}

func (tl *List) Metadata() map[string]string {
	return tl.ctx.Metadata()
}

// ShuffleStartPos returns the index where the shuffle portion begins.
// Tracks before this index are "already played" and were not shuffled.
// Returns 0 when not shuffled or when the entire list was shuffled.
func (tl *List) ShuffleStartPos() int {
	if !tl.shuffled {
		return 0
	}
	return tl.shuffleStartPos
}

func (tl *List) TrySeek(ctx context.Context, f func(track *connectpb.ContextTrack) bool) error {
	if err := tl.Seek(ctx, f); err != nil {
		tl.log.WithError(err).Warnf("failed seeking to track in context %s", tl.ctx.Uri())

		err = tl.tracks.moveStart(ctx)
		if err != nil {
			return err
		}
	}

	return nil
}

func (tl *List) Seek(ctx context.Context, f func(*connectpb.ContextTrack) bool) error {
	iter := tl.tracks.iterStart()
	for iter.next(ctx) {
		curr := iter.get()
		if f(curr.item) {
			tl.tracks.move(iter)
			return nil
		}
	}

	if err := iter.error(); err != nil {
		return fmt.Errorf("failed fetching tracks for seek: %w", err)
	}

	return fmt.Errorf("could not find track")
}

func (tl *List) AllTracks(ctx context.Context) []*connectpb.ProvidedTrack {
	tracks := make([]*connectpb.ProvidedTrack, 0, tl.tracks.len())

	iter := tl.tracks.iterStart()
	for iter.next(ctx) {
		curr := iter.get()
		tracks = append(tracks, librespot.ContextTrackToProvidedTrack(tl.ctx.Type(), curr.item))
	}

	if err := iter.error(); err != nil {
		tl.log.WithError(err).Error("failed fetching all tracks")
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

func (tl *List) PrevTracks() []*connectpb.ProvidedTrack {
	maxT := tl.maxTracks()
	tracks := make([]*connectpb.ProvidedTrack, 0, maxT)

	iter := tl.tracks.iterHere()
	for len(tracks) < maxT && iter.prev() {
		curr := iter.get()
		tracks = append(tracks, librespot.ContextTrackToProvidedTrack(tl.ctx.Type(), curr.item))
	}

	if err := iter.error(); err != nil {
		tl.log.WithError(err).Error("failed fetching prev tracks")
	}

	// Tracks were added in reverse order. Fix this by reversing them again.
	slices.Reverse(tracks)

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

			// if one moves one track out of the queue into the "coming next" tracks, it is unqueued, because queued items
			// are only the ones with the green symbol. if is_queued remains set, spotify will remove this track from the
			// coming up section entirely
			delete(curr.Metadata, "is_queued")
			tracks = append(tracks, librespot.ContextTrackToProvidedTrack(tl.ctx.Type(), curr))
		}
	} else {
		// Do not waste too much time fetching next tracks. Even if we do not fetch everything in time,
		// the playback will continue anyway.
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		iter := tl.tracks.iterHere()
		for len(tracks) < maxT && iter.next(ctx) {
			curr := iter.get()
			tracks = append(tracks, librespot.ContextTrackToProvidedTrack(tl.ctx.Type(), curr.item))
		}
		if err := iter.error(); err != nil {
			tl.log.WithError(err).Error("failed fetching next tracks")
		}
	}

	return tracks
}

func (tl *List) Index() *connectpb.ContextIndex {
	if tl.playingQueue {
		return &connectpb.ContextIndex{}
	}

	curr := tl.tracks.get()
	return &connectpb.ContextIndex{Page: uint32(curr.pageIdx), Track: uint32(curr.itemIdx)}
}

func (tl *List) current() *connectpb.ContextTrack {
	if tl.playingQueue {
		return tl.queue[0]
	}

	curr := tl.tracks.get()
	return curr.item
}

func (tl *List) CurrentTrack() *connectpb.ProvidedTrack {
	item := tl.current()
	return librespot.ContextTrackToProvidedTrack(tl.ctx.Type(), item)
}

func (tl *List) GoStart(ctx context.Context) bool {
	if err := tl.tracks.moveStart(ctx); err != nil {
		tl.log.WithError(err).Error("failed going to start")
		return false
	}

	return true
}

func (tl *List) PeekNext(ctx context.Context) *connectpb.ContextTrack {
	if tl.playingQueue && len(tl.queue) > 1 {
		return tl.queue[1]
	} else if !tl.playingQueue && len(tl.queue) > 0 {
		return tl.queue[0]
	}

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

	iter := tl.tracks.iterHere()
	if iter.next(ctx) {
		tl.tracks.move(iter)
		return true
	}

	if err := iter.error(); err != nil {
		tl.log.WithError(err).Error("failed going to next track")
	}

	return false
}

func (tl *List) GoPrev() bool {
	if tl.playingQueue {
		tl.playingQueue = false
	}

	iter := tl.tracks.iterHere()
	if iter.prev() {
		tl.tracks.move(iter)
		return true
	}

	if err := iter.error(); err != nil {
		tl.log.WithError(err).Error("failed going to previous track")
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

	// I don't know if this good enough, but it surely saves us a lot of complicated code
	for _, track := range next {
		// the queued tracks will always be the first tracks in the next list, so if we meet the first "non-queue",
		// 	the queue definitely ended
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
		// fetch all tracks
		iter := tl.tracks.iterStart()
		for iter.next(ctx) {
			// TODO: check that we do not seek forever
		}
		if err := iter.error(); err != nil {
			tl.log.WithError(err).Error("failed fetching all tracks")
		}

		currentPos := tl.tracks.pos
		tl.shuffleSeed = rand.Uint64() + 1

		if currentPos > 0 {
			// partial shuffle: only shuffle tracks from current position onward
			// tracks before currentPos are "already played" and stay in place
			tl.shuffleStartPos = currentPos
			tl.tracks.shuffleFromOffset(rand.New(rand.NewSource(tl.shuffleSeed)), currentPos)
		} else {
			// at the start: shuffle the entire list
			tl.shuffleStartPos = 0
			tl.tracks.shuffle(rand.New(rand.NewSource(tl.shuffleSeed)))
		}

		// move current track to position 0
		if tl.tracks.pos > 0 {
			tl.shuffleKeep = tl.tracks.pos
			tl.tracks.swap(0, tl.tracks.pos)
		} else {
			tl.shuffleKeep = -1
		}

		// save tracks list length
		tl.shuffleLen = tl.tracks.len()

		tl.shuffled = true
		tl.log.Debugf("shuffled context with seed %d (len: %d, keep: %d, offset: %d)", tl.shuffleSeed, tl.shuffleLen, tl.shuffleKeep, tl.shuffleStartPos)
		return nil
	} else {
		if tl.shuffleSeed != 0 && tl.tracks.len() == tl.shuffleLen {
			// restore track that was originally moved to first
			if tl.shuffleKeep > 0 {
				tl.tracks.swap(0, tl.shuffleKeep)
			}

			// partial unshuffle: only reverse the shuffled portion
			if tl.shuffleStartPos > 0 {
				tl.tracks.unshuffleFromOffset(rand.New(rand.NewSource(tl.shuffleSeed)), tl.shuffleStartPos)
			} else {
				tl.tracks.unshuffle(rand.New(rand.NewSource(tl.shuffleSeed)))
			}

			tl.shuffled = false
			tl.log.Debugf("unshuffled context with seed %d (len: %d, keep: %d, offset: %d)", tl.shuffleSeed, tl.shuffleLen, tl.shuffleKeep, tl.shuffleStartPos)
			return nil
		} else {
			// fallback: context changed or track list length differs
			// this should rarely happen with partial shuffle since the
			// seed+length check is more robust when only the unplayed portion is shuffled
			currentTrack := tl.current()

			tl.tracks.clear()
			if err := tl.Seek(ctx, ContextTrackComparator(tl.ctx.Type(), currentTrack)); err != nil {
				return fmt.Errorf("failed seeking to current track: %w", err)
			}

			tl.shuffled = false
			tl.log.Debugf("unshuffled context by fetching pages (len: %d)", tl.tracks.len())
			return nil
		}
	}
}
