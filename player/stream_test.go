package player

import (
	"errors"
	"io"
	"testing"
)

type closeCounter struct {
	closed bool
}

func (c *closeCounter) Close() error {
	c.closed = true
	return nil
}

type errCloser struct {
	err error
}

func (e *errCloser) Close() error {
	return e.err
}

type testSource struct {
	closed bool
}

func (s *testSource) Read(p []float32) (int, error) { return 0, io.EOF }
func (s *testSource) Close() error {
	s.closed = true
	return nil
}
func (s *testSource) SetPositionMs(int64) error { return nil }
func (s *testSource) PositionMs() int64        { return 0 }

func TestStreamCloseClosesSourceAndClosers(t *testing.T) {
	src := &testSource{}
	c1 := &closeCounter{}
	c2 := &closeCounter{}
	s := &Stream{
		Source:  src,
		closers: []io.Closer{c1, c2},
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if !src.closed {
		t.Error("expected Source to be closed")
	}
	if !c1.closed || !c2.closed {
		t.Error("expected all closers to be closed")
	}
}

func TestStreamCloseJoinsErrors(t *testing.T) {
	src := &testSource{}
	errA := errors.New("a")
	errB := errors.New("b")
	s := &Stream{
		Source:  src,
		closers: []io.Closer{&errCloser{err: errA}, &errCloser{err: errB}},
	}
	err := s.Close()
	if err == nil {
		t.Fatal("expected non-nil error from Close")
	}
	if !errors.Is(err, errA) || !errors.Is(err, errB) {
		t.Fatalf("expected joined errors to contain errA and errB, got %v", err)
	}
}

func TestStreamCloseIdempotent(t *testing.T) {
	src := &testSource{}
	c := &closeCounter{}
	s := &Stream{
		Source:  src,
		closers: []io.Closer{c},
	}
	_ = s.Close()
	_ = s.Close()
	if !src.closed || !c.closed {
		t.Fatal("expected Close to leave state closed after second call")
	}
}

func TestStreamCloseNilSafe(t *testing.T) {
	s := &Stream{}
	if err := s.Close(); err != nil {
		t.Fatalf("Close on empty Stream should not error, got %v", err)
	}
}