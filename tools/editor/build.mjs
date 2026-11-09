import { build } from "esbuild";
import { readFile, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(fileURLToPath(import.meta.url));
await build({
  entryPoints: [join(root, "editor.js")],
  outfile: resolve(root, "../../webui/static/editor.js"),
  bundle: true,
  minify: true,
  format: "iife",
  globalName: "BiueEditor",
  target: ["es2020"],
  // Preserve snippet indentation as escapes, avoiding literal whitespace-only
  // lines in the generated bundle while keeping snippet contents unchanged.
  supported: { "template-literal": false },
  legalComments: "inline",
  charset: "utf8",
  banner: { js: "/* Biue OJ local CodeMirror 6 bundle. Rebuild: tools/editor/README.md. Licenses: tools/editor/THIRD_PARTY_LICENSES.txt. */" },
});

const lock = JSON.parse(await readFile(join(root, "package-lock.json"), "utf8"));
const licenses = ["Third-party notices for the local CodeMirror editor bundle and build tool.\n"];
for (const [packagePath, entry] of Object.entries(lock.packages).sort(([a], [b]) => a.localeCompare(b))) {
  if (!packagePath || !packagePath.startsWith("node_modules/")) continue;
  const name = packagePath.slice("node_modules/".length);
  for (const file of ["LICENSE", "LICENSE.txt", "LICENSE.md", "license"]) {
    try {
      const license = await readFile(join(root, packagePath, file), "utf8");
      licenses.push("\n----- " + name + " @ " + entry.version + " -----\n\n" + license);
      break;
    } catch (error) {
      if (error.code !== "ENOENT") throw error;
    }
  }
}
await writeFile(join(root, "THIRD_PARTY_LICENSES.txt"), licenses.join(""), "utf8");
