FROM golang:1.26.5-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=0 go build -trimpath -o /application main.go
FROM alpine:3.23
RUN apk add --no-cache ca-certificates curl && adduser -D -u 10001 application
USER application
COPY --from=build /application /application
HEALTHCHECK --interval=2s --timeout=3s --retries=3 --start-period=5s CMD ["/application","check-health"]
ENTRYPOINT ["/application"]
