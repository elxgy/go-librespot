package catalog

const (
	ContextKindPlaylist = "playlist"
	ContextKindAlbum    = "album"
)

type Summary struct {
	ID            string
	Name          string
	URI           string
	Kind          string
	Owner         string
	OwnerID       string
	Collaborative bool
	TrackCount    int
	ImageURL      string
}

type Page struct {
	Offset     int
	Limit      int
	NextOffset int
	HasMore    bool
	Items      []Summary
}

type TrackInfo struct {
	ID         string
	Name       string
	Artist     string
	DurationMS int
}

type TrackPage struct {
	Offset     int
	Limit      int
	NextOffset int
	HasMore    bool
	TrackIDs   []string
	TrackInfos []TrackInfo
}
