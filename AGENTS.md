# AGENTS.md

Instructions for AI coding agents working in this repository.

## Project Overview

Podcaster (`github.com/tvaroska/podcaster`) is a split-plane private podcast platform for AI agents (Go 1.26, `CGO_ENABLED=0`):
- **Control plane (`cmd/server`)**: REST API (`/v1/*`), Streamable HTTP MCP server (`/mcp`), authenticated RSS 2.0 + iTunes + Podcasting 2.0 / Podlove chapters feeds (`/podcast.xml` and `/p/{id}/podcast.xml`), cover art, JSON chapters (`/episodes/{id}/chapters.json` and `/p/{id}/episodes/{ep}/chapters.json`), and audio delivery (`/audio/*` and `/p/{id}/audio/*`).
- **Data plane (`cmd/worker`)**: Claims `PENDING` episodes via CAS, preprocesses text (strips SSML/Markdown, splits paragraphs/sentences), runs Kokoro-82M ONNX TTS via `sherpa-onnx` (or Piper ONNX / mock TTS) + `ffmpeg` (128 kbps 24 kHz mono MP3 mastered to `-16 LUFS`), uploads `audio/<episode_id>.mp3`, and marks episodes `READY` or `FAILED`.

## Commands

```bash
make test         # go test ./... (uses SQLite, local storage, in-process jobs, mock TTS)
make vet          # go vet ./...
make build        # builds bin/server and bin/worker
make run          # runs bin/server with local dev defaults (does NOT read .env)
make smoke        # end-to-end ingest + RSS test against localhost:8080
```

Always run `go test ./...` and `go vet ./...` before finishing any code change. When running live/manual tests against a deployed environment, always use the `demo` user (`podcast_id: "demo"`) rather than production user feeds.

## Package Boundaries

- `internal/app`: Shared control-plane business logic (`CreateEpisode`, `UpdateEpisode`, `CreatePodcast`, `UpdatePodcast`, `RotatePodcastCredentials`, `AuthenticateBearer`, `Reconcile`) and backend wiring (`Open`, `OpenWithOptions`). Put domain orchestration here, not inside HTTP or MCP handlers.
- `internal/api`: HTTP router and handlers (`net/http` standard library `ServeMux`).
- `internal/mcp`: MCP server and tools (`github.com/modelcontextprotocol/go-sdk/mcp`). Keep MCP tools in sync with `internal/app` and `internal/api`.
- `internal/episode` & `internal/podcast`: Domain types, ID/secret generation, and input validation (`ValidationError` → HTTP `422` / MCP `IsError: true`).
- `internal/store`: Metadata persistence interface (`Store`) with two implementations: `SQLite` (`modernc.org/sqlite`) and `Firestore`.
- `internal/storage`: Audio blob storage interface (`Storage` + optional `URLSigner`) with two implementations: `Local` and `GCS`.
- `internal/job`: Async job dispatch (`Dispatcher`) with two implementations: `LocalDispatcher` (bounded channel + goroutines) and `CloudRunDispatcher` (`jobs.run` with `EPISODE_ID` override).
- `internal/tts`: Audio synthesis (`Engine`) with `KokoroEngine` (`sherpa-onnx-offline-tts` + `Kokoro-82M`), `PiperEngine` (Piper CLI), and `MockEngine` (embedded MP3 beep), plus `ffmpeg` WAV concat & `-16 LUFS` 24 kHz MP3 mastering.
- `internal/worker`: Episode claim (`CompareAndSwapStatus`), synthesis, duration probe, upload, and status transitions.

## Critical Invariants (See [CRITICAL.md](CRITICAL.md) & [DECISIONS.md](DECISIONS.md))

All production invariants and architectural decisions are documented in [CRITICAL.md](CRITICAL.md) and [DECISIONS.md](DECISIONS.md) (`ADR-001..005`):
- **Per-Show Isolation & 3-Tier Key Model (`ADR-002`, `ADR-004`)**: Non-empty `podcast_id` episodes and credentials never cross-authenticate with default `/podcast.xml`; publisher keys enforce the 3-tier hierarchy (`ADMIN_API_KEY` super key, default `AGENT_API_KEY`, per-show `submit_key`); `ListFilter{OnlyDefault: true}` filters `podcast_id == ""` before `LIMIT` ([CRITICAL.md](CRITICAL.md)).
- **SQLite & Firestore Parity (`ADR-005`)**: Implement every `store.Store` method identically in `sqlite.go` (using fixed 9-digit `sqliteTimeLayout`) and `firestore.go` (transactional `Update` existence check + `firestore.Exists(true)` on `Delete`), syncing composite indexes in `deploy/firestore.indexes.json` ([CRITICAL.md](CRITICAL.md)).
- **CAS Status Lifecycle, Reconcile & Immutable Audio (`ADR-003`)**: Map internal `PENDING` to public `QUEUED`, claim jobs via `CompareAndSwapStatus`, write immutable `audio/<episode_id>.mp3` blobs, enforce `WorkerTimeout` staleness in `App.Reconcile` when `JOB_BACKEND=cloudrun`, and persist `FAILED` via `context.WithoutCancel(ctx)` ([CRITICAL.md](CRITICAL.md)).
- **Pure-Go Zero-CGO, Distroless & Kokoro TTS (`ADR-001`)**: Build with `CGO_ENABLED=0` on `gcr.io/distroless/static-debian12:nonroot` (`65532:65532`, `--chown=65532:65532` on `/data`), chunk scripts `<= 500` runes, and master `Kokoro-82M` WAV output to `-16 LUFS` 24 kHz mono MP3 ([DECISIONS.md](DECISIONS.md)).
- **Environment, Cloud Run & RSS/Chapter Contract (`ADR-002`, `ADR-004`, `ADR-005`)**: Read process env vars only (dev defaults only when all 3 backends are local), use `CLOUD_RUN_JOB_NAME` and `/readyz`, return `500` (never `401`) on transient store errors in `requirePodcastFeed`, and keep RSS `<guid>` + `<psc:chapters>` / `chapters.json` stable ([DECISIONS.md](DECISIONS.md)).

## Documentation Sync Checklist

When modifying behavior, update the matching docs in the same change:
- New/changed env vars → `internal/config/config.go`, `.env.example`, `README.md`, `docs/deployment.md`
- New/changed REST routes or MCP tools → `docs/api.md`, `docs/architecture.md`, `README.md`
- New/changed Firestore queries or GCP IAM roles → `deploy/firestore.indexes.json`, `deploy/bootstrap-gcp.sh`, `docs/deployment.md`
