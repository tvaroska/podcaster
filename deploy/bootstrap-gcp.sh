#!/usr/bin/env bash
# First-time GCP bootstrap for Podcaster.
# Requires: gcloud, billing on the project, and a principal that can enable
# APIs and grant IAM (typically Owner).
#
# Usage:
#   export PROJECT=your-gcp-project
#   export REGION=us-central1          # optional, default us-central1
#   export BUCKET=podcaster-$PROJECT   # optional; must be globally unique
#   ./deploy/bootstrap-gcp.sh
set -euo pipefail

PROJECT="${PROJECT:?set PROJECT to a GCP project id}"
REGION="${REGION:-us-central1}"
BUCKET="${BUCKET:-podcaster-${PROJECT}}"
REPO="${REPO:-podcaster}"
SA_NAME="${SA_NAME:-podcaster-sa}"
SA="${SA_NAME}@${PROJECT}.iam.gserviceaccount.com"
JOB_NAME="${JOB_NAME:-podcaster-worker}"
SERVICE_NAME="${SERVICE_NAME:-podcaster}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

need() { command -v "$1" >/dev/null || { echo "missing $1" >&2; exit 1; }; }
need gcloud
need openssl

gcloud config set project "$PROJECT" >/dev/null
echo "==> project=$PROJECT region=$REGION bucket=$BUCKET sa=$SA"

echo "==> enable APIs"
gcloud services enable \
  run.googleapis.com \
  firestore.googleapis.com \
  storage.googleapis.com \
  secretmanager.googleapis.com \
  artifactregistry.googleapis.com \
  cloudbuild.googleapis.com \
  iam.googleapis.com \
  iamcredentials.googleapis.com \
  --project="$PROJECT"

echo "==> artifact registry docker repo $REPO"
if ! gcloud artifacts repositories describe "$REPO" \
    --location="$REGION" --project="$PROJECT" >/dev/null 2>&1; then
  gcloud artifacts repositories create "$REPO" \
    --repository-format=docker \
    --location="$REGION" \
    --description="Podcaster server and worker images" \
    --project="$PROJECT"
fi

echo "==> firestore native ($REGION)"
if ! gcloud firestore databases describe --database='(default)' --project="$PROJECT" >/dev/null 2>&1; then
  gcloud firestore databases create \
    --location="$REGION" \
    --type=firestore-native \
    --database='(default)' \
    --project="$PROJECT"
fi

ensure_index() {
  echo "    index: $*"
  gcloud firestore indexes composite create \
    --project="$PROJECT" \
    --database='(default)' \
    --collection-group=episodes \
    --query-scope=COLLECTION \
    "$@" >/dev/null 2>&1 || true
}

echo "==> firestore composite indexes (matches deploy/firestore.indexes.json)"
ensure_index \
  --field-config=field-path=status,order=ASCENDING \
  --field-config=field-path=created_at,order=DESCENDING
ensure_index \
  --field-config=field-path=podcast_id,order=ASCENDING \
  --field-config=field-path=created_at,order=DESCENDING
ensure_index \
  --field-config=field-path=podcast_id,order=ASCENDING \
  --field-config=field-path=status,order=ASCENDING \
  --field-config=field-path=created_at,order=DESCENDING

echo "==> gcs bucket gs://$BUCKET"
if ! gcloud storage buckets describe "gs://$BUCKET" --project="$PROJECT" >/dev/null 2>&1; then
  gcloud storage buckets create "gs://$BUCKET" \
    --project="$PROJECT" \
    --location="$REGION" \
    --uniform-bucket-level-access
fi
gcloud storage buckets update "gs://$BUCKET" \
  --lifecycle-file="$ROOT/deploy/gcs-lifecycle.json"

echo "==> runtime service account"
if ! gcloud iam service-accounts describe "$SA" --project="$PROJECT" >/dev/null 2>&1; then
  gcloud iam service-accounts create "$SA_NAME" \
    --project="$PROJECT" \
    --display-name="podcaster"
fi

bind() {
  gcloud projects add-iam-policy-binding "$PROJECT" \
    --member="serviceAccount:$SA" \
    --role="$1" \
    --condition=None \
    --quiet >/dev/null
}

echo "==> IAM"
bind roles/datastore.user
bind roles/logging.logWriter
bind roles/secretmanager.secretAccessor
# Needed so the control plane can RunJob with EPISODE_ID env overrides.
bind roles/run.jobsExecutorWithOverrides
# Signed GCS URLs on Cloud Run use IAM signBlob, not a JSON key.
bind roles/iam.serviceAccountTokenCreator

gcloud storage buckets add-iam-policy-binding "gs://$BUCKET" \
  --member="serviceAccount:$SA" \
  --role=roles/storage.objectAdmin \
  --quiet >/dev/null

# Cloud Run Jobs execute as this SA; the caller (also this SA) must be allowed to act as it.
gcloud iam service-accounts add-iam-policy-binding "$SA" \
  --project="$PROJECT" \
  --member="serviceAccount:$SA" \
  --role=roles/iam.serviceAccountUser \
  --quiet >/dev/null

ensure_secret() {
  local name="$1" value="$2"
  if gcloud secrets describe "$name" --project="$PROJECT" >/dev/null 2>&1; then
    echo "    secret $name already exists (left unchanged)"
    return
  fi
  printf '%s' "$value" | gcloud secrets create "$name" --project="$PROJECT" --data-file=-
  echo "    created secret $name"
}

echo "==> Cloud Build + Cloud Run image pull IAM"
PROJECT_NUMBER="$(gcloud projects describe "$PROJECT" --format='value(projectNumber)')"
COMPUTE_SA="${PROJECT_NUMBER}-compute@developer.gserviceaccount.com"
CB_SA="${PROJECT_NUMBER}@cloudbuild.gserviceaccount.com"
RUN_SA="service-${PROJECT_NUMBER}@serverless-robot-prod.iam.gserviceaccount.com"
CB_BUCKET="${PROJECT}_cloudbuild"

gcloud projects add-iam-policy-binding "$PROJECT" \
  --member="serviceAccount:${COMPUTE_SA}" \
  --role=roles/cloudbuild.builds.builder \
  --condition=None --quiet >/dev/null

for member in "serviceAccount:${COMPUTE_SA}" "serviceAccount:${CB_SA}"; do
  gcloud artifacts repositories add-iam-policy-binding "$REPO" \
    --location="$REGION" --project="$PROJECT" \
    --member="$member" \
    --role=roles/artifactregistry.writer \
    --quiet >/dev/null || true
done

# Cloud Run service agent is created when the Run API is enabled; retry briefly.
for i in 1 2 3 4 5 6; do
  if gcloud artifacts repositories add-iam-policy-binding "$REPO" \
      --location="$REGION" --project="$PROJECT" \
      --member="serviceAccount:${RUN_SA}" \
      --role=roles/artifactregistry.reader \
      --quiet >/dev/null 2>&1; then
    break
  fi
  sleep 5
done

if ! gcloud storage buckets describe "gs://${CB_BUCKET}" --project="$PROJECT" >/dev/null 2>&1; then
  gcloud storage buckets create "gs://${CB_BUCKET}" \
    --project="$PROJECT" \
    --location="$REGION" \
    --uniform-bucket-level-access
fi
for member in "serviceAccount:${COMPUTE_SA}" "serviceAccount:${CB_SA}"; do
  gcloud storage buckets add-iam-policy-binding "gs://${CB_BUCKET}" \
    --member="$member" \
    --role=roles/storage.objectAdmin \
    --quiet >/dev/null || true
done

echo "==> secrets (generated once; copy values from Secret Manager after this)"
ensure_secret agent-api-key "$(openssl rand -hex 32)"
ensure_secret feed-username "podcast"
ensure_secret feed-password "$(openssl rand -hex 24)"
ensure_secret feed-token "$(openssl rand -hex 32)"

IMAGE_BASE="${REGION}-docker.pkg.dev/${PROJECT}/${REPO}"
echo
echo "Bootstrap done. Next (see docs/deployment.md):"
echo "  gcloud builds submit --project=$PROJECT --config=deploy/cloudbuild.yaml --substitutions=_REGION=$REGION,_TAG=latest --timeout=1800s"
echo "  # then deploy job podcaster-worker, then service podcaster"
echo
echo "  IMAGE_BASE=${IMAGE_BASE}"
echo "  JOB=$JOB_NAME  SERVICE=$SERVICE_NAME  SA=$SA  BUCKET=$BUCKET"
echo "  Read secrets:"
echo "    gcloud secrets versions access latest --secret=agent-api-key --project=$PROJECT"
