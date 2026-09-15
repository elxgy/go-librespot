package player

import (
	"io"
	"sync"
	"testing"
)

type countingSource struct {
	mu     sync.Mutex
	closes int
}

func (s *countingSource) Read(p []float32) (int, error) { return 0, io.EOF }
func (s *countingSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closes++
	return nil
}
func (s *countingSource) SetPositionMs(int64) error { return nil }
func (s *countingSource) PositionMs() int64         { return 0 }

func (s *countingSource) closeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closes
}

func TestSetPrimaryClosesDisplacedSource(t *testing.T) {
	s := NewSwitchingAudioSource()
	first := &countingSource{}
	second := &countingSource{}

	s.SetPrimary(first)
	s.SetPrimary(second)

	if first.closeCount() != 1 {
		t.Fatalf("expected displaced primary to be closed once, got %d", first.closeCount())
	}
	if second.closeCount() != 0 {
		t.Fatalf("expected current primary to stay open, got %d closes", second.closeCount())
	}

	// Setting the same source again must not close it.
	s.SetPrimary(second)
	if second.closeCount() != 0 {
		t.Fatalf("expected re-set of same source to not close it")
	}
}

func TestSetSecondaryClosesDisplacedSource(t *testing.T) {
	s := NewSwitchingAudioSource()
	first := &countingSource{}
	second := &countingSource{}

	s.SetSecondary(first)
	s.SetSecondary(second)

	if first.closeCount() != 1 {
		t.Fatalf("expected displaced secondary to be closed once, got %d", first.closeCount())
	}
	if second.closeCount() != 0 {
		t.Fatalf("expected current secondary to stay open, got %d closes", second.closeCount())
	}
}

func TestSetPrimaryDoesNotCloseSourceInOtherSlot(t *testing.T) {
	s := NewSwitchingAudioSource()
	primary := &countingSource{}
	secondary := &countingSource{}

	s.SetPrimary(primary)
	s.SetSecondary(secondary)
	s.SetPrimary(&countingSource{})

	// The old primary is displaced (closed), but the secondary slot is untouched.
	if secondary.closeCount() != 0 {
		t.Fatalf("expected secondary to stay open, got %d closes", secondary.closeCount())
	}
}
