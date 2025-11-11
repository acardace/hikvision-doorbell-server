package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/spf13/cobra"
)

var (
	speakDuration int
	inputDevice   string
)

func speakCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "speak",
		Short: "Speak to the doorbell using your microphone",
		Long: `Capture audio from your microphone and send it to the doorbell speaker in real-time using WebRTC.
Uses ffmpeg to capture audio from your system's default microphone or a specified input device.`,
		Example: `  doorbell-cli speak
  doorbell-cli speak -d 30
  doorbell-cli speak --device "hw:0"
  doorbell-cli speak -s http://192.168.1.100:8080`,
		RunE: runSpeak,
	}

	cmd.Flags().IntVarP(&speakDuration, "duration", "d", 0, "Duration in seconds (0 = until Ctrl+C)")
	cmd.Flags().StringVarP(&inputDevice, "device", "i", "default", "Input device (default, hw:0, etc.)")

	return cmd
}

func runSpeak(cmd *cobra.Command, args []string) error {
	// Check if ffmpeg is available
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not found in PATH. Please install ffmpeg")
	}

	// Setup signal handler for graceful cleanup
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Create WebRTC peer connection
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{},
	}

	peerConnection, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return fmt.Errorf("failed to create peer connection: %w", err)
	}
	defer peerConnection.Close()

	// Create audio track for PCMU (G.711 µ-law)
	audioTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypePCMU},
		"audio",
		"doorbell-cli",
	)
	if err != nil {
		return fmt.Errorf("failed to create audio track: %w", err)
	}

	// Add track to peer connection
	_, err = peerConnection.AddTrack(audioTrack)
	if err != nil {
		return fmt.Errorf("failed to add track: %w", err)
	}

	// Wait for ICE gathering to complete
	gatherComplete := make(chan struct{})
	peerConnection.OnICEGatheringStateChange(func(state webrtc.ICEGatheringState) {
		log.Printf("ICE Gathering State: %s", state.String())
		if state == webrtc.ICEGatheringStateComplete {
			close(gatherComplete)
		}
	})

	// Create offer
	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		return fmt.Errorf("failed to create offer: %w", err)
	}

	// Set local description (this triggers ICE gathering)
	err = peerConnection.SetLocalDescription(offer)
	if err != nil {
		return fmt.Errorf("failed to set local description: %w", err)
	}

	// Wait for ICE gathering to complete
	log.Println("Gathering ICE candidates...")
	<-gatherComplete

	// Send offer to server (now with all ICE candidates)
	log.Println("Connecting to server...")
	answer, err := sendOffer(serverAddr, *peerConnection.LocalDescription())
	if err != nil {
		return fmt.Errorf("failed to send offer: %w", err)
	}

	// Set remote description
	err = peerConnection.SetRemoteDescription(*answer)
	if err != nil {
		return fmt.Errorf("failed to set remote description: %w", err)
	}

	log.Println("WebRTC connection established")

	// Wait for ICE connection
	connectionEstablished := make(chan struct{})
	peerConnection.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("ICE Connection State: %s", state.String())
		if state == webrtc.ICEConnectionStateConnected {
			close(connectionEstablished)
		}
	})

	// Handle incoming audio track (from doorbell)
	var ffplayCmd *exec.Cmd
	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		log.Printf("Receiving audio from doorbell: %s, codec: %s", track.Kind(), track.Codec().MimeType)

		// Start ffplay to play incoming audio
		ffplayArgs := []string{
			"-f", "mulaw", // G.711 µ-law format
			"-sample_rate", "8000", // Sample rate
			"-ch_layout", "mono", // Mono
			"-nodisp",   // No video display
			"-autoexit", // Exit when done
			"-",         // Read from stdin
		}

		ffplayCmd = exec.Command("ffplay", ffplayArgs...)
		ffplayStdin, err := ffplayCmd.StdinPipe()
		if err != nil {
			log.Printf("Failed to create ffplay stdin pipe: %v", err)
			return
		}

		if err := ffplayCmd.Start(); err != nil {
			log.Printf("Failed to start ffplay: %v", err)
			return
		}

		log.Println("Started playback of incoming audio")

		// Read RTP packets and send to ffplay
		go func() {
			defer ffplayStdin.Close()
			defer ffplayCmd.Wait()

			for {
				rtp, _, err := track.ReadRTP()
				if err != nil {
					if err != io.EOF {
						log.Printf("Error reading RTP: %v", err)
					}
					return
				}

				// Write audio payload to ffplay
				_, err = ffplayStdin.Write(rtp.Payload)
				if err != nil {
					log.Printf("Error writing to ffplay: %v", err)
					return
				}
			}
		}()
	})

	// Wait for connection or timeout
	select {
	case <-connectionEstablished:
		log.Println("ICE connection established")
	case <-time.After(10 * time.Second):
		return fmt.Errorf("timeout waiting for ICE connection")
	}

	// Start ffmpeg to capture microphone input
	ffmpegArgs := []string{
		"-f", "alsa", // Linux audio input
		"-i", inputDevice, // Input device
		"-sample_rate", "8000", // Sample rate: 8000 Hz
		"-ch_layout", "mono", // Channels: mono
		"-f", "mulaw", // Output format: G.711 µ-law
		"-", // Output to stdout
	}

	log.Printf("Starting microphone capture (device: %s, format: G.711µ-law, 8000Hz, mono)", inputDevice)
	ffmpegCmd := exec.Command("ffmpeg", ffmpegArgs...)

	ffmpegStdout, err := ffmpegCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create ffmpeg stdout pipe: %w", err)
	}

	if err := ffmpegCmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	// Ensure ffmpeg and ffplay are killed on exit
	defer func() {
		if ffmpegCmd != nil && ffmpegCmd.Process != nil {
			ffmpegCmd.Process.Kill()
			ffmpegCmd.Wait()
		}
		if ffplayCmd != nil && ffplayCmd.Process != nil {
			ffplayCmd.Process.Kill()
			ffplayCmd.Wait()
		}
	}()

	if speakDuration > 0 {
		log.Printf("Speaking for %d seconds (or press Ctrl+C to stop)", speakDuration)
	} else {
		log.Println("Speaking... (press Ctrl+C to stop)")
	}

	// Setup timeout if duration is specified
	var timeoutChan <-chan time.Time
	if speakDuration > 0 {
		timeoutChan = time.After(time.Duration(speakDuration) * time.Second)
	}

	// Read audio from ffmpeg and send via WebRTC
	done := make(chan error, 1)
	totalBytes := 0

	go func() {
		buffer := make([]byte, 160) // 20ms of audio at 8000Hz (160 samples for G.711)
		for {
			n, err := ffmpegStdout.Read(buffer)
			if err != nil {
				if err != io.EOF {
					done <- err
				} else {
					done <- nil
				}
				return
			}

			if n > 0 {
				totalBytes += n

				// Send via WebRTC track
				if err := audioTrack.WriteSample(media.Sample{
					Data:     buffer[:n],
					Duration: time.Millisecond * 20,
				}); err != nil {
					done <- fmt.Errorf("failed to send audio sample: %w", err)
					return
				}

				// Log progress every 100KB
				if totalBytes%(100*1024) == 0 {
					log.Printf("Sent: %.2f MB", float64(totalBytes)/(1024*1024))
				}
			}
		}
	}()

	// Wait for completion or interrupt
	select {
	case <-sigChan:
		log.Println("\nReceived interrupt signal, stopping...")
	case <-timeoutChan:
		log.Println("\nDuration reached")
	case err := <-done:
		if err != nil {
			return fmt.Errorf("error during speaking: %w", err)
		}
	}

	log.Printf("Complete! Total bytes sent: %d (%.2f MB)", totalBytes, float64(totalBytes)/(1024*1024))
	return nil
}

func sendOffer(serverAddr string, offer webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	url := strings.TrimSuffix(serverAddr, "/") + "/api/webrtc/offer"

	offerJSON, err := json.Marshal(offer)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal offer: %w", err)
	}

	resp, err := http.Post(url, "application/json", bytes.NewReader(offerJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned status %d: %s", resp.StatusCode, string(body))
	}

	var answer webrtc.SessionDescription
	if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil {
		return nil, fmt.Errorf("failed to decode answer: %w", err)
	}

	return &answer, nil
}
