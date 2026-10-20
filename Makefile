.PHONY: dev build test test-system test-live

dev:
	python3 scripts/run-local.py go run ./cmd/platform -targets .local/targets.json
build:
	go build -o bin/oap ./cmd/oap
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

.PHONY: generate check-generated
generate:
	python3 scripts/generate.py
check-generated:
	python3 scripts/generate.py --check

.PHONY: rewrite-check
rewrite-check:
	scripts/rewrite-check.sh

.PHONY: oapd-image
oapd-image:
	mkdir -p .local/oapd-context
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-X main.Version=$$(git rev-parse --short HEAD)" -o .local/oapd-context/oapd-linux-amd64 ./cmd/oapd
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o .local/oapd-context/oap-ext-builder-linux-amd64 ./cmd/oap-ext-builder
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o .local/oapd-context/oap-ext-registry-linux-amd64 ./cmd/oap-ext-registry
	docker build -q --platform linux/amd64 -f cmd/oapd/Dockerfile -t oapd:$$(git rev-parse --short HEAD) .local/oapd-context

.PHONY: gen
gen:
	tools/install.sh >/dev/null
	rm -rf gen/oap
	.local/bin/buf generate
