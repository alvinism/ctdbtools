package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"ctdbtools/internal/accuraterip"
	"ctdbtools/internal/ingest"
	"ctdbtools/internal/logparse"
	"ctdbtools/internal/network"
	"ctdbtools/internal/parity"
	"ctdbtools/internal/progress"
	"ctdbtools/internal/repair"
	"ctdbtools/internal/toc"
	"ctdbtools/internal/version"
)

// ctdbResult holds the async CTDB lookup result.
type ctdbResult struct {
	resp *network.CTDBResponse
	err  error
}

// parityResult holds the async parity fetch result.
type parityResult struct {
	data map[int][][]uint16
	err  error
}

// startCTDBQuery launches CTDB lookup in background goroutine.
// This allows the CTDB network request to run concurrently with audio decoding.
func startCTDBQuery(ctx context.Context, tocStr string) <-chan ctdbResult {
	ch := make(chan ctdbResult, 1)
	go func() {
		httpClient := network.NewHTTPClient()
		ctdbClient := network.NewCTDBClient(httpClient)
		resp, err := ctdbClient.Lookup(ctx, network.CTDBLookupOptions{
			TOC:            tocStr,
			CTDB:           true,
			Fuzzy:          false,
			MetadataSearch: network.CTDBMetadataSearchNone,
		})
		ch <- ctdbResult{resp: resp, err: err}
	}()
	return ch
}

// startParityFetchWithNotification launches parity fetching in background, chained off CTDB result.
// Returns two channels:
// - ctdbNotifyCh: sends CTDB result as soon as lookup completes (for immediate "found" message)
// - parityCh: sends parity data when all fetches complete
// This allows printing "found CTDB ID" immediately while parity fetch continues in background.
func startParityFetchWithNotification(ctx context.Context, ctdbCh <-chan ctdbResult) (<-chan ctdbResult, <-chan parityResult) {
	ctdbNotifyCh := make(chan ctdbResult, 1)
	parityCh := make(chan parityResult, 1)

	go func() {
		// Wait for CTDB result
		ctdbRes := <-ctdbCh

		// Send notification immediately so caller can print "found CTDB ID"
		ctdbNotifyCh <- ctdbRes

		if ctdbRes.err != nil {
			parityCh <- parityResult{err: ctdbRes.err}
			return
		}

		// Start fetching parity (runs in parallel with remaining audio processing)
		httpClient := network.NewHTTPClient()
		ctdbClient := network.NewCTDBClient(httpClient)
		const maxParallelFetches = 4
		data := fetchParityParallel(ctx, ctdbClient, ctdbRes.resp.Entries, maxParallelFetches)

		parityCh <- parityResult{data: data}
	}()

	return ctdbNotifyCh, parityCh
}

// fetchParityParallel fetches parity for multiple entries concurrently.
// Returns a map of entry index to syndrome data.
func fetchParityParallel(ctx context.Context, client network.CTDBClient,
	entries []network.CTDBEntry, maxConcurrent int) map[int][][]uint16 {

	results := make(map[int][][]uint16)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrent) // limit concurrency

	for i, entry := range entries {
		if entry.HasParity == "" {
			continue
		}
		wg.Add(1)
		go func(idx int, e network.CTDBEntry) {
			defer wg.Done()
			sem <- struct{}{}        // acquire
			defer func() { <-sem }() // release

			syndrome, err := client.FetchParity(ctx, &e, e.Npar)
			if err == nil && syndrome != nil {
				mu.Lock()
				results[idx] = syndrome
				mu.Unlock()
			}
		}(i, entry)
	}
	wg.Wait()
	return results
}

// processCTDBResult handles the async CTDB lookup result.
// This is called after audio processing completes to process the pre-fetched CTDB response.
func processCTDBResult(ctx context.Context, layout toc.Layout, proc *accuraterip.Processor, result ctdbResult, verbose bool) error {
	tocID, tocErr := layout.TOCID()

	if result.err != nil {
		if result.err == network.ErrNotFound {
			// Print TOCID with "not found" status
			if tocErr != nil {
				fmt.Printf("[CTDB TOCID: (error: %v)] not found.\n", tocErr)
			} else {
				fmt.Printf("[CTDB TOCID: %s] not found.\n", tocID)
			}
			return nil
		}
		return result.err
	}

	return processCTDBResponse(ctx, layout, proc, result.resp, verbose)
}

// VerifyOptions holds parameters for a verify run.
type VerifyOptions struct {
	AudioPath        string
	Layout           toc.Layout
	CuePath          string
	DirPath          string // Directory path for auto-discovery mode
	Stride           int
	LastStride       int
	Npar             int
	CalcParity       bool
	QueryAR          bool // Query AccurateRip database
	QueryCTDB        bool // Query CTDB database
	Verbose          bool // Verbose output
	Debug            bool // Debug output for CRC comparison
	ShowProgress     bool // Show progress bar during verification
	SeparateDecoding bool // Use separate goroutine for decoding
}

// Verify runs a verification pass (decode PCM, compute CRCs/parity, query databases).
func Verify(ctx context.Context, opts VerifyOptions) error {
	// Print log header (CueTools format)
	fmt.Printf("[CTDBTools log; Date: %s; Version: %s]\n",
		time.Now().Format("2006/01/02 15:04:05"),
		version.Version)

	var layout toc.Layout
	var sheet ingest.CueSheet
	var useCueSheet bool
	var proc *accuraterip.Processor

	// Enable debug feed if debug mode is on
	if opts.Debug {
		accuraterip.SetDebugFeed(true)
	}

	if opts.Layout.AudioTracks > 0 {
		// Layout provided directly
		layout = opts.Layout
	} else if opts.DirPath != "" {
		// Auto-discover mode: scan directory for audio files
		var err error
		sheet, err = ingest.DiscoverDirectory(ctx, opts.DirPath)
		if err != nil {
			return fmt.Errorf("failed to discover audio files: %w", err)
		}
		layout = sheet.Layout
		useCueSheet = true
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
		return fmt.Errorf("path required (CUE file or directory)")
	}

	// Process audio
	if useCueSheet && sheet.IsSplitTrack() {
		// Split track mode: probe each file for accurate durations
		err := probeSplitTrackDurations(ctx, &sheet)
		if err != nil {
			return fmt.Errorf("failed to probe track durations: %w", err)
		}
		layout = sheet.Layout
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
		opts.AudioPath = audioPath

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
	}

	// Compute lastStride if not explicitly set (-1 sentinel means auto-compute).
	//
	// LastStride determines how many samples to exclude from the end of the last track.
	// It's computed dynamically to ensure the total verified data length is a multiple
	// of stride (required for CTDB parity alignment).
	//
	// CueTools formula (CDRepair.cs:29):
	//   laststride = stride + ((finalSampleCount - pregap) * 2) % stride
	//
	// Where:
	//   - stride = 588 * 10 * 2 = 11760 (10 CD frames × 2 channels, in 16-bit samples)
	//   - finalSampleCount = total stereo samples (32-bit values)
	//   - pregap = pregap of first audio track in samples
	//   - "* 2" converts stereo samples to 16-bit channel samples
	//
	// The suffix samples excluded = laststride / 2 (converted back to stereo samples)
	lastStride := opts.LastStride
	if lastStride < 0 {
		finalSampleCount := layout.AudioLengthFrames() * 588
		pregap := 0
		if len(layout.Tracks) > 0 {
			pregap = layout.Tracks[0].Pregap * 588
		}
		lastStride = opts.Stride + ((finalSampleCount-pregap)*2)%opts.Stride
	}

	// Start CTDB query in background (runs concurrently with audio decoding)
	// This overlaps network I/O with CPU-bound audio processing for ~2s savings
	var ctdbCh <-chan ctdbResult
	if opts.QueryCTDB {
		ctdbCh = startCTDBQuery(ctx, layout.TOCString())
	}

	// Create progress reporter if enabled
	var reporter *progress.Reporter
	if opts.ShowProgress {
		reporter = progress.NewReporter(
			progress.WithOutput(os.Stderr),
			progress.WithProgressBar(true),
		)
	}

	// Build processing options
	processOpts := ingest.ProcessOptions{
		Stride:           opts.Stride,
		LastStride:       lastStride,
		Npar:             opts.Npar,
		CalcParity:       opts.CalcParity,
		Progress:         reporter,
		SeparateDecoding: opts.SeparateDecoding,
	}

	// Now process audio with correct lastStride
	if useCueSheet && sheet.IsSplitTrack() {
		var err2 error
		proc, err2 = ingest.ProcessCueSheetWithProgress(ctx, sheet, processOpts)
		if err2 != nil {
			return err2
		}
	} else {
		var err error
		proc, err = ingest.ProcessFileWithProgress(ctx, opts.AudioPath, layout, processOpts)
		if err != nil {
			return err
		}
	}
	opts.LastStride = lastStride // update opts for debug output

	// Debug output: dump layout and CRC state
	if opts.Debug {
		tocID, err := layout.TOCID()
		if err != nil {
			fmt.Printf("TOCID: (error: %v)\n", err)
		} else {
			fmt.Printf("TOCID: %s\n", tocID)
		}
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

		// Debug CTDB CRC details
		for track := 1; track <= layout.AudioTracks; track++ {
			ctdbCRC := proc.TrackCTDBCRC(track, 0, opts.Stride, opts.LastStride)
			fmt.Printf("Track %02d CTDB CRC: %08X (stride=%d, laststride=%d)\n", track, ctdbCRC, opts.Stride, opts.LastStride)

			if track == 1 || track == layout.AudioTracks {
				printDebugCTDBCRC(proc, layout, track, opts.Stride, opts.LastStride)
			}
		}
		discCTDBCRC := proc.DiscCTDBCRC(0, opts.Stride, opts.LastStride)
		fmt.Printf("Disc CTDB CRC: %08X\n", discCTDBCRC)
		printDebugRollingTables(proc, layout.AudioTracks)

		if opts.CalcParity {
			syn := proc.Syndrome()
			fmt.Printf("\nSyndrome rows: %d\n", len(syn))
			if len(syn) > 0 {
				fmt.Printf("Local Syndrome[0]: %v\n", syn[0])
				fmt.Printf("Local Syndrome[1]: %v\n", syn[1])
			}
			parBuf := proc.Parity().State().ParityBuf
			fmt.Printf("ParityBuf[0:32]: %v\n", parBuf[:32])
		}
		fmt.Println()
	}

	// Query CTDB first (matches CueTools output order)
	// Wait for async CTDB result (started before audio processing)
	if opts.QueryCTDB && ctdbCh != nil {
		ctdbRes := <-ctdbCh
		if err := processCTDBResult(ctx, layout, proc, ctdbRes, opts.Verbose); err != nil {
			// Print TOCID with error status
			tocID, tocErr := layout.TOCID()
			if tocErr != nil {
				fmt.Printf("[CTDB TOCID: (error: %v)] database access error: %v.\n", tocErr, err)
			} else {
				fmt.Printf("[CTDB TOCID: %s] database access error: %v.\n", tocID, err)
			}
		}
	}

	// Query AccurateRip
	if opts.QueryAR {
		if err := queryAccurateRip(ctx, layout, proc, opts.Verbose); err != nil {
			// Print AccurateRip ID with error status
			arID, _ := layout.AccurateRipID()
			if arID != "" {
				fmt.Printf("\n[AccurateRip ID: %s] database access error: %v.\n", arID, err)
			} else {
				fmt.Printf("\nAccurateRip: database access error: %v.\n", err)
			}
		}
	}

	// Try to find and parse log file for LOG column
	var logData *logparse.LogData
	logDir := ""
	if opts.DirPath != "" {
		logDir = opts.DirPath
	} else if opts.CuePath != "" {
		logDir = filepath.Dir(opts.CuePath)
	}
	if logDir != "" {
		if logPath := logparse.FindLogFile(logDir); logPath != "" {
			logData, _ = logparse.ParseLogFile(logPath) // Ignore errors, LOG column is optional
		}
	}

	// Display Track CRC table at the end (CueTools format)
	printTrackCRCTable(proc, layout.AudioTracks, logData)

	return nil
}

// AccurateRip offset search range: ±(5*588-1) = ±2939 samples
// This matches CueTools _arOffsetRange constant.
const arOffsetRange = 5*588 - 1

// queryAccurateRip queries the AccurateRip database and displays results.
func queryAccurateRip(ctx context.Context, layout toc.Layout, proc *accuraterip.Processor, verbose bool) error {
	arID, err := layout.AccurateRipID()
	if err != nil {
		return fmt.Errorf("failed to compute AccurateRip ID: %w", err)
	}

	httpClient := network.NewHTTPClient()
	arClient := network.NewAccurateRipClient(httpClient)

	resp, err := arClient.Query(ctx, arID, layout.AudioTracks)
	if err != nil {
		if err == network.ErrNotFound {
			fmt.Printf("\n[AccurateRip ID: %s] not found.\n", arID)
			return nil
		}
		return err
	}

	fmt.Printf("\n[AccurateRip ID: %s] found.\n", arID)

	fmt.Printf("AccurateRip: Found %d pressing(s)\n", len(resp.Disks))

	// Compare our CRCs against database for each track
	// AccurateRip database stores either v1 or v2 CRC in the CRC field
	// We need to compare both our v1 and v2 against the database CRC
	// CueTools searches ±2939 samples for v1 matches, v2 only at offset 0
	fmt.Println("Track   [  CRC   |   V2   ] Status")
	for track := 1; track <= layout.AudioTracks; track++ {
		localV2 := proc.TrackCRCV2(track)

		// Check all pressings for a match
		// matchCount = matches on v1 (any offset), v2MatchCount = matches on v2 (offset 0 only)
		var matchCount, v2MatchCount, totalCount int
		for _, disk := range resp.Disks {
			if track-1 < len(disk.Tracks) {
				dbTrack := disk.Tracks[track-1]
				totalCount += int(dbTrack.Count)

				// Try V2 at offset 0 first (faster check)
				if dbTrack.CRC == localV2 {
					v2MatchCount += int(dbTrack.Count)
					continue
				}

				// Search all offsets for V1 match (CueTools behavior)
				// Check offset 0 first, then search ±arOffsetRange
				localAR0 := proc.TrackCRCARWithOffset(track, 0)
				if dbTrack.CRC == localAR0 {
					matchCount += int(dbTrack.Count)
					continue
				}

				// Full offset search
				matched := false
				for oi := -arOffsetRange; oi <= arOffsetRange && !matched; oi++ {
					if oi == 0 {
						continue // Already checked
					}
					localAR := proc.TrackCRCARWithOffset(track, oi)
					if dbTrack.CRC == localAR {
						matchCount += int(dbTrack.Count)
						matched = true
					}
				}
			}
		}

		// Display local CRC at offset 0 for reference
		localAR := proc.TrackCRCAR(track)
		status := "No match"
		if matchCount > 0 || v2MatchCount > 0 {
			status = fmt.Sprintf("(%02d+%02d/%d) Accurately ripped", matchCount, v2MatchCount, totalCount)
		}
		fmt.Printf(" %02d     [%08x|%08x] %s\n", track, localAR, localV2, status)
	}

	// Search for matching offsets across all tracks (CueTools verbose mode behavior)
	// First pass: Show "Offsetted by X:" for each offset where ALL tracks match
	offsetsFound := 0
	const maxOffsetsToShow = 16
	shownOffsets := make(map[int]bool)

	for oi := -arOffsetRange; oi <= arOffsetRange; oi++ {
		if oi == 0 {
			continue // Already shown in main results
		}

		// Check if ALL tracks match at this offset
		allTracksMatch := true
		trackResults := make([]struct {
			crc   uint32
			conf  int
			total int
		}, layout.AudioTracks)

		for track := 1; track <= layout.AudioTracks; track++ {
			localAR := proc.TrackCRCARWithOffset(track, oi)
			trackResults[track-1].crc = localAR

			// Check against all pressings
			matched := false
			for _, disk := range resp.Disks {
				if track-1 < len(disk.Tracks) {
					dbTrack := disk.Tracks[track-1]
					trackResults[track-1].total += int(dbTrack.Count)
					if dbTrack.CRC == localAR && dbTrack.CRC != 0 {
						trackResults[track-1].conf += int(dbTrack.Count)
						matched = true
					}
				}
			}
			if !matched {
				allTracksMatch = false
			}
		}

		if allTracksMatch {
			offsetsFound++
			shownOffsets[oi] = true
			if offsetsFound > maxOffsetsToShow {
				fmt.Println("More than 16 offsets match!")
				break
			}

			fmt.Printf("Offsetted by %d:\n", oi)
			for track := 1; track <= layout.AudioTracks; track++ {
				tr := trackResults[track-1]
				status := "Accurately ripped"
				if tr.conf == 0 {
					status = "No match"
				}
				fmt.Printf(" %02d     [%08x] (%02d/%d) %s\n", track, tr.crc, tr.conf, tr.total, status)
			}
		}
	}

	// Second pass: Show offsets with PARTIAL matches (some tracks match or have Frame450 partial matches)
	// This matches CueTools behavior (line 1086): matches != all && oi != 0 && (matches + partials) != 0
	for oi := -arOffsetRange; oi <= arOffsetRange; oi++ {
		if oi == 0 || shownOffsets[oi] {
			continue // Already shown
		}

		trackResults := make([]struct {
			crc   uint32
			conf  int
			total int
		}, layout.AudioTracks)

		matchingTracks := 0
		partialTracks := 0
		for track := 1; track <= layout.AudioTracks; track++ {
			localAR := proc.TrackCRCARWithOffset(track, oi)
			local450 := proc.TrackCRC450WithOffset(track, oi)
			trackResults[track-1].crc = localAR

			// Check against all pressings
			trackMatched := false
			for _, disk := range resp.Disks {
				if track-1 < len(disk.Tracks) {
					dbTrack := disk.Tracks[track-1]
					trackResults[track-1].total += int(dbTrack.Count)
					if dbTrack.CRC == localAR && dbTrack.CRC != 0 {
						trackResults[track-1].conf += int(dbTrack.Count)
						if !trackMatched {
							matchingTracks++
							trackMatched = true
						}
					}
					// Check Frame450 partial match (only count once per track)
					if !trackMatched && dbTrack.Frame450CRC == local450 && dbTrack.Frame450CRC != 0 {
						partialTracks++
						trackMatched = true // Don't count same track twice
					}
				}
			}
		}

		// Show if SOME tracks match or have partials (but not ALL full matches)
		if matchingTracks < layout.AudioTracks && (matchingTracks+partialTracks) > 0 {
			offsetsFound++
			if offsetsFound > maxOffsetsToShow {
				fmt.Println("More than 16 offsets match!")
				break
			}

			fmt.Printf("Offsetted by %d:\n", oi)
			for track := 1; track <= layout.AudioTracks; track++ {
				tr := trackResults[track-1]
				if tr.conf > 0 {
					fmt.Printf(" %02d     [%08x] (%02d/%d) Accurately ripped\n", track, tr.crc, tr.conf, tr.total)
				} else {
					fmt.Printf(" %02d     [%08x] (%02d/%d) No match (V2 was not tested)\n", track, tr.crc, tr.conf, tr.total)
				}
			}
		}
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

// trackErrorInfo stores per-track error information from parity verification.
type trackErrorInfo struct {
	confidence int
	errorCount int
	positions  string // formatted as MM:SS:FF-MM:SS:FF
}

// getTrackSampleRange returns the sample range [min, max) for a track in 16-bit samples.
// This matches CueTools convention where positions are relative to first audio track start.
func getTrackSampleRange(layout toc.Layout, track int) (min, max int) {
	firstTrackStart := layout.TrackStartFrame(1)
	trackStart := layout.TrackStartFrame(track)
	trackEnd := trackStart + layout.TrackLengthFrames(track)

	// Convert frames to 16-bit samples (1 frame = 588 stereo = 1176 16-bit)
	// CueTools uses: (tri.Start - tr0.Start) * 588 and (tri.End + 1 - tr0.Start) * 588
	// where values are in stereo samples, but parity uses 16-bit samples (* 2)
	min = (trackStart - firstTrackStart) * 588 * 2
	max = (trackEnd - firstTrackStart) * 588 * 2
	return min, max
}

// queryCTDB queries the CUETools Database and displays results.
//
// CTDB Verification Algorithm (matches CueTools CUEToolsDB.cs):
// 1. Query CTDB server with TOC string to get entries
// 2. For each entry, try 3-case confidence matching:
//    a) Case 1: Exact CRC match (!hasErrors) - add full confidence
//    b) Case 2: Recoverable entry (canRecover) - check per-track error boundaries
//    c) Case 3: No parity, has trackcrcs - search offsets for per-track CRC match
// 3. Aggregate matches and display per-track status with "differs" info
func queryCTDB(ctx context.Context, layout toc.Layout, proc *accuraterip.Processor, verbose bool) error {
	tocStr := layout.TOCString()
	tocID, tocErr := layout.TOCID()

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
			// Print TOCID with "not found" status
			if tocErr != nil {
				fmt.Printf("[CTDB TOCID: (error: %v)] not found.\n", tocErr)
			} else {
				fmt.Printf("[CTDB TOCID: %s] not found.\n", tocID)
			}
			return nil
		}
		return err
	}

	return processCTDBResponse(ctx, layout, proc, resp, verbose)
}

// processCTDBResponse processes a CTDB response and displays results.
// This is the core processing logic used by both queryCTDB (sync) and processCTDBResult (async).
func processCTDBResponse(ctx context.Context, layout toc.Layout, proc *accuraterip.Processor, resp *network.CTDBResponse, verbose bool) error {
	tocStr := layout.TOCString()
	tocID, tocErr := layout.TOCID()

	// Print TOCID with "found" status
	if tocErr != nil {
		fmt.Printf("[CTDB TOCID: (error: %v)] found.\n", tocErr)
	} else {
		fmt.Printf("[CTDB TOCID: %s] found.\n", tocID)
	}
	fmt.Printf("CTDB TOC: %s\n", tocStr)
	fmt.Printf("CTDB: Found %d entries (total confidence: %d)\n", len(resp.Entries), resp.Total)

	// Create CTDB client for parity fetches
	httpClient := network.NewHTTPClient()
	ctdbClient := network.NewCTDBClient(httpClient)

	// Per-track confidence tracking
	trackMatches := make([]int, layout.AudioTracks)     // Exact matches
	trackErrorInfos := make([]trackErrorInfo, layout.AudioTracks) // "differs" info per track
	totalConfidence := 0

	// Compute layout parameters
	finalSampleCount := layout.AudioLengthFrames() * 588
	pregap := 0
	if len(layout.Tracks) > 0 {
		pregap = layout.Tracks[0].Pregap * 588
	}

	// Offset cache to avoid redundant CRC-based offset searches
	// Key: entry CRC32, Value: detected offset
	type offsetCacheKey struct {
		crc        uint32
		stride     int
		laststride int
	}
	offsetCache := make(map[offsetCacheKey]int)

	// getCachedOffset retrieves offset from cache or computes it
	getCachedOffset := func(crc uint32, stride, laststride int) int {
		key := offsetCacheKey{crc, stride, laststride}
		if cached, ok := offsetCache[key]; ok {
			return cached
		}
		offset := findCTDBOffsetByCRC(proc, crc, stride, laststride)
		offsetCache[key] = offset
		return offset
	}

	// Track best match for displaying detected offset
	var bestOffset int
	var bestConfidence int

	// First pass: find the best offset from any matching entry
	// This offset will be used for all parity-based comparisons
	for _, entry := range resp.Entries {
		if len(entry.TrackCRCs) != layout.AudioTracks {
			continue
		}
		stride := entry.Stride
		laststride := stride + ((finalSampleCount-pregap)*2)%stride
		offset := getCachedOffset(entry.CRC32, stride, laststride)
		if proc.DiscCTDBCRC(offset, stride, laststride) == entry.CRC32 {
			if entry.Confidence > bestConfidence {
				bestOffset = offset
				bestConfidence = entry.Confidence
			}
		}
	}

	for _, entry := range resp.Entries {
		if len(entry.TrackCRCs) != layout.AudioTracks {
			continue // skip entries with wrong track count
		}

		stride := entry.Stride
		laststride := stride + ((finalSampleCount-pregap)*2)%stride

		// Offset detection is deferred until we know if we have syndrome data
		// (syndrome-based is much faster than CRC-based)
		var offset int
		var offsetDetected bool

		totalConfidence += entry.Confidence

		// Process entry based on available data
		// Priority: 1. Parity-based verification, 2. CRC matching, 3. Offset search

		entryProcessed := false

		// Parity-Based Error Detection
		// ============================
		// CTDB entries can include Reed-Solomon parity data that enables us to:
		// 1. Detect exact match (syndrome XOR = 0)
		// 2. Detect and locate errors (syndrome XOR != 0, but RS decoder finds roots)
		// 3. Report "differs in X samples @MM:SS:FF" for repairable errors
		//
		// The challenge is that CTDB entries may have been submitted at different
		// offsets than our local rip. We must search for the correct offset where
		// the RS decoder can successfully find all error positions.
		//
		// See docs/CTDB_ALGORITHM.md for detailed algorithm explanation.
		if entry.HasParity != "" && proc.Parity() != nil {
			ctdbSyndrome, err := ctdbClient.FetchParity(ctx, &entry, entry.Npar)
			if err == nil && ctdbSyndrome != nil {
				// Use fast syndrome-based offset detection (O(npar) per offset vs O(samples) for CRC)
				offset = findCTDBOffset(proc, ctdbSyndrome, stride, entry.Npar)
				offsetDetected = true
				// strideCount = number of data strides, excluding lead-in/out
				// The -2 accounts for first and last strides being excluded
				// This matches CueTools CDRepair.cs:30
				strideCount := ((finalSampleCount - pregap) * 2) / stride - 2
				pregap16bit := pregap * 2

				// Build list of offsets to try, in priority order:
				// 1. This entry's detected offset (from syndrome matching - already accurate)
				// 2. Best offset from any matching entry (handles cross-offset comparison)
				// 3. Offset 0 (most common case)
				offsetsToTry := []int{}
				if offset != 0 {
					offsetsToTry = append(offsetsToTry, offset)
				}
				if bestOffset != 0 && bestOffset != offset {
					offsetsToTry = append(offsetsToTry, bestOffset)
				}
				offsetsToTry = append(offsetsToTry, 0)

				// tryOffset attempts RS-based error detection at a specific offset.
				// Returns: (success, errorCount, errorPositions)
				// - success=true, errorCount=0: Perfect syndrome match (no errors)
				// - success=true, errorCount>0: Errors found and located
				// - success=false: RS decoder failed (wrong offset or too many errors)
				tryOffset := func(tryOff int) (bool, int, []int) {
					// Get local syndrome adjusted for this offset
					localSyn := proc.Parity().SyndromeWithOffset(tryOff, len(ctdbSyndrome))
					if localSyn == nil {
						return false, -1, nil
					}

					// XOR syndromes: if result is all zeros, perfect match
					xorSyn := parity.XORSyndromes(localSyn, ctdbSyndrome)
					if xorSyn == nil {
						return false, -1, nil // Syndromes incompatible
					}
					if parity.IsZeroSyndrome(xorSyn) {
						return true, 0, nil // Perfect match
					}

					// Use RS decoder to find error positions
					// This uses Berlekamp-Massey to find error locator polynomial,
					// then Chien search to find the roots (error positions)
					rs := parity.NewRsDecode(entry.Npar)
					errCount, errPositions := rs.DetectErrorsWithOffset(
						localSyn, ctdbSyndrome, stride, strideCount, pregap16bit, tryOff)
					return errCount > 0 && errPositions != nil, errCount, errPositions
				}

				// Try priority offsets first
				foundMatch := false
				var matchOffset int
				var errCount int
				var errPositions []int

				for _, tryOff := range offsetsToTry {
					if verbose {
						fmt.Printf("  DEBUG: Trying offset=%d for syndrome\n", tryOff)
					}
					success, ec, ep := tryOffset(tryOff)
					if success {
						foundMatch = true
						matchOffset = tryOff
						errCount = ec
						errPositions = ep
						if verbose {
							fmt.Printf("  DEBUG CTDB Entry (conf=%d): Success at offset=%d, errCount=%d\n",
								entry.Confidence, tryOff, ec)
						}
						break
					}
				}

				// If priority offsets didn't work, search the full ±arOffsetRange
				// This is necessary because CTDB entries may have been submitted by users
				// with completely different drive offsets. For example:
				// - Our rip: offset 0
				// - Entry 1: submitted at offset +667 (matches via CRC)
				// - Entry 2: submitted at offset -1252 (no CRC match, need to search)
				//
				// The RS decoder will only succeed at the correct offset, so we search
				// until we find an offset where ChienSearch finds all roots.
				if !foundMatch {
					for searchOff := -arOffsetRange; searchOff <= arOffsetRange; searchOff++ {
						// Skip offsets we already tried
						alreadyTried := false
						for _, tried := range offsetsToTry {
							if searchOff == tried {
								alreadyTried = true
								break
							}
						}
						if alreadyTried {
							continue
						}

						success, ec, ep := tryOffset(searchOff)
						if success {
							foundMatch = true
							matchOffset = searchOff
							errCount = ec
							errPositions = ep
							if verbose {
								fmt.Printf("  DEBUG CTDB Entry (conf=%d): Found at search offset=%d, errCount=%d\n",
									entry.Confidence, searchOff, ec)
							}
							break
						}
					}
				}

				if foundMatch {
					if errCount == 0 {
						// Perfect syndrome match
						for track := 0; track < layout.AudioTracks; track++ {
							trackMatches[track] += entry.Confidence
						}
					} else {
						// Entry is recoverable - check per-track error boundaries
						for track := 1; track <= layout.AudioTracks; track++ {
							trackMin, trackMax := getTrackSampleRange(layout, track)
							errorsInTrack := parity.GetAffectedSectorsCountWithBounds(
								errPositions, trackMin, trackMax,
								pregap16bit, stride, laststride, finalSampleCount, matchOffset)

							if errorsInTrack == 0 {
								trackMatches[track-1] += entry.Confidence
							} else {
								posStr := parity.FormatAffectedSectorsFiltered(errPositions, trackMin, trackMax, trackMin, 5880)
								if trackErrorInfos[track-1].errorCount == 0 {
									trackErrorInfos[track-1] = trackErrorInfo{
										confidence: entry.Confidence,
										errorCount: errorsInTrack,
										positions:  posStr,
									}
								} else {
									trackErrorInfos[track-1].confidence += entry.Confidence
								}
							}
						}
					}
					entryProcessed = true
				}
			}
		}

		if entryProcessed {
			continue
		}

		// Fall back to CRC-based offset detection if not already detected
		if !offsetDetected {
			offset = getCachedOffset(entry.CRC32, stride, laststride)
		}

		// Check CRC matching at found offset
		trackCRCMatches := make([]bool, layout.AudioTracks)
		allTracksMatch := true
		for track := 1; track <= layout.AudioTracks; track++ {
			localCRC := proc.TrackCTDBCRC(track, offset, stride, laststride)
			if localCRC == entry.TrackCRCs[track-1] {
				trackCRCMatches[track-1] = true
			} else {
				allTracksMatch = false
			}
		}

		if allTracksMatch {
			// All tracks match exactly - add confidence to all tracks
			for track := 0; track < layout.AudioTracks; track++ {
				trackMatches[track] += entry.Confidence
			}
			continue
		}

		// CRC-based offset search for entries without parity
		// CueTools: for entries without parity but with trackcrcs, search ±arOffsetRange
		if entry.TrackCRCs != nil {
			for track := 1; track <= layout.AudioTracks; track++ {
				if trackCRCMatches[track-1] {
					// Already matched at detected offset
					trackMatches[track-1] += entry.Confidence
					continue
				}

				// Try at detected offset first (with negation like CueTools)
				localCRC := proc.TrackCTDBCRC(track, -offset, stride, laststride)
				if localCRC == entry.TrackCRCs[track-1] {
					trackMatches[track-1] += entry.Confidence
					continue
				}

				// Search ±arOffsetRange for match
				matched := false
				for oi := -arOffsetRange; oi <= arOffsetRange && !matched; oi++ {
					localCRC := proc.TrackCTDBCRC(track, oi, stride, laststride)
					if localCRC == entry.TrackCRCs[track-1] {
						trackMatches[track-1] += entry.Confidence
						matched = true
					}
				}
			}
		}
	}

	// Check disc CRC match status
	discMatched := false
	for _, entry := range resp.Entries {
		if len(entry.TrackCRCs) != layout.AudioTracks {
			continue
		}
		stride := entry.Stride
		laststride := stride + ((finalSampleCount-pregap)*2)%stride
		offset := getCachedOffset(entry.CRC32, stride, laststride)
		if proc.DiscCTDBCRC(offset, stride, laststride) == entry.CRC32 {
			discMatched = true
			break
		}
	}

	// Display per-track CTDB verification status (CueTools format)
	fmt.Println("Track | CTDB Status")
	for track := 1; track <= layout.AudioTracks; track++ {
		matches := trackMatches[track-1]
		errInfo := trackErrorInfos[track-1]

		var status string
		if matches > 0 {
			status = fmt.Sprintf("(%d/%d) Accurately ripped", matches, totalConfidence)
			// Also show "differs" info if present for this track
			if errInfo.errorCount > 0 {
				status += fmt.Sprintf(", or (%d/%d) differs in %d samples @%s",
					errInfo.confidence, totalConfidence, errInfo.errorCount, errInfo.positions)
			}
		} else if errInfo.errorCount > 0 {
			// Only "differs" status
			status = fmt.Sprintf("(%d/%d) Differs in %d samples @%s",
				errInfo.confidence, totalConfidence, errInfo.errorCount, errInfo.positions)
		} else if totalConfidence > 0 {
			status = fmt.Sprintf("(0/%d) No match", totalConfidence)
		} else {
			status = "No entries to compare"
		}
		fmt.Printf(" %2d   | %s\n", track, status)
	}

	if discMatched {
		fmt.Printf("Disc CRC: Match")
		if bestOffset != 0 {
			fmt.Printf(" (detected offset: %d samples)", bestOffset)
		}
		fmt.Println()
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

// findCTDBOffsetByCRC searches for the matching drive offset by comparing disc CRC.
//
// CD drives have different read offsets (typically -600 to +700 samples). When a disc
// is ripped, the samples are shifted by this offset. CTDB stores CRCs computed at a
// reference offset (usually 0), and we need to find what offset makes our local CRC
// match the expected CRC.
//
// Algorithm (based on CueTools CDRepair.cs FindOffset):
//  1. Try offset 0 first (most common case - offset already corrected during rip)
//  2. Search from -(stride/2)+1 to (stride/2)-1
//  3. Return the first offset where disc CRC matches
//
// Parameters:
//   - proc: The processor with computed rolling CRCs
//   - expectedCRC: The disc CRC from CTDB entry
//   - stride: Parity stride in samples (typically 11760)
//   - laststride: Last stride computed from disc length
//
// Returns: The detected offset, or 0 if no match found
func findCTDBOffsetByCRC(proc *accuraterip.Processor, expectedCRC uint32, stride, laststride int) int {
	strideHalf := stride / 2

	// First try offset 0 (most common case - correct offset already applied during rip)
	if proc.DiscCTDBCRC(0, stride, laststride) == expectedCRC {
		return 0
	}

	// Search for matching offset in range [-(stride/2)+1, (stride/2)-1]
	// This range covers typical drive offsets (stride/2 = 5880 samples)
	for offset := 1 - strideHalf; offset < strideHalf; offset++ {
		if offset == 0 {
			continue // already checked
		}
		localCRC := proc.DiscCTDBCRC(offset, stride, laststride)
		if localCRC == expectedCRC {
			return offset
		}
	}

	return 0 // No match found (rip may have errors, or disc not in database at any offset)
}

// findCTDBOffset searches for the matching drive offset by comparing local syndrome first row with CTDB syndrome.
// Returns 0 if no syndrome data is available or no match is found.
// This implements the CueTools FindOffset algorithm: search offsets from -stride/2+1 to +stride/2-1.
// Uses fast single-row comparison (O(npar) per offset) instead of full syndrome (O(stride × npar²)).
func findCTDBOffset(proc *accuraterip.Processor, ctdbSyndrome [][]uint16, stride, npar int) int {
	parityState := proc.Parity()
	if parityState == nil || ctdbSyndrome == nil || len(ctdbSyndrome) == 0 {
		return 0
	}

	strideHalf := stride / 2
	ctdbFirstRow := ctdbSyndrome[0]

	// First try offset 0 (most common case)
	localRow := parityState.SyndromeFirstRow(0)
	if localRow != nil && firstRowMatch(localRow, ctdbFirstRow, npar) {
		return 0
	}

	// Search for matching offset in range [-(stride/2)+1, (stride/2)-1]
	for offset := 1 - strideHalf; offset < strideHalf; offset++ {
		if offset == 0 {
			continue
		}
		// CUETools uses -offset for syndrome lookup
		localRow := parityState.SyndromeFirstRow(-offset)
		if localRow == nil {
			continue
		}
		if firstRowMatch(localRow, ctdbFirstRow, npar) {
			return offset
		}
	}

	return 0 // No match found
}

// firstRowMatch checks if two syndrome first rows match (XOR is all zeros).
// This is O(npar) instead of O(stride × npar) for full syndrome match.
func firstRowMatch(local, ctdb []uint16, npar int) bool {
	for j := 0; j < npar && j < len(ctdb) && j < len(local); j++ {
		if local[j]^ctdb[j] != 0 {
			return false
		}
	}
	return true
}

// syndromeMatch checks if two syndromes match (XOR is all zeros)
func syndromeMatch(local, ctdb [][]uint16, npar int) bool {
	for i := 0; i < len(ctdb) && i < len(local); i++ {
		for j := 0; j < npar && j < len(ctdb[i]) && j < len(local[i]); j++ {
			if local[i][j]^ctdb[i][j] != 0 {
				return false
			}
		}
	}
	return true
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

// printTrackCRCTable displays the Track/CRC table in CueTools format.
// This includes Track Peak, CRC32, W/O NULL, and optionally LOG columns.
func printTrackCRCTable(proc *accuraterip.Processor, audioTracks int, logData *logparse.LogData) {
	// CueTools format: Track Peak [ CRC32  ] [W/O NULL] [  LOG   ]
	hasLogData := logData != nil && len(logData.Tracks) > 0
	if hasLogData {
		fmt.Println("\nTrack Peak [ CRC32  ] [W/O NULL] [  LOG   ]")
	} else {
		fmt.Println("\nTrack Peak [ CRC32  ] [W/O NULL]")
	}

	// Display disc-wide row first (CueTools format)
	discCRC := proc.TrackCRC(0, 0)
	discCRCWN := proc.TrackCRCWONULL(0, 0)
	discPeak := proc.TrackPeak(0)
	if hasLogData {
		logStatus := getLogStatus(discCRC, discCRCWN, &logData.Disc)
		fmt.Printf(" -- %5.1f [%08X] [%08X] %s\n", discPeak, discCRC, discCRCWN, logStatus)
	} else {
		fmt.Printf(" -- %5.1f [%08X] [%08X]\n", discPeak, discCRC, discCRCWN)
	}

	// Then per-track rows
	for track := 1; track <= audioTracks; track++ {
		crc32 := proc.TrackCRC(track, 0)
		crcwn := proc.TrackCRCWONULL(track, 0)
		peak := proc.TrackPeak(track)
		if hasLogData && track <= len(logData.Tracks) {
			logStatus := getLogStatus(crc32, crcwn, &logData.Tracks[track-1])
			fmt.Printf(" %02d %5.1f [%08X] [%08X] %s\n", track, peak, crc32, crcwn, logStatus)
		} else {
			fmt.Printf(" %02d %5.1f [%08X] [%08X]\n", track, peak, crc32, crcwn)
		}
	}
}

// getLogStatus returns the LOG column status comparing computed CRCs with log file
func getLogStatus(crc32, crcwn uint32, logTrack *logparse.TrackLogData) string {
	if logTrack == nil || (!logTrack.HasCRC32 && !logTrack.HasCRCWONULL) {
		return "          "
	}

	// Check if log CRC matches computed CRC32
	if logTrack.HasCRC32 && logTrack.CRC32 == crc32 {
		return "  CRC32   "
	}

	// Check if log CRC matches computed W/O NULL
	if logTrack.HasCRCWONULL && logTrack.CRCWONULL == crcwn {
		return " W/O NULL "
	}

	// If log has CRC but doesn't match, show the log CRC
	if logTrack.HasCRC32 {
		return fmt.Sprintf("[%08X]", logTrack.CRC32)
	}

	return "          "
}

// RepairOptions holds parameters for a repair run.
type RepairOptions struct {
	CuePath      string
	DirPath      string
	OutputDir    string
	Stride       int
	Npar         int
	Auto         bool
	DryRun       bool
	Force        bool
	Verbose      bool
	ShowProgress bool
}

// Repair runs the repair process on an audio file using CTDB parity data.
func Repair(ctx context.Context, opts RepairOptions) error {
	fmt.Printf("[CTDBTools repair; Date: %s; Version: %s]\n",
		time.Now().Format("2006/01/02 15:04:05"),
		version.Version)

	var layout toc.Layout
	var sheet ingest.CueSheet
	var useCueSheet bool
	var proc *accuraterip.Processor

	// Parse input
	if opts.DirPath != "" {
		var err error
		sheet, err = ingest.DiscoverDirectory(ctx, opts.DirPath)
		if err != nil {
			return fmt.Errorf("failed to discover audio files: %w", err)
		}
		layout = sheet.Layout
		useCueSheet = true
	} else if opts.CuePath != "" {
		var err error
		sheet, err = ingest.ParseCueSheetFile(opts.CuePath)
		if err != nil {
			return fmt.Errorf("failed to parse CUE: %w", err)
		}
		layout = sheet.Layout
		useCueSheet = true
	} else {
		return fmt.Errorf("path required (CUE file or directory)")
	}

	// Handle split tracks vs single file
	isSplitTrack := useCueSheet && sheet.IsSplitTrack()

	if isSplitTrack {
		// Split track mode: probe each file for accurate durations
		err := probeSplitTrackDurations(ctx, &sheet)
		if err != nil {
			return fmt.Errorf("failed to probe track durations: %w", err)
		}
		layout = sheet.Layout
	} else {
		// Single file mode - get audio path
		var audioPath string
		if useCueSheet && len(sheet.Sources) > 0 {
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
			if len(layout.Tracks) > 0 {
				lastIdx := len(layout.Tracks) - 1
				lastTrack := &layout.Tracks[lastIdx]
				lastTrack.Length = frames - lastTrack.Start
				layout.Leadout = frames
			}
		}
	}

	// Compute lastStride
	finalSampleCount := layout.AudioLengthFrames() * 588
	pregap := 0
	if len(layout.Tracks) > 0 {
		pregap = layout.Tracks[0].Pregap * 588
	}
	lastStride := opts.Stride + ((finalSampleCount-pregap)*2)%opts.Stride

	// Create sample cache if not dry-run (to avoid second FFmpeg pass during correction)
	var sampleCache *ingest.SampleCache
	if !opts.DryRun {
		sampleCache = ingest.NewSampleCache(int64(finalSampleCount))
	}

	// Start CTDB query AND parity fetch in background (chained)
	// This overlaps network I/O with CPU-bound audio processing
	// Parity fetching starts automatically as soon as CTDB lookup completes
	// We get two channels: one for immediate CTDB notification, one for parity data
	ctdbCh := startCTDBQuery(ctx, layout.TOCString())
	ctdbNotifyCh, parityCh := startParityFetchWithNotification(ctx, ctdbCh)

	// Create progress reporter
	var reporter *progress.Reporter
	if opts.ShowProgress {
		reporter = progress.NewReporter(
			progress.WithOutput(os.Stderr),
			progress.WithProgressBar(true),
		)
	}

	// Process audio (parity fetch runs in parallel!)
	processOpts := ingest.ProcessOptions{
		Stride:           opts.Stride,
		LastStride:       lastStride,
		Npar:             16, // Use max npar for parity calculation
		CalcParity:       true,
		Progress:         reporter,
		SeparateDecoding: true,
		CacheSamples:     sampleCache != nil,
		SampleCache:      sampleCache,
	}

	var err error
	if isSplitTrack {
		proc, err = ingest.ProcessCueSheetWithProgress(ctx, sheet, processOpts)
	} else {
		audioPath := sheet.Sources[0].FilePath
		if audioPath != "" && sheet.CueDir != "" {
			audioPath = sheet.CueDir + "/" + audioPath
		}
		proc, err = ingest.ProcessFileWithProgress(ctx, audioPath, layout, processOpts)
	}
	if err != nil {
		return fmt.Errorf("failed to process audio: %w", err)
	}

	// Get CTDB result (should be ready by now, or very soon)
	tocID, tocErr := layout.TOCID()

	ctdbRes := <-ctdbNotifyCh
	if ctdbRes.err != nil {
		if ctdbRes.err == network.ErrNotFound {
			if tocErr != nil {
				fmt.Printf("[CTDB TOCID: (error: %v)] not found.\n", tocErr)
			} else {
				fmt.Printf("[CTDB TOCID: %s] not found.\n", tocID)
			}
			return fmt.Errorf("no CTDB entries found for this disc")
		}
		return fmt.Errorf("CTDB lookup failed: %w", ctdbRes.err)
	}
	resp := ctdbRes.resp

	// Print "found" immediately - parity may still be fetching
	if tocErr != nil {
		fmt.Printf("[CTDB TOCID: (error: %v)] found.\n", tocErr)
	} else {
		fmt.Printf("[CTDB TOCID: %s] found.\n", tocID)
	}

	// Wait for parity data (may already be complete)
	fmt.Print("Fetching parity data...")
	parityRes := <-parityCh
	fmt.Print("\r                       \r") // Clear the message
	if parityRes.err != nil {
		return fmt.Errorf("failed to fetch parity: %w", parityRes.err)
	}
	parityData := parityRes.data

	// Filter entries with parity data
	var candidates []*repair.RepairCandidate
	for i, entry := range resp.Entries {
		if entry.HasParity == "" {
			continue
		}

		// Get pre-fetched parity data
		ctdbSyndrome, ok := parityData[i]
		if !ok || ctdbSyndrome == nil {
			if opts.Verbose {
				fmt.Printf("Skipping entry %d: failed to fetch parity\n", i+1)
			}
			continue
		}

		// Analyze entry
		entryCopy := entry
		candidate, err := repair.AnalyzeEntry(proc, &entryCopy, ctdbSyndrome, layout, i)
		if err != nil {
			continue
		}

		candidates = append(candidates, candidate)
	}

	if len(candidates) == 0 {
		return fmt.Errorf("no CTDB entries with parity data found")
	}

	fmt.Printf("Found %d CTDB entries with parity:\n\n", len(candidates))

	// Display candidates
	fmt.Println("  #  Confidence  Errors  Error Positions")
	for i, c := range candidates {
		errStr := "0"
		if c.ErrorCount > 0 {
			errStr = fmt.Sprintf("%d", c.ErrorCount)
		} else if c.ErrorCount < 0 {
			errStr = "?"
		}

		positions := c.ErrorPositions
		if positions == "" {
			positions = "(no errors)"
		}

		fmt.Printf("  %d  %-10d  %-6s  %s\n", i+1, c.Entry.Confidence, errStr, positions)
	}
	fmt.Println()

	// Select candidate
	var selected *repair.RepairCandidate
	if opts.Auto {
		// Auto-select: prefer no errors, then highest confidence
		for _, c := range candidates {
			if c.ErrorCount == 0 {
				selected = c
				break
			}
		}
		if selected == nil {
			// No perfect match, select first correctable entry
			for _, c := range candidates {
				if c.CanRepair {
					selected = c
					break
				}
			}
		}
		if selected != nil {
			fmt.Printf("Auto-selected entry #%d (confidence %d)\n", selected.Index+1, selected.Entry.Confidence)
		}
	} else {
		// Interactive selection
		selected = selectRepairCandidate(candidates)
	}

	if selected == nil {
		return fmt.Errorf("no suitable entry selected")
	}

	// Check if repair is needed
	if selected.ErrorCount == 0 {
		fmt.Println("\nNo errors detected - file matches CTDB perfectly!")
		return nil
	}

	if !selected.CanRepair {
		return fmt.Errorf("errors cannot be corrected (too many errors per stride row)")
	}

	// Get parity data for selected entry (already fetched in parallel)
	ctdbSyndrome := parityData[selected.Index]
	if ctdbSyndrome == nil {
		return fmt.Errorf("parity data not available for selected entry")
	}

	// Execute repair
	fmt.Printf("\nRepairing %d errors using entry #%d (confidence %d)...\n\n",
		selected.ErrorCount, selected.Index+1, selected.Entry.Confidence)

	// Determine input path for repair
	inputPath := opts.CuePath
	if inputPath == "" {
		inputPath = opts.DirPath
	}

	// Extract source file paths for preserving filenames in split-track mode
	var sourceFiles []string
	for _, src := range sheet.Sources {
		sourceFiles = append(sourceFiles, src.FilePath)
	}

	// Use sheet.CuePath if opts.CuePath is empty (directory mode discovery)
	originalCuePath := opts.CuePath
	if originalCuePath == "" && sheet.CuePath != "" {
		originalCuePath = sheet.CuePath
	}

	repairOpts := repair.RepairOptions{
		InputPath:       inputPath,
		OutputDir:       opts.OutputDir,
		Layout:          layout,
		Stride:          selected.Entry.Stride,
		Npar:            selected.Entry.Npar,
		Auto:            opts.Auto,
		DryRun:          opts.DryRun,
		Force:           opts.Force,
		Verbose:         opts.Verbose,
		ShowProgress:    opts.ShowProgress,
		Reporter:        reporter,
		SampleCache:     sampleCache,
		OriginalCuePath: originalCuePath,
		IsSplitTrack:    isSplitTrack,
		SourceFiles:     sourceFiles,
	}

	result, err := repair.Execute(ctx, proc, selected.Entry, ctdbSyndrome, layout, repairOpts)
	if err != nil {
		return fmt.Errorf("repair failed: %w", err)
	}

	// Display track results
	fmt.Println("Track | Status")
	fmt.Println("----- | ------")
	for _, tr := range result.TrackResults {
		status := "OK (no errors)"
		if tr.ErrorCount > 0 {
			status = fmt.Sprintf("Repaired %d samples @%s", tr.ErrorCount, tr.Positions)
		}
		fmt.Printf(" %02d   | %s\n", tr.Track, status)
	}

	if opts.DryRun {
		fmt.Println("\n(dry-run mode - no files written)")
		return nil
	}

	// Write output files
	outputFiles, err := repair.WriteOutputFiles(opts.OutputDir, result, layout, repairOpts)
	if err != nil {
		return fmt.Errorf("failed to write output files: %w", err)
	}

	fmt.Printf("\nOutput files written to %s:\n", opts.OutputDir)
	if len(outputFiles.WAVPaths) > 1 {
		// Split-track mode: show all WAV files
		for _, wavPath := range outputFiles.WAVPaths {
			fmt.Printf("  - %s\n", filepath.Base(wavPath))
		}
	} else {
		// Single-file mode
		fmt.Printf("  - %s\n", filepath.Base(outputFiles.WAVPath))
	}
	fmt.Printf("  - %s\n", filepath.Base(outputFiles.CUEPath))
	fmt.Printf("  - %s\n", filepath.Base(outputFiles.LogPath))

	fmt.Printf("\nRepair complete. Verify with: ctdbtools verify %s/%s\n",
		opts.OutputDir, filepath.Base(outputFiles.CUEPath))

	return nil
}

// selectRepairCandidate prompts user to select a repair candidate.
func selectRepairCandidate(candidates []*repair.RepairCandidate) *repair.RepairCandidate {
	if len(candidates) == 0 {
		return nil
	}
	if len(candidates) == 1 {
		return candidates[0]
	}

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("Select entry for repair (1-%d): ", len(candidates))
		input, err := reader.ReadString('\n')
		if err != nil {
			return nil
		}

		input = strings.TrimSpace(input)
		idx, err := strconv.Atoi(input)
		if err != nil || idx < 1 || idx > len(candidates) {
			fmt.Printf("Invalid selection. Enter a number between 1 and %d.\n", len(candidates))
			continue
		}

		return candidates[idx-1]
	}
}
