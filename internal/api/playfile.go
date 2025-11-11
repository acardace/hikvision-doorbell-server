package api

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/acardace/hikvision-doorbell-server/internal/hikvision"
)

// HandlePlayFile handles uploading and playing an audio file
// This automatically manages the session lifecycle
func HandlePlayFile(hikClient *hikvision.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Println("[PlayFile] Received request to play audio file")

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

		// Get available channels
		channels, err := hikClient.GetTwoWayAudioChannels()
		if err != nil {
			log.Printf("[PlayFile] Failed to get channels: %v", err)
			http.Error(w, fmt.Sprintf("Failed to get channels: %v", err), http.StatusInternalServerError)
			return
		}

		if len(channels.Channels) == 0 {
			log.Println("[PlayFile] No audio channels available")
			http.Error(w, "No audio channels available", http.StatusNotFound)
			return
		}

		// Find first available channel (enabled=false means available)
		var channelID string
		for _, ch := range channels.Channels {
			if ch.Enabled == "false" {
				channelID = ch.ID
				break
			}
		}

		if channelID == "" {
			log.Println("[PlayFile] No available channels (all in use)")
			http.Error(w, "No available channels (all in use)", http.StatusConflict)
			return
		}

		// Open audio channel
		log.Printf("[PlayFile] Opening audio channel %s...", channelID)
		session, err := hikClient.OpenAudioChannel(channelID)
		if err != nil {
			log.Printf("[PlayFile] Failed to open audio channel: %v", err)
			http.Error(w, fmt.Sprintf("Failed to open audio channel: %v", err), http.StatusInternalServerError)
			return
		}

		// Ensure we close the channel when done
		defer func() {
			log.Println("[PlayFile] Closing audio channel...")
			if err := hikClient.CloseAudioChannel(session.ChannelID); err != nil {
				log.Printf("[PlayFile] Warning: Failed to close channel: %v", err)
			}
		}()

		// Create audio writer
		writer := hikClient.NewAudioStreamWriter(session)
		writer.Start()
		defer writer.Close()

		// Send audio data in chunks
		chunkSize := 4096
		totalChunks := (len(audioData) + chunkSize - 1) / chunkSize
		log.Printf("[PlayFile] Sending %d chunks...", totalChunks)

		for i := 0; i < len(audioData); i += chunkSize {
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

		log.Println("[PlayFile] All audio data sent")

		// Calculate playback duration and wait for audio to finish
		// G.711 is 8000 bytes/sec
		audioDuration := time.Duration(len(audioData)) * time.Second / 8000
		log.Printf("[PlayFile] Waiting %.2f seconds for playback to complete...", audioDuration.Seconds())
		time.Sleep(audioDuration)

		log.Println("[PlayFile] Playback complete")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Audio played successfully"))
	}
}
