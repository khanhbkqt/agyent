package main

import (
	"fmt"
	"os"

	"agyent/internal/logger"

	"github.com/spf13/cobra"
)

var (
	cfgFile string
	verbose bool

	rootCmd = &cobra.Command{
		Use:   "agyent",
		Short: "agyent - High-performance Go Core Gateway Daemon for Antigravity (AGY)",
		Long: `agyent is a Go-first, zero-CGO gateway daemon connecting Telegram and other messaging 
channels to Antigravity CLI (agy) agents with multi-project support and automated media synchronization.`,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			lvl := "info"
			if verbose {
				lvl = "debug"
			}
			logger.Init(logger.Options{
				Level:     lvl,
				Format:    "text",
				AddSource: verbose,
			})
		},
	}
)

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "~/.agyent/config.yaml", "Path to configuration file")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logging output")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
