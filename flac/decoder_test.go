//go:build test_unit

package flac_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"

	librespot "github.com/elxgy/go-librespot"
	"github.com/elxgy/go-librespot/flac"
)

// The API reports bit depth from the decoder, so it has to come off STREAMINFO.
// Ported media-format reporting (Stream.SampleRate/BitDepth) is a later upstream
// feature; until then only the Channels field is asserted here.
func TestDecoderStreamInfo(t *testing.T) {
	data, err := os.ReadFile("testdata/sine16.flac")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	d, err := flac.New(&librespot.NullLogger{}, bytes.NewReader(data), 1.0)
	if err != nil {
		t.Fatalf("failed to create decoder: %v", err)
	}
	defer func() { _ = d.Close() }()

	if d.Channels != 2 {
		t.Errorf("Channels = %d, want 2", d.Channels)
	}
}

func TestDecoderFullScalePeak(t *testing.T) {
	data, err := os.ReadFile("testdata/sine16.flac")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	d, err := flac.New(&librespot.NullLogger{}, bytes.NewReader(data), 1.0)
	if err != nil {
		t.Fatalf("failed to create decoder: %v", err)
	}
	defer func() { _ = d.Close() }()

	var peak float32
	buf := make([]float32, 4096)
	for {
		n, err := d.Read(buf)
		for _, v := range buf[:n] {
			if v < 0 {
				v = -v
			}
			if v > peak {
				peak = v
			}
		}
		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("failed to read samples: %v", err)
		}
	}

	want := float32(32767) / float32(32768)
	if peak != want {
		t.Fatalf("peak = %f, want %f", peak, want)
	}
}

func TestPositionTracksDecodedSamples(t *testing.T) {
	data, err := os.ReadFile("testdata/sine16.flac")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	d, err := flac.New(&librespot.NullLogger{}, bytes.NewReader(data), 1.0)
	if err != nil {
		t.Fatalf("failed to create decoder: %v", err)
	}
	defer func() { _ = d.Close() }()

	buf := make([]float32, 4096)
	var readSamples int
	for {
		n, err := d.Read(buf)
		readSamples += n
		if err != nil {
			break
		}
	}
	if readSamples == 0 {
		t.Fatal("expected to decode samples")
	}

	// SampleRate is not exposed until media-format reporting is ported; the
	// fixture is 16-bit stereo, so readSamples/2 equals decoded frames.
	got := d.PositionMs()
	want := int64(readSamples/2) * 1000 / int64(44100)
	if want == 0 {
		t.Fatal("fixture produced zero expected duration")
	}
	if got < want/2 || got > want*2 {
		t.Fatalf("PositionMs = %d, want ~%d (readSamples=%d)", got, want, readSamples)
	}
}

func TestPositionAnchoredAfterSeek(t *testing.T) {
	data, err := os.ReadFile("testdata/sine16.flac")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	d, err := flac.New(&librespot.NullLogger{}, bytes.NewReader(data), 1.0)
	if err != nil {
		t.Fatalf("failed to create decoder: %v", err)
	}
	defer func() { _ = d.Close() }()

	// Drain a little so the position advances past the start; the fixture
	// is only ~1000 frames, so keep reads small.
	buf := make([]float32, 256)
	if _, err := d.Read(buf); err != nil {
		t.Fatalf("read failed: %v", err)
	}

	// Right after a successful seek the counter is anchored at the target:
	// PositionMs must equal the seek target exactly (decodedSamples == 0).
	if err := d.SetPositionMs(5); err != nil {
		t.Fatalf("seek failed: %v", err)
	}
	// The ms->samples->ms round-trip is lossy by up to 1ms of integer division.
	if got := d.PositionMs(); got < 4 || got > 5 {
		t.Fatalf("PositionMs after seek to 5ms = %d, want ~5", got)
	}

	// Position must keep advancing monotonically while decoding.
	prev := int64(4)
	for range 3 {
		if _, err := d.Read(buf); err != nil {
			break
		}
		pos := d.PositionMs()
		if pos < prev {
			t.Fatalf("PositionMs went backwards: %d -> %d", prev, pos)
		}
		prev = pos
	}
}
