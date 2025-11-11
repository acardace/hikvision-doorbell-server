package api

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/acardace/hikvision-doorbell-server/internal/hikvision"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

type WebRTCHandler struct {
	hikClient      *hikvision.Client
	peerConnection *webrtc.PeerConnection
	audioWriter    *hikvision.AudioStreamWriter
	audioReader    *hikvision.AudioStreamReader
	activeSession  *hikvision.AudioSession
	mu             sync.Mutex
}

func NewWebRTCHandler(hikClient *hikvision.Client) *WebRTCHandler {
	return &WebRTCHandler{
		hikClient: hikClient,
	}
}

// HandleOffer handles WebRTC SDP offer from client
func (h *WebRTCHandler) HandleOffer(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Parse SDP offer
	var offer webrtc.SessionDescription
	if err := json.NewDecoder(r.Body).Decode(&offer); err != nil {
		log.Printf("[WebRTC] Failed to decode offer: %v", err)
		http.Error(w, "Invalid offer", http.StatusBadRequest)
		return
	}

	log.Printf("[WebRTC] Received offer type: %s", offer.Type)

	// Create WebRTC configuration for local network only
	// No ICE servers needed - this is meant for local/VPN use only
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{},
	}

	// Create a SettingEngine for local network use
	settingEngine := webrtc.SettingEngine{}
	settingEngine.SetNetworkTypes([]webrtc.NetworkType{
		webrtc.NetworkTypeTCP4,
		webrtc.NetworkTypeUDP4,
	})

	api := webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine))

	// Create new peer connection using the custom API
	peerConnection, err := api.NewPeerConnection(config)
	if err != nil {
		log.Printf("[WebRTC] Failed to create peer connection: %v", err)
		http.Error(w, "Failed to create peer connection", http.StatusInternalServerError)
		return
	}

	h.peerConnection = peerConnection

	// Create outgoing audio track for sending audio from doorbell to client
	audioTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypePCMU},
		"audio",
		"doorbell-audio",
	)
	if err != nil {
		log.Printf("[WebRTC] Failed to create audio track: %v", err)
		http.Error(w, "Failed to create audio track", http.StatusInternalServerError)
		return
	}

	// Add track to peer connection
	_, err = peerConnection.AddTrack(audioTrack)
	if err != nil {
		log.Printf("[WebRTC] Failed to add track: %v", err)
		http.Error(w, "Failed to add track", http.StatusInternalServerError)
		return
	}

	// Add transceiver for receiving audio from client
	_, err = peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	})
	if err != nil {
		log.Printf("[WebRTC] Failed to add transceiver: %v", err)
		http.Error(w, "Failed to add transceiver", http.StatusInternalServerError)
		return
	}

	// Handle incoming audio track (from browser/client to doorbell)
	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		log.Printf("[WebRTC] Received track: %s, codec: %s", track.Kind(), track.Codec().MimeType)

		// Start session if not already active
		if h.activeSession == nil {
			log.Println("[WebRTC] Starting audio session...")

			// Get available channels
			channels, err := h.hikClient.GetTwoWayAudioChannels()
			if err != nil {
				log.Printf("[WebRTC] Failed to get channels: %v", err)
				return
			}

			if len(channels.Channels) == 0 {
				log.Println("[WebRTC] No audio channels available")
				return
			}

			// Find first available channel
			var channelID string
			for _, ch := range channels.Channels {
				if ch.Enabled == "false" {
					channelID = ch.ID
					break
				}
			}

			if channelID == "" {
				log.Println("[WebRTC] No available channels (all in use)")
				return
			}

			session, err := h.hikClient.OpenAudioChannel(channelID)
			if err != nil {
				log.Printf("[WebRTC] Failed to open audio channel: %v", err)
				return
			}
			h.activeSession = session

			// Create audio writer (for sending to doorbell)
			h.audioWriter = h.hikClient.NewAudioStreamWriter(session)
			h.audioWriter.Start()

			// Create audio reader (for receiving from doorbell)
			h.audioReader = h.hikClient.NewAudioStreamReader(session)
			h.audioReader.Start()

			// Start goroutine to read from doorbell and send via WebRTC
			go func() {
				defer log.Println("[WebRTC] Stopped reading from doorbell")
				buffer := make([]byte, 160) // 20ms of G.711 audio

				for {
					n, err := h.audioReader.Read(buffer)
					if err != nil {
						if err != io.EOF {
							log.Printf("[WebRTC] Error reading from doorbell: %v", err)
						}
						return
					}

					if n > 0 {
						// Send to WebRTC track
						if err := audioTrack.WriteSample(media.Sample{
							Data:     buffer[:n],
							Duration: time.Millisecond * 20,
						}); err != nil {
							log.Printf("[WebRTC] Error sending sample: %v", err)
							return
						}
					}
				}
			}()
		}

		// Read RTP packets and send to doorbell
		go func() {
			defer func() {
				log.Println("[WebRTC] Track ended, cleaning up...")
				h.cleanup()
			}()

			for {
				rtp, _, err := track.ReadRTP()
				if err != nil {
					if err != io.EOF {
						log.Printf("[WebRTC] Error reading RTP: %v", err)
					}
					return
				}

				// Send audio payload to doorbell
				if h.audioWriter != nil {
					_, err = h.audioWriter.Write(rtp.Payload)
					if err != nil {
						log.Printf("[WebRTC] Error writing audio: %v", err)
						return
					}
				}
			}
		}()
	})

	// Handle connection state changes
	peerConnection.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("[WebRTC] Connection state changed: %s", state.String())

		if state == webrtc.PeerConnectionStateFailed ||
			state == webrtc.PeerConnectionStateClosed ||
			state == webrtc.PeerConnectionStateDisconnected {
			h.cleanup()
		}
	})

	// Set remote description (client's offer)
	err = peerConnection.SetRemoteDescription(offer)
	if err != nil {
		log.Printf("[WebRTC] Failed to set remote description: %v", err)
		http.Error(w, "Failed to set remote description", http.StatusInternalServerError)
		return
	}

	// Log ICE candidates for debugging
	peerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			log.Printf("[WebRTC] Server ICE candidate: type=%s protocol=%s address=%s port=%d",
				candidate.Typ.String(),
				candidate.Protocol.String(),
				candidate.Address,
				candidate.Port)
		}
	})

	// Wait for ICE gathering to complete
	gatherComplete := make(chan struct{})
	peerConnection.OnICEGatheringStateChange(func(state webrtc.ICEGatheringState) {
		log.Printf("[WebRTC] ICE Gathering State: %s", state.String())
		if state == webrtc.ICEGatheringStateComplete {
			close(gatherComplete)
		}
	})

	// Create answer
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		log.Printf("[WebRTC] Failed to create answer: %v", err)
		http.Error(w, "Failed to create answer", http.StatusInternalServerError)
		return
	}

	// Set local description (this triggers ICE gathering)
	err = peerConnection.SetLocalDescription(answer)
	if err != nil {
		log.Printf("[WebRTC] Failed to set local description: %v", err)
		http.Error(w, "Failed to set local description", http.StatusInternalServerError)
		return
	}

	// Wait for ICE gathering to complete
	log.Println("[WebRTC] Gathering ICE candidates...")
	<-gatherComplete

	// Send answer back to client (now with all ICE candidates)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(peerConnection.LocalDescription())

	log.Println("[WebRTC] SDP answer sent successfully")
}

// cleanup closes the session and cleans up resources
func (h *WebRTCHandler) cleanup() {
	if h.audioWriter != nil {
		h.audioWriter.Close()
		h.audioWriter = nil
	}

	if h.audioReader != nil {
		h.audioReader.Close()
		h.audioReader = nil
	}

	if h.activeSession != nil {
		h.hikClient.CloseAudioChannel(h.activeSession.ChannelID)
		h.activeSession = nil
	}

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
