package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "ctdbtool",
	Short: "CUETools database verification tool",
	Long:  `A CLI tool that replicates CUETools verify/repair functionality for CD ripping verification.`,
}

var verifyCmd = &cobra.Command{
	Use:   "verify [audio-file]",
	Short: "Verify audio file against AccurateRip and CTDB databases",
	Long: `Verify an audio file (FLAC, WAV, etc.) against the AccurateRip and CUETools databases.

Requires a CUE file to determine track layout. Computes CRCs and compares against
database entries to verify rip accuracy.

For split-track CUE files (multiple FILE directives), the audio file argument is optional
as the audio paths are embedded in the CUE sheet.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runVerify,
}

// Verify command flags
var (
	cuePath       string
	flagQueryAR   bool
	flagQueryCTDB bool
	flagVerbose   bool
	flagDebug     bool
	stride        int
	lastStride    int
	npar          int
	calcParity    bool
)

func init() {
	verifyCmd.Flags().StringVarP(&cuePath, "cue", "c", "", "Path to CUE file (required)")
	verifyCmd.Flags().BoolVar(&flagQueryAR, "ar", false, "Query AccurateRip database")
	verifyCmd.Flags().BoolVar(&flagQueryCTDB, "ctdb", false, "Query CTDB database")
	verifyCmd.Flags().BoolVarP(&flagVerbose, "verbose", "v", false, "Verbose output")
	verifyCmd.Flags().BoolVar(&flagDebug, "debug", false, "Debug output (dump CRC state for comparison with CueTools)")
	verifyCmd.Flags().IntVar(&stride, "stride", 588*10*2, "Parity stride in samples")
	verifyCmd.Flags().IntVar(&lastStride, "last-stride", 588*10*2, "Last stride in samples (defaults to stride)")
	verifyCmd.Flags().IntVar(&npar, "npar", 8, "Number of parity symbols")
	verifyCmd.Flags().BoolVar(&calcParity, "parity", false, "Calculate parity/syndrome")

	verifyCmd.MarkFlagRequired("cue")

	rootCmd.AddCommand(verifyCmd)
}

func runVerify(cmd *cobra.Command, args []string) error {
	var audioPath string
	if len(args) > 0 {
		audioPath = args[0]
		// Check audio file exists
		if _, err := os.Stat(audioPath); os.IsNotExist(err) {
			return fmt.Errorf("audio file not found: %s", audioPath)
		}
	}

	// Check CUE file exists
	if _, err := os.Stat(cuePath); os.IsNotExist(err) {
		return fmt.Errorf("CUE file not found: %s", cuePath)
	}

	// Use -1 as sentinel for "auto-compute" if not explicitly set
	if !cmd.Flags().Changed("last-stride") {
		lastStride = -1
	}

	// Setup context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle interrupt signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nInterrupted, cancelling...")
		cancel()
	}()

	opts := VerifyOptions{
		AudioPath:  audioPath,
		CuePath:    cuePath,
		Stride:     stride,
		LastStride: lastStride,
		Npar:       npar,
		CalcParity: calcParity,
		QueryAR:    flagQueryAR,
		QueryCTDB:  flagQueryCTDB,
		Verbose:    flagVerbose,
		Debug:      flagDebug,
	}

	return Verify(ctx, opts)
}

// Execute is the entry point for the ctdbtool CLI.
func Execute() error {
	return rootCmd.Execute()
}
