package player

import (
	"errors"
	"io"
	"sync"

	librespot "github.com/elxgy/go-librespot"
)

type SwitchingAudioSource struct {
	source map[bool]librespot.AudioSource
	which  bool
	cond   *sync.Cond

	done chan struct{}
}

func NewSwitchingAudioSource() *SwitchingAudioSource {
	return &SwitchingAudioSource{
		source: map[bool]librespot.AudioSource{},
		cond:   sync.NewCond(&sync.Mutex{}),
		done:   make(chan struct{}, 1),
	}
}

func (s *SwitchingAudioSource) SetPrimary(source librespot.AudioSource) {
	s.cond.L.Lock()
	old := s.source[s.which]
	s.source[s.which] = source
	s.cond.Broadcast()
	s.cond.L.Unlock()

	// Close the displaced decoder outside the lock: decoder Close waits on
	// the decoder's own mutex, which an in-flight Read holds for as long as
	// the decode+network read takes. Holding cond.L here would block
	// pause/seek/position for that duration. A concurrent Read of the
	// displaced source is safe: it re-validates the source identity after
	// re-locking.
	if old != nil && old != source {
		_ = old.Close()
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
		select {
		case s.done <- struct{}{}:
		default:
		}

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

func (s *SwitchingAudioSource) SetPositionMs(pos int64) error {
	s.cond.L.Lock()
	defer s.cond.L.Unlock()

	if s.source[s.which] == nil {
		return nil
	}

	return s.source[s.which].SetPositionMs(pos)
}

func (s *SwitchingAudioSource) PositionMs() int64 {
	s.cond.L.Lock()
	defer s.cond.L.Unlock()

	if s.source[s.which] == nil {
		return 0
	}

	return s.source[s.which].PositionMs()
}

func (s *SwitchingAudioSource) Close() error {
	s.cond.L.Lock()
	defer s.cond.L.Unlock()

	var err error
	for _, which := range []bool{true, false} {
		if s.source[which] != nil {
			err = errors.Join(err, s.source[which].Close())
		}
		delete(s.source, which)
	}
	return err
}
