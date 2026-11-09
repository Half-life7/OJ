"use strict";

(() => {
  class APIError extends Error {
    constructor(message, status) {
      super(message);
      this.status = status;
    }
  }

  let csrfToken = "";

  async function request(path, method = "GET", data) {
    const controller = new AbortController();
    const timeout = window.setTimeout(() => controller.abort(), 15000);
    const headers = { Accept: "application/json" };
    if (data !== undefined) headers["Content-Type"] = "application/json";
    if (method !== "GET") headers["X-CSRF-Token"] = csrfToken;
    try {
      const response = await fetch(path, {
        method,
        headers,
        credentials: "same-origin",
        cache: "no-store",
        signal: controller.signal,
        body: data === undefined ? undefined : JSON.stringify(data),
      });
      let result;
      try {
        result = await response.json();
      } catch {
        throw new APIError("服务暂时无法响应，请稍后重试。", response.status);
      }
      if (!response.ok || result.code !== 0) {
        if (response.status === 403) csrfToken = "";
        throw new APIError(result.message || "操作未完成，请稍后重试。", response.status);
      }
      return result.data;
    } catch (error) {
      if (error instanceof APIError) throw error;
      throw new APIError(error.name === "AbortError" ? "请求超时，请稍后重试。" : "网络连接失败，请检查网络后重试。", 0);
    } finally {
      window.clearTimeout(timeout);
    }
  }

  async function write(path, data = {}) {
    if (!csrfToken) {
      const result = await request("/api/auth/csrf");
      csrfToken = result && result.csrf_token;
      if (!csrfToken) throw new APIError("无法验证当前页面，请刷新后重试。", 403);
    }
    return request(path, "POST", data);
  }

  function showMessage(element, message, success = false) {
    element.textContent = message;
    element.classList.toggle("success", success);
    element.hidden = false;
  }

  function validatePassword(data) {
    const size = new TextEncoder().encode(data.password).length;
    if (size < 8 || size > 72) return "密码长度需为 8～72 个 UTF-8 字节。";
    if (data.password !== data.password_confirmation) return "两次输入的密码不一致。";
    return "";
  }

  function bindForm(id, messageID, action) {
    const form = document.getElementById(id);
    if (!form) return;
    const message = document.getElementById(messageID);
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      const button = form.querySelector("button[type=submit]");
      if (button.disabled) return;
      const data = Object.fromEntries(new FormData(form).entries());
      if (data.password_confirmation !== undefined) {
        const error = validatePassword(data);
        if (error) {
          showMessage(message, error);
          return;
        }
      }
      if (id === "password-form" && !window.confirm("更新密码后，所有设备都需要重新登录。确认更新？")) return;
      message.hidden = true;
      button.disabled = true;
      form.setAttribute("aria-busy", "true");
      const previous = button.textContent;
      button.textContent = "正在处理…";
      try {
        await action(data, message);
      } catch (error) {
        showMessage(message, error.message);
      } finally {
        button.disabled = false;
        button.textContent = previous;
        form.removeAttribute("aria-busy");
      }
    });
  }

  bindForm("register-form", "register-message", async (data) => {
    data.username = data.username.trim();
    data.student_no = data.student_no.trim();
    await write("/api/auth/register", data);
    window.location.assign("/login?registered=1");
  });

  bindForm("login-form", "login-message", async (data) => {
    data.username = data.username.trim();
    await write("/api/auth/login", data);
    csrfToken = "";
    window.location.assign("/me");
  });

  bindForm("password-form", "password-message", async (data) => {
    await write("/api/auth/password", data);
    csrfToken = "";
    window.location.assign("/login?password_changed=1");
  });

  if (document.body.dataset.page === "login") {
    const params = new URLSearchParams(window.location.search);
    const message = document.getElementById("login-message");
    if (params.get("registered") === "1") showMessage(message, "账号已创建，请使用账号 ID 和密码登录。", true);
    else if (params.get("password_changed") === "1") showMessage(message, "密码已更新，请重新登录。", true);
    else if (params.get("logged_out") === "1") showMessage(message, "你已安全退出当前会话。", true);
  }

  function setText(id, value) {
    document.getElementById(id).textContent = value === null || value === undefined || value === "" ? "未填写" : String(value);
  }

  async function loadProfile() {
    const state = document.getElementById("profile-state");
    const message = document.getElementById("profile-state-text");
    const login = document.getElementById("profile-login");
    const retry = document.getElementById("profile-retry");
    state.hidden = false;
    state.setAttribute("aria-busy", "true");
    state.querySelector(".loading-dot").hidden = false;
    message.textContent = "正在读取账号信息…";
    login.hidden = true;
    retry.hidden = true;
    try {
      const user = await request("/api/users/me");
      setText("profile-username", user.username);
      setText("profile-display-name", user.display_name);
      setText("profile-account-type", { permanent: "永久账号", contest: "比赛临时账号" }[user.account_type] || user.account_type);
      setText("profile-status", { active: "正常", disabled: "已禁用", retired: "已停用" }[user.status] || user.status);
      setText("profile-role", { user: "普通用户", super_admin: "超级管理员" }[user.global_role] || user.global_role);
      document.getElementById("profile-admin-link").hidden = user.global_role !== "super_admin";
      setText("profile-student-no", user.student_no);
      setText("profile-student-status", { none: "未填写", pending: "待审核", verified: "已验证", rejected: "审核未通过" }[user.student_no_status] || user.student_no_status);
      document.getElementById("profile-content").hidden = false;
      document.getElementById("password-fields").disabled = false;
      document.getElementById("logout-button").hidden = false;
      state.hidden = true;
    } catch (error) {
      state.querySelector(".loading-dot").hidden = true;
      message.textContent = error.status === 401 ? "请先登录，再查看你的账号信息。" : error.message;
      login.hidden = error.status !== 401;
      retry.hidden = error.status === 401;
    } finally {
      state.removeAttribute("aria-busy");
    }
  }

  if (document.body.dataset.page === "me") {
    document.getElementById("profile-retry").addEventListener("click", loadProfile);
    document.getElementById("logout-button").addEventListener("click", async (event) => {
      const button = event.currentTarget;
      button.disabled = true;
      try {
        await write("/api/auth/logout");
        csrfToken = "";
        window.location.assign("/login?logged_out=1");
      } catch (error) {
        showMessage(document.getElementById("logout-message"), error.message);
      } finally {
        button.disabled = false;
      }
    });
    loadProfile();
  }
})();
