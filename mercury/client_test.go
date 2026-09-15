//go:build test_unit

package mercury_test

import (
	"context"
	"testing"

	librespot "github.com/elxgy/go-librespot"
	"github.com/elxgy/go-librespot/ap"
	"github.com/elxgy/go-librespot/mercury"
	"github.com/stretchr/testify/require"
)

func TestRequestFailsWhenAccesspointIsClosed(t *testing.T) {
	accesspoint := ap.NewAccesspoint(&librespot.NullLogger{}, nil, "", context.Background())
	accesspoint.Close()

	client := mercury.NewClient(&librespot.NullLogger{}, accesspoint, context.Background())

	_, err := client.Request(context.Background(), "GET", "hm://test", nil, nil)
	require.ErrorIs(t, err, ap.ErrAccesspointClosed)
}
