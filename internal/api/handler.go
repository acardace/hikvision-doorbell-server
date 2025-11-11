package api

import (
	"log"
	"net/http"

	"github.com/acardace/hikvision-doorbell-server/internal/hikvision"
	"github.com/gorilla/mux"
)

type Handler struct {
	hikClient     *hikvision.Client
	webrtcHandler *WebRTCHandler
}

func NewHandler(hikClient *hikvision.Client) *Handler {
	return &Handler{
		hikClient:     hikClient,
		webrtcHandler: NewWebRTCHandler(hikClient),
	}
}

// Healthz endpoint for Kubernetes health probes
func (h *Handler) Healthz(w http.ResponseWriter, r *http.Request) {
	// Test connection to doorbell by getting channels (quietly, without logging)
	_, err := h.hikClient.GetTwoWayAudioChannelsQuiet()
	if err != nil {
		// Only log errors, not successful health checks
		log.Printf("[Health] Device unreachable: %v", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("unhealthy"))
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("healthy"))
}

// CloseAllSessions closes all active audio sessions
func (h *Handler) CloseAllSessions() error {
	log.Println("Closing all active sessions...")
	h.webrtcHandler.Close()
	log.Println("All sessions closed successfully")
	return nil
}

// SetupRoutes configures all API routes
func (h *Handler) SetupRoutes() *mux.Router {
	router := mux.NewRouter()

	// Health check
	router.HandleFunc("/healthz", h.Healthz).Methods("GET")

	// WebRTC signaling
	router.HandleFunc("/api/webrtc/offer", h.webrtcHandler.HandleOffer).Methods("POST")

	// Play audio file (with automatic session management)
	router.HandleFunc("/api/audio/play-file", HandlePlayFile(h.hikClient)).Methods("POST")

	return router
}
