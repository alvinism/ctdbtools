package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"ctdbtools/internal/ingest"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "ctdbtools",
	Short: "CUETools database tools",
	Long:  `A tool that replicates CUETools verify/repair functionality for CD ripping verification.`,
}

var verifyCmd = &cobra.Command{
	Use:   "verify <path>",
	Short: "Verify audio file against AccurateRip and CTDB databases",
	Long: `Verify audio files against the AccurateRip and CUETools databases.

The path can be:
  - A CUE file (.cue) - parses track layout from CUE sheet
  - A directory - auto-discovers audio files and optional CUE sheet

By default, queries both AccurateRip and CTDB with parity-based error detection.
Use --no-ar, --no-ctdb, or --no-parity to disable specific features.`,
	Args: cobra.ExactArgs(1),
	RunE: runVerify,
}

// Verify command flags
var (
	cuePath       string // deprecated, kept for backward compatibility
	flagNoAR      bool
	flagNoCTDB    bool
	flagNoParity  bool
	flagVerbose   bool
	flagDebug     bool
	stride        int
	lastStride    int
	npar          int
)

func init() {
	// Deprecated: --cue flag kept for backward compatibility
	verifyCmd.Flags().StringVarP(&cuePath, "cue", "c", "", "Path to CUE file (deprecated: use positional argument)")
	verifyCmd.Flags().MarkDeprecated("cue", "use positional argument instead")

	// Feature disable flags (features are enabled by default)
	verifyCmd.Flags().BoolVar(&flagNoAR, "no-ar", false, "Disable AccurateRip database query")
	verifyCmd.Flags().BoolVar(&flagNoCTDB, "no-ctdb", false, "Disable CTDB database query")
	verifyCmd.Flags().BoolVar(&flagNoParity, "no-parity", false, "Disable parity/syndrome calculation")

	// Output options
	verifyCmd.Flags().BoolVarP(&flagVerbose, "verbose", "v", false, "Verbose output")
	verifyCmd.Flags().BoolVar(&flagDebug, "debug", false, "Debug output (dump CRC state for comparison with CueTools)")

	// Advanced parity options
	verifyCmd.Flags().IntVar(&stride, "stride", 588*10*2, "Parity stride in samples")
	verifyCmd.Flags().IntVar(&lastStride, "last-stride", 588*10*2, "Last stride in samples (defaults to stride)")
	verifyCmd.Flags().IntVar(&npar, "npar", 8, "Number of parity symbols")

	rootCmd.AddCommand(verifyCmd)
}

func runVerify(cmd *cobra.Command, args []string) error {
	// Check required dependencies first
	if err := ingest.CheckDependencies(); err != nil {
		return err
	}

	inputPath := args[0]

	// Check input path exists
	info, err := os.Stat(inputPath)
	if os.IsNotExist(err) {
		return fmt.Errorf("path not found: %s", inputPath)
	}
	if err != nil {
		return fmt.Errorf("cannot access path: %w", err)
	}

	// Determine input type and set paths accordingly
	var cueFile string
	var dirPath string

	if info.IsDir() {
		// Directory mode: will auto-discover files
		dirPath = inputPath
	} else if strings.HasSuffix(strings.ToLower(inputPath), ".cue") {
		// CUE file mode
		cueFile = inputPath
	} else {
		return fmt.Errorf("path must be a CUE file (.cue) or directory: %s", inputPath)
	}

	// Handle deprecated --cue flag (backward compatibility)
	if cuePath != "" && cueFile == "" {
		cueFile = cuePath
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
		CuePath:    cueFile,
		DirPath:    dirPath,
		Stride:     stride,
		LastStride: lastStride,
		Npar:       npar,
		CalcParity: !flagNoParity,
		QueryAR:    !flagNoAR,
		QueryCTDB:  !flagNoCTDB,
		Verbose:    flagVerbose,
		Debug:      flagDebug,
	}

	return Verify(ctx, opts)
}

// Execute is the entry point for the ctdbtools CLI.
func Execute() error {
	return rootCmd.Execute()
}
