package hikvision

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/icholy/digest"
)

// AudioStreamWriter continuously sends audio data to the device
type AudioStreamWriter struct {
	client    *Client
	session   *AudioSession
	url       string
	stopChan  chan struct{}
	dataChan  chan []byte
	errChan   chan error
	closeOnce sync.Once
}

// NewAudioStreamWriter creates a new continuous audio stream writer
func (c *Client) NewAudioStreamWriter(session *AudioSession) *AudioStreamWriter {
	url := fmt.Sprintf("http://%s/ISAPI/System/TwoWayAudio/channels/%s/audioData", c.host, session.ChannelID)
	// if session.SessionID != "" {
	// url += "?sessionId=" + session.SessionID
	// }

	return &AudioStreamWriter{
		client:   c,
		session:  session,
		url:      url,
		stopChan: make(chan struct{}),
		dataChan: make(chan []byte, 100),
		errChan:  make(chan error, 1),
	}
}

// Start begins the continuous sending loop
func (w *AudioStreamWriter) Start() {
	log.Printf("[Hikvision] AudioStreamWriter: Starting stream for channel %s", w.session.ChannelID)
	go w.sendLoop()
}

// sendLoop continuously sends audio data via a persistent connection
func (w *AudioStreamWriter) sendLoop() {
	// Create a custom transport that gives us access to the connection
	var conn net.Conn

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := net.Dial(network, addr)
			if err != nil {
				return nil, err
			}
			conn = c
			return c, nil
		},
	}

	// Create HTTP client with our transport wrapped in digest auth
	client := &http.Client{
		Transport: &digest.Transport{
			Username: w.client.username,
			Password: w.client.password,
			Transport: transport,
		},
	}

	// Make the PUT request to establish the connection
	req, err := http.NewRequest("PUT", w.url, nil)
	if err != nil {
		log.Printf("[Hikvision] AudioStreamWriter: Failed to create request: %v", err)
		w.errChan <- err
		return
	}

	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Length", "0")

	// Start the request in a goroutine - don't close response to keep connection alive
	respChan := make(chan *http.Response, 1)
	errChan := make(chan error, 1)

	go func() {
		resp, err := client.Do(req)
		if err != nil {
			errChan <- err
			return
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			log.Printf("[Hikvision] AudioStreamWriter: Error status %d, body: %s", resp.StatusCode, string(body))
			errChan <- fmt.Errorf("status %d", resp.StatusCode)
			return
		}

		log.Printf("[Hikvision] AudioStreamWriter: PUT request established (status %d)", resp.StatusCode)
		respChan <- resp
		// Don't close resp.Body - keep connection alive
	}()

	// Wait for the response
	var httpResp *http.Response
	select {
	case httpResp = <-respChan:
		// Success
	case err := <-errChan:
		w.errChan <- err
		return
	case <-time.After(5 * time.Second):
		log.Printf("[Hikvision] AudioStreamWriter: Timeout waiting for response")
		w.errChan <- fmt.Errorf("timeout")
		return
	}

	if conn == nil {
		log.Printf("[Hikvision] AudioStreamWriter: Connection not established")
		w.errChan <- fmt.Errorf("connection not established")
		return
	}

	log.Printf("[Hikvision] AudioStreamWriter: Connection established, ready to send audio")

	// Defer cleanup
	defer func() {
		if httpResp != nil && httpResp.Body != nil {
			httpResp.Body.Close()
		}
		if conn != nil {
			conn.Close()
		}
	}()

	// Now write audio data directly to the connection
	chunkCount := 0
	for {
		select {
		case <-w.stopChan:
			log.Printf("[Hikvision] AudioStreamWriter: Stopped after %d chunks", chunkCount)
			return

		case data := <-w.dataChan:
			if len(data) == 0 {
				continue
			}

			chunkCount++
			_, err := conn.Write(data)
			if err != nil {
				log.Printf("[Hikvision] AudioStreamWriter: Failed to write data: %v", err)
				w.errChan <- err
				return
			}

			// Add delay to match audio playback rate
			// G.711 is 8000 samples/sec = 8000 bytes/sec
			// For each chunk, delay = (chunk_size / 8000) seconds
			chunkDuration := time.Duration(len(data)) * time.Second / 8000
			time.Sleep(chunkDuration)

			if chunkCount%100 == 0 {
				log.Printf("[Hikvision] AudioStreamWriter: Sent %d chunks so far", chunkCount)
			}
		}
	}
}

// Write implements io.Writer interface
func (w *AudioStreamWriter) Write(p []byte) (n int, err error) {
	data := make([]byte, len(p))
	copy(data, p)

	select {
	case w.dataChan <- data:
		return len(p), nil
	case <-w.stopChan:
		return 0, io.ErrClosedPipe
	case err := <-w.errChan:
		return 0, err
	}
}

// Close stops the audio stream writer
func (w *AudioStreamWriter) Close() error {
	w.closeOnce.Do(func() {
		close(w.stopChan)
	})
	return nil
}
