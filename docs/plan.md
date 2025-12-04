# Plan (Detailed)

Goal: Go CLI that replicates CUETools verify/repair (CTDB + AccurateRip) on macOS.

## Phases

1) Repo & Docs
- Module setup, directory layout, high-level plan/progress tracking. **Done**

2) Core Algorithms (CUETools parity/fidelity)
- TOC models + IDs (TOCID, CDDB, AccurateRip IDs). **Done**
- AccurateRip/CTDB CRC math: rolling CRC tables, offset handling, CRCWONULL, CTDB CRC. **In progress**
- Parity/RS: port CUETools Galois16, ParityToSyndrome, encode tables, stride/lead-in/out handling. **In progress**

3) Media ingestion
- Cue parsing, file grouping, track layouts.
- PCM decode via ffmpeg (pipe), feeding rolling/parity accumulators.

4) Network interactions
- CTDB query/submit (HTTP), AccurateRip query; caching.

5) CLI
- Commands: `verify` (ctdb+AR), `repair` (ctdb parity), config flags (ffmpeg path, temp dir, offsets).

6) Tests & polish
- Synthetic vectors for CRC/parity, offset cases; basic integration tests over small fixtures.
- Logging, progress output, docs.

## Detailed next steps (Algorithms)
- [ ] Rolling CRC fill: feed per-track data into rolling tables from PCM; ensure Cache use matches CUETools.
- [ ] Offset CRC getters: verify against synthetic cases (done for CRC/CRCWONULL basic) and expand coverage (lead-in/out, multi-track).
- [ ] Parity integration: hook ParityAggregator into rolling feed with stride/laststride/lead-in/out, mirroring AccurateRipVerify.CalculateCRCs parity path.
- [ ] Add tests: offset CRC/CRCWONULL across tracks; parity round-trips with lead-in/out; CTDB CRC edge cases.
- [ ] Prepare ingestion scaffold: interface to stream PCM frames per track into rolling/parity accumulators.

## Media ingestion/CLI steps (upcoming)
- [ ] Cue parser + layout builder.
- [ ] ffmpeg wrapper to emit 16-bit stereo PCM at 44.1kHz; slice per track respecting pregap/leadout.
- [ ] Wire verify/repair commands to use accumulators and CTDB/AR clients.

Progress is tracked in `docs/progress.md`; this plan will be updated as milestones complete.
