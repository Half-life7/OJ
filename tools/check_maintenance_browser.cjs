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
    await page.waitForFunction(() => document.querySelector("#operations-simulation-progress h3")?.textContent.includes("真实评测与三场结算完成"), null, { timeout: 360000 });
    const simulationText = await page.locator("#operations-simulation-result").innerText();
    assert.match(simulationText, /30 个账号/);
    assert.match(simulationText, /6 道题/);
    assert.match(simulationText, /90 条真实提交/);
    assert.match(simulationText, /终态 90 \/ 90/);
    for (const label of ["通过", "答案错误", "超时", "超内存", "运行错误", "编译错误"]) assert.match(simulationText, new RegExp(label));
    const simulationCredentials = await page.locator("#operations-simulation-result textarea").inputValue();
    assert.match(simulationCredentials, /统一初始密码/);
    assert.equal(await page.locator("#operations-simulation-progress").getAttribute("data-job-id").then(Boolean), true);
    const simulationJobID = await page.locator("#operations-simulation-progress").getAttribute("data-job-id");
    const simulationResponse = await page.request.get(`${baseURL}/api/admin/operations/simulate/${simulationJobID}`);
    assert.equal(simulationResponse.status(), 200);
    const simulationResult = (await simulationResponse.json()).data;
    assert.equal(simulationResult.complete, true);
    for (const outcome of ["Accepted", "WrongAnswer", "TimeLimitExceeded", "MemoryLimitExceeded", "RuntimeError", "CompileError"]) {
      assert.ok(simulationResult.progress[outcome] > 0, `missing real outcome ${outcome}`);
    }
    const contestLinks = page.locator("#operations-simulation-progress a");
    assert.equal(await contestLinks.count(), 3);
    const contestIDs = [];
    for (let index = 0; index < 3; index++) contestIDs.push(Number((await contestLinks.nth(index).getAttribute("href")).split("/").pop()));
    assert.deepEqual(contestIDs.every((id) => id > 0), true);
    await page.screenshot({ path: ".cache/maintenance-test-center-desktop.png", fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
    await page.screenshot({ path: ".cache/maintenance-test-center-mobile.png", fullPage: true });
    await page.setViewportSize({ width: 1440, height: 1000 });

    for (const contestID of contestIDs) {
      await page.goto(`${baseURL}/contests/${contestID}`);
      await page.waitForFunction(() => document.querySelector("#contest-overview").textContent.includes("已结算"));
    }
    const firstContestID = contestIDs[0];
    await page.goto(`${baseURL}/contests/${firstContestID}`);
    await page.locator("#contest-title").waitFor({ state: "visible" });
    await page.locator("[data-contest-panel='standings']").click();
    await page.locator("#contest-roster tr").first().waitFor();
    assert.equal(await page.locator("#contest-roster-head th.contest-problem-heading-cell").count(), 6);
    assert.equal(await page.locator("#contest-roster tr").count(), 10);
    assert.match(await page.locator("#contest-roster").innerText(), /ms/);
    assert.match(await page.locator("#contest-roster").innerText(), /MiB/);
    assert.equal(await page.locator(".contest-table-scroll").evaluate((element) => getComputedStyle(element).overflowX), "auto");

    const rowUsername = simulationCredentials.split("\n").find((line) => line.startsWith("sim"));
    const sharedPassword = /统一初始密码：(.+)/.exec(simulationCredentials)?.[1]?.trim();
    assert.ok(rowUsername && sharedPassword);
    const outsider = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
    const outsiderPage = await outsider.newPage();
    await outsiderPage.goto(`${baseURL}/login`);
    await outsiderPage.locator("#login-username").fill(rowUsername);
    await outsiderPage.locator("#login-password").fill(sharedPassword);
    await outsiderPage.locator("#login-form button[type=submit]").click();
    await outsiderPage.waitForURL("**/me");
    await outsiderPage.goto(`${baseURL}/contests/${contestIDs[1]}`);
    await outsiderPage.locator("[data-contest-panel='standings']").click();
    await outsiderPage.locator("#contest-roster tr").first().waitFor();
    assert.equal(await outsiderPage.locator("#contest-roster tr").count(), 10);
    await outsider.close();

    await page.goto(`${baseURL}/admin#submissions`);
    await page.locator("#operations-content").waitFor({ state: "visible" });
    await page.locator("#operations-submissions-rows button").first().waitFor({ timeout: 20000 });
    assert.ok(await page.locator("#operations-submissions-rows tr").count() >= 50);
    await page.locator("#operations-submissions-rows button").first().click();
    await page.locator("#operations-submission-source").filter({ hasText: "#include" }).waitFor();
    assert.equal(await page.locator("#operations-submissions-list-view").isVisible(), false);
    assert.equal(await page.locator("#operations-submission-detail-view").isVisible(), true);

    await page.locator("[data-operations-panel='accounts']").click();
    await page.locator("#operations-users button").first().waitFor();
    await page.locator("#operations-users button").first().click();
    assert.equal(await page.locator("#operations-accounts-list-view").isVisible(), false);
    assert.equal(await page.locator("#operations-accounts-detail-view").isVisible(), true);
    await page.locator("#operations-accounts-back").click();
    assert.equal(await page.locator("#operations-accounts-list-view").isVisible(), true);

    const discussionTitle = "独立讨论工作区 " + Date.now();
    await page.goto(`${baseURL}/learning?kind=discussion`);
    const csrf = await page.evaluate(async () => (await (await fetch("/api/auth/csrf")).json()).data.csrf_token);
    const createdDiscussion = await page.evaluate(async ({ title, csrf, contestID }) => {
      const response = await fetch("/api/community", {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
        body: JSON.stringify({ kind: "discussion", title, body: "## 独立详情\n\n内容", visibility: "public", problem_id: null, contest_id: contestID, parent_id: null, problems: [], revision: 0 }),
      });
      return { status: response.status, data: await response.json() };
    }, { title: discussionTitle, csrf, contestID: contestIDs[0] });
    assert.equal(createdDiscussion.status, 200, JSON.stringify(createdDiscussion.data));
    await page.reload();
    await page.locator("#learning-list button").filter({ hasText: discussionTitle }).click();
    await page.locator("#learning-title").filter({ hasText: discussionTitle }).waitFor();
    assert.equal(await page.locator("#learning-list-workspace").isVisible(), false);
    assert.equal(await page.locator("#learning-detail").isVisible(), true);

    const publicProblem = await page.evaluate(async () => {
      const response = await fetch(`/api/problems?q=${encodeURIComponent("真实测试 标准 A+B")}&limit=1`);
      return (await response.json()).data.items[0];
    });
    assert.ok(publicProblem?.id > 0);
    await page.goto(`${baseURL}/problems/${publicProblem.id}`);
    await page.locator("#problem-detail").waitFor({ state: "visible" });
    await page.locator("#problem-edit-mode").click();
    const layout = await page.evaluate(() => ({
      viewport: innerWidth,
      content: document.querySelector(".main-content").getBoundingClientRect().width,
      workspace: document.querySelector("#problem-workspace").getBoundingClientRect().width,
    }));
    assert.ok(layout.content >= 1360, JSON.stringify(layout));
    assert.ok(layout.workspace >= 1300, JSON.stringify(layout));
    await page.screenshot({ path: ".cache/maintenance-wide-editor.png", fullPage: true });

    await page.goto(`${baseURL}/admin#test`);
    await page.locator("#operations-content").waitFor({ state: "visible" });
    await page.locator("[data-operations-test-panel='data-control']").click();

    const problemRejudge = page.locator("#operations-rejudge-problem");
    await problemRejudge.locator("[name=problem_id]").fill(String(importedProblemID));
    await problemRejudge.locator("button").click();
    await page.locator("#operations-scope-rejudge-result").getByText(/已排队 0 条/).waitFor();

    const contestRejudge = page.locator("#operations-rejudge-contest");
    await contestRejudge.locator("[name=contest_id]").fill(String(contestIDs[0]));
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
    console.log("Edge: real OI/ACM/IOI runner simulation, wide roster/editor, all submissions source, independent discussion and guarded reset passed");
  } finally {
    await browser.close();
  }
})().catch((error) => {
  console.error(error.stack || error.message);
  process.exit(1);
});
