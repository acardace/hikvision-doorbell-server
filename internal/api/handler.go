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

// CORS middleware to allow requests from Home Assistant
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow all origins for local network deployment
		// In production, you might want to restrict this to specific origins
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		// Handle preflight requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// SetupRoutes configures all API routes
func (h *Handler) SetupRoutes() *mux.Router {
	router := mux.NewRouter()

	// Apply CORS middleware
	router.Use(corsMiddleware)

	// Health check
	router.HandleFunc("/healthz", h.Healthz).Methods("GET")

	// WebRTC signaling
	router.HandleFunc("/api/webrtc/offer", h.webrtcHandler.HandleOffer).Methods("POST", "OPTIONS")

	// Play audio file (with automatic session management)
	router.HandleFunc("/api/audio/play-file", HandlePlayFile(h.hikClient)).Methods("POST", "OPTIONS")

	return router
}
