ARG GO_IMAGE
ARG BROWSER_IMAGE
FROM ${GO_IMAGE} AS core
LABEL io.openappplatform.test-cache="true"
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
# Compile only Go-package inputs. Root docs and UI edits must not regenerate
# the large Go race/compiler cache layer on every verification run.
COPY cmd/ cmd/
COPY internal/ internal/
COPY tests/ tests/
COPY examples/ examples/
RUN go vet ./... && mkdir -p /opt/oap/tests && \
    go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./... | sed '/^$/d' > /opt/oap/packages.txt && \
    index=0; while read -r package; do \
      go test -c -race -o "/opt/oap/tests/$index.test" "$package" || exit 1; \
      index=$((index+1)); \
    done < /opt/oap/packages.txt
RUN CGO_ENABLED=0 go build -trimpath -o /opt/oap/platform ./cmd/platform && \
    CGO_ENABLED=0 go build -trimpath -o /opt/oap/oap-mcp ./cmd/oap-mcp && \
    CGO_ENABLED=0 go build -trimpath -ldflags '-X main.version=v1 -X main.port=8080' -o /opt/oap/fixture-v1 ./tests/env/fixture && \
    CGO_ENABLED=0 go build -trimpath -ldflags '-X main.version=v2 -X main.port=8025' -o /opt/oap/fixture-v2 ./tests/env/fixture

FROM ${BROWSER_IMAGE} AS runner
LABEL io.openappplatform.test-cache="true"
WORKDIR /workspace
COPY web/package.json web/package-lock.json ./web/
RUN PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 npm --prefix web ci
COPY . .
COPY --from=core /opt/oap /opt/oap
RUN node -e 'const p=require("./web/node_modules/@playwright/test/package.json");if(p.version!=="1.64.0")throw Error("Playwright browser/package versions must match")' && npm --prefix web run build
ENTRYPOINT ["node", "/workspace/tests/env/run.mjs"]
