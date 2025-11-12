package api

import (
	"context"
	"log"
	"net/http"
	"sync"

	"github.com/acardace/hikvision-doorbell-server/internal/session"
)

// OperationType represents the type of operation
type OperationType int

const (
	OperationTypePlayFile OperationType = iota
	OperationTypeWebRTC
)

// Operation represents a tracked operation
type Operation struct {
	Type   OperationType
	Cancel context.CancelFunc
}

func (o *Operation) IsPlayFile() bool {
	return o.Type == OperationTypePlayFile
}

func (o *Operation) IsWebRTC() bool {
	return o.Type == OperationTypeWebRTC
}

// AbortManager manages ongoing operations that can be aborted
type AbortManager struct {
	mu             sync.Mutex
	activeOps      []*Operation
	sessionManager session.SessionManager
}

// NewAbortManager creates a new abort manager
func NewAbortManager(sessionManager session.SessionManager) *AbortManager {
	return &AbortManager{
		activeOps:      make([]*Operation, 0),
		sessionManager: sessionManager,
	}
}

// Register registers a new operation with a cancel function
func (am *AbortManager) Register(opType OperationType, cancel context.CancelFunc) *Operation {
	am.mu.Lock()
	defer am.mu.Unlock()

	op := &Operation{
		Type:   opType,
		Cancel: cancel,
	}
	am.activeOps = append(am.activeOps, op)
	log.Printf("[AbortManager] Registered operation (type: %d)", opType)
	return op
}

// Unregister removes an operation from tracking
func (am *AbortManager) Unregister(op *Operation) {
	am.mu.Lock()
	defer am.mu.Unlock()

	for i, activeOp := range am.activeOps {
		if activeOp == op {
			am.activeOps = append(am.activeOps[:i], am.activeOps[i+1:]...)
			log.Printf("[AbortManager] Unregistered operation (type: %d)", op.Type)
			return
		}
	}
}

// AbortPlayFileOperations cancels only play-file operations (not WebRTC)
func (am *AbortManager) AbortPlayFileOperations(ctx context.Context) {
	am.mu.Lock()
	defer am.mu.Unlock()

	playFileOps := 0
	newActiveOps := make([]*Operation, 0)

	for _, op := range am.activeOps {
		if op.IsPlayFile() {
			log.Printf("[AbortManager] Cancelling play-file operation")
			op.Cancel()
			playFileOps++
		} else {
			newActiveOps = append(newActiveOps, op)
		}
	}

	am.activeOps = newActiveOps
	log.Printf("[AbortManager] Aborted %d play-file operations", playFileOps)
}

// HasActiveWebRTC returns true if there's an active WebRTC session
func (am *AbortManager) HasActiveWebRTC() bool {
	am.mu.Lock()
	defer am.mu.Unlock()

	for _, op := range am.activeOps {
		if op.IsWebRTC() {
			return true
		}
	}
	return false
}

// AbortAll cancels all active operations and closes all audio channels
func (am *AbortManager) AbortAll(ctx context.Context) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	log.Printf("[AbortManager] Aborting %d active operations", len(am.activeOps))

	// Cancel all active operations
	for _, op := range am.activeOps {
		log.Printf("[AbortManager] Cancelling operation (type: %d)", op.Type)
		op.Cancel()
	}

	// Clear the slice
	am.activeOps = make([]*Operation, 0)

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
