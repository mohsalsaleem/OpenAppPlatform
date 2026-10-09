.PHONY: dev build test test-system test-live

dev:
	python3 scripts/run-local.py go run ./cmd/platform -targets .local/targets.json
build:
	npm --prefix web run build
	python3 scripts/run-local.py go build -o bin/platform ./cmd/platform
	python3 scripts/run-local.py go build -o bin/oap-mcp ./cmd/oap-mcp
test:
	python3 scripts/run-local.py go test -race ./...
test-system:
	python3 scripts/run-local.py go test -race -v ./tests/system -run 'TestApplication|TestInterrupted|TestPartial'
test-live:
	OAP_LIVE_COOLIFY=1 python3 scripts/run-local.py go test -v ./tests/system -run TestLiveCoolifyLifecycle -count=1 -timeout=10m

.PHONY: test-docker
test-docker:
	OAP_TEST_DOCKER_SOCKET=$${OAP_TEST_DOCKER_SOCKET:-/var/run/docker.sock} python3 scripts/run-local.py go test -v ./tests/system -run TestLiveDockerLifecycleOffline -count=1 -timeout=3m

.PHONY: test-local install-test-hook
test-local:
	python3 scripts/test-env.py run
install-test-hook:
	python3 scripts/test-env.py install-hook
