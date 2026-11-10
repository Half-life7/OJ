const { chromium } = require(require("path").resolve(".cache/browser/node_modules/playwright"));
const fs = require("fs");
const assert = require("assert/strict");

(async () => {
  const env = Object.fromEntries(fs.readFileSync(".cache/2d-acceptance.env", "utf8").split(/\r?\n/)
    .filter((line) => line.includes("=") && !line.startsWith("#"))
    .map((line) => {
      const index = line.indexOf("=");
      return [line.slice(0, index), line.slice(index + 1)];
    }));
  const local = (date) => new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
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
    await page.locator("[data-operations-panel='accounts']").click();
    await page.locator("#operations-search [name=q]").fill("b5_");
    await page.locator("#operations-search button").click();
    await page.locator("#operations-users button").first().waitFor();
    await page.locator("#operations-users button").first().click();
    const status = page.locator("#operations-status");
    await status.locator("[name=reason]").fill("Browser status check");
    await status.locator("button").click();
    await page.waitForFunction(() => document.querySelector("#operations-message").textContent === "操作完成");
    assert.match(await page.locator("#operations-users").innerText(), /disabled/);
    await status.locator("[name=status]").selectOption("active");
    await status.locator("button").click();
    await page.waitForFunction(() => document.querySelector("#operations-message").textContent === "操作完成");
    await page.screenshot({ path: ".cache/5-admin-desktop.png", fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.screenshot({ path: ".cache/5-admin-mobile.png", fullPage: true });
    await page.setViewportSize({ width: 1440, height: 1000 });

    const people = await page.evaluate(async () => {
      const response = await fetch("/api/admin/operations/users?q=b5_");
      return (await response.json()).data;
    });
    assert.ok(people.length >= 2);
    await page.goto(env.PUBLIC_URL + "/contests");
    await page.locator("#contest-create-toggle").click();
    await page.locator("#contest-create").waitFor({ state: "visible" });
    const form = page.locator("#contest-create-form");
    const name = "CSV browser " + Date.now();
    await form.locator("[name=title]").fill(name);
    await form.locator("[name=starts_at]").fill(local(new Date(Date.now() + 3600000)));
    await form.locator("[name=duration_minutes]").fill("60");
    await form.locator("[type=submit]").click();
    await page.locator("#contest-title").filter({ hasText: name }).waitFor();

    await page.locator("[data-contest-panel='import']").click();
    await page.locator("#contest-csv-file").setInputFiles({
      name: "roster.csv",
      mimeType: "text/csv",
      buffer: Buffer.from("username\n" + people.slice(0, 2).map((person) => person.username).join("\n") + "\n"),
    });
    await page.waitForFunction(() => document.querySelector("#contest-csv-form textarea").value.includes("username"));
    await page.locator("#contest-csv-form button").click();
    await page.waitForFunction(() => !document.querySelector("#contest-csv-confirm").disabled);
    assert.equal(await page.locator("#contest-csv-preview p").count(), 4);
    await page.locator("#contest-csv-confirm").click();
    await page.waitForFunction(() => document.querySelector("#contest-detail-message").textContent.includes("名单已导入"));
    await page.locator("[data-contest-panel='standings']").click();
    await page.locator("#contest-roster tr").first().waitFor();
    assert.equal(await page.locator("#contest-roster tr").count(), 2);
    await page.locator("[data-contest-panel='import']").click();
    const [download] = await Promise.all([
      page.waitForEvent("download"),
      page.locator("#contest-export").click(),
    ]);
    assert.equal(download.suggestedFilename(), "contest.csv");

    await page.locator("[data-contest-panel='manage']").click();
    await page.locator("#contest-manage-action").selectOption("bulk_status");
    await page.locator("#contest-bulk-teams input[type=checkbox]").first().waitFor();
    const teams = page.locator("#contest-bulk-teams input[type=checkbox]");
    for (let index = 0; index < await teams.count(); index++) await teams.nth(index).check();
    await page.locator("#contest-manage-form [name=participant_type]").selectOption("star");
    page.once("dialog", (dialog) => dialog.accept());
    await page.locator("#contest-manage-form button[type=submit]").click();
    await page.waitForFunction(() => [...document.querySelectorAll("#contest-roster tr")].every((row) => row.textContent.includes("star")));
    assert.deepEqual(errors, []);
    console.log("Edge admin desktop/mobile: layered account operations, CSV preview/confirm, export and managed bulk star passed");
  } finally {
    await browser.close();
  }
})().catch((error) => {
  console.error(error.stack || error.message);
  process.exit(1);
});
