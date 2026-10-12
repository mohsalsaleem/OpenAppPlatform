FROM golang:1.26.5-alpine AS build
WORKDIR /src
COPY main.go .
RUN CGO_ENABLED=0 go build -trimpath -o /persistent-web main.go

FROM alpine:3.23
RUN adduser -D -u 10001 application && mkdir /data && chown application:application /data
COPY --from=build /persistent-web /persistent-web
USER application
EXPOSE 8080
HEALTHCHECK --interval=5s --timeout=2s --retries=3 CMD wget -q -O /dev/null http://127.0.0.1:8080/health/ready || exit 1
ENTRYPOINT ["/persistent-web"]
