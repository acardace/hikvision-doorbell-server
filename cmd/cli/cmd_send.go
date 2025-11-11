package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

var (
	audioFile string
)

func sendCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send",
		Short: "Send audio file to doorbell",
		Long: `Send an audio file to the doorbell speaker. The CLI will automatically
convert the audio to G.711 µ-law format using ffmpeg and upload it to the server.
The server handles session management automatically.`,
		Example: `  doorbell-cli send -f message.mp3
  doorbell-cli send --file announcement.wav
  doorbell-cli send -f alert.m4a -s http://192.168.1.100:8080`,
		RunE: runSend,
	}

	cmd.Flags().StringVarP(&audioFile, "file", "f", "", "Audio file to send (required)")
	cmd.MarkFlagRequired("file")

	return cmd
}

func runSend(cmd *cobra.Command, args []string) error {
	// Check if file exists
	if _, err := os.Stat(audioFile); os.IsNotExist(err) {
		return fmt.Errorf("audio file not found: %s", audioFile)
	}

	// Check if ffmpeg is available
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not found in PATH. Please install ffmpeg")
	}

	// Convert audio file to G.711 µ-law
	log.Println("Converting audio file to G.711 µ-law...")
	convertedData, err := convertToG711u(audioFile)
	if err != nil {
		return fmt.Errorf("failed to convert audio: %w", err)
	}

	log.Printf("Converted %d bytes of audio data", len(convertedData))

	// Upload to server
	log.Println("Uploading audio file to server...")
	if err := uploadAudioFile(serverAddr, convertedData); err != nil {
		return fmt.Errorf("failed to upload audio: %w", err)
	}

	log.Println("Audio file played successfully!")
	return nil
}

func convertToG711u(inputFile string) ([]byte, error) {
	// Build ffmpeg command to convert to G.711 µ-law
	args := []string{
		"-i", inputFile,
		"-ar", "8000",      // Sample rate: 8000 Hz
		"-ac", "1",         // Channels: mono
		"-acodec", "pcm_mulaw",
		"-f", "mulaw",
		"-",                // Output to stdout
	}

	ffmpegCmd := exec.Command("ffmpeg", args...)

	var stdout, stderr bytes.Buffer
	ffmpegCmd.Stdout = &stdout
	ffmpegCmd.Stderr = &stderr

	if err := ffmpegCmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg conversion failed: %w\nStderr: %s", err, stderr.String())
	}

	return stdout.Bytes(), nil
}

func uploadAudioFile(serverAddr string, audioData []byte) error {
	// Create multipart form data
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add audio file
	part, err := writer.CreateFormFile("audio", "audio.raw")
	if err != nil {
		return fmt.Errorf("failed to create form file: %w", err)
	}

	_, err = part.Write(audioData)
	if err != nil {
		return fmt.Errorf("failed to write audio data: %w", err)
	}

	writer.Close()

	// Send POST request
	url := strings.TrimSuffix(serverAddr, "/") + "/api/audio/play-file"
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}
