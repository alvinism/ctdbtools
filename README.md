# CTDB Tool (macOS CLI)

Goal: Go-based CLI that mirrors CUETools verify/repair on macOS using CTDB and AccurateRip. Uses ffmpeg for decoding and ports CUETools algorithms (TOCID, AccurateRip/CTDB CRCs, parity logic).

## Layout
- `cmd/ctdbtools`: CLI entrypoint(s).
- `internal/cli`: argument parsing, command wiring.
- `internal/toc`: TOC/CUE parsing, TOCID/AccurateRip IDs.
- `internal/hashes`: CRC/AccurateRip/CTDB hashing + parity helpers.
- `internal/accuraterip`: AccurateRip querying + matching logic.
- `internal/ctdb`: CTDB query/submit/repair logic.
- `internal/audio`: ffmpeg wrappers and PCM handling.
- `docs`: plan, progress, research notes.

## Status
Work in progress; core algorithms and CLI wiring not yet implemented.
