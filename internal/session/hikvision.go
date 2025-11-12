package session

import (
	"context"
	"log/slog"

	"github.com/acardace/hikvision-doorbell-server/internal/hikvision"
	"github.com/acardace/hikvision-doorbell-server/internal/logger"
)

// HikvisionSessionManager implements SessionManager for Hikvision devices
type HikvisionSessionManager struct {
	client *hikvision.Client
}

// NewHikvisionSessionManager creates a new Hikvision session manager
func NewHikvisionSessionManager(client *hikvision.Client) *HikvisionSessionManager {
	return &HikvisionSessionManager{
		client: client,
	}
}

// AcquireChannel finds and opens an available audio channel
func (m *HikvisionSessionManager) AcquireChannel(ctx context.Context) (*AudioSession, error) {
	// Get available channels from device
	channels, err := m.client.GetTwoWayAudioChannels()
	if err != nil {
		logger.Log.Error("failed to get audio channels",
			slog.String("component", "session_manager"),
			slog.String("error", err.Error()))
		return nil, err
	}

	if len(channels.Channels) == 0 {
		logger.Log.Warn("no audio channels available on device",
			slog.String("component", "session_manager"))
		return nil, ErrNoAvailableChannels
	}

	// Find first available channel (Enabled == "false" means available)
	var channelID string
	for _, ch := range channels.Channels {
		if ch.Enabled == "false" {
			channelID = ch.ID
			break
		}
	}

	if channelID == "" {
		logger.Log.Warn("no available channels, all in use",
			slog.String("component", "session_manager"),
			slog.Int("total_channels", len(channels.Channels)))
		return nil, ErrNoAvailableChannels
	}

	// Open the channel
	hikSession, err := m.client.OpenAudioChannel(channelID)
	if err != nil {
		logger.Log.Error("failed to open audio channel",
			slog.String("component", "session_manager"),
			slog.String("channel_id", channelID),
			slog.String("error", err.Error()))
		return nil, err
	}

	logger.Log.Info("acquired audio channel",
		slog.String("component", "session_manager"),
		slog.String("channel_id", channelID),
		slog.String("session_id", hikSession.SessionID))

	return &AudioSession{
		ChannelID: hikSession.ChannelID,
		SessionID: hikSession.SessionID,
	}, nil
}

// ReleaseChannel closes an audio channel by its ID
func (m *HikvisionSessionManager) ReleaseChannel(ctx context.Context, channelID string) error {
	err := m.client.CloseAudioChannel(channelID)
	if err != nil {
		logger.Log.Error("failed to close audio channel",
			slog.String("component", "session_manager"),
			slog.String("channel_id", channelID),
			slog.String("error", err.Error()))
		return err
	}

	logger.Log.Info("released audio channel",
		slog.String("component", "session_manager"),
		slog.String("channel_id", channelID))

	return nil
}

// GetAvailableChannels returns the list of available channel IDs
func (m *HikvisionSessionManager) GetAvailableChannels(ctx context.Context) ([]string, error) {
	channels, err := m.client.GetTwoWayAudioChannels()
	if err != nil {
		return nil, err
	}

	var available []string
	for _, ch := range channels.Channels {
		if ch.Enabled == "false" {
			available = append(available, ch.ID)
		}
	}

	return available, nil
}
