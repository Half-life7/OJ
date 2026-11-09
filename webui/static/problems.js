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
    constructor(message, status) {
      super(message);
      this.status = status;
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
        const message = response.status === 409 ? "题目已被其他操作更新。请重新加载此题，再确认你的修改。" : result.message || "操作未完成，请稍后重试。";
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
        for (const [field, suffix] of [["description", "description"], ["input_format", "input-format"], ["output_format", "output-format"], ["sample_input", "sample-input"], ["sample_output", "sample-output"]]) {
          byID("problem-" + suffix).textContent = problem[field] || "";
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
  const editorFields = ["title", "description", "input_format", "output_format", "sample_input", "sample_output", "time_limit_ms", "memory_limit_mb", "difficulty", "status", "visibility"];
  let selected = null;
  let dirty = false;
  let mutationPending = false;
  let adminPage = 1;
  let versionPage = 1;
  let listEpoch = 0;
  let editorEpoch = 0;
  let versionEpoch = 0;

  function updateControls() {
    byID("problem-editor-fields").disabled = mutationPending;
    byID("problem-upload-fields").disabled = mutationPending || !selected || dirty;
    byID("problem-new").disabled = mutationPending;
    byID("problem-reload").disabled = mutationPending;
    for (const button of byID("admin-problem-list").querySelectorAll("button")) button.disabled = mutationPending;
  }

  function adminError(error, target) {
    message(target, error.message);
    if (error.status === 401) {
      byID("problem-admin").hidden = true;
      state("登录会话已失效，请重新登录后再管理题目。", false, false, true);
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
    byID("problem-editor-mode").textContent = problem ? "EDIT PROBLEM #" + problem.id : "NEW PROBLEM";
    byID("problem-editor-heading").textContent = problem ? "编辑题目" : "创建题目";
    byID("problem-editor-version").textContent = problem ? "修订 " + problem.revision + " · 题面版本 " + problem.statement_version + " · 更新于 " + date(problem.updated_at) : "新题默认为隐藏草稿。";
    byID("problem-save").textContent = problem ? "保存题面修改" : "创建题目";
    byID("problem-reload").hidden = !problem;
    byID("problem-versions-panel").hidden = !problem;
    byID("problem-current-data").textContent = problem && problem.data_version ? "当前使用数据版本 v" + problem.data_version : "尚未上传测试数据。";
    byID("problem-version-detail").hidden = true;
    byID("problem-version-list").replaceChildren();
    message("problem-editor-message", "");
    message("problem-upload-message", "");
    message("problem-versions-message", "");
    byID("problem-versions-retry").hidden = true;
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

  editor.addEventListener("input", () => { dirty = true; updateControls(); });
  editor.addEventListener("change", () => { dirty = true; updateControls(); });
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
      message("problem-editor-message", id ? "题面已保存。" : "题目已创建，可以上传测试数据。", true);
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

  upload.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (mutationPending || !selected || dirty) return;
    const formData = new FormData(upload);
    const archive = formData.get("archive");
    try {
      if (!(archive instanceof File) || archive.size === 0 || archive.size > 32 * 1024 * 1024) throw new APIError("请选择非空且不超过 32 MiB 的 ZIP 文件。", 400);
      const rawScores = formData.get("scores");
      if (new TextEncoder().encode(rawScores).length > 65536) throw new APIError("分值配置最多 64 KiB。", 400);
      let scores;
      try { scores = JSON.parse(rawScores); } catch { throw new APIError("测试点分值必须是有效的 JSON 对象。", 400); }
      if (scores === null || Array.isArray(scores) || typeof scores !== "object" || Object.keys(scores).length === 0 || Object.keys(scores).length > 512 || Object.keys(scores).some((id) => !/^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(id)) || Object.values(scores).some((score) => !Number.isInteger(score) || score < 0)) throw new APIError("分值须包含 1～512 个测试点，键为测试点 ID，值为非负整数。", 400);
      // Preserve the original numeric bytes rather than round-trip int64 scores
      // through JavaScript Number. JSON syntax and decoded keys were checked above.
      const literals = [...rawScores.matchAll(/:\s*(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/g)].map((match) => match[1]);
      if (literals.length !== Object.keys(scores).length || literals.some((value) => !/^-?\d+$/.test(value))) throw new APIError("分值须使用整数写法，测试点 ID 不能重复。", 400);
      const exactScores = literals.map((value) => BigInt(value));
      const total = exactScores.reduce((sum, value) => sum + value, 0n);
      if (exactScores.some((value) => value < 0n || value > 9223372036854775807n) || total <= 0n || total > 9223372036854775807n) throw new APIError("每点分值和总分须在 64 位整数范围内，且总分大于零。", 400);
    } catch (error) {
      message("problem-upload-message", error.message);
      return;
    }
    const id = selected.id;
    formData.set("revision", String(selected.revision));
    mutationPending = true;
    updateControls();
    message("problem-upload-message", "正在校验并上传测试数据…", true);
    try {
      const version = await write("/api/admin/problems/" + encodeURIComponent(id) + "/data-versions", "POST", formData);
      selected = { ...selected, revision: version.revision, data_version: version.data_version };
      byID("problem-editor-version").textContent = "修订 " + selected.revision + " · 题面版本 " + selected.statement_version;
      byID("problem-current-data").textContent = "当前使用数据版本 v" + version.data_version;
      message("problem-upload-message", "数据版本 v" + version.data_version + " 已发布，共 " + number(version.case_count) + " 个测试点。", true);
      upload.reset();
      versionPage = 1;
      loadVersions();
    } catch (error) {
      adminError(error, "problem-upload-message");
    } finally {
      mutationPending = false;
      updateControls();
    }
  });

  byID("problem-new").addEventListener("click", () => {
    if (mutationPending || !mayDiscard()) return;
    ++editorEpoch;
    ++versionEpoch;
    fillEditor(null);
    editor.elements.namedItem("title").focus();
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
    state("正在验证管理权限…", true);
    byID("problem-admin").hidden = true;
    try {
      const user = await request("/api/users/me");
      if (user.global_role !== "super_admin") {
        state("当前账号没有题目管理权限。请使用超级管理员账号登录。");
        return;
      }
      fillEditor(null);
      byID("problem-state").hidden = true;
      byID("problem-admin").hidden = false;
      await loadList(1);
    } catch (error) {
      state(error.status === 401 ? "请先登录超级管理员账号，再管理题目。" : error.message, false, error.status !== 401, error.status === 401);
    }
  }
  byID("problem-retry").addEventListener("click", initializeAdmin);
  initializeAdmin();
})();
