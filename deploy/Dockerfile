FROM golang:1.25.4-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -mod=readonly -trimpath -ldflags="-s -w" -o /out/web ./cmd/web \
    && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -mod=readonly -trimpath -ldflags="-s -w" -o /out/ojctl ./cmd/ojctl

FROM alpine:3.22.2 AS runtime
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -g 10001 app \
    && adduser -D -H -u 10001 -G app app \
    && mkdir -p /app/oj-data/problem-data \
    && chown -R 10001:10001 /app/oj-data
WORKDIR /app
COPY --from=builder --chown=10001:10001 /out/web /out/ojctl /app/
USER 10001:10001
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q --spider "http://127.0.0.1:${APP_PORT:-8080}/health" || exit 1
ENTRYPOINT ["/app/web"]
