package session

import (
	"context"
	"errors"
)

var (
	// ErrNoAvailableChannels is returned when all channels are in use
	ErrNoAvailableChannels = errors.New("no available channels")
)

// AudioSession represents an active audio session with a device
type AudioSession struct {
	ChannelID string
	SessionID string
}

// ChannelInfo represents information about an audio channel
type ChannelInfo struct {
	ID      string
	Enabled bool // true if channel is currently in use
}

// SessionManager manages audio sessions with devices
// This interface allows for different backend implementations (Hikvision, Dahua, etc.)
type SessionManager interface {
	// AcquireChannel finds and opens an available audio channel
	AcquireChannel(ctx context.Context) (*AudioSession, error)

	// ReleaseChannel closes an audio channel by its ID
	ReleaseChannel(ctx context.Context, channelID string) error

	// ListChannels returns all available channels and their status
	ListChannels(ctx context.Context) ([]ChannelInfo, error)
}
