package player

import (
	"bytes"
	"errors"
	"io"

	librespot "github.com/elxgy/go-librespot"
	metadatapb "github.com/elxgy/go-librespot/proto/spotify/metadata"
)

type Stream struct {
	PlaybackId []byte

	Source librespot.AudioSource
	Media  *librespot.Media
	File   *metadatapb.AudioFile

	closers []io.Closer
}

func (s *Stream) Is(id librespot.SpotifyId) bool {
	if id.Type() == librespot.SpotifyIdTypeTrack && s.Media.IsTrack() {
		return bytes.Equal(id.Id(), s.Media.Track().Gid)
	} else if id.Type() == librespot.SpotifyIdTypeEpisode && s.Media.IsEpisode() {
		return bytes.Equal(id.Id(), s.Media.Episode().Gid)
	} else {
		return false
	}
}

func (s *Stream) Close() error {
	var err error
	if closer, ok := s.Source.(io.Closer); ok {
		err = closer.Close()
	}
	for _, c := range s.closers {
		err = errors.Join(err, c.Close())
	}
	return err
}
