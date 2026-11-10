#!/usr/bin/env bash
set -Eeuo pipefail
[[ ${OJ_PROJECT:-} == biue-deploy5-* ]] || exit 1
CODE="${OJ_DATA_DIR%/production}/code"
cp /source/deploy/Dockerfile "$CODE/deploy/Dockerfile"
cp /source/deploy/languages/Dockerfile "$CODE/deploy/languages/Dockerfile"
docker build --platform linux/arm64 -f "$CODE/deploy/Dockerfile" -t biue-app:arm64-0.1.0 "$CODE"
for language in cpp17 go python3 java javascript; do
  case "$language" in cpp17) base=gcc:14.2.0-bookworm;; go) base=golang:1.25.4-bookworm;; python3) base=python:3.12.10-slim-bookworm;; java) base=eclipse-temurin:21.0.7_6-jdk-jammy;; javascript) base=node:22.23.4-bookworm-slim;; esac
  docker build --platform linux/arm64 --build-arg LANGUAGE_IMAGE="$base" --build-arg LANGUAGE_ID="$language" -f "$CODE/deploy/languages/Dockerfile" -t "biue-lang-$language:arm64-0.1.0" "$CODE"
  docker run --rm --platform linux/arm64 --network none --read-only -e OJ_SANDBOX_SECRET=0000000000000000000000000000000000000000000000000000000000000000 "biue-lang-$language:arm64-0.1.0" | python3 -c 'import json,sys; value=json.load(sys.stdin); assert value; print("arm64 supervisor process started and returned a signed response")'
done
docker run --rm --platform linux/arm64 --network none --entrypoint python3 biue-lang-python3:arm64-0.1.0 --version
docker run --rm --platform linux/arm64 --network none --entrypoint g++ biue-lang-cpp17:arm64-0.1.0 --version
docker run --rm --platform linux/arm64 --network none --entrypoint go biue-lang-go:arm64-0.1.0 version
docker run --rm --platform linux/arm64 --network none --entrypoint java biue-lang-java:arm64-0.1.0 -version
docker run --rm --platform linux/arm64 --network none --entrypoint node biue-lang-javascript:arm64-0.1.0 --version
docker run --rm --platform linux/arm64 --network "${OJ_PROJECT}_backend" --env-file "$OJ_DATA_DIR/config/.env" --entrypoint /bin/sh biue-app:arm64-0.1.0 -c '
  /app/web & pid=$!
  trap "kill $pid 2>/dev/null || true" EXIT
  for attempt in $(seq 1 30); do
    if wget -q -O /dev/null http://127.0.0.1:8080/ready; then echo "arm64 Web /ready passed under emulation"; exit 0; fi
    sleep 1
  done
  exit 1'
bash /source/tools/check_arm64_ingress.sh
echo ARM64_BUILD_AND_STARTUP_PASSED
