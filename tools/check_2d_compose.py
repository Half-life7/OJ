#!/usr/bin/env python3
"""Validate `docker compose config --format json` from stdin without printing secrets."""
import json
import sys

services = json.load(sys.stdin)["services"]
for name in ("web", "worker", "dispatcher", "migrate"):
    assert not any("docker.sock" in v.get("source", "") or "docker.sock" in v.get("target", "") for v in services[name].get("volumes", [])), name
for name in ("web", "dispatcher", "migrate"):
    assert "RUNNER_TOKEN" not in services[name].get("environment", {}), name
assert "MYSQL_PASSWORD" not in services["runner"].get("environment", {})
assert set(services["runner"]["networks"]) == {"judge"}
assert not services["runner"].get("ports")
assert set(services["worker"]["networks"]) == {"judge", "backend"}
print("Compose role/network/socket/credential separation OK")
