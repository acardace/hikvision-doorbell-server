package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"github.com/acardace/hikvision-doorbell-server/internal/audio"
	"github.com/acardace/hikvision-doorbell-server/internal/logger"
	"github.com/acardace/hikvision-doorbell-server/internal/session"
	"github.com/acardace/hikvision-doorbell-server/internal/streaming"
	"github.com/pion/webrtc/v4"
)

type WebRTCHandler struct {
	config         *WebRTCConfig
	sessionManager session.SessionManager
	audioStreamer  streaming.AudioStreamer
	peerConnection *webrtc.PeerConnection
	activeSession  *session.AudioSession
	mu             sync.Mutex
	cancelFunc     context.CancelFunc // Cancel function for goroutines
}

func NewWebRTCHandler(sessionManager session.SessionManager, audioStreamer streaming.AudioStreamer) *WebRTCHandler {
	config := NewWebRTCConfig()
	config.LoadFromEnv()

	return &WebRTCHandler{
		config:         config,
		sessionManager: sessionManager,
		audioStreamer:  audioStreamer,
	}
}

// HandleOffer handles WebRTC SDP offer from client
func (h *WebRTCHandler) HandleOffer(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Create context for managing goroutines lifecycle
	ctx, cancel := context.WithCancel(r.Context())
	h.cancelFunc = cancel

	// Parse SDP offer
	var offer webrtc.SessionDescription
	if err := json.NewDecoder(r.Body).Decode(&offer); err != nil {
		logger.Log.Error("failed to decode SDP offer",
			slog.String("component", "webrtc"),
			slog.String("error", err.Error()))
		http.Error(w, "Invalid offer", http.StatusBadRequest)
		return
	}

	logger.Log.Info("received SDP offer",
		slog.String("component", "webrtc"),
		slog.String("type", offer.Type.String()))

	// Create peer connection using configuration
	peerConnection, err := h.config.CreatePeerConnection()
	if err != nil {
		http.Error(w, "Failed to create peer connection", http.StatusInternalServerError)
		return
	}

	h.peerConnection = peerConnection

	// Create outgoing audio track for sending audio from doorbell to client
	audioTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: audio.CodecMimeType},
		"audio",
		"doorbell-audio",
	)
	if err != nil {
		logger.Log.Error("failed to create audio track",
			slog.String("component", "webrtc"),
			slog.String("error", err.Error()))
		http.Error(w, "Failed to create audio track", http.StatusInternalServerError)
		return
	}

	// Add track to peer connection
	_, err = peerConnection.AddTrack(audioTrack)
	if err != nil {
		logger.Log.Error("failed to add track to peer connection",
			slog.String("component", "webrtc"),
			slog.String("error", err.Error()))
		http.Error(w, "Failed to add track", http.StatusInternalServerError)
		return
	}

	// Handle incoming audio track (from browser/client to device)
	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		logger.Log.Info("received remote track",
			slog.String("component", "webrtc"),
			slog.String("kind", track.Kind().String()),
			slog.String("codec", track.Codec().MimeType))

		// Start session if not already active
		if h.activeSession == nil {
			logger.Log.Info("acquiring audio session", slog.String("component", "webrtc"))

			// Acquire session using session manager
			sess, err := h.sessionManager.AcquireChannel(ctx)
			if err != nil {
				logger.Log.Error("failed to acquire audio session",
					slog.String("component", "webrtc"),
					slog.String("error", err.Error()))
				return
			}
			h.activeSession = sess

			// Start audio streaming
			if err := h.audioStreamer.Start(ctx, sess); err != nil {
				logger.Log.Error("failed to start audio streaming",
					slog.String("component", "webrtc"),
					slog.String("error", err.Error()))
				return
			}

			// Start goroutine to stream device audio to client
			go func() {
				if err := h.audioStreamer.StreamDeviceToClient(ctx, audioTrack); err != nil {
					logger.Log.Error("device-to-client streaming error",
						slog.String("component", "webrtc"),
						slog.String("error", err.Error()))
				}
			}()
		}

		// Start goroutine to stream client audio to device
		go func() {
			defer func() {
				logger.Log.Info("track ended, cleaning up session", slog.String("component", "webrtc"))
				h.cleanup()
			}()

			if err := h.audioStreamer.StreamClientToDevice(ctx, track); err != nil {
				logger.Log.Error("client-to-device streaming error",
					slog.String("component", "webrtc"),
					slog.String("error", err.Error()))
			}
		}()
	})

	// Handle connection state changes
	peerConnection.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		logger.Log.Info("connection state changed",
			slog.String("component", "webrtc"),
			slog.String("state", state.String()))

		if state == webrtc.PeerConnectionStateFailed ||
			state == webrtc.PeerConnectionStateClosed ||
			state == webrtc.PeerConnectionStateDisconnected {
			h.cleanup()
		}
	})

	// Set remote description (client's offer)
	err = peerConnection.SetRemoteDescription(offer)
	if err != nil {
		logger.Log.Error("failed to set remote description",
			slog.String("component", "webrtc"),
			slog.String("error", err.Error()))
		http.Error(w, "Failed to set remote description", http.StatusInternalServerError)
		return
	}

	// Log ICE candidates for debugging
	peerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			logger.Log.Debug("generated ICE candidate",
				slog.String("component", "webrtc"),
				slog.String("type", candidate.Typ.String()),
				slog.String("protocol", candidate.Protocol.String()),
				slog.String("address", candidate.Address),
				slog.Int("port", int(candidate.Port)))
		}
	})

	// Wait for ICE gathering to complete
	gatherComplete := make(chan struct{})
	peerConnection.OnICEGatheringStateChange(func(state webrtc.ICEGatheringState) {
		logger.Log.Info("ICE gathering state changed",
			slog.String("component", "webrtc"),
			slog.String("state", state.String()))
		if state == webrtc.ICEGatheringStateComplete {
			close(gatherComplete)
		}
	})

	// Create answer
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		logger.Log.Error("failed to create SDP answer",
			slog.String("component", "webrtc"),
			slog.String("error", err.Error()))
		http.Error(w, "Failed to create answer", http.StatusInternalServerError)
		return
	}

	// Set local description (this triggers ICE gathering)
	err = peerConnection.SetLocalDescription(answer)
	if err != nil {
		logger.Log.Error("failed to set local description",
			slog.String("component", "webrtc"),
			slog.String("error", err.Error()))
		http.Error(w, "Failed to set local description", http.StatusInternalServerError)
		return
	}

	// Wait for ICE gathering to complete
	logger.Log.Info("waiting for ICE gathering to complete", slog.String("component", "webrtc"))
	<-gatherComplete

	// Send answer back to client (now with all ICE candidates)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(peerConnection.LocalDescription())

	logger.Log.Info("SDP answer sent successfully", slog.String("component", "webrtc"))
}

// cleanup closes the session and cleans up resources
func (h *WebRTCHandler) cleanup() {
	// Cancel all goroutines first
	if h.cancelFunc != nil {
		h.cancelFunc()
		h.cancelFunc = nil
	}

	// Stop audio streaming
	if h.audioStreamer != nil {
		h.audioStreamer.Stop()
	}

	// Release audio session
	if h.activeSession != nil {
		ctx := context.Background()
		if err := h.sessionManager.ReleaseChannel(ctx, h.activeSession.ChannelID); err != nil {
			logger.Log.Error("failed to release audio session",
				slog.String("component", "webrtc"),
				slog.String("channel_id", h.activeSession.ChannelID),
				slog.String("error", err.Error()))
		}
		h.activeSession = nil
	}

	// Close peer connection
	if h.peerConnection != nil {
		h.peerConnection.Close()
		h.peerConnection = nil
	}
}

// Close closes all WebRTC resources
func (h *WebRTCHandler) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cleanup()
}
