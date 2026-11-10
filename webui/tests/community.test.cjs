"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

const root = path.resolve(__dirname, "..");
const script = fs.readFileSync(path.join(root, "static/community.js"), "utf8");
const template = fs.readFileSync(path.join(root, "templates/community.html"), "utf8");

class Element {
  constructor(tag = "div") {
    this.tag = tag;
    this.children = [];
    this.handlers = new Map();
    this.attributes = new Map();
    this.hidden = false;
    this.disabled = false;
    this.value = "";
    this.textContent = "";
    this.innerHTML = "";
    this.classList = { toggle() {} };
  }
  append(...children) { this.children.push(...children); }
  replaceChildren(...children) { this.children = children; }
  setAttribute() {}
  addEventListener(type, callback) { this.handlers.set(type, callback); }
  dispatchEvent() {}
  trigger(type, extra = {}) {
    const callback = this.handlers.get(type) || this[`on${type}`];
    return callback?.({ preventDefault() {}, target: this, currentTarget: this, ...extra });
  }
  focus() {}
  reset() {
    for (const field of this.fields?.values() || []) {
      field.value = field.defaultValue || "";
      field.checked = field.defaultChecked || false;
    }
  }
  querySelector(selector) {
    if (selector === "button") return this.children.find((child) => child.tag === "button") || null;
    return null;
  }
  querySelectorAll() { return []; }
}

class TestFormData {
  constructor(form) { this.data = new Map([...form.fields].map(([name, field]) => [name, field.value])); }
  entries() { return this.data.entries(); }
  [Symbol.iterator]() { return this.entries(); }
}

function boot(respond, account = { id: 2, username: "writer", account_type: "permanent", global_role: "user", status: "active" }) {
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
    const values = { namedItem: (name) => form.fields.get(name), [Symbol.iterator]: () => form.fields.values() };
    for (const field of match[2].matchAll(/<(input|textarea|select)\b([^>]*\bname="([^"]+)"[^>]*)>/g)) {
      const id = /\bid="([^"]+)"/.exec(field[2])?.[1];
      const element = id ? elements.get(id) : new Element(field[1]);
      element.value = /\bvalue="([^"]*)"/.exec(field[2])?.[1] || "";
      element.defaultValue = element.value;
      form.fields.set(field[3], element);
      values[field[3]] = element;
    }
    form.elements = values;
  }
  elements.get("learning-filter").elements.kind.value = "list";
  elements.get("learning-form").elements.kind.value = "list";
  elements.get("learning-form").elements.visibility.value = "public";
  const calls = [];
  const location = { search: "", pathname: "/learning" };
  const document = {
    body: { dataset: { page: "community" } },
    hidden: false,
    getElementById(id) { assert.ok(elements.has(id), `missing ID ${id}`); return elements.get(id); },
    createElement: (tag) => new Element(tag),
    createTextNode: (value) => ({ textContent: String(value) }),
    querySelector: () => null,
    querySelectorAll: () => [],
  };
  const window = { addEventListener() {}, confirm: () => true, location };
  const storage = new Map();
  vm.runInNewContext(script, {
    document, window, location, URLSearchParams, FormData: TestFormData, AbortController,
    setInterval: () => 1, clearInterval() {}, setTimeout, clearTimeout,
    sessionStorage: {
      getItem: (key) => storage.get(key) ?? null,
      setItem: (key, value) => storage.set(key, String(value)),
      removeItem: (key) => storage.delete(key),
    },
    fetch: async (url, options) => {
      calls.push({ url, ...options });
      const result = account === null && url === "/api/users/me"
        ? { status: 401 }
        : await respond(url, options);
      const status = result.status || 200;
      return {
        ok: status >= 200 && status < 300,
        status,
        headers: { get: (name) => name === "X-Request-ID" ? "community-request-id" : null },
        json: async () => ({ code: status < 300 ? 0 : status * 100 + 1, message: result.message || "请求失败", data: result.data ?? null }),
      };
    },
  }, { filename: "community.js" });
  return { elements, calls };
}

async function settle(ms = 0) {
  if (ms) await new Promise((resolve) => setTimeout(resolve, ms));
  for (let index = 0; index < 10; index++) await new Promise((resolve) => setImmediate(resolve));
}

function item(id, title, overrides = {}) {
  return {
    id, title, owner_id: 2, username: "writer", kind: "list", visibility: "public", review_status: "approved",
    revision: 1, likes: 3, favorites: 4, liked: false, favorited: false, can_edit: true, can_delete: true, can_review: false,
    html: "<p>safe</p>", problems: [], ...overrides,
  };
}

test("late community detail response cannot replace a newer selection", async () => {
  const first = item(1, "Slow note");
  const second = item(2, "New note", { owner_id: 9, can_edit: false, can_delete: false });
  const ui = boot(async (url) => {
    if (url === "/api/users/me") return { data: { id: 2, username: "writer", account_type: "permanent", global_role: "user", status: "active" } };
    if (url.startsWith("/api/community?kind=list")) return { data: { items: [first, second], next: 0 } };
    if (url === "/api/community/1") { await new Promise((resolve) => setTimeout(resolve, 30)); return { data: first }; }
    if (url === "/api/community/2") return { data: second };
    throw new Error(`unexpected ${url}`);
  });
  await settle();
  const buttons = ui.elements.get("learning-list").children;
  assert.ok(buttons.length >= 2, JSON.stringify(ui.calls));
  buttons[0].trigger("click");
  buttons[1].trigger("click");
  await settle(50);
  assert.equal(ui.elements.get("learning-title").textContent, second.title);
  assert.equal(ui.elements.get("learning-edit").hidden, true);
  assert.equal(ui.elements.get("learning-delete").hidden, true);
});

test("temporary accounts cannot see publishing or interaction forms", async () => {
  const ui = boot(async (url) => {
    if (url === "/api/users/me") return { data: { id: 3, username: "contest_user", account_type: "contest", global_role: "user", status: "active" } };
    if (url.startsWith("/api/community?kind=list")) return { data: { items: [item(1, "Discussion", { kind: "discussion", can_edit: false, can_delete: false })], next: 0 } };
    if (url === "/api/community/1") return { data: item(1, "Discussion", { kind: "discussion", can_edit: false, can_delete: false }) };
    if (url.startsWith("/api/community?kind=discussion&parent_id=")) return { data: { items: [], next: 0 } };
    throw new Error(`unexpected ${url}`);
  });
  await settle();
  assert.match(ui.elements.get("learning-access-note").textContent, /临时账号/);
  assert.equal(ui.elements.get("learning-new").hidden, true);
  assert.equal(ui.elements.get("learning-form").hidden, true);
  ui.elements.get("learning-list").children[0].trigger("click");
  await settle();
  for (const action of ["favorite", "unfavorite", "like", "unlike", "reply"]) assert.equal(ui.elements.get(`learning-${action}`).hidden, true);
});

test("guest sees login guidance instead of a publishable form", async () => {
  const ui = boot(async (url) => {
    if (url.startsWith("/api/community?kind=list")) return { data: { items: [], next: 0 } };
    throw new Error(`unexpected ${url}`);
  }, null);
  await settle();
  assert.equal(ui.elements.get("learning-form").hidden, true);
  assert.equal(ui.elements.get("learning-new").hidden, true);
  assert.equal(ui.elements.get("learning-login").hidden, false);
  assert.match(ui.elements.get("learning-access-note").textContent, /游客/);
});
