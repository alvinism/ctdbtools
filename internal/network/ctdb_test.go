package network

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseCTDBResponse(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="utf-8"?>
<ctdb xmlns="http://db.cuetools.net/ns/mmd-1.0#" status="ok" npar="8">
  <entry id="12345" crc32="DEADBEEF" confidence="5" npar="8" stride="588" toc="0:15000:30000:45000" trackcrcs="AABBCCDD EEFF0011"/>
  <entry id="12346" crc32="CAFEBABE" confidence="3" npar="8" stride="588" toc="0:15000:30000:45000"/>
</ctdb>`

	resp, err := parseCTDBResponse([]byte(xmlData))
	if err != nil {
		t.Fatalf("parseCTDBResponse failed: %v", err)
	}

	if len(resp.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(resp.Entries))
	}

	e1 := resp.Entries[0]
	if e1.ID != 12345 {
		t.Errorf("ID = %d, want 12345", e1.ID)
	}
	if e1.CRC32 != 0xDEADBEEF {
		t.Errorf("CRC32 = %x, want DEADBEEF", e1.CRC32)
	}
	if e1.Confidence != 5 {
		t.Errorf("Confidence = %d, want 5", e1.Confidence)
	}
	if e1.Npar != 8 {
		t.Errorf("Npar = %d, want 8", e1.Npar)
	}
	if e1.Stride != 1176 { // 588 * 2
		t.Errorf("Stride = %d, want 1176", e1.Stride)
	}
	if e1.TOC != "0:15000:30000:45000" {
		t.Errorf("TOC = %s, want 0:15000:30000:45000", e1.TOC)
	}
	if len(e1.TrackCRCs) != 2 {
		t.Errorf("TrackCRCs length = %d, want 2", len(e1.TrackCRCs))
	} else {
		if e1.TrackCRCs[0] != 0xAABBCCDD {
			t.Errorf("TrackCRCs[0] = %x, want AABBCCDD", e1.TrackCRCs[0])
		}
		if e1.TrackCRCs[1] != 0xEEFF0011 {
			t.Errorf("TrackCRCs[1] = %x, want EEFF0011", e1.TrackCRCs[1])
		}
	}

	if resp.Total != 8 { // 5 + 3
		t.Errorf("Total = %d, want 8", resp.Total)
	}
}

func TestParseCTDBResponseWithMetadata(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="utf-8"?>
<ctdb xmlns="http://db.cuetools.net/ns/mmd-1.0#" status="ok">
  <entry id="123" crc32="12345678" confidence="10" npar="8" stride="588" toc="0:1000:2000"/>
  <metadata source="musicbrainz" id="abc-123" artist="Test Artist" album="Test Album" year="2020" genre="Rock">
    <track name="Track 1" artist="Test Artist"/>
    <track name="Track 2"/>
    <coverart uri="http://example.com/cover.jpg" uri150="http://example.com/cover_150.jpg"/>
  </metadata>
</ctdb>`

	resp, err := parseCTDBResponse([]byte(xmlData))
	if err != nil {
		t.Fatalf("parseCTDBResponse failed: %v", err)
	}

	if len(resp.Metadata) != 1 {
		t.Fatalf("expected 1 metadata, got %d", len(resp.Metadata))
	}

	meta := resp.Metadata[0]
	if meta.Source != "musicbrainz" {
		t.Errorf("Source = %s, want musicbrainz", meta.Source)
	}
	if meta.Artist != "Test Artist" {
		t.Errorf("Artist = %s, want Test Artist", meta.Artist)
	}
	if meta.Album != "Test Album" {
		t.Errorf("Album = %s, want Test Album", meta.Album)
	}
	if meta.Year != "2020" {
		t.Errorf("Year = %s, want 2020", meta.Year)
	}

	if len(meta.Tracks) != 2 {
		t.Errorf("Tracks length = %d, want 2", len(meta.Tracks))
	}

	if len(meta.CoverArt) != 1 {
		t.Errorf("CoverArt length = %d, want 1", len(meta.CoverArt))
	} else if meta.CoverArt[0].URI != "http://example.com/cover.jpg" {
		t.Errorf("CoverArt URI = %s", meta.CoverArt[0].URI)
	}
}

func TestParseCTDBResponseNoEntries(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="utf-8"?>
<ctdb xmlns="http://db.cuetools.net/ns/mmd-1.0#" status="not found">
</ctdb>`

	_, err := parseCTDBResponse([]byte(xmlData))
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestParseCTDBResponseSkipsEntriesWithoutTOC(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="utf-8"?>
<ctdb xmlns="http://db.cuetools.net/ns/mmd-1.0#">
  <entry id="1" crc32="11111111" confidence="1" npar="8" stride="588"/>
  <entry id="2" crc32="22222222" confidence="2" npar="8" stride="588" toc="0:1000"/>
</ctdb>`

	resp, err := parseCTDBResponse([]byte(xmlData))
	if err != nil {
		t.Fatalf("parseCTDBResponse failed: %v", err)
	}

	// Should only have entry with TOC
	if len(resp.Entries) != 1 {
		t.Fatalf("expected 1 entry (with TOC), got %d", len(resp.Entries))
	}
	if resp.Entries[0].ID != 2 {
		t.Errorf("Entry ID = %d, want 2", resp.Entries[0].ID)
	}
}

func TestCTDBClientLookup(t *testing.T) {
	testResponse := `<?xml version="1.0" encoding="utf-8"?>
<ctdb xmlns="http://db.cuetools.net/ns/mmd-1.0#" status="ok">
  <entry id="999" crc32="ABCD1234" confidence="15" npar="8" stride="588" toc="0:1000:2000:3000"/>
</ctdb>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify query parameters
		q := r.URL.Query()
		if q.Get("version") != "3" {
			t.Errorf("version = %s, want 3", q.Get("version"))
		}
		if q.Get("ctdb") != "1" {
			t.Errorf("ctdb = %s, want 1", q.Get("ctdb"))
		}
		if q.Get("fuzzy") != "0" {
			t.Errorf("fuzzy = %s, want 0", q.Get("fuzzy"))
		}
		if q.Get("metadata") != "none" {
			t.Errorf("metadata = %s, want none", q.Get("metadata"))
		}
		if q.Get("toc") != "0:1000:2000:3000" {
			t.Errorf("toc = %s, want 0:1000:2000:3000", q.Get("toc"))
		}

		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(testResponse))
	}))
	defer server.Close()

	httpClient := NewHTTPClient()
	client := NewCTDBClient(httpClient, WithCTDBServer(server.Listener.Addr().String()))

	resp, err := client.Lookup(context.Background(), CTDBLookupOptions{
		TOC:            "0:1000:2000:3000",
		CTDB:           true,
		Fuzzy:          false,
		MetadataSearch: CTDBMetadataSearchNone,
	})
	if err != nil {
		t.Fatalf("Lookup failed: %v", err)
	}

	if len(resp.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(resp.Entries))
	}
	if resp.Entries[0].CRC32 != 0xABCD1234 {
		t.Errorf("CRC32 = %x, want ABCD1234", resp.Entries[0].CRC32)
	}
}

func TestCTDBClientNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	httpClient := NewHTTPClient()
	client := NewCTDBClient(httpClient, WithCTDBServer(server.Listener.Addr().String()))

	_, err := client.Lookup(context.Background(), CTDBLookupOptions{
		TOC:  "0:1000:2000",
		CTDB: true,
	})
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestBoolToStr(t *testing.T) {
	if boolToStr(true) != "1" {
		t.Error("boolToStr(true) should be 1")
	}
	if boolToStr(false) != "0" {
		t.Error("boolToStr(false) should be 0")
	}
}
