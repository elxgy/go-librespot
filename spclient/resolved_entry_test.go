//go:build test_unit

package spclient

import (
	"testing"

	metadatapb "github.com/elxgy/go-librespot/proto/spotify/metadata"
)

func ptr[T any](v T) *T {
	return &v
}

func imageWithId(size metadatapb.Image_Size, id []byte) *metadatapb.Image {
	return &metadatapb.Image{FileId: id, Size: &size}
}

func TestResolvedEntryFromTrackExtractsAlbumCover(t *testing.T) {
	defaultSize := metadatapb.Image_DEFAULT
	bigSize := metadatapb.Image_LARGE
	track := &metadatapb.Track{
		Name:     ptr("Song"),
		Artist:   []*metadatapb.Artist{{Name: ptr("Artist")}},
		Duration: ptr(int32(1000)),
		Album: &metadatapb.Album{
			Name:  ptr("Album"),
			Cover: []*metadatapb.Image{imageWithId(bigSize, []byte{1}), imageWithId(defaultSize, []byte{2})},
		},
	}
	entry := resolvedEntryFromTrack(track)
	if entry.Name != "Song" || entry.Artist != "Artist" || entry.DurationMS != 1000 {
		t.Fatalf("unexpected base metadata: %+v", entry)
	}
	if string(entry.AlbumCoverFileId) != string([]byte{2}) {
		t.Fatalf("expected default-size cover id, got %v", entry.AlbumCoverFileId)
	}

	noCover := &metadatapb.Track{Name: ptr("X")}
	if got := resolvedEntryFromTrack(noCover); got.AlbumCoverFileId != nil {
		t.Fatalf("expected nil cover for album-less track, got %v", got.AlbumCoverFileId)
	}

	viaGroup := &metadatapb.Track{
		Name: ptr("Y"),
		Album: &metadatapb.Album{
			CoverGroup: &metadatapb.ImageGroup{Image: []*metadatapb.Image{imageWithId(defaultSize, []byte{9})}},
		},
	}
	if got := resolvedEntryFromTrack(viaGroup); string(got.AlbumCoverFileId) != string([]byte{9}) {
		t.Fatalf("expected cover group fallback, got %v", got.AlbumCoverFileId)
	}
}

func TestResolvedEntryFromEpisodeExtractsCover(t *testing.T) {
	defaultSize := metadatapb.Image_DEFAULT
	ep := &metadatapb.Episode{
		Name:       ptr("Episode"),
		Show:       &metadatapb.Show{Name: ptr("Show")},
		Duration: ptr(int32(2000)),
		CoverImage: &metadatapb.ImageGroup{Image: []*metadatapb.Image{imageWithId(defaultSize, []byte{7})}},
	}
	entry := resolvedEntryFromEpisode(ep)
	if entry.Name != "Episode" || entry.Artist != "Show" || entry.DurationMS != 2000 {
		t.Fatalf("unexpected base metadata: %+v", entry)
	}
	if string(entry.AlbumCoverFileId) != string([]byte{7}) {
		t.Fatalf("expected cover id, got %v", entry.AlbumCoverFileId)
	}
}
