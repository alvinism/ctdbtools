// Package testutil provides test utilities similar to CueTools.TestHelpers.
package testutil

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// ExpectedCRCs contains expected CRC values parsed from a JSON test data file.
type ExpectedCRCs struct {
	TOCID     string
	NumTracks int
	// Per-track data (1-indexed, index 0 is for disc-level)
	ARV1     map[int]uint32 // AccurateRip v1 CRC
	ARV2     map[int]uint32 // AccurateRip v2 CRC
	CRC32    map[int]uint32 // CRC32
	CRCWONULL map[int]uint32 // CRC without nulls
	Peak     map[int]float64 // Peak amplitude percentage
}

// jsonData represents the JSON structure of test data files
type jsonData struct {
	TOCID   string `json:"TOCID"`
	Summary string `json:"summary"`
}

// ParseExpectedCRCs parses a JSON test data file and extracts expected CRC values.
// Note: The JSON files may contain literal newlines in string fields, which is
// technically invalid JSON. We handle this by parsing manually when needed.
func ParseExpectedCRCs(path string) (*ExpectedCRCs, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	// Try standard JSON parsing first
	var jd jsonData
	if err := json.Unmarshal(data, &jd); err != nil {
		// JSON parsing failed, likely due to literal newlines in strings
		// Parse manually
		jd, err = parseJSONWithLiteralNewlines(string(data))
		if err != nil {
			return nil, fmt.Errorf("parse JSON: %w", err)
		}
	}

	expected := &ExpectedCRCs{
		TOCID:     jd.TOCID,
		ARV1:      make(map[int]uint32),
		ARV2:      make(map[int]uint32),
		CRC32:     make(map[int]uint32),
		CRCWONULL: make(map[int]uint32),
		Peak:      make(map[int]float64),
	}

	// Parse AccurateRip CRCs
	// Format: " 01     [d7ae831e|5403f11c] (0+1/1) Accurately ripped"
	arPattern := regexp.MustCompile(`^\s*(\d+)\s+\[([0-9a-fA-F]+)\|([0-9a-fA-F]+)\]`)

	// Parse CRC32 and CRCWONULL
	// Format: " 01   99.6 [9A0F4349] [D1383D7F]   CRC32"
	crcPattern := regexp.MustCompile(`^\s*(\d+)\s+(\d+\.?\d*)\s+\[([0-9a-fA-F]+)\]\s+\[([0-9a-fA-F]+)\]`)

	// Also match disc-level CRC (track "--")
	discCRCPattern := regexp.MustCompile(`^\s*--\s+(\d+\.?\d*)\s+\[([0-9a-fA-F]+)\]\s+\[([0-9a-fA-F]+)\]`)

	lines := strings.Split(jd.Summary, "\n")
	for _, line := range lines {
		// Try AccurateRip pattern
		if matches := arPattern.FindStringSubmatch(line); len(matches) == 4 {
			track, _ := strconv.Atoi(matches[1])
			arv1, _ := strconv.ParseUint(matches[2], 16, 32)
			arv2, _ := strconv.ParseUint(matches[3], 16, 32)
			expected.ARV1[track] = uint32(arv1)
			expected.ARV2[track] = uint32(arv2)
			if track > expected.NumTracks {
				expected.NumTracks = track
			}
		}

		// Try CRC32 pattern
		if matches := crcPattern.FindStringSubmatch(line); len(matches) == 5 {
			track, _ := strconv.Atoi(matches[1])
			peak, _ := strconv.ParseFloat(matches[2], 64)
			crc32, _ := strconv.ParseUint(matches[3], 16, 32)
			crcwn, _ := strconv.ParseUint(matches[4], 16, 32)
			expected.CRC32[track] = uint32(crc32)
			expected.CRCWONULL[track] = uint32(crcwn)
			expected.Peak[track] = peak
		}

		// Try disc-level CRC pattern
		if matches := discCRCPattern.FindStringSubmatch(line); len(matches) == 4 {
			peak, _ := strconv.ParseFloat(matches[1], 64)
			crc32, _ := strconv.ParseUint(matches[2], 16, 32)
			crcwn, _ := strconv.ParseUint(matches[3], 16, 32)
			expected.CRC32[0] = uint32(crc32)
			expected.CRCWONULL[0] = uint32(crcwn)
			expected.Peak[0] = peak
		}
	}

	return expected, nil
}

// MaterialsPath returns the path to test materials directory.
// It checks the CTDB_TEST_MATERIALS environment variable first,
// then looks for the materials directory relative to common paths.
func MaterialsPath() string {
	// Check environment variable first
	if path := os.Getenv("CTDB_TEST_MATERIALS"); path != "" {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return path
		}
	}

	// Check relative to working directory
	candidates := []string{
		"materials",
		"../materials",
		"../../materials",
		"../../../materials",
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}

	return ""
}

// parseJSONWithLiteralNewlines handles JSON files that contain literal newlines
// in string fields (which is technically invalid JSON but happens in our test files).
func parseJSONWithLiteralNewlines(data string) (jsonData, error) {
	var jd jsonData

	// Extract TOCID using regex
	tocidPattern := regexp.MustCompile(`"TOCID"\s*:\s*"([^"]*)"`)
	if matches := tocidPattern.FindStringSubmatch(data); len(matches) == 2 {
		jd.TOCID = matches[1]
	}

	// Extract summary field - it spans multiple lines
	// Find the start of summary field
	summaryStart := strings.Index(data, `"summary":`)
	if summaryStart == -1 {
		return jd, fmt.Errorf("summary field not found")
	}

	// Find the opening quote after "summary":
	quoteStart := strings.Index(data[summaryStart:], `"`)
	if quoteStart == -1 {
		return jd, fmt.Errorf("summary field malformed")
	}
	summaryStart += quoteStart + 1

	// Find the next quote after "summary": "
	quoteStart = strings.Index(data[summaryStart:], `"`)
	if quoteStart == -1 {
		return jd, fmt.Errorf("summary field malformed")
	}
	summaryStart += quoteStart + 1

	// Now find the closing quote - it's followed by optional whitespace and either } or ,
	// We need to find the end of the summary string
	summaryEnd := -1
	for i := summaryStart; i < len(data); i++ {
		if data[i] == '"' {
			// Check if this is the closing quote (followed by whitespace and } or ,)
			rest := strings.TrimSpace(data[i+1:])
			if len(rest) > 0 && (rest[0] == '}' || rest[0] == ',') {
				summaryEnd = i
				break
			}
		}
	}

	if summaryEnd == -1 {
		return jd, fmt.Errorf("summary field end not found")
	}

	jd.Summary = data[summaryStart:summaryEnd]
	return jd, nil
}

// CheckFFmpeg checks if ffmpeg is available.
func CheckFFmpeg() error {
	// This is a simple check - just try to find ffmpeg in PATH
	paths := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))
	for _, p := range paths {
		ffmpegPath := p + string(os.PathSeparator) + "ffmpeg"
		if _, err := os.Stat(ffmpegPath); err == nil {
			return nil
		}
		// Also check with .exe on Windows
		if _, err := os.Stat(ffmpegPath + ".exe"); err == nil {
			return nil
		}
	}
	return fmt.Errorf("ffmpeg not found in PATH")
}
