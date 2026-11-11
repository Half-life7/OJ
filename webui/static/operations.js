"use strict";
(() => {
  if (document.body.dataset.page !== "operations") return;
  const $ = (id) => document.getElementById(id);
  const node = (tag, value, className) => {
    const element = document.createElement(tag);
    element.textContent = String(value ?? "");
    if (className) element.className = className;
    return element;
  };
  let csrfToken = "";
  let usersAfter = 0;
  let auditsAfter = 0;
  let lastRequestID = "";
  let activePanel = "overview";
  let activeTestPanel = "diagnostics";
  let codeEditor = null;
  let markdownEditor = null;
  let packageSelection = null;
  let importedPackage = null;
  let csvSample = "";
  const loaded = new Set();

  class APIError extends Error {
    constructor(message, status, requestID) {
      super(message);
      this.status = status;
      this.requestID = requestID;
    }
  }
  async function api(path, method = "GET", data) {
    const headers = { Accept: "application/json" };
    if (method !== "GET") {
      if (!csrfToken) csrfToken = (await api("/api/auth/csrf")).csrf_token;
      headers["X-CSRF-Token"] = csrfToken;
      headers["Content-Type"] = "application/json";
    }
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), /\/operations\/(simulate|reset|rejudge\/)/.test(path) ? 120000 : 20000);
    try {
      const response = await fetch(path, {
        method, headers, credentials: "same-origin", cache: "no-store", signal: controller.signal,
        body: data === undefined ? undefined : JSON.stringify(data),
      });
      lastRequestID = response.headers?.get?.("X-Request-ID") || "";
      if (response.status === 403) csrfToken = "";
      let result;
      try { result = await response.json(); } catch {
        throw new APIError("服务暂时无法响应，请稍后重试。", response.status, lastRequestID);
      }
      if (!response.ok || result.code) {
        throw new APIError((result.message || "请求失败") + (lastRequestID ? `（请求 ID：${lastRequestID}）` : ""), response.status, lastRequestID);
      }
      return result.data;
    } catch (error) {
      if (error instanceof APIError) throw error;
      throw new APIError(error.name === "AbortError" ? "请求超时，请刷新状态核对结果后再重试。" : "网络连接失败，请检查网络后重试。", 0);
    } finally {
      clearTimeout(timer);
    }
  }
  async function upload(path, formData) {
    if (!csrfToken) csrfToken = (await api("/api/auth/csrf")).csrf_token;
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 120000);
    try {
      const response = await fetch(path, {
        method: "POST", headers: { Accept: "application/json", "X-CSRF-Token": csrfToken },
        credentials: "same-origin", cache: "no-store", signal: controller.signal, body: formData,
      });
      lastRequestID = response.headers?.get?.("X-Request-ID") || "";
      let result;
      try { result = await response.json(); } catch {
        throw new APIError("服务暂时无法响应，请稍后重试。", response.status, lastRequestID);
      }
      if (!response.ok || result.code) throw new APIError((result.message || "上传失败") + (lastRequestID ? `（请求 ID：${lastRequestID}）` : ""), response.status, lastRequestID);
      return result.data;
    } catch (error) {
      if (error instanceof APIError) throw error;
      throw new APIError(error.name === "AbortError" ? "上传超时，请稍后核对是否已创建。" : "网络连接失败。", 0);
    } finally {
      clearTimeout(timer);
    }
  }
  async function run(button, fn) {
    if (button) button.disabled = true;
    $("operations-message").textContent = "正在处理…";
    try {
      await fn();
      if ($("operations-message").textContent === "正在处理…") $("operations-message").textContent = "操作完成";
    } catch (error) {
      $("operations-message").textContent = error.message;
    } finally {
      if (button) button.disabled = false;
    }
  }
  function form(id, fn) {
    $(id).addEventListener("submit", (event) => {
      event.preventDefault();
      run(event.submitter || event.target.querySelector("button"), () => fn(Object.fromEntries(new FormData(event.target))));
    });
  }

  const names = {
    users: "账号", problems: "题目", contests: "比赛", submissions: "提交", pending_tasks: "待完成任务",
    pending_outbox: "待派发", dead_letters: "死信", quarantined: "隔离消息", worker_ready: "Worker 就绪",
    dispatcher_ready: "派发器就绪", runner_via_worker_ready: "Runner（经 Worker）", redis_ready: "Redis 就绪",
  };
  async function metrics() {
    const start = performance.now();
    const data = await api("/api/admin/operations/metrics");
    $("operations-metrics").replaceChildren();
    for (const [key, value] of Object.entries(data)) {
      const wrap = document.createElement("div");
      wrap.append(node("dt", names[key] || key), node("dd", typeof value === "boolean" ? (value ? "正常" : "异常") : value));
      if (typeof value === "boolean") wrap.lastElementChild.classList.add(value ? "test-pass" : "test-fail");
      $("operations-metrics").append(wrap);
    }
    return { data, elapsed: Math.round(performance.now() - start), requestID: lastRequestID };
  }
  async function users(reset = false) {
    if (reset) {
      usersAfter = 0;
      $("operations-users").replaceChildren();
    }
    const rows = await api(`/api/admin/operations/users?after=${usersAfter}&q=${encodeURIComponent($("operations-search").elements.q.value)}`);
    for (const user of rows) {
      const button = node("button", `#${user.id} ${user.username} · ${user.display_name} · ${user.student_no || "无学号"} · ${user.status}`);
      button.type = "button";
      button.className = "button secondary";
      button.addEventListener("click", () => {
        for (const id of ["operations-status", "operations-history"]) $(id).elements.id.value = user.id;
      });
      $("operations-users").append(button);
      usersAfter = user.id;
    }
    $("operations-users-more").hidden = rows.length < 100;
    loaded.add("accounts");
  }
  async function audits(more = false) {
    if (!more) {
      auditsAfter = 0;
      $("operations-audits").replaceChildren();
    }
    const rows = await api(`/api/admin/operations/audits?after=${auditsAfter}`);
    for (const audit of rows) {
      $("operations-audits").append(node("p", `#${audit.id} ${audit.created_at} · ${audit.action} · ${audit.object_type} #${audit.object_id} · 操作人 ${audit.actor_user_id || "系统"} · ${JSON.stringify(audit.details)}`));
      auditsAfter = audit.id;
    }
    $("operations-audits-more").disabled = rows.length < 100;
    loaded.add("audit");
  }
  async function queue() {
    const [dead, isolated] = await Promise.all([api("/api/admin/operations/dead-letters"), api("/api/admin/operations/quarantine")]);
    $("operations-dead").replaceChildren();
    for (const item of dead) $("operations-dead").append(node("p", `死信 #${item.id} · 提交 #${item.submission_id} · generation ${item.generation} · ${item.reason}`));
    $("operations-quarantine").replaceChildren();
    for (const item of isolated) $("operations-quarantine").append(node("p", `隔离 ${item.stream_id} · ${item.payload_digest.slice(0, 12)} · ${item.reason}`));
    loaded.add("queues");
  }

  function testRow(label, status, detail, elapsed = null, requestID = lastRequestID) {
    const row = node("div", undefined, "operations-test-row");
    row.append(node("strong", label), node("span", status ? "通过" : "失败", status ? "test-pass" : "test-fail"));
    row.append(node("p", detail));
    if (elapsed !== null) row.append(node("small", `耗时 ${elapsed} ms${requestID ? ` · 请求 ID ${requestID}` : ""}`));
    $("operations-test-results").append(row);
    return row;
  }
  async function oneClickTest(button) {
    run(button, async () => {
      initializeEditors();
      $("operations-test-results").replaceChildren();
      let metricResult;
      try {
        metricResult = await metrics();
        testRow("管理 API 与依赖指标", true, "MySQL 指标、Redis、Worker 和 dispatcher 探测已返回。", metricResult.elapsed, metricResult.requestID);
      } catch (error) {
        testRow("管理 API 与依赖指标", false, error.message);
        throw error;
      }
      const booleans = Object.entries(metricResult.data).filter(([, value]) => typeof value === "boolean");
      testRow("服务就绪汇总", booleans.every(([, value]) => value), booleans.map(([key, value]) => `${names[key] || key}: ${value ? "正常" : "异常"}`).join("；"));
      testRow("代码编辑器", !!codeEditor, codeEditor ? "CodeMirror 已挂载，支持高亮、Tab 和 Ctrl/Cmd+/。" : "已使用 textarea 回退。", null, "");
      testRow("Markdown 编辑器", !!markdownEditor, markdownEditor ? "Markdown 高亮与快捷工具栏已挂载。" : "已使用 textarea 回退。", null, "");
      try {
        const preview = await upload("/api/admin/problems/package-preview", sampleFormData());
        testRow("内置题包解析", true, `${preview.preview.title} · ${preview.preview.cases.length} 个测试点 · ${preview.preview.total_score} 分`, null, lastRequestID);
      } catch (error) {
        testRow("内置题包解析", false, error.message);
      }
      try {
        renderCSVAnalysis();
        testRow("CSV 表头与样例", true, "中英文表头已映射并生成当前页面测试样例。", null, "");
      } catch (error) {
        testRow("CSV 表头与样例", false, error.message, null, "");
      }
      $("operations-message").textContent = "一键测试完成；请按上方实际结果核对。";
    });
  }

  function initializeEditors() {
    if (codeEditor || !window.BiueEditor) return;
    codeEditor = window.BiueEditor.mount($("operations-demo-code"), $("operations-demo-language").value, { ariaLabel: "管理测试代码编辑器" });
    markdownEditor = window.BiueEditor.mount($("operations-demo-markdown"), "markdown", {
      ariaLabel: "管理测试 Markdown 编辑器",
      onPreview: async (source) => {
        $("operations-markdown-preview").hidden = false;
        $("operations-markdown-preview-html").textContent = "正在生成安全预览…";
        try {
          const result = await api("/api/admin/problems/markdown-preview", "POST", { markdown: source });
          $("operations-markdown-preview-html").innerHTML = result.html;
        } catch (error) {
          $("operations-markdown-preview-html").textContent = error.message;
        }
      },
    });
  }

  const csvAliases = {
    username: "username", user: "username", account: "username", "账号": "username", "用户名": "username", "账号id": "username",
    display_name: "display_name", name: "display_name", "姓名": "display_name", "显示名称": "display_name",
    student_no: "student_no", student: "student_no", "学号": "student_no",
    organization: "organization", school: "organization", "学校": "organization", "组织": "organization", "学校或组织": "organization",
    team: "team", team_name: "team", "队伍": "team", "队名": "team",
    participant_type: "participant_type", type: "participant_type", "类型": "participant_type", "选手类型": "participant_type",
  };
  function analyzeCSV(raw) {
    const lines = raw.replace(/^\ufeff/, "").split(/\r?\n/).filter((line) => line.length);
    if (!lines.length) throw new APIError("请粘贴或选择 CSV。", 400);
    const headers = lines[0].split(",").map((value) => value.trim());
    const mapped = headers.map((source) => ({ source, field: csvAliases[source.toLowerCase()] || "" }));
    const unknown = mapped.filter((item) => !item.field);
    const seen = new Set();
    const duplicate = mapped.filter((item) => item.field && (seen.has(item.field) || !seen.add(item.field)));
    if (unknown.length || duplicate.length || !mapped.some((item) => item.field === "username")) throw new APIError("CSV 表头无效：需要 username/账号，未知列或重复别名会被拒绝。", 400);
    const normalizedHeader = mapped.map((item) => item.field).join(",");
    const sample = [
      normalizedHeader,
      mapped.map((item) => item.field === "username" ? "demo_" + Date.now() : item.field === "participant_type" ? "official" : item.field === "team" ? "Demo Team" : item.field === "display_name" ? "演示选手" : item.field === "student_no" ? "TEST0001" : item.field === "organization" ? "Biue OJ" : "").join(","),
    ].join("\n");
    return { mapped, sample };
  }
  function renderCSVAnalysis() {
    const result = analyzeCSV($("operations-csv-input").value);
    csvSample = result.sample;
    $("operations-csv-result").textContent = `列映射：${result.mapped.map((item) => `${item.source} → ${item.field}`).join("；")}\n\n${result.sample}`;
  }
  function downloadText(filename, content) {
    const link = document.createElement("a");
    link.href = URL.createObjectURL(new Blob([content], { type: "text/csv;charset=utf-8" }));
    link.download = filename;
    link.click();
    setTimeout(() => URL.revokeObjectURL(link.href), 1000);
  }

  function sampleFormData() {
    const data = new FormData();
    data.append("sample", "1");
    return data;
  }
  function operationKey(prefix) {
    if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") return `${prefix}-${crypto.randomUUID()}`;
    return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 12)}`;
  }
  function renderScopeRejudge(result) {
    const target = $("operations-scope-rejudge-result");
    target.replaceChildren();
    target.append(node("p", `已排队 ${result.queued || 0} 条，跳过 ${result.skipped || 0} 条，共 ${result.batches || 0} 个批次。`, result.complete === false ? "test-fail" : "test-pass"));
    for (const warning of result.warnings || []) target.append(node("p", warning, "test-fail"));
    for (const task of (result.tasks || []).slice(0, 20)) {
      const link = node("a", `提交 #${task.submission_id} · generation ${task.generation}`);
      link.href = `/submissions/${task.submission_id}`;
      const paragraph = node("p");
      paragraph.append(link);
      target.append(paragraph);
    }
  }
  function packageFormData() {
    if (packageSelection === "builtin") return sampleFormData();
    if (!(packageSelection instanceof File)) throw new APIError("请选择题包 ZIP，或使用内置样例。", 400);
    if (packageSelection.size <= 0 || packageSelection.size > 32 * 1024 * 1024) throw new APIError("题包 ZIP 必须不超过 32 MiB。", 400);
    const data = new FormData();
    data.append("archive", packageSelection);
    return data;
  }
  function renderPackagePreview(result) {
    const preview = result.preview;
    const target = $("operations-package-result");
    target.replaceChildren(node("h3", preview.title), node("p", `来源：${result.source === "builtin" ? "内置 A+B 样例" : "所选 ZIP"} · 摘要 ${preview.digest.slice(0, 16)}…`));
    target.append(node("p", `${preview.cases.length} 个测试点 · 总分 ${preview.total_score}`));
    const table = node("table", undefined, "problem-case-table");
    const head = node("thead");
    const row = node("tr");
    for (const label of ["测试点", "分值", "输入字节", "输出字节"]) row.append(node("th", label));
    head.append(row);
    const body = node("tbody");
    for (const item of preview.cases) {
      const line = node("tr");
      for (const value of [item.id, item.score, item.input_bytes, item.output_bytes]) line.append(node("td", value));
      body.append(line);
    }
    table.append(head, body);
    target.append(table);
    for (const warning of preview.warnings || []) target.append(node("p", warning, "field-help"));
    $("operations-package-import").disabled = false;
    importedPackage = null;
  }

  function switchPanel(name) {
    if (!document.querySelector(`[data-operations-panel='${name}']`)) name = "overview";
    activePanel = name;
    for (const button of document.querySelectorAll("[data-operations-panel]")) button.classList.toggle("active", button.dataset.operationsPanel === name);
    for (const panel of document.querySelectorAll("[data-operations-panel-content]")) panel.hidden = panel.dataset.operationsPanelContent !== name;
    window.history.replaceState(null, "", `#${name}`);
    if (name === "test") {
      if (activeTestPanel === "editors") initializeEditors();
    }
    if (name === "accounts" && !loaded.has("accounts")) run($("operations-search").querySelector("button"), () => users(true));
    if (name === "queues" && !loaded.has("queues")) run($("operations-queue-refresh"), queue);
    if (name === "audit" && !loaded.has("audit")) run($("operations-audits-more"), () => audits(false));
  }
  function switchTestPanel(name) {
    if (!document.querySelector(`[data-operations-test-panel='${name}']`)) name = "diagnostics";
    activeTestPanel = name;
    for (const button of document.querySelectorAll("[data-operations-test-panel]")) {
      const selected = button.dataset.operationsTestPanel === name;
      button.classList.toggle("active", selected);
      button.setAttribute("aria-selected", String(selected));
      button.tabIndex = selected ? 0 : -1;
    }
    for (const panel of document.querySelectorAll("[data-operations-test-content]")) panel.hidden = panel.dataset.operationsTestContent !== name;
    if (name === "editors") initializeEditors();
  }

  form("operations-search", () => users(true));
  form("operations-status", async (data) => {
    await api(`/api/admin/operations/users/${data.id}/status`, "POST", { status: data.status, reason: data.reason });
    await users(true);
  });
  form("operations-history", async (data) => {
    const rows = await api(`/api/admin/operations/users/${data.id}/submissions`);
    $("operations-history-list").replaceChildren();
    for (const submission of rows) {
      const paragraph = document.createElement("p");
      const link = node("a", `#${submission.id} ${submission.kind} ${submission.status}`);
      link.href = `/submissions/${submission.id}`;
      paragraph.append(link);
      $("operations-history-list").append(paragraph);
    }
  });
  form("operations-retry", async (data) => {
    await api(`/api/admin/operations/dead-letters/${data.id}/retry`, "POST", { generation: Number(data.generation) });
    await queue();
  });
  if (typeof crypto.randomUUID === "function") $("operations-rejudge").elements.key.value = crypto.randomUUID();
  form("operations-rejudge", async (data) => {
    const items = data.items.split(",").map((value) => {
      const [id, generation] = value.trim().split(":").map(Number);
      if (!Number.isSafeInteger(id) || id < 1 || !Number.isSafeInteger(generation) || generation < 1) throw new APIError("请按 编号:轮次 填写", 400);
      return { id, generation };
    });
    const tasks = await api("/api/admin/operations/rejudge", "POST", { key: data.key, reason: data.reason, items });
    $("operations-rejudge-result").replaceChildren();
    for (const task of tasks) {
      const paragraph = document.createElement("p");
      const link = node("a", `提交 #${task.submission_id} 新轮次 ${task.generation}`);
      link.href = `/submissions/${task.submission_id}`;
      paragraph.append(link);
      $("operations-rejudge-result").append(paragraph);
    }
  });
  $("operations-simulate").onclick = (event) => run(event.target, async () => {
    const result = await api("/api/admin/operations/simulate", "POST", {});
    const target = $("operations-simulation-result");
    target.replaceChildren(node("h3", "模拟数据已创建"), node("p", `${result.users} 个账号 · ${result.practice_submissions} 条题库提交 · ${result.contest_submissions} 条比赛提交。`), node("p", result.simulation_label, "test-pass"));
    const contestLink = node("a", `打开模拟比赛 #${result.contest_id}`);
    contestLink.href = `/contests/${result.contest_id}`;
    target.append(contestLink);
    for (const warning of result.warnings || []) target.append(node("p", warning, "test-fail"));
    const credentials = node("textarea", undefined, "operations-credentials");
    credentials.readOnly = true;
    credentials.rows = 8;
    credentials.value = `账号范围：${result.username_prefix}01 ～ ${result.username_prefix}${String(result.users).padStart(2, "0")}\n统一初始密码：${result.initial_password}\n\n${result.usernames.join("\n")}`;
    target.append(node("p", "以下凭据只在本次响应中显示，请立即保存；服务端只保存密码哈希。", "field-help"), credentials);
    $("operations-simulation-message").textContent = result.complete
      ? "模拟完成。练习榜、比赛榜和模拟比赛已可直接验收。"
      : "模拟数据已写入，但存在未完成步骤，请按提示核对。";
    $("operations-simulation-message").hidden = false;
  });
  for (const [id, field] of [["operations-rejudge-contest", "contest_id"], ["operations-rejudge-problem", "problem_id"]]) {
    $(id).addEventListener("submit", (event) => {
      event.preventDefault();
      run(event.submitter || event.target.querySelector("button"), async () => {
        const data = Object.fromEntries(new FormData(event.target));
        const scoped = {
          key: operationKey(id.endsWith("contest") ? "contest" : "problem"), reason: data.reason,
        };
        scoped[field] = Number(data[field]);
        const result = await api(`/api/admin/operations/rejudge/${id.endsWith("contest") ? "contest" : "problem"}`, "POST", scoped);
        renderScopeRejudge(result);
        $("operations-message").textContent = result.complete === false ? "范围重判部分完成；请查看批次结果。" : "范围重判已排队。";
      });
    });
  }
  $("operations-reset").addEventListener("submit", (event) => {
    event.preventDefault();
    run(event.submitter || event.target.querySelector("button"), async () => {
      const data = Object.fromEntries(new FormData(event.target));
      if (data.confirmation !== "初始化全部业务数据") {
        throw new APIError("确认词必须完全等于“初始化全部业务数据”。", 400);
      }
      if (!window.confirm("这将永久删除当前管理员以外的业务数据。确认已完成备份并继续？")) return;
      const result = await api("/api/admin/operations/reset", "POST", data);
      const target = $("operations-reset-result");
      target.replaceChildren(
        node("h3", result.complete ? "初始化完成" : "数据库已初始化，存在未完成清理"),
        node("p", `Redis 键 ${result.redis_keys} 个 · 题目数据版本 ${result.data_versions} 个。`),
      );
      for (const warning of result.warnings || []) target.append(node("p", warning, "test-fail"));
      const deleted = Object.entries(result.deleted || {}).filter(([, count]) => count > 0);
      if (deleted.length) target.append(node("p", deleted.map(([table, count]) => `${table}: ${count}`).join("；"), "field-help"));
      event.target.reset();
      $("operations-message").textContent = result.complete ? "全部业务数据已初始化。" : "数据库已初始化，但部分缓存或文件清理失败；请查看结果。";
      loaded.clear();
    });
  });

  for (const button of document.querySelectorAll("[data-operations-panel]")) button.addEventListener("click", () => switchPanel(button.dataset.operationsPanel));
  const testPanelButtons = [...document.querySelectorAll("[data-operations-test-panel]")];
  for (const button of testPanelButtons) {
    button.addEventListener("click", () => switchTestPanel(button.dataset.operationsTestPanel));
    button.addEventListener("keydown", (event) => {
      const current = testPanelButtons.indexOf(button);
      let next = null;
      if (event.key === "ArrowRight") next = (current + 1) % testPanelButtons.length;
      if (event.key === "ArrowLeft") next = (current - 1 + testPanelButtons.length) % testPanelButtons.length;
      if (event.key === "Home") next = 0;
      if (event.key === "End") next = testPanelButtons.length - 1;
      if (next === null) return;
      event.preventDefault();
      const target = testPanelButtons[next];
      switchTestPanel(target.dataset.operationsTestPanel);
      target.focus();
    });
  }
  $("operations-refresh").onclick = (event) => run(event.target, metrics);
  $("operations-users-more").onclick = (event) => run(event.target, () => users(false));
  $("operations-audits-more").onclick = (event) => run(event.target, () => audits(true));
  $("operations-queue-refresh").onclick = (event) => run(event.target, queue);
  $("operations-test-run").onclick = (event) => oneClickTest(event.target);
  $("operations-test-clear").onclick = () => { $("operations-test-results").replaceChildren(); };
  $("operations-demo-language").onchange = () => codeEditor?.setLanguage($("operations-demo-language").value);
  $("operations-markdown-preview-close").onclick = () => { $("operations-markdown-preview").hidden = true; };
  $("operations-csv-file").onchange = async (event) => {
    const file = event.target.files[0];
    if (!file) return;
    if (file.size > 256 * 1024) {
      $("operations-message").textContent = "CSV 测试文件最多 256 KiB。";
      event.target.value = "";
      return;
    }
    $("operations-csv-input").value = await file.text();
  };
  $("operations-csv-analyze").onclick = (event) => run(event.target, async () => { renderCSVAnalysis(); $("operations-message").textContent = "CSV 表头已识别并生成测试样例。"; });
  $("operations-csv-copy").onclick = async () => {
    try {
      if (!csvSample) renderCSVAnalysis();
      await navigator.clipboard.writeText(csvSample);
      $("operations-message").textContent = "测试样例已复制。";
    } catch (error) {
      $("operations-message").textContent = error.message || "浏览器不允许复制，请手动选择结果。";
    }
  };
  $("operations-csv-download").onclick = () => {
    if (!csvSample) renderCSVAnalysis();
    downloadText("roster-test-sample.csv", csvSample);
  };
  $("operations-package-builtin").onclick = (event) => run(event.target, async () => {
    packageSelection = "builtin";
    renderPackagePreview(await upload("/api/admin/problems/package-preview", sampleFormData()));
    $("operations-message").textContent = "内置题包预览通过，尚未创建题目。";
  });
  $("operations-package-file").onchange = (event) => {
    packageSelection = event.target.files[0] || null;
    importedPackage = null;
    $("operations-package-import").disabled = true;
    $("operations-package-result").replaceChildren();
  };
  $("operations-package-preview").onclick = (event) => run(event.target, async () => {
    renderPackagePreview(await upload("/api/admin/problems/package-preview", packageFormData()));
    $("operations-message").textContent = "题包预览通过，尚未创建题目。";
  });
  $("operations-package-import").onclick = (event) => run(event.target, async () => {
    if (importedPackage) {
      window.location.assign(`/admin/problems?select=${importedPackage.id}`);
      return;
    }
    const problem = await upload("/api/admin/problems/package-import", packageFormData());
    importedPackage = problem;
    $("operations-package-result").append(node("p", `已创建题目 #${problem.id}，题面版本 v1，数据版本 v1。`, "test-pass"));
    $("operations-package-import").textContent = `打开题目 #${problem.id}`;
    $("operations-message").textContent = "题包已导入。重复点击只打开本次已创建题目。";
  });
  window.addEventListener("hashchange", () => switchPanel(location.hash.slice(1) || "overview"));

  metrics().then(() => {
    loaded.add("overview");
    $("operations-content").hidden = false;
    $("operations-message").textContent = "管理概览已加载。";
    switchPanel(location.hash.slice(1) || "overview");
  }).catch((error) => {
    $("operations-message").textContent = error.message;
  });
})();
