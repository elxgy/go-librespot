//go:build test_unit

package audio_test

import (
	"context"
	"testing"

	librespot "github.com/elxgy/go-librespot"
	"github.com/elxgy/go-librespot/ap"
	"github.com/elxgy/go-librespot/audio"
	"github.com/stretchr/testify/require"
)

func TestRequestFailsWhenAccesspointIsClosed(t *testing.T) {
	accesspoint := ap.NewAccesspoint(&librespot.NullLogger{}, nil, "", context.Background())
	accesspoint.Close()

	provider := audio.NewAudioKeyProvider(&librespot.NullLogger{}, accesspoint, context.Background())

	_, err := provider.Request(context.Background(), nil, nil)
	require.ErrorIs(t, err, ap.ErrAccesspointClosed)
}
