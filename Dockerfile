# syntax=docker/dockerfile:1

FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN mkdir -p /out/data \
 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

FROM gcr.io/distroless/static-debian12:nonroot AS server
COPY --from=build /out/server /server
COPY --from=build --chown=65532:65532 /out/data /data
USER nonroot:nonroot
EXPOSE 8080
ENV LISTEN_ADDR=:8080
ENTRYPOINT ["/server"]

FROM debian:bookworm-slim AS worker
RUN apt-get update && apt-get install -y --no-install-recommends \
        bzip2 ca-certificates curl ffmpeg tar \
    && rm -rf /var/lib/apt/lists/*

ARG SHERPA_ONNX_VERSION=1.12.14
ARG KOKORO_MODEL_ARCHIVE=kokoro-multi-lang-v1_0
ARG SHERPA_ONNX_URL=https://github.com/k2-fsa/sherpa-onnx/releases/download/v${SHERPA_ONNX_VERSION}/sherpa-onnx-v${SHERPA_ONNX_VERSION}-linux-x64-static.tar.bz2
ARG KOKORO_MODEL_URL=https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/${KOKORO_MODEL_ARCHIVE}.tar.bz2

RUN mkdir -p /opt/sherpa-onnx /opt/kokoro \
    && curl -fsSL -o /tmp/sherpa-onnx.tar.bz2 "${SHERPA_ONNX_URL}" \
    && tar -xjf /tmp/sherpa-onnx.tar.bz2 -C /opt/sherpa-onnx --strip-components=1 \
        "sherpa-onnx-v${SHERPA_ONNX_VERSION}-linux-x64-static/bin/sherpa-onnx-offline-tts" \
    && curl -fsSL -o /tmp/kokoro.tar.bz2 "${KOKORO_MODEL_URL}" \
    && tar -xjf /tmp/kokoro.tar.bz2 -C /opt/kokoro \
    && printf '你好 n i3 h ao3\n' > "/opt/kokoro/${KOKORO_MODEL_ARCHIVE}/lexicon-zh.txt" \
    && rm -f /tmp/sherpa-onnx.tar.bz2 /tmp/kokoro.tar.bz2 \
        "/opt/kokoro/${KOKORO_MODEL_ARCHIVE}"/*.wav \
        "/opt/kokoro/${KOKORO_MODEL_ARCHIVE}"/*.py \
    && /opt/sherpa-onnx/bin/sherpa-onnx-offline-tts \
        --kokoro-model="/opt/kokoro/${KOKORO_MODEL_ARCHIVE}/model.onnx" \
        --kokoro-voices="/opt/kokoro/${KOKORO_MODEL_ARCHIVE}/voices.bin" \
        --kokoro-tokens="/opt/kokoro/${KOKORO_MODEL_ARCHIVE}/tokens.txt" \
        --kokoro-data-dir="/opt/kokoro/${KOKORO_MODEL_ARCHIVE}/espeak-ng-data" \
        --kokoro-dict-dir="/opt/kokoro/${KOKORO_MODEL_ARCHIVE}/dict" \
        --kokoro-lexicon="/opt/kokoro/${KOKORO_MODEL_ARCHIVE}/lexicon-us-en.txt,/opt/kokoro/${KOKORO_MODEL_ARCHIVE}/lexicon-zh.txt" \
        --tts-max-num-sentences=1 \
        --num-threads=2 \
        --sid=3 \
        --output-filename=/tmp/smoke.wav \
        "Build check." \
    && test -s /tmp/smoke.wav \
    && rm -f /tmp/smoke.wav \
    && useradd --system --uid 65532 --create-home nonroot

COPY --from=build /out/worker /worker
USER nonroot
ENV KOKORO_BIN=/opt/sherpa-onnx/bin/sherpa-onnx-offline-tts \
    KOKORO_MODEL_DIR=/opt/kokoro/kokoro-multi-lang-v1_0 \
    DEFAULT_VOICE=af_heart \
    FFMPEG_BIN=ffmpeg \
    TTS_ENGINE=kokoro
ENTRYPOINT ["/worker"]
