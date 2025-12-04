package network

import (
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseARID(t *testing.T) {
	tests := []struct {
		name    string
		arID    string
		want1   uint32
		want2   uint32
		wantCD  uint32
		wantErr bool
	}{
		{
			name:   "valid lowercase",
			arID:   "00012345-0006789a-0a0b0c0d",
			want1:  0x00012345,
			want2:  0x0006789a,
			wantCD: 0x0a0b0c0d,
		},
		{
			name:   "valid uppercase",
			arID:   "00012345-0006789A-0A0B0C0D",
			want1:  0x00012345,
			want2:  0x0006789A,
			wantCD: 0x0A0B0C0D,
		},
		{
			name:    "invalid format - too few parts",
			arID:    "00012345-0006789a",
			wantErr: true,
		},
		{
			name:    "invalid format - too many parts",
			arID:    "00012345-0006789a-0a0b0c0d-extra",
			wantErr: true,
		},
		{
			name:    "invalid hex in discID1",
			arID:    "0001234g-0006789a-0a0b0c0d",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d1, d2, cd, err := parseARID(tt.arID)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseARID() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if d1 != tt.want1 {
					t.Errorf("discID1 = %x, want %x", d1, tt.want1)
				}
				if d2 != tt.want2 {
					t.Errorf("discID2 = %x, want %x", d2, tt.want2)
				}
				if cd != tt.wantCD {
					t.Errorf("cddbID = %x, want %x", cd, tt.wantCD)
				}
			}
		})
	}
}

func TestParseARResponse(t *testing.T) {
	// Build a test response with 1 disk, 2 tracks
	// Header: 1 byte count, 4 bytes each for discId1, discId2, cddbId (13 bytes total)
	// Track: 1 byte count, 4 bytes CRC, 4 bytes Frame450CRC (9 bytes each)
	data := make([]byte, 13+2*9)
	data[0] = 2 // 2 tracks
	binary.LittleEndian.PutUint32(data[1:], 0x11111111)  // discId1
	binary.LittleEndian.PutUint32(data[5:], 0x22222222)  // discId2
	binary.LittleEndian.PutUint32(data[9:], 0x33333333)  // cddbId

	// Track 1
	data[13] = 5                                            // 5 submissions
	binary.LittleEndian.PutUint32(data[14:], 0xAAAAAAAA)    // CRC
	binary.LittleEndian.PutUint32(data[18:], 0xBBBBBBBB)    // Frame450CRC

	// Track 2
	data[22] = 3                                            // 3 submissions
	binary.LittleEndian.PutUint32(data[23:], 0xCCCCCCCC)    // CRC
	binary.LittleEndian.PutUint32(data[27:], 0xDDDDDDDD)    // Frame450CRC

	resp, err := parseARResponse(data)
	if err != nil {
		t.Fatalf("parseARResponse failed: %v", err)
	}

	if len(resp.Disks) != 1 {
		t.Fatalf("expected 1 disk, got %d", len(resp.Disks))
	}

	disk := resp.Disks[0]
	if disk.TrackCount != 2 {
		t.Errorf("TrackCount = %d, want 2", disk.TrackCount)
	}
	if disk.DiscID1 != 0x11111111 {
		t.Errorf("DiscID1 = %x, want 11111111", disk.DiscID1)
	}
	if disk.DiscID2 != 0x22222222 {
		t.Errorf("DiscID2 = %x, want 22222222", disk.DiscID2)
	}
	if disk.CDDBDiscID != 0x33333333 {
		t.Errorf("CDDBDiscID = %x, want 33333333", disk.CDDBDiscID)
	}

	if len(disk.Tracks) != 2 {
		t.Fatalf("expected 2 tracks, got %d", len(disk.Tracks))
	}

	if disk.Tracks[0].Count != 5 || disk.Tracks[0].CRC != 0xAAAAAAAA {
		t.Errorf("Track[0] = %+v, want Count=5 CRC=AAAAAAAA", disk.Tracks[0])
	}
	if disk.Tracks[1].Count != 3 || disk.Tracks[1].CRC != 0xCCCCCCCC {
		t.Errorf("Track[1] = %+v, want Count=3 CRC=CCCCCCCC", disk.Tracks[1])
	}
}

func TestParseARResponseMultipleDisks(t *testing.T) {
	// Build response with 2 disks
	data := make([]byte, 2*(13+1*9))

	// Disk 1 (1 track)
	data[0] = 1
	binary.LittleEndian.PutUint32(data[1:], 0x11111111)
	binary.LittleEndian.PutUint32(data[5:], 0x22222222)
	binary.LittleEndian.PutUint32(data[9:], 0x33333333)
	data[13] = 2
	binary.LittleEndian.PutUint32(data[14:], 0xAABBCCDD)
	binary.LittleEndian.PutUint32(data[18:], 0x11223344)

	// Disk 2 (1 track)
	offset := 13 + 9
	data[offset] = 1
	binary.LittleEndian.PutUint32(data[offset+1:], 0x44444444)
	binary.LittleEndian.PutUint32(data[offset+5:], 0x55555555)
	binary.LittleEndian.PutUint32(data[offset+9:], 0x66666666)
	data[offset+13] = 4
	binary.LittleEndian.PutUint32(data[offset+14:], 0xEEEEEEEE)
	binary.LittleEndian.PutUint32(data[offset+18:], 0xFFFFFFFF)

	resp, err := parseARResponse(data)
	if err != nil {
		t.Fatalf("parseARResponse failed: %v", err)
	}

	if len(resp.Disks) != 2 {
		t.Fatalf("expected 2 disks, got %d", len(resp.Disks))
	}

	if resp.Disks[0].DiscID1 != 0x11111111 {
		t.Errorf("Disk[0].DiscID1 = %x, want 11111111", resp.Disks[0].DiscID1)
	}
	if resp.Disks[1].DiscID1 != 0x44444444 {
		t.Errorf("Disk[1].DiscID1 = %x, want 44444444", resp.Disks[1].DiscID1)
	}
}

func TestParseARResponseMalformed(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "too short for header",
			data: make([]byte, 5),
		},
		{
			name: "header claims more tracks than data",
			data: func() []byte {
				d := make([]byte, 13)
				d[0] = 5 // claims 5 tracks but no track data
				return d
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseARResponse(tt.data)
			if err == nil {
				t.Error("expected error for malformed response")
			}
		})
	}
}

func TestAccurateRipClientQuery(t *testing.T) {
	// Create test response
	testData := make([]byte, 13+2*9)
	testData[0] = 2
	binary.LittleEndian.PutUint32(testData[1:], 0x00012345)
	binary.LittleEndian.PutUint32(testData[5:], 0x0006789A)
	binary.LittleEndian.PutUint32(testData[9:], 0x0A0B0C0D)
	testData[13] = 10
	binary.LittleEndian.PutUint32(testData[14:], 0xDEADBEEF)
	binary.LittleEndian.PutUint32(testData[18:], 0x12345678)
	testData[22] = 8
	binary.LittleEndian.PutUint32(testData[23:], 0xCAFEBABE)
	binary.LittleEndian.PutUint32(testData[27:], 0x87654321)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify URL format
		expectedPath := "/5/4/3/dBAR-002-00012345-0006789a-0a0b0c0d.bin"
		if r.URL.Path != expectedPath {
			t.Errorf("unexpected path: %s, want %s", r.URL.Path, expectedPath)
		}
		w.Write(testData)
	}))
	defer server.Close()

	httpClient := NewHTTPClient()
	client := NewAccurateRipClient(httpClient, WithARBaseURL(server.URL))

	resp, err := client.Query(context.Background(), "00012345-0006789a-0a0b0c0d", 2)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(resp.Disks) != 1 {
		t.Fatalf("expected 1 disk, got %d", len(resp.Disks))
	}

	if resp.Disks[0].Tracks[0].CRC != 0xDEADBEEF {
		t.Errorf("CRC = %x, want DEADBEEF", resp.Disks[0].Tracks[0].CRC)
	}
}

func TestAccurateRipClientNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	httpClient := NewHTTPClient()
	client := NewAccurateRipClient(httpClient, WithARBaseURL(server.URL))

	_, err := client.Query(context.Background(), "00012345-0006789a-0a0b0c0d", 2)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
