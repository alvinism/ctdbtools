# Progress Log

## 2024-05-27
- Initialized Go module scaffolding and directory layout for CLI/library split.
- Added planning document with phased roadmap.
- Began source reading: located TOCID computation in `CUETools.CDImage/CDImage.cs` (SHA1 over track offsets) and identified AccurateRip/CTDB verification code paths in `CUETools.AccurateRip/AccurateRip.cs` and `CUETools.CTDB/CUEToolsDB.cs` for future porting.
- Implemented TOC model and ported TOCID/CDDB/AccurateRip ID calculators into `internal/toc` (matching CUETools logic).
- Added CRC utilities (`internal/hashes`) including zlib-style `Combine`, AR CRC helpers, and a zero-offset AccurateRip track calculator scaffold with unit test; offset-aware/CTDB parity work still pending.
- Added neighbor-aware offset CRC calculators to mirror CUETools offset handling (TrackWindow with prefix/suffix) and tests for offset shifts.
- Implemented CTDB CRC computation over track windows with offset/prefix/suffix trimming plus tests; still computing directly rather than via precomputed tables but matches expected window behavior.
- Introduced parity scaffolding (`internal/parity`): GF(2^16) arithmetic (poly 0x1100b), encode table builder, and basic syndrome generator to mirror CUETools parity path; tests added.
- Ported CUETools parity Galois helpers and ParityToSyndrome conversion (syndrome <-> parity) into `internal/parity`, replacing earlier stubs.
- Added rolling CRC offset calculators and tests, plus parity aggregation scaffolding (stride-based parity state and syndromes) in `internal/accuraterip`; full lead-in/out parity handling still pending.
- Created detailed plan in `docs/plan.md` to track remaining algorithm and ingestion/CLI work. Started parity feed lead-in/out skipping; processor scaffolding combines rolling CRC and parity.
- Added lead-in/out-aware parity aggregator with tail stride support and offset-adjusted syndromes (mirrors CUETools leadin/leadout adjustments), with tests kept green.
- Added multi-track parity/CRC tests and initial ingestion scaffold: ffmpeg PCM decoder and minimal cue parser.
- Added more parity offset/lead-out tests plus PCM ingestion runner and CLI verify scaffold (no CTDB/AR network yet).
- Current focus: tighten parity window rules and offset corrections exactly like CUETools CalculateCRCs/GetSyndrome (including leadin/leadout adjustments for offsets, stride/laststride windows across tracks); expand synthetic multi-track tests; replace minimal cue parser and wire ffmpeg→processor into CLI verify; CTDB/AR network still pending.

## 2024-12-04 (Parity Fidelity Improvements)
- Refactored `ParityState` to match CueTools `AccurateRipVerify` parity behavior exactly:
  - Added `pregap`, `finalSampleCount`, and `sampleCount` fields for tracking sample position
  - Computed `strideCount = (finalSampleCount - pregap*588) * 2 / stride` to match CueTools
  - Parity window now uses CueTools logic: `doParity = currentStride >= 1 && currentStride <= stridecount`
  - `currentSample = sampleCount - pregap*588` (can be negative during pregap)
  - `currentStride = (currentSample * 2) / stride`
- Fixed lead-in/lead-out buffer indexing to match CueTools word-based approach:
  - Lead-in: `index = currentSample*2 + wordOffset`
  - Lead-out: `index = (finalSampleCount - sampleCount)*2 - wordOffset - 1`
- Updated `ParityAggregator` to use new `NewParityState(stride, npar, pregap, finalSampleCount)` signature
- Updated `Processor` to compute pregap and finalSampleCount from layout and pass to parity aggregator
- `AddSamples()` now processes both left/right channels correctly (lo word at currentPart, hi word at currentPart+1)
- Simplified `FeedSamples()` in `ParityAggregator` - parity window logic moved to `ParityState.AddSamples()`
- All tests updated and passing with new API

**Key Reference**: CueTools `AccurateRip.cs` lines 493-547 (CalculateCRCs), 611-618 (Write), 317-352 (GetSyndrome)

## 2024-12-04 (Network Clients)
- Created `internal/network/` package with CTDB and AccurateRip HTTP clients
- Implemented AccurateRip client (`accuraterip.go`):
  - URL construction matching CueTools format: `http://www.accuraterip.com/accuraterip/{d1&0xF}/{d1>>4&0xF}/{d1>>8&0xF}/dBAR-{tracks:03d}-{d1}-{d2}-{cddb}.bin`
  - Binary response parsing: 13-byte disk header + 9-byte track entries (LE uint32)
  - Rate limiting: 500ms minimum between requests per AccurateRip guidelines
- Implemented CTDB client (`ctdb.go`):
  - Lookup URL: `http://db.cuetools.net/lookup2.php?version=3&ctdb={0|1}&fuzzy={0|1}&metadata={level}&toc={toc}`
  - XML response parsing with entry and metadata extraction
  - Syndrome/parity handling via existing `internal/parity` package
  - Range request support for incremental parity fetching
- Added base HTTP client (`client.go`) with configurable timeouts and user-agent
- Added shared types (`types.go`): ARTrack, ARDisk, ARResponse, CTDBEntry, CTDBResponse, etc.
- Added `TOCString()` method to `internal/toc/toc.go` for CTDB queries
- Integrated network clients into CLI verify command (`internal/cli/commands.go`):
  - New options: `QueryAR`, `QueryCTDB`, `Verbose`
  - Displays AccurateRip ID, queries database, compares CRCs
  - Displays CTDB entries with confidence scores
- Full test coverage for binary/XML parsing and HTTP client integration

**Key Reference**: CueTools `AccurateRip.cs` lines 829-903 (URL/response), `CUEToolsDB.cs` lines 77-83 (lookup URL)

## 2024-12-04 (CLI Wiring Complete)
- Wired CLI entry point using Cobra framework (`internal/cli/execute.go`)
- Added `github.com/spf13/cobra` dependency
- Implemented `verify` subcommand with flags:
  - `-c, --cue`: Path to CUE file (required)
  - `--ar`: Query AccurateRip database
  - `--ctdb`: Query CTDB database
  - `-v, --verbose`: Verbose output
  - `--parity`: Calculate parity/syndrome
  - `--stride`, `--last-stride`, `--npar`: Parity parameters
- Added signal handling for graceful cancellation
- Added file existence validation

**Verify feature is now complete and ready for real-world testing.**

## 2024-12-04 (CTDB Verification Display)
- Added `CTDBCRCWithOffset()` to `internal/accuraterip/rolling.go` for CTDB-style CRC with prefix/suffix skipping
- Added `TrackCTDBCRC()` and `DiscCTDBCRC()` to processor for easy CTDB CRC computation
- Added `TrackStartFrame()` helper to `internal/toc/toc.go`
- Updated `queryCTDB()` in CLI to display per-track verification status against CTDB entries
- Aggregates match counts across all CTDB entries with confidence weighting

**Test Results:**
- AccurateRip: Works perfectly on all test albums (V6, LAST LOVE)
- CTDB: Middle tracks verified correctly on LAST LOVE (tracks 2-12 all 7/7)
- Known limitation: First/last track CTDB CRC calculations need further investigation
  - Last track suffix handling may have edge case issues
  - V6 CTDB entries appear to be from different source rips (none match our audio)

## 2024-12-05 (Split Track Support)
- Implemented full split track support for CUE sheets with multiple FILE directives
- Created `internal/ingest/source.go`:
  - `SourceSegment` struct: FilePath, Offset, Length for audio segments
  - `CueSheet` struct: embeds Layout with CueDir, Sources, and AudioLayout
  - Helper methods: `IsSplitTrack()`, `SingleFilePath()`, `GetAudioLayout()`
- Created `internal/ingest/multisource.go`:
  - `MultiSourceReader`: sequentially reads audio from multiple files
  - Automatically switches FFmpeg streams when source length exhausted
  - Handles offset skipping for tracks starting mid-file
- Updated `internal/ingest/cue.go`:
  - Added `ParseCueSheet()` and `ParseCueSheetFile()` functions
  - Parses FILE directives and builds source mappings
  - Handles INDEX 00/01 positions for track boundaries
- Updated `internal/ingest/orchestrator.go`:
  - Added `ProcessCueSheet()` for both single-file and split-track CUEs
  - Uses AudioLayout for CRC calculation with proper track lengths
- Updated `internal/cli/commands.go`:
  - Modified `Verify()` to use CueSheet parsing
  - Added `probeSplitTrackDurations()` to probe file durations with ffprobe
  - Audio file argument now optional for split track CUEs
- Updated `internal/cli/execute.go`:
  - Changed args from `ExactArgs(1)` to `MaximumNArgs(1)` for split tracks

**Key Technical Insight**: For split track CUEs, CueTools uses the ENTIRE FILE for each track's CRC calculation, including any embedded pregap at the end of the file (the pregap for the next track). Track boundaries in the CUE (INDEX 00) don't affect CRC calculation - each file is processed in full.

**Test Results:**
- Split track CUE (希望子午線 〜ホライズン・ブルー〜): All 4 tracks verified against AccurateRip ✓
- Single-file CUEs still work correctly (V6, LAST LOVE albums verified) ✓
- CTDB: Tracks 1-3 match, track 4 and disc CRC have known last-track issues

## Roadmap

1. **[CURRENT] Fix CTDB first/last track CRC** - Investigate prefix/suffix calculation discrepancy
2. **[DONE] Add split-track album support** - Handle multi-FILE CUE sheets ✓
3. **[FUTURE] Implement Repair** - Reed-Solomon error correction
   - Port `calcSigmaMBM()` (Berlekamp-Massey) from `RsDecode.cs`
   - Port `chienSearch()` for error locations
   - Port `doForney()` for error magnitudes
   - Implement CDRepairFix equivalent
