// Uses the dedicated acceptance project; credentials are read from the ignored env file.
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

    const suffix = Date.now().toString();
    const problemTitle = "Optimization problem " + suffix;
    await page.goto(env.PUBLIC_URL + "/admin/problems");
    await page.locator("#problem-admin").waitFor({ state: "visible" });
    await page.locator("#problem-new").click();
    await page.locator("#edit-title").fill(problemTitle);
    await page.locator("#edit-description + .editor-toolbar + .code-editor .cm-content").fill("Direct files and public visibility acceptance.");
    await page.locator("#edit-input-format + .editor-toolbar + .code-editor .cm-content").fill("Two integers");
    await page.locator("#edit-output-format + .editor-toolbar + .code-editor .cm-content").fill("Their sum");
    await page.locator("#edit-sample-input").fill("1 2\n");
    await page.locator("#edit-sample-output").fill("3\n");
    await page.locator("#edit-status").selectOption("draft");
    await page.locator("#edit-visibility").selectOption("public");
    assert.equal(await page.locator("#edit-status").inputValue(), "published", "public must publish the problem");
    await page.locator("#problem-save").click();
    try {
      await page.waitForFunction(() => !document.querySelector("#problem-upload-fields").disabled);
    } catch (error) {
      console.error("problem save:", await page.locator("#problem-editor-message").innerText());
      throw error;
    }

    await page.locator("#data-files").setInputFiles([
      { name: "1.in", mimeType: "text/plain", buffer: Buffer.from("1 2\n") },
      { name: "1.out", mimeType: "text/plain", buffer: Buffer.from("3\n") },
    ]);
    await page.locator("#data-preview-summary").filter({ hasText: "1 个测试点" }).waitFor();
    assert.equal(await page.locator("[data-score-id='1']").inputValue(), "100");
    await page.locator("#data-upload-submit").click();
    await page.waitForFunction(() => document.querySelector("#problem-current-data").textContent.includes("v1"));
    const createdProblem = await page.evaluate(async (title) => {
      const response = await fetch(`/api/admin/problems?q=${encodeURIComponent(title)}&limit=20`);
      return (await response.json()).data.items.find((problem) => problem.title === title);
    }, problemTitle);
    assert.ok(createdProblem?.id > 0);
    await page.locator("#data-files").setInputFiles([
      { name: "2.in", mimeType: "text/plain", buffer: Buffer.from("3 4\n") },
      { name: "2.out", mimeType: "text/plain", buffer: Buffer.from("7\n") },
    ]);
    await page.locator("#data-import-mode").selectOption("merge");
    await page.locator("#data-preview-summary").filter({ hasText: "1 个测试点" }).waitFor();
    await page.locator("#data-upload-submit").click();
    await page.waitForFunction(() => document.querySelector("#problem-current-data").textContent.includes("v2"));
    const mergedVersion = await page.evaluate(async (id) => {
      const response = await fetch(`/api/admin/problems/${id}/data-versions/2`);
      return (await response.json()).data;
    }, createdProblem.id);
    assert.deepEqual(mergedVersion.cases.map((testcase) => testcase.id), ["1", "2"]);

    await page.goto(env.PUBLIC_URL + "/problems?q=" + encodeURIComponent(problemTitle));
    await page.locator("#problem-list").getByText(problemTitle, { exact: true }).waitFor();

    await page.goto(env.PUBLIC_URL + "/admin/problems");
    await page.locator("#problem-admin").waitFor({ state: "visible" });
    const problemButton = page.locator("#admin-problem-list button", { hasText: problemTitle });
    await problemButton.click();
    await page.waitForFunction(() => document.querySelector("#problem-editor-heading").textContent === "编辑题目");
    await page.locator("#edit-checker-mode").selectOption("spj");
    assert.equal(await page.locator("#checker-workspace").isVisible(), true);
    await page.locator("#checker-template").click();
    assert.match(await page.locator("#edit-checker-source").inputValue(), /base64/);
    await page.locator("#problem-save").click();
    await page.locator("#problem-editor-message").filter({ hasText: "题面已保存" }).waitFor();

    const contestTitle = "Optimization contest " + suffix;
    await page.goto(env.PUBLIC_URL + "/contests");
    await page.locator("#contest-create-toggle").waitFor({ state: "visible" });
    await page.locator("#contest-create-toggle").click();
    await page.locator("#contest-create").waitFor({ state: "visible" });
    await page.locator("#contest-create-form [name=title]").fill(contestTitle);
    await page.locator("#contest-create-form [name=starts_at]").fill(local(new Date(Date.now() + 60 * 60000)));
    await page.locator("#contest-create-form [name=duration_minutes]").fill("60");
    await page.locator("#contest-create-form [type=submit]").click();
    await page.waitForURL(/\/contests\/\d+$/);
    await page.locator("#contest-title").filter({ hasText: contestTitle }).waitFor();
    const contestID = Number(page.url().split("/").pop());
    assert.ok(contestID > 0);
    await page.locator("[data-contest-panel='manage']").click();
    await page.locator("#contest-manage").waitFor({ state: "visible" });
    const options = await page.evaluate(async ({ id, title }) => {
      const response = await fetch(`/api/contests/${id}/manage-options?q=${encodeURIComponent(title)}`);
      return (await response.json()).data;
    }, { id: contestID, title: problemTitle });
    assert.ok(options.problems.some((problem) => problem.title === problemTitle));
    await page.locator("#contest-problem-option").selectOption(String(options.problems.find((problem) => problem.title === problemTitle).id));
    const manageResponse = page.waitForResponse((response) =>
      response.url().endsWith(`/api/contests/${contestID}/manage`) && response.request().method() === "POST");
    await page.locator("#contest-manage-form button[type=submit]").click();
    assert.equal((await manageResponse).status(), 200);
    await page.waitForFunction(() => {
      const submit = document.querySelector("#contest-manage-form button[type=submit]");
      return !submit.disabled && document.querySelector("#contest-detail-message").textContent.includes("完成");
    });
    await page.locator("[data-contest-panel='problems']").click();
    await page.locator("#contest-problems").getByText(problemTitle, { exact: false }).waitFor();
    await page.screenshot({ path: ".cache/opts-6-17-contest-desktop.png", fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
    await page.screenshot({ path: ".cache/opts-6-17-contest-mobile.png", fullPage: true });
    await page.setViewportSize({ width: 1440, height: 1000 });

    const communityTitle = "Optimization note " + suffix;
    await page.goto(env.PUBLIC_URL + "/learning");
    await page.locator("#learning-new").waitFor({ state: "visible" });
    assert.equal(await page.locator("#learning-form").isVisible(), false);
    await page.locator("#learning-new").click();
    await page.locator("#learning-form [name=title]").fill(communityTitle);
    await page.locator("#learning-form .cm-content").fill("# Acceptance\n\nSafe content.");
    await page.locator("#learning-form > button").click();
    await page.locator("#learning-title").filter({ hasText: communityTitle }).waitFor();
    assert.equal(await page.locator("#learning-favorite").isVisible(), true);
    assert.equal(await page.locator("#learning-unfavorite").isVisible(), false);
    await page.locator("#learning-favorite").click();
    await page.locator("#learning-unfavorite").waitFor({ state: "visible" });
    assert.equal(await page.locator("#learning-favorite").isVisible(), false);

    await page.screenshot({ path: ".cache/opts-6-17-desktop.png", fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
    await page.screenshot({ path: ".cache/opts-6-17-mobile.png", fullPage: true });
    assert.deepEqual(errors, []);
    console.log("Edge desktop/mobile: public publication, paired files, replace/merge versions, score preview, SPJ template, contest workspace/manage selector, community capability state passed");
  } finally {
    await browser.close();
  }
})().catch((error) => {
  console.error(error.stack || error.message);
  process.exit(1);
});
