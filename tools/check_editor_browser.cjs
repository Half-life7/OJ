const assert = require("node:assert/strict");
const path = require("node:path");
const root = path.resolve(__dirname, "..");
const { chromium } = require(path.join(root, ".cache/browser/node_modules/playwright"));

(async () => {
  const browser = await chromium.launch({
    headless: true,
    executablePath: "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe",
  });
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
  await page.setContent(`<!doctype html><html><head><meta charset="utf-8"><meta name="csp-nonce" content="editor-test"><style>
    body{margin:0;padding:30px;background:#eef4f0;font-family:system-ui}
    .host{max-width:900px;margin:auto;padding:24px;background:white}
    textarea{width:100%;height:80px}
  </style></head><body><div class="host"><textarea>int main() { return 0; }</textarea><textarea id="markdown"># Title</textarea></div></body></html>`);
  await page.addStyleTag({ path: path.join(root, "webui/static/app.css") });
  await page.addScriptTag({ path: path.join(root, "webui/static/editor.js") });
  await page.evaluate(() => {
    const textarea = document.querySelector("textarea");
    window.editorApi = window.BiueEditor.mount(textarea, "cpp17", { ariaLabel: "测试编辑器" });
  });
  const editor = page.locator(".cm-content");
  await editor.click();

  await page.evaluate(() => window.editorApi.setValue("abc"));
  await page.locator(".cm-content").click();
  await page.keyboard.press("Control+End");
  await page.keyboard.press("Tab");
  assert.equal(await page.evaluate(() => window.editorApi.getValue()), "abc    ");

  await page.keyboard.press("Control+A");
  await page.keyboard.press("Tab");
  assert.equal(await page.evaluate(() => window.editorApi.getValue()), "    abc    ");
  await page.keyboard.press("Shift+Tab");
  assert.equal(await page.evaluate(() => window.editorApi.getValue()), "abc    ");

  await page.evaluate(() => window.editorApi.setValue("cou"));
  await page.locator(".cm-content").click();
  await page.keyboard.press("Control+End");
  await page.keyboard.press("Control+Space");
  await page.waitForSelector(".cm-tooltip-autocomplete");
  assert.match(await page.locator(".cm-tooltip-autocomplete").innerText(), /cout/);
  await page.keyboard.press("Escape");

  await page.evaluate(() => {
    window.editorApi.setLanguage("go");
    window.editorApi.setValue("fmt.Println(\"x\")");
  });
  await page.locator(".cm-content").click();
  await page.keyboard.press("Control+A");
  await page.keyboard.press("Control+/");
  assert.match(await page.evaluate(() => window.editorApi.getValue()), /^\s*\/\/ fmt\.Println/);
  await page.evaluate(() => window.editorApi.setValue("fm"));
  await page.locator(".cm-content").click();
  await page.keyboard.press("Control+End");
  await page.keyboard.press("Control+Space");
  await page.waitForSelector(".cm-tooltip-autocomplete");
  assert.match(await page.locator(".cm-tooltip-autocomplete").innerText(), /fmt/);
  await page.keyboard.press("Escape");

  await page.getByRole("button", { name: /查找与替换/ }).click();
  await page.waitForSelector(".cm-search");
  await page.getByRole("button", { name: /放大到全屏编辑/ }).click();
  assert.equal(await page.locator(".code-editor-shell").evaluate((node) => node.classList.contains("is-expanded")), true);
  const expandedHeight = await page.locator(".code-editor .cm-editor").evaluate((node) => node.getBoundingClientRect().height);
  assert.ok(expandedHeight > 700, `expanded editor height ${expandedHeight}`);
  assert.match(await page.locator(".editor-statusbar").innerText(), /Go/);
  await page.getByRole("button", { name: /放大到全屏编辑/ }).click();
  await page.evaluate(() => {
    window.previewed = "";
    window.markdownApi = window.BiueEditor.mount(document.querySelector("#markdown"), "markdown", {
      ariaLabel: "Markdown 测试",
      onPreview: (value) => { window.previewed = value; },
    });
    window.markdownApi.setValue("text");
  });
  const markdownContent = page.locator("#markdown + .code-editor-shell .cm-content");
  await markdownContent.click();
  await page.keyboard.press("Control+A");
  await page.getByRole("button", { name: /Ctrl\/Cmd\+B/ }).click();
  assert.equal(await page.evaluate(() => window.markdownApi.getValue()), "**text**");
  await markdownContent.click();
  await page.keyboard.press("Control+Enter");
  assert.equal(await page.evaluate(() => window.previewed), "**text**");
  assert.ok((await page.locator("#markdown + .code-editor-shell .editor-toolbar-button").count()) >= 10);
  await page.screenshot({ path: path.join(root, ".cache/editor-feature-check.png"), fullPage: true });
  console.log(JSON.stringify({ tabInsert: true, selectedIndent: true, completion: "cout", comment: true, search: true, markdown: true, expandedHeight }));
  await browser.close();
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
