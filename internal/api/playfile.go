package api

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/acardace/hikvision-doorbell-server/internal/hikvision"
	"github.com/acardace/hikvision-doorbell-server/internal/session"
	"github.com/google/uuid"
)

// HandlePlayFile handles uploading and playing an audio file
// This automatically manages the session lifecycle
func HandlePlayFile(hikClient *hikvision.Client, abortManager *AbortManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Create a cancellable context for this operation
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		// Generate unique ID for this operation
		operationID := fmt.Sprintf("playfile-%s", uuid.New().String())

		// Register with abort manager
		abortManager.Register(operationID, cancel)
		defer abortManager.Unregister(operationID)

		log.Printf("[PlayFile] Received request to play audio file (operation: %s)", operationID)

		// Read uploaded file
		err := r.ParseMultipartForm(10 << 20) // 10 MB max
		if err != nil {
			log.Printf("[PlayFile] Failed to parse multipart form: %v", err)
			http.Error(w, "Failed to parse form", http.StatusBadRequest)
			return
		}

		file, _, err := r.FormFile("audio")
		if err != nil {
			log.Printf("[PlayFile] Failed to get file from form: %v", err)
			http.Error(w, "No audio file provided", http.StatusBadRequest)
			return
		}
		defer file.Close()

		// Read file contents
		audioData, err := io.ReadAll(file)
		if err != nil {
			log.Printf("[PlayFile] Failed to read file: %v", err)
			http.Error(w, "Failed to read file", http.StatusInternalServerError)
			return
		}

		log.Printf("[PlayFile] Read %d bytes of audio data", len(audioData))

		sessionManager := session.NewHikvisionSessionManager(hikClient)

		session, err := sessionManager.AcquireChannel(ctx)
		if err != nil {
			log.Printf("[PlayFile] Failed to open audio channel: %v", err)
			http.Error(w, fmt.Sprintf("Failed to open audio channel: %v", err), http.StatusInternalServerError)
			return
		}

		// Ensure we close the channel when done
		defer func() {
			log.Println("[PlayFile] Closing audio channel...")
			sessionManager.ReleaseChannel(ctx, session.ChannelID)
		}()

		// Create audio writer
		hikvisionSession := hikvision.AudioSession{
			ChannelID: session.ChannelID,
			SessionID: session.SessionID,
		}

		writer := hikClient.NewAudioStreamWriter(&hikvisionSession)
		writer.Start()
		defer writer.Close()

		// Send audio data in chunks
		chunkSize := 4096
		totalChunks := (len(audioData) + chunkSize - 1) / chunkSize
		log.Printf("[PlayFile] Sending %d chunks...", totalChunks)

		for i := 0; i < len(audioData); i += chunkSize {
			select {
			case <-ctx.Done():
				return
			default:
				end := i + chunkSize
				if end > len(audioData) {
					end = len(audioData)
				}

				chunk := audioData[i:end]
				_, err := writer.Write(chunk)
				if err != nil {
					log.Printf("[PlayFile] Failed to write chunk: %v", err)
					http.Error(w, "Failed to send audio", http.StatusInternalServerError)
					return
				}
			}
		}

		log.Println("[PlayFile] All audio data sent")

		// Calculate playback duration and wait for audio to finish
		// G.711 is 8000 bytes/sec
		audioDuration := time.Duration(len(audioData)) * time.Second / 8000
		log.Printf("[PlayFile] Waiting %.2f seconds for playback to complete...", audioDuration.Seconds())

		select {
		case <-ctx.Done():
			return
		case <-time.After(audioDuration):
			log.Println("[PlayFile] Playback complete")
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Audio played successfully"))
	}
}
