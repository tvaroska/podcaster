GO        ?= go
MODULE    := github.com/tvaroska/podcaster
BIN_DIR   := bin
SERVER    := $(BIN_DIR)/server
WORKER    := $(BIN_DIR)/worker

.PHONY: all build test vet fmt tidy run worker smoke docker-up docker-down clean

all: test build

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

build: $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build -o $(SERVER) ./cmd/server
	CGO_ENABLED=0 $(GO) build -o $(WORKER) ./cmd/worker

test:
	CGO_ENABLED=0 $(GO) test ./...

vet:
	CGO_ENABLED=0 $(GO) vet ./...

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

run: build
	PODCASTER_DEV=1 TTS_ENGINE=mock STORE_BACKEND=sqlite STORAGE_BACKEND=local JOB_BACKEND=local \
		$(SERVER)

worker: build
	@test -n "$(EPISODE_ID)" || (echo "EPISODE_ID is required" && exit 2)
	PODCASTER_DEV=1 TTS_ENGINE=mock STORE_BACKEND=sqlite STORAGE_BACKEND=local JOB_BACKEND=local \
		EPISODE_ID=$(EPISODE_ID) $(WORKER)

smoke: build
	@command -v curl >/dev/null
	@echo "==> posting episode"
	@ID=$$(curl -sS -X POST http://localhost:8080/v1/episodes \
		-H "Authorization: Bearer $${AGENT_API_KEY:-dev-agent-key}" \
		-H "Content-Type: application/json" \
		-d '{"title":"Smoke Test","content":"Good morning. This is a smoke-test briefing from make smoke."}' \
		| $(GO) run ./tools/jsonget episode_id); \
	echo "episode_id=$$ID"; \
	echo "==> waiting for READY"; \
	for i in 1 2 3 4 5 6 7 8 9 10; do \
		STATUS=$$(curl -sS http://localhost:8080/v1/episodes/$$ID \
			-H "Authorization: Bearer $${AGENT_API_KEY:-dev-agent-key}" \
			| $(GO) run ./tools/jsonget status); \
		echo "status=$$STATUS"; \
		if [ "$$STATUS" = "READY" ]; then break; fi; \
		sleep 0.5; \
	done; \
	curl -sS -u "$${FEED_USERNAME:-podcast}:$${FEED_PASSWORD:-podcast}" http://localhost:8080/podcast.xml | head -n 30

docker-up:
	docker compose up --build

docker-down:
	docker compose down

clean:
	rm -rf $(BIN_DIR)
