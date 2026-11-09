"use strict";

(() => {
  const page = document.body.dataset.page;
  if (!["problem", "submissions", "submission"].includes(page)) return;
  const byID = (id) => document.getElementById(id);
  const terminal = new Set(["Accepted", "WrongAnswer", "TimeLimitExceeded", "MemoryLimitExceeded", "RuntimeError", "CompileError", "SystemError", "Cancelled"]);
  const statusNames = { Pending: "待处理", Queued: "等待评测", Compiling: "正在编译", Running: "正在运行", Accepted: "通过", WrongAnswer: "答案错误", TimeLimitExceeded: "超过时间限制", MemoryLimitExceeded: "超过内存限制", RuntimeError: "运行错误", CompileError: "编译错误", SystemError: "评测服务错误", Cancelled: "已取消" };
  const kindName = (kind) => kind === "Run" ? "自测运行" : "正式提交";
  const controllers = new Set();
  const pollers = new Set();
  let disposed = false;
  let csrfToken = "";

  class APIError extends Error {
    constructor(message, status) { super(message); this.status = status; }
  }

  async function request(path, method = "GET", body, signal) {
    const controller = new AbortController();
    const abort = () => controller.abort();
    controllers.add(controller);
    if (signal?.aborted) controller.abort();
    else signal?.addEventListener("abort", abort, { once: true });
    const timeout = window.setTimeout(abort, 15000);
    const headers = { Accept: "application/json" };
    if (body !== undefined) headers["Content-Type"] = "application/json";
    if (method !== "GET") headers["X-CSRF-Token"] = csrfToken;
    try {
      const response = await fetch(path, { method, headers, credentials: "same-origin", cache: "no-store", signal: controller.signal, body: body === undefined ? undefined : JSON.stringify(body) });
      let result;
      try { result = await response.json(); } catch { throw new APIError("服务暂时无法响应，请稍后重试。", response.status); }
      if (!response.ok || result.code !== 0) {
        if (response.status === 403) csrfToken = "";
        throw new APIError(result.message || "操作未完成，请稍后重试。", response.status);
      }
      return result.data;
    } catch (error) {
      if (error instanceof APIError) throw error;
      throw new APIError(error.name === "AbortError" ? "请求超时，请重新读取状态后再重试。" : "网络连接失败，请检查网络后重试。", 0);
    } finally {
      window.clearTimeout(timeout);
      controllers.delete(controller);
      signal?.removeEventListener("abort", abort);
    }
  }

  async function write(path, body) {
    if (!csrfToken) {
      const response = await request("/api/auth/csrf");
      csrfToken = response && response.csrf_token;
      if (!csrfToken) throw new APIError("无法验证当前页面，请刷新后重试。", 403);
    }
    return request(path, "POST", body);
  }

  function node(tag, text, className) {
    const element = document.createElement(tag);
    if (text !== undefined && text !== null) element.textContent = String(text);
    if (className) element.className = className;
    return element;
  }

  function textMessage(id, text, success = false) {
    const element = byID(id);
    element.textContent = text;
    element.classList.toggle("success", success);
    element.hidden = !text;
  }

  function state(text, loading = false, retry = false, login = false) {
    byID("submission-state").hidden = false;
    byID("submission-state").setAttribute("aria-busy", String(loading));
    byID("submission-state").querySelector(".loading-dot").hidden = !loading;
    byID("submission-state-text").textContent = text;
    byID("submission-retry").hidden = !retry;
    byID("submission-login").hidden = !login;
  }

  function number(value) { return typeof value === "number" && Number.isFinite(value) ? value.toLocaleString("zh-CN") : "—"; }
  function date(value) { const parsed = new Date(value); return Number.isNaN(parsed.valueOf()) ? "—" : parsed.toLocaleString("zh-CN"); }

  function statusBadge(value) {
    const className = value === "Accepted" ? "submission-accepted" : terminal.has(value) ? "submission-failed" : "submission-pending";
    return node("span", statusNames[value] || value, "problem-badge " + className);
  }

  function summary(container, submission) {
    container.replaceChildren(node("span", kindName(submission.kind), "problem-badge"), statusBadge(submission.status));
    if (terminal.has(submission.status)) {
      if (submission.kind === "Submit") container.append(node("span", "得分 " + number(submission.score), "problem-badge"));
      container.append(node("span", number(submission.execution_time_ms) + " ms", "problem-badge"), node("span", number(submission.memory_usage_kb) + " KiB", "problem-badge"));
    }
  }

  function poller(load, render, ongoing, errorHandler, signature) {
    let timer;
    let controller;
    let sequence = 0;
    let failures = 0;
    let delay = 2000;
    let lastSignature = "";
    function stop() {
      ++sequence;
      window.clearTimeout(timer);
      if (controller) controller.abort();
      controller = null;
    }
    function schedule(current) {
      if (!disposed && current === sequence) timer = window.setTimeout(() => tick(current), delay);
    }
    function received(data, current) {
      if (disposed || current !== sequence) return;
      failures = 0;
      const nextSignature = signature(data);
      delay = nextSignature === lastSignature ? Math.min(10000, Math.round(delay * 1.5)) : 2000;
      lastSignature = nextSignature;
      render(data);
      if (ongoing(data)) schedule(current);
    }
    async function tick(current) {
      controller = new AbortController();
      try {
        const data = await load(controller.signal);
        received(data, current);
      } catch (error) {
        if (disposed || current !== sequence) return;
        ++failures;
        const retrying = ![400, 401, 403, 404].includes(error.status) && failures < 3;
        errorHandler(error, retrying);
        if (retrying) { delay = Math.min(30000, delay * 2); schedule(current); }
      }
    }
    const api = {
      stop,
      start(initial) {
        stop(); failures = 0; delay = 2000; lastSignature = "";
        if (initial === undefined) tick(sequence);
        else received(initial, sequence);
      },
    };
    pollers.add(api);
    return api;
  }

  window.addEventListener("pagehide", () => {
    disposed = true;
    for (const item of pollers) item.stop();
    for (const controller of controllers) controller.abort();
  });
  // A page restored from the back/forward cache must recheck session state
  // and restart its disposed requests and editor with a fresh document.
  window.addEventListener("pageshow", (event) => {
    if (event.persisted) window.location.reload();
  });

  if (page === "problem") {
    const problemID = window.location.pathname.split("/")[2];
    let problem;
    let user;
    let editor;
    let busy = false;
    let activeSubmission;

    function controls() {
      const allowed = user?.account_type === "permanent";
      byID("submission-fields").disabled = busy || !allowed;
      byID("submit-button").disabled = busy || !allowed || !problem?.data_version;
      byID("run-stdin").disabled = byID("run-use-sample").checked;
      editor?.setDisabled(busy || !allowed);
    }

    function renderResult(submission) {
      activeSubmission = submission.id;
      byID("composer-result").hidden = false;
      byID("composer-result-link").href = "/submissions/" + encodeURIComponent(submission.id);
      summary(byID("composer-result-summary"), submission);
      byID("composer-poll-message").textContent = terminal.has(submission.status) ? "本次处理已结束。" : "状态会自动更新，也可在提交记录中稍后查看。";
      byID("composer-poll-retry").hidden = true;
      const isRun = submission.kind === "Run";
      byID("composer-run-output").hidden = !isRun;
      byID("composer-output").textContent = isRun ? submission.output || "" : "";
      const diagnostic = isRun || submission.status === "CompileError" ? submission.error_message || "" : "";
      byID("composer-error-box").hidden = !diagnostic;
      byID("composer-error").textContent = diagnostic;
    }

    const resultPoll = poller(
      (signal) => request("/api/submissions/" + encodeURIComponent(activeSubmission), "GET", undefined, signal), renderResult,
      (submission) => !terminal.has(submission.status),
      (error, retrying) => {
        byID("composer-poll-message").textContent = error.status === 401 ? "会话已失效，请登录后到提交记录查看。" : error.message + (retrying ? " 正在等待重新读取。" : "");
        byID("composer-poll-retry").hidden = retrying || error.status === 401 || error.status === 404;
        if (error.status === 401) { user = null; controls(); byID("composer-login").hidden = false; byID("composer-result").hidden = true; }
      }, (submission) => submission.status + ":" + submission.generation,
    );

    async function initialize() {
      byID("submission-composer").hidden = true;
      byID("composer-retry").hidden = true;
      try {
        problem = await request("/api/problems/" + encodeURIComponent(problemID));
        if (disposed) return;
        byID("submission-composer").hidden = false;
        byID("composer-state").textContent = "正在读取提交设置…";
        const [languages, currentUser] = await Promise.all([
          request("/api/languages"),
          request("/api/users/me").catch((error) => { if (error.status === 401) return null; throw error; }),
        ]);
        if (disposed) return;
        if (!Array.isArray(languages) || languages.length === 0) throw new APIError("暂时无法读取可用语言，请稍后重试。", 503);
        const select = byID("submission-language");
        const selectedLanguage = select.value;
        select.replaceChildren(...languages.map((language) => { const option = node("option", language.name || language.id); option.value = language.id; return option; }));
        select.value = languages.some((language) => language.id === selectedLanguage) ? selectedLanguage : languages[0].id;
        user = currentUser;
        if (!editor && typeof window.BiueEditor?.mount === "function") editor = window.BiueEditor.mount(byID("source-code"), select.value);
        byID("composer-login").hidden = !!user;
        byID("composer-state").textContent = !user ? "登录永久账号后，即可运行自测或正式提交代码。" : user.account_type !== "permanent" ? "当前账号仅用于比赛，日常练习请使用永久账号。" : problem.data_version ? "运行用于自测，正式提交将使用题目的测试数据。" : "本题尚未配置正式测试数据，可以先运行自测。";
        controls();
      } catch (error) {
        if (disposed) return;
        user = null;
        controls();
        byID("submission-composer").hidden = error.status === 404;
        byID("composer-state").textContent = error.message;
        byID("composer-retry").hidden = error.status === 404;
      }
    }

    async function create(kind) {
      if (busy || user?.account_type !== "permanent") return;
      textMessage("submission-create-message", "");
      const source = editor ? editor.getValue() : byID("source-code").value;
      if (!source.trim() || /\p{Surrogate}/u.test(source) || new TextEncoder().encode(source).length > 65536) {
        textMessage("submission-create-message", "源代码须为非空有效文本，且不超过 64 KiB。");
        return;
      }
      const id = Number(problemID);
      if (!Number.isSafeInteger(id) || id < 1) { textMessage("submission-create-message", "无法读取题目信息，请刷新后重试。"); return; }
      const input = { problem_id: id, language: byID("submission-language").value, source_code: source };
      if (kind === "Run" && !byID("run-use-sample").checked) {
        input.stdin = byID("run-stdin").value;
        if (/\p{Surrogate}/u.test(input.stdin) || new TextEncoder().encode(input.stdin).length > 65536) { textMessage("submission-create-message", "自测输入须为有效文本，且不超过 64 KiB。"); return; }
      }
      busy = true;
      controls();
      try {
        const submission = await write(kind === "Run" ? "/api/runs" : "/api/submissions", input);
        if (disposed) return;
        resultPoll.start(submission);
        textMessage("submission-create-message", kind === "Run" ? "自测已创建。" : "正式提交已创建。", true);
      } catch (error) {
        if (disposed) return;
        textMessage("submission-create-message", error.message);
        if (error.status === 401) { user = null; byID("composer-login").hidden = false; byID("composer-state").textContent = "登录会话已失效，请重新登录。"; }
      } finally {
        busy = false;
        if (!disposed) controls();
      }
    }

    byID("submission-form").addEventListener("submit", (event) => { event.preventDefault(); create("Submit"); });
    byID("run-button").addEventListener("click", () => create("Run"));
    byID("run-use-sample").addEventListener("change", controls);
    byID("submission-language").addEventListener("change", () => editor?.setLanguage(byID("submission-language").value));
    byID("composer-retry").addEventListener("click", initialize);
    byID("problem-retry").addEventListener("click", initialize);
    byID("composer-poll-retry").addEventListener("click", () => resultPoll.start());
    window.addEventListener("pagehide", () => editor?.destroy());
    initialize();
    return;
  }

  if (page === "submissions") {
    const form = byID("submission-filter-form");
    const params = new URLSearchParams(window.location.search);
    for (const name of ["kind", "problem_id"]) form.elements.namedItem(name).value = params.get(name) || "";
    let currentPage = Number(params.get("page"));
    if (!Number.isSafeInteger(currentPage) || currentPage < 1) currentPage = 1;
    let activeQuery;

    function renderList(result) {
      const list = byID("submission-list");
      list.replaceChildren();
      for (const submission of result.items || []) {
        const card = node("article", undefined, "problem-card");
        const heading = node("div", undefined, "problem-card-heading");
        const title = node("h2");
        const link = node("a", "#" + submission.id + " · 题目 " + submission.problem_id);
        link.href = "/submissions/" + encodeURIComponent(submission.id);
        title.append(link);
        heading.append(title, statusBadge(submission.status));
        const meta = node("p", kindName(submission.kind) + " · " + submission.language + " · " + date(submission.created_at), "profile-note");
        card.append(heading, meta);
        list.append(card);
      }
      if (!result.items || result.items.length === 0) list.append(node("p", "还没有符合条件的提交。去题库选择一道题目开始吧。", "problem-empty"));
      byID("submission-page-info").textContent = "第 " + number(result.page) + " 页 · 共 " + number(result.total) + " 项";
      byID("submission-prev").disabled = result.page <= 1;
      byID("submission-next").disabled = result.page * result.limit >= result.total;
      byID("submission-state").hidden = true;
      byID("submission-results").hidden = false;
      byID("submission-list-poll").textContent = (result.items || []).some((item) => !terminal.has(item.status)) ? "待处理记录的状态会自动更新。" : "";
    }

    const listPoll = poller(
      (signal) => request("/api/submissions?" + activeQuery, "GET", undefined, signal), renderList,
      (result) => (result.items || []).some((item) => !terminal.has(item.status)),
      (error, retrying) => {
        if (error.status === 401) { byID("submission-results").hidden = true; byID("submission-list").replaceChildren(); state("请先登录，再查看你的提交记录。", false, false, true); }
        else { state(error.message + (retrying ? " 正在等待重新读取。" : ""), false, !retrying); }
      }, (result) => (result.items || []).map((item) => item.id + ":" + item.status + ":" + item.generation).join(","),
    );
    function load(pageNumber = currentPage) {
      currentPage = pageNumber;
      activeQuery = new URLSearchParams({ page: String(currentPage), limit: "20" });
      for (const [key, value] of new FormData(form)) if (value.trim()) activeQuery.set(key, value.trim());
      window.history.replaceState(null, "", "/submissions?" + activeQuery);
      byID("submission-results").hidden = true;
      state("正在读取提交记录…", true);
      listPoll.start();
    }
    form.addEventListener("submit", (event) => { event.preventDefault(); load(1); });
    byID("submission-prev").addEventListener("click", () => load(Math.max(1, currentPage - 1)));
    byID("submission-next").addEventListener("click", () => load(currentPage + 1));
    byID("submission-retry").addEventListener("click", () => load());
    load();
    return;
  }

  const submissionID = window.location.pathname.split("/")[2];
  function renderDetail(submission) {
    byID("submission-heading").textContent = "提交 #" + submission.id;
    document.title = "提交 #" + submission.id + " · Biue OJ";
    summary(byID("submission-summary"), submission);
    const metadata = byID("submission-metadata");
    metadata.replaceChildren();
    for (const [label, value] of [["题目", submission.problem_id], ["语言", submission.language], ["题面版本", submission.statement_version], ["数据版本", submission.data_version || "未使用正式数据"], ["提交时间", date(submission.created_at)], ["更新时间", date(submission.updated_at)]]) {
      const row = node("div");
      row.append(node("dt", label), node("dd", value));
      metadata.append(row);
    }
    byID("submission-source").textContent = submission.source_code || "";
    const isRun = submission.kind === "Run";
    byID("submission-run-data").hidden = !isRun;
    byID("submission-stdin").textContent = isRun ? submission.stdin || "" : "";
    byID("submission-output").textContent = isRun ? submission.output || "" : "";
    const diagnostic = isRun || submission.status === "CompileError" ? submission.error_message || "" : "";
    byID("submission-error-section").hidden = !diagnostic;
    byID("submission-error").textContent = diagnostic;
    byID("submission-poll-message").textContent = terminal.has(submission.status) ? "本次处理已结束。" : "状态会自动更新，也可稍后回到本页查看。";
    byID("submission-poll-retry").hidden = true;
    byID("submission-state").hidden = true;
    byID("submission-detail").hidden = false;
  }
  const detailPoll = poller(
    (signal) => request("/api/submissions/" + encodeURIComponent(submissionID), "GET", undefined, signal), renderDetail,
    (submission) => !terminal.has(submission.status),
    (error, retrying) => {
      if ([401, 403, 404].includes(error.status)) {
        byID("submission-detail").hidden = true;
        for (const id of ["submission-source", "submission-stdin", "submission-output", "submission-error"]) byID(id).textContent = "";
        state(error.status === 401 ? "请先登录，再查看提交详情。" : "记录不存在或无权查看。", false, false, error.status === 401);
      } else {
        state(error.message + (retrying ? " 正在等待重新读取。" : ""), false, !retrying);
      }
    }, (submission) => submission.status + ":" + submission.generation,
  );
  byID("submission-retry").addEventListener("click", () => detailPoll.start());
  byID("submission-poll-retry").addEventListener("click", () => detailPoll.start());
  detailPoll.start();
})();
