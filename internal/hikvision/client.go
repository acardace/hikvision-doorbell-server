package hikvision

import (
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/icholy/digest"
)

// Client handles communication with Hikvision ISAPI
type Client struct {
	host     string
	username string
	password string
	client   *http.Client
}

// TwoWayAudioChannelList represents the list of available two-way audio channels
type TwoWayAudioChannelList struct {
	XMLName  xml.Name             `xml:"TwoWayAudioChannelList"`
	Channels []TwoWayAudioChannel `xml:"TwoWayAudioChannel"`
}

// TwoWayAudioChannel represents a single two-way audio channel
type TwoWayAudioChannel struct {
	ID                   string `xml:"id"`
	Enabled              string `xml:"enabled"`
	AudioInputID         string `xml:"audioInputID"`
	AudioOutputID        string `xml:"audioOutputID"`
	AudioCompressionType string `xml:"audioCompressionType"`
}

// ResponseStatus represents ISAPI response status
type ResponseStatus struct {
	XMLName       xml.Name `xml:"ResponseStatus"`
	RequestURL    string   `xml:"requestURL"`
	StatusCode    int      `xml:"statusCode"`
	StatusString  string   `xml:"statusString"`
	SubStatusCode string   `xml:"subStatusCode"`
}

// AudioSession represents an active two-way audio session
type AudioSession struct {
	ChannelID string
	SessionID string
}

// TwoWayAudioSession represents the XML response from opening a channel
type TwoWayAudioSession struct {
	XMLName   xml.Name `xml:"TwoWayAudioSession"`
	SessionID string   `xml:"sessionId"`
}

// NewClient creates a new Hikvision ISAPI client
func NewClient(host, username, password string) *Client {
	// Create a digest transport that will handle auth challenges
	transport := &digest.Transport{
		Username: username,
		Password: password,
	}

	// Wrap in a custom RoundTripper that logs auth challenges
	retryTransport := &retryRoundTripper{
		transport: transport,
	}

	return &Client{
		host:     host,
		username: username,
		password: password,
		client: &http.Client{
			Transport: retryTransport,
		},
	}
}

// loggingRoundTripper wraps digest.Transport to log auth attempts
type retryRoundTripper struct {
	transport http.RoundTripper
}

func (l *retryRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := l.transport.RoundTrip(req)

	if err != nil {
		log.Printf("[Hikvision] Transport error: %v", err)
		return resp, err
	}

	// Handle buggy 401 responses from Hikvision that have empty WWW-Authenticate headers
	if resp.StatusCode == 401 {
		wwwAuth := resp.Header.Get("WWW-Authenticate")
		// If we get 401 with empty auth header, retry once
		if wwwAuth == "" {
			log.Printf("[Hikvision] Empty WWW-Authenticate header, retrying request...")
			resp.Body.Close()

			// Clone the request for retry
			retryReq := req.Clone(req.Context())
			return l.transport.RoundTrip(retryReq)
		}
	}

	return resp, err
}

// GetTwoWayAudioChannels retrieves available two-way audio channels
func (c *Client) GetTwoWayAudioChannels() (*TwoWayAudioChannelList, error) {
	url := fmt.Sprintf("http://%s/ISAPI/System/TwoWayAudio/channels", c.host)
	resp, err := c.client.Get(url)
	if err != nil {
		log.Printf("[Hikvision] GetTwoWayAudioChannels: Request failed: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[Hikvision] GetTwoWayAudioChannels: Error response body: %s", string(body))
		return nil, fmt.Errorf("failed to get channels: status %d, body: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var channels TwoWayAudioChannelList
	if err := xml.Unmarshal(body, &channels); err != nil {
		log.Printf("[Hikvision] GetTwoWayAudioChannels: Failed to parse XML: %v", err)
		return nil, err
	}

	log.Printf("[Hikvision] GetTwoWayAudioChannels: Found %d channels", len(channels.Channels))
	for i, ch := range channels.Channels {
		log.Printf("[Hikvision] GetTwoWayAudioChannels: Channel %d - ID: %s, Enabled: %s, Codec: %s",
			i, ch.ID, ch.Enabled, ch.AudioCompressionType)
	}

	return &channels, nil
}

// OpenAudioChannel opens a two-way audio channel and returns the session
func (c *Client) OpenAudioChannel(channelID string) (*AudioSession, error) {
	url := fmt.Sprintf("http://%s/ISAPI/System/TwoWayAudio/channels/%s/open", c.host, channelID)

	req, err := http.NewRequest("PUT", url, nil)
	if err != nil {
		log.Printf("[Hikvision] OpenAudioChannel: Failed to create request: %v", err)
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		log.Printf("[Hikvision] OpenAudioChannel: Request failed: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[Hikvision] OpenAudioChannel: Error response body: %s", string(body))
		return nil, fmt.Errorf("failed to open channel: status %d, body: %s", resp.StatusCode, string(body))
	}

	// Parse the XML response to get the sessionId
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var sessionResp TwoWayAudioSession
	if err := xml.Unmarshal(body, &sessionResp); err != nil {
		log.Printf("[Hikvision] OpenAudioChannel: Failed to parse XML: %v", err)
		return nil, fmt.Errorf("failed to parse session response: %w", err)
	}

	log.Printf("[Hikvision] OpenAudioChannel: Session opened - Channel: %s, SessionID: %s", channelID, sessionResp.SessionID)

	return &AudioSession{
		ChannelID: channelID,
		SessionID: sessionResp.SessionID,
	}, nil
}

// CloseAudioChannel closes an active two-way audio session
func (c *Client) CloseAudioChannel(channelID string) error {
	url := fmt.Sprintf("http://%s/ISAPI/System/TwoWayAudio/channels/%s/close", c.host, channelID)

	req, err := http.NewRequest("PUT", url, nil)
	if err != nil {
		log.Printf("[Hikvision] CloseAudioChannel: Failed to create request: %v", err)
		return err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		log.Printf("[Hikvision] CloseAudioChannel: Request failed: %v", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[Hikvision] CloseAudioChannel: Error response body: %s", string(body))
		return fmt.Errorf("failed to close channel: status %d", resp.StatusCode)
	}

	log.Printf("[Hikvision] CloseAudioChannel: Channel %s closed successfully", channelID)
	return nil
}
