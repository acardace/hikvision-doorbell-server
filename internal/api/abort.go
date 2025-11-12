package api

import (
	"context"
	"log"
	"net/http"
	"sync"

	"github.com/acardace/hikvision-doorbell-server/internal/session"
)

// AbortManager manages ongoing operations that can be aborted
type AbortManager struct {
	mu             sync.Mutex
	activeContexts map[string]context.CancelFunc
	sessionManager session.SessionManager
}

// NewAbortManager creates a new abort manager
func NewAbortManager(sessionManager session.SessionManager) *AbortManager {
	return &AbortManager{
		activeContexts: make(map[string]context.CancelFunc),
		sessionManager: sessionManager,
	}
}

// Register registers a new operation with a cancel function
func (am *AbortManager) Register(id string, cancel context.CancelFunc) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.activeContexts[id] = cancel
	log.Printf("[AbortManager] Registered operation: %s", id)
}

// Unregister removes an operation from tracking
func (am *AbortManager) Unregister(id string) {
	am.mu.Lock()
	defer am.mu.Unlock()
	delete(am.activeContexts, id)
	log.Printf("[AbortManager] Unregistered operation: %s", id)
}

// AbortAll cancels all active operations and closes all audio channels
func (am *AbortManager) AbortAll(ctx context.Context) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	log.Printf("[AbortManager] Aborting %d active operations", len(am.activeContexts))

	// Cancel all active operations
	for id, cancel := range am.activeContexts {
		log.Printf("[AbortManager] Cancelling operation: %s", id)
		cancel()
	}

	// Clear the map
	am.activeContexts = make(map[string]context.CancelFunc)

	// List all channels and close any that are enabled (in use)
	channels, err := am.sessionManager.ListChannels(ctx)
	if err != nil {
		log.Printf("[AbortManager] Failed to list channels: %v", err)
		return err
	}

	closedCount := 0
	for _, ch := range channels {
		if ch.Enabled {
			log.Printf("[AbortManager] Releasing active channel: %s", ch.ID)
			if err := am.sessionManager.ReleaseChannel(ctx, ch.ID); err != nil {
				log.Printf("[AbortManager] Failed to release channel %s: %v", ch.ID, err)
				// Continue closing other channels
			} else {
				closedCount++
			}
		}
	}

	log.Printf("[AbortManager] Closed %d audio channels", closedCount)
	return nil
}

// HandleAbort handles the abort endpoint
func (h *Handler) HandleAbort(w http.ResponseWriter, r *http.Request) {
	log.Println("[Abort] Received abort request - stopping all operations")

	// Abort all tracked operations and close all channels
	if err := h.abortManager.AbortAll(r.Context()); err != nil {
		log.Printf("[Abort] Error during abort: %v", err)
		http.Error(w, "Failed to abort all operations", http.StatusInternalServerError)
		return
	}

	// Close all WebRTC sessions
	if err := h.CloseAllSessions(); err != nil {
		log.Printf("[Abort] Error closing WebRTC sessions: %v", err)
		http.Error(w, "Failed to close all sessions", http.StatusInternalServerError)
		return
	}

	log.Println("[Abort] All operations aborted successfully")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("All operations aborted"))
}
