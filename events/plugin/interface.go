package plugin

import (
	librespot "github.com/elxgy/go-librespot"
	"github.com/elxgy/go-librespot/mercury"
	"github.com/elxgy/go-librespot/player"
	"github.com/elxgy/go-librespot/spclient"
)

type Interface interface {
	NewEventManager(log librespot.Logger, state *librespot.AppState, hg *mercury.Client, sp *spclient.Spclient, username string) (player.EventManager, error)
}
