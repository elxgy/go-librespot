package session

import (
	"context"
	"github.com/elxgy/go-librespot/mercury"
	"github.com/elxgy/go-librespot/player"
	"net/http"
	"net/url"

	"github.com/elxgy/go-librespot/ap"
	"github.com/elxgy/go-librespot/audio"
	"github.com/elxgy/go-librespot/dealer"
	"github.com/elxgy/go-librespot/spclient"
)

func (s *Session) ClientId() string {
	return s.clientId
}

func (s *Session) DeviceId() string {
	return s.deviceId
}

func (s *Session) Username() string {
	return s.ap.Username()
}

func (s *Session) StoredCredentials() []byte {
	return s.ap.StoredCredentials()
}

func (s *Session) Spclient() *spclient.Spclient {
	return s.sp
}

func (s *Session) Events() player.EventManager {
	return s.events
}

func (s *Session) AudioKey() *audio.KeyProvider {
	return s.audioKey
}

func (s *Session) Dealer() *dealer.Dealer {
	return s.dealer
}

func (s *Session) Accesspoint() *ap.Accesspoint {
	return s.ap
}

func (s *Session) Mercury() *mercury.Client {
	return s.hg
}

func (s *Session) WebApi(ctx context.Context, method string, path string, query url.Values, header http.Header, body []byte) (*http.Response, error) {
	return s.sp.WebApiRequest(ctx, method, path, query, header, body)
}
