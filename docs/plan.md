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

## Detailed next steps (Algorithms & Ingestion)
- Parity window rules: enforce lead-in = pregap*588, lead-out = lastStride; parity covers only data window, tail parity uses final lastStride window; add stride-misalignment test (length not multiple of stride).
- Offset-aware syndromes: ensure SyndromeWithOffset applies leadin/leadout corrections per offset; test multi-track with pregap and offset differences.
- Rolling CRC/parity coherence: multi-track synthetic test asserting CRC/CRCWONULL differ per offset and parity stays zero in skipped regions; add CTDB CRC once implemented.
- Ingestion wiring: replace minimal cue parser with robust parser; wire ffmpeg->processor (per-track feed) in ingest; expose processor results via CLI verify scaffold (CTDB/AR network hooks TODO).

## Media ingestion/CLI steps (upcoming)
- [ ] Cue parser + layout builder.
- [ ] ffmpeg wrapper to emit 16-bit stereo PCM at 44.1kHz; slice per track respecting pregap/leadout.
- [ ] Wire verify/repair commands to use accumulators and CTDB/AR clients.

Progress is tracked in `docs/progress.md`; this plan will be updated as milestones complete.
