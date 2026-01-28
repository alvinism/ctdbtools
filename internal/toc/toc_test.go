package toc

import (
	"testing"
)

func TestParseTOCString(t *testing.T) {
	tests := []struct {
		name    string
		tocStr  string
		want    []int
		wantErr bool
	}{
		{
			name:   "simple TOC",
			tocStr: "0:21658:46750:100000",
			want:   []int{0, 21658, 46750, 100000},
		},
		{
			name:   "TOC with pregap",
			tocStr: "37:21695:46787:100037",
			want:   []int{37, 21695, 46787, 100037},
		},
		{
			name:   "TOC with non-audio track",
			tocStr: "-150:21808:46900:100150",
			want:   []int{150, 21808, 46900, 100150},
		},
		{
			name:    "empty TOC",
			tocStr:  "",
			wantErr: true,
		},
		{
			name:    "invalid TOC - single value",
			tocStr:  "123",
			wantErr: true,
		},
		{
			name:    "invalid TOC - non-numeric",
			tocStr:  "abc:def",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTOCString(tt.tocStr)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseTOCString() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if len(got) != len(tt.want) {
					t.Errorf("ParseTOCString() got %d values, want %d", len(got), len(tt.want))
					return
				}
				for i := range got {
					if got[i] != tt.want[i] {
						t.Errorf("ParseTOCString()[%d] = %d, want %d", i, got[i], tt.want[i])
					}
				}
			}
		})
	}
}

func TestDetectPregapFromTOC(t *testing.T) {
	tests := []struct {
		name     string
		localTOC string
		ctdbTOC  string
		want     int
	}{
		{
			name:     "pregap detected",
			localTOC: "0:21658:46750:100000",
			ctdbTOC:  "37:21695:46787:100037",
			want:     37,
		},
		{
			name:     "no pregap difference",
			localTOC: "150:21808:46900:100150",
			ctdbTOC:  "150:21808:46900:100150",
			want:     0,
		},
		{
			name:     "larger pregap",
			localTOC: "0:21658:46750:100000",
			ctdbTOC:  "150:21808:46900:100150",
			want:     150,
		},
		{
			name:     "empty local TOC",
			localTOC: "",
			ctdbTOC:  "37:21695:46787:100037",
			want:     0,
		},
		{
			name:     "empty ctdb TOC",
			localTOC: "0:21658:46750:100000",
			ctdbTOC:  "",
			want:     0,
		},
		{
			name:     "negative difference (local has more)",
			localTOC: "150:21808:46900:100150",
			ctdbTOC:  "0:21658:46750:100000",
			want:     -150,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectPregapFromTOC(tt.localTOC, tt.ctdbTOC)
			if got != tt.want {
				t.Errorf("DetectPregapFromTOC() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestLayout_ApplyPregap(t *testing.T) {
	tests := []struct {
		name         string
		layout       Layout
		pregap       int
		wantStart0   int
		wantStart1   int
		wantPregap0  int
		wantLeadout  int
	}{
		{
			name: "apply 37 frame pregap",
			layout: Layout{
				FirstAudio:  1,
				AudioTracks: 2,
				Leadout:     100000,
				Tracks: []Track{
					{Start: 0, Length: 21658, IsAudio: true},
					{Start: 21658, Length: 78342, IsAudio: true},
				},
			},
			pregap:      37,
			wantStart0:  37,
			wantStart1:  21695,
			wantPregap0: 37,
			wantLeadout: 100037,
		},
		{
			name: "zero pregap - no change",
			layout: Layout{
				FirstAudio:  1,
				AudioTracks: 1,
				Leadout:     50000,
				Tracks: []Track{
					{Start: 0, Length: 50000, IsAudio: true},
				},
			},
			pregap:      0,
			wantStart0:  0,
			wantPregap0: 0,
			wantLeadout: 50000,
		},
		{
			name: "negative pregap - no change",
			layout: Layout{
				FirstAudio:  1,
				AudioTracks: 1,
				Leadout:     50000,
				Tracks: []Track{
					{Start: 100, Length: 50000, IsAudio: true},
				},
			},
			pregap:      -10,
			wantStart0:  100,
			wantPregap0: 0,
			wantLeadout: 50000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.layout.ApplyPregap(tt.pregap)

			if len(tt.layout.Tracks) > 0 {
				if tt.layout.Tracks[0].Start != tt.wantStart0 {
					t.Errorf("Track[0].Start = %d, want %d", tt.layout.Tracks[0].Start, tt.wantStart0)
				}
				if tt.layout.Tracks[0].Pregap != tt.wantPregap0 {
					t.Errorf("Track[0].Pregap = %d, want %d", tt.layout.Tracks[0].Pregap, tt.wantPregap0)
				}
			}
			if len(tt.layout.Tracks) > 1 && tt.wantStart1 != 0 {
				if tt.layout.Tracks[1].Start != tt.wantStart1 {
					t.Errorf("Track[1].Start = %d, want %d", tt.layout.Tracks[1].Start, tt.wantStart1)
				}
			}
			if tt.layout.Leadout != tt.wantLeadout {
				t.Errorf("Leadout = %d, want %d", tt.layout.Leadout, tt.wantLeadout)
			}
		})
	}
}

func TestAdjustTOCByPregap(t *testing.T) {
	tests := []struct {
		name   string
		tocStr string
		pregap int
		want   string
	}{
		{
			name:   "add 37 frame pregap",
			tocStr: "0:21658:46750:100000",
			pregap: 37,
			want:   "37:21695:46787:100037",
		},
		{
			name:   "add 150 frame pregap",
			tocStr: "0:21658:46750:100000",
			pregap: 150,
			want:   "150:21808:46900:100150",
		},
		{
			name:   "zero pregap - no change",
			tocStr: "0:21658:46750:100000",
			pregap: 0,
			want:   "0:21658:46750:100000",
		},
		{
			name:   "empty TOC",
			tocStr: "",
			pregap: 37,
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AdjustTOCByPregap(tt.tocStr, tt.pregap)
			if got != tt.want {
				t.Errorf("AdjustTOCByPregap() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLayout_TOCID(t *testing.T) {
	tests := []struct {
		name    string
		layout  Layout
		wantErr bool
	}{
		{
			name: "basic single track layout",
			layout: Layout{
				FirstAudio:  1,
				AudioTracks: 1,
				Leadout:     50000,
				Tracks: []Track{
					{Start: 0, Length: 50000, IsAudio: true},
				},
			},
			wantErr: false,
		},
		{
			name: "multi-track layout",
			layout: Layout{
				FirstAudio:  1,
				AudioTracks: 3,
				Leadout:     100000,
				Tracks: []Track{
					{Start: 0, Length: 30000, IsAudio: true},
					{Start: 30000, Length: 35000, IsAudio: true},
					{Start: 65000, Length: 35000, IsAudio: true},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid FirstAudio",
			layout: Layout{
				FirstAudio:  0,
				AudioTracks: 1,
				Tracks:      []Track{{Start: 0, Length: 1000, IsAudio: true}},
			},
			wantErr: true,
		},
		{
			name: "invalid AudioTracks",
			layout: Layout{
				FirstAudio:  1,
				AudioTracks: 0,
				Tracks:      []Track{{Start: 0, Length: 1000, IsAudio: true}},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.layout.TOCID()
			if (err != nil) != tt.wantErr {
				t.Errorf("TOCID() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got == "" {
				t.Error("TOCID() returned empty string")
			}
		})
	}
}

func TestLayout_TOCID_SameRelativeOffsets(t *testing.T) {
	// Key insight: TOCID is based on RELATIVE offsets from track 1
	// Two layouts with the same relative track positions should produce the same TOCID
	// regardless of their absolute start positions

	layout1 := Layout{
		FirstAudio:  1,
		AudioTracks: 3,
		Leadout:     100000,
		Tracks: []Track{
			{Start: 0, Length: 30000, IsAudio: true},
			{Start: 30000, Length: 35000, IsAudio: true},
			{Start: 65000, Length: 35000, IsAudio: true},
		},
	}

	// Same layout but with 37-frame offset (simulating pregap adjustment)
	layout2 := Layout{
		FirstAudio:  1,
		AudioTracks: 3,
		Leadout:     100037,
		Tracks: []Track{
			{Start: 37, Length: 30000, IsAudio: true},
			{Start: 30037, Length: 35000, IsAudio: true},
			{Start: 65037, Length: 35000, IsAudio: true},
		},
	}

	tocid1, err1 := layout1.TOCID()
	tocid2, err2 := layout2.TOCID()

	if err1 != nil || err2 != nil {
		t.Fatalf("TOCID() errors: %v, %v", err1, err2)
	}

	if tocid1 != tocid2 {
		t.Errorf("Expected same TOCID for layouts with same relative offsets.\nLayout1: %s\nLayout2: %s", tocid1, tocid2)
	}
}

func TestLayout_TOCString(t *testing.T) {
	tests := []struct {
		name   string
		layout Layout
		want   string
	}{
		{
			name: "single audio track",
			layout: Layout{
				Leadout: 50000,
				Tracks: []Track{
					{Start: 0, Length: 50000, IsAudio: true},
				},
			},
			want: "0:50000",
		},
		{
			name: "multiple audio tracks",
			layout: Layout{
				Leadout: 100000,
				Tracks: []Track{
					{Start: 0, Length: 30000, IsAudio: true},
					{Start: 30000, Length: 35000, IsAudio: true},
					{Start: 65000, Length: 35000, IsAudio: true},
				},
			},
			want: "0:30000:65000:100000",
		},
		{
			name: "with non-audio track",
			layout: Layout{
				Leadout: 100000,
				Tracks: []Track{
					{Start: 150, Length: 30000, IsAudio: false},
					{Start: 30150, Length: 35000, IsAudio: true},
					{Start: 65150, Length: 34850, IsAudio: true},
				},
			},
			want: "-150:30150:65150:100000",
		},
		{
			name: "with pregap offset",
			layout: Layout{
				Leadout: 100037,
				Tracks: []Track{
					{Start: 37, Length: 30000, IsAudio: true},
					{Start: 30037, Length: 35000, IsAudio: true},
					{Start: 65037, Length: 35000, IsAudio: true},
				},
			},
			want: "37:30037:65037:100037",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.layout.TOCString()
			if got != tt.want {
				t.Errorf("TOCString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPregapFieldDoesNotAffectAudioLengthCalculation(t *testing.T) {
	// This test documents the critical insight from the pregap bug fix:
	// The Pregap field in Track does NOT affect AudioLengthFrames() calculation.
	// AudioLengthFrames() uses Start positions only.
	//
	// This is why when we detect a pregap offset for TOCID matching, we should
	// adjust track Start positions (for TOCID calculation) but NOT set the
	// Pregap field on the original layout used for repair calculations.

	layout := Layout{
		FirstAudio:  1,
		AudioTracks: 2,
		Leadout:     50000,
		Tracks: []Track{
			{Start: 0, Length: 25000, IsAudio: true},
			{Start: 25000, Length: 25000, IsAudio: true},
		},
	}

	lengthBefore := layout.AudioLengthFrames()

	// Setting Pregap field should NOT change AudioLengthFrames
	// because AudioLengthFrames uses Start/End positions, not Pregap
	layout.Tracks[0].Pregap = 37

	lengthAfter := layout.AudioLengthFrames()

	if lengthBefore != lengthAfter {
		t.Errorf("Setting Pregap field unexpectedly changed AudioLengthFrames: before=%d, after=%d",
			lengthBefore, lengthAfter)
	}
}

func TestApplyPregapAffectsStartPositions(t *testing.T) {
	// This test documents ApplyPregap behavior:
	// ApplyPregap modifies Start positions AND sets Pregap field.
	// This is used when we need to adjust the TOC for CTDB matching.

	layout := Layout{
		FirstAudio:  1,
		AudioTracks: 2,
		Leadout:     50000,
		Tracks: []Track{
			{Start: 0, Length: 25000, IsAudio: true},
			{Start: 25000, Length: 25000, IsAudio: true},
		},
	}

	tocBefore := layout.TOCString()
	layout.ApplyPregap(37)
	tocAfter := layout.TOCString()

	if tocBefore == tocAfter {
		t.Error("ApplyPregap should change TOCString")
	}

	expectedTOC := "37:25037:50037"
	if tocAfter != expectedTOC {
		t.Errorf("TOCString after ApplyPregap = %q, want %q", tocAfter, expectedTOC)
	}

	// Verify Pregap field was set
	if layout.Tracks[0].Pregap != 37 {
		t.Errorf("Tracks[0].Pregap = %d, want 37", layout.Tracks[0].Pregap)
	}
}

func TestDetectPregapFromTOC_RealWorldCase(t *testing.T) {
	// Real-world scenario from VDR-1255 disc:
	// Local rip starts at frame 0, but CTDB entry starts at frame 37
	// This indicates the original disc had a 37-frame pregap before track 1

	localTOC := "0:21658:46750:100000"
	ctdbTOC := "37:21695:46787:100037"

	pregap := DetectPregapFromTOC(localTOC, ctdbTOC)

	if pregap != 37 {
		t.Errorf("DetectPregapFromTOC() = %d, want 37", pregap)
	}

	// Verify that adjusting local TOC by detected pregap matches CTDB TOC
	adjustedTOC := AdjustTOCByPregap(localTOC, pregap)
	if adjustedTOC != ctdbTOC {
		t.Errorf("AdjustTOCByPregap(localTOC, %d) = %q, want %q", pregap, adjustedTOC, ctdbTOC)
	}
}
