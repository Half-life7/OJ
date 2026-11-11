"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

const root = path.resolve(__dirname, "..");
const script = fs.readFileSync(path.join(root, "static/submissions.js"), "utf8");
const template = ["problems.html", "submissions.html"].map((name) => fs.readFileSync(path.join(root, "templates", name), "utf8")).join("\n");

// Test actual page behavior with a small DOM and deterministic timer surface.
// This does not replace browser acceptance of layout and CodeMirror rendering.
class Element {
  constructor(tag = "div") {
    this.tag = tag; this.children = []; this.handlers = new Map(); this.attributes = new Map();
    this.hidden = false; this.disabled = false; this.value = ""; this.checked = false; this._text = "";
    const classes = new Set();
    this.classList = {
      toggle(name, force) {
        if (force === undefined) force = !classes.has(name);
        if (force) classes.add(name); else classes.delete(name);
        return force;
      },
      contains(name) { return classes.has(name); },
    };
  }
  set textContent(value) { this._text = String(value); this.children = []; }
  get textContent() { return this._text + this.children.map((child) => child.textContent).join(""); }
  set innerHTML(_) { throw new Error("Untrusted content must remain text"); }
  append(...children) { this.children.push(...children); }
  replaceChildren(...children) { this._text = ""; this.children = children; }
  setAttribute(key, value) { this.attributes.set(key, value); }
  querySelector(selector) {
    if (selector === ".loading-dot") return this.loadingDot || (this.loadingDot = new Element("span"));
    return null;
  }
  addEventListener(type, callback) { this.handlers.set(type, callback); }
  trigger(type) { return this.handlers.get(type)?.({ preventDefault() {}, target: this, currentTarget: this }); }
}

class TestFormData {
  constructor(form) { this.data = new Map([...form.fields].map(([name, field]) => [name, field.value])); }
  [Symbol.iterator]() { return this.data.entries(); }
}

function boot(page, respond, options = {}) {
  const elements = new Map();
  for (const match of template.matchAll(/<(\w+)\b[^>]*\bid="([^"]+)"[^>]*>/g)) {
    const element = new Element(match[1]);
    element.hidden = /\bhidden\b/.test(match[0]);
    element.disabled = /\bdisabled\b/.test(match[0]);
    element.checked = /\bchecked\b/.test(match[0]);
    elements.set(match[2], element);
  }
  for (const match of template.matchAll(/<form\b[^>]*id="([^"]+)"[^>]*>([\s\S]*?)<\/form>/g)) {
    const form = elements.get(match[1]); form.fields = new Map();
    for (const field of match[2].matchAll(/<(input|textarea|select)\b([^>]*\bname="([^"]+)"[^>]*)>/g)) {
      const id = /\bid="([^"]+)"/.exec(field[2])[1];
      form.fields.set(field[3], elements.get(id));
    }
    form.elements = { namedItem: (name) => form.fields.get(name) };
  }
  const calls = [], timers = new Map(), handlers = new Map();
  let nextTimer = 0, reloads = 0;
  const document = {
    body: { dataset: { page }, classList: { toggle() {} } }, title: "Biue OJ",
    getElementById(id) { assert.ok(elements.has(id), "missing template ID " + id); return elements.get(id); },
    createElement: (tag) => new Element(tag),
  };
  const window = {
    location: { pathname: options.pathname || (page === "problem" ? "/problems/7" : page === "submission" ? "/submissions/11" : "/submissions"), search: options.search || "", reload() { reloads++; } },
    history: { replaceState() {} },
    setTimeout(callback, delay) { const id = ++nextTimer; timers.set(id, { callback, delay }); return id; },
    clearTimeout(id) { timers.delete(id); },
    addEventListener(type, callback) { if (!handlers.has(type)) handlers.set(type, []); handlers.get(type).push(callback); },
    BiueEditor: options.editor,
  };
  vm.runInNewContext(script, {
    document, window, URLSearchParams, TextEncoder, AbortController, FormData: TestFormData,
    fetch: async (url, init) => {
      calls.push({ url, ...init });
      const result = await respond(url, init);
      const status = result.status || 200;
      return { ok: status < 300, status, json: async () => ({ code: status < 300 ? 0 : status * 100 + 1, message: result.message || "请求失败", data: result.data ?? null }) };
    },
  }, { filename: "submissions.js" });
  return {
    elements, calls, timers, document, window,
    dispatch(type, event = {}) { for (const callback of handlers.get(type) || []) callback(event); },
    get reloads() { return reloads; },
    async tick() {
      const entry = [...timers].sort((a, b) => a[1].delay - b[1].delay)[0];
      assert.ok(entry, "expected scheduled timer"); timers.delete(entry[0]);
      await entry[1].callback(); await settle();
    },
  };
}

async function settle() { for (let i = 0; i < 8; i++) await new Promise((resolve) => setImmediate(resolve)); }

const account = { id: 2, account_type: "permanent", global_role: "user" };
const pending = { id: 11, user_id: 2, problem_id: 7, kind: "Submit", language: "go", generation: 1, status: "Pending", score: 0, execution_time_ms: 0, memory_usage_kb: 0 };
function composerResponses(custom, user = account, dataVersion = 1) {
  return async (url, init) => {
    if (url === "/api/problems/7") return { data: { id: 7, data_version: dataVersion } };
    if (url === "/api/languages") return { data: [{ id: "go", name: "Go" }, { id: "python3", name: "Python 3" }] };
    if (url === "/api/users/me") return user ? { data: user } : { status: 401 };
    if (url === "/api/auth/csrf") return { data: { csrf_token: "csrf-token" } };
    return custom(url, init);
  };
}

test("Run and Submit send distinct bodies with current source and CSRF", async () => {
  const ui = boot("problem", composerResponses(async (url) => ({ data: { ...pending, kind: url === "/api/runs" ? "Run" : "Submit", status: "Accepted" } })));
  await settle();
  ui.elements.get("source-code").value = "package main\n// 中文 😀";
  await ui.elements.get("run-button").trigger("click"); await settle();
  ui.elements.get("run-use-sample").checked = false;
  ui.elements.get("run-stdin").value = "";
  await ui.elements.get("run-button").trigger("click"); await settle();
  ui.elements.get("run-stdin").value = "custom\n\x00";
  await ui.elements.get("run-button").trigger("click"); await settle();
  ui.elements.get("submission-form").trigger("submit"); await settle();
  const writes = ui.calls.filter((call) => call.method === "POST");
  assert.deepEqual(writes.map((call) => call.url), ["/api/runs", "/api/runs", "/api/runs", "/api/submissions"]);
  const bodies = writes.map((call) => JSON.parse(call.body));
  assert.equal(Object.hasOwn(bodies[0], "stdin"), false);
  assert.equal(bodies[1].stdin, ""); assert.equal(bodies[2].stdin, "custom\n\x00");
  assert.equal(Object.hasOwn(bodies[3], "stdin"), false);
  for (const [index, call] of writes.entries()) {
    assert.equal(bodies[index].problem_id, 7); assert.equal(bodies[index].language, "go");
    assert.equal(bodies[index].source_code, ui.elements.get("source-code").value);
    assert.equal(call.headers["X-CSRF-Token"], "csrf-token"); assert.equal(call.credentials, "same-origin");
  }
  assert.equal(ui.calls.filter((call) => call.url === "/api/auth/csrf").length, 1);
});

test("self-test output stays inline and only formal submission exposes a record link", async () => {
  const ui = boot("problem", composerResponses(async (url) => ({
    data: {
      ...pending,
      id: url === "/api/runs" ? 30 : 31,
      kind: url === "/api/runs" ? "Run" : "Submit",
      status: "Accepted",
      output: url === "/api/runs" ? "hello from run" : "",
    },
  })));
  await settle();
  ui.elements.get("problem-edit-mode").trigger("click");
  assert.equal(ui.elements.get("problem-edit-mode").attributes.get("aria-pressed"), "true");
  assert.equal(ui.elements.get("problem-edit-mode").textContent, "退出编辑模式");
  ui.elements.get("source-code").value = "print('hello')";
  await ui.elements.get("run-button").trigger("click");
  await settle();
  assert.equal(ui.elements.get("composer-output").textContent, "hello from run");
  assert.equal(ui.elements.get("composer-result-link").hidden, true);
  assert.match(ui.elements.get("composer-poll-message").textContent, /不进入“我的提交”/);

  await ui.elements.get("submission-form").trigger("submit");
  await settle();
  assert.equal(ui.elements.get("composer-result-link").hidden, false);
  assert.match(ui.elements.get("composer-result-link").href, /\/submissions\/31$/);
  assert.match(ui.elements.get("composer-poll-message").textContent, /正式提交已进入提交记录/);
});

test("Composer protects account access and source/input byte limits", async () => {
  for (const user of [null, { account_type: "contest" }]) {
    const ui = boot("problem", composerResponses(async () => { throw new Error("unexpected write"); }, user));
    await settle(); ui.elements.get("source-code").value = "code";
    await ui.elements.get("run-button").trigger("click");
    assert.equal(ui.elements.get("submission-fields").disabled, true);
    assert.equal(ui.calls.some((call) => call.method === "POST"), false);
  }
  const ui = boot("problem", composerResponses(async () => { throw new Error("invalid input wrote"); }));
  await settle();
  for (const source of ["", " ", "x".repeat(65537), "中".repeat(21846), "\ud800"]) {
    ui.elements.get("source-code").value = source;
    await ui.elements.get("run-button").trigger("click");
    assert.equal(ui.elements.get("submission-create-message").hidden, false);
  }
  ui.elements.get("source-code").value = "code";
  ui.elements.get("run-use-sample").checked = false; ui.elements.get("run-stdin").value = "x".repeat(65537);
  await ui.elements.get("run-button").trigger("click");
  assert.equal(ui.calls.some((call) => call.method === "POST"), false);
});

test("Polling advances real states, stops at terminal state and clears on pagehide", async () => {
  let reads = 0;
  const ui = boot("submission", async () => ({ data: { ...pending, status: ++reads === 1 ? "Queued" : reads === 2 ? "Running" : "Accepted" } }));
  await settle(); assert.equal(reads, 1); assert.equal(ui.timers.size, 1);
  await ui.tick(); assert.equal(reads, 2); assert.equal(ui.timers.size, 1);
  await ui.tick(); assert.equal(reads, 3); assert.equal(ui.timers.size, 0);
  assert.match(ui.elements.get("submission-summary").textContent, /通过/);
  const waiting = boot("submission", async () => ({ data: pending }));
  await settle(); assert.equal(waiting.timers.size, 1);
  waiting.dispatch("pagehide"); assert.equal(waiting.timers.size, 0);
  waiting.dispatch("pageshow", { persisted: true }); assert.equal(waiting.reloads, 1);
});

test("Pagehide aborts in-flight status requests without retrying or rendering stale data", async () => {
  let signal;
  const ui = boot("submission", async (_, init) => {
    signal = init.signal;
    return new Promise((_, reject) => signal.addEventListener("abort", () => reject(Object.assign(new Error("aborted"), { name: "AbortError" })), { once: true }));
  });
  await settle(); assert.equal(signal.aborted, false);
  ui.dispatch("pagehide"); await settle();
  assert.equal(signal.aborted, true); assert.equal(ui.timers.size, 0);
  assert.equal(ui.elements.get("submission-detail").hidden, true);
});

test("Formal details hide runtime input/output/diagnostics and render own source as text", async () => {
  const record = { ...pending, status: "RuntimeError", source_code: "<script>alert(1)</script>", stdin: "hidden input", output: "hidden output", error_message: "hidden stderr" };
  const ui = boot("submission", async () => ({ data: record })); await settle();
  assert.equal(ui.elements.get("submission-source").textContent, record.source_code);
  assert.equal(ui.elements.get("submission-run-data").hidden, true);
  for (const id of ["submission-stdin", "submission-output", "submission-error"]) assert.equal(ui.elements.get(id).textContent, "");
  const run = boot("submission", async () => ({ data: { ...record, kind: "Run" } })); await settle();
  assert.equal(run.elements.get("submission-output").textContent, record.output);
  assert.equal(run.elements.get("submission-run-data").hidden, false);
  const compile = boot("submission", async () => ({ data: { ...record, status: "CompileError" } })); await settle();
  assert.equal(compile.elements.get("submission-error").textContent, record.error_message);
});

test("Status authentication loss clears previously rendered source and stops retries", async () => {
  let reads = 0;
  const ui = boot("submission", async () => ++reads === 1 ? { data: { ...pending, source_code: "private source" } } : { status: 401 });
  await settle(); assert.equal(ui.elements.get("submission-source").textContent, "private source");
  await ui.tick();
  assert.equal(ui.elements.get("submission-detail").hidden, true);
  assert.equal(ui.elements.get("submission-source").textContent, "");
  assert.equal(ui.elements.get("submission-login").hidden, false); assert.equal(ui.timers.size, 0);
});

test("History uses encoded filters and never injects returned markup", async () => {
  const ui = boot("submissions", async () => ({ data: { items: [{ ...pending, status: "Accepted", language: "<img src=x>" }], page: 2, limit: 20, total: 42 } }), { search: "?kind=Submit&problem_id=7&page=2" });
  await settle(); const params = new URL(ui.calls[0].url, "http://test").searchParams;
  assert.equal(params.get("kind"), "Submit"); assert.equal(params.get("problem_id"), "7"); assert.equal(params.get("page"), "2");
  assert.match(ui.elements.get("submission-list").textContent, /<img src=x>/);
  assert.equal(ui.elements.get("submission-prev").disabled, false); assert.equal(ui.elements.get("submission-next").disabled, false);
  assert.equal(ui.timers.size, 0);
});

test("Composer uses local editor content and disposes the editor on navigation", async () => {
  let disabled = false, destroyed = false, language;
  const editor = { mount: () => ({ getValue: () => "editor source", setDisabled(value) { disabled = value; }, setLanguage(value) { language = value; }, destroy() { destroyed = true; } }) };
  const ui = boot("problem", composerResponses(async () => ({ data: { ...pending, status: "Accepted" } })), { editor });
  await settle(); assert.equal(disabled, false);
  ui.elements.get("submission-language").value = "python3"; ui.elements.get("submission-language").trigger("change");
  assert.equal(language, "python3");
  ui.elements.get("source-code").value = "fallback source"; ui.elements.get("submission-form").trigger("submit"); await settle();
  const sent = ui.calls.find((call) => call.method === "POST"); assert.equal(JSON.parse(sent.body).source_code, "editor source");
  ui.dispatch("pagehide"); assert.equal(destroyed, true);
});

test("Bundled editor keeps the textarea fallback when the style nonce is unavailable", () => {
  const bundle = fs.readFileSync(path.join(root, "static/editor.js"), "utf8");
  const context = { document: { querySelector: () => null, documentElement: { style: {} } }, window: {}, navigator: { userAgent: "Node" } };
  vm.runInNewContext(bundle, context, { filename: "editor.js" });
  const textarea = { value: "unchanged", hidden: false, required: true };
  assert.equal(context.BiueEditor.mount(textarea, "go"), null);
  assert.equal(textarea.hidden, false); assert.equal(textarea.required, true); assert.equal(textarea.value, "unchanged");
});
