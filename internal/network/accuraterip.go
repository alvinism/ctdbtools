package network

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	accurateRipBaseURL = "http://www.accuraterip.com/accuraterip"
	arMinInterval      = 500 * time.Millisecond
)

// arClient implements AccurateRipClient.
type arClient struct {
	http    *HTTPClient
	baseURL string

	// Rate limiting per AccurateRip guidelines
	mu         sync.Mutex
	lastAccess time.Time
}

// ARClientOption configures an AccurateRip client.
type ARClientOption func(*arClient)

// WithARBaseURL overrides the AccurateRip server URL (for testing).
func WithARBaseURL(url string) ARClientOption {
	return func(c *arClient) {
		c.baseURL = url
	}
}

// NewAccurateRipClient creates a new AccurateRip client.
func NewAccurateRipClient(httpClient *HTTPClient, opts ...ARClientOption) AccurateRipClient {
	if httpClient == nil {
		httpClient = NewHTTPClient()
	}
	c := &arClient{
		http:    httpClient,
		baseURL: accurateRipBaseURL,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Query fetches AccurateRip data for a disc.
// arID format: "discid1-discid2-cddbid" (lowercase hex, 8 chars each)
func (c *arClient) Query(ctx context.Context, arID string, trackCount int) (*ARResponse, error) {
	// Parse AR ID components
	discID1, discID2, cddbID, err := parseARID(arID)
	if err != nil {
		return nil, fmt.Errorf("invalid AR ID: %w", err)
	}

	// Build URL per CUETools AccurateRip.cs:829
	// Format: http://www.accuraterip.com/accuraterip/{d1&0xF}/{d1>>4&0xF}/{d1>>8&0xF}/dBAR-{tracks:03d}-{d1:08x}-{d2:08x}-{cddb:08x}.bin
	url := fmt.Sprintf("%s/%x/%x/%x/dBAR-%03d-%08x-%08x-%08x.bin",
		c.baseURL,
		discID1&0xF,
		(discID1>>4)&0xF,
		(discID1>>8)&0xF,
		trackCount,
		discID1,
		discID2,
		cddbID,
	)

	// Enforce rate limiting
	c.waitForRateLimit()

	// Create request with context
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("accuraterip: HTTP %d", resp.StatusCode)
	}

	// Read and parse response
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return parseARResponse(data)
}

// waitForRateLimit enforces minimum interval between requests.
// Per AccurateRip guidelines: 0.5s minimum between requests.
func (c *arClient) waitForRateLimit() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.lastAccess.IsZero() {
		elapsed := time.Since(c.lastAccess)
		if elapsed < arMinInterval {
			time.Sleep(arMinInterval - elapsed)
		}
	}
	c.lastAccess = time.Now()
}

// parseARID parses "discid1-discid2-cddbid" into three uint32 values.
func parseARID(arID string) (discID1, discID2, cddbID uint32, err error) {
	parts := strings.Split(arID, "-")
	if len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("expected 3 parts, got %d", len(parts))
	}

	d1, err := strconv.ParseUint(parts[0], 16, 32)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid discID1: %w", err)
	}
	d2, err := strconv.ParseUint(parts[1], 16, 32)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid discID2: %w", err)
	}
	cd, err := strconv.ParseUint(parts[2], 16, 32)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid cddbID: %w", err)
	}

	return uint32(d1), uint32(d2), uint32(cd), nil
}

// parseARResponse parses the binary AccurateRip response.
// Format from CUETools AccurateRip.cs:864-903:
//   - 13-byte disk header: 1 byte track count, 4 bytes discId1, 4 bytes discId2, 4 bytes cddbDiscId (LE)
//   - 9-byte track entries per track: 1 byte count, 4 bytes CRC, 4 bytes Frame450CRC (LE)
//   - Multiple disks concatenated
func parseARResponse(data []byte) (*ARResponse, error) {
	resp := &ARResponse{}
	pos := 0

	for pos < len(data) {
		// Need at least 13 bytes for header
		if pos+13 > len(data) {
			return nil, ErrMalformedResponse
		}

		disk := ARDisk{
			TrackCount: data[pos],
			DiscID1:    binary.LittleEndian.Uint32(data[pos+1:]),
			DiscID2:    binary.LittleEndian.Uint32(data[pos+5:]),
			CDDBDiscID: binary.LittleEndian.Uint32(data[pos+9:]),
		}
		pos += 13

		// Read track entries
		disk.Tracks = make([]ARTrack, 0, disk.TrackCount)
		for i := uint8(0); i < disk.TrackCount; i++ {
			// Need 9 bytes per track
			if pos+9 > len(data) {
				return nil, ErrMalformedResponse
			}
			track := ARTrack{
				Count:       data[pos],
				CRC:         binary.LittleEndian.Uint32(data[pos+1:]),
				Frame450CRC: binary.LittleEndian.Uint32(data[pos+5:]),
			}
			disk.Tracks = append(disk.Tracks, track)
			pos += 9
		}

		resp.Disks = append(resp.Disks, disk)
	}

	return resp, nil
}
