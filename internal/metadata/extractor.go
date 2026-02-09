package metadata

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"ctdbtools/internal/audio"
)

// Extract extracts metadata from an audio file using the best available method.
// For FLAC files: uses metaflac (preserves exact tag values)
// For other formats: uses ffprobe
func Extract(ctx context.Context, path string) (*audio.Metadata, error) {
	ext := strings.ToLower(filepath.Ext(path))

	// Use metaflac for FLAC files if available
	if ext == ".flac" && hasMetaflac() {
		return ExtractWithMetaflac(ctx, path)
	}

	// Fall back to ffprobe for all other formats
	return ExtractWithFFprobe(ctx, path)
}

// ExtractWithMetaflac extracts metadata from a FLAC file using metaflac.
// This preserves exact tag values including special characters.
func ExtractWithMetaflac(ctx context.Context, path string) (*audio.Metadata, error) {
	cmd := exec.CommandContext(ctx, "metaflac", "--export-tags-to=-", path)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("metaflac failed: %w", err)
	}

	meta := audio.NewMetadata()

	// Parse output: KEY=VALUE format, one per line
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		idx := strings.Index(line, "=")
		if idx < 0 {
			continue
		}
		key := line[:idx]
		value := line[idx+1:]

		// Normalize and set
		normalizedKey := NormalizeTagKey(key)
		meta.Set(normalizedKey, value)
	}

	return meta, nil
}

// ExtractWithFFprobe extracts metadata using ffprobe (JSON output).
// Works with WAV, M4A, MP3, and most other formats.
func ExtractWithFFprobe(ctx context.Context, path string) (*audio.Metadata, error) {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	// Parse JSON output
	var result ffprobeResult
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("failed to parse ffprobe output: %w", err)
	}

	meta := audio.NewMetadata()

	// Extract tags from format section
	for key, value := range result.Format.Tags {
		normalizedKey := NormalizeTagKey(key)
		meta.Set(normalizedKey, value)
	}

	// Also check stream tags (some formats store tags per-stream)
	for _, stream := range result.Streams {
		if stream.CodecType != "audio" {
			continue
		}
		for key, value := range stream.Tags {
			normalizedKey := NormalizeTagKey(key)
			// Don't overwrite format-level tags
			if meta.Get(normalizedKey) == "" {
				meta.Set(normalizedKey, value)
			}
		}
	}

	return meta, nil
}

// ExtractFromFirstFile extracts metadata from the first file in a list.
// Returns nil metadata (not error) if extraction fails.
func ExtractFromFirstFile(ctx context.Context, paths []string) *audio.Metadata {
	if len(paths) == 0 {
		return nil
	}

	// Try first file
	meta, err := Extract(ctx, paths[0])
	if err != nil {
		// Log warning but don't fail
		fmt.Fprintf(os.Stderr, "Warning: failed to extract metadata from %s: %v\n", paths[0], err)
		return nil
	}

	return meta
}

// ExtractFromFiles extracts metadata from each file in the list.
// Returns a slice of metadata (one per file), with nil for failed extractions.
func ExtractFromFiles(ctx context.Context, paths []string) []*audio.Metadata {
	result := make([]*audio.Metadata, len(paths))
	for i, path := range paths {
		meta, err := Extract(ctx, path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to extract metadata from %s: %v\n", path, err)
			continue
		}
		result[i] = meta
	}
	return result
}

// ffprobeResult represents the JSON output from ffprobe.
type ffprobeResult struct {
	Format  ffprobeFormat   `json:"format"`
	Streams []ffprobeStream `json:"streams"`
}

type ffprobeFormat struct {
	Filename   string            `json:"filename"`
	FormatName string            `json:"format_name"`
	Duration   string            `json:"duration"`
	Tags       map[string]string `json:"tags"`
}

type ffprobeStream struct {
	Index     int               `json:"index"`
	CodecType string            `json:"codec_type"`
	CodecName string            `json:"codec_name"`
	Tags      map[string]string `json:"tags"`
}

// hasMetaflac checks if metaflac is available.
func hasMetaflac() bool {
	_, err := exec.LookPath("metaflac")
	return err == nil
}

// hasFFprobe checks if ffprobe is available.
func hasFFprobe() bool {
	_, err := exec.LookPath("ffprobe")
	return err == nil
}
