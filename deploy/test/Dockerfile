ARG TEST_BASE=python:3.12.10-slim-bookworm
FROM ${TEST_BASE}
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    bash ca-certificates curl python3 openssl gzip tar coreutils util-linux iproute2 \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /source
ENTRYPOINT ["bash"]
