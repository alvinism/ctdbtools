# Plan

Phases to replicate CUETools verify/repair in Go:

1) Repo scaffolding
- Define module, directories, and docs for plan/progress.
- Establish coding/testing conventions.

2) Research + algorithm porting
- Extract TOCID, AccurateRip IDs/CRCs, CTDB CRC/parity math from CUETools source.
- Document findings and produce parity/CRC test vectors.

3) Core library implementation
- Implement TOC model + ID calculators.
- Implement AccurateRip/CTDB CRC + parity generation/validation with tests mirroring CUETools.
- Add CTDB/AR network client stubs (no calls until wired).

4) Media ingestion
- Parse cuesheets and scan audio files; normalize to PCM via ffmpeg (pipe).
- Track-aware PCM iteration respecting offsets/pregap.

5) CLI surface
- `verify` command: read cue/audio, compute IDs/CRCs, query CTDB/AccurateRip, print report.
- `repair` command: fetch parity, patch audio where possible, output corrected files.
- Config for paths, offsets, temp dirs, ffmpeg binary.

6) Polish
- Logging, progress reporting, cache, packaging.
- Add docs and examples.

State will be tracked in `docs/progress.md`.
