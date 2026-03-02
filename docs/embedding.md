# Embedding go-librespot

This document describes how to embed go-librespot in your own application (e.g. a TUI, custom daemon, or other client) instead of using the built-in daemon.

## Session creation

### Option 1: Session from config directory

Use the `sessionconfig` package to create a session from a config directory. It loads persisted state (device ID, credentials), creates the session, and optionally persists new credentials after interactive login.

```go
import (
    "github.com/devgianlu/go-librespot"
    "github.com/devgianlu/go-librespot/sessionconfig"
)

sess, appState, err := sessionconfig.NewSessionFromConfigDir(ctx, log, sessionconfig.Options{
    ConfigDir:    configDir,
    CallbackPort: 8080,
    DeviceType:   "computer",
    ClientToken:  "",
    ClientId:     "",  // optional; leave empty for default librespot client ID
    Credentials:  nil, // nil = use stored creds from appState, or interactive if none
})
```

To use a **single OAuth flow** (e.g. your app already has a Spotify token from PKCE), pass the same client ID and token credentials:

```go
import "github.com/devgianlu/go-librespot/session"

sess, appState, err := sessionconfig.NewSessionFromConfigDir(ctx, log, sessionconfig.Options{
    ConfigDir:    configDir,
    CallbackPort: 8080,
    DeviceType:   "computer",
    ClientId:     yourSpotifyClientID,
    Credentials:  &session.SpotifyTokenCredentials{
        Username: username,
        Token:    accessToken,
    },
})
```

Then use one login (e.g. `orpheus auth login`) and pass that token when creating the session; no separate librespot interactive auth is needed.

### Option 2: Manual session options

Build `session.Options` yourself and call `session.NewSessionFromOptions`. Set `Options.ClientId` if you use a custom Spotify app. Use `Session.ClientId()` and `Session.DeviceId()` when building device state so it matches the session.

## Track list and context size

When creating a track list from a context, pass the **fifth argument** for how many prev/next tracks to load:

```go
import "github.com/devgianlu/go-librespot/tracks"

list, err := tracks.NewTrackListFromContext(ctx, log, sp, spotCtx, maxTracksInContext)
```

- Use `0` for the default (32 tracks).
- Use a positive value (e.g. 64, 100) to request more; the Connect API accepts larger lists, but you can test for your use case.

## Logger: Logrus

If you use Logrus, implement the library `Logger` interface with the provided adapter:

```go
import (
    librespot "github.com/devgianlu/go-librespot"
    "github.com/sirupsen/logrus"
)

log := logrus.New()
// ... configure log ...
logger := &librespot.LogrusAdapter{Log: logrus.NewEntry(log)}
```

Pass `logger` wherever the library expects a `librespot.Logger` (session, player, etc.).

## Web API and 429 retries

The session exposes `WebApi` for raw HTTP calls to the Spotify Web API. For endpoints that may return 429 (rate limit), use the retry helper:

```go
resp, err := sess.WebApiWith429Retry(ctx, "GET", "v1/me", nil, nil, nil)
if err != nil {
    return err
}
defer resp.Body.Close()
// ...
```

Alternatively, use `WebApiWith429RetryAndReadBody` to get the response body and status code in one call (body read is capped at 512KB).

## Images and product info

- **Best image for size:** use `librespot.GetBestImageIdForSize(images, size)` with `size` one of `"default"`, `"small"`, `"large"`, `"xlarge"`. Empty string is treated as default.
- **Product info (AP):** the `ap` package provides `ap.ProductInfo` (unmarshal from the ProductInfo AP packet XML) and `ImageUrl(fileId []byte) *string` to build image URLs from the template.

## Summary of embedder-facing APIs

| Need | API |
|------|-----|
| Session from config dir | `sessionconfig.NewSessionFromConfigDir(ctx, log, sessionconfig.Options{...})` |
| Custom client ID | `session.Options.ClientId`, then `sess.ClientId()` for device state |
| Token-only auth | `session.SpotifyTokenCredentials{Username, Token}` in Options.Credentials |
| Device ID | `sess.DeviceId()` |
| Track list size | `tracks.NewTrackListFromContext(..., maxTracksInContext)` (use 0 for default 32) |
| Logrus logger | `librespot.LogrusAdapter{Log: logrus.NewEntry(log)}` |
| Web API with 429 retry | `sess.WebApiWith429Retry(ctx, method, path, query, header, body)` |
| Image size selection | `librespot.GetBestImageIdForSize(images, size)` |
| Product info / image URL | `ap.ProductInfo`, `pi.ImageUrl(fileId)` |
