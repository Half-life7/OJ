#!/usr/bin/env python3
"""Opt-in HTTP acceptance against a real deployment; preserves created history.

BIUE_URL must match PUBLIC_URL. Set BIUE_SMOKE_PASSWORD for an initialized
super administrator; BIUE_SMOKE_USERNAME defaults to admin. No secrets printed.
"""
import http.cookiejar
import io
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
import zipfile


def main():
    origin = os.environ.get("BIUE_URL", "http://localhost:8080").rstrip("/")
    password = os.environ.get("BIUE_SMOKE_PASSWORD", "")
    if not password:
        raise RuntimeError("BIUE_SMOKE_PASSWORD is required for an initialized administrator")
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def request(path, data=None, content_type="application/json", method=None):
        headers = {"Origin": origin, "Accept": "application/json"}
        if data is not None:
            csrf = request("/api/auth/csrf")["csrf_token"]
            headers.update({"X-CSRF-Token": csrf, "Content-Type": content_type})
            if isinstance(data, dict):
                data = json.dumps(data).encode()
        req = urllib.request.Request(origin + path, data=data, headers=headers, method=method)
        try:
            with opener.open(req, timeout=30) as response:
                result = json.load(response)
        except urllib.error.HTTPError as error:
            raise RuntimeError(f"HTTP {error.code} for {path}") from None
        if result.get("code") != 0:
            raise RuntimeError(f"API operation failed for {path}")
        return result["data"]

    actor = request("/api/auth/login", {"username": os.environ.get("BIUE_SMOKE_USERNAME", "admin"), "password": password})
    problem = request("/api/admin/problems", {
        "title": "2D acceptance " + uuid.uuid4().hex[:12], "description": "Add two integers.",
        "input_format": "a b", "output_format": "a+b", "sample_input": "1 2\n", "sample_output": "3\n",
        "time_limit_ms": 3000, "memory_limit_mb": 256, "difficulty": "easy", "tags": ["2d-acceptance"],
        "status": "published", "visibility": "public",
    })
    archive = io.BytesIO()
    with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED) as writer:
        for name, value in {"1.in": "1 2\n", "1.out": "3\n", "2.in": "-4 9\n", "2.out": "5\n"}.items():
            writer.writestr(name, value)
    boundary = "biue-" + uuid.uuid4().hex
    body = bytearray()
    for name, value in {"revision": str(problem["revision"]), "scores": '{"1":40,"2":60}'}.items():
        body.extend(f'--{boundary}\r\nContent-Disposition: form-data; name="{name}"\r\n\r\n{value}\r\n'.encode())
    body.extend(f'--{boundary}\r\nContent-Disposition: form-data; name="archive"; filename="cases.zip"\r\nContent-Type: application/zip\r\n\r\n'.encode())
    body.extend(archive.getvalue())
    body.extend(f'\r\n--{boundary}--\r\n'.encode())
    request(f'/api/admin/problems/{problem["id"]}/data-versions', bytes(body), f"multipart/form-data; boundary={boundary}")
    sources = {
        "cpp17": "#include <iostream>\nint main(){long long a,b;std::cin>>a>>b;std::cout<<a+b<<'\\n';}",
        "go": 'package main\nimport("fmt";"os")\nfunc main(){var a,b int64;fmt.Fscan(os.Stdin,&a,&b);fmt.Println(a+b)}',
        "python3": "a,b=map(int,input().split());print(a+b)",
        "java": 'import java.util.Scanner; public class Main{public static void main(String[] a){Scanner s=new Scanner(System.in);System.out.println(s.nextLong()+s.nextLong());}}',
    }
    terminal = {"Accepted", "WrongAnswer", "TimeLimitExceeded", "MemoryLimitExceeded", "RuntimeError", "CompileError", "SystemError", "Cancelled"}

    def await_result(submission):
        deadline = time.monotonic() + 300
        while time.monotonic() < deadline:
            result = request(f'/api/submissions/{submission["id"]}')
            if result["status"] in terminal:
                return result
            time.sleep(1)
        raise RuntimeError("judging did not finish within 300 seconds")

    record = {"problem_id": problem["id"], "actor_id": actor["id"], "submissions": {}}
    for language, source in sources.items():
        submission = request("/api/submissions", {"problem_id": problem["id"], "language": language, "source_code": source})
        result = await_result(submission)
        if result["status"] != "Accepted" or result["score"] != 100 or result["passed_cases"] != 2:
            raise RuntimeError(f'{language} failed with {result["status"]}')
        if result.get("output") or result.get("stdin"):
            raise RuntimeError("hidden submission data was exposed")
        print(f'{language}: Accepted, submission={result["id"]}')
        record["submissions"][language] = result["id"]
    run = request("/api/runs", {"problem_id": problem["id"], "language": "python3", "source_code": sources["python3"], "stdin": "40 2\n"})
    result = await_result(run)
    if result["status"] != "Accepted" or result["score"] != 0 or result.get("output", "").strip() != "42":
        raise RuntimeError("Run input/output or zero-score contract failed")
    print(f'Run: Accepted, problem={problem["id"]}; records retained for inspection')
    record["run_id"] = result["id"]
    if os.environ.get("BIUE_SMOKE_VERDICTS") == "1":
        from verify_2d_verdicts import verify_verdicts
        record["verdicts"] = verify_verdicts(request, await_result)
    if os.environ.get("BIUE_SMOKE_RECOVERY") == "1":
        from verify_2d_recovery import verify_recovery
        record["recovery"] = verify_recovery(request, await_result, problem, sources, actor["id"])
    if os.environ.get("BIUE_SMOKE_RECORD"):
        with open(os.environ["BIUE_SMOKE_RECORD"], "w", encoding="utf-8") as target:
            json.dump(record, target, indent=2)


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(f"2D acceptance failed: {error}", file=sys.stderr)
        sys.exit(1)
