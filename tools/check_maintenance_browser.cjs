const { chromium } = require(require("path").resolve(".cache/browser/node_modules/playwright"));
const assert = require("node:assert/strict");
const path = require("node:path");

const baseURL = process.env.BASE_URL;
const username = process.env.ADMIN_USER;
const password = process.env.ADMIN_PASSWORD;
if (!baseURL || !username || !password) throw new Error("BASE_URL, ADMIN_USER and ADMIN_PASSWORD are required");

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

    await page.goto(`${baseURL}/login`);
    await page.locator("#login-username").fill(username);
    await page.locator("#login-password").fill(password);
    await page.locator("#login-form button[type=submit]").click();
    await page.waitForURL("**/me");

    await page.goto(`${baseURL}/admin/problems`);
    await page.locator("#problem-admin").waitFor({ state: "visible" });
    assert.equal(await page.locator("[data-problem-admin-panel-content]:visible").count(), 1);
    assert.equal(await page.locator("#problem-list-workspace").isVisible(), true);
    await page.locator("#problem-nav-new").click();
    assert.equal(await page.locator("#problem-edit-workspace").isVisible(), true);
    assert.equal(await page.locator("#problem-list-workspace").isVisible(), false);
    assert.match(page.url(), /#new$/);
    await page.setViewportSize({ width: 390, height: 844 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
    await page.screenshot({ path: ".cache/maintenance-problem-mobile.png", fullPage: true });
    await page.setViewportSize({ width: 1440, height: 1000 });

    await page.goto(`${baseURL}/contests`);
    await page.locator("#contest-list-workspace").waitFor({ state: "visible" });
    assert.equal(await page.locator("#contest-create").isVisible(), false);
    await page.locator("#contest-create-toggle").click();
    assert.equal(await page.locator("#contest-create").isVisible(), true);
    assert.equal(await page.locator("#contest-list-workspace").isVisible(), false);
    assert.match(page.url(), /#create$/);
    await page.setViewportSize({ width: 390, height: 844 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
    await page.screenshot({ path: ".cache/maintenance-contest-mobile.png", fullPage: true });
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.locator("#contest-nav-list").click();
    assert.equal(await page.locator("#contest-list-workspace").isVisible(), true);

    await page.goto(`${baseURL}/admin/identities`);
    await page.locator("#identity-list-workspace").waitFor({ state: "visible" });
    assert.equal(await page.locator("#identity-edit-workspace").isVisible(), false);
    await page.locator("#identity-nav-edit").click();
    assert.equal(await page.locator("#identity-edit-workspace").isVisible(), true);
    assert.equal(await page.locator("[data-identity-panel-content]:visible").count(), 1);
    await page.setViewportSize({ width: 390, height: 844 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
    await page.screenshot({ path: ".cache/maintenance-identity-mobile.png", fullPage: true });
    await page.setViewportSize({ width: 1440, height: 1000 });

    await page.goto(`${baseURL}/admin#test`);
    await page.locator("#operations-content").waitFor({ state: "visible" });
    await page.locator("[data-operations-test-panel='package']").click();
    await page.locator("#operations-package-file").setInputFiles(path.resolve("导入样例/菲波那契数.zip"));
    await page.locator("#operations-package-preview").click();
    await page.locator("#operations-package-result").getByText("菲波那契数", { exact: true }).waitFor();
    assert.match(await page.locator("#operations-package-result").innerText(), /10 个测试点/);
    await page.locator("#operations-package-import:not([disabled])").click();
    await page.waitForFunction(() => document.querySelector("#operations-package-result").textContent.includes("已创建题目"));
    const importedText = await page.locator("#operations-package-result").innerText();
    const importedProblemID = Number(/已创建题目 #(\d+)/.exec(importedText)?.[1]);
    assert.ok(importedProblemID > 0);

    await page.locator("[data-operations-test-panel='data-control']").click();
    await page.locator("#operations-simulate").click();
    await page.locator("#operations-simulation-result h3").waitFor({ timeout: 60000 });
    const simulationText = await page.locator("#operations-simulation-result").innerText();
    assert.match(simulationText, /36 个账号/);
    assert.match(simulationText, /不是真实 Runner 执行结果/);
    const simulationCredentials = await page.locator("#operations-simulation-result textarea").inputValue();
    assert.match(simulationCredentials, /统一初始密码/);
    const contestLink = page.locator("#operations-simulation-result a").first();
    const simulatedContestID = Number((await contestLink.getAttribute("href")).split("/").pop());
    assert.ok(simulatedContestID > 0);
    await page.screenshot({ path: ".cache/maintenance-test-center-desktop.png", fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
    await page.screenshot({ path: ".cache/maintenance-test-center-mobile.png", fullPage: true });
    await page.setViewportSize({ width: 1440, height: 1000 });

    await page.goto(`${baseURL}/contests/${simulatedContestID}`);
    await page.waitForFunction(() => document.querySelector("#contest-overview").textContent.includes("已结算"));
    assert.match(await page.locator("#contest-overview").innerText(), /已结算/);
    await page.goto(`${baseURL}/admin#test`);
    await page.locator("#operations-content").waitFor({ state: "visible" });
    await page.locator("[data-operations-test-panel='data-control']").click();

    const problemRejudge = page.locator("#operations-rejudge-problem");
    await problemRejudge.locator("[name=problem_id]").fill(String(importedProblemID));
    await problemRejudge.locator("button").click();
    await page.locator("#operations-scope-rejudge-result").getByText(/已排队 0 条/).waitFor();

    const contestRejudge = page.locator("#operations-rejudge-contest");
    await contestRejudge.locator("[name=contest_id]").fill(String(simulatedContestID));
    await contestRejudge.locator("button").click();
    await page.waitForFunction(() => document.querySelector("#operations-message").textContent.includes("状态已发生变化"));

    const reset = page.locator("#operations-reset");
    await reset.locator("[name=confirmation]").fill("初始化全部业务数据");
    await reset.locator("[name=reason]").fill("完成功能验收后清理临时数据");
    page.once("dialog", (dialog) => dialog.accept());
    await reset.locator("button[type=submit]").click();
    await page.locator("#operations-reset-result h3").waitFor({ timeout: 60000 });
    assert.match(await page.locator("#operations-reset-result").innerText(), /初始化完成/);
    assert.equal((await page.request.get(`${baseURL}/api/users/me`)).status(), 200);
    assert.equal((await page.request.get(`${baseURL}/ready`)).status(), 200);
    await page.screenshot({ path: ".cache/maintenance-reset-desktop.png", fullPage: true });

    assert.deepEqual(errors, []);
    console.log("Edge: independent problem/contest/identity workspaces, wrapped ZIP import, labelled simulation, scoped rejudge and guarded reset passed");
  } finally {
    await browser.close();
  }
})().catch((error) => {
  console.error(error.stack || error.message);
  process.exit(1);
});
