package streaming

import (
	"context"
	"io"
	"log/slog"

	"github.com/acardace/hikvision-doorbell-server/internal/audio"
	"github.com/acardace/hikvision-doorbell-server/internal/hikvision"
	"github.com/acardace/hikvision-doorbell-server/internal/logger"
	"github.com/acardace/hikvision-doorbell-server/internal/session"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// HikvisionAudioStreamer implements AudioStreamer for Hikvision devices
type HikvisionAudioStreamer struct {
	client      *hikvision.Client
	audioWriter *hikvision.AudioStreamWriter
	audioReader *hikvision.AudioStreamReader
}

// NewHikvisionAudioStreamer creates a new Hikvision audio streamer
func NewHikvisionAudioStreamer(client *hikvision.Client) *HikvisionAudioStreamer {
	return &HikvisionAudioStreamer{
		client: client,
	}
}

// Start begins the audio streaming session
func (s *HikvisionAudioStreamer) Start(ctx context.Context, sess *session.AudioSession) error {
	// Convert to Hikvision AudioSession
	hikSession := &hikvision.AudioSession{
		ChannelID: sess.ChannelID,
		SessionID: sess.SessionID,
	}

	// Create and start audio writer (for sending to doorbell)
	s.audioWriter = s.client.NewAudioStreamWriter(hikSession)
	s.audioWriter.Start()

	// Create and start audio reader (for receiving from doorbell)
	s.audioReader = s.client.NewAudioStreamReader(hikSession)
	s.audioReader.Start()

	logger.Log.Info("started audio streaming session",
		slog.String("component", "audio_streamer"),
		slog.String("channel_id", sess.ChannelID))

	return nil
}

// StreamDeviceToClient reads audio from the device and sends to WebRTC client
func (s *HikvisionAudioStreamer) StreamDeviceToClient(ctx context.Context, track *webrtc.TrackLocalStaticSample) error {
	defer logger.Log.Info("stopped streaming device to client",
		slog.String("component", "audio_streamer"))

	buffer := make([]byte, audio.SampleSize)

	for {
		select {
		case <-ctx.Done():
			logger.Log.Info("device-to-client streaming cancelled",
				slog.String("component", "audio_streamer"))
			return ctx.Err()
		default:
			// Read exactly audio.SampleSize bytes from device
			n, err := io.ReadFull(s.audioReader, buffer)
			if err != nil {
				if err != io.EOF && err != io.ErrUnexpectedEOF {
					logger.Log.Error("error reading from device",
						slog.String("component", "audio_streamer"),
						slog.String("error", err.Error()))
				}
				return err
			}

			// Send to WebRTC track with precise timing
			if err := track.WriteSample(media.Sample{
				Data:     buffer[:n],
				Duration: audio.SampleDuration,
			}); err != nil {
				logger.Log.Error("error sending audio sample to client",
					slog.String("component", "audio_streamer"),
					slog.String("error", err.Error()))
				return err
			}
		}
	}
}

// StreamClientToDevice reads audio from WebRTC client and sends to device
func (s *HikvisionAudioStreamer) StreamClientToDevice(ctx context.Context, track *webrtc.TrackRemote) error {
	defer logger.Log.Info("stopped streaming client to device",
		slog.String("component", "audio_streamer"))

	for {
		select {
		case <-ctx.Done():
			logger.Log.Info("client-to-device streaming cancelled",
				slog.String("component", "audio_streamer"))
			return ctx.Err()
		default:
			rtp, _, err := track.ReadRTP()
			if err != nil {
				if err != io.EOF {
					logger.Log.Error("error reading RTP packet",
						slog.String("component", "audio_streamer"),
						slog.String("error", err.Error()))
				}
				return err
			}

			// Send audio payload to device
			_, err = s.audioWriter.Write(rtp.Payload)
			if err != nil {
				logger.Log.Error("error writing audio to device",
					slog.String("component", "audio_streamer"),
					slog.String("error", err.Error()))
				return err
			}
		}
	}
}

// Stop closes the streaming session
func (s *HikvisionAudioStreamer) Stop() error {
	if s.audioWriter != nil {
		s.audioWriter.Close()
		s.audioWriter = nil
	}

	if s.audioReader != nil {
		s.audioReader.Close()
		s.audioReader = nil
	}

	logger.Log.Info("stopped audio streaming session",
		slog.String("component", "audio_streamer"))

	return nil
}
