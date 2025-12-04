package network

import (
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"ctdbtool/internal/parity"
)

const (
	ctdbDefaultServer = "db.cuetools.net"
	ctdbLookupPath    = "/lookup2.php"
)

// ctdbClient implements CTDBClient.
type ctdbClient struct {
	http      *HTTPClient
	baseURL   string
	userAgent string
}

// CTDBClientOption configures a CTDB client.
type CTDBClientOption func(*ctdbClient)

// WithCTDBServer sets the CTDB server hostname.
func WithCTDBServer(server string) CTDBClientOption {
	return func(c *ctdbClient) {
		c.baseURL = "http://" + server
	}
}

// WithCTDBUserAgent sets a custom user agent for CTDB requests.
func WithCTDBUserAgent(ua string) CTDBClientOption {
	return func(c *ctdbClient) {
		c.userAgent = ua
	}
}

// NewCTDBClient creates a new CTDB client.
func NewCTDBClient(httpClient *HTTPClient, opts ...CTDBClientOption) CTDBClient {
	if httpClient == nil {
		httpClient = NewHTTPClient()
	}
	c := &ctdbClient{
		http:      httpClient,
		baseURL:   "http://" + ctdbDefaultServer,
		userAgent: "ctdbtool/1.0",
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Lookup queries CTDB for disc entries matching the TOC.
func (c *ctdbClient) Lookup(ctx context.Context, opts CTDBLookupOptions) (*CTDBResponse, error) {
	// Build URL per CUEToolsDB.cs:77-83
	// Format: http://db.cuetools.net/lookup2.php?version=3&ctdb={0|1}&fuzzy={0|1}&metadata={level}&toc={toc}
	params := url.Values{}
	params.Set("version", "3")
	params.Set("ctdb", boolToStr(opts.CTDB))
	params.Set("fuzzy", boolToStr(opts.Fuzzy))
	if opts.MetadataSearch == "" {
		opts.MetadataSearch = CTDBMetadataSearchNone
	}
	params.Set("metadata", string(opts.MetadataSearch))
	params.Set("toc", opts.TOC)

	fullURL := c.baseURL + ctdbLookupPath + "?" + params.Encode()

	// Create request with context
	req, err := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept-Encoding", "gzip, deflate")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ctdb: HTTP %d", resp.StatusCode)
	}

	// Handle gzip-compressed responses
	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("ctdb: gzip error: %w", err)
		}
		defer gzReader.Close()
		reader = gzReader
	}

	// Read and parse XML response
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}

	return parseCTDBResponse(data)
}

// FetchParity downloads parity data for a specific entry.
func (c *ctdbClient) FetchParity(ctx context.Context, entry *CTDBEntry, npar int) ([][]uint16, error) {
	if entry.HasParity == "" {
		return nil, fmt.Errorf("no parity available for entry")
	}

	parityURL := entry.HasParity
	if strings.HasPrefix(parityURL, "/") {
		parityURL = c.baseURL + parityURL
	}

	// Calculate byte range for incremental fetch
	prevLen := 0
	if entry.Syndrome != nil && len(entry.Syndrome) > 0 {
		prevLen = len(entry.Syndrome[0]) * entry.Stride * 2
	}
	rangeEnd := npar * entry.Stride * 2 - 1

	req, err := http.NewRequestWithContext(ctx, "GET", parityURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	if prevLen > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", prevLen, rangeEnd))
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("ctdb parity fetch: HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Convert parity bytes to syndrome
	return parity.Bytes2Syndrome(entry.Stride, npar, data), nil
}

// XML structures for parsing CTDB response
type ctdbXMLResponse struct {
	XMLName  xml.Name            `xml:"ctdb"`
	Status   string              `xml:"status,attr"`
	Npar     int                 `xml:"npar,attr"`
	Entries  []ctdbXMLEntry      `xml:"entry"`
	Metadata []ctdbXMLMetadata   `xml:"metadata"`
}

type ctdbXMLEntry struct {
	ID         int64  `xml:"id,attr"`
	CRC32      string `xml:"crc32,attr"`
	Confidence int    `xml:"confidence,attr"`
	Npar       int    `xml:"npar,attr"`
	Stride     int    `xml:"stride,attr"`
	HasParity  string `xml:"hasparity,attr"`
	Parity     string `xml:"parity,attr"`
	Syndrome   string `xml:"syndrome,attr"`
	TrackCRCs  string `xml:"trackcrcs,attr"`
	TOC        string `xml:"toc,attr"`
}

type ctdbXMLMetadata struct {
	Source     string               `xml:"source,attr"`
	ID         string               `xml:"id,attr"`
	Artist     string               `xml:"artist,attr"`
	Album      string               `xml:"album,attr"`
	Year       string               `xml:"year,attr"`
	Genre      string               `xml:"genre,attr"`
	DiscNumber string               `xml:"discnumber,attr"`
	DiscCount  string               `xml:"disccount,attr"`
	DiscName   string               `xml:"discname,attr"`
	InfoURL    string               `xml:"infourl,attr"`
	Barcode    string               `xml:"barcode,attr"`
	Tracks     []ctdbXMLTrack       `xml:"track"`
	CoverArt   []ctdbXMLCoverArt    `xml:"coverart"`
}

type ctdbXMLTrack struct {
	Name   string `xml:"name,attr"`
	Artist string `xml:"artist,attr"`
}

type ctdbXMLCoverArt struct {
	URI    string `xml:"uri,attr"`
	URI150 string `xml:"uri150,attr"`
}

// parseCTDBResponse parses the XML response from CTDB.
func parseCTDBResponse(data []byte) (*CTDBResponse, error) {
	var xmlResp ctdbXMLResponse
	if err := xml.Unmarshal(data, &xmlResp); err != nil {
		return nil, fmt.Errorf("ctdb: XML parse error: %w", err)
	}

	resp := &CTDBResponse{}

	// Convert entries
	for _, xe := range xmlResp.Entries {
		if xe.TOC == "" {
			continue
		}

		entry := CTDBEntry{
			ID:         xe.ID,
			Confidence: xe.Confidence,
			Npar:       xe.Npar,
			Stride:     xe.Stride * 2, // Note: CTDB stores stride/2
			HasParity:  xe.HasParity,
			TOC:        xe.TOC,
		}

		// Parse CRC32 from hex string
		if xe.CRC32 != "" {
			crc, err := strconv.ParseUint(xe.CRC32, 16, 32)
			if err == nil {
				entry.CRC32 = uint32(crc)
			}
		}

		// Parse track CRCs (space-separated hex values)
		if xe.TrackCRCs != "" {
			parts := strings.Fields(xe.TrackCRCs)
			entry.TrackCRCs = make([]uint32, len(parts))
			for i, p := range parts {
				crc, _ := strconv.ParseUint(p, 16, 32)
				entry.TrackCRCs[i] = uint32(crc)
			}
		}

		// Parse syndrome from base64
		if xe.Syndrome != "" {
			synData, err := base64.StdEncoding.DecodeString(xe.Syndrome)
			if err == nil && len(synData) > 0 {
				entry.Syndrome = parity.Bytes2Syndrome(entry.Stride, entry.Npar, synData)
			}
		} else if xe.Parity != "" {
			// Fall back to parity->syndrome conversion
			parData, err := base64.StdEncoding.DecodeString(xe.Parity)
			if err == nil && len(parData) > 0 {
				entry.Parity = parData
				entry.Syndrome = parity.Parity2Syndrome(entry.Stride, entry.Stride, entry.Npar, entry.Npar, parData, 0, 0)
			}
		}

		resp.Entries = append(resp.Entries, entry)
		resp.Total += entry.Confidence
	}

	// Convert metadata
	for _, xm := range xmlResp.Metadata {
		meta := CTDBMetadata{
			Source:     xm.Source,
			ID:         xm.ID,
			Artist:     xm.Artist,
			Album:      xm.Album,
			Year:       xm.Year,
			Genre:      xm.Genre,
			DiscNumber: xm.DiscNumber,
			DiscCount:  xm.DiscCount,
			DiscName:   xm.DiscName,
			InfoURL:    xm.InfoURL,
			Barcode:    xm.Barcode,
		}

		for _, xt := range xm.Tracks {
			meta.Tracks = append(meta.Tracks, CTDBTrackMeta{
				Name:   xt.Name,
				Artist: xt.Artist,
			})
		}

		for _, xc := range xm.CoverArt {
			meta.CoverArt = append(meta.CoverArt, CTDBCoverArt{
				URI:    xc.URI,
				URI150: xc.URI150,
			})
		}

		resp.Metadata = append(resp.Metadata, meta)
	}

	if len(resp.Entries) == 0 {
		return nil, ErrNotFound
	}

	return resp, nil
}

func boolToStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
