FROM node:22-alpine AS web
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 go build -trimpath -o /platform ./cmd/platform && CGO_ENABLED=0 go build -trimpath -o /oap-mcp ./cmd/oap-mcp && CGO_ENABLED=0 go build -trimpath -o /oap-admin ./cmd/oap-admin && CGO_ENABLED=0 go build -trimpath -o /oap-build-client ./cmd/oap-build-client

FROM alpine:3.23
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 platform
WORKDIR /app
COPY --from=backend /platform /app/platform
COPY --from=backend /oap-mcp /app/oap-mcp
COPY --from=backend /oap-admin /app/oap-admin
COPY --from=backend /oap-build-client /app/oap-build-client
COPY --from=web /web/dist /app/web/dist
USER platform
ENV OAP_ADDR=0.0.0.0:8787
EXPOSE 8787
HEALTHCHECK --interval=30s --timeout=3s CMD wget -q -O /dev/null http://127.0.0.1:8787/healthz || exit 1
ENTRYPOINT ["/app/platform"]
