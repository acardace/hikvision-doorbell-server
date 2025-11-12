package api

import (
	"log/slog"
	"os"
	"strings"

	"github.com/acardace/hikvision-doorbell-server/internal/logger"
	"github.com/pion/webrtc/v4"
)

// WebRTCConfig holds configuration for WebRTC connections
type WebRTCConfig struct {
	// Port is the UDP port to use for WebRTC (default: 50000)
	Port uint16

	// PublicIP is the public IP address to advertise for ICE candidates
	PublicIP string

	// PublicIPFile is the path to a file containing the public IP
	// (useful when IP is set by init containers in Kubernetes)
	PublicIPFile string
}

// NewWebRTCConfig creates a new WebRTC configuration with defaults
func NewWebRTCConfig() *WebRTCConfig {
	return &WebRTCConfig{
		Port: 50000, // Default port
	}
}

// LoadFromEnv loads configuration from environment variables
func (c *WebRTCConfig) LoadFromEnv() error {
	// Load public IP from environment variable
	if ip := os.Getenv("WEBRTC_PUBLIC_IP"); ip != "" {
		c.PublicIP = ip
	}

	// Load public IP file path
	if ipFile := os.Getenv("WEBRTC_PUBLIC_IP_FILE"); ipFile != "" {
		c.PublicIPFile = ipFile

		// Try to read the file
		if data, err := os.ReadFile(ipFile); err == nil {
			c.PublicIP = strings.TrimSpace(string(data))
		} else {
			logger.Log.Warn("could not read public IP from file",
				slog.String("component", "webrtc_config"),
				slog.String("file", ipFile),
				slog.String("error", err.Error()))
		}
	}

	if c.PublicIP != "" {
		logger.Log.Info("loaded WebRTC public IP",
			slog.String("component", "webrtc_config"),
			slog.String("ip", c.PublicIP))
	} else {
		logger.Log.Warn("no public IP configured, ICE candidates may not work over NAT/VPN",
			slog.String("component", "webrtc_config"))
	}

	return nil
}

// CreateAPI creates a WebRTC API with the configured settings
func (c *WebRTCConfig) CreateAPI() (*webrtc.API, error) {
	settingEngine := webrtc.SettingEngine{}

	// Only use UDP4 (no TCP, no IPv6)
	settingEngine.SetNetworkTypes([]webrtc.NetworkType{
		webrtc.NetworkTypeUDP4,
	})

	// Use fixed UDP port (single user at a time)
	if err := settingEngine.SetEphemeralUDPPortRange(c.Port, c.Port); err != nil {
		logger.Log.Error("failed to set UDP port range",
			slog.String("component", "webrtc_config"),
			slog.Int("port", int(c.Port)),
			slog.String("error", err.Error()))
		return nil, err
	}

	// Set public IP for NAT traversal if configured
	if c.PublicIP != "" {
		logger.Log.Info("configuring NAT 1:1 IP mapping",
			slog.String("component", "webrtc_config"),
			slog.String("ip", c.PublicIP))
		settingEngine.SetNAT1To1IPs([]string{c.PublicIP}, webrtc.ICECandidateTypeHost)
	}

	return webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine)), nil
}

// CreatePeerConnection creates a new WebRTC peer connection with the configured API
func (c *WebRTCConfig) CreatePeerConnection() (*webrtc.PeerConnection, error) {
	api, err := c.CreateAPI()
	if err != nil {
		return nil, err
	}

	// Create WebRTC configuration (no ICE servers for local/VPN use)
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{},
	}

	peerConnection, err := api.NewPeerConnection(config)
	if err != nil {
		logger.Log.Error("failed to create peer connection",
			slog.String("component", "webrtc_config"),
			slog.String("error", err.Error()))
		return nil, err
	}

	logger.Log.Info("created WebRTC peer connection",
		slog.String("component", "webrtc_config"),
		slog.Int("port", int(c.Port)))

	return peerConnection, nil
}
