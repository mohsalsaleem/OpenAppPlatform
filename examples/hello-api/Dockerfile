FROM golang:1.26-alpine AS build
WORKDIR /src
COPY main.go .
RUN CGO_ENABLED=0 go build -trimpath -o /hello-api main.go
FROM alpine:3.23
RUN adduser -D -u 10001 app
USER app
COPY --from=build /hello-api /hello-api
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=2s CMD wget -q -O /dev/null http://127.0.0.1:8080/health/ready || exit 1
ENTRYPOINT ["/hello-api"]
