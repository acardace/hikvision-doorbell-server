package streaming

import (
	"context"
	"io"

	"github.com/acardace/hikvision-doorbell-server/internal/session"
	"github.com/pion/webrtc/v4"
)

// AudioStreamer handles bidirectional audio streaming between a device and WebRTC
// This interface allows for different backend implementations (Hikvision, Dahua, etc.)
type AudioStreamer interface {
	// Start begins the audio streaming session
	Start(ctx context.Context, sess *session.AudioSession) error

	// StreamDeviceToClient reads audio from the device and sends to WebRTC client
	StreamDeviceToClient(ctx context.Context, track *webrtc.TrackLocalStaticSample) error

	// StreamClientToDevice reads audio from WebRTC client and sends to device
	StreamClientToDevice(ctx context.Context, track *webrtc.TrackRemote) error

	// Stop closes the streaming session
	Stop() error
}

// AudioReader represents a source of audio data (doorbell microphone)
type AudioReader interface {
	io.Reader
	Start()
	Close() error
}

// AudioWriter represents a sink for audio data (doorbell speaker)
type AudioWriter interface {
	io.Writer
	Start()
	Close() error
}
