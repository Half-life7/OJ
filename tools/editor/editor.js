import { basicSetup } from "codemirror";
import { Compartment, EditorSelection, EditorState, Prec } from "@codemirror/state";
import { EditorView, keymap } from "@codemirror/view";
import { indentLess, indentMore, toggleComment } from "@codemirror/commands";
import { indentUnit } from "@codemirror/language";
import { cpp } from "@codemirror/lang-cpp";
import { go } from "@codemirror/lang-go";
import { java } from "@codemirror/lang-java";
import { javascript } from "@codemirror/lang-javascript";
import { markdown } from "@codemirror/lang-markdown";
import { python } from "@codemirror/lang-python";

const languages = { cpp17: cpp, go, java, javascript, markdown, python3: python };

function wrapSelection(view, before, after = before) {
  const transaction = view.state.changeByRange((range) => ({
    changes: [
      { from: range.from, insert: before },
      { from: range.to, insert: after },
    ],
    range: EditorSelection.range(range.from + before.length, range.to + before.length),
  }));
  view.dispatch(transaction);
  view.focus();
  return true;
}

function prefixSelectedLines(view, prefix) {
  const first = view.state.doc.lineAt(view.state.selection.main.from).number;
  const last = view.state.doc.lineAt(view.state.selection.main.to).number;
  const changes = [];
  for (let lineNumber = first; lineNumber <= last; lineNumber++) {
    const line = view.state.doc.line(lineNumber);
    changes.push({ from: line.from, insert: prefix });
  }
  view.dispatch({ changes });
  view.focus();
  return true;
}

const markdownShortcuts = [
  { key: "Mod-b", label: "粗体", title: "Ctrl/Cmd+B：粗体", run: (view) => wrapSelection(view, "**") },
  { key: "Mod-i", label: "斜体", title: "Ctrl/Cmd+I：斜体", run: (view) => wrapSelection(view, "_") },
  { key: "Mod-k", label: "链接", title: "Ctrl/Cmd+K：链接", run: (view) => wrapSelection(view, "[", "](https://)") },
  { key: "Mod-`", label: "行内代码", title: "Ctrl/Cmd+`：行内代码", run: (view) => wrapSelection(view, "`") },
  { key: "Mod-Shift-c", label: "代码块", title: "Ctrl/Cmd+Shift+C：代码块", run: (view) => wrapSelection(view, "\n```\n", "\n```\n") },
  { key: "Mod-Shift-1", label: "标题", title: "Ctrl/Cmd+Shift+1：标题", run: (view) => prefixSelectedLines(view, "## ") },
  { key: "Mod-Shift-8", label: "无序列表", title: "Ctrl/Cmd+Shift+8：无序列表", run: (view) => prefixSelectedLines(view, "- ") },
  { key: "Mod-Shift-7", label: "有序列表", title: "Ctrl/Cmd+Shift+7：有序列表", run: (view) => prefixSelectedLines(view, "1. ") },
  { key: "Mod-Shift-9", label: "引用", title: "Ctrl/Cmd+Shift+9：引用", run: (view) => prefixSelectedLines(view, "> ") },
];

function toolbarButton(label, title, run) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "editor-toolbar-button";
  button.textContent = label;
  button.title = title;
  button.setAttribute("aria-label", title);
  button.addEventListener("mousedown", (event) => event.preventDefault());
  button.addEventListener("click", run);
  return button;
}

// The textarea remains a usable fallback when JavaScript or a style nonce is
// unavailable. Callers never need to build or download editor assets at runtime.
export function mount(textarea, language = "cpp17", options = {}) {
  const nonce = document.querySelector('meta[name="csp-nonce"]')?.content;
  if (!nonce || !textarea) return null;
  const syntax = new Compartment();
  const editing = new Compartment();
  const markdownMode = language === "markdown";
  const host = document.createElement("div");
  host.className = "code-editor";
  let toolbar;
  if (markdownMode) {
    toolbar = document.createElement("div");
    toolbar.className = "editor-toolbar markdown-toolbar";
    toolbar.setAttribute("aria-label", "Markdown 快捷工具栏");
    for (const command of markdownShortcuts) {
      toolbar.append(toolbarButton(command.label, command.title, () => command.run(view)));
    }
    if (typeof options.onPreview === "function") {
      toolbar.append(toolbarButton("预览", "Ctrl/Cmd+Enter：安全预览", () => options.onPreview(view.state.doc.toString())));
    }
    toolbar.append(Object.assign(document.createElement("span"), {
      className: "editor-hints",
      textContent: "Tab 缩进 · Ctrl/Cmd+/ 注释 · Ctrl/Cmd+Enter 预览",
    }));
  }
  if (toolbar) {
    textarea.insertAdjacentElement("afterend", toolbar);
    toolbar.insertAdjacentElement("afterend", host);
  } else {
    textarea.insertAdjacentElement("afterend", host);
  }
  const keyboardHint = markdownMode ? null : Object.assign(document.createElement("div"), {
    className: "editor-keyboard-hint",
    textContent: "Tab / Shift+Tab 缩进 · Ctrl/Cmd+/ 切换注释 · 语法高亮",
  });
  if (keyboardHint) host.insertAdjacentElement("afterend", keyboardHint);
  let view;
  try {
    const keymaps = [
      { key: "Tab", run: indentMore },
      { key: "Shift-Tab", run: indentLess },
      { key: "Mod-/", run: toggleComment },
    ];
    if (markdownMode) {
      for (const command of markdownShortcuts) {
        keymaps.push({ key: command.key, run: command.run });
      }
      if (typeof options.onPreview === "function") {
        keymaps.push({ key: "Mod-Enter", run: () => { options.onPreview(view.state.doc.toString()); return true; } });
      }
    }
    view = new EditorView({
      parent: host,
      state: EditorState.create({
        doc: textarea.value,
        extensions: [
          basicSetup,
          EditorView.cspNonce.of(nonce),
          EditorView.contentAttributes.of({ "aria-label": options.ariaLabel || (markdownMode ? "Markdown 内容" : "源代码"), spellcheck: "false" }),
          EditorState.tabSize.of(4),
          indentUnit.of("    "),
          Prec.highest(keymap.of(keymaps)),
          syntax.of(languages[language] ? languages[language]() : []),
          editing.of([EditorState.readOnly.of(false), EditorView.editable.of(true)]),
          EditorView.updateListener.of((update) => {
            if (update.docChanged) {
              textarea.value = update.state.doc.toString();
              textarea.dispatchEvent(new Event("input", { bubbles: true }));
            }
          }),
        ],
      }),
    });
    textarea.hidden = true;
    // HTML required validation cannot focus a hidden textarea. Server and
    // submission handlers enforce the same non-empty/size contract for both UIs.
    textarea.required = false;
    return {
      getValue: () => view.state.doc.toString(),
      setValue(value) { view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: String(value ?? "") } }); },
      setLanguage(value) { view.dispatch({ effects: syntax.reconfigure(languages[value] ? languages[value]() : []) }); },
      setDisabled(disabled) {
        view.dispatch({ effects: editing.reconfigure([EditorState.readOnly.of(disabled), EditorView.editable.of(!disabled)]) });
        for (const button of toolbar?.querySelectorAll("button") || []) button.disabled = disabled;
      },
      focus: () => view.focus(),
      preview() { return options.onPreview?.(view.state.doc.toString()); },
      destroy() { view.destroy(); host.remove(); toolbar?.remove(); keyboardHint?.remove(); textarea.hidden = false; textarea.required = true; },
    };
  } catch {
    if (view) view.destroy();
    host.remove();
    toolbar?.remove();
    keyboardHint?.remove();
    textarea.hidden = false;
    return null;
  }
}
