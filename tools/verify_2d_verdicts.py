"""HTTP-to-database verdict acceptance, including zero-score Run output limits."""
import io
import uuid
import zipfile


def verify_verdicts(request, await_result):
    problem = request("/api/admin/problems", {
        "title": "2D verdict acceptance " + uuid.uuid4().hex[:10],
        "description": "Print ok.", "input_format": "empty", "output_format": "ok",
        "sample_input": "", "sample_output": "ok\n", "time_limit_ms": 2000,
        "memory_limit_mb": 64, "difficulty": "easy", "tags": ["2d-verdict-acceptance"],
        "status": "published", "visibility": "public",
    })
    memory = io.BytesIO()
    with zipfile.ZipFile(memory, "w", compression=zipfile.ZIP_DEFLATED) as writer:
        writer.writestr("1.in", b"")
        writer.writestr("1.out", b"ok\n")
    boundary = "biue-" + uuid.uuid4().hex
    body = bytearray()
    for name, value in {"revision": str(problem["revision"]), "scores": '{"1":100}'}.items():
        body.extend(f'--{boundary}\r\nContent-Disposition: form-data; name="{name}"\r\n\r\n{value}\r\n'.encode())
    body.extend(f'--{boundary}\r\nContent-Disposition: form-data; name="archive"; filename="cases.zip"\r\nContent-Type: application/zip\r\n\r\n'.encode())
    body.extend(memory.getvalue())
    body.extend(f'\r\n--{boundary}--\r\n'.encode())
    request(f'/api/admin/problems/{problem["id"]}/data-versions', bytes(body), f"multipart/form-data; boundary={boundary}")
    record = {"problem_id": problem["id"]}
    for language, source, expected in (
        ("cpp17", "not valid C++", "CompileError"),
        ("python3", "print('wrong')", "WrongAnswer"),
        ("python3", "raise RuntimeError('expected')", "RuntimeError"),
        ("python3", "while True: pass", "TimeLimitExceeded"),
        ("python3", "x=bytearray(512*1024*1024);print(len(x))", "MemoryLimitExceeded"),
    ):
        item = request("/api/submissions", {"problem_id": problem["id"], "language": language, "source_code": source})
        result = await_result(item)
        if result["status"] != expected or result["score"] != 0 or result.get("output") or result.get("stdin"):
            raise RuntimeError(f'HTTP verdict {expected} returned {result["status"]}')
        record[expected] = result["id"]
        print(f'HTTP {expected}: passed, submission={result["id"]}', flush=True)
    item = request("/api/runs", {"problem_id": problem["id"], "language": "python3", "source_code": "print('x'*200000)", "stdin": ""})
    result = await_result(item)
    if result["status"] != "RuntimeError" or result["score"] != 0 or len(result.get("output", "").encode()) > 65536:
        raise RuntimeError("Run output limit failed")
    record["output_limit_run"] = result["id"]
    print("HTTP Run output limit: passed", flush=True)
    return record
