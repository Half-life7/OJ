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
    this.hidden = false;
    this.disabled = false;
    this.value = "";
    this.className = "";
    this._text = "";
  }
  set textContent(value) { this._text = String(value); this.children = []; }
  get textContent() { return this._text + this.children.map((child) => child.textContent || "").join(""); }
  append(...children) { this.children.push(...children); }
  replaceChildren(...children) { this._text = ""; this.children = children; }
  addEventListener(type, callback) { this.handlers.set(type, callback); }
  trigger(type) { return this.handlers.get(type)?.({ preventDefault() {}, target: this }); }
}

class TestFormData {
  constructor(form) { this.data = new Map([...form.fields].map(([name, field]) => [name, field.value])); }
  entries() { return this.data.entries(); }
  [Symbol.iterator]() { return this.entries(); }
}

function boot(pathname, respond) {
  const elements = new Map();
  for (const match of template.matchAll(/<(\w+)\b[^>]*\bid="([^"]+)"[^>]*>/g)) {
    const element = new Element(match[1]);
    element.hidden = /\bhidden\b/.test(match[0]);
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
      form.fields.set(field[3], element);
      values[field[3]] = element;
    }
    form.elements = values;
  }
  const calls = [];
  const location = { pathname, search: "" };
  const document = {
    body: { dataset: { page: "rankings" } },
    hidden: false,
    getElementById(id) { assert.ok(elements.has(id), `missing ID ${id}`); return elements.get(id); },
    createElement: (tag) => new Element(tag),
    querySelectorAll: () => [],
  };
  const window = {
    location,
    addEventListener() {},
    setInterval: () => 1,
    clearInterval() {},
  };
  vm.runInNewContext(script, {
    document, window, location, URLSearchParams, FormData: TestFormData, AbortController,
    setInterval: window.setInterval, clearInterval: window.clearInterval, setTimeout, clearTimeout,
    fetch: async (url) => {
      calls.push(url);
      const result = await respond(url);
      return {
        ok: true,
        status: 200,
        headers: { get: () => null },
        json: async () => ({ code: 0, message: "success", data: result }),
      };
    },
  }, { filename: "community.js" });
  return { elements, calls };
}

async function settle() {
  for (let index = 0; index < 12; index++) await new Promise((resolve) => setImmediate(resolve));
}

function board(kind, item) {
  return { kind, period: "all", built_at: "2026-11-11T08:00:00Z", items: [item], total: 1, page: 1 };
}

test("rankings overview loads and renders all three fixed boards side by side", async () => {
  const ui = boot("/rankings", async (url) => {
    if (url.includes("/leaderboards/practice")) return board("practice", { rank: 1, username: "p", display_name: "Practice", value: 8 });
    if (url.includes("/leaderboards/contest")) return board("contest", { rank: 1, username: "c", display_name: "Contest", value: 150, participations: 3 });
    if (url.includes("/leaderboards/solutions")) return board("solutions", { rank: 1, username: "s", display_name: "Solutions", value: 4 });
    throw new Error(`unexpected ${url}`);
  });
  await settle();
  assert.equal(ui.calls.length, 3);
  assert.match(ui.elements.get("ranking-preview-practice").textContent, /Practice/);
  assert.match(ui.elements.get("ranking-preview-contest").textContent, /150 累计积分 · 3 场/);
  assert.match(ui.elements.get("ranking-preview-solutions").textContent, /Solutions/);
  assert.equal(ui.elements.get("ranking-detail").hidden, true);
});

test("contest ranking detail uses student number and participation columns", async () => {
  const ui = boot("/rankings/contest", async () => board("contest", {
    rank: 1, username: "linked", display_name: "Linked Student", student_no: "S001", value: 188, participations: 4, reached_at: "2026-11-10T10:00:00Z",
  }));
  await settle();
  assert.equal(ui.elements.get("ranking-detail").hidden, false);
  assert.equal(ui.elements.get("ranking-overview").hidden, true);
  assert.match(ui.elements.get("board-rows").textContent, /S0011884/);
  assert.match(ui.elements.get("board-meta").textContent, /1 位参与者/);
});
