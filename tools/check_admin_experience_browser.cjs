// Admin hierarchy, editors, live contest editing, CSV aliases and package import.
const { chromium } = require(require("path").resolve(".cache/browser/node_modules/playwright"));
const fs = require("fs");
const assert = require("assert/strict");

const env = Object.fromEntries(fs.readFileSync(".cache/2d-acceptance.env", "utf8").split(/\r?\n/)
  .filter((line) => line.includes("=") && !line.startsWith("#"))
  .map((line) => {
    const index = line.indexOf("=");
    return [line.slice(0, index), line.slice(index + 1)];
  }));
const local = (date) => new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 16);

(async () => {
  const browser = await chromium.launch({
    executablePath: "C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe",
    headless: true,
  });
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.goto(env.PUBLIC_URL + "/login");
    await page.locator("#login-username").fill("admin");
    await page.locator("#login-password").fill(env.BIUE_SMOKE_PASSWORD);
    await page.locator("#login-form button").click();
    await page.waitForURL("**/me");

    await page.goto(env.PUBLIC_URL + "/admin");
    await page.locator("#operations-content").waitFor({ state: "visible" });
    assert.equal(await page.locator("[data-operations-panel]").count(), 5);
    assert.equal(await page.locator("[data-operations-panel-content]:visible").count(), 1);
    await page.locator("[data-operations-panel='test']").click();
    await page.locator("[data-operations-panel-content='test']").waitFor({ state: "visible" });
    assert.equal(await page.locator("[data-operations-panel-content]:visible").count(), 1);
    assert.match(page.url(), /#test$/);
    await page.locator("#operations-test-run").click();
    await page.waitForFunction(() => document.querySelectorAll(".operations-test-row").length >= 5);
    assert.match(await page.locator("#operations-test-results").innerText(), /内置题包解析\s+通过/);
    assert.match(await page.locator("#operations-test-results").innerText(), /代码编辑器\s+通过/);
    assert.match(await page.locator("#operations-test-results").innerText(), /Markdown 编辑器\s+通过/);

    await page.locator("[data-operations-test-panel='diagnostics']").focus();
    await page.keyboard.press("ArrowRight");
    assert.equal(await page.locator("[data-operations-test-panel='data-control']").getAttribute("aria-selected"), "true");
    assert.equal(await page.locator("[data-operations-test-content='data-control']").isVisible(), true);
    await page.keyboard.press("ArrowRight");
    assert.equal(await page.locator("[data-operations-test-panel='editors']").getAttribute("aria-selected"), "true");
    assert.equal(await page.locator("[data-operations-test-content='editors']").isVisible(), true);
    assert.equal(await page.locator("[data-operations-test-content]:visible").count(), 1);
    const codeBlock = page.locator(".operations-demo", { has: page.locator("#operations-demo-code") }).locator(".cm-content");
    await codeBlock.click();
    await page.keyboard.press("Control+A");
    await page.keyboard.insertText("int main() { return 0; }");
    await page.keyboard.press("Control+/");
    let code = await page.locator("#operations-demo-code").evaluate((element) => element.value);
    assert.match(code, /^\/\//);
    await page.keyboard.press("Home");
    await page.keyboard.press("Tab");
    code = await page.locator("#operations-demo-code").evaluate((element) => element.value);
    assert.match(code, /^ {2,}\/\//);
    await page.locator("#operations-demo-language").selectOption("python3");
    await codeBlock.click();
    await page.keyboard.press("Control+A");
    await page.keyboard.insertText("print(1)");
    await page.keyboard.press("Control+/");
    code = await page.locator("#operations-demo-code").evaluate((element) => element.value);
    assert.match(code, /^#/);

    const markdownDemo = page.locator(".operations-demo", { has: page.locator("#operations-demo-markdown") });
    const markdownBlock = markdownDemo.locator(".cm-content");
    await markdownBlock.click();
    await page.keyboard.press("Control+A");
    await page.keyboard.insertText("toolbar");
    await page.keyboard.press("Control+A");
    await page.keyboard.press("Control+b");
    assert.match(await page.locator("#operations-demo-markdown").evaluate((element) => element.value), /\*\*toolbar\*\*/);
    assert.ok((await markdownDemo.locator(".editor-toolbar-button").count()) >= 9);

    await page.locator("[data-operations-test-panel='csv']").click();
    assert.equal(await page.locator("[data-operations-test-content='csv']").isVisible(), true);
    assert.equal(await page.locator("[data-operations-test-content]:visible").count(), 1);
    await page.locator("#operations-csv-analyze").click();
    assert.match(await page.locator("#operations-csv-result").innerText(), /账号 → username/);
    assert.match(await page.locator("#operations-csv-result").innerText(), /username,display_name,team,participant_type/);
    await page.locator("[data-operations-test-panel='package']").click();
    assert.equal(await page.locator("[data-operations-test-content='package']").isVisible(), true);
    assert.equal(await page.locator("[data-operations-test-content]:visible").count(), 1);
    await page.locator("#operations-package-builtin").click();
    await page.locator("#operations-package-import:not([disabled])").waitFor();
    assert.match(await page.locator("#operations-package-result").innerText(), /A\+B 输入输出练习I/);
    await page.locator("#operations-package-import").click();
    await page.waitForFunction(() => document.querySelector("#operations-package-result").textContent.includes("已创建题目"));
    const importedText = await page.locator("#operations-package-result").innerText();
    const problemID = Number(/已创建题目 #(\d+)/.exec(importedText)?.[1]);
    assert.ok(problemID > 0);
    await page.screenshot({ path: ".cache/admin-test-center-desktop.png", fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    assert.equal(await page.locator("[data-operations-test-panel]").evaluateAll((buttons) =>
      new Set(buttons.map((button) => Math.round(button.getBoundingClientRect().top))).size), 1);
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
    await page.screenshot({ path: ".cache/admin-test-center-mobile.png", fullPage: true });
    await page.setViewportSize({ width: 1440, height: 1000 });

    await page.goto(env.PUBLIC_URL + `/admin/problems?select=${problemID}`);
    await page.locator("#problem-admin").waitFor({ state: "visible" });
    await page.waitForFunction(() => document.querySelector("#problem-editor-heading").textContent === "编辑题目");
    assert.ok(await page.locator("#edit-description").evaluate((element) => element.offsetParent === null));
    assert.ok((await page.locator("#problem-editor-form .markdown-toolbar").count()) >= 3);
    assert.ok((await page.locator("#problem-editor-form .cm-editor").count()) >= 4);
    const descriptionEditor = page.locator("#edit-description + .editor-toolbar + .code-editor .cm-content");
    await descriptionEditor.fill("# Imported statement\n\n**Safe Markdown**\n\n<script>window.markdownXSS=1</script>");
    await page.locator("#edit-status").selectOption("published");
    await page.locator("#edit-visibility").selectOption("public");
    await page.locator("#problem-save").click();
    await page.locator("#problem-editor-message").filter({ hasText: "题面已保存" }).waitFor();
    await page.goto(env.PUBLIC_URL + `/problems/${problemID}`);
    await page.locator("#problem-description h1").filter({ hasText: "Imported statement" }).waitFor();
    assert.equal(await page.locator("#problem-description script").count(), 0);
    assert.equal(await page.evaluate(() => window.markdownXSS), undefined);

    const suffix = Date.now();
    const csrf = (await page.evaluate(async () => (await (await fetch("/api/auth/csrf")).json()).data.csrf_token));
    const post = async (path, body) => page.evaluate(async ({ path, body, csrf }) => {
      const response = await fetch(path, {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
        body: JSON.stringify(body),
      });
      return { status: response.status, data: await response.json() };
    }, { path, body, csrf });
    const username = `markdown_user_${suffix}`;
    const password = `Secure-${suffix}-Pass`;
    assert.equal((await post("/api/auth/register", { username, password, password_confirmation: password, student_no: "" })).status, 201);
    const created = await post("/api/contests", {
      title: `Live edit ${suffix}`, scoring_mode: "OI", enrollment_policy: "admin_add",
      starts_at: new Date(Date.now() + 3600000).toISOString(), ends_at: new Date(Date.now() + 7200000).toISOString(),
      approval_required: false, team_mode: false,
    });
    assert.equal(created.status, 200);
    const contestID = created.data.data.id;
    await page.goto(env.PUBLIC_URL + `/contests/${contestID}`);
    await page.locator("#contest-title").filter({ hasText: `Live edit ${suffix}` }).waitFor();
    await page.locator("[data-contest-panel='import']").click();
    await page.locator("#contest-csv-form textarea").fill(`账号,队伍,选手类型\n${username},Chinese Team,official`);
    await page.locator("#contest-csv-form button").click();
    await page.locator("#contest-csv-preview").getByText(/已识别列/).waitFor();
    assert.match(await page.locator("#contest-csv-preview").innerText(), /账号 → username/);
    assert.equal(await page.locator("#contest-csv-confirm").isDisabled(), false);

    await page.locator("[data-contest-panel='edit']").click();
    const editForm = page.locator("#contest-live-edit-form");
    await editForm.locator("[name=starts_at]").fill(local(new Date(Date.now() - 300000)));
    await editForm.locator("[name=ends_at]").fill(local(new Date(Date.now() + 1800000)));
    await editForm.locator("[name=change_reason]").fill("network outage");
    page.once("dialog", (dialog) => dialog.accept());
    await editForm.locator("button[type=submit]").click();
    await page.waitForFunction(() => document.querySelector("#contest-detail-message").textContent.includes("设置已保存"));
    assert.match(page.url(), new RegExp(`/contests/${contestID}$`));
    assert.equal(await editForm.locator("[name=scoring_mode]").isDisabled(), true);
    assert.equal(await editForm.locator("[name=team_mode]").isDisabled(), true);
    await editForm.locator("[name=ends_at]").fill(local(new Date(Date.now() + 3600000)));
    await editForm.locator("[name=change_reason]").fill("extend after network recovery");
    page.once("dialog", (dialog) => dialog.accept());
    const secondEdit = page.waitForResponse((response) =>
      response.url().endsWith(`/api/contests/${contestID}`) && response.request().method() === "PUT");
    await editForm.locator("button[type=submit]").click();
    assert.equal((await secondEdit).status(), 200);
    await page.waitForFunction(() => document.querySelector("#contest-detail-message").textContent.includes("设置已保存"));
    assert.match(page.url(), new RegExp(`/contests/${contestID}$`));
    await page.screenshot({ path: ".cache/admin-live-contest-edit.png", fullPage: true });

    await page.goto(env.PUBLIC_URL + "/learning");
    await page.locator(".learning-filter-compact").waitFor();
    const filterBox = await page.locator(".learning-filter-compact").boundingBox();
    assert.ok(filterBox.height < 180, `community filter height ${filterBox.height}`);
    await page.locator("#learning-new").click();
    await page.locator("#learning-form .markdown-toolbar").waitFor();
    await page.setViewportSize({ width: 390, height: 844 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
    await page.screenshot({ path: ".cache/admin-experience-mobile.png", fullPage: true });
    assert.deepEqual(errors, []);
    console.log("Edge: admin hierarchy and test-center tabs, one-click diagnostics, code/Markdown shortcuts, CSV aliases, package import, live contest editing without navigation, compact community passed");
  } finally {
    await browser.close();
  }
})().catch((error) => {
  console.error(error.stack || error.message);
  process.exit(1);
});
