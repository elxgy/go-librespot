package player

import (
	"errors"
	"io"
	"math"
	"sync"

	librespot "github.com/elxgy/go-librespot"
)

// maxSampleValue is the largest sample value that survives the int16 output
// conversion without overflowing (32767/32768). Mixed samples are clamped to
// [-1, maxSampleValue] so a crossfade can never wrap around to a click.
const maxSampleValue = float32(0x7fff) / float32(0x8000)

// lookaheadHeadroom is how many samples beyond the crossfade length are
// buffered. Serving playback only from this headroom keeps a full crossfade
// worth of audio in reserve, so the fade always has its complete length
// available when the end of a track is discovered.
const lookaheadHeadroom = 8 * 1024

type SwitchingAudioSource struct {
	source map[bool]librespot.AudioSource
	which  bool
	cond   *sync.Cond

	done        chan struct{}
	eofReported bool

	// Crossfade state. When crossfadeSamples is zero the source behaves
	// exactly as it did before crossfade support was introduced.
	//
	// While crossfading is enabled, audio is buffered ahead of playback in a
	// ring buffer (buf) holding up to crossfadeSamples plus a small headroom.
	// This lookahead is how the end of the primary source is discovered
	// before it is audible: when the decoder returns EOF, the ring holds the
	// track's tail. If a secondary source is available, the tail is faded out
	// while the secondary fades in with equal-power gains.
	crossfadeSamples int
	buf              []float32 // lookahead ring, len == crossfadeSamples+lookaheadHeadroom
	bufStart         int       // ring read index
	bufLen           int       // samples currently buffered
	primaryEOF       bool      // primary decoder returned EOF, ring holds its tail
	pendingErr       error     // non-EOF read error, delivered once the ring drains
	fading           bool      // mixing ring tail (fade out) with primary slot (fade in)
	fadeTotal        int       // samples of tail at fade start, gain ramp length
	fadePos          int       // samples of tail already mixed
	scratch          []float32 // reusable buffer for fill and fade-in reads
}

// NewSwitchingAudioSource creates an audio source that switches seamlessly
// between a primary and a prefetched secondary source.
//
// If crossfadeSamples is greater than zero, track transitions overlap by up
// to that many samples (interleaved, so frames times channels) with an
// equal-power crossfade. A value of zero disables crossfading and preserves
// gapless behavior.
func NewSwitchingAudioSource(crossfadeSamples int) *SwitchingAudioSource {
	if crossfadeSamples < 0 {
		crossfadeSamples = 0
	}

	// Round down to a whole frame so channels never desynchronize.
	crossfadeSamples -= crossfadeSamples % Channels

	s := &SwitchingAudioSource{
		source:           map[bool]librespot.AudioSource{},
		cond:             sync.NewCond(&sync.Mutex{}),
		done:             make(chan struct{}, 1),
		crossfadeSamples: crossfadeSamples,
	}

	if crossfadeSamples > 0 {
		s.buf = make([]float32, crossfadeSamples+lookaheadHeadroom)
		s.scratch = make([]float32, 8*1024)
	}

	return s
}

func (s *SwitchingAudioSource) SetPrimary(source librespot.AudioSource) {
	s.cond.L.Lock()
	var displaced librespot.AudioSource

	// After a crossfade begins, the incoming source has already been promoted
	// to the primary slot. The embedder acknowledges the very same transition
	// by re-setting the source it was told is now playing: that must not
	// disturb the fade that is still in progress.
	if s.source[s.which] != source {
		// A genuinely new source replaces whatever was there, including any
		// in-flight crossfade tail.
		s.resetCrossfade()
		displaced = s.source[s.which]
		s.source[s.which] = source
	}

	// If the same source was also prefetched into the secondary slot (e.g. a
	// manual skip straight to the prefetched track), drop the alias: switching
	// a source into itself would replay or fade into an exhausted stream.
	if s.source[!s.which] == source {
		delete(s.source, !s.which)
	}

	s.eofReported = false
	s.cond.Broadcast()
	s.cond.L.Unlock()

	// Close the displaced decoder outside the lock: decoder Close waits on
	// the decoder's own mutex, which an in-flight Read holds for as long as
	// the decode+network read takes.
	if displaced != nil && displaced != source {
		_ = displaced.Close()
	}
}

func (s *SwitchingAudioSource) SetSecondary(source librespot.AudioSource) {
	s.cond.L.Lock()
	old := s.source[!s.which]
	s.source[!s.which] = source
	s.cond.Broadcast()
	s.cond.L.Unlock()

	// See SetPrimary: close outside the lock.
	if old != nil && old != source {
		_ = old.Close()
	}
}

func (s *SwitchingAudioSource) Done() <-chan struct{} {
	return s.done
}

func (s *SwitchingAudioSource) Read(p []float32) (n int, err error) {
	if s.crossfadeSamples > 0 {
		return s.readCrossfade(p)
	}
	return s.readDirect(p)
}

// readDirect is the original, crossfade-free read path.
func (s *SwitchingAudioSource) readDirect(p []float32) (n int, err error) {
	s.cond.L.Lock()
	for s.source[s.which] == nil {
		s.cond.Wait()
	}
	source := s.source[s.which]
	which := s.which
	s.cond.L.Unlock()

	// Read without holding the lock: the decoder can block for a long time
	// on decode/network, and holding the lock here would serialize
	// SetPositionMs/PositionMs/Close behind an entire period read.
	n, err = source.Read(p)

	s.cond.L.Lock()
	defer s.cond.L.Unlock()

	if s.source[which] != source {
		// The source was replaced or closed while reading. Report what we
		// got and leave the switch logic to whoever mutated the state.
		return n, err
	}

	if errors.Is(err, io.EOF) {
		// notify this source is done. Non-blocking: if done already has
		// a pending value, manageLoop hasn't consumed it yet so another
		// EventTypeNotPlaying would be redundant.
		s.reportDone()

		// if there's no other source just let the EOF through
		if s.source[!which] == nil {
			return n, err
		}

		// delete current source and switch to the other one
		_ = source.Close()
		delete(s.source, which)
		s.which = !which

		// ignore the EOF, we have mode data
		return n, nil
	} else if err != nil {
		return n, err
	}

	return n, nil
}

// readCrossfade is the read path used when crossfading is enabled. Playback
// is served from a lookahead ring buffer so the end of the current track is
// known before it is audible. While the track is still decoding, a full
// crossfade worth of samples is kept in reserve and playback is served only
// from the headroom above it, guaranteeing the complete fade length is
// available the moment EOF is discovered.
//
// Unlike the original upstream implementation, this fork releases the cond
// lock across every decoder read and re-validates the source identity after
// each read: the same reason readDirect does. Crossfade state is only
// touched while the lock is held; decoder output lands in scratch and is
// copied into the ring under the lock.
func (s *SwitchingAudioSource) readCrossfade(p []float32) (n int, err error) {
	for {
		s.cond.L.Lock()

		if s.fading {
			n, err = s.readFadeLocked(p)
			s.cond.L.Unlock()
			return n, err
		}

		for s.source[s.which] == nil && !(s.primaryEOF && s.bufLen > 0) {
			s.cond.Wait()
		}

		// Refill the lookahead with bounded reads. Decoders greedily fill
		// whatever buffer they are handed, so reads are capped to keep each
		// one short; playback is served as soon as anything is buffered and
		// the ring keeps filling a little further on every call.
		budget := 2 * len(s.scratch)
		for !s.primaryEOF && s.pendingErr == nil && s.bufLen < len(s.buf) &&
			(s.bufLen == 0 || budget > 0) {
			src := s.source[s.which]
			if src == nil {
				break
			}
			which := s.which

			// Cap the read to the ring's free capacity so the copy below
			// never discards samples. Computed under the lock; bufLen only
			// shrinks while the lock is released.
			readCap := min(len(s.freeChunkLocked()), len(s.scratch))

			s.cond.L.Unlock()
			nn, rerr := src.Read(s.scratch[:readCap])
			s.cond.L.Lock()

			if s.source[which] != src {
				// The source was replaced mid-read; discard the data and
				// re-evaluate the (possibly new) state on the next loop.
				continue
			}

			s.appendChunk(s.scratch[:nn])
			budget -= nn
			if errors.Is(rerr, io.EOF) {
				// Frame-align defensively so a torn final frame cannot
				// desynchronize channels during the fade.
				s.bufLen -= s.bufLen % Channels
				s.primaryEOF = true
			} else if rerr != nil {
				// Deliver buffered audio first; the error surfaces once the
				// ring drains, mirroring how the original path returned data
				// alongside errors.
				s.pendingErr = rerr
			}
		}

		if !s.primaryEOF && s.pendingErr == nil {
			// While the track is still decoding, serve only the audio above
			// the crossfade reserve. If the ring has not built up that far
			// yet (right after a start or seek), serve from what exists so
			// playback is never stalled; a track that ends this early simply
			// gets a proportionally shorter fade.
			limit := s.bufLen - s.crossfadeSamples
			if limit <= 0 {
				limit = min(s.bufLen, len(p))
			}
			n = s.popChunk(p, limit)
			s.cond.L.Unlock()
			return n, nil
		}

		if s.pendingErr != nil && !s.primaryEOF {
			if s.bufLen > 0 {
				n = s.popChunk(p, s.bufLen)
				s.cond.L.Unlock()
				return n, nil
			}
			err = s.pendingErr
			s.pendingErr = nil
			s.cond.L.Unlock()
			return 0, err
		}

		if s.source[!s.which] != nil {
			// There is a next track. Any buffered audio beyond the fade
			// length plays out plainly first.
			if s.bufLen > s.crossfadeSamples {
				n = s.popChunk(p, s.bufLen-s.crossfadeSamples)
				s.cond.L.Unlock()
				return n, nil
			}

			// Report the current track as done, close the outgoing decoder
			// (its remaining audio is already buffered) and promote the next
			// one to the primary slot. The buffered tail fades out over it
			// from the very next read.
			s.reportDone()
			outgoing := s.source[s.which]
			delete(s.source, s.which)
			s.which = !s.which
			s.fading = true
			s.fadeTotal = s.bufLen
			s.fadePos = 0
			s.cond.L.Unlock()

			if outgoing != nil {
				_ = outgoing.Close()
			}
			continue
		}

		if s.bufLen == 0 {
			// Tail fully drained and nobody to switch to: this is the end
			// of playback, matching the original EOF timing.
			s.reportDone()
			s.cond.L.Unlock()
			return 0, io.EOF
		}

		// No next track (yet): keep draining the tail. A late prefetch can
		// still upgrade this to a crossfade on a following read.
		n = s.popChunk(p, s.bufLen)
		drained := s.bufLen == 0
		if drained {
			s.reportDone()
		}
		s.cond.L.Unlock()
		if drained {
			return n, io.EOF
		}
		return n, nil
	}
}

// readFadeLocked mixes the buffered tail of the previous track (fading out)
// with the already-promoted primary source (fading in) using equal-power
// gains. The cond lock must be held; it is released across the fade-in
// decoder read and re-validated.
func (s *SwitchingAudioSource) readFadeLocked(p []float32) (n int, err error) {
	want := min(len(p), s.fadeTotal-s.fadePos, len(s.scratch))
	want -= want % Channels
	if want <= 0 {
		// Nothing left to mix (e.g. a zero-length tail).
		s.finishFade()
		return 0, nil
	}

	// Read the fade-in side without holding the lock. A short read simply
	// mixes a shorter chunk; EOF means the next track is shorter than the
	// fade, mix against silence and let the regular EOF handling deal with
	// it once the tail is done.
	var nIn int
	src := s.source[s.which]
	if src != nil {
		which := s.which

		s.cond.L.Unlock()
		var rerr error
		nIn, rerr = src.Read(s.scratch[:want])
		s.cond.L.Lock()

		nIn -= nIn % Channels
		if s.source[which] != src || !s.fading {
			// The fade was reset while reading (manual skip). Discard the
			// data; the caller re-evaluates on the next read.
			return 0, nil
		}
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			return 0, rerr
		}
	}

	chunk := want
	if nIn > 0 {
		chunk = nIn
	}

	for i := 0; i < chunk; i += Channels {
		// One gain per frame, cheap equal-power curves.
		x := float64(s.fadePos+i) / float64(s.fadeTotal)
		gainOut := float32(math.Cos(x * math.Pi / 2))
		gainIn := float32(math.Sin(x * math.Pi / 2))

		for c := 0; c < Channels; c++ {
			sample := s.buf[(s.bufStart+i+c)%len(s.buf)] * gainOut
			if i+c < nIn {
				sample += s.scratch[i+c] * gainIn
			}

			// Clamp so the int16 output conversion cannot overflow.
			if sample > maxSampleValue {
				sample = maxSampleValue
			} else if sample < -1 {
				sample = -1
			}

			p[i+c] = sample
		}
	}

	s.bufStart = (s.bufStart + chunk) % len(s.buf)
	s.bufLen -= chunk
	s.fadePos += chunk

	if s.fadePos >= s.fadeTotal {
		s.finishFade()
	}

	return chunk, nil
}

// finishFade clears all crossfade state, returning to normal lookahead
// buffering of the (already promoted) primary source.
func (s *SwitchingAudioSource) finishFade() {
	s.fading = false
	s.fadeTotal = 0
	s.fadePos = 0
	s.bufStart = 0
	s.bufLen = 0
	s.primaryEOF = false
}

// resetCrossfade drops any lookahead and fade state, for when the current
// source is being replaced entirely (e.g. a manual skip).
func (s *SwitchingAudioSource) resetCrossfade() {
	if s.crossfadeSamples == 0 {
		return
	}

	s.pendingErr = nil
	s.finishFade()
}

// freeChunk returns the largest contiguous writable slice of the ring buffer.
// Only valid while the cond lock is held.
func (s *SwitchingAudioSource) freeChunkLocked() []float32 {
	start := (s.bufStart + s.bufLen) % len(s.buf)
	end := min(start+(len(s.buf)-s.bufLen), len(s.buf))
	return s.buf[start:end]
}

// appendChunk appends scratch into the lookahead ring; scratch capacity is
// capped to the free chunk size so the copy always fits.
func (s *SwitchingAudioSource) appendChunk(data []float32) {
	free := s.freeChunkLocked()
	free = free[:min(len(free), len(data))]
	copy(free, data[:len(free)])
	s.bufLen += len(free)
}

// popChunk copies up to limit buffered samples into p and consumes them from
// the ring. The amount served is frame-aligned so the ring can never end up
// on a torn frame.
func (s *SwitchingAudioSource) popChunk(p []float32, limit int) (n int) {
	want := min(len(p), limit, s.bufLen)
	want -= want % Channels

	for n < want {
		end := min(s.bufStart+(want-n), len(s.buf))
		nn := copy(p[n:want], s.buf[s.bufStart:end])
		s.bufStart = (s.bufStart + nn) % len(s.buf)
		s.bufLen -= nn
		n += nn
	}
	return n
}

func (s *SwitchingAudioSource) reportDone() {
	if s.eofReported {
		return
	}

	s.eofReported = true

	select {
	case s.done <- struct{}{}:
	default:
	}
}

func (s *SwitchingAudioSource) SetPositionMs(pos int64) error {
	s.cond.L.Lock()
	defer s.cond.L.Unlock()

	if s.source[s.which] == nil {
		return nil
	}

	// Seeking invalidates both the lookahead and any fade in progress: the
	// remains of the previous track are dropped and buffering restarts at
	// the new position.
	s.resetCrossfade()

	if err := s.source[s.which].SetPositionMs(pos); err != nil {
		return err
	}

	if s.crossfadeSamples > 0 {
		// The crossfade path can discover EOF and report done well before the
		// audio finishes; a seek revives the source, so re-arm the signal.
		// The disabled path keeps the original behavior untouched.
		s.eofReported = false
	}
	return nil
}

func (s *SwitchingAudioSource) PositionMs() int64 {
	s.cond.L.Lock()
	defer s.cond.L.Unlock()

	if s.source[s.which] == nil {
		return 0
	}

	pos := s.source[s.which].PositionMs()
	if s.crossfadeSamples > 0 && !s.fading {
		// The decoder runs ahead of playback by the buffered lookahead,
		// report where the listener actually is. While fading, the primary
		// slot holds the incoming track which has no lookahead yet.
		pos -= int64(s.bufLen / Channels * 1000 / SampleRate)
		if pos < 0 {
			pos = 0
		}
	}

	return pos
}

func (s *SwitchingAudioSource) Close() error {
	s.cond.L.Lock()
	var err error
	for _, which := range []bool{true, false} {
		if s.source[which] != nil {
			err = errors.Join(err, s.source[which].Close())
		}
		delete(s.source, which)
	}
	s.resetCrossfade()
	s.cond.L.Unlock()
	return err
}
