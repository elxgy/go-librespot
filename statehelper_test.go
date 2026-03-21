package go_librespot

import (
	"testing"
	"time"

	connectpb "github.com/elxgy/go-librespot/proto/spotify/connectstate"
	devicespb "github.com/elxgy/go-librespot/proto/spotify/connectstate/devices"
	"github.com/stretchr/testify/assert"
)

func TestTrackPositionPaused(t *testing.T) {
	state := &connectpb.PlayerState{
		IsPaused:              true,
		IsPlaying:             true,
		PositionAsOfTimestamp: 5000,
		Timestamp:             time.Now().UnixMilli() - 10000,
	}
	assert.Equal(t, int64(5000), TrackPosition(state, 0))
}

func TestTrackPositionNotPlaying(t *testing.T) {
	state := &connectpb.PlayerState{
		IsPlaying:             false,
		PositionAsOfTimestamp: 3000,
		Timestamp:             time.Now().UnixMilli() - 10000,
	}
	assert.Equal(t, int64(3000), TrackPosition(state, 0))
}

func TestTrackPositionActive(t *testing.T) {
	now := time.Now().UnixMilli()
	state := &connectpb.PlayerState{
		IsPlaying:             true,
		IsPaused:              false,
		PositionAsOfTimestamp: 1000,
		Timestamp:             now - 5000,
		PlaybackSpeed:         1,
	}
	pos := TrackPosition(state, 0)
	assert.InDelta(t, int64(6000), pos, 200) // 1000 + ~5000 elapsed
}

func TestTrackPositionClampsToDuration(t *testing.T) {
	now := time.Now().UnixMilli()
	state := &connectpb.PlayerState{
		IsPlaying:             true,
		IsPaused:              false,
		PositionAsOfTimestamp: 10000,
		Timestamp:             now - 1000,
		PlaybackSpeed:         1,
	}
	pos := TrackPosition(state, 5000)
	assert.Equal(t, int64(5000), pos)
}

func TestTrackPositionStaleTimestamp(t *testing.T) {
	state := &connectpb.PlayerState{
		IsPlaying:             true,
		IsPaused:              false,
		PositionAsOfTimestamp: 1000,
		Timestamp:             time.Now().UnixMilli() - 20*60*1000, // 20 minutes ago
		PlaybackSpeed:         1,
	}
	pos := TrackPosition(state, 0)
	assert.Equal(t, int64(1000), pos) // falls back to raw position
}

func TestTrackPositionNil(t *testing.T) {
	assert.Equal(t, int64(0), TrackPosition(nil, 0))
}

func TestUpdateTimestamp(t *testing.T) {
	state := &connectpb.PlayerState{
		PositionAsOfTimestamp: 1000,
		Timestamp:             time.Now().UnixMilli() - 5000,
		PlaybackSpeed:         1,
	}
	UpdateTimestamp(state, 0)
	assert.True(t, state.Timestamp > 0)
	assert.True(t, state.PositionAsOfTimestamp > 1000)
}

func TestUpdateTimestampClampsToDuration(t *testing.T) {
	state := &connectpb.PlayerState{
		PositionAsOfTimestamp: 10000,
		Timestamp:             time.Now().UnixMilli() - 1000,
		PlaybackSpeed:         1,
	}
	UpdateTimestamp(state, 5000)
	assert.Equal(t, int64(5000), state.PositionAsOfTimestamp)
}

func TestUpdateTimestampNil(t *testing.T) {
	assert.NotPanics(t, func() { UpdateTimestamp(nil, 0) })
}

func TestSetPausedTrue(t *testing.T) {
	state := &connectpb.PlayerState{PlaybackSpeed: 1}
	SetPaused(state, true)
	assert.True(t, state.IsPaused)
	assert.Equal(t, float32(0), state.PlaybackSpeed)
}

func TestSetPausedFalse(t *testing.T) {
	state := &connectpb.PlayerState{PlaybackSpeed: 0, IsPaused: true}
	SetPaused(state, false)
	assert.False(t, state.IsPaused)
	assert.Equal(t, float32(1), state.PlaybackSpeed)
}

func TestSetPausedNil(t *testing.T) {
	assert.NotPanics(t, func() { SetPaused(nil, true) })
}

func TestNewPlayerState(t *testing.T) {
	state := NewPlayerState()
	assert.True(t, state.IsSystemInitiated)
	assert.Equal(t, float32(1), state.PlaybackSpeed)
	assert.NotNil(t, state.PlayOrigin)
	assert.NotNil(t, state.Suppressions)
	assert.NotNil(t, state.Options)
}

func TestDefaultDeviceInfo(t *testing.T) {
	info := DefaultDeviceInfo(DeviceInfoOpts{
		DeviceName:      "test-device",
		DeviceId:        "abc123",
		DeviceType:      devicespb.DeviceType_COMPUTER,
		ClientId:        "client1",
		VolumeSteps:     100,
		ZeroconfEnabled: true,
	})
	assert.Equal(t, "test-device", info.Name)
	assert.Equal(t, "abc123", info.DeviceId)
	assert.Equal(t, devicespb.DeviceType_COMPUTER, info.DeviceType)
	assert.Equal(t, "client1", info.ClientId)
	assert.Equal(t, MaxStateVolume, info.Volume)
	assert.True(t, info.CanPlay)
	assert.NotNil(t, info.Capabilities)
	assert.True(t, info.Capabilities.CanBePlayer)
	assert.True(t, info.Capabilities.SupportsLogout)
	assert.Equal(t, int32(100), info.Capabilities.VolumeSteps)
	assert.Contains(t, info.Capabilities.SupportedTypes, "audio/track")
	assert.Contains(t, info.Capabilities.SupportedTypes, "audio/episode")
	assert.Contains(t, info.DeviceSoftwareVersion, "go-librespot")
}

func TestBuildPutStateRequest(t *testing.T) {
	activeSince := time.Now().Add(-5 * time.Minute)
	req := BuildPutStateRequest(PutStateOpts{
		Device:                    DefaultDeviceInfo(DeviceInfoOpts{DeviceName: "test"}),
		PlayerState:               NewPlayerState(),
		Active:                    true,
		ActiveSince:               activeSince,
		LastCommandMsgId:          42,
		LastCommandSentByDeviceId: "device-1",
		HasBeenPlayingForMs:       300000,
	}, connectpb.PutStateReason_PLAYER_STATE_CHANGED)

	assert.Equal(t, connectpb.MemberType_CONNECT_STATE, req.MemberType)
	assert.Equal(t, connectpb.PutStateReason_PLAYER_STATE_CHANGED, req.PutStateReason)
	assert.True(t, req.IsActive)
	assert.Equal(t, uint64(activeSince.UnixMilli()), req.StartedPlayingAt)
	assert.Equal(t, uint32(42), req.LastCommandMessageId)
	assert.Equal(t, "device-1", req.LastCommandSentByDeviceId)
	assert.Equal(t, uint64(300000), req.HasBeenPlayingForMs)
	assert.NotNil(t, req.Device)
	assert.NotNil(t, req.Device.DeviceInfo)
	assert.NotNil(t, req.Device.PlayerState)
	assert.True(t, req.ClientSideTimestamp > 0)
}

func TestBuildPutStateRequestInactive(t *testing.T) {
	req := BuildPutStateRequest(PutStateOpts{
		Device:      DefaultDeviceInfo(DeviceInfoOpts{DeviceName: "test"}),
		PlayerState: NewPlayerState(),
		Active:      false,
	}, connectpb.PutStateReason_PLAYER_STATE_CHANGED)

	assert.False(t, req.IsActive)
	assert.Equal(t, uint64(0), req.StartedPlayingAt)
	assert.Equal(t, uint32(0), req.LastCommandMessageId)
}

func TestPlayOrigin(t *testing.T) {
	state := &connectpb.PlayerState{
		PlayOrigin: &connectpb.PlayOrigin{FeatureIdentifier: "my-app"},
	}
	assert.Equal(t, "my-app", PlayOrigin(state))
}

func TestPlayOriginNil(t *testing.T) {
	assert.Equal(t, "", PlayOrigin(nil))
	assert.Equal(t, "", PlayOrigin(&connectpb.PlayerState{}))
}
