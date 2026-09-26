# Deployment

## Environment variables

| Variable | Default | Description |
| --- | --- | --- |
| `LISTEN_ADDR` | `:8080` | HTTP bind address. If empty, `PORT` is used (Cloud Run convention). |
| `PORT` | — | Alternate bind port when `LISTEN_ADDR` is unset |
| `PUBLIC_BASE_URL` | `http://localhost:8080` | Absolute URL used in RSS enclosures and artwork |
| `AGENT_API_KEY` | `dev-agent-key` in local mode | Bearer token for REST + MCP |
| `FEED_USERNAME` / `FEED_PASSWORD` | `podcast` / `podcast` in local mode | HTTP Basic for listeners |
| `FEED_TOKEN` | `dev-feed-token` in local mode | Query-param alternative; derived from user:pass if empty |
| `STORE_BACKEND` | `sqlite` | `sqlite` or `firestore` |
| `SQLITE_PATH` | `data/podcaster.db` | SQLite file |
| `GCP_PROJECT` | — | Required for Firestore, GCS, Cloud Run Jobs |
| `STORAGE_BACKEND` | `local` | `local` or `gcs` |
| `LOCAL_DATA_DIR` | `data` | Root for local audio objects |
| `GCS_BUCKET` | — | Private bucket for enclosures |
| `JOB_BACKEND` | `local` | `local` (in-process) or `cloudrun` |
| `CLOUD_RUN_JOB` | — | Job name used by the control plane |
| `CLOUD_RUN_REGION` | `us-central1` | Job region |
| `WORKER_TIMEOUT` | `30m` | Per-episode synthesis deadline |
| `SHUTDOWN_TIMEOUT` | `25s` | HTTP graceful shutdown |
| `TTS_ENGINE` | `mock` | `mock` (embedded beep) or `piper` |
| `PIPER_BIN` | `piper` | Piper executable |
| `PIPER_MODEL` | — | Path to `.onnx` (required for `piper`) |
| `PIPER_CONFIG` | — | Optional `.onnx.json` |
| `FFMPEG_BIN` | `ffmpeg` | ffmpeg for MP3 encode + duration probe |
| `DEFAULT_VOICE` | `en_US-lessac-medium` | Stored when the request omits `voice_id` |
| `VOICE_ALLOWLIST` | empty | Comma-separated allowed `voice_id`s |
| `PODCAST_TITLE` | `Private Agent Briefing` | RSS channel title |
| `PODCAST_DESCRIPTION` | *(short default)* | RSS description |
| `PODCAST_AUTHOR` | `Podcaster` | `itunes:author` |
| `PODCAST_LANGUAGE` | `en-us` | RSS language |
| `PODCAST_CATEGORY` | `Technology` | `itunes:category` |
| `PODCAST_EXPLICIT` | `false` | `itunes:explicit` |
| `PODCAST_IMAGE_FILE` | — | Custom cover PNG; generated if empty |
| `PODCAST_OWNER_EMAIL` | — | Optional `itunes:owner` email |
| `MIN_CONTENT_LENGTH` | `10` | Ingest validation |
| `MAX_CONTENT_LENGTH` | `100000` | Ingest validation |
| `MAX_TITLE_LENGTH` | `200` | Ingest validation |
| `PODCASTER_DEV` | unset | Force documented local defaults when secrets are missing |

When `AGENT_API_KEY` is unset and backends are local, the process fills in the documented development secrets and logs a warning. Production (`firestore` / `gcs` / `cloudrun`) refuses to start without real credentials.

## Local

Requirements: Go 1.22+, ffmpeg on `$PATH` (optional for the mock engine, required for Piper).

```bash
cp .env.example .env
make run
# another terminal
make smoke
```

With a real voice:

1. Install [Piper](https://github.com/rhasspy/piper/releases) and a voice, e.g. `en_US-lessac-medium`.
2. Set `TTS_ENGINE=piper`, `PIPER_BIN`, `PIPER_MODEL`, `PIPER_CONFIG`.
3. `make run`.

Docker Compose (mock TTS):

```bash
docker compose up --build
```

## GCP

The recommended production topology matches the spec:

- **Cloud Run Service** — `cmd/server` image (no Piper binary required if jobs run elsewhere).
- **Cloud Run Job** — `cmd/worker` image with Piper + ffmpeg + the voice model.
- **Firestore** — native mode, collection `episodes`. Deploy composite indexes from `deploy/firestore.indexes.json` (`gcloud firestore indexes composite create` / Terraform).
- **GCS** — private bucket, optional lifecycle delete/archive after N days.
- **Secret Manager** — `AGENT_API_KEY`, `FEED_USERNAME`, `FEED_PASSWORD`, `FEED_TOKEN`.

IAM for the service account:

- `roles/datastore.user` (Firestore)
- `roles/storage.objectAdmin` on the audio bucket
- `roles/run.developer` (or a custom role with `run.jobs.run`) so the service can start the worker job
- `roles/secretmanager.secretAccessor`

### Build images

```bash
export PROJECT=your-gcp-project
export REGION=us-central1
gcloud builds submit --config deploy/cloudbuild.yaml
```

Or locally:

```bash
docker build --target server -t $REGION-docker.pkg.dev/$PROJECT/podcaster/server:latest .
docker build --target worker -t $REGION-docker.pkg.dev/$PROJECT/podcaster/worker:latest .
```

The worker image downloads a Piper Linux x86_64 release and the `en_US-lessac-medium` voice at build time. Override `PIPER_VOICE_URL` / `PIPER_VERSION` build args as needed.

### First-time GCP resources

```bash
PROJECT=your-gcp-project
REGION=us-central1
BUCKET=podcaster-$PROJECT
SA=podcaster-sa@$PROJECT.iam.gserviceaccount.com

gcloud config set project $PROJECT
gcloud services enable run.googleapis.com firestore.googleapis.com storage.googleapis.com secretmanager.googleapis.com artifactregistry.googleapis.com

gcloud firestore databases create --location=$REGION --type=firestore-native || true
gcloud storage buckets create gs://$BUCKET --location=$REGION --uniform-bucket-level-access
gcloud storage buckets update gs://$BUCKET --lifecycle-file=deploy/gcs-lifecycle.json

gcloud iam service-accounts create podcaster-sa --display-name="podcaster"
gcloud projects add-iam-policy-binding $PROJECT --member="serviceAccount:$SA" --role=roles/datastore.user
gcloud storage buckets add-iam-policy-binding gs://$BUCKET --member="serviceAccount:$SA" --role=roles/storage.objectAdmin
gcloud projects add-iam-policy-binding $PROJECT --member="serviceAccount:$SA" --role=roles/run.developer

printf 'replace-me' | gcloud secrets create agent-api-key --data-file=-
printf 'podcast' | gcloud secrets create feed-username --data-file=-
printf 'replace-me' | gcloud secrets create feed-password --data-file=-
```

### Deploy the job, then the service

See `deploy/cloudrun-job.yaml` and `deploy/cloudrun-service.yaml`. After deploy, set `PUBLIC_BASE_URL` to the Cloud Run https URL and re-deploy the service so RSS enclosure links are absolute.

Cloud Run Job executions receive `EPISODE_ID` via container env overrides from `internal/job/cloudrun.go`.

## Observability

Both binaries log JSON (`slog`) with `episode_id`, HTTP path/status, and synthesis byte counts. Wire Cloud Logging / Error Reporting by running on Cloud Run — stdout is collected automatically.

Probe:

- liveness: `GET /healthz`
- readiness: `GET /readyz` (fails if Firestore/SQLite is unreachable)

## Cost notes

Standby cost is dominated by the Cloud Run service min instances (keep at 0 unless you need sub-second ingest). Jobs are billed only while synthesizing. Firestore and GCS are pay-per-use. Piper avoids per-character TTS API fees.
