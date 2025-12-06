package progress

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestReporterBasic(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporter(
		WithOutput(&buf),
		WithProgressBar(true),
		WithMinInterval(0), // No rate limiting for tests
	)

	r.Start(1000, 4, "test.flac")

	// First update
	err := r.Update(100, 1, "Verifying track 01...")
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Check output contains progress
	output := buf.String()
	if !strings.Contains(output, "Verifying track 01") {
		t.Errorf("Expected 'Verifying track 01' in output, got: %s", output)
	}
	if !strings.Contains(output, "10.0%") {
		t.Errorf("Expected '10.0%%' in output, got: %s", output)
	}

	r.Finish()
}

func TestReporterCallback(t *testing.T) {
	callbackCalled := false
	var lastState State

	r := NewReporter(
		WithProgressBar(false),
		WithCallback(func(state State) error {
			callbackCalled = true
			lastState = state
			return nil
		}),
		WithMinInterval(0),
	)

	r.Start(1000, 4, "test.flac")
	r.Update(500, 2, "Verifying track 02...")

	if !callbackCalled {
		t.Error("Callback was not called")
	}
	if lastState.SamplesRead != 500 {
		t.Errorf("Expected SamplesRead=500, got %d", lastState.SamplesRead)
	}
	if lastState.CurrentTrack != 2 {
		t.Errorf("Expected CurrentTrack=2, got %d", lastState.CurrentTrack)
	}
	if lastState.Percent != 0.5 {
		t.Errorf("Expected Percent=0.5, got %f", lastState.Percent)
	}
}

func TestReporterCallbackError(t *testing.T) {
	expectedErr := errors.New("callback error")

	r := NewReporter(
		WithProgressBar(false),
		WithCallback(func(state State) error {
			return expectedErr
		}),
		WithMinInterval(0),
	)

	r.Start(1000, 4, "test.flac")
	err := r.Update(500, 2, "Verifying...")

	if err != expectedErr {
		t.Errorf("Expected callback error, got: %v", err)
	}
}

func TestReporterRateLimiting(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporter(
		WithOutput(&buf),
		WithProgressBar(true),
		WithMinInterval(100 * time.Millisecond),
	)

	r.Start(1000, 4, "test.flac")

	// First update should always work
	r.Update(100, 1, "Verifying...")
	firstLen := buf.Len()

	// Immediate second update should be skipped
	r.Update(200, 1, "Verifying...")
	if buf.Len() != firstLen {
		t.Error("Rate limiting should have skipped second update")
	}

	// Wait and update should work
	time.Sleep(150 * time.Millisecond)
	r.Update(300, 1, "Verifying...")
	if buf.Len() == firstLen {
		t.Error("Update after interval should have been written")
	}

	r.Finish()
}

func TestReporterForceUpdate(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporter(
		WithOutput(&buf),
		WithProgressBar(true),
		WithMinInterval(1 * time.Hour), // Very long interval
	)

	r.Start(1000, 4, "test.flac")

	// First update
	r.Update(100, 1, "Verifying...")
	firstLen := buf.Len()

	// Normal update should be skipped
	r.Update(200, 1, "Verifying...")
	if buf.Len() != firstLen {
		t.Error("Normal update should have been skipped")
	}

	// Force update should work
	r.ForceUpdate(300, 1, "Verifying...")
	if buf.Len() == firstLen {
		t.Error("ForceUpdate should have been written")
	}

	r.Finish()
}

func TestReporterDisabled(t *testing.T) {
	r := Disabled()

	// Should not panic
	r.Start(1000, 4, "test.flac")
	err := r.Update(500, 2, "Verifying...")
	if err != nil {
		t.Errorf("Disabled reporter should not return error: %v", err)
	}
	r.Finish()
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d        time.Duration
		expected string
	}{
		{0, "0:00"},
		{30 * time.Second, "0:30"},
		{1*time.Minute + 30*time.Second, "1:30"},
		{10*time.Minute + 5*time.Second, "10:05"},
		{1*time.Hour + 2*time.Minute + 3*time.Second, "1:02:03"},
		{10*time.Hour + 30*time.Minute, "10:30:00"},
	}

	for _, tt := range tests {
		got := formatDuration(tt.d)
		if got != tt.expected {
			t.Errorf("formatDuration(%v) = %s, want %s", tt.d, got, tt.expected)
		}
	}
}

func TestStatePercentCalculation(t *testing.T) {
	r := NewReporter(
		WithProgressBar(false),
		WithMinInterval(0),
	)

	r.Start(1000, 4, "test.flac")
	r.Update(250, 1, "Verifying...")

	r.mu.Lock()
	percent := r.state.Percent
	r.mu.Unlock()

	if percent != 0.25 {
		t.Errorf("Expected percent=0.25, got %f", percent)
	}
}

func TestProgressBarFormat(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporter(
		WithOutput(&buf),
		WithProgressBar(true),
		WithMinInterval(0),
	)

	r.Start(100, 1, "test.flac")
	r.Update(50, 1, "Verifying...")

	output := buf.String()

	// Should contain progress bar characters
	if !strings.Contains(output, "█") && !strings.Contains(output, "░") {
		t.Error("Output should contain progress bar characters")
	}

	// Should contain percentage
	if !strings.Contains(output, "50.0%") {
		t.Errorf("Output should contain '50.0%%', got: %s", output)
	}

	// Should start with carriage return for line overwrite
	if !strings.HasPrefix(output, "\r") {
		t.Error("Output should start with carriage return")
	}

	r.Finish()
}
