.PHONY: build test
build:
	go build -o bin/oapctl ./cmd/oapctl
	go build -o bin/oapd ./cmd/oapd
test:
	go test -race ./...

.PHONY: rewrite-check
rewrite-check:
	scripts/rewrite-check.sh

.PHONY: oapd-image
oapd-image:
	mkdir -p .local/oapd-context
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-X main.Version=$$(git rev-parse --short HEAD)" -o .local/oapd-context/oapd-linux-amd64 ./cmd/oapd
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o .local/oapd-context/oap-ext-builder-linux-amd64 ./cmd/oap-ext-builder
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o .local/oapd-context/oap-ext-registry-linux-amd64 ./cmd/oap-ext-registry
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o .local/oapd-context/oap-ext-github-linux-amd64 ./cmd/oap-ext-github
	docker build -q --platform linux/amd64 -f cmd/oapd/Dockerfile -t oapd:$$(git rev-parse --short HEAD) .local/oapd-context

.PHONY: gen
gen:
	tools/install.sh >/dev/null
	rm -rf gen/oap
	.local/bin/buf generate

.PHONY: release
release:
	scripts/release.sh $(VERSION)
