package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var (
	Version   = "0.1.0-dev"
	GitCommit = "none"
	BuildDate = "unknown"

	versionCmd = &cobra.Command{
		Use:   "version",
		Short: "Print the version number of agyent",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("agyent version %s\n", Version)
			fmt.Printf("  Git Commit: %s\n", GitCommit)
			fmt.Printf("  Build Date: %s\n", BuildDate)
			fmt.Printf("  Go Version: %s\n", runtime.Version())
			fmt.Printf("  OS/Arch:    %s/%s\n", runtime.GOOS, runtime.GOARCH)
		},
	}
)

func init() {
	rootCmd.AddCommand(versionCmd)
}
