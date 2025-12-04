package cli

import (
	"context"
	"fmt"

	"ctdbtool/internal/accuraterip"
	"ctdbtool/internal/ingest"
	"ctdbtool/internal/network"
	"ctdbtool/internal/toc"
)

// VerifyOptions holds parameters for a verify run.
type VerifyOptions struct {
	AudioPath  string
	Layout     toc.Layout
	CuePath    string
	Stride     int
	LastStride int
	Npar       int
	CalcParity bool
	QueryAR    bool // Query AccurateRip database
	QueryCTDB  bool // Query CTDB database
	Verbose    bool // Verbose output
	Debug      bool // Debug output for CRC comparison
}

// Verify runs a verification pass (decode PCM, compute CRCs/parity, query databases).
func Verify(ctx context.Context, opts VerifyOptions) error {
	var layout toc.Layout
	var sheet ingest.CueSheet
	var useCueSheet bool
	var proc *accuraterip.Processor

	if opts.Layout.AudioTracks > 0 {
		// Layout provided directly
		layout = opts.Layout
	} else if opts.CuePath != "" {
		// Parse CUE sheet with split track support
		var err error
		sheet, err = ingest.ParseCueSheetFile(opts.CuePath)
		if err != nil {
			return fmt.Errorf("failed to parse CUE: %w", err)
		}
		layout = sheet.Layout
		useCueSheet = true
	} else {
		return fmt.Errorf("layout or cue path required")
	}

	// Process audio
	if useCueSheet && sheet.IsSplitTrack() {
		// Split track mode: probe each file for accurate durations
		err := probeSplitTrackDurations(ctx, &sheet)
		if err != nil {
			return fmt.Errorf("failed to probe track durations: %w", err)
		}
		layout = sheet.Layout

		// Process split tracks
		var err2 error
		proc, err2 = ingest.ProcessCueSheet(ctx, sheet, opts.Stride, opts.LastStride, opts.Npar, opts.CalcParity)
		if err2 != nil {
			return err2
		}
	} else {
		// Single file mode
		audioPath := opts.AudioPath
		if audioPath == "" && useCueSheet && len(sheet.Sources) > 0 {
			// Get audio path from CUE sheet
			audioPath = sheet.Sources[0].FilePath
			if audioPath != "" && sheet.CueDir != "" {
				audioPath = sheet.CueDir + "/" + audioPath
			}
		}
		if audioPath == "" {
			return fmt.Errorf("audio path required")
		}

		// Probe duration for accurate leadout
		if frames, err := ingest.ProbeDurationFrames(ctx, audioPath); err == nil && frames > 0 {
			// Update last track length with probed duration
			if len(layout.Tracks) > 0 {
				lastIdx := len(layout.Tracks) - 1
				lastTrack := &layout.Tracks[lastIdx]
				lastTrack.Length = frames - lastTrack.Start
				layout.Leadout = frames
			}
		}

		var err error
		proc, err = ingest.ProcessFile(ctx, audioPath, layout, opts.Stride, opts.LastStride, opts.Npar, opts.CalcParity)
		if err != nil {
			return err
		}
	}

	// Display TOCID
	tocID, err := layout.TOCID()
	if err != nil {
		fmt.Printf("TOCID: (error: %v)\n", err)
	} else {
		fmt.Printf("TOCID: %s\n", tocID)
	}

	// Debug output: dump layout and CRC state
	if opts.Debug {
		printDebugLayout(layout, opts.Stride, opts.LastStride)
		// Also print AudioLayout and Sources for split tracks
		if useCueSheet && sheet.IsSplitTrack() {
			audioLayout := sheet.GetAudioLayout()
			fmt.Println("\n=== Split Track Details ===")
			for i, t := range audioLayout.Tracks {
				fmt.Printf("  Track %d: Length=%d frames (%d samples)\n",
					i+1, t.Length, t.Length*588)
			}
			fmt.Println("===========================")
		}
	}

	// Display per-track CRCs
	fmt.Println("\nTrack  [ CRC32  ] [W/O NULL] [  AR   |   V2   ]")
	for track := 1; track <= layout.AudioTracks; track++ {
		crc32 := proc.TrackCRC(track, 0)
		crcwn := proc.TrackCRCWONULL(track, 0)
		crcar := proc.TrackCRCAR(track)
		crcv2 := proc.TrackCRCV2(track)
		fmt.Printf(" %02d    [%08X] [%08X] [%08x|%08x]\n", track, crc32, crcwn, crcar, crcv2)

		// Debug: show CTDB CRC calculation details
		if opts.Debug {
			ctdbCRC := proc.TrackCTDBCRC(track, 0, opts.Stride, opts.LastStride)
			fmt.Printf("       CTDB CRC: %08X (stride=%d, laststride=%d)\n", ctdbCRC, opts.Stride, opts.LastStride)

			// For first and last track, show detailed calculation
			if track == 1 || track == layout.AudioTracks {
				printDebugCTDBCRC(proc, layout, track, opts.Stride, opts.LastStride)
			}
		}
	}

	// Display disc-wide CRC
	discCRC := proc.TrackCRC(0, 0)
	discCRCWN := proc.TrackCRCWONULL(0, 0)
	fmt.Printf(" --    [%08X] [%08X]\n", discCRC, discCRCWN)

	if opts.Debug {
		discCTDBCRC := proc.DiscCTDBCRC(0, opts.Stride, opts.LastStride)
		fmt.Printf("       Disc CTDB CRC: %08X\n", discCTDBCRC)
		printDebugRollingTables(proc, layout.AudioTracks)
	}

	if opts.CalcParity {
		fmt.Printf("\nSyndrome rows: %d\n", len(proc.Syndrome()))
	}

	// Query AccurateRip if requested
	if opts.QueryAR {
		if err := queryAccurateRip(ctx, layout, proc, opts.Verbose); err != nil {
			fmt.Printf("AccurateRip: %v\n", err)
		}
	}

	// Query CTDB if requested
	if opts.QueryCTDB {
		if err := queryCTDB(ctx, layout, proc, opts.Verbose); err != nil {
			fmt.Printf("CTDB: %v\n", err)
		}
	}

	return nil
}

// queryAccurateRip queries the AccurateRip database and displays results.
func queryAccurateRip(ctx context.Context, layout toc.Layout, proc *accuraterip.Processor, verbose bool) error {
	arID, err := layout.AccurateRipID()
	if err != nil {
		return fmt.Errorf("failed to compute AccurateRip ID: %w", err)
	}

	fmt.Printf("\n[AccurateRip ID: %s]\n", arID)

	httpClient := network.NewHTTPClient()
	arClient := network.NewAccurateRipClient(httpClient)

	resp, err := arClient.Query(ctx, arID, layout.AudioTracks)
	if err != nil {
		if err == network.ErrNotFound {
			fmt.Println("AccurateRip: Disc not found in database")
			return nil
		}
		return err
	}

	fmt.Printf("AccurateRip: Found %d pressing(s)\n", len(resp.Disks))

	// Compare our CRCs against database for each track
	// AccurateRip database stores either v1 or v2 CRC in the CRC field
	// We need to compare both our v1 and v2 against the database CRC
	fmt.Println("Track   [  CRC   |   V2   ] Status")
	for track := 1; track <= layout.AudioTracks; track++ {
		localAR := proc.TrackCRCAR(track)
		localV2 := proc.TrackCRCV2(track)

		// Check all pressings for a match
		// matchCount = matches on v1, v2MatchCount = matches on v2
		var matchCount, v2MatchCount, totalCount int
		for _, disk := range resp.Disks {
			if track-1 < len(disk.Tracks) {
				dbTrack := disk.Tracks[track-1]
				totalCount += int(dbTrack.Count)
				// Compare both v1 and v2 against database CRC
				if dbTrack.CRC == localAR {
					matchCount += int(dbTrack.Count)
				} else if dbTrack.CRC == localV2 {
					v2MatchCount += int(dbTrack.Count)
				}
			}
		}

		status := "No match"
		if matchCount > 0 || v2MatchCount > 0 {
			status = fmt.Sprintf("(%02d+%02d/%d) Accurately ripped", matchCount, v2MatchCount, totalCount)
		}
		fmt.Printf(" %02d     [%08x|%08x] %s\n", track, localAR, localV2, status)
	}

	// Show pressing details in verbose mode
	if verbose {
		for i, disk := range resp.Disks {
			fmt.Printf("\n  Pressing %d: %d tracks\n", i+1, disk.TrackCount)
			for j, track := range disk.Tracks {
				fmt.Printf("    Track %d: CRC=%08X Frame450=%08X count=%d\n", j+1, track.CRC, track.Frame450CRC, track.Count)
			}
		}
	}

	return nil
}

// queryCTDB queries the CUETools Database and displays results.
func queryCTDB(ctx context.Context, layout toc.Layout, proc *accuraterip.Processor, verbose bool) error {
	tocStr := layout.TOCString()
	fmt.Printf("\nCTDB TOC: %s\n", tocStr)

	httpClient := network.NewHTTPClient()
	ctdbClient := network.NewCTDBClient(httpClient)

	resp, err := ctdbClient.Lookup(ctx, network.CTDBLookupOptions{
		TOC:            tocStr,
		CTDB:           true,
		Fuzzy:          false,
		MetadataSearch: network.CTDBMetadataSearchNone,
	})
	if err != nil {
		if err == network.ErrNotFound {
			fmt.Println("CTDB: Disc not found in database")
			return nil
		}
		return err
	}

	fmt.Printf("CTDB: Found %d entries (total confidence: %d)\n", len(resp.Entries), resp.Total)

	// Aggregate per-track match counts across all entries
	trackMatches := make([]int, layout.AudioTracks)
	totalConfidence := 0

	for _, entry := range resp.Entries {
		if len(entry.TrackCRCs) != layout.AudioTracks {
			continue // skip entries with wrong track count
		}
		// CTDB entry.Stride is already doubled by the parser (CTDB returns stride/2)
		stride := entry.Stride
		laststride := stride // CTDB uses same stride for last track typically

		for track := 1; track <= layout.AudioTracks; track++ {
			localCRC := proc.TrackCTDBCRC(track, 0, stride, laststride)
			if localCRC == entry.TrackCRCs[track-1] {
				trackMatches[track-1] += entry.Confidence
			}
		}
		totalConfidence += entry.Confidence
	}

	// Check disc CRC against entries
	discMatched := false
	for _, entry := range resp.Entries {
		if len(entry.TrackCRCs) != layout.AudioTracks {
			continue
		}
		stride := entry.Stride
		laststride := stride
		localDiscCRC := proc.DiscCTDBCRC(0, stride, laststride)
		if localDiscCRC == entry.CRC32 {
			discMatched = true
			break
		}
	}

	// Display per-track CTDB verification status
	fmt.Println("Track | CTDB Status")
	for track := 1; track <= layout.AudioTracks; track++ {
		matches := trackMatches[track-1]
		var status string
		if matches > 0 {
			status = fmt.Sprintf("(%d/%d) Accurately ripped", matches, totalConfidence)
		} else if totalConfidence > 0 {
			status = fmt.Sprintf("(0/%d) No match", totalConfidence)
		} else {
			status = "No entries to compare"
		}
		fmt.Printf(" %2d   | %s\n", track, status)
	}

	if discMatched {
		fmt.Println("Disc CRC: Match")
	} else if totalConfidence > 0 {
		fmt.Println("Disc CRC: No match")
	}

	// Display entry details in verbose mode
	if verbose {
		fmt.Println()
		for i, entry := range resp.Entries {
			fmt.Printf("  Entry %d: CRC32=%08X confidence=%d npar=%d stride=%d\n",
				i+1, entry.CRC32, entry.Confidence, entry.Npar, entry.Stride)
			if len(entry.TrackCRCs) > 0 {
				fmt.Printf("    Track CRCs: ")
				for _, crc := range entry.TrackCRCs {
					fmt.Printf("%08X ", crc)
				}
				fmt.Println()
			}
			if entry.HasParity != "" {
				fmt.Printf("    Parity available: %s\n", entry.HasParity)
			}
		}

		// Display metadata if present
		if len(resp.Metadata) > 0 {
			meta := resp.Metadata[0]
			fmt.Printf("  Metadata: %s - %s (%s)\n", meta.Artist, meta.Album, meta.Year)
		}
	}

	return nil
}

// printDebugLayout prints layout information for debugging.
func printDebugLayout(layout toc.Layout, stride, laststride int) {
	fmt.Println("\n=== DEBUG: Layout Information ===")
	fmt.Printf("AudioTracks: %d\n", layout.AudioTracks)
	fmt.Printf("FirstAudio: %d\n", layout.FirstAudio)
	fmt.Printf("Leadout: %d frames\n", layout.Leadout)
	fmt.Printf("AudioLengthFrames: %d\n", layout.AudioLengthFrames())
	fmt.Printf("Stride: %d, LastStride: %d\n", stride, laststride)
	fmt.Printf("MaxOffset (calculated): %d\n", calculateMaxOffset(stride, laststride))

	fmt.Println("\nTrack details:")
	for i, t := range layout.Tracks {
		fmt.Printf("  Track %d: Start=%d Length=%d Pregap=%d\n",
			i+1, t.Start, t.Length, t.Pregap)
	}

	fmt.Println("\nTrack positions (frames):")
	for track := 1; track <= layout.AudioTracks; track++ {
		startFrame := layout.TrackStartFrame(track)
		lengthFrames := layout.TrackLengthFrames(track)
		fmt.Printf("  Track %d: StartFrame=%d LengthFrames=%d Samples=%d\n",
			track, startFrame, lengthFrames, lengthFrames*588)
	}
	fmt.Println("=================================")
}

// printDebugRollingTables prints rolling table values at key offsets.
func printDebugRollingTables(proc *accuraterip.Processor, audioTracks int) {
	rolling := proc.Rolling()
	if rolling == nil {
		return
	}

	fmt.Println("\n=== DEBUG: Rolling Table State ===")
	fmt.Printf("MaxOffset: %d\n", rolling.MaxOffset)
	fmt.Printf("OffsetRangeAR: %d\n", rolling.OffsetRangeAR)

	// Print CRC state at key offsets for each track
	for track := 0; track <= audioTracks; track++ {
		fmt.Printf("\nTrack %d:\n", track)

		// Show values at offset 0 (accumulated state)
		fmt.Printf("  Offset 0: CRCAR=%08X CRCSM=%08X CRCV2=%08X\n",
			rolling.CRCAR[track][0],
			rolling.CRCSM[track][0],
			rolling.CRCV2[track][0])

		// Show values at 2*maxOffset (final accumulated CRC32/CRCWN)
		if 2*rolling.MaxOffset < len(rolling.CRC32[track]) {
			fmt.Printf("  Offset 2*maxOffset (%d): CRC32=%08X CRCWN=%08X CRCNL=%d\n",
				2*rolling.MaxOffset,
				rolling.CRC32[track][2*rolling.MaxOffset],
				rolling.CRCWN[track][2*rolling.MaxOffset],
				rolling.CRCNL[track][2*rolling.MaxOffset])
		}

		// Show head values (first few offsets)
		fmt.Printf("  Head [0..4]: CRC32=[%08X %08X %08X %08X %08X]\n",
			rolling.CRC32[track][0],
			rolling.CRC32[track][1],
			rolling.CRC32[track][2],
			rolling.CRC32[track][3],
			rolling.CRC32[track][4])

		// Show tail values (around 2*maxOffset - samples)
		tailStart := 2*rolling.MaxOffset - 5
		if tailStart >= 0 && tailStart+4 < len(rolling.CRC32[track]) {
			fmt.Printf("  Tail [%d..%d]: CRC32=[%08X %08X %08X %08X %08X]\n",
				tailStart, tailStart+4,
				rolling.CRC32[track][tailStart],
				rolling.CRC32[track][tailStart+1],
				rolling.CRC32[track][tailStart+2],
				rolling.CRC32[track][tailStart+3],
				rolling.CRC32[track][tailStart+4])
		}
	}
	fmt.Println("==================================")
}

// calculateMaxOffset mirrors CueTools maxOffset calculation.
func calculateMaxOffset(stride, laststride int) int {
	maxOffset := stride + laststride
	if maxOffset < 4096*2 {
		maxOffset = 4096 * 2
	}
	if rem := maxOffset % 588; rem != 0 {
		maxOffset += 588 - rem
	}
	return maxOffset
}

// printDebugCTDBCRC prints detailed CTDBCRC calculation for debugging.
func printDebugCTDBCRC(proc *accuraterip.Processor, layout toc.Layout, track, stride, laststride int) {
	rolling := proc.Rolling()
	if rolling == nil {
		return
	}

	prefixSamples := stride / 2
	suffixSamples := laststride / 2
	oi := 0 // offset = 0

	fmt.Printf("       === CTDBCRC Debug for track %d ===\n", track)
	fmt.Printf("       prefixSamples=%d, suffixSamples=%d, oi=%d\n", prefixSamples, suffixSamples, oi)

	// Show track positions
	var posA, posB int
	if track > 1 {
		posA = layout.TrackStartFrame(track)*588 + oi
	} else {
		posA = layout.TrackStartFrame(track)*588 + prefixSamples
	}
	if track < layout.AudioTracks {
		posB = layout.TrackStartFrame(track+1)*588 + oi
	} else {
		posB = layout.Leadout*588 - suffixSamples
	}
	fmt.Printf("       posA=%d, posB=%d, chunk=%d samples, %d bytes\n", posA, posB, posB-posA, (posB-posA)*4)

	// Show CRC values being used
	maxOffset := rolling.MaxOffset
	var crcA, crcB uint32
	var crcASource, crcBSource string
	if oi > 0 {
		if track > 1 {
			crcA = rolling.CRC32[track][oi]
			crcASource = fmt.Sprintf("CRC32[%d][%d]", track, oi)
		} else {
			crcA = rolling.CRC32[track][prefixSamples]
			crcASource = fmt.Sprintf("CRC32[%d][%d]", track, prefixSamples)
		}
		if track < layout.AudioTracks {
			crcB = rolling.CRC32[track+1][oi]
			crcBSource = fmt.Sprintf("CRC32[%d][%d]", track+1, oi)
		} else {
			crcB = rolling.CRC32[track][2*maxOffset-suffixSamples]
			crcBSource = fmt.Sprintf("CRC32[%d][%d]", track, 2*maxOffset-suffixSamples)
		}
	} else {
		if track > 1 {
			crcA = rolling.CRC32[track-1][2*maxOffset+oi]
			crcASource = fmt.Sprintf("CRC32[%d][%d]", track-1, 2*maxOffset+oi)
		} else {
			crcA = rolling.CRC32[track][prefixSamples]
			crcASource = fmt.Sprintf("CRC32[%d][%d]", track, prefixSamples)
		}
		if track < layout.AudioTracks {
			crcB = rolling.CRC32[track][2*maxOffset+oi]
			crcBSource = fmt.Sprintf("CRC32[%d][%d]", track, 2*maxOffset+oi)
		} else {
			crcB = rolling.CRC32[track][2*maxOffset-suffixSamples]
			crcBSource = fmt.Sprintf("CRC32[%d][%d]", track, 2*maxOffset-suffixSamples)
		}
	}
	fmt.Printf("       crcA=%08X from %s\n", crcA, crcASource)
	fmt.Printf("       crcB=%08X from %s\n", crcB, crcBSource)

	// For last track, also show surrounding values
	if track == layout.AudioTracks {
		tailOffset := 2*maxOffset - suffixSamples
		fmt.Printf("       Tail region around offset %d:\n", tailOffset)
		for i := -3; i <= 3; i++ {
			off := tailOffset + i
			if off >= 0 && off < len(rolling.CRC32[track]) {
				fmt.Printf("         CRC32[%d][%d] = %08X\n", track, off, rolling.CRC32[track][off])
			}
		}
		// Also show offset 2*maxOffset (final accumulated)
		fmt.Printf("       Final CRC at 2*maxOffset (%d): %08X\n", 2*maxOffset, rolling.CRC32[track][2*maxOffset])
		// And show what we'd expect if posInTrack = trackTotal - suffixSamples at that offset
		trackTotal := layout.TrackLengthFrames(track) * 588
		expectedPos := trackTotal - suffixSamples
		fmt.Printf("       Track total samples: %d, expectedPos for suffix: %d\n", trackTotal, expectedPos)
		// Also show a few values in the head region to compare with tail
		if expectedPos < maxOffset {
			fmt.Printf("       (expectedPos is in head region, should have been copied to tail)\n")
			fmt.Printf("       CRC32[%d][%d] (head) = %08X\n", track, expectedPos, rolling.CRC32[track][expectedPos])
		}

		// Show previous track's final value
		fmt.Printf("       Previous track (track %d) final CRC at [%d][%d]: %08X\n",
			track-1, track-1, 2*maxOffset, rolling.CRC32[track-1][2*maxOffset])
		// Show this track's head
		fmt.Printf("       This track head [0]: CRC32=%08X\n", rolling.CRC32[track][0])
		// Show the expected neededCrcB
		fmt.Printf("       Needed crcB for correct result: DA7D6B85\n")
	}
	fmt.Printf("       =====================================\n")
}

// probeSplitTrackDurations probes audio files to determine the correct TOC for split tracks.
// For split track CUEs, CueTools uses the ENTIRE FILE for each track's CRC calculation,
// including any embedded pregap at the end of the file (pregap for next track).
// Both Layout and AudioLayout use file durations for split tracks.
func probeSplitTrackDurations(ctx context.Context, sheet *ingest.CueSheet) error {
	if len(sheet.Sources) == 0 {
		return nil
	}

	// Probe all file durations first
	fileDurations := make([]int, len(sheet.Sources))
	for i, src := range sheet.Sources {
		if src.FilePath == "" {
			continue
		}

		audioPath := src.FilePath
		if sheet.CueDir != "" {
			audioPath = sheet.CueDir + "/" + audioPath
		}

		frames, err := ingest.ProbeDurationFrames(ctx, audioPath)
		if err != nil {
			return fmt.Errorf("failed to probe %s: %w", audioPath, err)
		}
		fileDurations[i] = frames
	}

	// For CRC calculation, use FULL FILE durations (including embedded pregaps)
	// This matches CueTools behavior where each track file is processed entirely
	audioLengths := make([]int, len(sheet.Layout.Tracks))
	for i := range sheet.Layout.Tracks {
		if i < len(fileDurations) {
			audioLengths[i] = fileDurations[i]
		} else {
			audioLengths[i] = sheet.Layout.Tracks[i].Length
		}
	}

	// For TOC: track positions are cumulative file durations
	cumulativeFrames := 0
	for i := range sheet.Layout.Tracks {
		sheet.Layout.Tracks[i].Start = cumulativeFrames

		if i < len(fileDurations) {
			// Track length for TOC = file duration (includes audio + embedded pregap)
			sheet.Layout.Tracks[i].Length = fileDurations[i]

			// Update ALL source lengths to full file durations
			sheet.Sources[i].Length = int64(fileDurations[i]) * 588

			cumulativeFrames += fileDurations[i]
		}
	}

	// Update leadout
	if len(sheet.Layout.Tracks) > 0 {
		lastTrack := sheet.Layout.Tracks[len(sheet.Layout.Tracks)-1]
		sheet.Layout.Leadout = lastTrack.Start + lastTrack.Length
	}

	// Create AudioLayout with audio-only lengths for CRC calculation
	sheet.AudioLayout = toc.Layout{
		FirstAudio:  sheet.Layout.FirstAudio,
		AudioTracks: sheet.Layout.AudioTracks,
		Leadout:     sheet.Layout.Leadout,
		Tracks:      make([]toc.Track, len(sheet.Layout.Tracks)),
	}
	audioCumulative := 0
	for i := range sheet.AudioLayout.Tracks {
		sheet.AudioLayout.Tracks[i] = sheet.Layout.Tracks[i]
		sheet.AudioLayout.Tracks[i].Start = audioCumulative
		sheet.AudioLayout.Tracks[i].Length = audioLengths[i]
		audioCumulative += audioLengths[i]
	}
	sheet.AudioLayout.Leadout = audioCumulative

	return nil
}
