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
	defer s.cond.L.Unlock()
	s.source[s.which] = source
	s.cond.Broadcast()
}

func (s *SwitchingAudioSource) SetSecondary(source librespot.AudioSource) {
	s.cond.L.Lock()
	defer s.cond.L.Unlock()
	s.source[!s.which] = source
	s.cond.Broadcast()
}

func (s *SwitchingAudioSource) Done() <-chan struct{} {
	return s.done
}

func (s *SwitchingAudioSource) Read(p []float32) (n int, err error) {
	s.cond.L.Lock()
	defer s.cond.L.Unlock()

	for s.source[s.which] == nil {
		s.cond.Wait()
	}

	n, err = s.source[s.which].Read(p)
	if errors.Is(err, io.EOF) {
		// notify this source is done. Non-blocking: if done already has
		// a pending value, manageLoop hasn't consumed it yet so another
		// EventTypeNotPlaying would be redundant.
		select {
		case s.done <- struct{}{}:
		default:
		}

		// if there's no other source just let the EOF through
		if s.source[!s.which] == nil {
			return n, err
		}

		// delete current source and switch to the other one
		if closer, ok := s.source[s.which].(interface{ Close() error }); ok {
			_ = closer.Close()
		}
		delete(s.source, s.which)
		s.which = !s.which

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
		if source, ok := s.source[which].(io.Closer); ok && source != nil {
			err = errors.Join(err, source.Close())
		}
		delete(s.source, which)
	}
	return err
}
