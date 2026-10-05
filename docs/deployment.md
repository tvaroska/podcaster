# Deployment

A stranger should be able to get a private feed from this file. Local first, then a new GCP project.

The binary reads **process environment variables only**. It does not load `.env`. `.env.example` is a checklist. Locally:

```bash
set -a && source .env && set +a
./bin/server
```

`make run` ignores `.env` and injects local defaults itself.

## Environment variables

| Variable | Default | Description |
| --- | --- | --- |
| `LISTEN_ADDR` | `:8080` | HTTP bind address. If empty, `PORT` is used (Cloud Run convention). |
| `PORT` | — | Alternate bind port when `LISTEN_ADDR` is unset |
| `PUBLIC_BASE_URL` | `http://localhost:8080` | Absolute URL used in RSS enclosures and artwork. Must match the host listeners use. |
| `AGENT_API_KEY` | `dev-agent-key` in local mode | Bearer token for REST + MCP |
| `FEED_USERNAME` / `FEED_PASSWORD` | `podcast` / `podcast` in local mode | HTTP Basic for the **default** show (`/podcast.xml`) |
| `FEED_TOKEN` | `dev-feed-token` in local mode | Default-show `?token=`; derived from user:pass if empty |
| `STORE_BACKEND` | `sqlite` | `sqlite` or `firestore` |
| `SQLITE_PATH` | `data/podcaster.db` | SQLite file |
| `GCP_PROJECT` | — | Required for Firestore, GCS, Cloud Run Jobs |
| `STORAGE_BACKEND` | `local` | `local` or `gcs` |
| `LOCAL_DATA_DIR` | `data` | Root for local audio objects |
| `GCS_BUCKET` | — | Private bucket for enclosures |
| `JOB_BACKEND` | `local` | `local` (in-process) or `cloudrun` |
| `CLOUD_RUN_JOB_NAME` | — | Job name used by the control plane. Do **not** set `CLOUD_RUN_JOB` on a Cloud Run **Service** — the platform reserves that name and the deploy is rejected. |
| `CLOUD_RUN_REGION` | `us-central1` | Job region |
| `WORKER_TIMEOUT` | `30m` | Per-episode synthesis deadline |
| `SHUTDOWN_TIMEOUT` | `25s` | HTTP graceful shutdown |
| `TTS_ENGINE` | `mock` | `mock` (embedded beep) or `piper` |
| `PIPER_BIN` | `piper` | Piper executable (worker image sets `/opt/piper/piper`) |
| `PIPER_MODEL` | — | Path to `.onnx` (required for `piper`; baked into the worker image) |
| `PIPER_CONFIG` | — | Optional `.onnx.json` |
| `FFMPEG_BIN` | `ffmpeg` | ffmpeg for MP3 encode + duration probe |
| `DEFAULT_VOICE` | `en_US-lessac-medium` | Stored when the request omits `voice_id` |
| `VOICE_ALLOWLIST` | empty | Comma-separated allowed `voice_id`s. Set this in production. |
| `PODCAST_TITLE` | `Private Agent Briefing` | Default-show RSS title |
| `PODCAST_DESCRIPTION` | *(short default)* | Default-show RSS description |
| `PODCAST_AUTHOR` | `Podcaster` | `itunes:author` |
| `PODCAST_LANGUAGE` | `en-us` | RSS language |
| `PODCAST_CATEGORY` | `Technology` | `itunes:category` |
| `PODCAST_EXPLICIT` | `false` | `itunes:explicit` |
| `PODCAST_IMAGE_FILE` | — | Custom cover PNG; generated if empty |
| `PODCAST_OWNER_EMAIL` | — | Optional `itunes:owner` email |
| `MIN_CONTENT_LENGTH` | `10` | Ingest validation |
| `MAX_CONTENT_LENGTH` | `100000` | Ingest validation |
| `MAX_TITLE_LENGTH` | `200` | Ingest validation |
| `PODCASTER_DEV` | unset | Fill documented local secrets when `AGENT_API_KEY` is empty |

When `AGENT_API_KEY` is unset and backends are local, the process fills in the documented development secrets and logs a warning. `Validate()` only checks that secrets are *non-empty* — documented `dev-agent-key` / `podcast` values are accepted even with Firestore/GCS. Bootstrap generates random secrets; use those.

Per-user shows (`POST /v1/podcasts`) store **their own** username/password/token in the metadata store (`podcasts` table / Firestore collection). They do not use `FEED_*`.

## Local

Requirements: Go 1.26+, ffmpeg on `$PATH` (optional for the mock engine, required for Piper).

```bash
make test
make run
# another terminal
make smoke
```

With a real voice:

1. Install [Piper](https://github.com/rhasspy/piper/releases) and a voice, e.g. `en_US-lessac-medium`.
2. Export `TTS_ENGINE=piper`, `PIPER_BIN`, `PIPER_MODEL`, `PIPER_CONFIG` (and the rest of the local defaults).
3. `./bin/server`.

Docker Compose (mock TTS):

```bash
docker compose up --build
```

## GCP — new project from scratch

Topology:

| Piece | What |
| --- | --- |
| Cloud Run **Service** `podcaster` | `cmd/server` — REST, MCP, RSS, audio. Mock TTS is fine here; it only enqueues. |
| Cloud Run **Job** `podcaster-worker` | `cmd/worker` + Piper + ffmpeg. One execution per episode (`EPISODE_ID` override). |
| Firestore | Native mode. Collections `episodes` and `podcasts`. |
| GCS | Private bucket `audio/<id>.mp3`. |
| Secret Manager | `agent-api-key`, `feed-username`, `feed-password`, `feed-token` (default show only). |
| Artifact Registry | Docker repo `podcaster` (matches `deploy/cloudbuild.yaml`). |

Use **one region** for Firestore, the bucket, Artifact Registry, and Cloud Run. Firestore location cannot be changed later.

`deploy/cloudrun-*.yaml` are comments + placeholders (`IMAGE`). Deploy with the `gcloud` commands below, not `kubectl apply` and not `gcloud run services replace` of those files.

### 0. Project

```bash
export PROJECT=your-gcp-project          # must already exist
export REGION=us-central1
export BUCKET=podcaster-$PROJECT         # GCS names are global; change if taken
export SA=podcaster-sa@$PROJECT.iam.gserviceaccount.com
export IMAGE_BASE=$REGION-docker.pkg.dev/$PROJECT/podcaster

gcloud config set project $PROJECT
```

Prerequisites that the script will not invent:

- Billing enabled on `$PROJECT` (API enable and Cloud Run fail without it).
- Your user (or the SA running bootstrap) can enable APIs and grant IAM — typically **Owner** on a greenfield project.
- `gcloud auth login` and `gcloud auth application-default login` if you will talk to APIs from a laptop.

### 1. APIs, store, bucket, SA, secrets, Cloud Build IAM

```bash
chmod +x deploy/bootstrap-gcp.sh
PROJECT=$PROJECT REGION=$REGION BUCKET=$BUCKET ./deploy/bootstrap-gcp.sh
```

What that creates (if you would rather do it by hand, or to debug a failed step):

**APIs:** `run`, `firestore`, `storage`, `secretmanager`, `artifactregistry`, `cloudbuild`, `iam`, `iamcredentials` (the last one is required for GCS signed URLs on Cloud Run).

**Artifact Registry** docker repo named `podcaster` in `$REGION`.

**Firestore** native `(default)` in `$REGION`, plus the composite indexes in `deploy/firestore.indexes.json`:

- `episodes`: `status ASC, created_at DESC` (status-filtered episode lists and startup `Reconcile`)
- `episodes`: `podcast_id ASC, created_at DESC` (per-show episode lists)
- `episodes`: `podcast_id ASC, status ASC, created_at DESC` (default and per-user RSS feeds)

Indexes take a few minutes to go `READY`. Feeds 500 with `FAILED_PRECONDITION` until the `podcast_id` indexes exist.

```bash
gcloud firestore indexes composite list --project=$PROJECT --database='(default)'
```

**Bucket** `gs://$BUCKET` with uniform access and the lifecycle in `deploy/gcs-lifecycle.json` (audio → Nearline after 90 days).

**Runtime SA** `podcaster-sa` with:

| Role | Why |
| --- | --- |
| `roles/datastore.user` | Firestore read/write (`episodes` + `podcasts`) |
| `roles/storage.objectAdmin` on the bucket | Put/Get MP3s |
| `roles/run.jobsExecutorWithOverrides` | Server calls `jobs.run` **with env overrides** (`EPISODE_ID`). Plain `run.invoker` / `jobsExecutor` is not enough. Do **not** use `roles/run.developer` (that can mutate job specs). |
| `roles/iam.serviceAccountUser` on **itself** | Required to execute a Job that runs as this SA |
| `roles/iam.serviceAccountTokenCreator` on **itself** | IAM `signBlob` for GCS signed URLs (no JSON key on Cloud Run) |
| `roles/secretmanager.secretAccessor` | Mount secrets as env |
| `roles/logging.logWriter` | JSON logs |

**Cloud Build identities** (this is what a first `gcloud builds submit` 403s on if skipped):

| Who | Grant |
| --- | --- |
| `$PROJECT_NUMBER-compute@developer.gserviceaccount.com` | `roles/cloudbuild.builds.builder`; `roles/artifactregistry.writer` on the repo; `roles/storage.objectAdmin` on `gs://${PROJECT}_cloudbuild` |
| `$PROJECT_NUMBER@cloudbuild.gserviceaccount.com` | `roles/artifactregistry.writer` on the repo (classic Cloud Build SA) |
| `service-$PROJECT_NUMBER@serverless-robot-prod.iam.gserviceaccount.com` | `roles/artifactregistry.reader` on the repo (Cloud Run pull) |

**Secrets** (random; bootstrap leaves existing secrets alone):

- `agent-api-key` — Bearer for REST + MCP
- `feed-username` / `feed-password` — HTTP Basic for the **default** RSS/audio
- `feed-token` — `?token=` on the default feed + its enclosure URLs

Read them later with:

```bash
gcloud secrets versions access latest --secret=agent-api-key --project=$PROJECT
gcloud secrets versions access latest --secret=feed-username --project=$PROJECT
gcloud secrets versions access latest --secret=feed-password --project=$PROJECT
```

The worker binary also requires `AGENT_API_KEY` + feed creds at startup even though it does not serve HTTP (`config.Validate`). Mount the same secrets on the Job.

### 2. Build both images

```bash
gcloud builds submit \
  --project=$PROJECT \
  --config=deploy/cloudbuild.yaml \
  --substitutions=_REGION=$REGION,_TAG=latest \
  --timeout=1800s
```

`deploy/cloudbuild.yaml` substitutions must be `_NAME` (underscore prefix). `_TAG` defaults to `latest`. Worker image is large; Hugging Face/GitHub fetches during Docker build take ~5 minutes.

Produces:

- `$IMAGE_BASE/server:latest` — distroless, no Piper
- `$IMAGE_BASE/worker:latest` — Debian + ffmpeg + Piper + `en_US-lessac-medium` (downloaded at **build** time, no checksum). The image already sets `PIPER_BIN` / `PIPER_MODEL` / `PIPER_CONFIG`.

If submit 403s on `gs://${PROJECT}_cloudbuild`, re-run bootstrap or grant the compute SA `roles/storage.objectAdmin` on that bucket. If the image name ends with `server:` (empty tag), you passed an empty substitution — use `_TAG=latest`.

### 3. Deploy the Job first

The Job must exist before the Service can call `RunJob`.

```bash
gcloud run jobs deploy podcaster-worker \
  --project=$PROJECT --region=$REGION \
  --image=$IMAGE_BASE/worker:latest \
  --service-account=$SA \
  --tasks=1 --max-retries=1 --task-timeout=30m \
  --cpu=2 --memory=2Gi \
  --set-env-vars=STORE_BACKEND=firestore,STORAGE_BACKEND=gcs,JOB_BACKEND=local,TTS_ENGINE=piper,GCP_PROJECT=$PROJECT,GCS_BUCKET=$BUCKET \
  --set-secrets=AGENT_API_KEY=agent-api-key:latest,FEED_USERNAME=feed-username:latest,FEED_PASSWORD=feed-password:latest
```

`JOB_BACKEND=local` on the worker is required: `cmd/worker` already sets `DisableDispatcher`. Setting `cloudrun` there only makes `Validate()` require `CLOUD_RUN_JOB_NAME` for a process that never enqueues.

Optional worker-only smoke (bypasses the service dispatcher). Prefer step 5 unless you are debugging the job image.

A minimal Firestore document (`status` must be `PENDING` for CAS):

```json
{
  "id": "ep_manual",
  "title": "Manual worker smoke",
  "script_text": "Good morning. This is a worker-only smoke test.",
  "category": "",
  "voice_id": "en_US-lessac-medium",
  "status": "PENDING",
  "podcast_id": "",
  "created_at": "TIMESTAMP"
}
```

Console → Firestore → `episodes` → add document `ep_manual` with those fields (`created_at` as timestamp, numbers as double where needed), then:

```bash
gcloud run jobs execute podcaster-worker --project=$PROJECT --region=$REGION \
  --update-env-vars=EPISODE_ID=ep_manual --wait
```

Success: status `READY` and `gs://$BUCKET/audio/ep_manual.mp3`. First execution often takes **2–3 minutes** (image pull). Later runs are typically 20–60s.

### 4. Deploy the Service, then pin PUBLIC_BASE_URL

Cloud Run assigns the URL on first deploy. RSS enclosure links are absolute, so you must set `PUBLIC_BASE_URL` to the hostname you will subscribe with and deploy **again**. Cloud Run also exposes a second hostname (`https://SERVICE-PROJECTNUMBER.REGION.run.app`); if `PUBLIC_BASE_URL` and the URL in the podcast app disagree, artwork and enclosures 404.

```bash
gcloud run deploy podcaster \
  --project=$PROJECT --region=$REGION \
  --image=$IMAGE_BASE/server:latest \
  --service-account=$SA \
  --allow-unauthenticated \
  --port=8080 --cpu=1 --memory=512Mi --timeout=3600 \
  --min-instances=0 --max-instances=4 \
  --set-env-vars=STORE_BACKEND=firestore,STORAGE_BACKEND=gcs,JOB_BACKEND=cloudrun,TTS_ENGINE=mock,GCP_PROJECT=$PROJECT,GCS_BUCKET=$BUCKET,CLOUD_RUN_JOB_NAME=podcaster-worker,CLOUD_RUN_REGION=$REGION,VOICE_ALLOWLIST=en_US-lessac-medium \
  --set-secrets=AGENT_API_KEY=agent-api-key:latest,FEED_USERNAME=feed-username:latest,FEED_PASSWORD=feed-password:latest,FEED_TOKEN=feed-token:latest

URL=$(gcloud run services describe podcaster --project=$PROJECT --region=$REGION --format='value(status.url)')

gcloud run services update podcaster --project=$PROJECT --region=$REGION \
  --update-env-vars=PUBLIC_BASE_URL=$URL
```

`--allow-unauthenticated` is so podcast apps can hit `/podcast.xml` and `/audio/...` (those routes still check Basic / `?token=`). `/cover.png` is intentionally unauthenticated. Tighten ingest with a load balancer / IAP if you do not want REST/MCP on the public internet — today a stolen `AGENT_API_KEY` is enough to enqueue unlimited jobs.

If deploy fails with:

```
One or more users named in the policy do not belong to a permitted customer
Constraint constraints/iam.allowedPolicyMemberDomains
```

the org forbids `allUsers`. Redeploy with `--no-allow-unauthenticated --no-invoker-iam-check` instead of `--allow-unauthenticated`. Then grant `roles/run.invoker` only to identities that should call the service from `gcloud` / Cloud Scheduler; podcast apps still reach the URL because the invoker check is off.

With invoker IAM left on, unauthenticated `GET /healthz` is often **Google-frontend HTML 404** (`/healthz` is reserved by the GFE). The app probe is `GET /readyz`. Cloud Run's own startup probe still talks to the container, not the GFE.

`TTS_ENGINE=mock` on the **service** is correct (it does not synthesize). Piper lives only in the Job image.

Do **not** set env `CLOUD_RUN_JOB` on the service.

### 5. End-to-end check

```bash
URL=$(gcloud run services describe podcaster --project=$PROJECT --region=$REGION --format='value(status.url)')
KEY=$(gcloud secrets versions access latest --secret=agent-api-key --project=$PROJECT)
USER=$(gcloud secrets versions access latest --secret=feed-username --project=$PROJECT)
PASS=$(gcloud secrets versions access latest --secret=feed-password --project=$PROJECT)

# default show
ID=$(curl -sS -X POST "$URL/v1/episodes" \
  -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{"title":"GCP briefing","content":"Good morning. This is the first episode from a new project."}' \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["episode_id"])')
echo episode=$ID

# poll until READY (Piper + first-job cold start: often 2–3 minutes)
for i in $(seq 1 40); do
  BODY=$(curl -sS "$URL/v1/episodes/$ID" -H "Authorization: Bearer $KEY")
  echo "$BODY"
  echo "$BODY" | python3 -c 'import json,sys; s=json.load(sys.stdin).get("status",""); raise SystemExit(0 if s in ("READY","FAILED") else 1)' && break
  sleep 5
done

curl -sS -u "$USER:$PASS" "$URL/podcast.xml" | head
curl -sS -u "$USER:$PASS" -o /tmp/ep.mp3 -D - "$URL/audio/$ID.mp3"
# 307 to a GCS signed URL is success; if signing is mis-IAM'd the service proxies the bytes instead.

# per-user show
SHOW=$(curl -sS -X POST "$URL/v1/podcasts" \
  -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{"id":"demo","title":"Demo Briefing","description":"A private show for one listener."}')
echo "$SHOW"
# subscribe_url is ready to paste into Overcast. Default-show creds must 401 on /p/demo/podcast.xml.
```

Subscribe in Overcast / Apple Podcasts with the `subscribe_url` from show creation, or for the default show:

```
https://FEED_USERNAME:FEED_PASSWORD@HOST/podcast.xml
```

### Troubleshooting

| Symptom | Likely cause |
| --- | --- |
| `gcloud builds submit` 403 on `gs://PROJECT_cloudbuild` | Compute SA missing `cloudbuild.builds.builder` / objectAdmin on that bucket. Re-run bootstrap. |
| Image name `.../server:` | Empty tag. Pass `_TAG=latest`. |
| Deploy: reserved env `CLOUD_RUN_JOB` | Use `CLOUD_RUN_JOB_NAME` on the service. |
| Deploy: `iam.allowedPolicyMemberDomains` | Org blocks `allUsers`. `--no-invoker-iam-check`. |
| Public `GET /healthz` is HTML 404 | GFE reserved path. Use `/readyz`. |
| Ingest 202, episode stuck `QUEUED` | Job missing, SA missing `jobsExecutorWithOverrides` or `serviceAccountUser` on itself, or job logs show a crash. |
| Ingest 202, stuck `PROCESSING` | Worker died after CAS. Reconcile only runs on **service process start**; `--min-instances=0` waits for the next cold start. |
| Per-user feed 500 `FAILED_PRECONDITION` | `podcast_id` Firestore indexes not `READY`. |
| RSS links 404 / wrong host | `PUBLIC_BASE_URL` is not the hostname in the subscribe URL. |
| Audio 200 but slow / disconnects | Signed URL IAM failed; service is proxying. Grant `iam.serviceAccountTokenCreator` on the runtime SA. |
| Worker exits on start: `PIPER_MODEL is required` | You overrode `TTS_ENGINE=piper` without the image env. Do not clear `PIPER_*`. |
| Worker exits: `CLOUD_RUN_JOB_NAME is required` | `JOB_BACKEND=cloudrun` on the job. Set `local`. |

### Operational notes

- **Ingest → Job is not a queue.** Each `POST /v1/episodes` is a synchronous `jobs.run` RPC. Batch publishes can hit Cloud Run Jobs concurrency quotas (default 100). There is no DLQ.
- **Reconcile only runs on service process start.** With `--min-instances=0` a crash-stranded `PROCESSING` row waits until the next cold start.
- **Permanent feed tokens are embedded in every enclosure URL.** Sharing one episode link leaks that show's credentials. Treat subscribe URLs as secrets.
- **Listener passwords live in Firestore/SQLite** on the `podcasts` document (plaintext). Anyone with `roles/datastore.user` can read them. Default-show secrets are in Secret Manager.
- **Do not reuse `dev-agent-key` / `podcast`/`podcast`.** The process will start with them on Firestore/GCS.
- **`VOICE_ALLOWLIST` should be set in production.** Otherwise `voice_id` may be treated as a Piper model path (`*.onnx` / path separators).

## Observability

Both binaries log JSON (`slog`) with `episode_id`, HTTP path/status, and synthesis byte counts. Cloud Run collects stdout.

Probes:

- liveness (container): `GET /healthz` — always `{"status":"ok"}`. Do not use this from the public internet; the GFE may intercept it.
- readiness: `GET /readyz` (fails if Firestore/SQLite is unreachable)

## Cost notes

Standby cost is dominated by the Cloud Run service min instances (keep at 0 unless you need sub-second ingest). Jobs are billed only while synthesizing. Firestore and GCS are pay-per-use. Piper avoids per-character TTS API fees.
