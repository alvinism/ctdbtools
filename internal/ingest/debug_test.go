package ingest

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"testing"
	"time"
)

func TestCueParse(t *testing.T) {
	f, err := os.Open("../../materials/[[960916]V6 - TAKE ME HIGHER/V6 - TAKE ME HIGHER.cue")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil && err != io.EOF {
		t.Fatal(err)
	}
	layout, err := ParseCue(lines)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("Layout: FirstAudio=%d, AudioTracks=%d, Leadout=%d\n", layout.FirstAudio, layout.AudioTracks, layout.Leadout)
	for i, tr := range layout.Tracks {
		fmt.Printf("  Track %d: Start=%d, Length=%d, Pregap=%d, IsAudio=%v\n", i+1, tr.Start, tr.Length, tr.Pregap, tr.IsAudio)
	}
	fmt.Printf("AudioLengthFrames: %d\n", layout.AudioLengthFrames())
	for i := 1; i <= layout.AudioTracks; i++ {
		fmt.Printf("TrackLengthFrames(%d): %d samples\n", i, layout.TrackLengthFrames(i)*588)
	}
	tocid, _ := layout.TOCID()
	arid, _ := layout.AccurateRipID()
	fmt.Printf("TOCID: %s\n", tocid)
	fmt.Printf("AccurateRip ID: %s\n", arid)
}

func TestProcessFile(t *testing.T) {
	ctx := context.Background()
	cuePath := "../../materials/[[960916]V6 - TAKE ME HIGHER/V6 - TAKE ME HIGHER.cue"
	audioPath := "../../materials/[[960916]V6 - TAKE ME HIGHER/V6 - TAKE ME HIGHER.wav"

	fmt.Println("Probing duration...")
	start := time.Now()
	frames, err := ProbeDurationFrames(ctx, audioPath)
	fmt.Printf("Duration: %d frames (took %v)\n", frames, time.Since(start))
	if err != nil {
		t.Fatal(err)
	}

	fmt.Println("Parsing CUE...")
	start = time.Now()
	layout, err := ParseCueFileWithLeadout(cuePath, frames)
	fmt.Printf("Parsed (took %v)\n", time.Since(start))
	if err != nil {
		t.Fatal(err)
	}

	fmt.Printf("Layout: %d tracks, leadout=%d\n", layout.AudioTracks, layout.Leadout)
	for i, tr := range layout.Tracks {
		fmt.Printf("  Track %d: Start=%d, Length=%d (%d samples)\n", i+1, tr.Start, tr.Length, tr.Length*588)
	}

	fmt.Println("Processing audio...")
	start = time.Now()
	proc, err := ProcessFile(ctx, audioPath, layout, 11760, 11760, 8, false)
	fmt.Printf("Processed (took %v)\n", time.Since(start))
	if err != nil {
		t.Fatal(err)
	}

	fmt.Println("CRCs:")
	for i := 1; i <= layout.AudioTracks; i++ {
		fmt.Printf("  Track %d: CRC32=%08X\n", i, proc.TrackCRC(i, 0))
	}
}
