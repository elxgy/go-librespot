package go_librespot

import (
	"time"

	connectpb "github.com/elxgy/go-librespot/proto/spotify/connectstate"
	devicespb "github.com/elxgy/go-librespot/proto/spotify/connectstate/devices"
)

const maxReasonableElapsed = 10 * 60 * 1000 // 10 minutes

// MaxStateVolume is the maximum volume value used in DeviceInfo and PlayerState.
// Duplicated from player package to avoid circular imports.
const MaxStateVolume uint32 = 65535

// TrackPosition computes the current playback position in milliseconds
// from the PlayerState's timestamp and position fields. It handles paused/playback
// state, stale timestamps, and negative values. If durationMs > 0, the result
// is clamped to not exceed the duration.
func TrackPosition(state *connectpb.PlayerState, durationMs int64) int64 {
	if state == nil {
		return 0
	}
	if state.IsPaused || !state.IsPlaying {
		pos := state.PositionAsOfTimestamp
		if durationMs > 0 && pos > durationMs {
			pos = durationMs
		}
		return pos
	}

	now := time.Now().UnixMilli()
	elapsed := now - state.Timestamp
	if elapsed > maxReasonableElapsed || elapsed < 0 {
		pos := state.PositionAsOfTimestamp
		if durationMs > 0 && pos > durationMs {
			pos = durationMs
		}
		return pos
	}

	calculated := state.PositionAsOfTimestamp + elapsed
	if calculated < 0 {
		pos := state.PositionAsOfTimestamp
		if durationMs > 0 && pos > durationMs {
			pos = durationMs
		}
		return pos
	}
	if durationMs > 0 && calculated > durationMs {
		return durationMs
	}
	return calculated
}

// UpdateTimestamp refreshes the PlayerState's timestamp and position fields
// to reflect how much playback has advanced since the last update.
// If durationMs > 0, the position is clamped to not exceed the duration.
func UpdateTimestamp(state *connectpb.PlayerState, durationMs int64) {
	if state == nil {
		return
	}
	now := time.Now()
	advancedTimeMillis := now.UnixMilli() - state.Timestamp
	advancedPositionMillis := int64(float64(advancedTimeMillis) * state.PlaybackSpeed)
	state.PositionAsOfTimestamp += advancedPositionMillis
	state.Timestamp = now.UnixMilli()
	if durationMs > 0 && state.PositionAsOfTimestamp > durationMs {
		state.PositionAsOfTimestamp = durationMs
	}
}

// SetPaused sets the IsPaused flag and PlaybackSpeed on a PlayerState.
// PlaybackSpeed must be 0 when paused, or Spotify Android will have subtle bugs.
func SetPaused(state *connectpb.PlayerState, paused bool) {
	if state == nil {
		return
	}
	state.IsPaused = paused
	if paused {
		state.PlaybackSpeed = 0
	} else {
		state.PlaybackSpeed = 1
	}
}

// NewPlayerState creates a fresh PlayerState with sensible defaults.
func NewPlayerState() *connectpb.PlayerState {
	return &connectpb.PlayerState{
		IsSystemInitiated: true,
		PlaybackSpeed:     1,
		PlayOrigin:        &connectpb.PlayOrigin{},
		Suppressions:      &connectpb.Suppressions{},
		Options:           &connectpb.ContextPlayerOptions{},
	}
}

// DeviceInfoOpts holds consumer-specific values for DefaultDeviceInfo.
type DeviceInfoOpts struct {
	DeviceName      string
	DeviceId        string
	DeviceType      devicespb.DeviceType
	ClientId        string
	VolumeSteps     uint32
	ZeroconfEnabled bool
}

// DefaultDeviceInfo creates a DeviceInfo with standard capabilities for a Spotify
// Connect device. Only consumer-specific values need to be provided via opts.
func DefaultDeviceInfo(opts DeviceInfoOpts) *connectpb.DeviceInfo {
	return &connectpb.DeviceInfo{
		CanPlay:               true,
		Volume:                MaxStateVolume,
		Name:                  opts.DeviceName,
		DeviceId:              opts.DeviceId,
		DeviceType:            opts.DeviceType,
		DeviceSoftwareVersion: VersionString(),
		ClientId:              opts.ClientId,
		SpircVersion:          "3.2.6",
		Capabilities: &connectpb.Capabilities{
			CanBePlayer:                true,
			RestrictToLocal:            false,
			GaiaEqConnectId:            true,
			SupportsLogout:             opts.ZeroconfEnabled,
			IsObservable:               true,
			VolumeSteps:                int32(opts.VolumeSteps),
			SupportedTypes:             []string{"audio/track", "audio/episode"},
			CommandAcks:                true,
			SupportsRename:             false,
			Hidden:                     false,
			DisableVolume:              false,
			ConnectDisabled:            false,
			SupportsPlaylistV2:         true,
			IsControllable:             true,
			SupportsExternalEpisodes:   false,
			SupportsSetBackendMetadata: true,
			SupportsTransferCommand:    true,
			SupportsCommandRequest:     true,
			IsVoiceEnabled:             false,
			NeedsFullPlayerState:       false,
			SupportsGzipPushes:         true,
			SupportsSetOptionsCommand:  true,
			SupportsHifi:               nil,
			ConnectCapabilities:        "",
		},
	}
}

// PutStateOpts holds the values needed to build a PutStateRequest.
type PutStateOpts struct {
	Device                    *connectpb.DeviceInfo
	PlayerState               *connectpb.PlayerState
	Active                    bool
	ActiveSince               time.Time
	LastCommandMsgId          uint32
	LastCommandSentByDeviceId string
	HasBeenPlayingForMs       uint64
}

// BuildPutStateRequest constructs a PutStateRequest for the given reason.
// For BECAME_INACTIVE, callers should use Spclient.PutConnectStateInactive directly.
func BuildPutStateRequest(opts PutStateOpts, reason connectpb.PutStateReason) *connectpb.PutStateRequest {
	req := &connectpb.PutStateRequest{
		ClientSideTimestamp: uint64(time.Now().UnixMilli()),
		MemberType:          connectpb.MemberType_CONNECT_STATE,
		PutStateReason:      reason,
	}

	if !opts.ActiveSince.IsZero() {
		req.StartedPlayingAt = uint64(opts.ActiveSince.UnixMilli())
	}
	if opts.HasBeenPlayingForMs > 0 {
		req.HasBeenPlayingForMs = opts.HasBeenPlayingForMs
	}

	req.IsActive = opts.Active
	req.Device = &connectpb.Device{
		DeviceInfo:  opts.Device,
		PlayerState: opts.PlayerState,
	}

	if opts.LastCommandMsgId != 0 {
		req.LastCommandMessageId = opts.LastCommandMsgId
		req.LastCommandSentByDeviceId = opts.LastCommandSentByDeviceId
	}

	return req
}

// PlayOrigin returns the FeatureIdentifier from a PlayerState's PlayOrigin.
func PlayOrigin(state *connectpb.PlayerState) string {
	if state == nil || state.PlayOrigin == nil {
		return ""
	}
	return state.PlayOrigin.FeatureIdentifier
}
