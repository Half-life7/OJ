"use strict";
(() => {
  const page = document.body.dataset.page;
  if (!["contests", "identities"].includes(page)) return;
  const byID = (id) => document.getElementById(id);
  const node = (tag, value, className) => {
    const element = document.createElement(tag);
    if (value !== undefined && value !== null) element.textContent = String(value);
    if (className) element.className = className;
    return element;
  };
  const message = (id, value, success = false) => {
    const element = byID(id);
    if (!element) return;
    element.textContent = value || "";
    element.classList.toggle("success", success);
    element.hidden = !value;
  };
  const date = (value) => {
    const parsed = new Date(value);
    return Number.isNaN(parsed.valueOf()) ? "—" : parsed.toLocaleString();
  };
  const localTime = (value) => {
    const parsed = new Date(value);
    return new Date(parsed.getTime() - parsed.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
  };
  class APIError extends Error {
    constructor(value, status, fields, requestID) {
      super(value);
      this.status = status;
      this.fields = fields;
      this.requestID = requestID;
    }
  }

  let csrfToken = "";
  let serverClockOffset = 0;
  async function api(path, method = "GET", data) {
    if (method !== "GET" && !csrfToken) {
      const token = await api("/api/auth/csrf");
      csrfToken = token?.csrf_token || "";
      if (!csrfToken) throw new APIError("无法验证当前页面，请刷新后重试。", 403);
    }
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 20000);
    const headers = { Accept: "application/json" };
    if (method !== "GET") {
      headers["Content-Type"] = "application/json";
      headers["X-CSRF-Token"] = csrfToken;
    }
    try {
      const response = await fetch(path, {
        method, headers, credentials: "same-origin", cache: "no-store", signal: controller.signal,
        body: data === undefined ? undefined : JSON.stringify(data),
      });
      const clock = Date.parse(response.headers?.get?.("Date"));
      if (Number.isFinite(clock)) serverClockOffset = clock - Date.now();
      let result = null;
      try { result = await response.json(); } catch {
        throw new APIError(response.status === 413 ? "上传内容超过大小限制。" : "服务暂时无法响应，请稍后重试。", response.status);
      }
      if (!response.ok || result.code) {
        if (response.status === 403) csrfToken = "";
        const requestID = response.headers?.get?.("X-Request-ID") || "";
        const suffix = requestID ? `（请求 ID：${requestID}）` : "";
        throw new APIError((result.message || (response.status === 409 ? "状态已变化，请刷新后重试。" : "操作未完成，请稍后重试。")) + suffix, response.status, result.data?.fields, requestID);
      }
      return result.data;
    } catch (error) {
      if (error instanceof APIError) throw error;
      throw new APIError(error.name === "AbortError" ? "请求超时。写操作请先刷新核对结果，再决定是否重试。" : "网络连接失败，请检查网络后重试。", 0);
    } finally {
      clearTimeout(timer);
    }
  }
  async function apiUpload(path, formData) {
    if (!csrfToken) {
      const token = await api("/api/auth/csrf");
      csrfToken = token?.csrf_token || "";
      if (!csrfToken) throw new APIError("无法验证当前页面，请刷新后重试。", 403);
    }
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 120000);
    try {
      const response = await fetch(path, {
        method: "POST",
        headers: { Accept: "application/json", "X-CSRF-Token": csrfToken },
        credentials: "same-origin",
        cache: "no-store",
        signal: controller.signal,
        body: formData,
      });
      let result = null;
      try { result = await response.json(); } catch {
        throw new APIError(response.status === 413 ? "题包超过大小限制。" : "题包服务暂时无法响应。", response.status);
      }
      if (!response.ok || result.code) {
        if (response.status === 403) csrfToken = "";
        const requestID = response.headers?.get?.("X-Request-ID") || "";
        throw new APIError((result.message || "题包操作未完成。") + (requestID ? `（请求 ID：${requestID}）` : ""), response.status, result.data?.fields, requestID);
      }
      return result.data;
    } catch (error) {
      if (error instanceof APIError) throw error;
      throw new APIError(error.name === "AbortError" ? "题包上传超时，请先核对题目是否已经导入。" : "题包上传失败，请检查网络。", 0);
    } finally {
      clearTimeout(timer);
    }
  }
  async function run(button, fn, target = "contest-message") {
    if (button) button.disabled = true;
    message(target, "正在处理…", true);
    try {
      await fn();
    } catch (error) {
      message(target, error.message);
    } finally {
      if (button) button.disabled = button.id === "contest-package-confirm" ? !packagePreview : false;
    }
  }

  if (page === "identities") {
    let cursor = 0;
    function switchIdentityWorkspace(name, updateHash = true) {
      if (!["list", "edit", "link"].includes(name)) name = "list";
      for (const candidate of ["list", "edit", "link"]) {
        byID(`identity-${candidate}-workspace`).hidden = candidate !== name;
        byID(`identity-nav-${candidate}`).classList.toggle("active", candidate === name);
      }
      if (updateHash && window.history?.replaceState) window.history.replaceState(null, "", `/admin/identities#${name}`);
    }
    async function people() {
      const rows = await api(`/api/admin/persons?after=${cursor}`);
      for (const person of rows) {
        const button = node("button", `#${person.id} ${person.display_name} · ${person.student_no || "无学号"} · ${person.student_no_status} · 修订 ${person.revision} · ${(person.accounts || []).map((account) => `${account.username} (#${account.id})`).join(", ")}`);
        button.type = "button";
        button.addEventListener("click", () => {
          for (const [key, value] of Object.entries(person)) {
            const input = byID("identity-form").elements.namedItem(key);
            if (input) input.value = value ?? "";
          }
          switchIdentityWorkspace("edit");
        });
        byID("identity-list").append(button);
        cursor = person.id;
      }
      byID("identity-more").hidden = rows.length < 100;
    }
    byID("identity-more").onclick = (event) => run(event.target, people);
    byID("identity-form").onsubmit = (event) => {
      event.preventDefault();
      run(event.submitter || event.target.querySelector("button"), async () => {
        const data = Object.fromEntries(new FormData(event.target));
        const id = Number(data.id);
        delete data.id;
        data.revision = Number(data.revision);
        data.student_no = data.student_no || null;
        await api(`/api/admin/persons/${id}`, "PUT", data);
        cursor = 0;
        byID("identity-list").replaceChildren();
        await people();
        message("contest-message", "身份修改已保存并记录审计。", true);
      });
    };
    byID("identity-link-form").onsubmit = (event) => {
      event.preventDefault();
      run(event.submitter || event.target.querySelector("button"), async () => {
        const data = Object.fromEntries(new FormData(event.target));
        for (const key of ["user_id", "from_person_id", "to_person_id"]) data[key] = Number(data[key]);
        await api("/api/admin/identity-links", "POST", data);
        message("contest-message", "账号身份关联已保存；既有比赛快照保持不变。", true);
      });
    };
    byID("identity-nav-list").addEventListener("click", () => switchIdentityWorkspace("list"));
    byID("identity-nav-edit").addEventListener("click", () => switchIdentityWorkspace("edit"));
    byID("identity-nav-link").addEventListener("click", () => switchIdentityWorkspace("link"));
    people().then(() => message("contest-message", "请选择需要审核的身份。")).catch((error) => message("contest-message", error.message));
    switchIdentityWorkspace((window.location.hash || "").slice(1), false);
    return;
  }

  const createForm = byID("contest-create-form");
  const contestSourceEditor = page === "contests" && window.BiueEditor?.mount
    ? window.BiueEditor.mount(byID("contest-submit-form").elements.source_code, byID("contest-submit-form").elements.language.value || "cpp17", { ariaLabel: "比赛提交源码" })
    : null;
  const createFields = () => [...createForm.elements].filter((field) => field.name !== "edit_current");
  let user = null;
  let selected = null;
  let view = null;
  let listBefore = 0;
  let detailEpoch = 0;
  let manageEpoch = 0;
  let csvState = null;
  let csvEpoch = 0;
  let activePanel = "overview";
  let dirty = false;
  let savedSnapshot = "";
  let detailLoading = false;
  let editingMode = false;
  let packageSource = null;
  let packageFile = null;
  let packagePreview = null;
  let packageEpoch = 0;
  let lastCreation = null;
  try { lastCreation = JSON.parse(sessionStorage.getItem("biue-contest-creation-v2") || "null"); } catch {}
  function rememberCreation(value) {
    lastCreation = value;
    try {
      if (value) sessionStorage.setItem("biue-contest-creation-v2", JSON.stringify(value));
      else sessionStorage.removeItem("biue-contest-creation-v2");
    } catch {}
  }

  function formSnapshot() {
    return JSON.stringify([createForm.elements.edit_current.checked ? selected?.id || 0 : 0, ...createFields().map((field) => field.type === "checkbox" ? field.checked : field.value)]);
  }
  function markDirty() {
    dirty = formSnapshot() !== savedSnapshot;
    persistDraft();
  }
  function draftKey() {
    return createForm.elements.edit_current.checked && selected ? `contest-edit-${selected.id}` : "contest-create";
  }
  function persistDraft() {
    try {
      const values = {};
      for (const field of createFields()) if (field.type !== "password") values[field.name] = field.type === "checkbox" ? field.checked : field.value;
      sessionStorage.setItem(`biue-draft-${draftKey()}`, JSON.stringify(values));
    } catch {}
  }
  function restoreDraft() {
    try {
      const values = JSON.parse(sessionStorage.getItem(`biue-draft-${draftKey()}`) || "null");
      if (!values) return false;
      for (const [name, value] of Object.entries(values)) {
        const field = createForm.elements.namedItem(name);
        if (!field || field.type === "password") continue;
        if (field.type === "checkbox") field.checked = !!value;
        else field.value = value;
      }
      return true;
    } catch { return false; }
  }
  function clearDraft() {
    try { sessionStorage.removeItem(`biue-draft-${draftKey()}`); } catch {}
  }
  function mayDiscard() {
    return !dirty || window.confirm("当前比赛设置尚未保存。离开会保留一份本标签页草稿；是否继续？");
  }
  function clearCreateErrors() {
    for (const field of createForm.elements) {
      field.removeAttribute?.("aria-invalid");
      field.setCustomValidity?.("");
    }
    byID("contest-form-error").hidden = true;
  }
  function createErrors(fields) {
    const errors = Object.values(fields);
    for (const [name, error] of Object.entries(fields)) {
      const field = createForm.elements.namedItem(name);
      if (field) {
        field.setAttribute("aria-invalid", "true");
        field.setCustomValidity(error);
      }
    }
    byID("contest-form-error").textContent = errors.join("；");
    byID("contest-form-error").hidden = false;
    createForm.querySelector('[aria-invalid="true"]')?.focus();
  }
  function calculatedEnd() {
    const fields = createForm.elements;
    return fields.end_mode.value === "duration"
      ? new Date(new Date(fields.starts_at.value).getTime() + Number(fields.duration_minutes.value) * 60000)
      : new Date(fields.ends_at.value);
  }
  function previewEnd() {
    const fields = createForm.elements;
    const automatic = fields.end_mode.value === "duration";
    const start = new Date(fields.starts_at.value);
    const minutes = Number(fields.duration_minutes.value);
    fields.ends_at.readOnly = automatic;
    fields.duration_minutes.disabled = !automatic;
    byID("contest-duration-label").hidden = !automatic;
    if (automatic) {
      const end = new Date(start.getTime() + minutes * 60000);
      fields.ends_at.value = Number.isFinite(start.getTime()) && Number.isInteger(minutes) && minutes > 0 && minutes <= 43200 && end.getFullYear() <= 9999 ? localTime(end) : "";
    }
    const end = calculatedEnd();
    byID("contest-end-preview").textContent = Number.isFinite(end.getTime())
      ? `预计结束：${end.toLocaleString()}；实际时长 ${(end - start) / 60000} 分钟。`
      : "填写开始时间和有效时长后，自动显示结束时间。";
  }
  function contestInput() {
    clearCreateErrors();
    previewEnd();
    const fields = createForm.elements;
    const data = Object.fromEntries(new FormData(createForm));
    const start = new Date(fields.starts_at.value);
    const end = calculatedEnd();
    const errors = {};
    if (!data.title.trim() || Array.from(data.title.trim()).length > 200) errors.title = "比赛名称需为 1～200 个字符";
    if (!Number.isFinite(start.getTime())) errors.starts_at = "请填写有效开始时间";
    else if (start.getTime() <= Date.now() + serverClockOffset) errors.starts_at = "开始时间必须晚于服务器当前时间，请至少预留几分钟";
    if (fields.end_mode.value === "duration" && (!Number.isInteger(Number(fields.duration_minutes.value)) || Number(fields.duration_minutes.value) <= 0 || Number(fields.duration_minutes.value) > 43200)) errors.duration_minutes = "持续时间需为 1～43200 的整数分钟";
    if (!Number.isFinite(end.getTime()) || end <= start || end.getFullYear() > 9999) errors.ends_at = "结束时间必须晚于开始时间，且年份不能超过 9999";
    else if (end - start > 30 * 86400000) errors.ends_at = "比赛持续时间不能超过 30 天";
    if (Object.keys(errors).length) {
      createErrors(errors);
      throw new APIError(Object.values(errors).join("；"), 400);
    }
    const input = {
      title: data.title.trim(), scoring_mode: data.scoring_mode, enrollment_policy: data.enrollment_policy,
      starts_at: start.toISOString(), ends_at: end.toISOString(),
      approval_required: fields.approval_required.checked, team_mode: fields.team_mode.checked,
    };
    if (fields.edit_current.checked) {
      if (!selected) throw new APIError("请先打开要编辑的比赛。", 400);
      input.revision = selected.revision;
    }
    return input;
  }

  function switchCenterWorkspace(name, updateHash = true) {
    if (!["list", "create"].includes(name)) name = "list";
    if (name === "create" && (!user || user.global_role !== "super_admin" || user.account_type !== "permanent")) name = "list";
    byID("contest-list-workspace").hidden = name !== "list";
    byID("contest-create").hidden = name !== "create";
    byID("contest-nav-list").classList.toggle("active", name === "list");
    byID("contest-nav-create").classList.toggle("active", name === "create");
    byID("contest-nav-create").disabled = !user || user.global_role !== "super_admin" || user.account_type !== "permanent";
    if (updateHash && window.history?.replaceState) {
      window.history.replaceState(null, "", `/contests${name === "create" ? "#create" : "#list"}`);
    }
  }

  function showCenter() {
    activePanel = "overview";
    document.body.classList.remove("wide-workspace");
    byID("contest-detail").hidden = true;
    byID("contest-center").hidden = false;
    switchCenterWorkspace(window.location.hash === "#create" ? "create" : "list", false);
    document.title = "比赛中心 · Biue OJ";
  }
  function setDetailBusy(busy) {
    for (const id of ["contest-submit-form", "contest-live-edit-form", "contest-manage-form", "contest-csv-form", "contest-temporary-form", "contest-history-form"]) {
      const form = byID(id);
      for (const field of form.elements) field.disabled = busy;
    }
    for (const id of ["contest-enroll", "contest-refresh", "contest-settle", "contest-csv-file", "contest-csv-confirm", "contest-package-file", "contest-package-sample"]) byID(id).disabled = busy;
    byID("contest-package-confirm").disabled = busy || !packagePreview;
  }
  function showCreate() {
    if (!user || user.global_role !== "super_admin" || user.account_type !== "permanent") {
      message("contest-message", "当前账号没有创建比赛权限。");
      return;
    }
    switchCenterWorkspace("create");
  }
  function switchPanel(name) {
    if (!view) return;
    if (!view.manager && ["edit", "package", "manage", "import", "accounts", "settle"].includes(name)) name = "overview";
    if (name === "package" && user?.global_role !== "super_admin") name = "overview";
    activePanel = name;
    document.body.classList.toggle("wide-workspace", ["problems", "standings", "submit"].includes(name));
    for (const button of document.querySelectorAll("[data-contest-panel]")) button.classList.toggle("active", button.dataset.contestPanel === name);
    for (const section of document.querySelectorAll("[data-contest-panel-content]")) section.hidden = section.dataset.contestPanelContent !== name;
    if (name === "manage") loadManageOptions().catch((error) => message("contest-detail-message", error.message));
    if (name === "settle") renderSettleTarget();
    if (name === "submit") {
      renderFocusedProblem();
      setTimeout(() => contestSourceEditor?.requestMeasure?.(), 0);
    }
  }
  function clearPackageState(text = "") {
    ++packageEpoch;
    packageSource = null;
    packageFile = null;
    packagePreview = null;
    byID("contest-package-preview").replaceChildren();
    byID("contest-package-confirm").disabled = true;
    message("contest-package-message", text);
  }
  function packageFormData() {
    if (!packageSource) throw new APIError("请先选择题包或内置样例。", 400);
    const data = new FormData();
    if (packageSource === "builtin") data.append("sample", "1");
    else if (packageFile) data.append("archive", packageFile, packageFile.name);
    else throw new APIError("选择题包文件已失效，请重新选择。", 400);
    return data;
  }
  function renderPackagePreview(preview) {
    const target = byID("contest-package-preview");
    target.replaceChildren();
    target.append(node("h3", preview.title || "未命名题包"));
    target.append(node("p", `${preview.cases?.length || 0} 个测试点 · ${preview.total_score || 0} 分 · 摘要 ${preview.digest || "—"}`, "profile-note"));
    if (preview.tags?.length) target.append(node("p", `标签：${preview.tags.join("、")}`, "field-help"));
    if (preview.warnings?.length) {
      const warnings = node("ul", undefined, "field-help");
      for (const warning of preview.warnings) warnings.append(node("li", warning));
      target.append(warnings);
    }
    const cases = node("div", undefined, "package-case-grid");
    for (const item of preview.cases || []) cases.append(node("span", `${item.id} · ${item.score} 分`, "problem-badge"));
    target.append(cases);
  }
  async function previewContestPackage(sample) {
    const sequence = ++packageEpoch;
    packagePreview = null;
    byID("contest-package-confirm").disabled = true;
    byID("contest-package-preview").replaceChildren(node("p", "正在读取并校验题包…", "profile-note"));
    const result = await apiUpload("/api/admin/problems/package-preview", packageFormData());
    if (sequence !== packageEpoch || !selected) return;
    packagePreview = result.preview;
    renderPackagePreview(result.preview);
    byID("contest-package-confirm").disabled = false;
    message("contest-package-message", sample ? "内置题包校验通过，确认后将导入并加入当前比赛。" : "题包校验通过，确认后将导入并加入当前比赛。", true);
  }
  async function importContestPackage() {
    if (!selected || !packageSource || !packagePreview) throw new APIError("请先重新预览题包。", 409);
    if (new Date(selected.ends_at) <= new Date()) throw new APIError("比赛已经结束，不能再导入并绑定新题目。", 409);
    const contestID = selected.id;
    const contestTitle = selected.title;
    const imported = await apiUpload("/api/admin/problems/package-import", packageFormData());
    if (!imported?.id) throw new APIError("题包导入响应缺少题目编号，请到题目管理核对。", 503);
    try {
      await api(`/api/admin/problems/${imported.id}`, "PUT", {
        title: imported.title,
        problem_type: imported.problem_type,
        checker_mode: imported.checker_mode,
        checker_source: imported.checker_source || "",
        description: imported.description,
        input_format: imported.input_format,
        output_format: imported.output_format,
        sample_input: imported.sample_input,
        sample_output: imported.sample_output,
        time_limit_ms: imported.time_limit_ms,
        memory_limit_mb: imported.memory_limit_mb,
        difficulty: imported.difficulty,
        tags: imported.tags,
        status: "published",
        visibility: "contest_only",
        revision: imported.revision,
      });
      await api(`/api/contests/${contestID}/manage`, "POST", { action: "add_problem", problem_id: imported.id });
    } catch (error) {
      throw new APIError(`题包已经导入题目管理（#${imported.id}），但发布或加入比赛失败：${error.message}`, error.status);
    }
    clearPackageState();
    await loadDetail(contestID);
    activePanel = "package";
    switchPanel("package");
    message("contest-package-message", `“${imported.title}”已导入并固定到比赛“${contestTitle}”（#${contestID}）。`, true);
  }
  function renderOverview(contest) {
    const target = byID("contest-overview");
    target.replaceChildren();
    for (const [label, value] of [
      ["比赛编号", `#${contest.id}`], ["计分模式", contest.scoring_mode], ["报名方式", contest.enrollment_policy],
      ["开始", date(contest.starts_at)], ["结束", date(contest.ends_at)],
      ["报名审核", contest.approval_required ? "需要管理员审核" : "自动通过"], ["队伍模式", contest.team_mode ? "团队赛" : "个人赛"],
      ["状态", contest.settled_at ? `已结算 · ${date(contest.settled_at)}` : new Date(contest.starts_at) > new Date() ? "赛前" : new Date(contest.ends_at) <= new Date() ? "已结束，待结算" : "进行中"],
    ]) {
      const row = node("div", undefined, "contest-overview-row");
      row.append(node("dt", label), node("dd", value));
      target.append(row);
    }
  }
  function renderRosterHeader() {
    const head = byID("contest-roster-head");
    head.replaceChildren();
    const labels = ["名次", "队伍", "成员", "状态", "提交", "AC", "总分", "罚时"];
    for (const label of labels) head.append(node("th", label));
    for (const problem of view?.problems || []) {
      const title = problem.statement?.title || `题目 ${problem.problem_id}`;
      const cell = node("th", `P${problem.problem_id} · ${title}`, "contest-problem-heading-cell");
      cell.title = title;
      head.append(cell);
    }
  }
  function resultLabel(status) {
    return {
      Accepted: "通过", WrongAnswer: "答案错误", TimeLimitExceeded: "超时",
      MemoryLimitExceeded: "超内存", RuntimeError: "运行错误", CompileError: "编译错误",
      SystemError: "系统错误", Cancelled: "已取消",
    }[status] || (status || "未提交");
  }
  function renderProblemResult(point, problem) {
    if (!point || (!point.attempts && !point.last_status)) return node("span", "未提交");
    const maximum = Number(problem.max_score || 0);
    const score = Number(point.score || 0);
    const duration = Number(point.execution_time_ms || 0);
    const memory = Number(point.memory_usage_kb || 0);
    const wrap = node("div", undefined, "contest-problem-result");
    wrap.append(
      node("strong", `${resultLabel(point.last_status)} · ${score}${maximum ? "/" + maximum : ""} 分`),
      node("span", `${point.attempts || 0} 次提交 · ${duration ? duration + " ms" : "耗时—"}`),
      node("span", `${memory ? Math.round(memory / 1024) + " MiB" : "内存—"}${point.wrong ? ` · ${point.wrong} 次错误` : ""}`),
    );
    return wrap;
  }
  function renderRoster() {
    const target = byID("contest-roster");
    target.replaceChildren();
    if (!view) return;
    renderRosterHeader();
    const query = byID("contest-search").value.toLowerCase();
    for (const team of view.teams) {
      const row = view.standings.find((entry) => entry.team_id === team.id);
      const members = view.members.filter((member) => member.team_id === team.id).map((member) => {
        const identity = member.identity;
        return `${identity.Username} (#${member.user_id}) / ${identity.Name} / ${identity.Student || "—"} / ${identity.StudentStatus} / ${identity.Organization || "—"} / ${identity.Kind}`;
      }).join("; ");
      if (query && !`${team.name} ${members}`.toLowerCase().includes(query)) continue;
      if (byID("contest-filter-type").value && team.participant_type !== byID("contest-filter-type").value) continue;
      if (byID("contest-filter-status").value && team.status !== byID("contest-filter-status").value) continue;
      if ((row?.submissions || 0) < Number(byID("contest-filter-submissions").value || 0)) continue;
      if (byID("contest-filter-account").value && !view.members.some((member) => member.team_id === team.id && member.identity.Kind === byID("contest-filter-account").value)) continue;
      const tr = node("tr");
      tr.append(
        node("td", row?.rank || "—"),
        node("td", `${team.name} (#${team.id})`),
        node("td", members, "contest-team-members"),
        node("td", `${team.participant_type} / ${team.status}`),
        node("td", row?.submissions || 0),
        node("td", row?.solved || 0),
        node("td", row?.score || 0),
        node("td", row?.penalty || 0),
      );
      for (const problem of view.problems) {
        const cell = node("td");
        cell.append(renderProblemResult(row?.problems?.[problem.problem_id], problem));
        tr.append(cell);
      }
      target.append(tr);
    }
  }
  function contestLanguageControls() {
    if (!view) return;
    const id = Number(byID("contest-problem-select").value);
    const problem = view.problems.find((item) => item.problem_id === id);
    const outputOnly = problem?.statement.problem_type === "output_only";
    const language = byID("contest-submit-form").elements.language;
    for (const option of language.options) option.disabled = outputOnly ? option.value !== "text" : option.value === "text";
    if (language.selectedOptions[0]?.disabled) language.value = outputOnly ? "text" : "cpp17";
    contestSourceEditor?.setLanguage(language.value);
    byID("contest-submit-form").elements.source_code.placeholder = outputOnly
      ? JSON.stringify(Object.fromEntries((problem.output_cases || []).map((caseID) => [caseID, ""])), null, 2)
      : "";
  }
  function contestTeamIDs(activeOnly = false) {
    if (!view) return [];
    return view.teams
      .filter((team) => !activeOnly || team.status === "active")
      .filter((team) => view.members.some((member) => member.team_id === team.id && member.user_id === user?.id))
      .map((team) => team.id);
  }
  function currentTeamStanding() {
    if (!view) return null;
    const ids = new Set(contestTeamIDs());
    return view.standings.find((entry) => ids.has(entry.team_id)) || null;
  }
  function contestProblemState(problemID) {
    const standing = currentTeamStanding();
    const result = standing?.problems?.[problemID];
    const attempts = Number(view?.problem_attempts?.[problemID] ?? result?.attempts ?? 0);
    if (result?.accepted) return { key: "accepted", label: "通过", attempts };
    if (attempts > 0 || Number(result?.score || 0) > 0 || Number(result?.wrong || 0) > 0) return { key: "written", label: "已写", attempts };
    return { key: "unwritten", label: "未写", attempts };
  }
  function appendContestStatement(target, problem) {
    target.append(node("span", `#${problem.problem_id} · 题面 v${problem.statement_version}`, "problem-number"));
    target.append(node("h2", problem.statement.title || "题目"));
    for (const [key, label, htmlKey] of [["description", "题面", "description_html"], ["input_format", "输入", "input_format_html"], ["output_format", "输出", "output_format_html"], ["sample_input", "样例输入", ""], ["sample_output", "样例输出", ""]]) {
      if (problem.statement[htmlKey]) {
        const prose = node("div", undefined, "learning-prose");
        prose.innerHTML = problem.statement[htmlKey];
        target.append(node("h4", label), prose);
      } else if (problem.statement[key]) {
        target.append(node("h4", label), node("pre", problem.statement[key]));
      }
    }
  }
  function renderAnswerSheet() {
    const sheet = byID("contest-answer-sheet");
    sheet.replaceChildren();
    if (!view.problems.length) {
      sheet.append(node("p", "当前没有可展示的比赛题目。", "problem-empty"));
      byID("contest-answer-summary").textContent = "尚未绑定题目，或比赛开始前题面仍隐藏。";
      return;
    }
    let written = 0, accepted = 0;
    for (const problem of view.problems) {
      const state = contestProblemState(problem.problem_id);
      if (state.key !== "unwritten") written++;
      if (state.key === "accepted") accepted++;
      const button = node("button", undefined, `contest-answer-item contest-answer-${state.key}`);
      button.type = "button";
      button.append(node("strong", `${problem.problem_id}. ${problem.statement.title || "题目"}`), node("span", state.label));
      button.setAttribute("aria-label", `${problem.problem_id} 题${state.label}，进入单题答题`);
      button.addEventListener("click", () => focusContestProblem(problem.problem_id));
      sheet.append(button);
    }
    byID("contest-answer-summary").textContent = `本人：已写 ${written} 题 · 通过 ${accepted} 题 · 未写 ${view.problems.length - written} 题；Run 不计入答题卡。`;
  }
  function renderFocusedProblem(problemID = Number(byID("contest-problem-select").value)) {
    const target = byID("contest-focus-statement");
    target.replaceChildren();
    byID("contest-submission-result").replaceChildren();
    const problem = view?.problems.find((item) => item.problem_id === Number(problemID));
    if (!problem) {
      target.append(node("p", "请从“比赛题库”选择一道题。", "profile-note"));
      byID("contest-problem-prev").disabled = true;
      byID("contest-problem-next").disabled = true;
      updateContestSubmissionAvailability();
      return;
    }
    if (byID("contest-problem-select").value !== String(problem.problem_id)) byID("contest-problem-select").value = String(problem.problem_id);
    const index = view.problems.findIndex((item) => item.problem_id === problem.problem_id);
    const state = contestProblemState(problem.problem_id);
    const status = node("span", state.label, `contest-focus-state contest-answer-${state.key}`);
    target.append(status);
    appendContestStatement(target, problem);
    byID("contest-problem-prev").disabled = index <= 0;
    byID("contest-problem-next").disabled = index < 0 || index >= view.problems.length - 1;
    contestLanguageControls();
    updateContestSubmissionAvailability();
  }
  function focusContestProblem(problemID) {
    if (!view?.problems.some((problem) => problem.problem_id === Number(problemID))) return;
    byID("contest-problem-select").value = String(problemID);
    activePanel = "submit";
    switchPanel("submit");
    renderFocusedProblem(problemID);
  }
  function updateContestSubmissionAvailability() {
    if (!selected || !view) return;
    const now = new Date();
    const inWindow = new Date(selected.starts_at) <= now && now < new Date(selected.ends_at);
    const member = contestTeamIDs(true).length > 0;
    const hasProblem = view.problems.length > 0;
    const disabled = !inWindow || !member || !hasProblem;
    byID("contest-submit-button").disabled = disabled;
    byID("contest-submit-state").textContent = new Date(selected.ends_at) <= now
      ? "比赛已结束，可以查看题面和本人成绩，但不能再提交。"
      : new Date(selected.starts_at) > now
        ? "比赛尚未开始，题面与提交将在开始后开放。"
        : !member
          ? "当前账号不在本场比赛名单中，不能提交。"
          : !hasProblem
            ? "本比赛尚未绑定可提交题目。"
            : "正式提交会进入比赛记录并参与计分。";
  }
  function renderDetail() {
    const contest = view.contest;
    document.title = `${contest.title} · Biue OJ`;
    byID("contest-title").textContent = contest.title;
    byID("contest-info").textContent = `#${contest.id} · ${contest.scoring_mode} · ${date(contest.starts_at)} — ${date(contest.ends_at)}`;
    byID("contest-export").href = `/api/contests/${contest.id}/export.csv`;
    for (const item of document.querySelectorAll("[data-contest-manager]")) item.hidden = !view.manager;
    byID("contest-package-nav").hidden = user?.global_role !== "super_admin";
    byID("contest-package-access").hidden = user?.global_role === "super_admin";
    const enrollAvailable = contest.enrollment_policy === "open" && !contest.team_mode && !contest.settled_at && new Date(contest.starts_at) > new Date();
    byID("contest-enroll").hidden = !enrollAvailable;
    renderOverview(contest);
    byID("contest-problems").replaceChildren();
    byID("contest-problem-select").replaceChildren();
    for (const problem of view.problems) {
      const article = node("article", undefined, "contest-problem-card");
      const heading = node("div", undefined, "contest-problem-heading");
      const state = contestProblemState(problem.problem_id);
      heading.append(node("h3", `${problem.problem_id}. ${problem.statement.title || "题目"}`), node("span", state.label, `contest-answer-${state.key}`));
      const focus = node("button", "进入单题答题", "button secondary");
      focus.type = "button";
      focus.addEventListener("click", () => focusContestProblem(problem.problem_id));
      heading.append(focus);
      article.append(heading);
      appendContestStatement(article, problem);
      byID("contest-problems").append(article);
      const option = node("option", `${problem.problem_id}. ${problem.statement.title || "题目"}`);
      option.value = problem.problem_id;
      byID("contest-problem-select").append(option);
    }
    renderAnswerSheet();
    renderFocusedProblem();
    contestLanguageControls();
    renderRoster();
    const teamIDs = new Set(contestTeamIDs());
    byID("contest-my-status").textContent = teamIDs.size
      ? `你在本比赛中的身份：${view.teams.filter((team) => teamIDs.has(team.id)).map((team) => `${team.name}（${team.participant_type} / ${team.status}）`).join("，")}`
      : contest.team_mode ? "本比赛为管理员组队；如名单没有你，请联系比赛管理员。" : "当前账号尚未报名。";
    byID("contest-identity-link").href = view.manager ? "/admin/identities" : "/me";
    renderLiveEdit();
    switchPanel(activePanel);
  }
  function renderLiveEdit() {
    if (!selected) return;
    const form = byID("contest-live-edit-form");
    const fields = form.elements;
    fields.title.value = selected.title;
    fields.scoring_mode.value = selected.scoring_mode;
    fields.enrollment_policy.value = selected.enrollment_policy;
    fields.starts_at.value = localTime(selected.starts_at);
    fields.ends_at.value = localTime(selected.ends_at);
    fields.approval_required.checked = selected.approval_required;
    fields.team_mode.checked = selected.team_mode;
    fields.change_reason.value = "";
    const started = new Date(selected.starts_at) <= new Date();
    const settled = !!selected.settled_at;
    fields.scoring_mode.disabled = started || settled;
    fields.team_mode.disabled = started || settled;
    for (const field of fields) {
      if (field !== fields.scoring_mode && field !== fields.team_mode) field.disabled = settled;
    }
    byID("contest-live-mode-help").textContent = started ? "比赛已开始，计分模式和团队模式已锁定。" : "比赛开始前仍可调整计分模式。";
    byID("contest-live-impact").textContent = !started
      ? "当前仍是赛前，可普通编辑；开始后每次修改必须填写原因。"
      : settled
        ? "比赛已结算，设置已锁定。"
        : "状态为进行中或已结束但未结算。修改结束时间会立即影响提交准入与结算资格，请核对作用对象后保存。";
  }
  async function loadDetail(id) {
    if (!Number.isSafeInteger(Number(id)) || Number(id) < 1) throw new APIError("比赛编号无效。", 400);
    const sequence = ++detailEpoch;
    detailLoading = true;
    setDetailBusy(true);
    for (const button of document.querySelectorAll(".contest-nav-item")) button.disabled = true;
    byID("contest-detail").hidden = false;
    byID("contest-center").hidden = true;
    byID("contest-title").textContent = `比赛 #${id}`;
    message("contest-detail-message", "正在读取比赛详情…", true);
    invalidateCSV("切换比赛后，请重新生成 CSV 预览。");
    clearPackageState("切换比赛后，请重新选择题包。");
    try {
      const result = await api(`/api/contests/${encodeURIComponent(id)}`);
      if (sequence !== detailEpoch || result.contest.id !== Number(id)) return null;
      view = result;
      selected = result.contest;
      byID("contest-detail-message").hidden = true;
      renderDetail();
      return result;
    } catch (error) {
      if (sequence === detailEpoch) {
        message("contest-detail-message", error.status === 404 ? "比赛不存在或当前账号不可见。" : error.message);
        view = null;
        selected = null;
      }
      throw error;
    } finally {
      if (sequence === detailEpoch) {
        detailLoading = false;
        setDetailBusy(!selected);
        if (selected) {
          renderLiveEdit();
          updateContestSubmissionAvailability();
        }
        byID("contest-csv-confirm").disabled = !csvState;
        for (const button of document.querySelectorAll(".contest-nav-item")) button.disabled = false;
      }
    }
  }
  async function openContest(id, replace = false) {
    if (!mayDiscard()) return;
    const path = `/contests/${id}`;
    if (replace) history.replaceState({ contest: id }, "", path);
    else history.pushState({ contest: id }, "", path);
    activePanel = "overview";
    try { await loadDetail(id); } catch {}
  }
  async function list(more = false) {
    if (!more) {
      listBefore = 0;
      byID("contest-list").replaceChildren();
    }
    const items = await api(`/api/contests?before=${listBefore}`);
    for (const contest of items) {
      const card = node("article", undefined, "contest-list-item");
      const copy = node("div");
      const now = new Date();
      const state = contest.settled_at ? "已结束 · 已结算" : new Date(contest.ends_at) <= now ? "已结束" : new Date(contest.starts_at) > now ? "赛前" : "进行中";
      const stateKey = contest.settled_at ? "settled" : new Date(contest.ends_at) <= now ? "ended" : new Date(contest.starts_at) > now ? "upcoming" : "live";
      const meta = node("div", undefined, "contest-list-meta");
      meta.append(node("span", `#${contest.id} · ${contest.scoring_mode}`, "problem-number"), node("span", state, `contest-state contest-state-${stateKey}`));
      copy.append(meta, node("h3", contest.title), node("p", `${date(contest.starts_at)} — ${date(contest.ends_at)}`));
      const button = node("button", "进入比赛", "button primary");
      button.type = "button";
      button.addEventListener("click", () => run(button, () => openContest(contest.id), "contest-message"));
      card.append(copy, button);
      byID("contest-list").append(card);
      listBefore = contest.id;
    }
    if (!items.length && !more) byID("contest-list").append(node("p", "暂时没有可见比赛。", "problem-empty"));
    byID("contest-more").hidden = items.length < 100;
  }

  function option(value, label) {
    const element = node("option", label);
    element.value = String(value);
    return element;
  }
  function selectedUserLabel(person) {
    return `${person.username} (#${person.id}) · ${person.display_name} · ${person.student_no || "无学号"} · ${person.account_type}`;
  }
  function populateManageOptions(options) {
    const problemSelect = byID("contest-problem-option");
    problemSelect.replaceChildren();
    const problems = new Map();
    for (const problem of view?.problems || []) problems.set(problem.problem_id, { id: problem.problem_id, title: problem.statement.title || "题目", statement_version: problem.statement_version, data_version: problem.data_version });
    for (const problem of options.problems || []) problems.set(problem.id, problem);
    for (const problem of [...problems.values()].sort((a, b) => a.id - b.id)) problemSelect.append(option(problem.id, `#${problem.id} ${problem.title} · 题面 v${problem.statement_version} / 数据 v${problem.data_version}`));
    const userSelect = byID("contest-user-option");
    userSelect.replaceChildren();
    for (const person of options.users || []) userSelect.append(option(person.id, selectedUserLabel(person)));
    const adminSelect = byID("contest-admin-option");
    adminSelect.replaceChildren();
    for (const person of options.admins || []) adminSelect.append(option(person.id, selectedUserLabel(person)));
    const memberSelect = byID("contest-member-option");
    memberSelect.replaceChildren();
    for (const person of options.users || []) memberSelect.append(option(person.id, selectedUserLabel(person)));
    const teamSelect = byID("contest-team-option");
    teamSelect.replaceChildren();
    const bulk = byID("contest-bulk-teams");
    bulk.replaceChildren(node("legend", "批量作用队伍"));
    for (const team of view?.teams || []) {
      teamSelect.append(option(team.id, `#${team.id} ${team.name} · ${team.status} / ${team.participant_type}`));
      const label = node("label");
      const checkbox = node("input");
      checkbox.type = "checkbox";
      checkbox.value = String(team.id);
      label.append(checkbox, document.createTextNode(` #${team.id} ${team.name}`));
      bulk.append(label);
    }
    syncManageFields();
  }
  async function loadManageOptions(query = "") {
    if (!view?.manager || !selected) return;
    const sequence = ++manageEpoch;
    const id = selected.id;
    const result = await api(`/api/contests/${id}/manage-options?q=${encodeURIComponent(query.trim())}`);
    if (sequence === manageEpoch && selected?.id === id) populateManageOptions(result);
  }
  function syncManageFields() {
    if (!selected) return;
    const action = byID("contest-manage-action").value;
    for (const group of document.querySelectorAll("[data-manage-fields]")) group.hidden = !group.dataset.manageFields.split(" ").includes(action);
    byID("contest-bulk-teams").hidden = action !== "bulk_status";
    byID("contest-manage-problem-id").value = byID("contest-problem-option").value || "";
    byID("contest-manage-team-id").value = byID("contest-team-option").value || "";
    const userID = action === "revoke_admin" ? byID("contest-admin-option").value : byID("contest-user-option").value;
    byID("contest-manage-user-id").value = userID || "";
    byID("contest-manage-members").value = [...byID("contest-member-option").selectedOptions].map((item) => item.value).join(",");
    byID("contest-manage-team-ids").value = [...byID("contest-bulk-teams").querySelectorAll('input[type="checkbox"]:checked')].map((item) => item.value).join(",");
    const labels = {
      add_problem: "会把所选题目的当前题面版本与数据版本固定到本比赛；进行中允许应急补题，结束后拒绝。",
      remove_problem: "会从本比赛移除所选题目；只允许赛前执行。",
      add_team: "会创建队伍并绑定所选人员；进行中允许应急补录，结束后拒绝。",
      team_status: "会改变所选队伍的状态和正式/打星类型。",
      bulk_status: "会批量改变已勾选队伍的状态和类型，请核对名单。",
      grant_admin: "会授予所选永久账号本比赛的管理权限。",
      revoke_admin: "会撤销所选账号的本比赛管理权限。",
      freeze: "会保存当前榜单作为封榜快照；普通成员不再看到实时变化。",
      unfreeze: "会结束当前封榜并恢复展示实时榜。",
      retire_account: "会永久停用所选比赛临时账号，且该操作只能对已结束比赛执行。",
    };
    byID("contest-manage-impact").textContent = `作用对象：${selected.title}（#${selected.id}）。${labels[action] || ""}`;
  }
  function confirmManage(action) {
    const risks = { bulk_status: "批量队伍状态", remove_problem: "移除比赛题目", retire_account: "报废临时账号", freeze: "封存当前榜单", revoke_admin: "撤销管理员" };
    return !risks[action] || window.confirm(`将在“${selected.title}”（#${selected.id}）执行${risks[action]}。确认继续？`);
  }

  function invalidateCSV(text = "") {
    ++csvEpoch;
    csvState = null;
    byID("contest-csv-confirm").disabled = true;
    byID("contest-csv-preview").replaceChildren();
    if (text) byID("contest-csv-preview").append(node("p", text, "field-help"));
  }
  function renderCSVPreview(state, preview) {
    const target = byID("contest-csv-preview");
    target.replaceChildren(node("h3", `预览所属：${state.title}（#${state.contestID}）`));
    target.append(node("p", `${preview.rows.length} 行 · ${preview.valid ? "可以导入" : "存在错误"} · 令牌仅绑定此比赛和此 CSV 内容`));
    if (preview.columns?.length) target.append(node("p", `已识别列：${preview.columns.map((column) => `${column.source} → ${column.field}`).join("；")}`, "field-help"));
    const credentials = preview.rows.filter((row) => row.new_account && row.initial_password);
    state.credentials = credentials;
    for (const row of preview.rows) {
      const identity = `${row.display_name || "—"} / ${row.student_no || "无学号"}`;
      const account = row.new_account ? "将创建比赛临时账号" : "匹配已有永久账号";
      target.append(node("p", `第 ${row.line} 行 · ${row.username} · ${identity} · ${row.team} · ${row.participant_type} · ${account} · ${row.error || "可导入"}`));
    }
    if (credentials.length) {
      const heading = node("div", undefined, "credential-result-heading");
      heading.append(node("h3", `新比赛账号 ${credentials.length} 个`), node("p", "初始密码只在此预览响应中返回；刷新或重新预览后旧密码无法再次取得。请确认前复制或下载。", "field-help"));
      const copy = node("button", "复制账号密码", "button secondary");
      copy.type = "button";
      const textarea = node("textarea");
      textarea.readOnly = true;
      textarea.rows = Math.min(10, credentials.length + 1);
      textarea.value = ["账号,初始密码,姓名,学号,队伍", ...credentials.map((row) => [row.username, row.initial_password, row.display_name, row.student_no, row.team].join(","))].join("\n");
      copy.addEventListener("click", () => {
        textarea.select();
        if (navigator.clipboard?.writeText) navigator.clipboard.writeText(textarea.value).then(() => message("contest-detail-message", "比赛账号和初始密码已复制。", true)).catch(() => textarea.select());
      });
      heading.append(copy);
      target.append(heading, textarea);
    }
  }
  function renderSettleTarget() {
    if (!selected) return;
    byID("contest-settle-target").textContent = `作用比赛：${selected.title}（#${selected.id}）· 结束于 ${date(selected.ends_at)} · ${selected.settled_at ? `已于 ${date(selected.settled_at)} 结算，重复请求只返回既有快照` : "当前未结算"}`;
  }
  function setEditMode(editing) {
    if (!editing && editingMode && dirty && !window.confirm("编辑内容尚未保存。切换会保留当前标签页草稿；是否继续？")) {
      createForm.elements.edit_current.checked = true;
      return;
    }
    if (editing && (!selected || new Date(selected.starts_at) <= new Date() || selected.settled_at)) {
      byID("contest-edit-current").checked = false;
      message("contest-detail-message", "只有尚未开始且未结算的比赛可以编辑。");
      return;
    }
    createForm.elements.edit_current.checked = editing;
    editingMode = editing;
    byID("contest-create-submit").textContent = editing ? "保存比赛修改" : "创建比赛";
    createForm.elements.team_mode.disabled = editing;
    if (!editing) {
      createForm.reset();
      createForm.elements.starts_at.value = localTime(new Date(Date.now() + 15 * 60000));
      createForm.elements.duration_minutes.value = "120";
      createForm.elements.end_mode.value = "duration";
      restoreDraft();
      savedSnapshot = formSnapshot();
      dirty = false;
      previewEnd();
      return;
    }
    for (const [key, value] of Object.entries(selected)) {
      const field = createForm.elements.namedItem(key);
      if (!field) continue;
      if (field.type === "checkbox") field.checked = !!value;
      else if (field.type === "datetime-local") field.value = localTime(value);
      else field.value = value;
    }
    const minutes = (new Date(selected.ends_at) - new Date(selected.starts_at)) / 60000;
    createForm.elements.duration_minutes.value = Number.isInteger(minutes) ? minutes : "";
    createForm.elements.end_mode.value = Number.isInteger(minutes) ? "duration" : "manual";
    restoreDraft();
    savedSnapshot = formSnapshot();
    dirty = false;
    previewEnd();
  }

  createForm.elements.starts_at.value = localTime(new Date(Date.now() + 15 * 60000));
  byID("contest-time-help").textContent = `浏览器时区：${Intl.DateTimeFormat().resolvedOptions().timeZone}。开始时间须晚于服务器当前时间，结束须晚于开始，最长 30 天；提交时仍会按服务器时间复核。`;
  previewEnd();
  savedSnapshot = formSnapshot();
  createForm.addEventListener("input", () => { clearCreateErrors(); previewEnd(); markDirty(); });
  createForm.addEventListener("change", () => { clearCreateErrors(); previewEnd(); markDirty(); });
  createForm.addEventListener("submit", (event) => {
    event.preventDefault();
    run(event.submitter || createForm.querySelector("button"), async () => {
      const input = contestInput();
      const editing = createForm.elements.edit_current.checked;
      const id = editing ? selected.id : 0;
      const inputKey = JSON.stringify(input);
      if (!editing && lastCreation?.key === inputKey) {
        if (lastCreation.id) {
          await openContest(lastCreation.id);
          return;
        }
        if (!window.confirm("上次创建结果尚未确认。请先查看比赛列表；确认没有成功后再重试。已核对并继续？")) {
          throw new APIError("已保留表单，请先核对比赛列表。", 409);
        }
      }
      if (!editing) rememberCreation({ key: inputKey });
      let data;
      try {
        data = await api(editing ? `/api/contests/${id}` : "/api/contests", editing ? "PUT" : "POST", input);
      } catch (error) {
        if (error.fields) createErrors(error.fields);
        if (!editing && error.status >= 400 && error.status < 500 && !lastCreation?.id) rememberCreation(null);
        if (!editing && error.status === 0) error.message += "；请先核对比赛列表，勿直接重复创建。";
        if (editing && error.status === 0) {
          await loadDetail(id).catch(() => {});
          error.message += "；已重新读取服务器状态，请核对后再决定是否重试。";
        }
        throw error;
      }
      if (!editing) rememberCreation({ key: inputKey, id: data.id });
      dirty = false;
      clearDraft();
      byID("contest-create").hidden = true;
      message("contest-message", editing ? "比赛修改已保存。" : "比赛已创建。", true);
      await openContest(data.id);
    }, "contest-form-error");
  });
  byID("contest-create-toggle").addEventListener("click", (event) => { event.preventDefault(); setEditMode(false); showCreate(); });
  byID("contest-create-close").addEventListener("click", () => { if (mayDiscard()) switchCenterWorkspace("list"); });
  byID("contest-nav-list").addEventListener("click", () => { if (mayDiscard()) switchCenterWorkspace("list"); });
  byID("contest-nav-create").addEventListener("click", () => { setEditMode(false); showCreate(); });
  byID("contest-edit-current").addEventListener("change", (event) => setEditMode(event.target.checked));
  byID("contest-live-edit-form").addEventListener("submit", (event) => {
    event.preventDefault();
    if (!selected) return;
    const fields = event.target.elements;
    const start = new Date(fields.starts_at.value);
    const end = new Date(fields.ends_at.value);
    if (!Number.isFinite(start.getTime()) || !Number.isFinite(end.getTime()) || end <= start) {
      message("contest-detail-message", "开始和结束时间无效，结束时间必须晚于开始时间。");
      return;
    }
    if (end - start > 30 * 86400000) {
      message("contest-detail-message", "比赛持续时间不能超过 30 天。");
      return;
    }
    const started = new Date(selected.starts_at) <= new Date();
    if (started && !fields.change_reason.value.trim()) {
      message("contest-detail-message", "比赛开始后修改设置必须填写变更原因。");
      fields.change_reason.focus();
      return;
    }
    const timeChanged = start.getTime() !== new Date(selected.starts_at).getTime() || end.getTime() !== new Date(selected.ends_at).getTime();
    if (timeChanged && !window.confirm(`将修改“${selected.title}”（#${selected.id}）的比赛时间。这会立即影响提交准入和结算，确认保存？`)) return;
    run(event.submitter || event.target.querySelector("button"), async () => {
      const id = selected.id;
      const input = {
        title: fields.title.value.trim(),
        scoring_mode: selected.scoring_mode,
        enrollment_policy: fields.enrollment_policy.value,
        starts_at: start.toISOString(),
        ends_at: end.toISOString(),
        approval_required: fields.approval_required.checked,
        team_mode: selected.team_mode,
        revision: selected.revision,
        change_reason: fields.change_reason.value.trim(),
      };
      try {
        await api(`/api/contests/${id}`, "PUT", input);
      } catch (error) {
        if (error.fields) {
          const labels = { title: "名称", scoring_mode: "计分模式", enrollment_policy: "报名方式", starts_at: "开始时间", ends_at: "结束时间", team_mode: "团队模式", change_reason: "变更原因" };
          error.message = Object.entries(error.fields).map(([key, value]) => `${labels[key] || key}：${value}`).join("；") + (error.requestID ? `（请求 ID：${error.requestID}）` : "");
        }
        throw error;
      }
      await loadDetail(id);
      activePanel = "edit";
      switchPanel("edit");
      message("contest-detail-message", `“${selected.title}”（#${selected.id}）设置已保存，当前仍在比赛工作区。`, true);
    }, "contest-detail-message");
  });
  byID("contest-more").addEventListener("click", (event) => run(event.target, () => list(true), "contest-message"));
  byID("contest-refresh").addEventListener("click", (event) => run(event.target, () => selected && loadDetail(selected.id), "contest-detail-message"));
  byID("contest-package-file").addEventListener("change", (event) => {
    const file = event.target.files?.[0];
    if (!file) return;
    if (file.size > 32 * 1024 * 1024) {
      clearPackageState("题包 ZIP 最大 32 MiB。");
      return;
    }
    packageFile = file;
    packageSource = "upload";
    run(event.target, () => previewContestPackage(false), "contest-package-message");
  });
  byID("contest-package-sample").addEventListener("click", (event) => {
    packageFile = null;
    packageSource = "builtin";
    run(event.target, () => previewContestPackage(true), "contest-package-message");
  });
  byID("contest-package-confirm").addEventListener("click", (event) => {
    run(event.target, importContestPackage, "contest-package-message");
  });
  byID("contest-enroll").addEventListener("click", (event) => run(event.target, async () => {
    if (!selected) return;
    const id = selected.id;
    await api(`/api/contests/${id}/enroll`, "POST", {});
    await loadDetail(id);
    message("contest-detail-message", `已提交“${selected.title}”（#${id}）的报名。`, true);
  }, "contest-detail-message"));
  for (const button of document.querySelectorAll("[data-contest-panel]")) button.addEventListener("click", () => switchPanel(button.dataset.contestPanel));
  byID("contest-back").addEventListener("click", (event) => {
    event.preventDefault();
    if (!mayDiscard()) return;
    history.pushState({}, "", "/contests");
    showCenter();
  });
  window.addEventListener("popstate", () => {
    const id = Number(window.location.pathname.split("/")[2]);
    if (Number.isSafeInteger(id) && id > 0) loadDetail(id).catch(() => {});
    else showCenter();
  });
  byID("contest-problem-select").addEventListener("change", () => renderFocusedProblem());
  byID("contest-problem-list").addEventListener("click", () => switchPanel("problems"));
  byID("contest-problem-prev").addEventListener("click", () => {
    const index = view?.problems.findIndex((problem) => problem.problem_id === Number(byID("contest-problem-select").value)) ?? -1;
    if (index > 0) renderFocusedProblem(view.problems[index - 1].problem_id);
  });
  byID("contest-problem-next").addEventListener("click", () => {
    const index = view?.problems.findIndex((problem) => problem.problem_id === Number(byID("contest-problem-select").value)) ?? -1;
    if (index >= 0 && index + 1 < view.problems.length) renderFocusedProblem(view.problems[index + 1].problem_id);
  });
  byID("contest-submit-form").elements.language.addEventListener("change", () => contestSourceEditor?.setLanguage(byID("contest-submit-form").elements.language.value));
  for (const id of ["contest-search", "contest-filter-type", "contest-filter-status", "contest-filter-account", "contest-filter-submissions"]) byID(id).addEventListener("input", renderRoster);
  byID("contest-manage-action").addEventListener("change", syncManageFields);
  for (const id of ["contest-problem-option", "contest-user-option", "contest-admin-option", "contest-team-option"]) byID(id).addEventListener("change", syncManageFields);
  byID("contest-member-option").addEventListener("change", syncManageFields);
  byID("contest-bulk-teams").addEventListener("change", syncManageFields);
  let manageQueryTimer = 0;
  for (const id of ["contest-problem-query", "contest-user-query"]) byID(id).addEventListener("input", () => {
    clearTimeout(manageQueryTimer);
    manageQueryTimer = setTimeout(() => {
      const query = byID("contest-problem-query").value || byID("contest-user-query").value;
      loadManageOptions(query).catch((error) => message("contest-detail-message", error.message));
    }, 250);
  });
  byID("contest-manage-form").addEventListener("submit", (event) => {
    event.preventDefault();
    const action = byID("contest-manage-action").value;
    if (!confirmManage(action)) return;
    run(event.submitter || event.target.querySelector("button"), async () => {
      const id = selected.id;
      const title = selected.title;
      const data = { action };
      if (action === "add_problem" || action === "remove_problem") data.problem_id = Number(byID("contest-manage-problem-id").value);
      if (["grant_admin", "revoke_admin", "retire_account"].includes(action)) data.user_id = Number(byID("contest-manage-user-id").value);
      if (["team_status", "bulk_status"].includes(action)) {
        data.status = event.target.elements.status.value;
        data.participant_type = event.target.elements.participant_type.value;
      }
      if (action === "team_status") data.team_id = Number(byID("contest-manage-team-id").value);
      if (action === "bulk_status") data.team_ids = byID("contest-manage-team-ids").value.split(",").filter(Boolean).map(Number);
      if (action === "add_team") {
        data.name = event.target.elements.name.value;
        data.members = [...byID("contest-member-option").selectedOptions].map((item) => Number(item.value));
        data.status = event.target.elements.status.value;
        data.participant_type = event.target.elements.participant_type.value;
      }
      try {
        await api(`/api/contests/${id}/manage`, "POST", data);
      } catch (error) {
        if (error.status === 0) {
          await loadDetail(id).catch(() => {});
          error.message += "；已重新读取比赛状态，请核对后再决定是否重试。";
        }
        throw error;
      }
      await loadDetail(id);
      message("contest-detail-message", `已在“${title}”（#${id}）完成${byID("contest-manage-action").selectedOptions[0].textContent}。`, true);
    }, "contest-detail-message");
  });
  byID("contest-rejudge").addEventListener("click", (event) => {
    if (!selected) return;
    run(event.target, async () => {
      const key = typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
        ? "contest-" + crypto.randomUUID() : `contest-${Date.now()}-${Math.random().toString(36).slice(2, 12)}`;
      const result = await api("/api/admin/operations/rejudge/contest", "POST", {
        key, reason: "从比赛工作区一键重测未结算提交", contest_id: selected.id,
      });
      message("contest-rejudge-result", `已排队 ${result.queued} 条，跳过 ${result.skipped} 条，共 ${result.batches} 个批次。${(result.warnings || []).join("；")}`, result.complete !== false);
    }, "contest-rejudge-result");
  });
  byID("contest-csv-file").addEventListener("change", async (event) => {
    const file = event.target.files[0];
    if (!file) return;
    if (file.size > 256 * 1024) { message("contest-detail-message", "CSV 最多 256 KiB。"); return; }
    byID("contest-csv-form").elements.csv.value = await file.text();
    invalidateCSV("CSV 文件已载入；请重新生成预览。");
  });
  byID("contest-csv-form").elements.csv.addEventListener("input", () => invalidateCSV("CSV 内容已变化；旧预览和确认令牌已失效。"));
  byID("contest-csv-form").addEventListener("submit", (event) => {
    event.preventDefault();
    run(event.submitter || event.target.querySelector("button"), async () => {
      const id = selected.id;
      const csv = event.target.elements.csv.value;
      const sequence = ++csvEpoch;
      const preview = await api(`/api/contests/${id}/csv/preview`, "POST", { csv });
      if (sequence !== csvEpoch || !selected || selected.id !== id) return;
      csvState = { contestID: id, title: selected.title, csv, token: preview.token, rows: preview.rows, valid: preview.valid, credentials: [] };
      renderCSVPreview(csvState, preview);
      byID("contest-csv-confirm").disabled = !preview.valid;
      message("contest-detail-message", preview.valid ? "预览已生成，确认按钮仅在当前比赛和当前 CSV 未变化时有效。" : "预览包含错误，请修改后重新生成。", preview.valid);
    }, "contest-detail-message");
  });
  byID("contest-csv-confirm").addEventListener("click", (event) => {
    run(event.target, async () => {
      if (!csvState || !selected || csvState.contestID !== selected.id || csvState.csv !== byID("contest-csv-form").elements.csv.value) {
        invalidateCSV("预览已失效，请重新生成。");
        throw new APIError("不能确认旧预览。", 409);
      }
      const state = csvState;
      byID("contest-csv-confirm").disabled = true;
      const result = await api(`/api/contests/${state.contestID}/csv/confirm`, "POST", { token: state.token });
      csvState = null;
      await loadDetail(state.contestID);
      message("contest-detail-message", `名单已导入“${state.title}”（#${state.contestID}），新建 ${result?.created_accounts || 0} 个比赛账号。`, true);
    }, "contest-detail-message").finally(() => { byID("contest-csv-confirm").disabled = !csvState; });
  });
  byID("contest-temporary-form").addEventListener("submit", (event) => {
    event.preventDefault();
    run(event.submitter || event.target.querySelector("button"), async () => {
      const id = selected.id;
      const data = Object.fromEntries(new FormData(event.target));
      await api(`/api/contests/${id}/temporary-accounts`, "POST", data);
      event.target.reset();
      await loadDetail(id);
      message("contest-detail-message", "临时账号已创建并加入比赛；密码不会保存在浏览器草稿中。", true);
    }, "contest-detail-message");
  });
  byID("contest-history-form").addEventListener("submit", (event) => {
    event.preventDefault();
    run(event.submitter || event.target.querySelector("button"), async () => {
      const data = Object.fromEntries(new FormData(event.target));
      const records = await api(`/api/contests/${selected.id}/submissions?user_id=${encodeURIComponent(data.user_id)}`);
      byID("contest-history").replaceChildren();
      for (const submission of records) {
        const paragraph = node("p");
        const link = node("a", `#${submission.id} · ${submission.kind} · ${submission.status} · ${submission.score} 分`);
        link.href = `/submissions/${submission.id}`;
        paragraph.append(link);
        byID("contest-history").append(paragraph);
      }
    }, "contest-detail-message");
  });
  byID("contest-submit-form").addEventListener("submit", (event) => {
    event.preventDefault();
    run(event.submitter || event.target.querySelector("button"), async () => {
      const id = selected.id;
      const data = Object.fromEntries(new FormData(event.target));
      data.contest_id = id;
      data.problem_id = Number(data.problem_id);
      const submission = await api("/api/submissions", "POST", data);
      await loadDetail(id);
      activePanel = "submit";
      switchPanel("submit");
      const link = node("a", `查看提交 #${submission.id}`);
      link.href = `/submissions/${submission.id}`;
      byID("contest-submission-result").replaceChildren(link);
      message("contest-detail-message", `已向“${selected.title}”（#${id}）提交。`, true);
    }, "contest-detail-message");
  });
  byID("contest-settle").addEventListener("click", (event) => {
    if (!selected || !window.confirm(`将结算“${selected.title}”（#${selected.id}）。请确认比赛已结束且全部提交评测完成。继续？`)) return;
    run(event.target, async () => {
      const id = selected.id;
      const title = selected.title;
      try {
        await api(`/api/contests/${id}/settle`, "POST", {});
      } catch (error) {
        if (error.status === 0) {
          await loadDetail(id).catch(() => {});
          error.message += "；已重新读取结算状态，请核对结果后再重试。";
        }
        throw error;
      }
      await loadDetail(id);
      message("contest-detail-message", `“${title}”（#${id}）已结算；重复请求不会重复计入积分。`, true);
    }, "contest-detail-message");
  });
  window.addEventListener("beforeunload", (event) => {
    if (dirty || detailLoading) { event.preventDefault(); event.returnValue = ""; }
  });

  Promise.allSettled([api("/api/users/me"), list()]).then(([accountResult, listResult]) => {
    if (accountResult.status === "fulfilled") user = accountResult.value;
    if (listResult.status === "rejected") message("contest-message", listResult.reason.message);
    else message("contest-message", "请选择比赛进入工作区。");
    const routeID = Number(window.location.pathname.split("/")[2]);
    if (Number.isSafeInteger(routeID) && routeID > 0) {
      history.replaceState({ contest: routeID }, "", `/contests/${routeID}`);
      loadDetail(routeID).catch(() => {});
    } else {
      switchCenterWorkspace((window.location.hash || "").slice(1), false);
    }
  });
})();
