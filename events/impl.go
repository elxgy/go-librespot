package events

import (
	"github.com/elxgy/go-librespot/events/impl"
	"github.com/elxgy/go-librespot/events/plugin"
)

var Plugin plugin.Interface = impl.Impl{}
