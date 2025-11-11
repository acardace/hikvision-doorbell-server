package hikvision

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
)

// AudioStreamReader continuously reads audio data from the device
type AudioStreamReader struct {
	client    *Client
	session   *AudioSession
	url       string
	stopChan  chan struct{}
	dataChan  chan []byte
	errChan   chan error
	closeOnce sync.Once
}

// NewAudioStreamReader creates a new continuous audio stream reader
func (c *Client) NewAudioStreamReader(session *AudioSession) *AudioStreamReader {
	url := fmt.Sprintf("http://%s/ISAPI/System/TwoWayAudio/channels/%s/audioData", c.host, session.ChannelID)
	if session.SessionID != "" {
		url += "?sessionId=" + session.SessionID
	}

	return &AudioStreamReader{
		client:   c,
		session:  session,
		url:      url,
		stopChan: make(chan struct{}),
		dataChan: make(chan []byte, 128),
		errChan:  make(chan error, 1),
	}
}

// Start begins the continuous streaming
func (a *AudioStreamReader) Start() {
	log.Printf("[Hikvision] AudioStreamReader: Starting stream for channel %s", a.session.ChannelID)
	go a.streamLoop()
}

// streamLoop continuously reads audio data from a single persistent connection
func (a *AudioStreamReader) streamLoop() {
	// Make a single GET request that stays open
	req, err := http.NewRequest("GET", a.url, nil)
	if err != nil {
		log.Printf("[Hikvision] AudioStreamReader: Failed to create request: %v", err)
		a.errChan <- err
		return
	}

	// Set headers like go2rtc does
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Length", "0")

	resp, err := a.client.client.Do(req)
	if err != nil {
		log.Printf("[Hikvision] AudioStreamReader: Request failed: %v", err)
		a.errChan <- err
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[Hikvision] AudioStreamReader: Error status %d, body: %s", resp.StatusCode, string(body))
		a.errChan <- fmt.Errorf("failed to get audio data: status %d, body: %s", resp.StatusCode, string(body))
		return
	}

	log.Printf("[Hikvision] AudioStreamReader: Connected, streaming audio data...")

	// Continuously read from the persistent connection
	buffer := make([]byte, 8192)
	chunkCount := 0

	for {
		select {
		case <-a.stopChan:
			log.Printf("[Hikvision] AudioStreamReader: Stopped after %d chunks", chunkCount)
			return
		default:
			n, err := resp.Body.Read(buffer)
			if n > 0 {
				chunkCount++
				// Make a copy of the data to send to channel
				data := make([]byte, n)
				copy(data, buffer[:n])

				select {
				case a.dataChan <- data:
					if chunkCount%100 == 0 {
						log.Printf("[Hikvision] AudioStreamReader: Read %d chunks so far", chunkCount)
					}
				case <-a.stopChan:
					log.Printf("[Hikvision] AudioStreamReader: Stopped while sending chunk %d", chunkCount)
					return
				}
			}

			if err != nil {
				if err == io.EOF {
					log.Printf("[Hikvision] AudioStreamReader: Stream ended (EOF) after %d chunks", chunkCount)
				} else {
					log.Printf("[Hikvision] AudioStreamReader: Read error after %d chunks: %v", chunkCount, err)
					a.errChan <- err
				}
				return
			}
		}
	}
}

// Read implements io.Reader interface
func (a *AudioStreamReader) Read(p []byte) (int, error) {
	select {
	case data := <-a.dataChan:
		n := copy(p, data)
		return n, nil
	case err := <-a.errChan:
		return 0, err
	case <-a.stopChan:
		return 0, io.EOF
	}
}

// Close stops the audio stream
func (a *AudioStreamReader) Close() error {
	a.closeOnce.Do(func() {
		close(a.stopChan)
	})
	return nil
}
