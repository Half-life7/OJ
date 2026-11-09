"""Opt-in failure tests ONLY for an explicitly named biue-2d-* project.

Pauses its own worker, kills/restarts its own runner, flushes its isolated Redis
and temporarily moves one newly-created test dataset. Never removes volumes.
"""
import io
import json
import os
from pathlib import Path
import re
import subprocess
import time
import uuid
import zipfile


def verify_recovery(request, await_result, problem, sources, actor):
    project = os.environ.get("BIUE_SMOKE_PROJECT", "")
    if not re.fullmatch(r"biue-2d-[a-z0-9-]+", project):
        raise RuntimeError("failure tests require an explicit isolated BIUE_SMOKE_PROJECT=biue-2d-...")
    env_file = Path(os.environ.get("BIUE_SMOKE_ENV_FILE", ".cache/2d-acceptance.env")).resolve()
    root = Path(__file__).resolve().parent.parent
    if not env_file.is_relative_to(root / ".cache") or not env_file.is_file():
        raise RuntimeError("failure-test env file must be inside workspace .cache")
    base = ["docker", "compose", "--env-file", str(env_file), "-p", project, "-f", str(root / "deploy/docker-compose.yml")]

    def command(*args):
        result = subprocess.run(base + list(args), cwd=root, capture_output=True, text=True, timeout=150)
        if result.returncode:
            raise RuntimeError(f"isolated Compose operation failed: {args[0]}")
        return result.stdout.strip()

    def sql(query, expected_error=None):
        shell = 'MYSQL_PWD="$MYSQL_PASSWORD" exec mysql --user="$MYSQL_USER" --database="$MYSQL_DATABASE" --batch --skip-column-names --execute="$1"'
        result = subprocess.run(base + ["exec", "-T", "mysql", "sh", "-c", shell, "sh", query], cwd=root, capture_output=True, text=True, timeout=30)
        if bool(result.returncode) != (expected_error is not None):
            raise RuntimeError("MySQL acceptance query/constraint check failed")
        if expected_error is not None and f"ERROR {expected_error} " not in result.stderr:
            raise RuntimeError("unexpected MySQL constraint error")
        return result.stdout.strip()

    def redis(*args):
        shell = 'REDISCLI_AUTH="$REDIS_PASSWORD" exec redis-cli "$@"'
        return command("exec", "-T", "redis", "sh", "-c", shell, "sh", *map(str, args))

    def wait_for(fn, message, timeout=180):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            value = fn()
            if value:
                return value
            time.sleep(0.5)
        raise RuntimeError(message)

    def submit(source):
        return request("/api/submissions", {"problem_id": problem["id"], "language": "python3", "source_code": source})

    def accepted(submission, generation=1):
        result = await_result(submission)
        if result["status"] != "Accepted" or result["score"] != 100 or result["generation"] != generation:
            raise RuntimeError(f'expected recovered AC, got {result["status"]}')
        return result

    def upload(revision):
        memory = io.BytesIO()
        with zipfile.ZipFile(memory, "w", compression=zipfile.ZIP_DEFLATED) as writer:
            for name, data in {"1.in": "1 2\n", "1.out": "103\n", "2.in": "-4 9\n", "2.out": "105\n"}.items():
                writer.writestr(name, data)
        boundary = "biue-" + uuid.uuid4().hex
        body = bytearray()
        for name, value in {"revision": str(revision), "scores": '{"1":40,"2":60}'}.items():
            body.extend(f'--{boundary}\r\nContent-Disposition: form-data; name="{name}"\r\n\r\n{value}\r\n'.encode())
        body.extend(f'--{boundary}\r\nContent-Disposition: form-data; name="archive"; filename="cases.zip"\r\nContent-Type: application/zip\r\n\r\n'.encode())
        body.extend(memory.getvalue())
        body.extend(f'\r\n--{boundary}--\r\n'.encode())
        request(f'/api/admin/problems/{problem["id"]}/data-versions', bytes(body), f"multipart/form-data; boundary={boundary}")

    record = {}
    command("stop", "worker")
    try:
        pinned = submit(sources["python3"])
        pinned_run = request("/api/runs", {"problem_id": problem["id"], "language": "python3", "source_code": sources["python3"]})
        current = request(f'/api/admin/problems/{problem["id"]}')
        fields = ["title", "description", "input_format", "output_format", "sample_input", "sample_output", "time_limit_ms", "memory_limit_mb", "difficulty", "tags", "status", "visibility", "revision"]
        edit = {name: current[name] for name in fields}
        edit["description"], edit["output_format"] = "Add two integers, then add 100.", "a+b+100"
        edit["sample_input"], edit["sample_output"] = "8 9\n", "117\n"
        current = request(f'/api/admin/problems/{problem["id"]}', edit, method="PUT")
        upload(current["revision"])
    finally:
        command("start", "worker")
    old = accepted(pinned)
    old_run = await_result(pinned_run)
    if old["data_version"] != 1 or old["statement_version"] != 1 or old_run.get("output", "").strip() != "3":
        raise RuntimeError("fixed-version or saved Run input was replaced")
    wrong = await_result(submit(sources["python3"]))
    if wrong["status"] != "WrongAnswer":
        raise RuntimeError("new submission did not use replacement dataset")
    record["pinned_submission"] = old["id"]
    print("fixed statement/data versions and saved Run input: passed", flush=True)

    current_source = "a,b=map(int,input().split());print(a+b+100)"
    slow_source = "import time\na,b=map(int,input().split());time.sleep(1);print(a+b+100)"
    for service in ("worker", "runner"):
        item = submit(slow_source)
        wait_for(lambda: request(f'/api/submissions/{item["id"]}')["status"] == "Running", "task did not enter Running", 60)
        try:
            command("kill", "-s", "SIGKILL", service)
        finally:
            command("start", service)
        recovered = accepted(item)
        record[service + "_crash"] = recovered["id"]
        print(f"{service} crash recovery: passed", flush=True)

    command("stop", "worker")
    moved = False
    mover = 'test -d "$1" && test ! -e "$2" && mv -- "$1" "$2"'
    try:
        dead_item = submit(current_source)
        key = sql(f'SELECT storage_key FROM problem_data_versions WHERE problem_id={int(problem["id"])} AND data_version=2')
        if not re.fullmatch(r"[0-9a-f]{32}", key):
            raise RuntimeError("unexpected generated dataset key")
        source = "/app/oj-data/problem-data/" + key + "/data"
        paused = "/app/oj-data/problem-data/" + key + "/acceptance-paused"
        command("exec", "-T", "web", "sh", "-c", mover, "sh", source, paused)
        moved = True
        command("start", "worker")
        result = await_result(dead_item)
        if result["status"] != "SystemError":
            raise RuntimeError("fourth infrastructure failure did not terminate")
        failures = sql(f'SELECT COUNT(*) FROM judge_attempt_failures WHERE submission_id={int(dead_item["id"])} AND generation=1')
        if failures != "4":
            raise RuntimeError("retry budget was not exactly four attempts")
        rows = json.loads(command("run", "--rm", "migrate", "judge", "dead-list", "--actor-user-id", str(actor)))
        dead = next(row for row in rows if row["submission_id"] == dead_item["id"])
        def dead_published():
            entries = json.loads(redis("--json", "XRANGE", "oj:judge:dead", "-", "+"))
            return any(dict(zip(fields[::2], fields[1::2])).get("dead_letter_id") == str(dead["id"]) for _, fields in entries)
        wait_for(dead_published, "dead stream was not published", 30)
    finally:
        try:
            if moved:
                command("exec", "-T", "web", "sh", "-c", mover, "sh", paused, source)
        finally:
            command("start", "worker")
    retried = json.loads(command("run", "--rm", "migrate", "judge", "retry", "--actor-user-id", str(actor), "--dead-letter-id", str(dead["id"]), "--generation", "1"))
    manual = accepted(dead_item, 2)
    if sql(f'SELECT COUNT(*) FROM submission_results WHERE submission_id={int(dead_item["id"])}') != "2":
        raise RuntimeError("manual retry replaced old result history")
    record["dead_and_manual_retry"] = manual["id"]
    print("three retries, dead stream and audited new-generation retry: passed", flush=True)

    redis("XADD", "oj:judge", "*", "task_id", retried["task_id"], "submission_id", retried["submission_id"], "generation", 2, "attempt", 1)
    # Let the new message be read before examining its pending/ACK state.
    time.sleep(2)
    wait_for(lambda: redis("XPENDING", "oj:judge", "judgers").splitlines()[0] == "0", "duplicate remained pending", 30)
    if sql(f'SELECT COUNT(*) FROM submission_results WHERE submission_id={int(dead_item["id"])}') != "2":
        raise RuntimeError("duplicate message appended another result")
    print("duplicate terminal message: passed", flush=True)

    command("stop", "worker")
    try:
        lost = submit(current_source)
        wait_for(lambda: sql(f'SELECT COUNT(*) FROM judge_task_outbox WHERE submission_id={int(lost["id"])} AND dispatched_at IS NOT NULL') == "1", "intent was not dispatched", 30)
        if redis("FLUSHDB") != "OK":
            raise RuntimeError("isolated Redis loss simulation failed")
    finally:
        command("start", "worker")
    accepted(lost)
    record["redis_loss"] = lost["id"]
    print("Redis data loss and MySQL reconciliation: passed", flush=True)

    sql("START TRANSACTION; UPDATE judge_tasks SET attempt=5 WHERE id=" + str(int(retried["task_id"])) + "; ROLLBACK;", expected_error=3819)
    sql("START TRANSACTION; DELETE FROM problem_versions WHERE problem_id=" + str(int(problem["id"])) + " AND version=1; ROLLBACK;", expected_error=1451)
    print("real MySQL CHECK and historical foreign key protection: passed", flush=True)
    return record
