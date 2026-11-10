#!/usr/bin/env bash
set -Eeuo pipefail
mkdir -p /tmp/extensions-source
tar -xf /tmp/extensions-source.tar -C /tmp/extensions-source
CODE=/tmp/extensions-source
docker build --platform linux/arm64 -f "$CODE/deploy/Dockerfile" -t biue-app:arm64-extensions "$CODE"
docker build --platform linux/arm64 --build-arg LANGUAGE_IMAGE=node:22.23.4-bookworm-slim --build-arg LANGUAGE_ID=javascript -f "$CODE/deploy/languages/Dockerfile" -t biue-lang-javascript:arm64-extensions "$CODE"
docker run --rm --platform linux/arm64 --network none --entrypoint node biue-lang-javascript:arm64-extensions --version
docker run --rm --platform linux/arm64 --network none --read-only -e OJ_SANDBOX_SECRET=0000000000000000000000000000000000000000000000000000000000000000 biue-lang-javascript:arm64-extensions | python3 -c 'import json,sys; assert json.load(sys.stdin); print("Node arm64 supervisor startup passed")'
echo EXTENSION_ARM64_BUILD_STARTUP_PASSED
