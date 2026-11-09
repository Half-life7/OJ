ARG LANGUAGE_IMAGE=python:3.12.10-slim-bookworm
FROM golang:1.25.4-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -mod=readonly -trimpath -ldflags="-s -w" -o /out/oj-sandbox ./cmd/sandbox

FROM ${LANGUAGE_IMAGE}
ARG LANGUAGE_ID=python3
LABEL biue.judge.language=${LANGUAGE_ID}
RUN mkdir -p /work && chown 10001:10001 /work
COPY --from=builder /out/oj-sandbox /usr/local/bin/oj-sandbox
WORKDIR /work
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/oj-sandbox"]
