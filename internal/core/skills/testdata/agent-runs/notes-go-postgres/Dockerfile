# Suggested Dockerfile for the API image (ghcr.io/example/notes). Not needed by
# OAP if CI already publishes the image; shown for reference. No secrets here.
FROM golang:1.24.6-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/notes ./cmd/notes

FROM alpine:3.20
# busybox wget is present, so OAP's http health check (GET /healthz) works.
RUN adduser -D -u 10001 app
COPY --from=build /out/notes /app/notes
USER app
EXPOSE 8080
ENTRYPOINT ["/app/notes"]
