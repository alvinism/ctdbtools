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

**Next steps**: CTDB/AccurateRip network clients, CLI framework with standard `flag` package.
