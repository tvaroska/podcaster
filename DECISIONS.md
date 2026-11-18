# Architectural Decisions (`DECISIONS.md`)

Architectural Decision Records for **`podcaster`** (`github.com/tvaroska/podcaster`). See [`CRITICAL.md`](CRITICAL.md) for the load-bearing invariants table and [`docs/architecture.md`](docs/architecture.md) for system topology.

---

## ADR-001: Kokoro-82M ONNX TTS Chunking, Voice Routing & `-16 LUFS` Mastering (2026-09-26)
- **Context:** Long-form technical briefings (`10,000–100,000` characters) contain Markdown formatting, code blocks, and multi-paragraph sections that exceed single-pass neural TTS context windows or cause prosody drift and uneven loudness across podcast apps.
- **Decision:** Preprocess script text in [`internal/tts/preprocess.go`](internal/tts/preprocess.go) by stripping SSML/Markdown and splitting on paragraph and sentence boundaries into chunks of `<= 500` runes. Validate requested `voice_id` against `DEFAULT_VOICE` (`af_heart`) and `VOICE_ALLOWLIST` ([`internal/config/config.go`](internal/config/config.go)), synthesize each chunk via `KokoroEngine` (`sherpa-onnx-offline-tts` + `Kokoro-82M` ONNX with bounded `KOKORO_CONCURRENCY` and `KOKORO_THREADS` in [`internal/tts/kokoro.go`](internal/tts/kokoro.go)), and concatenate/master the resulting WAV segments via `ffmpeg` (`128 kbps`, `24 kHz` mono MP3 normalized to `-16 LUFS` in [`internal/tts/ffmpeg.go`](internal/tts/ffmpeg.go)).

---

## ADR-002: RSS 2.0 Feed GUID Stability & Per-Show Namespace Isolation (2026-09-26)
- **Context:** Apple Podcasts, Overcast, and Pocket Casts deduplicate episodes strictly by `<guid>` and cache enclosure URLs per feed. Any GUID mutation across metadata edits (`PATCH /v1/episodes/{id}`) or credential rotations triggers duplicate downloads, while leaking episodes across show slugs violates tenant privacy.
- **Decision:** Lock each RSS `<item>` `<guid isPermaLink="false">` permanently to the immutable `ep.ID` in [`internal/rss/rss.go`](internal/rss/rss.go). Enforce strict namespace isolation in [`internal/app/app.go`](internal/app/app.go) and [`internal/api/handlers.go`](internal/api/handlers.go): episodes with `podcast_id == ""` appear exclusively on `/podcast.xml` (`ListFilter{OnlyDefault: true}` pushed down into SQLite/Firestore queries before `LIMIT`), while episodes with a non-empty `podcast_id` are served exclusively under `/p/{id}/podcast.xml`, `/p/{id}/audio/{ep}.mp3`, and `/p/{id}/episodes/{ep}/chapters.json`.

---

## ADR-003: GCS Audio Object Immutability & CAS Worker Status Lifecycle (2026-09-26)
- **Context:** Concurrent worker retries (`CLOUD_RUN_TASK_ATTEMPT > 0`), cold-starting control-plane instances running `App.Reconcile`, and `SIGTERM` / `WORKER_TIMEOUT` interruptions can cause duplicate TTS synthesis or stranded `PROCESSING` episodes if state transitions and blob writes are not coordinated.
- **Decision:** Store synthesized MP3s as immutable, episode-keyed objects (`audio/<episode_id>.mp3`) in [`internal/storage/gcs.go`](internal/storage/gcs.go) and [`internal/storage/local.go`](internal/storage/local.go), delivered via authenticated `307 Temporary Redirect` V4 signed URLs (or byte-range proxy fallback). Workers claim episodes atomically via `Store.CompareAndSwapStatus` in [`internal/worker/worker.go`](internal/worker/worker.go), persist `FAILED` status using `context.WithoutCancel(ctx)` when interrupted, and rely on `App.Reconcile` (gated by `WorkerTimeout` staleness cutoff when `JOB_BACKEND=cloudrun`) to re-enqueue stranded `PENDING` or expired `PROCESSING` jobs.

---

## ADR-004: Cloud Run / GKE 3-Tier Key Hierarchy & Listener Auth Separation (2026-10-04)
- **Context:** Autonomous AI agents publishing episodes over REST (`/v1/*`) or Streamable HTTP MCP (`/mcp`) should not hold administrative credentials capable of enumerating other users' shows, nor should listener credentials used in podcast apps grant write access. Additionally, transient database errors on RSS feed refreshes must never cause podcast clients to wipe saved Basic Auth credentials.
- **Decision:** Separate authentication in [`internal/auth/auth.go`](internal/auth/auth.go) and [`internal/app/app.go`](internal/app/app.go) into a **3-tier publisher hierarchy** (**Super Key** `ADMIN_API_KEY`, **Default-Show Submitter** `AGENT_API_KEY`, and **Per-Show Submitter** `submit_key`) completely disjoint from **Per-Show Listener Credentials** (`username`/`password` Basic Auth and `?token=` query auth, rotatable via `POST /v1/podcasts/{id}/rotate`). Populate local dev secrets only when all three backends are `(sqlite, local, local)`, use `/readyz` for external probes (`/healthz` is reserved by GFE), and return HTTP `401` on RSS/audio routes strictly on `store.ErrNotFound` or invalid credentials (`500` on transient store errors).

---

## ADR-005: Dual-Format Chapter & Show-Note Schema with SQLite/Firestore Parity (2026-10-12)
- **Context:** Podcast apps split between inline Podlove Simple Chapters (`<psc:chapters>`) and Podcasting 2.0 external JSON Chapters (`<podcast:chapters>`), and agents frequently enrich show notes (`description`), artwork (`image_url`), and chapter timestamps after initial audio synthesis.
- **Decision:** Validate `Chapter` markers (`start_sec >= 0`, non-empty `title`, optional `href`/`image_url`) and episode metadata in [`internal/episode/validate.go`](internal/episode/validate.go), persist chapters identically across SQLite (`chapters_json` in [`internal/store/sqlite.go`](internal/store/sqlite.go) with fixed 9-digit `sqliteTimeLayout` timestamps) and Firestore (`chapters` in [`internal/store/firestore.go`](internal/store/firestore.go) with transactional existence preconditions), emit both `<psc:chapters>` and `<podcast:chapters>` (`chapters.json`) alongside `<itunes:summary>` and `<content:encoded>` in [`internal/rss/rss.go`](internal/rss/rss.go), and allow post-publish updates via `PATCH /v1/episodes/{id}` and MCP `update_episode`.
