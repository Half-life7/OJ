"use strict";
(() => {
  const page = document.body.dataset.page;
  if (!["community", "rankings", "problem", "problems"].includes(page)) return;
  const byID = (id) => document.getElementById(id);
  const node = (tag, value, className) => {
    const element = document.createElement(tag);
    if (value !== undefined && value !== null) element.textContent = String(value);
    if (className) element.className = className;
    return element;
  };
  let disposed = false;
  const pending = new Set();
  window.addEventListener("pagehide", () => {
    disposed = true;
    for (const controller of pending) controller.abort();
  });
  const showMessage = (value) => {
    if (!disposed && byID("learning-message")) byID("learning-message").textContent = value;
  };
  class APIError extends Error {
    constructor(value, status, requestID) {
      super(value);
      this.status = status;
      this.requestID = requestID;
    }
  }
  async function api(path, method = "GET", body) {
    if (method !== "GET") {
      const token = await api("/api/auth/csrf");
      body = body === undefined ? {} : body;
      method = method === "GET" ? "POST" : method;
      return apiWithToken(path, method, body, token.csrf_token);
    }
    return apiWithToken(path, method, body, "");
  }
  async function apiWithToken(path, method, body, token) {
    const controller = new AbortController();
    pending.add(controller);
    const timer = setTimeout(() => controller.abort(), 20000);
    const headers = { Accept: "application/json" };
    if (method !== "GET") {
      headers["Content-Type"] = "application/json";
      headers["X-CSRF-Token"] = token;
    }
    try {
      const response = await fetch(path, {
        method, headers, credentials: "same-origin", cache: "no-store", signal: controller.signal,
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      let result = null;
      try { result = await response.json(); } catch {
        throw new APIError("服务暂时无法响应，请稍后重试。", response.status);
      }
      if (!response.ok || result.code) {
        const requestID = response.headers?.get?.("X-Request-ID") || "";
        throw new APIError((result.message || "请求失败") + (requestID ? `（请求 ID：${requestID}）` : ""), response.status, requestID);
      }
      return result.data;
    } catch (error) {
      if (error instanceof APIError) throw error;
      throw new APIError(error.name === "AbortError" ? "请求超时，请刷新状态后再试。" : "网络连接失败，请检查网络后重试。", 0);
    } finally {
      clearTimeout(timer);
      pending.delete(controller);
    }
  }
  async function run(button, fn) {
    if (button) button.disabled = true;
    showMessage("正在处理…");
    try {
      await fn();
      if (byID("learning-message").textContent === "正在处理…") showMessage("操作完成");
    } catch (error) {
      showMessage(error.message);
    } finally {
      if (button) button.disabled = false;
    }
  }

  const params = new URLSearchParams(location.search);
  if (page === "problems") {
    const field = document.querySelector('[name="category"]');
    if (field) field.value = params.get("category") || "";
    return;
  }
  if (page === "problem") {
    const id = location.pathname.split("/").pop();
    const links = node("p");
    for (const [kind, label] of [["discussion", "讨论这道题"], ["solution", "查看题解"]]) {
      const link = node("a", label);
      link.href = `/learning?kind=${kind}&problem_id=${encodeURIComponent(id)}`;
      links.append(link, document.createTextNode("　"));
    }
    byID("problem-detail").append(links);
    api(`/api/practice/problems/${id}`).then((statistics) => {
      if (disposed) return;
      const summary = node("p", `日常通过人数：${statistics.people} · 通过率：${(statistics.acceptance_rate * 100).toFixed(1)}% · ${statistics.solved ? "你已通过" : "尚未通过"}`);
      summary.id = "practice-statistics";
      byID("problem-detail").append(summary);
    }).catch(() => {});
    return;
  }
  if (page === "rankings") {
    let currentPage = 1;
    let epoch = 0;
    async function load(requestedPage = currentPage) {
      const sequence = ++epoch;
      const kind = byID("board-form").elements.kind.value;
      const period = byID("board-form").elements.period.value;
      const board = await api(`/api/leaderboards/${encodeURIComponent(kind)}?page=${requestedPage}&period=${encodeURIComponent(period)}`);
      if (disposed || sequence !== epoch || kind !== byID("board-form").elements.kind.value || period !== byID("board-form").elements.period.value) return;
      currentPage = requestedPage;
      byID("board-rows").replaceChildren();
      for (const row of board.items) {
        const tr = node("tr");
        for (const value of [row.rank, row.username, row.display_name, row.value, new Date(row.reached_at).toLocaleString()]) tr.append(node("td", value));
        byID("board-rows").append(tr);
        }
      byID("board-meta").textContent = `${board.total} 位参与者 · 第 ${currentPage} 页 · ${new Date(board.built_at).toLocaleString()} 更新`;
      byID("board-prev").disabled = currentPage === 1;
      byID("board-next").disabled = currentPage * 50 >= board.total;
      showMessage("只展示三个固定榜单，Run 不计入日常成绩。");
    }
    byID("board-form").onsubmit = (event) => {
      event.preventDefault();
      currentPage = 1;
      run(event.submitter, load);
    };
    byID("board-prev").onclick = (event) => run(event.target, () => load(Math.max(1, currentPage - 1)));
    byID("board-next").onclick = (event) => run(event.target, () => load(currentPage + 1));
    const timer = setInterval(() => {
      if (!document.hidden) load().catch((error) => showMessage(error.message));
    }, 30000);
    window.addEventListener("pagehide", () => clearInterval(timer));
    load().catch((error) => showMessage(error.message));
    return;
  }

  let user = null;
  let selected = null;
  let editing = false;
  let cursor = 0;
  let listEpoch = 0;
  let detailEpoch = 0;
  let replyEpoch = 0;
  let editorDirty = false;
  let savedEditor = "";
  let lastListState = null;
  const filter = byID("learning-filter");
  const form = byID("learning-form");
  for (const key of ["kind", "problem_id", "contest_id"]) {
    if (params.has(key)) {
      filter.elements.namedItem(key).value = params.get(key);
      form.elements.namedItem(key).value = params.get(key);
    }
  }
  const canWrite = () => !!user && user.status === "active" && user.account_type === "permanent";
  const formSnapshot = () => JSON.stringify([...form.elements].map((field) => field.value));
  function draftKey() {
    return editing && selected ? `edit-${selected.id}` : "new";
  }
  function persistDraft() {
    try { sessionStorage.setItem(`biue-community-${draftKey()}`, JSON.stringify(Object.fromEntries(new FormData(form)))); } catch {}
  }
  function restoreDraft() {
    try {
      const values = JSON.parse(sessionStorage.getItem(`biue-community-${draftKey()}`) || "null");
      if (!values) return false;
      for (const [name, value] of Object.entries(values)) {
        const field = form.elements.namedItem(name);
        if (field) field.value = value;
      }
      return true;
    } catch { return false; }
  }
  function clearDraft() {
    try { sessionStorage.removeItem(`biue-community-${draftKey()}`); } catch {}
  }
  function markDirty() {
    editorDirty = formSnapshot() !== savedEditor;
    if (editorDirty) persistDraft();
  }
  function mayAbandon() {
    return !editorDirty || window.confirm("当前内容尚未保存。切换会保留本标签页草稿；是否继续？");
  }
  function hideActions() {
    for (const action of ["favorite", "unfavorite", "like", "unlike", "reply", "edit", "delete", "approve", "reject"]) byID(`learning-${action}`).hidden = true;
  }
  function configureActions(document) {
    hideActions();
    const writer = canWrite();
    if (writer && document.kind === "list") {
      byID("learning-favorite").hidden = document.favorited;
      byID("learning-unfavorite").hidden = !document.favorited;
    }
    if (writer && document.kind === "discussion") {
      byID("learning-like").hidden = document.liked;
      byID("learning-unlike").hidden = !document.liked;
      byID("learning-reply").hidden = false;
    }
    byID("learning-edit").hidden = !document.can_edit;
    byID("learning-delete").hidden = !document.can_delete;
    byID("learning-approve").hidden = !document.can_review;
    byID("learning-reject").hidden = !document.can_review;
    byID("learning-state").textContent = `${document.favorited ? "已收藏" : "未收藏"} · ${document.liked ? "已点赞" : "未点赞"} · ${document.likes} 赞 / ${document.favorites} 收藏 · 审核：${document.review_status}`;
  }
  async function list(reset = true, retry = false) {
    if (reset) {
      cursor = 0;
      byID("learning-list").replaceChildren();
    }
    const kind = filter.elements.kind.value;
    const problemID = filter.elements.problem_id.value;
    const contestID = filter.elements.contest_id.value;
    const scope = filter.elements.scope.value;
    const after = retry && lastListState ? lastListState.after : cursor;
    const sequence = ++listEpoch;
    lastListState = { kind, problemID, contestID, scope, after };
    const query = new URLSearchParams({ kind, after });
    if (problemID) query.set("problem_id", problemID);
    if (contestID) query.set("contest_id", contestID);
    if (scope) query.set(scope, "1");
    byID("learning-retry").hidden = true;
    showMessage("正在读取内容…");
    setListBusy(true);
    try {
      const data = await api(`/api/community?${query}`);
      if (disposed || sequence !== listEpoch) return;
      for (const document of data.items) {
        const button = node("button", `#${document.id} ${document.title} · ${document.username} · ${document.review_status} · ${document.likes} 赞 / ${document.favorites} 收藏`);
        button.type = "button";
        button.className = "button secondary";
        button.addEventListener("click", () => run(button, () => open(document.id)));
        byID("learning-list").append(button);
      }
      cursor = data.next;
      byID("learning-more").hidden = !cursor;
      lastListState.after = cursor;
      byID("learning-retry").hidden = true;
      showMessage(data.items.length ? "请选择内容查看详情。" : "没有找到符合条件的内容。");
    } catch (error) {
      if (sequence === listEpoch) {
        byID("learning-retry").hidden = false;
        showMessage(error.message);
        throw error;
      }
    } finally {
      if (sequence === listEpoch) setListBusy(false);
    }
  }
  function setListBusy(busy) {
    byID("learning-list").setAttribute("aria-busy", String(busy));
    byID("learning-more").disabled = busy;
  }
  async function loadReplies(document) {
    const sequence = ++replyEpoch;
    byID("learning-replies").replaceChildren();
    if (document.kind !== "discussion") return;
    const replies = await api(`/api/community?kind=discussion&parent_id=${document.id}`);
    if (sequence !== replyEpoch || selected?.id !== document.id || disposed) return;
    for (const reply of replies.items) {
      const button = node("button", `${reply.username}：${reply.title}`);
      button.type = "button";
      button.addEventListener("click", () => run(button, () => open(reply.id)));
      byID("learning-replies").append(button);
    }
  }
  async function open(id) {
    if (!mayAbandon()) return;
    const sequence = ++detailEpoch;
    const targetID = Number(id);
    showMessage("正在读取内容详情…");
    const document = await api(`/api/community/${targetID}`);
    if (disposed || sequence !== detailEpoch) return;
    selected = document;
    editing = false;
    byID("learning-detail").hidden = false;
    byID("learning-title").textContent = document.title;
    byID("learning-meta").textContent = `${document.username} · ${document.visibility} · ${document.kind} · 修订 ${document.revision}`;
    byID("learning-html").innerHTML = document.html;
    byID("learning-problems").replaceChildren();
    for (const problem of document.problems || []) {
      const item = node("li");
      const link = node("a", problem.title);
      link.href = `/problems/${problem.id}`;
      item.append(link);
      byID("learning-problems").append(item);
    }
    configureActions(document);
    loadReplies(document).catch((error) => showMessage(error.message));
    showMessage("内容已加载。");
  }
  function input() {
    const data = Object.fromEntries(new FormData(form));
    for (const key of ["problem_id", "contest_id", "parent_id"]) data[key] = data[key] ? Number(data[key]) : null;
    data.problems = data.problems.trim() ? data.problems.split(/[,，]/).map((value) => Number(value.trim())) : [];
    if (data.problems.some((value) => !Number.isSafeInteger(value) || value < 1)) throw new APIError("题单题目编号必须是正整数。", 400);
    data.revision = editing && selected ? selected.revision : 0;
    return data;
  }
  function showEditor(mode) {
    const nextEditing = mode === "edit";
    if ((nextEditing || editorDirty) && !mayAbandon()) return;
    editing = nextEditing;
    form.hidden = false;
    if (nextEditing && selected) {
      for (const key of ["kind", "title", "body", "visibility", "problem_id", "contest_id", "parent_id"]) form.elements.namedItem(key).value = selected[key] ?? "";
      form.elements.problems.value = (selected.problems || []).map((problem) => problem.id).join(",");
      byID("learning-editor-title").textContent = `编辑 #${selected.id}`;
    } else {
      form.reset();
      if (selected) {
        form.elements.kind.value = "discussion";
        form.elements.problem_id.value = selected.problem_id || "";
        form.elements.contest_id.value = selected.contest_id || "";
        form.elements.visibility.value = selected.visibility || "public";
      }
      byID("learning-editor-title").textContent = "发布内容";
    }
    restoreDraft();
    savedEditor = formSnapshot();
    editorDirty = false;
    form.elements.title.focus?.();
  }
  function configureWriter() {
    byID("learning-new").hidden = !canWrite();
    byID("learning-login").hidden = !!user;
    byID("learning-form").hidden = true;
    if (!user) {
      byID("learning-access-note").textContent = "游客可以浏览公开内容；登录永久账号后可以发布和互动。";
    } else if (!canWrite()) {
      byID("learning-access-note").textContent = "当前是比赛临时账号，只能浏览已授权的比赛内容；如需发布，请登录永久账号。";
    } else {
      byID("learning-access-note").textContent = "当前账号可以发布题单、讨论和题解。";
    }
  }

  filter.onsubmit = (event) => { event.preventDefault(); run(event.submitter, () => list(true)); };
  byID("learning-more").onclick = (event) => run(event.target, () => list(false));
  byID("learning-retry").onclick = (event) => run(event.target, () => list(false, true));
  byID("learning-new").onclick = () => showEditor("new");
  byID("learning-edit").onclick = () => showEditor("edit");
  byID("learning-reply").onclick = () => {
    if (!selected || !mayAbandon()) return;
    editing = false;
    form.hidden = false;
    form.reset();
    form.elements.kind.value = "discussion";
    form.elements.title.value = `回复：${selected.title}`.slice(0, 200);
    for (const key of ["problem_id", "contest_id", "visibility"]) form.elements.namedItem(key).value = selected[key] ?? "";
    form.elements.parent_id.value = selected.id;
    savedEditor = formSnapshot();
    editorDirty = false;
    byID("learning-editor-title").textContent = `回复 #${selected.id}`;
    form.elements.body.focus?.();
  };
  form.addEventListener("input", markDirty);
  form.addEventListener("change", markDirty);
  form.onsubmit = (event) => {
    event.preventDefault();
    run(event.submitter, async () => {
      const targetID = editing && selected ? selected.id : 0;
      const result = await api(editing ? `/api/community/${targetID}` : "/api/community", editing ? "PUT" : "POST", input());
      editorDirty = false;
      clearDraft();
      form.hidden = true;
      await list(true);
      await open(result.id);
    });
  };
  for (const action of ["favorite", "unfavorite", "like", "unlike", "delete", "approve", "reject"]) {
    byID(`learning-${action}`).onclick = (event) => run(event.target, async () => {
      if (!selected) return;
      const targetID = selected.id;
      const revision = selected.revision;
      await api(`/api/community/${targetID}/actions`, "POST", { action, revision });
      if (action === "delete") {
        if (selected?.id === targetID) {
          byID("learning-detail").hidden = true;
          selected = null;
          editing = false;
        }
      } else if (selected?.id === targetID) {
        await open(targetID);
      }
      await list(true);
    });
  }
  window.addEventListener("beforeunload", (event) => {
    if (editorDirty) { event.preventDefault(); event.returnValue = ""; }
  });

  api("/api/users/me").then((account) => {
    user = account;
  }).catch(() => {
    user = null;
  }).finally(() => {
    configureWriter();
    hideActions();
    list(true).catch((error) => showMessage(error.message));
  });
})();
