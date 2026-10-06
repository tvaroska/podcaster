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
        ca-certificates curl ffmpeg tar \
    && rm -rf /var/lib/apt/lists/*

ARG PIPER_VERSION=2023.11.14-2
ARG PIPER_VOICE=en_US-lessac-medium
ARG PIPER_VOICE_URL=https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/lessac/medium/en_US-lessac-medium.onnx
ARG PIPER_VOICE_CONFIG_URL=https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/lessac/medium/en_US-lessac-medium.onnx.json

RUN mkdir -p /opt/piper/voices \
    && curl -fsSL -o /tmp/piper.tar.gz \
        "https://github.com/rhasspy/piper/releases/download/${PIPER_VERSION}/piper_linux_x86_64.tar.gz" \
    && tar -xzf /tmp/piper.tar.gz -C /opt/piper --strip-components=1 \
    && curl -fsSL -o /opt/piper/voices/${PIPER_VOICE}.onnx "${PIPER_VOICE_URL}" \
    && curl -fsSL -o /opt/piper/voices/${PIPER_VOICE}.onnx.json "${PIPER_VOICE_CONFIG_URL}" \
    && rm -f /tmp/piper.tar.gz \
    && useradd --system --uid 65532 --create-home nonroot

COPY --from=build /out/worker /worker
USER nonroot
ENV PIPER_BIN=/opt/piper/piper \
    PIPER_MODEL=/opt/piper/voices/en_US-lessac-medium.onnx \
    PIPER_CONFIG=/opt/piper/voices/en_US-lessac-medium.onnx.json \
    FFMPEG_BIN=ffmpeg \
    TTS_ENGINE=piper
ENTRYPOINT ["/worker"]
