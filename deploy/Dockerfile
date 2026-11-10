ARG GO_IMAGE=golang:1.25.4-bookworm
ARG RUNTIME_IMAGE=alpine:3.22.2
FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS builder
WORKDIR /src
COPY go.mod go.sum ./
ARG GO_PROXY=https://proxy.golang.org,direct
RUN GOPROXY="$GO_PROXY" go mod download
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -mod=readonly -trimpath -ldflags="-s -w" -o /out/web ./cmd/web \
    && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -mod=readonly -trimpath -ldflags="-s -w" -o /out/ojctl ./cmd/ojctl \
    && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -mod=readonly -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker \
    && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -mod=readonly -trimpath -ldflags="-s -w" -o /out/dispatcher ./cmd/dispatcher \
    && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -mod=readonly -trimpath -ldflags="-s -w" -o /out/judge-runner ./cmd/judge-runner

FROM ${RUNTIME_IMAGE} AS runtime
ARG ALPINE_MIRROR=https://dl-cdn.alpinelinux.org/alpine
RUN case "$ALPINE_MIRROR" in https://*) ;; *) exit 1 ;; esac \
    && release="v$(cut -d. -f1,2 /etc/alpine-release)" \
    && printf '%s/%s/main\n%s/%s/community\n' "$ALPINE_MIRROR" "$release" "$ALPINE_MIRROR" "$release" > /etc/apk/repositories \
    && apk add --no-cache ca-certificates tzdata \
    && addgroup -g 10001 app \
    && adduser -D -H -u 10001 -G app app \
    && mkdir -p /app/oj-data/problem-data /app/oj-data/runner \
    && chown -R 10001:10001 /app/oj-data
WORKDIR /app
COPY --from=builder --chown=10001:10001 /out/web /out/ojctl /out/worker /out/dispatcher /out/judge-runner /app/
USER 10001:10001
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q --spider "http://127.0.0.1:${APP_PORT:-8080}/health" || exit 1
ENTRYPOINT ["/app/web"]
