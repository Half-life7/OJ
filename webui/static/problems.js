"use strict";

(() => {
  const page = document.body.dataset.page;
  if (!["problems", "problem", "admin-problems"].includes(page)) return;

  const byID = (id) => document.getElementById(id);
  const difficultyLabels = { easy: "简单", medium: "中等", hard: "困难" };
  const statusLabels = { draft: "草稿", published: "已发布", archived: "已下架" };
  const visibilityLabels = { public: "公开", hidden: "隐藏", contest_only: "仅比赛" };
  let csrfToken = "";

  class APIError extends Error {
    constructor(message, status, name = "APIError") {
      super(message);
      this.status = status;
      this.name = name;
    }
  }

  async function request(path, method = "GET", body) {
    const controller = new AbortController();
    const timeout = window.setTimeout(() => controller.abort(), body instanceof FormData ? 60000 : 15000);
    const headers = { Accept: "application/json" };
    if (body !== undefined && !(body instanceof FormData)) headers["Content-Type"] = "application/json";
    if (method !== "GET") headers["X-CSRF-Token"] = csrfToken;
    try {
      const response = await fetch(path, {
        method, headers, credentials: "same-origin", cache: "no-store", signal: controller.signal,
        body: body === undefined ? undefined : body instanceof FormData ? body : JSON.stringify(body),
      });
      let result;
      try {
        result = await response.json();
      } catch {
        throw new APIError(response.status === 413 ? "上传内容超过允许的大小。" : "服务暂时无法响应，请稍后重试。", response.status);
      }
      if (!response.ok || result.code !== 0) {
        if (response.status === 403) csrfToken = "";
        const requestID = response.headers?.get?.("X-Request-ID");
        const message = (response.status === 409 ? "题目已被其他操作更新。请重新加载此题，再确认你的修改。" : result.message || "操作未完成，请稍后重试。") + (requestID ? `（请求 ID：${requestID}）` : "");
        throw new APIError(message, response.status);
      }
      return result.data;
    } catch (error) {
      if (error instanceof APIError) throw error;
      throw new APIError(error.name === "AbortError" ? "请求超时，请重新加载以确认操作结果。" : "网络连接失败，请检查网络后重试。", 0);
    } finally {
      window.clearTimeout(timeout);
    }
  }

  async function write(path, method, body) {
    if (!csrfToken) {
      const result = await request("/api/auth/csrf");
      csrfToken = result && result.csrf_token;
      if (!csrfToken) throw new APIError("无法验证当前页面，请刷新后重试。", 403);
    }
    return request(path, method, body);
  }

  async function uploadWithProgress(path, body) {
    if (!csrfToken) {
      const result = await request("/api/auth/csrf");
      csrfToken = result && result.csrf_token;
      if (!csrfToken) throw new APIError("无法验证当前页面，请刷新后重试。", 403);
    }
    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      uploadXHR = xhr;
      xhr.open("POST", path);
      xhr.timeout = 120000;
      xhr.setRequestHeader("Accept", "application/json");
      xhr.setRequestHeader("X-CSRF-Token", csrfToken);
      xhr.upload.onprogress = (event) => {
        if (!event.lengthComputable) return;
        const percent = Math.max(0, Math.min(100, Math.round(event.loaded * 100 / event.total)));
        setUploadProgress(percent, `正在上传 ${percent}%（${number(event.loaded)} / ${number(event.total)} 字节）`);
      };
      xhr.onload = () => {
        uploadXHR = null;
        let result = null;
        try { result = JSON.parse(xhr.responseText); } catch {}
        if (xhr.status >= 200 && xhr.status < 300 && result?.code === 0) {
          setUploadProgress(100, "上传完成，正在读取发布结果。");
          resolve(result.data);
          return;
        }
        const requestID = xhr.getResponseHeader?.("X-Request-ID");
        const base = xhr.status === 409 ? "题目或数据已被其他操作更新。请重新加载后再合并。" : result?.message || (xhr.status === 413 ? "上传内容超过大小限制。" : "上传未完成，请稍后重试。");
        reject(new APIError(base + (requestID ? `（请求 ID：${requestID}）` : ""), xhr.status));
      };
      xhr.onerror = () => { uploadXHR = null; reject(new APIError("网络连接中断。正在核对是否已发布。", 0)); };
      xhr.ontimeout = () => { uploadXHR = null; reject(new APIError("上传等待超过 120 秒。正在核对是否已发布。", 0)); };
      xhr.onabort = () => { uploadXHR = null; reject(new APIError("上传已取消。正在核对服务器是否已经完成发布。", 0, "AbortError")); };
      xhr.send(body);
    });
  }

  function node(tag, text, className) {
    const element = document.createElement(tag);
    if (text !== undefined && text !== null) element.textContent = String(text);
    if (className) element.className = className;
    return element;
  }

  function message(id, text, success = false) {
    const element = byID(id);
    element.textContent = text;
    element.classList.toggle("success", success);
    element.hidden = !text;
  }

  function state(text, loading = false, retry = false, login = false) {
    const element = byID("problem-state");
    element.hidden = false;
    element.setAttribute("aria-busy", String(loading));
    element.querySelector(".loading-dot").hidden = !loading;
    byID("problem-state-text").textContent = text;
    byID("problem-retry").hidden = !retry;
    byID("problem-login").hidden = !login;
  }

  function number(value) {
    return typeof value === "number" && Number.isFinite(value) ? value.toLocaleString("zh-CN") : "—";
  }

  function date(value) {
    const parsed = new Date(value);
    return Number.isNaN(parsed.valueOf()) ? "—" : parsed.toLocaleString("zh-CN");
  }

  function queryFrom(form, pageNumber) {
    const query = new URLSearchParams({ page: String(pageNumber), limit: "20" });
    for (const [key, value] of new FormData(form)) {
      if (typeof value === "string" && value.trim()) query.set(key, value.trim());
    }
    return query;
  }

  function pageInfo(prefix, result) {
    const pageNumber = result.page || 1;
    const limit = result.limit || 20;
    byID(prefix + "-page-info").textContent = "第 " + number(pageNumber) + " 页 · 共 " + number(result.total) + " 项";
    byID(prefix + "-prev").disabled = pageNumber <= 1;
    byID(prefix + "-next").disabled = pageNumber * limit >= result.total;
  }

  function badge(text, className = "") {
    return node("span", text, "problem-badge " + className);
  }

  function difficultyBadge(value) {
    return badge(difficultyLabels[value] || value, Object.hasOwn(difficultyLabels, value) ? "difficulty-" + value : "");
  }

  function problemCard(problem, select) {
    const card = node("article", undefined, "problem-card");
    const heading = node("div", undefined, "problem-card-heading");
    const title = node("h2");
    const link = node(select ? "button" : "a", problem.title);
    if (select) {
      link.type = "button";
      link.className = "problem-select";
      link.addEventListener("click", () => select(problem.id));
    } else {
      link.href = "/problems/" + encodeURIComponent(problem.id);
    }
    title.append(link);
    heading.append(node("span", "#" + problem.id, "problem-number"), title, difficultyBadge(problem.difficulty));
    const meta = node("div", undefined, "problem-badges");
    for (const tag of problem.tags || []) meta.append(badge(tag));
    if (select) {
      meta.append(badge(statusLabels[problem.status] || problem.status), badge(visibilityLabels[problem.visibility] || problem.visibility));
    }
    card.append(heading, meta);
    return card;
  }

  if (page === "problems") {
    const form = byID("problem-filter-form");
    const params = new URLSearchParams(window.location.search);
    for (const name of ["q", "difficulty", "tag"]) form.elements.namedItem(name).value = params.get(name) || "";
    let currentPage = Number(params.get("page"));
    if (!Number.isSafeInteger(currentPage) || currentPage < 1) currentPage = 1;
    let epoch = 0;

    async function load(pageNumber = currentPage) {
      currentPage = pageNumber;
      const sequence = ++epoch;
      state("正在加载题目…", true);
      byID("problem-results").hidden = true;
      const query = queryFrom(form, currentPage);
      window.history.replaceState(null, "", "/problems?" + query);
      try {
        const result = await request("/api/problems?" + query);
        if (sequence !== epoch) return;
        const list = byID("problem-list");
        list.replaceChildren();
        for (const problem of result.items || []) list.append(problemCard(problem));
        if (!result.items || result.items.length === 0) list.append(node("p", "没有找到符合条件的题目。试试其他关键词或筛选条件。", "problem-empty"));
        pageInfo("problem", result);
        byID("problem-state").hidden = true;
        byID("problem-results").hidden = false;
      } catch (error) {
        if (sequence === epoch) state(error.message, false, true);
      }
    }
    form.addEventListener("submit", (event) => { event.preventDefault(); load(1); });
    byID("problem-prev").addEventListener("click", () => load(Math.max(1, currentPage - 1)));
    byID("problem-next").addEventListener("click", () => load(currentPage + 1));
    byID("problem-retry").addEventListener("click", () => load());
    load();
  }

  if (page === "problem") {
    const id = window.location.pathname.split("/")[2];
    async function load() {
      state("正在读取题面…", true);
      byID("problem-detail").hidden = true;
      try {
        const problem = await request("/api/problems/" + encodeURIComponent(id));
        byID("problem-title").textContent = problem.title;
        document.title = problem.title + " · Biue OJ";
        byID("problem-metadata").replaceChildren(badge("#" + problem.id), difficultyBadge(problem.difficulty), badge(number(problem.time_limit_ms) + " ms"), badge(number(problem.memory_limit_mb) + " MiB"));
        byID("problem-tags").replaceChildren(...(problem.tags || []).map((tag) => badge(tag)));
        for (const [field, suffix, htmlField] of [["description", "description", "description_html"], ["input_format", "input-format", "input_html"], ["output_format", "output-format", "output_html"], ["sample_input", "sample-input", ""], ["sample_output", "sample-output", ""]]) {
          const target = byID("problem-" + suffix);
          if (htmlField && problem[htmlField]) target.innerHTML = problem[htmlField];
          else target.textContent = problem[field] || "";
        }
        byID("problem-state").hidden = true;
        byID("problem-detail").hidden = false;
      } catch (error) {
        state(error.status === 404 ? "题目不存在或尚未公开。" : error.message, false, error.status !== 404);
      }
    }
    byID("problem-retry").addEventListener("click", load);
    load();
  }

  if (page !== "admin-problems") return;

  const editor = byID("problem-editor-form");
  const upload = byID("problem-upload-form");
  const adminFilters = byID("admin-filter-form");
  function previewMarkdown(source) {
    byID("problem-markdown-preview").hidden = false;
    byID("problem-markdown-preview-html").textContent = "正在生成安全预览…";
    return write("/api/admin/problems/markdown-preview", "POST", { markdown: source }).then((result) => {
      byID("problem-markdown-preview-html").innerHTML = result.html;
      byID("problem-markdown-preview").scrollIntoView?.({ behavior: "smooth", block: "nearest" });
    }).catch((error) => {
      byID("problem-markdown-preview-html").textContent = error.message;
    });
  }
  function mountTextEditor(id, language, options) {
    if (!window.BiueEditor || typeof window.BiueEditor.mount !== "function") return null;
    return window.BiueEditor.mount(byID(id), language, options);
  }
  const markdownEditors = {
    description: mountTextEditor("edit-description", "markdown", { ariaLabel: "题目描述 Markdown", onPreview: previewMarkdown }),
    input_format: mountTextEditor("edit-input-format", "markdown", { ariaLabel: "输入格式 Markdown", onPreview: previewMarkdown }),
    output_format: mountTextEditor("edit-output-format", "markdown", { ariaLabel: "输出格式 Markdown", onPreview: previewMarkdown }),
  };
  const checkerEditor = mountTextEditor("edit-checker-source", "python3", { ariaLabel: "Python Special Judge 源码" });
  const editorFields = ["title", "description", "input_format", "output_format", "sample_input", "sample_output", "time_limit_ms", "memory_limit_mb", "difficulty", "status", "visibility"];
  let selected = null;
  let dirty = false;
  let mutationPending = false;
  let adminPage = 1;
  let versionPage = 1;
  let listEpoch = 0;
  let editorEpoch = 0;
  let versionEpoch = 0;
  let savedEditor = "";
  let adminAllowed = false;
  let uploadPreview = null;
  let uploadXHR = null;
  let uploadInspectEpoch = 0;
  let scoreInputs = new Map();
  let activeWorkspace = "list";
  const directFileLimit = 16 * 1024 * 1024;
  const directTotalLimit = 64 * 1024 * 1024;
  const archiveLimit = 32 * 1024 * 1024;
  const maxCases = 512;
  const checkerTemplate = `import base64
import json
import sys

data = json.load(sys.stdin)
input_data = base64.b64decode(data["input"])
expected = base64.b64decode(data["expected"])
output = base64.b64decode(data["output"])

# Replace this exact comparison when the problem accepts multiple answers.
print("AC" if output.split() == expected.split() else "WA")
`;

  function updateCheckerWorkspace() {
    const mode = editor.elements.namedItem("checker_mode")?.value || "tokens";
    byID("checker-workspace").hidden = mode !== "spj";
    const source = editor.elements.namedItem("checker_source");
    if (source) source.disabled = mode !== "spj";
    checkerEditor?.setDisabled(mode !== "spj");
    byID("checker-example-input").value = editor.elements.namedItem("sample_input")?.value || "";
    byID("checker-example-output").value = editor.elements.namedItem("sample_output")?.value || "";
    const link = byID("checker-self-test");
    link.hidden = !selected || mode !== "spj" || selected.status !== "published" || selected.visibility !== "public";
    if (selected) link.href = "/problems/" + encodeURIComponent(selected.id);
  }

  function setUploadProgress(percent, text) {
    const progress = byID("data-upload-progress");
    const label = byID("data-upload-progress-label");
    progress.hidden = false;
    progress.value = percent;
    label.hidden = false;
    label.textContent = text;
  }

  function clearUploadProgress() {
    const progress = byID("data-upload-progress");
    progress.value = 0;
    progress.hidden = true;
    byID("data-upload-progress-label").hidden = true;
  }

  function invalidateUpload() {
    uploadPreview = null;
    scoreInputs = new Map();
    byID("data-preview").hidden = true;
    byID("data-score-table").replaceChildren();
    byID("data-scores").value = "";
    byID("data-upload-submit").disabled = true;
    message("problem-upload-message", "");
    clearUploadProgress();
  }

  function validCaseID(id) {
    if (!/^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(id)) return false;
    const upper = id.toUpperCase();
    return !["CON", "PRN", "AUX", "NUL"].includes(upper) && !/^(COM|LPT)[1-9]$/.test(upper);
  }

  function inspectNames(names) {
    const pairs = new Map();
    const folded = new Map();
    const invalid = [];
    const duplicates = [];
    const seen = new Set();
    for (const name of names) {
      if (seen.has(name)) {
        duplicates.push(name);
        continue;
      }
      seen.add(name);
      const match = /^([^/\\]+)\.(in|out)$/.exec(name);
      if (!match || !validCaseID(match[1])) {
        invalid.push(name);
        continue;
      }
      const id = match[1];
      const fold = id.toLowerCase();
      if (folded.has(fold) && folded.get(fold) !== id) {
        duplicates.push(`${folded.get(fold)} / ${id}（大小写冲突）`);
        continue;
      }
      folded.set(fold, id);
      const pair = pairs.get(id) || { in: false, out: false };
      pair[match[2]] = true;
      pairs.set(id, pair);
    }
    const ids = [...pairs.keys()].sort();
    const missing = ids.filter((id) => !pairs.get(id).in || !pairs.get(id).out).map((id) => !pairs.get(id).in ? `${id}.in` : `${id}.out`);
    return { ids, invalid, duplicates, missing, valid: ids.length > 0 && invalid.length === 0 && duplicates.length === 0 && missing.length === 0 && ids.length <= maxCases };
  }

  async function zipEntryNames(file) {
    if (typeof file.arrayBuffer !== "function") throw new APIError("当前浏览器无法预读 ZIP 目录，请刷新后重试或直接选择 .in/.out 文件。", 400);
    const raw = new Uint8Array(await file.arrayBuffer());
    const view = new DataView(raw.buffer, raw.byteOffset, raw.byteLength);
    let eocd = -1;
    const first = Math.max(0, raw.length - 65557);
    for (let offset = raw.length - 22; offset >= first; offset--) {
      if (view.getUint32(offset, true) === 0x06054b50) { eocd = offset; break; }
    }
    if (eocd < 0) throw new APIError("ZIP 目录不完整或文件不是有效 ZIP。", 400);
    const count = view.getUint16(eocd + 10, true);
    if (count < 1 || count > maxCases * 2) throw new APIError("ZIP 必须包含 1～1024 个 .in/.out 文件。", 400);
    let offset = view.getUint32(eocd + 16, true);
    const decoder = new TextDecoder("utf-8", { fatal: true });
    const names = [];
    for (let index = 0; index < count; index++) {
      if (offset + 46 > raw.length || view.getUint32(offset, true) !== 0x02014b50) throw new APIError("ZIP 中央目录损坏。", 400);
      const nameLength = view.getUint16(offset + 28, true);
      const extraLength = view.getUint16(offset + 30, true);
      const commentLength = view.getUint16(offset + 32, true);
      const end = offset + 46 + nameLength;
      if (end > raw.length) throw new APIError("ZIP 文件名越界。", 400);
      names.push(decoder.decode(raw.slice(offset + 46, end)));
      offset = end + extraLength + commentLength;
    }
    return names;
  }

  function setDefaultScores(ids) {
    scoreInputs = new Map();
    const base = ids.length ? Math.floor(100 / ids.length) : 0;
    ids.forEach((id, index) => {
      const score = index === ids.length - 1 ? base + 100 - base * ids.length : base;
      scoreInputs.set(id, String(score));
    });
  }

  function renderScores(ids) {
    const table = node("table", undefined, "problem-case-table score-entry-table");
    const head = node("thead");
    const row = node("tr");
    for (const label of ["测试点", "输入文件", "输出文件", "分值"]) {
      const th = node("th", label);
      th.scope = "col";
      row.append(th);
    }
    head.append(row);
    const body = node("tbody");
    for (const id of ids) {
      const line = node("tr");
      line.append(node("td", id), node("td", `${id}.in`), node("td", `${id}.out`));
      const input = node("input");
      input.type = "number";
      input.min = "0";
      input.step = "1";
      input.value = scoreInputs.get(id) || "0";
      input.setAttribute("data-score-id", id);
      input.addEventListener("input", () => {
        scoreInputs.set(id, input.value);
        validateScores(true);
      });
      const cell = node("td");
      cell.append(input);
      line.append(cell);
      body.append(line);
    }
    table.append(head, body);
    byID("data-score-table").replaceChildren(table);
  }

  function validateScores(write = false) {
    const errors = [];
    let total = 0n;
    for (const [id, raw] of scoreInputs) {
      if (!/^(0|[1-9][0-9]*)$/.test(raw)) {
        errors.push(`${id} 的分值必须是整数`);
        continue;
      }
      const value = BigInt(raw);
      if (value < 0n || value > 9223372036854775807n) errors.push(`${id} 的分值超出 64 位整数范围`);
      if (total > 9223372036854775807n - value) errors.push("总分超出 64 位整数范围");
      else total += value;
    }
    if (total <= 0n) errors.push("总分必须大于零");
    const unique = [...new Set(errors)];
    const error = byID("data-preview-error");
    error.textContent = unique.join("；");
    error.hidden = unique.length === 0;
    byID("data-upload-submit").disabled = unique.length > 0 || !uploadPreview?.valid || mutationPending;
    if (write || unique.length === 0) {
      byID("data-scores").value = unique.length === 0 ? `{${[...scoreInputs].map(([id, score]) => `${JSON.stringify(id)}:${score}`).join(",")}}` : "";
    }
    if (uploadPreview) {
      byID("data-preview-summary").textContent = `${uploadPreview.kind === "zip" ? "ZIP 批量" : "直接文件"} · ${scoreInputs.size} 个测试点 · 总分 ${total.toString()} · 发布方式：${byID("data-import-mode").value === "merge" ? "合并/替换同 ID" : "完整替换"}`;
    }
    return unique.length === 0;
  }

  async function refreshUploadPreview() {
    const sequence = ++uploadInspectEpoch;
    const files = Array.from(byID("data-files").files || []);
    invalidateUpload();
    if (files.length === 0) return null;
    byID("data-preview").hidden = false;
    byID("data-preview-error").hidden = false;
    byID("data-preview-error").textContent = "正在校验文件名和配对…";
    let names;
    let kind = "files";
    try {
      if (files.length === 1 && /\.zip$/i.test(files[0].name)) {
        if (files[0].size <= 0 || files[0].size > archiveLimit) throw new APIError("ZIP 必须非空且不超过 32 MiB。", 400);
        kind = "zip";
        names = await zipEntryNames(files[0]);
      } else {
        if (files.some((file) => /\.zip$/i.test(file.name))) throw new APIError("ZIP 不能与单个 .in/.out 文件混合上传。", 400);
        if (files.some((file) => file.size > directFileLimit)) throw new APIError("每个直接上传文件不得超过 16 MiB。", 400);
        if (files.reduce((sum, file) => sum + file.size, 0) > directTotalLimit) throw new APIError("直接文件合计不得超过 64 MiB。", 400);
        names = files.map((file) => file.name);
      }
    } catch (error) {
      if (sequence !== uploadInspectEpoch) return null;
      uploadPreview = { valid: false, kind, files };
      byID("data-preview-error").textContent = error.message;
      byID("data-preview-summary").textContent = "未生成测试点";
      validateScores(true);
      return uploadPreview;
    }
    if (sequence !== uploadInspectEpoch) return null;
    const inspected = inspectNames(names);
    uploadPreview = { valid: inspected.valid, kind, files, names, ids: inspected.ids };
    const problems = [];
    if (inspected.invalid.length) problems.push(`非法文件：${inspected.invalid.join("、")}`);
    if (inspected.duplicates.length) problems.push(`重复或冲突：${inspected.duplicates.join("、")}`);
    if (inspected.missing.length) problems.push(`缺少配对：${inspected.missing.join("、")}`);
    if (inspected.ids.length > maxCases) problems.push(`共 ${inspected.ids.length} 个测试点，超过 512 上限`);
    if (inspected.ids.length === 0) problems.push("没有发现合法的 .in/.out 测试点");
    byID("data-preview-error").textContent = problems.join("；");
    byID("data-preview-error").hidden = problems.length === 0;
    if (inspected.ids.length && !inspected.invalid.length && !inspected.duplicates.length && !inspected.missing.length && inspected.ids.length <= maxCases) {
      setDefaultScores(inspected.ids);
      renderScores(inspected.ids);
      validateScores(true);
    } else {
      scoreInputs = new Map();
      byID("data-score-table").replaceChildren();
      byID("data-preview-summary").textContent = "未生成测试点";
      validateScores(true);
    }
    return uploadPreview;
  }

  function editorSnapshot() {
    return JSON.stringify([...editorFields, "tags", "problem_type", "checker_mode", "checker_source"].map(name => editor.elements.namedItem(name)?.value || ""));
  }

  function workspaceRoute(name) {
    if (selected && !["list", "new"].includes(name)) return `#${name}-${selected.id}`;
    return `#${name}`;
  }

  function workspaceFromHash() {
    const match = /^#(list|new|edit|data|versions)(?:-(\d+))?$/.exec(window.location.hash || "");
    if (!match) return { name: "list", id: 0 };
    return { name: match[1], id: Number(match[2] || 0) };
  }

  function switchWorkspace(name, updateHash = true) {
    if (!["list", "new", "edit", "data", "versions"].includes(name)) name = "list";
    if (!selected && ["edit", "data", "versions"].includes(name)) name = "new";
    activeWorkspace = name;
    const pane = name === "new" ? "edit" : name;
    for (const candidate of ["list", "edit", "data", "versions"]) byID(`problem-${candidate}-workspace`).hidden = candidate !== pane;
    for (const button of ["list", "new", "edit", "data", "versions"]) {
      byID(`problem-nav-${button}`).classList.toggle("active", button === activeWorkspace);
    }
    if (updateHash && window.history?.replaceState) {
      const base = `${window.location.pathname || ""}${window.location.search || ""}`;
      window.history.replaceState(null, "", base + workspaceRoute(name));
    }
  }

  function updateControls() {
    byID("problem-editor-fields").disabled = mutationPending || !adminAllowed;
    byID("problem-upload-fields").disabled = !adminAllowed || mutationPending || !selected || dirty;
    byID("problem-upload-state").textContent = !adminAllowed ? "需要有效的超级管理员会话才能上传。" : mutationPending ? "正在处理操作，请稍候…" : !selected ? "请先创建题目，或从列表选择已保存的题目。" : dirty ? "题面有未保存修改。请先保存，或重新加载以放弃修改。" : "题面已保存，可以拖入成对 .in/.out 文件，或选择 ZIP。";
    byID("data-upload-submit").disabled = !adminAllowed || mutationPending || !selected || dirty || !uploadPreview?.valid;
    byID("problem-save-for-upload").hidden = !adminAllowed || mutationPending || (!!selected && !dirty);
    byID("problem-new").disabled = mutationPending;
    byID("problem-nav-new").disabled = mutationPending;
    for (const name of ["edit", "data", "versions"]) byID(`problem-nav-${name}`).disabled = mutationPending || !selected;
    byID("problem-reload").disabled = mutationPending;
    for (const button of byID("admin-problem-list").querySelectorAll("button")) button.disabled = mutationPending;
  }

  function adminError(error, target) {
    message(target, error.message);
    if (error.status === 401) {
      adminAllowed = false;
      updateControls();
      byID("problem-admin").hidden = true;
      state("登录会话已失效，请重新登录后再管理题目。", false, false, true);
    } else if (error.status === 403) {
      adminAllowed = false;
      updateControls();
      byID("problem-admin").hidden = true;
      state("当前操作未获授权，请重新验证管理权限。", false, true);
    }
  }

  function mayDiscard() {
    return !dirty || window.confirm("切换题目会放弃未保存的题面修改。继续？");
  }

  function fillEditor(problem) {
    selected = problem;
    dirty = false;
    const defaults = { title: "", description: "", input_format: "", output_format: "", sample_input: "", sample_output: "", time_limit_ms: 1000, memory_limit_mb: 256, difficulty: "medium", status: "draft", visibility: "hidden" };
    for (const field of editorFields) editor.elements.namedItem(field).value = problem ? problem[field] : defaults[field];
    editor.elements.namedItem("tags").value = problem ? (problem.tags || []).join(", ") : "";
      for (const field of ["problem_type","checker_mode","checker_source"]) {const input=editor.elements.namedItem(field);if(input)input.value=problem?.[field]||(field==="problem_type"?"programming":field==="checker_mode"?"tokens":"");}
    for (const [field, codeEditor] of Object.entries(markdownEditors)) codeEditor?.setValue(editor.elements.namedItem(field).value);
    checkerEditor?.setValue(editor.elements.namedItem("checker_source").value);
    byID("problem-editor-mode").textContent = problem ? "EDIT PROBLEM #" + problem.id : "NEW PROBLEM";
    byID("problem-editor-heading").textContent = problem ? "编辑题目" : "创建题目";
    byID("problem-editor-version").textContent = problem ? "修订 " + problem.revision + " · 题面版本 " + problem.statement_version + " · 更新于 " + date(problem.updated_at) : "新题默认为隐藏草稿。";
    byID("problem-save").textContent = problem ? "保存题面修改" : "创建题目";
    byID("problem-reload").hidden = !problem;
    const publicPreview = byID("problem-public-preview");
    publicPreview.hidden = !problem || problem.status !== "published" || problem.visibility !== "public";
    publicPreview.href = problem ? `/problems?q=${encodeURIComponent(problem.title)}` : "/problems";
    byID("problem-current-data").textContent = problem && problem.data_version ? "当前使用数据版本 v" + problem.data_version : "尚未上传测试数据。";
    byID("problem-version-detail").hidden = true;
    byID("problem-version-list").replaceChildren();
    message("problem-editor-message", "");
    message("problem-upload-message", "");
    message("problem-versions-message", "");
    byID("problem-versions-retry").hidden = true;
    invalidateUpload();
    upload.reset();
    byID("data-import-mode").value = "replace";
    updateCheckerWorkspace();
    savedEditor = editorSnapshot();
    dirty = false;
    updateControls();
  }

  async function loadList(pageNumber = adminPage) {
    adminPage = pageNumber;
    const sequence = ++listEpoch;
    const list = byID("admin-problem-list");
    list.setAttribute("aria-busy", "true");
    message("admin-list-message", "正在加载题目…", true);
    byID("admin-list-retry").hidden = true;
    try {
      const result = await request("/api/admin/problems?" + queryFrom(adminFilters, adminPage));
      if (sequence !== listEpoch) return;
      list.replaceChildren();
      for (const problem of result.items || []) list.append(problemCard(problem, selectProblem));
      if (!result.items || result.items.length === 0) list.append(node("p", "没有找到符合条件的题目。", "problem-empty"));
      pageInfo("admin", result);
      message("admin-list-message", "");
      updateControls();
    } catch (error) {
      if (sequence === listEpoch) {
        adminError(error, "admin-list-message");
        byID("admin-list-retry").hidden = error.status === 401;
      }
    } finally {
      if (sequence === listEpoch) list.removeAttribute("aria-busy");
    }
  }

  async function selectProblem(id, force = false) {
    if (mutationPending || (!force && !mayDiscard())) return;
    const sequence = ++editorEpoch;
    mutationPending = true;
    updateControls();
    message("problem-editor-message", "正在读取题目…", true);
    try {
      const problem = await request("/api/admin/problems/" + encodeURIComponent(id));
      if (sequence !== editorEpoch) return;
      fillEditor(problem);
      switchWorkspace("edit");
      versionPage = 1;
      loadVersions();
    } catch (error) {
      if (sequence === editorEpoch) adminError(error, "problem-editor-message");
    } finally {
      if (sequence === editorEpoch) {
        mutationPending = false;
        updateControls();
      }
    }
  }

  async function loadVersions(pageNumber = versionPage) {
    if (!selected) return;
    versionPage = pageNumber;
    const sequence = ++versionEpoch;
    const id = selected.id;
    message("problem-versions-message", "正在读取数据版本…", true);
    byID("problem-versions-retry").hidden = true;
    byID("problem-version-detail").hidden = true;
    try {
      const result = await request("/api/admin/problems/" + encodeURIComponent(id) + "/data-versions?page=" + versionPage + "&limit=20");
      if (sequence !== versionEpoch || !selected || selected.id !== id) return;
      const list = byID("problem-version-list");
      list.replaceChildren();
      for (const version of result.items || []) {
        const item = node("article", undefined, "problem-version-item");
        const name = "v" + version.data_version + (version.data_version === selected.data_version ? " · 当前" : "");
        item.append(node("h3", name), node("p", number(version.case_count) + " 个测试点 · " + number(version.total_score) + " 分 · " + number(version.total_bytes) + " 字节"), node("p", date(version.created_at)));
        const button = node("button", "查看分值", "text-button");
        button.type = "button";
        button.addEventListener("click", () => loadVersion(version.data_version, button));
        item.append(button);
        list.append(item);
      }
      if (!result.items || result.items.length === 0) list.append(node("p", "还没有数据版本。上传第一个测试数据 ZIP 即可创建。", "problem-empty"));
      pageInfo("version", result);
      message("problem-versions-message", "");
    } catch (error) {
      if (sequence === versionEpoch) {
        adminError(error, "problem-versions-message");
        byID("problem-versions-retry").hidden = error.status === 401;
      }
    }
  }

  async function loadVersion(version, button) {
    const id = selected && selected.id;
    if (!id) return;
    const sequence = ++versionEpoch;
    button.disabled = true;
    message("problem-versions-message", "正在读取测试点分值…", true);
    try {
      const result = await request("/api/admin/problems/" + encodeURIComponent(id) + "/data-versions/" + encodeURIComponent(version));
      if (sequence !== versionEpoch || !selected || selected.id !== id) return;
      const detail = byID("problem-version-detail");
      detail.replaceChildren(node("h3", "数据版本 v" + result.data_version));
      detail.append(node("p", "内容摘要：" + result.digest, "problem-digest"));
      const table = node("table", undefined, "problem-case-table");
      const head = node("thead");
      const headings = node("tr");
      for (const label of ["测试点", "分值", "输入字节", "输出字节"]) {
        const cell = node("th", label);
        cell.scope = "col";
        headings.append(cell);
      }
      head.append(headings);
      const body = node("tbody");
      for (const entry of result.cases || []) {
        const row = node("tr");
        for (const value of [entry.id, number(entry.score), number(entry.input_bytes), number(entry.output_bytes)]) row.append(node("td", value));
        body.append(row);
      }
      table.append(head, body);
      detail.append(table);
      detail.hidden = false;
      message("problem-versions-message", "");
    } catch (error) {
      if (sequence === versionEpoch) adminError(error, "problem-versions-message");
    } finally {
      button.disabled = false;
    }
  }

  function editorInput() {
    const result = Object.fromEntries(new FormData(editor));
    result.time_limit_ms = Number(result.time_limit_ms);
    result.memory_limit_mb = Number(result.memory_limit_mb);
    result.tags = result.tags.trim() ? result.tags.split(/[,，]/).map((tag) => tag.trim()) : [];
    if (!result.title.trim() || Array.from(result.title).length > 200) throw new APIError("请填写最多 200 个字符的题目标题。", 400);
    const byteLength = (value) => new TextEncoder().encode(value).length;
    if (!result.description.trim() || byteLength(result.description) > 65536) throw new APIError("请填写最多 64 KiB 的题目描述。", 400);
    for (const field of ["input_format", "output_format", "sample_input", "sample_output"]) {
      if (byteLength(result[field]) > 16384) throw new APIError("输入输出格式与每项样例各最多 16 KiB。", 400);
    }
    if (result.tags.length > 16 || result.tags.some((tag) => !tag || Array.from(tag).length > 32)) throw new APIError("最多填写 16 个非空标签，每项最多 32 个字符。", 400);
    if (!Number.isSafeInteger(result.time_limit_ms) || result.time_limit_ms < 1 || result.time_limit_ms > 60000 || !Number.isSafeInteger(result.memory_limit_mb) || result.memory_limit_mb < 16 || result.memory_limit_mb > 4096) throw new APIError("请检查时间和内存限制。", 400);
    if (selected) result.revision = selected.revision;
    return result;
  }

  function editorChanged() { dirty = editorSnapshot() !== savedEditor; updateCheckerWorkspace(); updateControls(); }
  editor.addEventListener("input", editorChanged);
  editor.addEventListener("change", editorChanged);
  editor.elements.namedItem("checker_mode").addEventListener("change", updateCheckerWorkspace);
  editor.elements.namedItem("visibility").addEventListener("change", (event) => {
    if (event.target.value === "public" && editor.elements.namedItem("status").value === "draft") {
      editor.elements.namedItem("status").value = "published";
      message("problem-editor-message", "选择公开可见时，题目会同时设为已发布，保存后出现在公共题库。");
    }
    editorChanged();
  });
  byID("checker-template").addEventListener("click", () => {
    editor.elements.namedItem("checker_source").value = checkerTemplate;
    checkerEditor?.setValue(checkerTemplate);
    editorChanged();
  });
  byID("checker-file").addEventListener("change", async (event) => {
    const file = event.target.files?.[0];
    if (!file) return;
    if (file.size > 64 * 1024) { message("problem-editor-message", "SPJ 源码最多 64 KiB。"); return; }
    const source = await file.text();
    editor.elements.namedItem("checker_source").value = source;
    checkerEditor?.setValue(source);
    editorChanged();
  });
  byID("problem-markdown-preview-close").addEventListener("click", () => { byID("problem-markdown-preview").hidden = true; });
  byID("problem-save-for-upload").addEventListener("click", () => editor.requestSubmit());

  function autoRejudgeKey() {
    if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") return "auto-" + crypto.randomUUID();
    return `auto-${Date.now()}-${Math.random().toString(36).slice(2, 12)}`;
  }

  async function autoRejudgeProblem(id) {
    if (!id) return null;
    return write("/api/admin/operations/rejudge/problem", "POST", {
      key: autoRejudgeKey(), reason: "题目编辑或测试数据发布后自动重测", problem_id: Number(id),
    });
  }

  editor.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (mutationPending) return;
    let input;
    try { input = editorInput(); } catch (error) { message("problem-editor-message", error.message); return; }
    const id = selected && selected.id;
    mutationPending = true;
    updateControls();
    message("problem-editor-message", "正在保存…", true);
    try {
      const problem = await write("/api/admin/problems" + (id ? "/" + encodeURIComponent(id) : ""), id ? "PUT" : "POST", input);
      fillEditor(problem);
      switchWorkspace("edit");
      let savedMessage = id ? "题面已保存。" : "题目已创建，可以上传测试数据。";
      try {
        const rejudge = await autoRejudgeProblem(problem.id);
        savedMessage += ` 自动重测已排队 ${rejudge?.queued || 0} 条，跳过 ${rejudge?.skipped || 0} 条。`;
        if (rejudge?.complete === false) savedMessage += " 后续批次未全部入队：" + (rejudge.warnings || []).join("；");
      } catch (error) {
        savedMessage += " 自动重测失败：" + error.message;
      }
      message("problem-editor-message", savedMessage, !savedMessage.includes("自动重测失败") && !savedMessage.includes("后续批次未全部入队"));
      versionPage = 1;
      loadVersions();
      loadList();
    } catch (error) {
      adminError(error, "problem-editor-message");
    } finally {
      mutationPending = false;
      updateControls();
    }
  });

  async function reconcileUploadOutcome(id, previousRevision, previousDataVersion) {
    try {
      const latest = await request("/api/admin/problems/" + encodeURIComponent(id));
      const exactNextVersion = latest.revision === previousRevision + 1 && latest.data_version === previousDataVersion + 1;
      selected = latest;
      byID("problem-editor-version").textContent = "修订 " + latest.revision + " · 题面版本 " + latest.statement_version;
      byID("problem-current-data").textContent = latest.data_version ? "当前使用数据版本 v" + latest.data_version : "尚未上传测试数据。";
      versionPage = 1;
      await loadVersions();
      if (exactNextVersion) {
        message("problem-upload-message", `连接中断前服务器已完成发布，当前数据版本为 v${latest.data_version}。系统没有自动重复上传。`, true);
      } else {
        message("problem-upload-message", `服务器版本已变为 v${latest.data_version || 0}（修订 ${latest.revision}），但无法确认是否由本次上传产生。请核对下方版本记录；系统没有自动重试。`);
      }
    } catch {
      message("problem-upload-message", "无法确认上传结果。请稍后刷新数据版本；系统没有自动重试。");
    }
  }

  upload.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (mutationPending || !selected || dirty) return;
    const currentFiles = Array.from(byID("data-files").files || []);
    const previewIsCurrent = uploadPreview && uploadPreview.files.length === currentFiles.length && currentFiles.every((file, index) => file === uploadPreview.files[index]);
    const preview = previewIsCurrent ? uploadPreview : await refreshUploadPreview();
    if (!preview?.valid || !validateScores(true)) {
      message("problem-upload-message", "请先修正文件配对或分值。");
      return;
    }
    const id = selected.id;
    const previousRevision = selected.revision;
    const previousDataVersion = selected.data_version || 0;
    const formData = new FormData();
    if (preview.kind === "zip") formData.append("archive", preview.files[0]);
    else for (const file of preview.files) formData.append("files", file);
    formData.append("scores", byID("data-scores").value);
    formData.append("revision", String(previousRevision));
    formData.append("mode", byID("data-import-mode").value);
    mutationPending = true;
    updateControls();
    setUploadProgress(1, "正在上传 1%");
    message("problem-upload-message", "正在上传并在服务器完整校验文件、配对、分值与大小。", true);
    byID("data-upload-cancel").hidden = false;
    try {
      const version = await uploadWithProgress("/api/admin/problems/" + encodeURIComponent(id) + "/data-versions", formData);
      selected = { ...selected, revision: version.revision, data_version: version.data_version };
      byID("problem-editor-version").textContent = "修订 " + selected.revision + " · 题面版本 " + selected.statement_version;
      byID("problem-current-data").textContent = "当前使用数据版本 v" + version.data_version;
      let uploadedMessage = "数据版本 v" + version.data_version + " 已发布，共 " + number(version.case_count) + " 个测试点。旧版本已保留。";
      try {
        const rejudge = await autoRejudgeProblem(id);
        uploadedMessage += ` 自动重测已排队 ${rejudge?.queued || 0} 条，跳过 ${rejudge?.skipped || 0} 条。`;
        if (rejudge?.complete === false) uploadedMessage += " 后续批次未全部入队：" + (rejudge.warnings || []).join("；");
      } catch (error) {
        uploadedMessage += " 自动重测失败：" + error.message;
      }
      upload.reset();
      invalidateUpload();
      versionPage = 1;
      loadVersions();
      message("problem-upload-message", uploadedMessage, !uploadedMessage.includes("自动重测失败") && !uploadedMessage.includes("后续批次未全部入队"));
    } catch (error) {
      if (error.status === 0 || error.status >= 500) {
        await reconcileUploadOutcome(id, previousRevision, previousDataVersion);
      } else {
        adminError(error, "problem-upload-message");
      }
    } finally {
      uploadXHR = null;
      byID("data-upload-cancel").hidden = true;
      mutationPending = false;
      updateControls();
    }
  });

  byID("data-files").addEventListener("change", () => {
    if (!mutationPending) refreshUploadPreview().catch((error) => message("problem-upload-message", error.message));
  });
  byID("data-import-mode").addEventListener("change", () => { if (uploadPreview?.valid) validateScores(true); });
  byID("data-dropzone").addEventListener("click", () => { if (!mutationPending) byID("data-files").click(); });
  byID("data-dropzone").addEventListener("keydown", (event) => {
    if (event.key === "Enter" || event.key === " ") { event.preventDefault(); byID("data-files").click(); }
  });
  for (const type of ["dragenter", "dragover"]) {
    byID("data-dropzone").addEventListener(type, (event) => { event.preventDefault(); byID("data-dropzone").classList.add("dragging"); });
  }
  for (const type of ["dragleave", "drop"]) {
    byID("data-dropzone").addEventListener(type, (event) => { event.preventDefault(); byID("data-dropzone").classList.remove("dragging"); });
  }
  byID("data-dropzone").addEventListener("drop", (event) => {
    if (mutationPending || !event.dataTransfer?.files?.length) return;
    const transfer = new DataTransfer();
    for (const file of event.dataTransfer.files) transfer.items.add(file);
    byID("data-files").files = transfer.files;
    refreshUploadPreview().catch((error) => message("problem-upload-message", error.message));
  });
  byID("data-apply-score").addEventListener("click", () => {
    const raw = byID("data-bulk-score").value.trim();
    if (!/^(0|[1-9][0-9]*)$/.test(raw)) { message("problem-upload-message", "批量分值必须是非负整数。"); return; }
    for (const id of scoreInputs.keys()) scoreInputs.set(id, raw);
    for (const input of byID("data-score-table").querySelectorAll("input")) input.value = raw;
    validateScores(true);
  });
  byID("data-copy-first-score").addEventListener("click", () => {
    const first = scoreInputs.values().next().value;
    if (first === undefined) return;
    for (const id of scoreInputs.keys()) scoreInputs.set(id, first);
    for (const input of byID("data-score-table").querySelectorAll("input")) input.value = first;
    validateScores(true);
  });
  byID("data-upload-cancel").addEventListener("click", () => {
    if (uploadXHR) uploadXHR.abort();
  });

  function startNewProblem() {
    if (mutationPending || !mayDiscard()) return;
    ++editorEpoch;
    ++versionEpoch;
    fillEditor(null);
    switchWorkspace("new");
    editor.elements.namedItem("title").focus();
  }
  byID("problem-new").addEventListener("click", startNewProblem);
  byID("problem-nav-list").addEventListener("click", () => switchWorkspace("list"));
  byID("problem-nav-new").addEventListener("click", startNewProblem);
  byID("problem-nav-edit").addEventListener("click", () => { if (selected) switchWorkspace("edit"); });
  byID("problem-nav-data").addEventListener("click", () => { if (selected) switchWorkspace("data"); });
  byID("problem-nav-versions").addEventListener("click", () => {
    if (!selected) return;
    switchWorkspace("versions");
    loadVersions();
  });
  byID("problem-reload").addEventListener("click", () => {
    if (selected && !mutationPending && mayDiscard()) selectProblem(selected.id, true);
  });
  adminFilters.addEventListener("submit", (event) => { event.preventDefault(); loadList(1); });
  byID("admin-prev").addEventListener("click", () => loadList(Math.max(1, adminPage - 1)));
  byID("admin-next").addEventListener("click", () => loadList(adminPage + 1));
  byID("version-prev").addEventListener("click", () => loadVersions(Math.max(1, versionPage - 1)));
  byID("version-next").addEventListener("click", () => loadVersions(versionPage + 1));
  byID("admin-list-retry").addEventListener("click", () => loadList());
  byID("problem-versions-retry").addEventListener("click", () => loadVersions());
  window.addEventListener("beforeunload", (event) => {
    if (dirty || mutationPending) { event.preventDefault(); event.returnValue = ""; }
  });

  async function initializeAdmin() {
    adminAllowed = false;
    updateControls();
    state("正在验证管理权限…", true);
    byID("problem-admin").hidden = true;
    try {
      const user = await request("/api/users/me");
      if (user.global_role !== "super_admin") {
        state("当前账号没有题目管理权限。请使用超级管理员账号登录。");
        return;
      }
      adminAllowed = true;
      if (!savedEditor) fillEditor(null);
      byID("problem-state").hidden = true;
      byID("problem-admin").hidden = false;
      await loadList(1);
      const route = workspaceFromHash();
      const initialID = Number(new URLSearchParams(window.location.search).get("select")) || route.id;
      if (Number.isSafeInteger(initialID) && initialID > 0) {
        await selectProblem(initialID, true);
        if (["data", "versions"].includes(route.name)) switchWorkspace(route.name);
      }
      else if (route.name === "new") startNewProblem();
      else switchWorkspace("list");
    } catch (error) {
      state(error.status === 401 ? "请先登录超级管理员账号，再管理题目。" : error.message, false, error.status !== 401, error.status === 401);
    }
  }
  byID("problem-retry").addEventListener("click", initializeAdmin);
  initializeAdmin();
})();
