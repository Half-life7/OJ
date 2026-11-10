"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

const root = path.resolve(__dirname, "..");
const script = fs.readFileSync(path.join(root, "static/contests.js"), "utf8");
const template = fs.readFileSync(path.join(root, "templates/contests.html"), "utf8");

class Element {
  constructor(tag = "div") {
    this.tag = tag;
    this.children = [];
    this.handlers = new Map();
    this.attributes = new Map();
    this.dataset = {};
    this.hidden = false;
    this.disabled = false;
    this.value = "";
    this.type = tag === "input" ? "text" : "";
    this.checked = false;
    this._text = "";
    this.className = "";
    const classes = new Set();
    this.classList = {
      toggle: (name, force) => force === undefined ? (classes.has(name) ? (classes.delete(name), false) : (classes.add(name), true)) : (force ? classes.add(name) : classes.delete(name)),
      add: (name) => classes.add(name),
      remove: (name) => classes.delete(name),
    };
  }
  set textContent(value) { this._text = String(value); this.children = []; }
  get textContent() { return this._text + this.children.map((child) => child.textContent).join(""); }
  append(...children) {
    for (const child of children) {
      if (child?.isTextNode) continue;
      this.children.push(child);
      if (this.tag === "select" && child?.tag === "option") this.options.push(child);
    }
  }
  replaceChildren(...children) {
    this._text = "";
    this.children = [];
    this.options = [];
    this.append(...children);
  }
  setAttribute(key, value) {
    this.attributes.set(key, String(value));
    if (key.startsWith("data-")) {
      const name = key.slice(5).replace(/-([a-z])/g, (_, letter) => letter.toUpperCase());
      this.dataset[name] = String(value);
    }
  }
  removeAttribute(key) {
    this.attributes.delete(key);
    if (key.startsWith("data-")) delete this.dataset[key.slice(5).replace(/-([a-z])/g, (_, letter) => letter.toUpperCase())];
  }
  addEventListener(type, callback) { this.handlers.set(type, callback); }
  trigger(type, extra = {}) {
    const callback = this.handlers.get(type);
    return callback?.({ preventDefault() {}, target: this, currentTarget: this, ...extra });
  }
  querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }
  querySelectorAll(selector) {
    const matches = [];
    for (const child of this.children) {
      let hit = false;
      if (selector.startsWith(".")) hit = child.className.split(/\s+/).includes(selector.slice(1));
      else if (selector.startsWith("[data-") && selector.endsWith("]")) {
        const key = selector.slice(1, -1).replace(/=.*$/, "").replace(/^data-/, "").replace(/-([a-z])/g, (_, letter) => letter.toUpperCase());
        hit = Object.hasOwn(child.dataset, key);
      } else if (selector.startsWith("[") && selector.endsWith("]")) {
        const key = selector.slice(1, -1).split("=")[0];
        hit = child.attributes.has(key);
      } else {
        hit = child.tag === selector;
      }
      if (hit) matches.push(child);
      matches.push(...child.querySelectorAll(selector));
    }
    return matches;
  }
  focus() {}
  scrollIntoView() {}
  reset() {
    for (const field of Object.values(this.fields || {})) {
      if (typeof field === "object" && "defaultValue" in field) field.value = field.defaultValue;
      if (typeof field === "object" && field.type === "checkbox") field.checked = field.defaultChecked;
    }
  }
}

class TestFormData {
  constructor(form) {
    this.data = new Map();
    if (form) for (const [name, field] of form.fields) this.data.set(name, field.value);
  }
  entries() { return this.data.entries(); }
  [Symbol.iterator]() { return this.entries(); }
}

function boot(responses, pathname = "/contests") {
  const elements = new Map();
  for (const match of template.matchAll(/<(\w+)\b([^>]*\bid="([^"]+)"[^>]*)>/g)) {
    const element = new Element(match[1]);
    element.hidden = /\bhidden\b/.test(match[2]);
    element.disabled = /\bdisabled\b/.test(match[2]);
    element.checked = /\bchecked\b/.test(match[2]);
    if (match[1] === "select") { element.options = []; element.selectedOptions = []; }
    for (const attribute of match[2].matchAll(/\b(data-[a-z-]+)="([^"]*)"/g)) element.setAttribute(attribute[1], attribute[2]);
    elements.set(match[3], element);
  }
  for (const match of template.matchAll(/<form\b[^>]*id="([^"]+)"[^>]*>([\s\S]*?)<\/form>/g)) {
    const form = elements.get(match[1]);
    form.fields = new Map();
    const values = {
      namedItem: (name) => form.fields.get(name),
      [Symbol.iterator]: () => form.fields.values(),
    };
    for (const field of match[2].matchAll(/<(input|textarea|select)\b([^>]*\bname="([^"]+)"[^>]*)>/g)) {
      const id = /\bid="([^"]+)"/.exec(field[2])?.[1];
      const element = id ? elements.get(id) : new Element(field[1]);
      element.type = /\btype="([^"]+)"/.exec(field[2])?.[1] || (field[1] === "textarea" ? "textarea" : field[1] === "select" ? "select" : "text");
      element.value = /\bvalue="([^"]*)"/.exec(field[2])?.[1] || "";
      element.defaultValue = element.value;
      element.defaultChecked = /\bchecked\b/.test(field[2]);
      if (field[1] === "select") { element.options ||= []; element.selectedOptions = []; }
      form.fields.set(field[3], element);
      values[field[3]] = element;
    }
    form.elements = values;
  }
  for (const select of [...elements.values()].filter((item) => item.tag === "select")) if (!select.options) select.options = [];
  for (const select of [...elements.values()].filter((item) => item.tag === "select")) {
    select.selectedOptions = select.options.filter((option) => option.selected);
  }

  const all = () => [...new Set(elements.values())];
  const document = {
    body: { dataset: { page: "contests" } },
    title: "",
    getElementById(id) { assert.ok(elements.has(id), `missing template ID ${id}`); return elements.get(id); },
    createElement(tag) { return new Element(tag); },
    createTextNode(value) { return { isTextNode: true, textContent: String(value) }; },
    querySelectorAll(selector) {
      if (selector === ".contest-nav-item") return all().filter((element) => element.className.split(/\s+/).includes("contest-nav-item"));
      if (selector.startsWith("[data-")) {
        const key = selector.slice(1, -1).replace(/^data-/, "").replace(/-([a-z])/g, (_, letter) => letter.toUpperCase());
        return all().filter((element) => Object.hasOwn(element.dataset, key));
      }
      return [];
    },
  };
  const location = { pathname, search: "", href: `http://oj.test${pathname}` };
  const calls = [];
  const storage = new Map();
  const history = {
    pushState(_, __, next) { location.pathname = next; location.href = `http://oj.test${next}`; },
    replaceState(_, __, next) { location.pathname = next; location.href = `http://oj.test${next}`; },
  };
  const window = {
    location, history,
    confirm: () => true,
    addEventListener() {},
  };
  const context = {
    document, window, location, history, URLSearchParams, FormData: TestFormData,
    Intl, Date, console, setTimeout, clearTimeout, AbortController,
    sessionStorage: {
      getItem: (key) => storage.get(key) ?? null,
      setItem: (key, value) => storage.set(key, String(value)),
      removeItem: (key) => storage.delete(key),
    },
    fetch: async (url, options) => {
      calls.push({ url, ...options });
      const result = await responses(url, options);
      const status = result.status || 200;
      return {
        ok: status >= 200 && status < 300,
        status,
        headers: { get: (name) => name === "X-Request-ID" ? result.requestID || "contest-request-id" : null },
        json: async () => ({ code: status < 300 ? 0 : status * 100 + 1, message: result.message || "请求失败", data: result.data ?? null }),
      };
    },
  };
  vm.runInNewContext(script, context, { filename: "contests.js" });
  return { elements, calls, location };
}

async function settle(ms = 0) {
  if (ms) await new Promise((resolve) => setTimeout(resolve, ms));
  for (let index = 0; index < 10; index++) await new Promise((resolve) => setImmediate(resolve));
}

const user = { id: 1, username: "admin", account_type: "permanent", global_role: "super_admin", status: "active" };
function contest(id, title, overrides = {}) {
  return {
    id, title, scoring_mode: "OI", enrollment_policy: "admin_add", approval_required: false, team_mode: false,
    starts_at: "2026-12-01T00:00:00Z", ends_at: "2026-12-01T02:00:00Z", revision: 1, settled_at: null, ...overrides,
  };
}
function view(record) {
  return { contest: record, manager: true, frozen: false, teams: [], members: [], problems: [], standings: [] };
}

test("late contest detail response cannot overwrite the latest visible contest", async () => {
  const first = contest(1, "Slow contest");
  const second = contest(2, "Latest contest");
  const ui = boot(async (url) => {
    if (url === "/api/users/me") return { data: user };
    if (url === "/api/contests?after=0") return { data: [first, second] };
    if (url === "/api/contests/1") { await new Promise((resolve) => setTimeout(resolve, 30)); return { data: view(first) }; }
    if (url === "/api/contests/2") return { data: view(second) };
    throw new Error(`unexpected URL ${url}`);
  });
  await settle();
  const buttons = ui.elements.get("contest-list").querySelectorAll("button");
  assert.equal(buttons.length, 2);
  buttons[0].trigger("click");
  buttons[1].trigger("click");
  await settle(50);
  assert.equal(ui.elements.get("contest-title").textContent, second.title);
  assert.equal(ui.location.pathname, "/contests/2");
});

test("CSV preview becomes unusable after switching contests", async () => {
  const first = contest(1, "Roster A");
  const second = contest(2, "Roster B");
  const ui = boot(async (url, options) => {
    if (url === "/api/users/me") return { data: user };
    if (url === "/api/contests?after=0") return { data: [first, second] };
    if (url === "/api/contests/1") return { data: view(first) };
    if (url === "/api/contests/2") return { data: view(second) };
    if (url === "/api/auth/csrf") return { data: { csrf_token: "csrf" } };
    if (url === "/api/contests/1/csv/preview") return { data: { token: "token-a", valid: true, rows: [{ line: 2, username: "u", team: "t", participant_type: "official", error: "" }] } };
    throw new Error(`unexpected URL ${url} ${options?.method}`);
  });
  await settle();
  const entry = ui.elements.get("contest-list").querySelectorAll("button")[0];
  entry.trigger("click");
  await settle();
  ui.elements.get("contest-csv-form").elements.csv.value = "username,team\nu,t";
  await ui.elements.get("contest-csv-form").trigger("submit", { submitter: ui.elements.get("contest-csv-form").querySelector("button") });
  await settle();
  assert.match(ui.elements.get("contest-csv-preview").textContent, /Roster A/);
  assert.equal(ui.elements.get("contest-csv-confirm").disabled, false);
  ui.elements.get("contest-list").querySelectorAll("button")[1].trigger("click");
  await settle();
  assert.equal(ui.elements.get("contest-title").textContent, "Roster B");
  assert.equal(ui.elements.get("contest-csv-confirm").disabled, true);
  assert.match(ui.elements.get("contest-csv-preview").textContent, /重新生成/);
});
