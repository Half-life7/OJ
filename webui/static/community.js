"use strict";
(() => {
  const page = document.body.dataset.page;
  if (!["community", "rankings", "problem", "problems"].includes(page)) return;
  const byID = id => document.getElementById(id);
  const element = (tag, value) => { const n = document.createElement(tag); if (value != null) n.textContent = String(value); return n; };
  let disposed = false;
  const pending = new Set();
  window.addEventListener("pagehide", () => { disposed = true; for (const c of pending) c.abort(); });
  async function api(path, method = "GET", body) {
    const headers = { Accept: "application/json" };
    if (method !== "GET") { const token = await api("/api/auth/csrf"); headers["X-CSRF-Token"] = token.csrf_token; headers["Content-Type"] = "application/json"; }
    const c = new AbortController(); pending.add(c); const timer = setTimeout(() => c.abort(), 20000);
    try {
      const r = await fetch(path, { method, headers, credentials: "same-origin", cache: "no-store", signal: c.signal, body: body === undefined ? undefined : JSON.stringify(body) });
      const v = await r.json(); if (!r.ok || v.code) throw Error(v.message || "请求失败"); return v.data;
    } catch (e) { if (e.name === "AbortError") throw Error("请求超时，请刷新状态后再试"); throw e; }
    finally { clearTimeout(timer); pending.delete(c); }
  }
  const message = text => { if (!disposed && byID("learning-message")) byID("learning-message").textContent = text; };
  async function run(button, fn) { button.disabled = true; message("正在处理…"); try { await fn(); message("操作完成"); } catch (e) { message(e.message); } finally { button.disabled = false; } }
  const params = new URLSearchParams(location.search);
  if (page === "problems") { const field = document.querySelector('[name="category"]'); if (field) field.value = params.get("category") || ""; return; }
  if (page === "problem") {
    const id = location.pathname.split("/").pop();
    const links = element("p"); for (const [kind, label] of [["discussion", "讨论这道题"], ["solution", "查看题解"]]) { const a = element("a", label); a.href = `/learning?kind=${kind}&problem_id=${encodeURIComponent(id)}`; links.append(a, document.createTextNode("　")); }
    byID("problem-detail").append(links);
    api(`/api/practice/problems/${id}`).then(v => { if (disposed) return; const n = element("p", `日常通过人数：${v.people} · 通过率：${(v.acceptance_rate * 100).toFixed(1)}% · ${v.solved ? "你已通过" : "尚未通过"}`); n.id = "practice-statistics"; byID("problem-detail").append(n); }).catch(() => {}); return;
  }
  if (page === "rankings") {
    let current = 1;
    async function load() { const b = await api(`/api/leaderboards/${byID("board-form").elements.kind.value}?page=${current}&period=${byID("board-form").elements.period.value}`); if (disposed) return; byID("board-rows").replaceChildren(); for (const r of b.items) { const tr = element("tr"); for (const value of [r.rank, r.username, r.display_name, r.value, new Date(r.reached_at).toLocaleString()]) tr.append(element("td", value)); byID("board-rows").append(tr); } byID("board-meta").textContent = `${b.total} 位参与者 · 第 ${current} 页 · ${new Date(b.built_at).toLocaleString()} 更新`; byID("board-prev").disabled = current === 1; byID("board-next").disabled = current * 50 >= b.total; }
    byID("board-form").onsubmit = e => { e.preventDefault(); current = 1; run(e.submitter, load); };
    byID("board-prev").onclick = e => run(e.target, () => { current = Math.max(1, current - 1); return load(); });
    byID("board-next").onclick = e => run(e.target, () => { current++; return load(); });
    const timer = setInterval(() => { if (!document.hidden) load().catch(e => message(e.message)); }, 30000); window.addEventListener("pagehide", () => clearInterval(timer)); load().then(() => message("只展示三个固定榜单，Run 不计入日常成绩。")).catch(e => message(e.message)); return;
  }
  let user = null, selected = null, editing = false, cursor = 0;
  const filter = byID("learning-filter"), form = byID("learning-form");
  for (const key of ["kind", "problem_id", "contest_id"]) if (params.has(key)) { filter.elements.namedItem(key).value = params.get(key); form.elements.namedItem(key).value = params.get(key); }
  function input() { const d = Object.fromEntries(new FormData(form)); for (const k of ["problem_id", "contest_id", "parent_id"]) d[k] = d[k] ? Number(d[k]) : null; d.problems = d.problems.trim() ? d.problems.split(/[,，]/).map(v => Number(v.trim())) : []; d.revision = editing ? selected.revision : 0; return d; }
  async function list(reset = true) {
    if (reset) { cursor = 0; byID("learning-list").replaceChildren(); }
    const q = new URLSearchParams({ kind: filter.elements.kind.value, after: cursor });
    for (const key of ["problem_id", "contest_id"]) if (filter.elements.namedItem(key).value) q.set(key, filter.elements.namedItem(key).value);
    if (filter.elements.scope.value) q.set(filter.elements.scope.value, "1");
    const data = await api(`/api/community?${q}`); if (disposed) return;
    for (const d of data.items) { const b = element("button", `#${d.id} ${d.title} · ${d.username} · ${d.review_status} · ${d.likes} 赞 / ${d.favorites} 收藏`); b.type = "button"; b.className = "button secondary"; b.onclick = () => run(b, () => open(d.id)); byID("learning-list").append(b); }
    cursor = data.next; byID("learning-more").hidden = !cursor;
  }
  async function open(id) {
    const d = await api(`/api/community/${id}`); if (disposed) return; selected = d; editing = false;
    byID("learning-detail").hidden = false; byID("learning-title").textContent = d.title; byID("learning-meta").textContent = `${d.username} · ${d.visibility} · ${d.review_status} · 修订 ${d.revision}`;
    // html is rendered and sanitized on the server; all other fields use text nodes.
    byID("learning-html").innerHTML = d.html;
    byID("learning-problems").replaceChildren(); for (const p of d.problems || []) { const li = element("li"); const a = element("a", p.title); a.href = `/problems/${p.id}`; li.append(a); byID("learning-problems").append(li); }
    const owner = user && user.id === d.owner_id, admin = user && user.global_role === "super_admin";
    byID("learning-edit").hidden = !owner; byID("learning-delete").hidden = !user;
    for (const action of ["approve", "reject"]) byID(`learning-${action}`).hidden = !admin || d.kind !== "solution";
    for (const action of ["favorite", "unfavorite"]) byID(`learning-${action}`).hidden = d.kind !== "list" || !user;
    for (const action of ["like", "unlike", "reply"]) byID(`learning-${action}`).hidden = d.kind !== "discussion" || !user;
    byID("learning-replies").replaceChildren(); if (d.kind === "discussion") { const replies = await api(`/api/community?kind=discussion&parent_id=${d.id}`); for (const reply of replies.items) { const b = element("button", `${reply.username}：${reply.title}`); b.type = "button"; b.onclick = () => run(b, () => open(reply.id)); byID("learning-replies").append(b); } }
  }
  filter.onsubmit = e => { e.preventDefault(); run(e.submitter, () => list()); };
  byID("learning-more").onclick = e => run(e.target, () => list(false));
  byID("learning-new").onclick = () => { editing = false; form.reset(); byID("learning-editor-title").textContent = "发布内容"; };
  byID("learning-edit").onclick = () => { editing = true; for (const key of ["kind", "title", "body", "visibility", "problem_id", "contest_id", "parent_id"]) form.elements.namedItem(key).value = selected[key] ?? ""; form.elements.problems.value = (selected.problems || []).map(p => p.id).join(","); byID("learning-editor-title").textContent = `编辑 #${selected.id}`; };
  byID("learning-reply").onclick = () => { editing = false; form.reset(); form.elements.kind.value = "discussion"; form.elements.title.value = `回复：${selected.title}`.slice(0, 200); for (const key of ["problem_id", "contest_id", "visibility"]) form.elements.namedItem(key).value = selected[key] ?? ""; form.elements.parent_id.value = selected.id; };
  form.onsubmit = e => { e.preventDefault(); run(e.submitter, async () => { const r = await api(editing ? `/api/community/${selected.id}` : "/api/community", editing ? "PUT" : "POST", input()); await list(); await open(r.id); }); };
  for (const action of ["favorite", "unfavorite", "like", "unlike", "delete", "approve", "reject"]) byID(`learning-${action}`).onclick = e => run(e.target, async () => { await api(`/api/community/${selected.id}/actions`, "POST", { action, revision: selected.revision }); if (action === "delete") { byID("learning-detail").hidden = true; selected = null; editing = false; } else await open(selected.id); await list(); });
  api("/api/users/me").then(a => { user = a; }).catch(() => {}).then(() => list()).then(() => message("请选择内容，或使用下方表单发布。")).catch(e => message(e.message));
})();
