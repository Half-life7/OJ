# Local code editor assets

The Go binary embeds `webui/static/editor.js`, a local CodeMirror 6 bundle with C++17, Go, Python 3 and Java syntax support. It does not load scripts, fonts or styles from a CDN. Normal Go development and deployment do not require Node or an editor build step.

Dependencies are pinned exactly in `package.json`; `package-lock.json` records the complete dependency graph and integrity hashes. The generated bundle is committed with `THIRD_PARTY_LICENSES.txt`.

To rebuild after an intentional editor update, use Node 20 or newer:

```powershell
Set-Location tools/editor
npm.cmd ci --ignore-scripts --no-audit --no-fund
npm.cmd run build
```

On other shells use `npm` instead of `npm.cmd`. `node_modules` is ignored. The build uses esbuild's platform package; third-party install scripts are disabled. Commit the source, lockfile, notices and generated bundle together.

The editor reads the request-specific nonce from `<meta name="csp-nonce">` and passes it to `EditorView.cspNonce.of(nonce)` for CodeMirror's generated style sheets. Keep the application Content Security Policy strict; do not add `unsafe-inline`. Without a nonce or when editor initialization fails, the source textarea stays usable.

Reference: [CodeMirror view API](https://codemirror.net/docs/ref/#view.EditorView^cspNonce).
