# AGENTS.md

Instructions for AI coding agents working in this repository.

## Project Overview

Podcaster (`github.com/tvaroska/podcaster`) is a split-plane private podcast platform for AI agents (Go 1.26, `CGO_ENABLED=0`):
- **Control plane (`cmd/server`)**: REST API (`/v1/*`), Streamable HTTP MCP server (`/mcp`), authenticated RSS 2.0 + iTunes feeds (`/podcast.xml` and `/p/{id}/podcast.xml`), cover art, and audio delivery (`/audio/*` and `/p/{id}/audio/*`).
- **Data plane (`cmd/worker`)**: Claims `PENDING` episodes via CAS, preprocesses text (strips SSML, chunks sentences), runs Piper ONNX TTS + `ffmpeg` (128 kbps mono MP3), uploads `audio/<episode_id>.mp3`, and marks episodes `READY` or `FAILED`.

## Commands

```bash
make test         # go test ./... (uses SQLite, local storage, in-process jobs, mock TTS)
make vet          # go vet ./...
make build        # builds bin/server and bin/worker
make run          # runs bin/server with local dev defaults (does NOT read .env)
make smoke        # end-to-end ingest + RSS test against localhost:8080
```

Always run `go test ./...` and `go vet ./...` before finishing any code change.

## Package Boundaries

- `internal/app`: Shared control-plane business logic (`CreateEpisode`, `CreatePodcast`, `Reconcile`) and backend wiring (`Open`, `OpenWithOptions`). Put domain orchestration here, not inside HTTP or MCP handlers.
- `internal/api`: HTTP router and handlers (`net/http` standard library `ServeMux`).
- `internal/mcp`: MCP server and tools (`github.com/modelcontextprotocol/go-sdk/mcp`). Keep MCP tools in sync with `internal/app` and `internal/api`.
- `internal/episode` & `internal/podcast`: Domain types, ID/secret generation, and input validation (`ValidationError` → HTTP `422` / MCP `IsError: true`).
- `internal/store`: Metadata persistence interface (`Store`) with two implementations: `SQLite` (`modernc.org/sqlite`) and `Firestore`.
- `internal/storage`: Audio blob storage interface (`Storage` + optional `URLSigner`) with two implementations: `Local` and `GCS`.
- `internal/job`: Async job dispatch (`Dispatcher`) with two implementations: `LocalDispatcher` (bounded channel + goroutines) and `CloudRunDispatcher` (`jobs.run` with `EPISODE_ID` override).
- `internal/tts`: Audio synthesis (`Engine`) with `MockEngine` (embedded MP3 beep) and `PiperEngine` (Piper CLI + `ffmpeg` WAV concat & MP3 encode).
- `internal/worker`: Episode claim (`CompareAndSwapStatus`), synthesis, duration probe, upload, and status transitions.

## Critical Invariants (Do Not Break)

1. **Per-Show Isolation**:
   - Episodes with a non-empty `podcast_id` belong exclusively to `/p/{id}/podcast.xml` and `/p/{id}/audio/{ep}.mp3`. They must **never** appear on the default `/podcast.xml` feed or be served by `/audio/{ep}.mp3`.
   - Default-show credentials (`FEED_*`) and per-show credentials (`podcasts` table/collection) must never cross-authenticate.
   - `ListFilter{OnlyDefault: true}` must filter `podcast_id == ""` at the query level in **both** `SQLite.List` and `Firestore.List` before applying `LIMIT`.

2. **SQLite & Firestore Parity**:
   - Every `store.Store` method change or filter addition must be implemented identically in both `internal/store/sqlite.go` and `internal/store/firestore.go`.
   - If you add or change a multi-field Firestore query (`Where` + `OrderBy`), you **must** update `deploy/firestore.indexes.json`, `deploy/bootstrap-gcp.sh`, and `docs/deployment.md`.

3. **Status Lifecycle & Concurrency**:
   - Internal store statuses are `PENDING`, `PROCESSING`, `READY`, `FAILED`.
   - External API/MCP responses map `PENDING` → `QUEUED` via `ep.PublicStatus()`. Incoming `List` filters accept both `QUEUED` and `PENDING`.
   - Workers must always claim episodes via `Store.CompareAndSwapStatus` (see `Worker.claim`).

4. **Pure-Go / Zero-CGO Builds**:
   - Production Docker images build with `CGO_ENABLED=0` and run `cmd/server` on `gcr.io/distroless/static-debian12:nonroot`. Do not add dependencies that require CGO.

5. **Environment & Cloud Run Constraints**:
   - Binaries read process environment variables only (`internal/config/config.go`); they do **not** parse `.env` files.
   - Never use `CLOUD_RUN_JOB` as a custom env var on a Cloud Run Service (GCP reserves it); use `CLOUD_RUN_JOB_NAME`.
   - `cmd/worker` opens the app with `DisableDispatcher: true` and uses `JOB_BACKEND=local` in production so it does not require `CLOUD_RUN_JOB_NAME`.
   - On public Cloud Run URLs, `/healthz` is intercepted by the Google Frontend (GFE); `/readyz` is the externally reachable readiness probe.
   - `deploy/cloudrun-service.yaml` and `deploy/cloudrun-job.yaml` are reference templates only — `docs/deployment.md` and `deploy/bootstrap-gcp.sh` are the source of truth for GCP deployment.

## Documentation Sync Checklist

When modifying behavior, update the matching docs in the same change:
- New/changed env vars → `internal/config/config.go`, `.env.example`, `README.md`, `docs/deployment.md`
- New/changed REST routes or MCP tools → `docs/api.md`, `docs/architecture.md`, `README.md`
- New/changed Firestore queries or GCP IAM roles → `deploy/firestore.indexes.json`, `deploy/bootstrap-gcp.sh`, `docs/deployment.md`
