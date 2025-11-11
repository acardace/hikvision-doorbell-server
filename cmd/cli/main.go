package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	serverAddr string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "doorbell-cli",
		Short: "Hikvision Doorbell CLI",
		Long:  `A command-line tool to interact with the Hikvision Doorbell Middleware for two-way audio communication.`,
	}

	// Global flags
	rootCmd.PersistentFlags().StringVarP(&serverAddr, "server", "s", "http://localhost:8080", "Middleware server address")

	// Add commands
	rootCmd.AddCommand(sendCommand())
	rootCmd.AddCommand(speakCommand())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
