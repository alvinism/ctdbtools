package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"ctdbtools/internal/audio"
	"ctdbtools/internal/ingest"

	"github.com/spf13/cobra"
	"golang.org/x/term"
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
Use --no-ar, --no-ctdb, or --no-parity to disable specific features.

Progress bar is shown by default when running in a terminal.
Use --progress or --no-progress to override.`,
	Args: cobra.ExactArgs(1),
	RunE: runVerify,
}

var repairCmd = &cobra.Command{
	Use:   "repair <path>",
	Short: "Repair audio file using CTDB parity data",
	Long: `Repair audio files using Reed-Solomon error correction with CTDB parity data.

The path can be:
  - A CUE file (.cue) - parses track layout from CUE sheet
  - A directory - auto-discovers audio files and optional CUE sheet

The repair process:
  1. Queries CTDB for parity data
  2. Detects errors using Berlekamp-Massey and Chien search
  3. Calculates error magnitudes using Forney algorithm
  4. Applies XOR corrections to produce corrected output

Output is written to a directory containing:
  - Corrected audio files (FLAC by default, or WAV with --format wav)
  - CUE sheet
  - Repair log with details

Use --dry-run to see what would be repaired without writing files.`,
	Args: cobra.ExactArgs(1),
	RunE: runRepair,
}

// Verify command flags
var (
	cuePath              string // deprecated, kept for backward compatibility
	flagNoAR             bool
	flagNoCTDB           bool
	flagNoParity         bool
	flagVerbose          bool
	flagDebug            bool
	flagProgress         bool // Show progress bar
	flagNoProgress       bool // Explicitly disable progress bar
	flagSeparateDecoding bool // Use separate goroutine for decoding
	stride               int
	lastStride           int
	npar                 int
)

// Repair command flags
var (
	repairOutput       string
	repairAuto         bool
	repairDryRun       bool
	repairForce        bool
	repairVerbose      bool
	repairFormat       string
	repairEncoder      string
	repairCompression  int
	repairCopyMetadata bool
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
	verifyCmd.Flags().BoolVar(&flagProgress, "progress", false, "Show progress bar (default: auto-detect terminal)")
	verifyCmd.Flags().BoolVar(&flagNoProgress, "no-progress", false, "Disable progress bar")
	verifyCmd.Flags().BoolVar(&flagSeparateDecoding, "separate-decoding", true, "Use separate goroutine for decoding (like CueTools)")

	// Advanced parity options
	verifyCmd.Flags().IntVar(&stride, "stride", 588*10*2, "Parity stride in samples")
	verifyCmd.Flags().IntVar(&lastStride, "last-stride", 588*10*2, "Last stride in samples (defaults to stride)")
	verifyCmd.Flags().IntVar(&npar, "npar", 16, "Number of parity symbols (max 16)")

	rootCmd.AddCommand(verifyCmd)

	// Repair command flags
	repairCmd.Flags().StringVarP(&repairOutput, "output", "o", "", "Output directory path (default: <input>_repaired/)")
	repairCmd.Flags().BoolVar(&repairAuto, "auto", false, "Auto-select highest confidence CTDB entry")
	repairCmd.Flags().BoolVar(&repairDryRun, "dry-run", false, "Show repair info without writing files")
	repairCmd.Flags().BoolVar(&repairForce, "force", false, "Overwrite existing output directory")
	repairCmd.Flags().BoolVarP(&repairVerbose, "verbose", "v", false, "Verbose output")
	repairCmd.Flags().BoolVar(&flagProgress, "progress", false, "Show progress bar (default: auto-detect terminal)")
	repairCmd.Flags().BoolVar(&flagNoProgress, "no-progress", false, "Disable progress bar")

	// Output format flags
	repairCmd.Flags().StringVar(&repairFormat, "format", "flac", "Output format: flac, wav")
	repairCmd.Flags().StringVar(&repairEncoder, "encoder", "auto", "FLAC encoder: auto, native, ffmpeg")
	repairCmd.Flags().IntVar(&repairCompression, "compression", 8, "FLAC compression level 0-8")
	repairCmd.Flags().BoolVar(&repairCopyMetadata, "copy-metadata", true, "Copy metadata from source files")

	rootCmd.AddCommand(repairCmd)
}

// shouldShowProgress determines if the progress bar should be shown.
// Returns true if:
// - --progress flag is explicitly set, OR
// - Running in a terminal (isatty) AND --no-progress is not set
func shouldShowProgress() bool {
	if flagNoProgress {
		return false
	}
	if flagProgress {
		return true
	}
	// Auto-detect: show progress if stderr is a terminal
	return term.IsTerminal(int(os.Stderr.Fd()))
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
		CuePath:          cueFile,
		DirPath:          dirPath,
		Stride:           stride,
		LastStride:       lastStride,
		Npar:             npar,
		CalcParity:       !flagNoParity,
		QueryAR:          !flagNoAR,
		QueryCTDB:        !flagNoCTDB,
		Verbose:          flagVerbose,
		Debug:            flagDebug,
		ShowProgress:     shouldShowProgress(),
		SeparateDecoding: flagSeparateDecoding,
	}

	return Verify(ctx, opts)
}

func runRepair(cmd *cobra.Command, args []string) error {
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

	// Determine input type
	var cueFile string
	var dirPath string

	if info.IsDir() {
		dirPath = inputPath
	} else if strings.HasSuffix(strings.ToLower(inputPath), ".cue") {
		cueFile = inputPath
	} else {
		return fmt.Errorf("path must be a CUE file (.cue) or directory: %s", inputPath)
	}

	// Determine output directory
	outputDir := repairOutput
	if outputDir == "" {
		// Default: _repaired/ inside input directory (or CUE file's directory)
		if info.IsDir() {
			outputDir = filepath.Join(inputPath, "_repaired")
		} else {
			outputDir = filepath.Join(filepath.Dir(inputPath), "_repaired")
		}
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

	// Parse output format
	format, err := audio.ParseOutputFormat(repairFormat)
	if err != nil {
		return err
	}

	// Parse encoder preference
	encoder, err := audio.ParseEncoderPreference(repairEncoder)
	if err != nil {
		return err
	}

	// Validate compression level
	if repairCompression < 0 || repairCompression > 8 {
		return fmt.Errorf("compression level must be 0-8, got %d", repairCompression)
	}

	opts := RepairOptions{
		CuePath:      cueFile,
		DirPath:      dirPath,
		OutputDir:    outputDir,
		Stride:       588 * 10 * 2, // Default stride
		Npar:         8,            // Default npar
		Auto:         repairAuto,
		DryRun:       repairDryRun,
		Force:        repairForce,
		Verbose:      repairVerbose,
		ShowProgress: shouldShowProgress(),
		Format:       format,
		Encoder:      encoder,
		Compression:  repairCompression,
		CopyMetadata: repairCopyMetadata,
	}

	return Repair(ctx, opts)
}

// Execute is the entry point for the ctdbtools CLI.
func Execute() error {
	return rootCmd.Execute()
}
