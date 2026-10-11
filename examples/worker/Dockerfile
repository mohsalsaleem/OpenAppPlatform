FROM golang:1.26.5-alpine AS build
WORKDIR /src
COPY main.go ./main.go
RUN CGO_ENABLED=0 go build -trimpath -o /worker main.go
FROM alpine:3.23
RUN adduser -D -u 10001 worker
USER worker
COPY --from=build /worker /worker
HEALTHCHECK --interval=2s --timeout=2s --retries=3 CMD ["/worker", "check-health"]
ENTRYPOINT ["/worker"]
