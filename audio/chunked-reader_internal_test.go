//go:build test_unit

package audio

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	librespot "github.com/elxgy/go-librespot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseContentRange(t *testing.T) {
	tests := []struct {
		name        string
		header      string
		wantStart   int64
		wantEnd     int64
		wantSize    int64
		wantErr     bool
		errContains string
	}{
		{
			name:      "valid range",
			header:    "bytes 0-511/1024",
			wantStart: 0,
			wantEnd:   511,
			wantSize:  1024,
			wantErr:   false,
		},
		{
			name:      "middle range",
			header:    "bytes 512-1023/2048",
			wantStart: 512,
			wantEnd:   1023,
			wantSize:  2048,
			wantErr:   false,
		},
		{
			name:        "no header",
			header:      "",
			wantErr:     true,
			errContains: "no Content-Range header",
		},
		{
			name:        "invalid format",
			header:      "invalid format",
			wantErr:     true,
			errContains: "invalid content range header",
		},
		{
			name:        "invalid start",
			header:      "bytes abc-511/1024",
			wantErr:     true,
			errContains: "invalid content range header",
		},
		{
			name:        "invalid end",
			header:      "bytes 0-abc/1024",
			wantErr:     true,
			errContains: "invalid content range header",
		},
		{
			name:        "invalid size",
			header:      "bytes 0-511/abc",
			wantErr:     true,
			errContains: "invalid content range header",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{
				Header: http.Header{},
			}
			if tt.header != "" {
				resp.Header.Set("Content-Range", tt.header)
			}

			start, end, size, err := parseContentRange(resp)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantStart, start)
				assert.Equal(t, tt.wantEnd, end)
				assert.Equal(t, tt.wantSize, size)
			}
		})
	}
}

func TestCloseCancelsConcurrentFetchChunkCallers(t *testing.T) {
	transport, started := newBlockingRoundTripper()
	reader := newFetchTestReader(t, transport)

	errCh := make(chan error, 2)
	go func() {
		_, err := reader.fetchChunk(0)
		errCh <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first fetchChunk did not start downloading")
	}

	go func() {
		_, err := reader.fetchChunk(0)
		errCh <- err
	}()

	closeErrCh := make(chan error, 1)
	go func() {
		closeErrCh <- reader.Close()
	}()

	select {
	case err := <-closeErrCh:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Close did not return")
	}

	for i := 0; i < 2; i++ {
		select {
		case err := <-errCh:
			require.ErrorIs(t, err, net.ErrClosed)
		case <-time.After(time.Second):
			t.Fatal("fetchChunk did not return")
		}
	}
}

func TestCloseCancelsInFlightFetchChunk(t *testing.T) {
	transport, started := newBlockingRoundTripper()
	testCloseCancelsFetchChunk(t, transport, started, time.Second)
}

func TestCloseCancelsRetryBackoffSleep(t *testing.T) {
	transport, started := newRetryThenBlockRoundTripper()
	testCloseCancelsFetchChunk(t, transport, started, 250*time.Millisecond)
}

func TestCloseBeforeChunkPublishesReturnsErrClosed(t *testing.T) {
	chunkReader := strings.NewReader("chunk")
	beforeEOF := make(chan struct{})
	release := make(chan struct{})

	body := io.NopCloser(readerFunc(func(p []byte) (int, error) {
		n, err := chunkReader.Read(p)
		if err == io.EOF {
			close(beforeEOF)
			<-release
		}

		return n, err
	}))

	reader := newFetchTestReader(t, roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusPartialContent,
			Status:     "206 Partial Content",
			Body:       body,
			Header:     http.Header{},
		}, nil
	}))

	errCh := make(chan error, 1)
	go func() {
		_, err := reader.fetchChunk(0)
		errCh <- err
	}()

	select {
	case <-beforeEOF:
	case <-time.After(time.Second):
		t.Fatal("fetchChunk did not finish reading chunk data")
	}

	require.NoError(t, reader.Close())
	close(release)

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, net.ErrClosed)
	case <-time.After(time.Second):
		t.Fatal("fetchChunk did not return")
	}
}

func TestCloseNormalizesBodyReadTransportErrors(t *testing.T) {
	reader := newFetchTestReader(t, nil)

	started := make(chan struct{})
	var startedOnce sync.Once
	body := io.NopCloser(readerFunc(func([]byte) (int, error) {
		startedOnce.Do(func() {
			close(started)
		})

		<-reader.ctx.Done()
		return 0, errors.New("use of closed network connection")
	}))

	reader.client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusPartialContent,
			Status:     "206 Partial Content",
			Body:       body,
			Header:     http.Header{},
		}, nil
	})

	errCh := make(chan error, 1)
	go func() {
		_, err := reader.fetchChunk(0)
		errCh <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("fetchChunk did not start reading response body")
	}

	require.NoError(t, reader.Close())

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, net.ErrClosed)
	case <-time.After(time.Second):
		t.Fatal("fetchChunk did not return")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type readerFunc func([]byte) (int, error)

func (fn readerFunc) Read(p []byte) (int, error) {
	return fn(p)
}

func newFetchTestReader(t *testing.T, transport http.RoundTripper) *HttpChunkedReader {
	t.Helper()

	chunkURL, err := url.Parse("https://example.com/audio")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &HttpChunkedReader{
		log:    &librespot.NullLogger{},
		client: &http.Client{Transport: transport},
		url:    chunkURL,
		len:    DefaultChunkSize,
		chunks: []*chunkItem{newChunkItem()},
		ctx:    ctx,
		cancel: cancel,
	}
}

func newBlockingRoundTripper() (http.RoundTripper, <-chan struct{}) {
	started := make(chan struct{})
	var startedOnce sync.Once

	return roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		startedOnce.Do(func() {
			close(started)
		})

		<-req.Context().Done()
		return nil, req.Context().Err()
	}), started
}

func newRetryThenBlockRoundTripper() (http.RoundTripper, <-chan struct{}) {
	started := make(chan struct{})
	attempts := 0

	return roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			close(started)
			return nil, errors.New("transient")
		}

		<-req.Context().Done()
		return nil, req.Context().Err()
	}), started
}

func testCloseCancelsFetchChunk(t *testing.T, transport http.RoundTripper, started <-chan struct{}, closeTimeout time.Duration) {
	t.Helper()

	reader := newFetchTestReader(t, transport)

	errCh := make(chan error, 1)
	go func() {
		_, err := reader.fetchChunk(0)
		errCh <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("fetchChunk did not start downloading")
	}

	closeErrCh := make(chan error, 1)
	go func() {
		closeErrCh <- reader.Close()
	}()

	select {
	case err := <-closeErrCh:
		require.NoError(t, err)
	case <-time.After(closeTimeout):
		t.Fatal("Close did not return")
	}

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, net.ErrClosed)
	case <-time.After(2 * time.Second):
		t.Fatal("fetchChunk did not return")
	}
}

func TestOnCompleteFiresOnceAllChunksPresent(t *testing.T) {
	reader := newFetchTestReader(t, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusPartialContent,
			Header:     http.Header{"Content-Range": []string{"bytes 0-1023/1024"}},
			Body:       io.NopCloser(bytes.NewReader(make([]byte, 1024))),
		}, nil
	}))

	// The constructor counts the eagerly fetched first chunk.
	reader.completedChunks = 1

	fired := make(chan struct{}, 1)
	reader.OnComplete(func(_ io.ReaderAt, size int64) {
		require.EqualValues(t, DefaultChunkSize, size)
		fired <- struct{}{}
	})

	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("OnComplete did not fire for a fully present single-chunk reader")
	}

	require.Equal(t, 1, reader.completedChunks)
	require.True(t, reader.onCompleteFired)
}

func TestOnCompleteNeverFiresAfterClose(t *testing.T) {
	transport, started := newBlockingRoundTripper()
	reader := newFetchTestReader(t, transport)

	go reader.fetchChunk(0)

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("fetchChunk did not start downloading")
	}

	require.NoError(t, reader.Close())

	fired := make(chan struct{}, 1)
	reader.OnComplete(func(_ io.ReaderAt, _ int64) {
		fired <- struct{}{}
	})

	select {
	case <-fired:
		t.Fatal("OnComplete fired after close")
	case <-time.After(100 * time.Millisecond):
	}
}
