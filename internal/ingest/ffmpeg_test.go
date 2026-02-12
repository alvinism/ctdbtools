package ingest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// testDataDir returns the absolute path to the test fixtures directory.
func testDataDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("unable to determine test file path")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "cuetools.net", "CUETools", "CUETools.TestCodecs", "Data")
}

// skipIfNoFFprobe skips the test if ffprobe is not available.
func skipIfNoFFprobe(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not available, skipping test")
	}
}

// skipIfNoFFmpeg skips the test if ffmpeg is not available.
func skipIfNoFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available, skipping test")
	}
}

func TestProbeSampleRate(t *testing.T) {
	skipIfNoFFprobe(t)
	dataDir := testDataDir(t)

	tests := []struct {
		name     string
		file     string
		wantRate int
	}{
		{"CD format", "test.flac", 44100},
		{"Hi-res", "hires1.flac", 96000},
		{"Padded 24-bit", "padded24.flac", 44100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dataDir, tt.file)
			rate, err := ProbeSampleRate(context.Background(), path)
			if err != nil {
				t.Fatalf("ProbeSampleRate(%s) error: %v", tt.file, err)
			}
			if rate != tt.wantRate {
				t.Errorf("ProbeSampleRate(%s) = %d, want %d", tt.file, rate, tt.wantRate)
			}
		})
	}
}

func TestProbeSampleCount(t *testing.T) {
	skipIfNoFFprobe(t)
	dataDir := testDataDir(t)

	tests := []struct {
		name string
		file string
		want int64
	}{
		{"CD 16-bit", "test.flac", 42000},
		{"Padded 24-bit", "padded24.flac", 4410},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dataDir, tt.file)
			samples, err := ProbeSampleCount(context.Background(), path)
			if err != nil {
				t.Fatalf("ProbeSampleCount(%s) error: %v", tt.file, err)
			}
			if samples != tt.want {
				t.Errorf("ProbeSampleCount(%s) = %d, want %d", tt.file, samples, tt.want)
			}
		})
	}
}

func TestProbeSampleCountVsFrameRounding(t *testing.T) {
	skipIfNoFFprobe(t)
	dataDir := testDataDir(t)

	// test.flac has 42000 samples (42000 % 588 = 252, not frame-aligned)
	// ProbeDurationFrames rounds to 71 frames → 71*588 = 41748 (off by 252)
	// ProbeSampleCount must return the exact 42000
	path := filepath.Join(dataDir, "test.flac")

	samples, err := ProbeSampleCount(context.Background(), path)
	if err != nil {
		t.Fatalf("ProbeSampleCount error: %v", err)
	}
	frames, err := ProbeDurationFrames(context.Background(), path)
	if err != nil {
		t.Fatalf("ProbeDurationFrames error: %v", err)
	}

	frameRounded := int64(frames) * 588
	if samples == frameRounded {
		t.Fatal("expected ProbeSampleCount to differ from ProbeDurationFrames*588 for non-frame-aligned file")
	}
	if samples != 42000 {
		t.Errorf("ProbeSampleCount = %d, want 42000", samples)
	}
	if frameRounded != 41748 {
		t.Errorf("ProbeDurationFrames*588 = %d, want 41748", frameRounded)
	}
}

func TestProbeBitDepth(t *testing.T) {
	skipIfNoFFprobe(t)
	dataDir := testDataDir(t)

	tests := []struct {
		name      string
		file      string
		wantDepth int
	}{
		{"CD 16-bit", "test.flac", 16},
		{"Hi-res 24-bit", "hires1.flac", 24},
		{"Padded 24-bit", "padded24.flac", 24},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dataDir, tt.file)
			depth, err := ProbeBitDepth(context.Background(), path)
			if err != nil {
				t.Fatalf("ProbeBitDepth(%s) error: %v", tt.file, err)
			}
			if depth != tt.wantDepth {
				t.Errorf("ProbeBitDepth(%s) = %d, want %d", tt.file, depth, tt.wantDepth)
			}
		})
	}
}

func TestProbeEffectiveBitDepth(t *testing.T) {
	skipIfNoFFmpeg(t)
	dataDir := testDataDir(t)

	tests := []struct {
		name      string
		file      string
		wantDepth int
	}{
		{"CD 16-bit", "test.flac", 16},
		{"Hi-res 24-bit", "hires1.flac", 24},
		{"Padded 24-bit is effectively 16", "padded24.flac", 16},
		{"True 24-bit at 44100", "true24at44.flac", 24},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dataDir, tt.file)
			depth, err := ProbeEffectiveBitDepth(context.Background(), path)
			if err != nil {
				t.Fatalf("ProbeEffectiveBitDepth(%s) error: %v", tt.file, err)
			}
			if depth != tt.wantDepth {
				t.Errorf("ProbeEffectiveBitDepth(%s) = %d, want %d", tt.file, depth, tt.wantDepth)
			}
		})
	}
}

func TestValidateCDFormat(t *testing.T) {
	skipIfNoFFprobe(t)
	skipIfNoFFmpeg(t)
	dataDir := testDataDir(t)

	t.Run("valid CD format", func(t *testing.T) {
		path := filepath.Join(dataDir, "test.flac")
		if err := ValidateCDFormat(context.Background(), path); err != nil {
			t.Errorf("ValidateCDFormat(test.flac) unexpected error: %v", err)
		}
	})

	t.Run("hi-res rejected", func(t *testing.T) {
		path := filepath.Join(dataDir, "hires1.flac")
		err := ValidateCDFormat(context.Background(), path)
		if err == nil {
			t.Fatal("ValidateCDFormat(hires1.flac) expected error, got nil")
		}
		msg := err.Error()
		if !strings.Contains(msg, "44100") {
			t.Errorf("error should mention expected rate 44100, got: %s", msg)
		}
		if !strings.Contains(msg, "96000") {
			t.Errorf("error should mention actual rate 96000, got: %s", msg)
		}
	})

	t.Run("padded 24-bit accepted", func(t *testing.T) {
		path := filepath.Join(dataDir, "padded24.flac")
		if err := ValidateCDFormat(context.Background(), path); err != nil {
			t.Errorf("ValidateCDFormat(padded24.flac) unexpected error: %v", err)
		}
	})

	t.Run("true 24-bit at 44100 rejected", func(t *testing.T) {
		path := filepath.Join(dataDir, "true24at44.flac")
		err := ValidateCDFormat(context.Background(), path)
		if err == nil {
			t.Fatal("ValidateCDFormat(true24at44.flac) expected error, got nil")
		}
		msg := err.Error()
		if !strings.Contains(msg, "16-bit") {
			t.Errorf("error should mention expected 16-bit, got: %s", msg)
		}
		if !strings.Contains(msg, "24-bit") {
			t.Errorf("error should mention actual 24-bit, got: %s", msg)
		}
	})
}

// materialsDir returns the absolute path to the materials/repair test fixtures directory.
func materialsDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("unable to determine test file path")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "materials", "repair")
}

func TestValidateCDFormat_WMA(t *testing.T) {
	skipIfNoFFprobe(t)
	skipIfNoFFmpeg(t)
	dir := materialsDir(t)
	wmaDir := filepath.Join(dir, "NEDA-10011")
	if _, err := os.Stat(wmaDir); os.IsNotExist(err) {
		t.Skip("materials/repair/NEDA-10011 not available, skipping WMA test")
	}
	path := filepath.Join(wmaDir, "01 SAVE ME.wma")
	if err := ValidateCDFormat(context.Background(), path); err != nil {
		t.Errorf("ValidateCDFormat(WMA Lossless 16-bit) unexpected error: %v", err)
	}
}

func TestValidateCDFormat_NonexistentFile(t *testing.T) {
	skipIfNoFFprobe(t)
	err := ValidateCDFormat(context.Background(), "/nonexistent/audio.flac")
	if err == nil {
		t.Fatal("ValidateCDFormat(nonexistent) expected error, got nil")
	}
}
