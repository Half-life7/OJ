#!/usr/bin/env bash
set -Eeuo pipefail
[[ ${OJ_PROJECT:-} == biue-deploy5-* ]] || exit 1
CODE="${OJ_DATA_DIR%/production}/code"
docker build --platform linux/arm64 -f "$CODE/deploy/nginx/Dockerfile" -t biue-nginx:arm64-0.1.0 "$CODE"
docker run --rm --platform linux/arm64 --network "${OJ_PROJECT}_backend" --entrypoint nginx biue-nginx:arm64-0.1.0 -t
echo ARM64_INGRESS_STARTUP_PASSED
