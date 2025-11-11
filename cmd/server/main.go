package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/acardace/hikvision-doorbell-server/internal/api"
	"github.com/acardace/hikvision-doorbell-server/internal/config"
	"github.com/acardace/hikvision-doorbell-server/internal/hikvision"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create Hikvision client
	hikClient := hikvision.NewClient(
		cfg.Hikvision.Host,
		cfg.Hikvision.Username,
		cfg.Hikvision.Password,
	)

	// Test connection by getting channels
	log.Println("Testing connection to Hikvision device...")
	channelList, err := hikClient.GetTwoWayAudioChannels()
	if err != nil {
		log.Fatalf("Failed to connect to Hikvision device: %v", err)
	}
	log.Printf("Found %d two-way audio channels", len(channelList.Channels))

	for _, c := range channelList.Channels {
		if c.Enabled == "true" {
			if err := hikClient.CloseAudioChannel(c.ID); err != nil {
				log.Fatalf("Cannot re-initiliaze hikvision device")
			}
		}
	}

	// Create API handler
	handler := api.NewHandler(hikClient)
	router := handler.SetupRoutes()

	// Setup HTTP server
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	server := &http.Server{
		Addr:    addr,
		Handler: router,
	}

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("Starting server on %s", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Wait for interrupt signal
	<-sigChan
	log.Println("\nShutdown signal received, cleaning up...")

	// Close any active sessions
	if err := handler.CloseAllSessions(); err != nil {
		log.Printf("Warning: Error closing sessions: %v", err)
	}

	// Shutdown HTTP server with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}

	log.Println("Server stopped")
}
