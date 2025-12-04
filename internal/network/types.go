// Package network provides HTTP clients for AccurateRip and CTDB database queries.
package network

import (
	"context"
	"errors"
)

// Common errors.
var (
	ErrNotFound          = errors.New("disc not found in database")
	ErrMalformedResponse = errors.New("malformed response from server")
	ErrNetworkTimeout    = errors.New("network request timed out")
	ErrRateLimited       = errors.New("rate limited by server")
)

// ARTrack represents AccurateRip CRC data for a single track.
type ARTrack struct {
	Count       uint8  // Number of submissions with this CRC
	CRC         uint32 // AccurateRip v1 CRC
	Frame450CRC uint32 // CRC at frame 450 (for offset detection)
}

// ARDisk represents AccurateRip data for one "pressing" of a disc.
type ARDisk struct {
	TrackCount uint8    // Number of tracks in this entry
	DiscID1    uint32   // First disc ID component
	DiscID2    uint32   // Second disc ID component
	CDDBDiscID uint32   // CDDB disc ID
	Tracks     []ARTrack
}

// ARResponse contains all AccurateRip database results for a disc query.
type ARResponse struct {
	Disks []ARDisk
}

// CTDBEntry represents a single entry from CTDB lookup.
type CTDBEntry struct {
	ID         int64     // Entry ID
	CRC32      uint32    // Hex-parsed CRC32
	Confidence int       // Confidence score
	Npar       int       // Number of parity symbols
	Stride     int       // Parity stride (note: stored as stride/2 in response)
	HasParity  string    // URL path or full URL to parity file
	Parity     []byte    // Base64-decoded parity data
	Syndrome   [][]uint16 // Parsed syndrome matrix
	TrackCRCs  []uint32  // Per-track CRCs
	TOC        string    // TOC string for matching
}

// CTDBMetadata contains album/track metadata from CTDB.
type CTDBMetadata struct {
	Source     string
	ID         string
	Artist     string
	Album      string
	Year       string
	Genre      string
	DiscNumber string
	DiscCount  string
	DiscName   string
	InfoURL    string
	Barcode    string
	Tracks     []CTDBTrackMeta
	CoverArt   []CTDBCoverArt
}

// CTDBTrackMeta contains track-level metadata.
type CTDBTrackMeta struct {
	Name   string
	Artist string
}

// CTDBCoverArt contains cover art reference.
type CTDBCoverArt struct {
	URI   string
	URI150 string
}

// CTDBResponse contains the full CTDB lookup response.
type CTDBResponse struct {
	Entries  []CTDBEntry
	Metadata []CTDBMetadata
	Total    int // Sum of all confidence values
}

// CTDBMetadataSearch specifies metadata search level.
type CTDBMetadataSearch string

const (
	CTDBMetadataSearchNone      CTDBMetadataSearch = "none"
	CTDBMetadataSearchFast      CTDBMetadataSearch = "fast"
	CTDBMetadataSearchDefault   CTDBMetadataSearch = "default"
	CTDBMetadataSearchExtensive CTDBMetadataSearch = "extensive"
)

// CTDBLookupOptions configures a CTDB lookup request.
type CTDBLookupOptions struct {
	TOC            string             // TOC string (from Layout.TOCString())
	CTDB           bool               // Query CTDB entries
	Fuzzy          bool               // Allow fuzzy TOC matching
	MetadataSearch CTDBMetadataSearch // Metadata search level
}

// AccurateRipClient defines the interface for AccurateRip database queries.
type AccurateRipClient interface {
	// Query fetches AccurateRip data for a disc identified by its AR ID.
	// arID format: "discid1-discid2-cddbid" (lowercase hex)
	// trackCount is the number of audio tracks.
	Query(ctx context.Context, arID string, trackCount int) (*ARResponse, error)
}

// CTDBClient defines the interface for CUETools Database queries.
type CTDBClient interface {
	// Lookup queries CTDB for disc entries matching the TOC.
	Lookup(ctx context.Context, opts CTDBLookupOptions) (*CTDBResponse, error)

	// FetchParity downloads parity data for a specific entry.
	// npar specifies how many parity symbols to fetch.
	FetchParity(ctx context.Context, entry *CTDBEntry, npar int) ([][]uint16, error)
}
