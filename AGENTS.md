# AGENTS.md

Instructions for AI coding agents working in this repository.

## Project Overview

Podcaster (`github.com/tvaroska/podcaster`) is a split-plane private podcast platform for AI agents (Go 1.26, `CGO_ENABLED=0`):
- **Control plane (`cmd/server`)**: REST API (`/v1/*`), Streamable HTTP MCP server (`/mcp`), authenticated RSS 2.0 + iTunes + Podcasting 2.0 / Podlove chapters feeds (`/podcast.xml` and `/p/{id}/podcast.xml`), cover art, JSON chapters (`/episodes/{id}/chapters.json` and `/p/{id}/episodes/{ep}/chapters.json`), and audio delivery (`/audio/*` and `/p/{id}/audio/*`).
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

- `internal/app`: Shared control-plane business logic (`CreateEpisode`, `UpdateEpisode`, `CreatePodcast`, `UpdatePodcast`, `RotatePodcastCredentials`, `AuthenticateBearer`, `Reconcile`) and backend wiring (`Open`, `OpenWithOptions`). Put domain orchestration here, not inside HTTP or MCP handlers.
- `internal/api`: HTTP router and handlers (`net/http` standard library `ServeMux`).
- `internal/mcp`: MCP server and tools (`github.com/modelcontextprotocol/go-sdk/mcp`). Keep MCP tools in sync with `internal/app` and `internal/api`.
- `internal/episode` & `internal/podcast`: Domain types, ID/secret generation, and input validation (`ValidationError` → HTTP `422` / MCP `IsError: true`).
- `internal/store`: Metadata persistence interface (`Store`) with two implementations: `SQLite` (`modernc.org/sqlite`) and `Firestore`.
- `internal/storage`: Audio blob storage interface (`Storage` + optional `URLSigner`) with two implementations: `Local` and `GCS`.
- `internal/job`: Async job dispatch (`Dispatcher`) with two implementations: `LocalDispatcher` (bounded channel + goroutines) and `CloudRunDispatcher` (`jobs.run` with `EPISODE_ID` override).
- `internal/tts`: Audio synthesis (`Engine`) with `MockEngine` (embedded MP3 beep) and `PiperEngine` (Piper CLI + `ffmpeg` WAV concat & MP3 encode).
- `internal/worker`: Episode claim (`CompareAndSwapStatus`), synthesis, duration probe, upload, and status transitions.

## Critical Invariants (Do Not Break)

1. **Per-Show Isolation & 3-Tier Key Model**:
   - Episodes with a non-empty `podcast_id` belong exclusively to `/p/{id}/podcast.xml`, `/p/{id}/audio/{ep}.mp3`, and `/p/{id}/episodes/{ep}/chapters.json`. They must **never** appear on the default `/podcast.xml` feed or be served by `/audio/{ep}.mp3` or `/episodes/{ep}/chapters.json`.
   - Default-show credentials (`FEED_*`) and per-show listener credentials (`password` / `token` in the `podcasts` table/collection) must never cross-authenticate.
   - **3-Tier Publisher & Listener Separation**:
     - **Super Key (`ADMIN_API_KEY`, or `AGENT_API_KEY` when `ADMIN_API_KEY` is unset)**: Required to create and list shows (`POST /v1/podcasts`, `GET /v1/podcasts`), and authorized across all shows, episode updates, and credential rotations.
     - **Default-Show Submitter (`AGENT_API_KEY` when `ADMIN_API_KEY` is set)**: Scoped exclusively to publishing, updating (`PATCH /v1/episodes/{id}` / MCP `update_episode`), and listing episodes on the default feed (`podcast_id == ""`).
     - **Per-Show Submitter (`submit_key`)**: Scoped exclusively to publishing/updating/listing episodes for show `{id}`, viewing `{id}` (`GET /v1/podcasts/{id}`), updating `{id}`'s metadata (`PATCH /v1/podcasts/{id}` / MCP `update_podcast`), and rotating `{id}`'s listening credentials (`POST /v1/podcasts/{id}/rotate` / MCP `rotate_podcast_credentials`). It cannot create/list other shows (`403`) or read RSS/MP3/chapters endpoints.
   - **Credential Rotation**: `App.RotatePodcastCredentials` and `Store.UpdatePodcast` must persist updated listener `password`/`token` (and optional `submit_key`) in both SQLite and Firestore while preserving `created_at` and returning `store.ErrNotFound` when the show does not exist.
   - `ListFilter{OnlyDefault: true}` must filter `podcast_id == ""` at the query level in **both** `SQLite.List` and `Firestore.List` before applying `LIMIT`.

2. **SQLite & Firestore Parity**:
   - Every `store.Store` method change or filter addition must be implemented identically in both `internal/store/sqlite.go` and `internal/store/firestore.go`.
   - **SQLite Timestamp Ordering**: SQLite sorts `ORDER BY created_at DESC` lexicographically on `TEXT`. Always format SQLite timestamps with fixed 9-digit zero-padded nanoseconds (`sqliteTimeLayout = "2006-01-02T15:04:05.000000000Z"`), never variable-width `time.RFC3339Nano` on write (where `"Z" > "."` misorders sub-second timestamps).
   - **Firestore Preconditions**: `Firestore.Update` must check document existence in a transaction (returning `store.ErrNotFound` and preserving `created_at`) and `Firestore.Delete` must use `firestore.Exists(true)` to match `SQLite`'s `RowsAffected() == 0 -> store.ErrNotFound` behavior.
   - If you add or change a multi-field Firestore query (`Where` + `OrderBy`), you **must** update `deploy/firestore.indexes.json`, `deploy/bootstrap-gcp.sh`, and `docs/deployment.md`.

3. **Status Lifecycle, Reconcile & Concurrency**:
   - Internal store statuses are `PENDING`, `PROCESSING`, `READY`, `FAILED`.
   - External API/MCP responses map `PENDING` → `QUEUED` via `ep.PublicStatus()`. Incoming `List` filters accept both `QUEUED` and `PENDING`.
   - Workers must always claim episodes via `Store.CompareAndSwapStatus` (see `Worker.claim`).
   - **Cloud Run `Reconcile` Staleness Cutoff**: `App.Reconcile` runs asynchronously on `cmd/server` startup and must enforce the `WorkerTimeout` staleness cutoff when `JOB_BACKEND=cloudrun` so cold-starting or scaling server instances never reset in-flight `PROCESSING` Cloud Run Jobs.
   - **Canceled Context Failure Persistence**: When synthesis fails due to `WORKER_TIMEOUT` or `SIGTERM`, `Worker.fail` must use `context.WithoutCancel(ctx)` with a bounded timeout so `Store.Update` can still persist `FAILED` status.
   - **Enqueue Failure Recovery**: If `Jobs.Enqueue` fails in `App.CreateEpisode`, keep the episode in `PENDING` (with `ErrorMessage` set) so `Reconcile` can retry it later.

4. **Pure-Go / Zero-CGO & Distroless Container Builds**:
   - Production Docker images build with `CGO_ENABLED=0` (`GOARCH=amd64`) and run `cmd/server` on `gcr.io/distroless/static-debian12:nonroot` (`65532:65532`). Do not add dependencies that require CGO.
   - Writable volume mount paths (such as `/data` in `Dockerfile`) must be pre-created with `--chown=65532:65532` so Docker named volumes do not initialize as `root:root`.

5. **Environment, Security & Cloud Run Constraints**:
   - Binaries read process environment variables only (`internal/config/config.go`); they do **not** parse `.env` files.
   - Local dev default credentials (`dev-agent-key`, `podcast:podcast`, `dev-feed-token`) must **only** populate when all three backends are local (`sqlite`, `local`, `local`), never when any cloud backend (`firestore`, `gcs`, `cloudrun`) is active.
   - Never use `CLOUD_RUN_JOB` as a custom env var on a Cloud Run Service (GCP reserves it); use `CLOUD_RUN_JOB_NAME`.
   - `cmd/worker` opens the app with `DisableDispatcher: true` and uses `JOB_BACKEND=local` in production so it does not require `CLOUD_RUN_JOB_NAME`.
   - On public Cloud Run URLs, `/healthz` is intercepted by the Google Frontend (GFE); `/readyz` is the externally reachable readiness probe.
   - In `requirePodcastFeed`, return `401 Unauthorized` only on `store.ErrNotFound` or bad credentials; return `500` on transient store errors so podcast apps do not treat DB blips as revoked credentials.
   - `deploy/cloudrun-service.yaml` and `deploy/cloudrun-job.yaml` are reference templates only — `docs/deployment.md` and `deploy/bootstrap-gcp.sh` are the source of truth for GCP deployment.

## Documentation Sync Checklist

When modifying behavior, update the matching docs in the same change:
- New/changed env vars → `internal/config/config.go`, `.env.example`, `README.md`, `docs/deployment.md`
- New/changed REST routes or MCP tools → `docs/api.md`, `docs/architecture.md`, `README.md`
- New/changed Firestore queries or GCP IAM roles → `deploy/firestore.indexes.json`, `deploy/bootstrap-gcp.sh`, `docs/deployment.md`
