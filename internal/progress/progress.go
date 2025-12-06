// Package progress provides progress reporting for audio verification operations.
// This mirrors CueTools' CUEToolsProgressEventArgs and progress reporting pattern.
package progress

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// State represents the current progress of an operation.
// Mirrors CueTools CUEToolsProgressEventArgs.
type State struct {
	Status       string  // Status message (e.g., "Verifying track 01...")
	Percent      float64 // Progress 0.0-1.0
	CurrentTrack int     // Current track being processed (1-based, 0 for disc-wide)
	TotalTracks  int     // Total number of tracks
	SamplesRead  int64   // Samples processed so far
	TotalSamples int64   // Total samples to process
	InputPath    string  // Current input file path
}

// Callback is called when progress is updated.
// Returning an error will abort the operation.
type Callback func(state State) error

// Reporter manages progress reporting with configurable output.
type Reporter struct {
	mu           sync.Mutex
	callback     Callback
	lastUpdate   time.Time
	startTime    time.Time
	minInterval  time.Duration // Minimum time between updates (default: 100ms)
	state        State
	enabled      bool
	output       io.Writer
	lastLineLen  int
	showProgress bool // Whether to show progress bar
}

// Option configures a Reporter.
type Option func(*Reporter)

// WithCallback sets a custom progress callback.
func WithCallback(cb Callback) Option {
	return func(r *Reporter) {
		r.callback = cb
	}
}

// WithOutput sets the output writer for progress display.
func WithOutput(w io.Writer) Option {
	return func(r *Reporter) {
		r.output = w
	}
}

// WithMinInterval sets the minimum time between progress updates.
func WithMinInterval(d time.Duration) Option {
	return func(r *Reporter) {
		r.minInterval = d
	}
}

// WithProgressBar enables/disables the progress bar display.
func WithProgressBar(enabled bool) Option {
	return func(r *Reporter) {
		r.showProgress = enabled
	}
}

// NewReporter creates a new progress reporter.
func NewReporter(opts ...Option) *Reporter {
	r := &Reporter{
		enabled:      true,
		minInterval:  100 * time.Millisecond,
		output:       os.Stderr,
		showProgress: true,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Start initializes progress reporting for a new operation.
func (r *Reporter) Start(totalSamples int64, totalTracks int, inputPath string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.startTime = time.Now()
	r.lastUpdate = time.Time{}
	r.state = State{
		Status:       "Starting...",
		TotalSamples: totalSamples,
		TotalTracks:  totalTracks,
		InputPath:    inputPath,
	}
}

// Update reports progress. Respects minInterval to avoid excessive updates.
// Returns error from callback if any.
func (r *Reporter) Update(samplesRead int64, currentTrack int, status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.enabled {
		return nil
	}

	now := time.Now()
	// Skip update if too soon (unless this is the first update)
	if !r.lastUpdate.IsZero() && now.Sub(r.lastUpdate) < r.minInterval {
		return nil
	}

	r.state.SamplesRead = samplesRead
	r.state.CurrentTrack = currentTrack
	r.state.Status = status
	if r.state.TotalSamples > 0 {
		r.state.Percent = float64(samplesRead) / float64(r.state.TotalSamples)
	}
	r.lastUpdate = now

	// Display progress bar if enabled
	if r.showProgress && r.output != nil {
		r.displayProgress()
	}

	// Call custom callback if set
	if r.callback != nil {
		return r.callback(r.state)
	}
	return nil
}

// ForceUpdate updates progress immediately, ignoring minInterval.
func (r *Reporter) ForceUpdate(samplesRead int64, currentTrack int, status string) error {
	r.mu.Lock()
	r.lastUpdate = time.Time{} // Reset to force update
	r.mu.Unlock()
	return r.Update(samplesRead, currentTrack, status)
}

// Finish completes progress reporting.
func (r *Reporter) Finish() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.showProgress && r.output != nil {
		// Clear the progress line
		r.clearLine()
	}
}

// displayProgress renders the progress bar to output.
// CueTools format: "Verifying track 01 (45%)..." with ETA
func (r *Reporter) displayProgress() {
	elapsed := time.Since(r.startTime)
	percent := r.state.Percent * 100

	// Calculate ETA
	var eta string
	if r.state.Percent > 0.01 && elapsed > time.Second {
		totalTime := time.Duration(float64(elapsed) / r.state.Percent)
		remaining := totalTime - elapsed
		if remaining > 0 {
			eta = fmt.Sprintf(" ETA: %s", formatDuration(remaining))
		}
	}

	// Calculate speed (samples per second → seconds of audio per second)
	var speed string
	if elapsed > time.Second {
		samplesPerSec := float64(r.state.SamplesRead) / elapsed.Seconds()
		audioSpeed := samplesPerSec / 44100 // 44.1kHz
		if audioSpeed >= 1 {
			speed = fmt.Sprintf(" (%.1fx)", audioSpeed)
		}
	}

	// Build progress bar
	barWidth := 30
	filled := int(float64(barWidth) * r.state.Percent)
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

	// Format line: [████████░░░░░░░░░░░░░░░░░░░░░░] 45% Verifying track 01... (2.5x) ETA: 0:30
	line := fmt.Sprintf("\r[%s] %5.1f%% %s%s%s",
		bar, percent, r.state.Status, speed, eta)

	// Pad to clear previous line if it was longer
	if len(line) < r.lastLineLen {
		line += strings.Repeat(" ", r.lastLineLen-len(line))
	}
	r.lastLineLen = len(line)

	fmt.Fprint(r.output, line)
}

// clearLine clears the progress line.
func (r *Reporter) clearLine() {
	if r.lastLineLen > 0 {
		fmt.Fprint(r.output, "\r"+strings.Repeat(" ", r.lastLineLen)+"\r")
		r.lastLineLen = 0
	}
}

// formatDuration formats a duration as M:SS or H:MM:SS.
func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second

	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// Disabled returns a no-op reporter that doesn't output anything.
func Disabled() *Reporter {
	return &Reporter{enabled: false}
}
