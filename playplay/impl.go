package playplay

import (
	"github.com/elxgy/go-librespot/playplay/impl"
	"github.com/elxgy/go-librespot/playplay/plugin"
)

var Plugin plugin.Interface = impl.Impl{}
