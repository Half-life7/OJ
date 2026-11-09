"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

const root = path.resolve(__dirname, "..");
const script = fs.readFileSync(path.join(root, "static/problems.js"), "utf8");
const template = fs.readFileSync(path.join(root, "templates/problems.html"), "utf8");

// Only the DOM surface used by the page is modeled. Actual HTML rendering and
// browser integration still require browser acceptance, independent of this suite.
class Element {
  constructor(tag = "div") {
    this.tag = tag;
    this.children = [];
    this.handlers = new Map();
    this.attributes = new Map();
    this.hidden = false;
    this.disabled = false;
    this.value = "";
    this._text = "";
    this.classList = { toggle() {} };
  }
  set textContent(value) { this._text = String(value); this.children = []; }
  get textContent() { return this._text + this.children.map((child) => child.textContent).join(""); }
  set innerHTML(_) { throw new Error("Untrusted content must not be parsed as HTML"); }
  append(...children) { this.children.push(...children); }
  replaceChildren(...children) { this._text = ""; this.children = children; }
  setAttribute(key, value) { this.attributes.set(key, value); }
  removeAttribute(key) { this.attributes.delete(key); }
  addEventListener(type, callback) { this.handlers.set(type, callback); }
  querySelector(selector) {
    if (selector === ".loading-dot") return this.loadingDot || (this.loadingDot = new Element("span"));
    return this.querySelectorAll(selector)[0];
  }
  querySelectorAll(selector) {
    const result = [];
    for (const child of this.children) {
      if (child.tag === selector) result.push(child);
      result.push(...child.querySelectorAll(selector));
    }
    return result;
  }
  focus() {}
  reset() { for (const field of this.fields.values()) field.value = field.defaultValue; }
  trigger(type) {
    const callback = this.handlers.get(type);
    return callback && callback({ preventDefault() {}, currentTarget: this, target: this });
  }
}

class TestFile {
  constructor(size) { this.size = size; }
}

class TestFormData {
  constructor(form) {
    this.data = new Map();
    if (form) for (const [name, field] of form.fields) this.data.set(name, field.file || field.value);
  }
  get(key) { return this.data.get(key) ?? null; }
  set(key, value) { this.data.set(key, value); }
  entries() { return this.data.entries(); }
  [Symbol.iterator]() { return this.entries(); }
}

const sample = {
  id: 7, title: "A + B", description: "求两个数的和。", input_format: "两个整数。", output_format: "整数和。",
  sample_input: "1 2\n", sample_output: "3\n", time_limit_ms: 1000, memory_limit_mb: 256,
  difficulty: "easy", tags: ["数学"], status: "draft", visibility: "hidden", revision: 1,
  statement_version: 1, data_version: 0, created_at: "2026-11-09T00:00:00Z", updated_at: "2026-11-09T00:00:00Z",
};

function boot(page, responses, pathname = "/problems", search = "") {
  const elements = new Map();
  for (const match of template.matchAll(/<(\w+)\b[^>]*\bid="([^"]+)"[^>]*>/g)) {
    const element = new Element(match[1]);
    element.hidden = /\bhidden\b/.test(match[0]);
    element.disabled = /\bdisabled\b/.test(match[0]);
    elements.set(match[2], element);
  }
  for (const match of template.matchAll(/<form\b[^>]*id="([^"]+)"[^>]*>([\s\S]*?)<\/form>/g)) {
    const form = elements.get(match[1]);
    form.fields = new Map();
    for (const field of match[2].matchAll(/<(input|textarea|select)\b([^>]*\bname="([^"]+)"[^>]*)>/g)) {
      const id = /\bid="([^"]+)"/.exec(field[2])[1];
      const element = elements.get(id);
      element.value = /\bvalue="([^"]*)"/.exec(field[2])?.[1] || "";
      element.defaultValue = element.value;
      form.fields.set(field[3], element);
    }
    form.elements = { namedItem: (name) => form.fields.get(name) };
  }
  const calls = [];
  const document = {
    body: { dataset: { page } }, title: "Biue OJ",
    getElementById(id) { assert.ok(elements.has(id), "missing template element: " + id); return elements.get(id); },
    createElement(tag) { return new Element(tag); },
  };
  const window = { location: { pathname, search }, history: { replaceState() {} }, setTimeout, clearTimeout, addEventListener() {}, confirm: () => true };
  const context = {
    document, window, URLSearchParams, TextEncoder, AbortController, FormData: TestFormData, File: TestFile,
    fetch: async (url, options) => {
      calls.push({ url, ...options });
      const result = await responses(url, options);
      const status = result.status || 200;
      return { ok: status >= 200 && status < 300, status, json: async () => ({ code: status < 300 ? 0 : status * 100 + 1, message: result.message || "请求失败", data: result.data ?? null }) };
    },
  };
  vm.runInNewContext(script, context, { filename: "problems.js" });
  return { elements, calls, document, window };
}

async function settle() {
  for (let i = 0; i < 8; i++) await new Promise((resolve) => setImmediate(resolve));
}

test("public detail renders markup and samples as text", async () => {
  const payload = { ...sample, title: '<img src=x onerror="alert(1)">', description: "<script>alert(2)</script>", sample_input: "<svg/onload=alert(3)>\n" };
  const ui = boot("problem", async (url) => {
    assert.equal(url, "/api/problems/7");
    return { data: payload };
  }, "/problems/7");
  await settle();
  assert.equal(ui.elements.get("problem-title").textContent, payload.title);
  assert.equal(ui.elements.get("problem-description").textContent, payload.description);
  assert.equal(ui.elements.get("problem-sample-input").textContent, payload.sample_input);
  assert.equal(ui.elements.get("problem-detail").hidden, false);
  assert.equal(ui.elements.get("problem-state").hidden, true);
  assert.equal(ui.calls[0].credentials, "same-origin");
});

test("missing or hidden public problem shows a 404 state", async () => {
  const ui = boot("problem", async () => ({ status: 404 }), "/problems/7");
  await settle();
  assert.equal(ui.elements.get("problem-detail").hidden, true);
  assert.equal(ui.elements.get("problem-state-text").textContent, "题目不存在或尚未公开。");
  assert.equal(ui.elements.get("problem-retry").hidden, true);
});

test("public filters encode queries and keep pagination state", async () => {
  const ui = boot("problems", async () => ({ data: { items: [sample], page: 2, limit: 20, total: 45 } }), "/problems", "?q=%3Cscript%3E&difficulty=easy&page=2");
  await settle();
  const query = new URL(ui.calls[0].url, "http://test").searchParams;
  assert.equal(query.get("q"), "<script>");
  assert.equal(query.get("difficulty"), "easy");
  assert.equal(query.get("page"), "2");
  assert.equal(ui.elements.get("problem-prev").disabled, false);
  assert.equal(ui.elements.get("problem-next").disabled, false);
  assert.equal(ui.elements.get("problem-results").hidden, false);
});

test("admin interface stays hidden for ordinary users and anonymous users", async () => {
  for (const response of [{ data: { global_role: "user" } }, { status: 401 }]) {
    const ui = boot("admin-problems", async (url) => {
      assert.equal(url, "/api/users/me");
      return response;
    });
    await settle();
    assert.equal(ui.elements.get("problem-admin").hidden, true);
    assert.equal(ui.elements.get("problem-upload-fields").disabled, true);
    assert.equal(ui.calls.length, 1);
    assert.equal(ui.elements.get("problem-login").hidden, response.status !== 401);
  }
});

test("admin edits carry the loaded revision and CSRF token; conflicts preserve edits", async () => {
  const ui = boot("admin-problems", async (url, options) => {
    if (url === "/api/users/me") return { data: { global_role: "super_admin" } };
    if (url.startsWith("/api/admin/problems?")) return { data: { items: [sample], page: 1, limit: 20, total: 1 } };
    if (url === "/api/admin/problems/7" && options.method === "GET") return { data: sample };
    if (url.includes("/data-versions?")) return { data: { items: [], page: 1, limit: 20, total: 0 } };
    if (url === "/api/auth/csrf") return { data: { csrf_token: "csrf-test-token" } };
    assert.equal(url, "/api/admin/problems/7");
    assert.equal(options.method, "PUT");
    return { status: 409 };
  });
  await settle();
  await ui.elements.get("admin-problem-list").querySelectorAll("button")[0].trigger("click");
  await settle();
  assert.equal(ui.elements.get("problem-upload-fields").disabled, false);
  ui.elements.get("edit-title").value = "未保存的新题名";
  ui.elements.get("problem-editor-form").trigger("input");
  assert.equal(ui.elements.get("problem-upload-fields").disabled, true);
  await ui.elements.get("problem-editor-form").trigger("submit");
  await settle();
  const saved = ui.calls.find((call) => call.method === "PUT");
  const input = JSON.parse(saved.body);
  assert.equal(input.revision, 1);
  assert.equal(input.title, "未保存的新题名");
  assert.equal(saved.headers["X-CSRF-Token"], "csrf-test-token");
  assert.equal(saved.headers["Content-Type"], "application/json");
  assert.match(ui.elements.get("problem-editor-message").textContent, /重新加载此题/);
  assert.equal(ui.elements.get("edit-title").value, "未保存的新题名");
  assert.equal(ui.elements.get("problem-editor-fields").disabled, false);
});

test("test data upload sends multipart fields with the current revision", async () => {
  const ui = boot("admin-problems", async (url, options) => {
    if (url === "/api/users/me") return { data: { global_role: "super_admin" } };
    if (url.startsWith("/api/admin/problems?")) return { data: { items: [sample], page: 1, limit: 20, total: 1 } };
    if (url === "/api/admin/problems/7") return { data: sample };
    if (url.includes("/data-versions?")) return { data: { items: [], page: 1, limit: 20, total: 0 } };
    if (url === "/api/auth/csrf") return { data: { csrf_token: "csrf-test-token" } };
    assert.equal(url, "/api/admin/problems/7/data-versions");
    assert.equal(options.method, "POST");
    return { data: { data_version: 1, revision: 2, case_count: 1 } };
  });
  await settle();
  await ui.elements.get("admin-problem-list").querySelectorAll("button")[0].trigger("click");
  await settle();
  ui.elements.get("data-archive").file = new TestFile(100);
  ui.elements.get("data-scores").value = '{"1":9007199254740993}';
  await ui.elements.get("problem-upload-form").trigger("submit");
  await settle();
  const sent = ui.calls.find((call) => call.method === "POST");
  assert.ok(sent.body instanceof TestFormData);
  assert.equal(sent.body.get("revision"), "1");
  assert.equal(sent.body.get("scores"), '{"1":9007199254740993}', "int64 scores must not be rounded through JavaScript Number");
  assert.ok(sent.body.get("archive") instanceof TestFile);
  assert.equal(sent.headers["X-CSRF-Token"], "csrf-test-token");
  assert.equal(sent.headers["Content-Type"], undefined, "browser must generate the multipart boundary");
  assert.match(ui.elements.get("problem-current-data").textContent, /v1/);
  assert.match(ui.elements.get("problem-editor-version").textContent, /修订 2/);
});

test("duplicate, fractional, overflowing and empty scores do not send an upload", async () => {
  for (const scores of ['{"1":40,"1":60}', '{"1":1.5}', '{"1":1e2}', '{"1":9223372036854775808}', '{"1":9223372036854775807,"2":1}', '{"1":0}', '{}']) {
    const ui = boot("admin-problems", async (url, options) => {
      assert.equal(options.method, "GET", "invalid local input must not write");
      if (url === "/api/users/me") return { data: { global_role: "super_admin" } };
      if (url.startsWith("/api/admin/problems?")) return { data: { items: [sample], page: 1, limit: 20, total: 1 } };
      if (url === "/api/admin/problems/7") return { data: sample };
      assert.ok(url.includes("/data-versions?"));
      return { data: { items: [], page: 1, limit: 20, total: 0 } };
    });
    await settle();
    await ui.elements.get("admin-problem-list").querySelectorAll("button")[0].trigger("click");
    await settle();
    ui.elements.get("data-archive").file = new TestFile(100);
    ui.elements.get("data-scores").value = scores;
    await ui.elements.get("problem-upload-form").trigger("submit");
    await settle();
    assert.equal(ui.calls.some((call) => call.method !== "GET"), false, scores);
    assert.equal(ui.elements.get("problem-upload-message").hidden, false, scores);
    assert.equal(ui.elements.get("problem-upload-fields").disabled, false, scores);
  }
});
