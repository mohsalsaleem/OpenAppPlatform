.PHONY: dev build test test-system test-live

dev:
	python3 scripts/run-local.py go run ./cmd/platform -targets .local/targets.json
build:
	npm --prefix web run build
	python3 scripts/run-local.py go build -o bin/platform ./cmd/platform
test:
	python3 scripts/run-local.py go test -race ./...
test-system:
	python3 scripts/run-local.py go test -race -v ./tests/system -run 'TestApplication|TestInterrupted|TestPartial'
test-live:
	OAP_LIVE_COOLIFY=1 python3 scripts/run-local.py go test -v ./tests/system -run TestLiveCoolifyLifecycle -count=1 -timeout=10m
