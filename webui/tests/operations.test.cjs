"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

const root = path.resolve(__dirname, "..");
const script = fs.readFileSync(path.join(root, "static/operations.js"), "utf8");
const template = fs.readFileSync(path.join(root, "templates/operations.html"), "utf8");

class Element {
  constructor(tag = "div") {
    this.tag = tag;
    this.children = [];
    this.handlers = new Map();
    this.attributes = new Map();
    this.dataset = {};
    this.hidden = false;
    this.disabled = false;
    this.className = "";
    this.value = "";
    this._text = "";
    const classes = new Set();
    this.classList = {
      toggle: (name, force) => force ? classes.add(name) : classes.delete(name),
      add: (name) => classes.add(name),
      remove: (name) => classes.delete(name),
    };
  }
  set textContent(value) { this._text = String(value); this.children = []; }
  get textContent() { return this._text + this.children.map((child) => child.textContent).join(""); }
  append(...children) { this.children.push(...children.filter((child) => child)); }
  replaceChildren(...children) { this._text = ""; this.children = []; this.append(...children); }
  setAttribute(key, value) {
    this.attributes.set(key, String(value));
    if (key.startsWith("data-")) {
      const name = key.slice(5).replace(/-([a-z])/g, (_, letter) => letter.toUpperCase());
      this.dataset[name] = String(value);
    }
  }
  addEventListener(type, callback) { this.handlers.set(type, callback); }
  trigger(type, extra = {}) {
    if (this.handlers.has(type)) return this.handlers.get(type)({ preventDefault() {}, target: this, currentTarget: this, ...extra });
    if (typeof this[`on${type}`] === "function") return this[`on${type}`]({ preventDefault() {}, target: this, currentTarget: this, ...extra });
  }
  querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }
  querySelectorAll(selector) {
    const matches = [];
    for (const child of this.children) {
      let hit = false;
      if (selector.startsWith("[data-") && selector.endsWith("]")) {
        const key = selector.slice(1, -1).replace(/^data-/, "").replace(/-([a-z])/g, (_, letter) => letter.toUpperCase());
        hit = Object.hasOwn(child.dataset, key);
      } else if (selector.startsWith(".")) hit = child.className.split(/\s+/).includes(selector.slice(1));
      else hit = child.tag === selector;
      if (hit) matches.push(child);
      matches.push(...child.querySelectorAll(selector));
    }
    return matches;
  }
  reset() {
    for (const field of Object.values(this.fields || {})) if ("defaultValue" in field) field.value = field.defaultValue;
  }
}

class TestFormData {
  constructor(form) {
    this.data = new Map();
    for (const [name, field] of form.fields || []) this.data.set(name, field.value);
  }
  entries() { return this.data.entries(); }
  [Symbol.iterator]() { return this.entries(); }
}

function boot(handler) {
  const elements = new Map();
  for (const match of template.matchAll(/<(\w+)\b([^>]*\bid="([^"]+)"[^>]*)>/g)) {
    const element = new Element(match[1]);
    element.hidden = /\bhidden\b/.test(match[2]);
    element.disabled = /\bdisabled\b/.test(match[2]);
    for (const attribute of match[2].matchAll(/\b(data-[a-z-]+)="([^"]*)"/g)) element.setAttribute(attribute[1], attribute[2]);
    elements.set(match[3], element);
  }
  for (const match of template.matchAll(/<form\b[^>]*id="([^"]+)"[^>]*>([\s\S]*?)<\/form>/g)) {
    const form = elements.get(match[1]);
    form.fields = new Map();
    for (const field of match[2].matchAll(/<(input|textarea|select)\b([^>]*\bname="([^"]+)"[^>]*)>/g)) {
      const id = /\bid="([^"]+)"/.exec(field[2])?.[1];
      const element = id ? elements.get(id) : new Element(field[1]);
      element.value = /\bvalue="([^"]*)"/.exec(field[2])?.[1] || "";
      element.defaultValue = element.value;
      form.fields.set(field[3], element);
    }
    form.elements = { namedItem: (name) => form.fields.get(name) };
    for (const [name, field] of form.fields) form.elements[name] = field;
  }
  const all = () => [...new Set(elements.values())];
  const document = {
    body: { dataset: { page: "operations" } },
    getElementById(id) { assert.ok(elements.has(id), `missing template ID ${id}`); return elements.get(id); },
    createElement(tag) { return new Element(tag); },
    querySelector(selector) { return this.querySelectorAll(selector)[0] || null; },
    querySelectorAll(selector) {
      if (selector.startsWith("[data-")) {
        const key = selector.slice(1, -1).replace(/=.*$/, "").replace(/^data-/, "").replace(/-([a-z])/g, (_, letter) => letter.toUpperCase());
        return all().filter((element) => Object.hasOwn(element.dataset, key));
      }
      return [];
    },
  };
  const calls = [];
  const location = { hash: "", pathname: "/admin" };
  const history = { replaceState() {}, pushState() {} };
  const window = {
    location, history, confirm: () => true, addEventListener() {},
  };
  let csrf = 0;
  const context = {
    document, window, location, history, URLSearchParams, FormData: TestFormData, Date, Math, Intl,
    AbortController, setTimeout, clearTimeout, performance, crypto: { randomUUID: () => "00000000-0000-4000-8000-000000000000" },
    navigator: { clipboard: { writeText: async () => {} } }, URL: { createObjectURL: () => "blob:test", revokeObjectURL() {} },
    Blob,
    fetch: async (url, options = {}) => {
      calls.push({ url, ...options });
      if (url === "/api/auth/csrf") return { ok: true, status: 200, headers: { get: () => "" }, json: async () => ({ code: 0, message: "ok", data: { csrf_token: `csrf-${++csrf}` } }) };
      const result = await handler(url, options);
      return { ok: true, status: 200, headers: { get: () => "request-id" }, json: async () => ({ code: 0, message: "ok", data: result.data }) };
    },
  };
  vm.runInNewContext(script, context, { filename: "operations.js" });
  return { elements, calls };
}

async function settle() {
  for (let index = 0; index < 10; index++) await new Promise((resolve) => setImmediate(resolve));
}

test("test center can create labelled simulation data and reveal credentials once", async () => {
  const ui = boot(async (url) => {
    if (url === "/api/admin/operations/metrics") return { data: { users: 1, redis_ready: true } };
    if (url === "/api/admin/operations/simulate") return { data: {
      contest_id: 3, problem_ids: [1, 2, 3], username_prefix: "simabc", initial_password: "sim-once",
      users: 2, practice_submissions: 12, contest_submissions: 12,
      usernames: ["simabc01", "simabc02"], simulation_label: "测试中心模拟数据，不是真实 Runner 执行结果",
      warnings: [], complete: true,
    } };
    throw new Error(`unexpected URL ${url}`);
  });
  await settle();
  await ui.elements.get("operations-simulate").trigger("click");
  await settle();
  const result = ui.elements.get("operations-simulation-result");
  assert.match(result.textContent, /12 条题库提交 · 12 条比赛提交/);
  const credentials = result.querySelectorAll("textarea")[0];
  assert.match(credentials.value, /sim-once/);
  assert.match(result.textContent, /不是真实 Runner 执行结果/);
  const call = ui.calls.find((item) => item.url === "/api/admin/operations/simulate");
  assert.equal(call.method, "POST");
  assert.equal(JSON.parse(call.body).password, undefined);
});

test("scope rejudge controls submit only the selected scope and show queue totals", async () => {
  const ui = boot(async (url) => {
    if (url === "/api/admin/operations/metrics") return { data: { users: 1 } };
    if (url.endsWith("/rejudge/problem")) return { data: { queued: 21, skipped: 2, batches: 1, tasks: [] } };
    if (url.endsWith("/rejudge/contest")) return { data: { queued: 4, skipped: 0, batches: 1, tasks: [] } };
    throw new Error(`unexpected URL ${url}`);
  });
  await settle();
  const problemForm = ui.elements.get("operations-rejudge-problem");
  problemForm.fields.get("problem_id").value = "7";
  await problemForm.trigger("submit", { submitter: new Element("button") });
  await settle();
  const problemCall = ui.calls.find((item) => item.url.endsWith("/rejudge/problem"));
  assert.equal(JSON.parse(problemCall.body).problem_id, 7);
  assert.match(ui.elements.get("operations-scope-rejudge-result").textContent, /21 条/);
  const contestForm = ui.elements.get("operations-rejudge-contest");
  contestForm.fields.get("contest_id").value = "3";
  await contestForm.trigger("submit", { submitter: new Element("button") });
  await settle();
  assert.equal(JSON.parse(ui.calls.find((item) => item.url.endsWith("/rejudge/contest")).body).contest_id, 3);
});

test("full initialization requires the exact phrase and reports partial cleanup", async () => {
  const ui = boot(async (url) => {
    if (url === "/api/admin/operations/metrics") return { data: { users: 1 } };
    if (url === "/api/admin/operations/reset") return { data: {
      deleted: { submissions: 12, users: 4 }, redis_keys: 2, data_versions: 3,
      warnings: ["数据库已初始化，但 Redis oj:* 缓存未完全清理"], complete: false,
    } };
    throw new Error(`unexpected URL ${url}`);
  });
  await settle();
  const form = ui.elements.get("operations-reset");
  form.fields.get("confirmation").value = "初始化全部业务数据";
  form.fields.get("reason").value = "重建验收环境";
  await form.trigger("submit", { submitter: new Element("button") });
  await settle();
  assert.equal(JSON.parse(ui.calls.find((item) => item.url === "/api/admin/operations/reset").body).reason, "重建验收环境");
  assert.match(ui.elements.get("operations-reset-result").textContent, /Redis oj:\* 缓存未完全清理/);
  assert.match(ui.elements.get("operations-reset-result").textContent, /submissions: 12/);
});
