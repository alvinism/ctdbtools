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
}

// Verify runs a verification pass (decode PCM, compute CRCs/parity, query databases).
func Verify(ctx context.Context, opts VerifyOptions) error {
	layout := opts.Layout
	if layout.AudioTracks == 0 {
		if opts.CuePath == "" {
			return fmt.Errorf("layout or cue path required")
		}
		var err error
		layout, err = ingest.ParseCueFile(opts.CuePath)
		if err != nil {
			return err
		}
	}

	// Process audio and compute CRCs/parity
	proc, err := ingest.ProcessFile(ctx, opts.AudioPath, layout, opts.Stride, opts.LastStride, opts.Npar, opts.CalcParity)
	if err != nil {
		return err
	}

	// Display computed CRCs
	fmt.Printf("CRC (track1, offset0): %08x\n", proc.CRC(0))
	if opts.CalcParity {
		fmt.Printf("Syndrome rows: %d\n", len(proc.Syndrome()))
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

	fmt.Printf("\nAccurateRip ID: %s\n", arID)

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

	// Compare our CRCs against database
	localCRC := proc.CRC(0)
	for i, disk := range resp.Disks {
		if verbose || i == 0 {
			fmt.Printf("  Pressing %d: %d tracks\n", i+1, disk.TrackCount)
		}
		for j, track := range disk.Tracks {
			match := ""
			if uint32(j) < uint32(layout.AudioTracks) {
				// For first track, compare with our computed CRC
				if j == 0 && track.CRC == localCRC {
					match = " [MATCH]"
				}
			}
			if verbose {
				fmt.Printf("    Track %d: CRC=%08X count=%d%s\n", j+1, track.CRC, track.Count, match)
			} else if match != "" {
				fmt.Printf("  Track %d: CRC match! (confidence=%d)\n", j+1, track.Count)
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

	// Display entries
	for i, entry := range resp.Entries {
		if verbose || i == 0 {
			fmt.Printf("  Entry %d: CRC32=%08X confidence=%d npar=%d stride=%d\n",
				i+1, entry.CRC32, entry.Confidence, entry.Npar, entry.Stride)
			if len(entry.TrackCRCs) > 0 && verbose {
				fmt.Printf("    Track CRCs: ")
				for _, crc := range entry.TrackCRCs {
					fmt.Printf("%08X ", crc)
				}
				fmt.Println()
			}
			if entry.HasParity != "" && verbose {
				fmt.Printf("    Parity available: %s\n", entry.HasParity)
			}
		}
	}

	// Display metadata if present
	if verbose && len(resp.Metadata) > 0 {
		meta := resp.Metadata[0]
		fmt.Printf("  Metadata: %s - %s (%s)\n", meta.Artist, meta.Album, meta.Year)
	}

	return nil
}
