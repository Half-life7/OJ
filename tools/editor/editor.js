import { basicSetup } from "codemirror";
import { autocompletion, completeAnyWord, startCompletion } from "@codemirror/autocomplete";
import { copyLineDown, deleteLine, indentLess, indentMore, redo, toggleComment, undo } from "@codemirror/commands";
import { indentUnit } from "@codemirror/language";
import { openSearchPanel } from "@codemirror/search";
import { Compartment, EditorSelection, EditorState, Prec } from "@codemirror/state";
import { EditorView, keymap } from "@codemirror/view";
import { cpp } from "@codemirror/lang-cpp";
import { go } from "@codemirror/lang-go";
import { java } from "@codemirror/lang-java";
import { javascript } from "@codemirror/lang-javascript";
import { markdown } from "@codemirror/lang-markdown";
import { python } from "@codemirror/lang-python";

const languages = { cpp17: cpp, go, java, javascript, markdown, python3: python };
const languageNames = {
  cpp17: "C++17",
  go: "Go",
  java: "Java",
  javascript: "JavaScript",
  markdown: "Markdown",
  python3: "Python 3",
  text: "JSON",
};

const dictionaries = {
  cpp17: [
    "alignas", "alignof", "and", "asm", "auto", "bool", "break", "case", "catch", "char", "class", "const",
    "constexpr", "continue", "decltype", "default", "delete", "do", "double", "else", "enum", "explicit",
    "export", "extern", "false", "float", "for", "friend", "if", "inline", "int", "long", "namespace",
    "new", "noexcept", "nullptr", "operator", "or", "private", "protected", "public", "register", "return",
    "short", "signed", "sizeof", "static", "struct", "switch", "template", "this", "throw", "true", "try",
    "typedef", "typename", "union", "unsigned", "using", "virtual", "void", "volatile", "while",
    "std", "vector", "string", "map", "set", "unordered_map", "unordered_set", "queue", "priority_queue",
    "stack", "deque", "pair", "tuple", "array", "sort", "stable_sort", "reverse", "lower_bound",
    "upper_bound", "binary_search", "accumulate", "min", "max", "swap", "cout", "cin", "cerr", "endl",
  ],
  go: [
    "break", "case", "chan", "const", "continue", "default", "defer", "else", "fallthrough", "for", "func",
    "go", "goto", "if", "import", "interface", "map", "package", "range", "return", "select", "struct",
    "switch", "type", "var", "append", "cap", "close", "complex", "copy", "delete", "imag", "len", "make",
    "new", "panic", "print", "println", "real", "recover", "error", "string", "int", "int64", "float64",
    "bool", "byte", "rune", "fmt", "bufio", "os", "sort", "strings", "strconv", "math", "time",
  ],
  python3: [
    "and", "as", "assert", "async", "await", "break", "class", "continue", "def", "del", "elif", "else",
    "except", "False", "finally", "for", "from", "global", "if", "import", "in", "is", "lambda", "None",
    "nonlocal", "not", "or", "pass", "raise", "return", "True", "try", "while", "with", "yield",
    "list", "dict", "set", "tuple", "str", "int", "float", "bool", "len", "range", "enumerate", "zip",
    "map", "filter", "sum", "min", "max", "sorted", "reversed", "print", "input", "open", "abs", "all",
    "any", "bytes", "round", "staticmethod", "classmethod", "property",
  ],
  java: [
    "abstract", "assert", "boolean", "break", "byte", "case", "catch", "char", "class", "const",
    "continue", "default", "do", "double", "else", "enum", "extends", "final", "finally", "float", "for",
    "if", "implements", "import", "instanceof", "int", "interface", "long", "native", "new", "package",
    "private", "protected", "public", "return", "short", "static", "strictfp", "super", "switch",
    "synchronized", "this", "throw", "throws", "transient", "try", "void", "volatile", "while",
    "String", "StringBuilder", "Math", "Integer", "Long", "Double", "Boolean", "List", "ArrayList",
    "Map", "HashMap", "Set", "HashSet", "Queue", "Deque", "ArrayDeque", "Arrays", "Collections",
    "System", "Scanner", "BufferedReader", "IOException",
  ],
  javascript: [
    "async", "await", "break", "case", "catch", "class", "const", "continue", "debugger", "default",
    "delete", "do", "else", "export", "extends", "false", "finally", "for", "from", "function", "get",
    "if", "import", "in", "instanceof", "let", "new", "null", "of", "return", "set", "static", "super",
    "switch", "this", "throw", "true", "try", "typeof", "undefined", "var", "void", "while", "with",
    "yield", "console", "Array", "BigInt", "Boolean", "Date", "Error", "JSON", "Map", "Math", "Number",
    "Object", "Promise", "RegExp", "Set", "String", "Symbol", "parseInt", "parseFloat", "require",
  ],
  markdown: ["标题", "粗体", "斜体", "链接", "行内代码", "代码块", "引用", "无序列表", "有序列表", "表格"],
  text: ["true", "false", "null"],
};

const snippets = {
  cpp17: [
    { label: "cpp-template", detail: "竞赛程序模板", apply: "#include <bits/stdc++.h>\nusing namespace std;\n\nint main() {\n    ios::sync_with_stdio(false);\n    cin.tie(nullptr);\n    \n    return 0;\n}" },
    { label: "for-range", detail: "范围循环", apply: "for (const auto &item : values) {\n    \n}" },
    { label: "pair", detail: "pair 声明", apply: "pair<int, int> value;" },
  ],
  go: [
    { label: "package", detail: "package main", apply: "package main\n\nfunc main() {\n    \n}" },
    { label: "read-all", detail: "读取标准输入", apply: "data, err := io.ReadAll(os.Stdin)\nif err != nil {\n    panic(err)\n}" },
    { label: "scanner-loop", detail: "逐行读取", apply: "scanner := bufio.NewScanner(os.Stdin)\nfor scanner.Scan() {\n    \n}" },
  ],
  python3: [
    { label: "main", detail: "Python 主程序", apply: "def main():\n    \n\nif __name__ == \"__main__\":\n    main()" },
    { label: "input-int", detail: "读取整数", apply: "value = int(input())" },
    { label: "input-list", detail: "读取整数列表", apply: "values = list(map(int, input().split()))" },
  ],
  java: [
    { label: "main-class", detail: "Java 主类", apply: "import java.util.*;\n\npublic class Main {\n    public static void main(String[] args) {\n        Scanner scanner = new Scanner(System.in);\n        \n    }\n}" },
    { label: "read-long", detail: "读取 long", apply: "long value = scanner.nextLong();" },
  ],
  javascript: [
    { label: "node-main", detail: "Node.js 主程序", apply: "const input = require(\"fs\").readFileSync(0, \"utf8\");\n\nfunction main() {\n    \n}\n\nmain();" },
    { label: "read-lines", detail: "读取多行输入", apply: "const lines = input.trim().split(/\\r?\\n/);" },
  ],
  markdown: [
    { label: "题目模板", detail: "Markdown 题目结构", apply: "## 题目描述\n\n## 输入格式\n\n## 输出格式\n\n## 样例\n\n```text\n\n```" },
    { label: "代码块", detail: "带语言代码块", apply: "```cpp\n\n```" },
    { label: "表格", detail: "Markdown 表格", apply: "| 列 1 | 列 2 |\n| --- | --- |\n|  |  |" },
  ],
  text: [],
};

function completionOptions(language) {
  const options = new Map();
  for (const label of dictionaries[language] || []) {
    options.set(label, { label, type: "keyword" });
  }
  for (const snippet of snippets[language] || []) {
    options.set(snippet.label, { ...snippet, type: "text" });
  }
  return Array.from(options.values());
}

function dictionarySource(language) {
  const options = completionOptions(language);
  return (context) => {
    const word = context.matchBefore(/[_$A-Za-z][_$A-Za-z0-9]*/);
    if (!word || word.from === word.to && !context.explicit) return null;
    return {
      from: word.from,
      options,
      validFor: /^[_$A-Za-z][_$A-Za-z0-9]*$/,
    };
  };
}

function languageSupport(language) {
  const support = languages[language] ? languages[language]() : [];
  const source = dictionarySource(language);
  if (support?.data?.of) {
    return [support, support.data.of({ autocomplete: [source, completeAnyWord], wordChars: "_$" })];
  }
  return [support, autocompletion({ override: [source, completeAnyWord], activateOnTyping: true })];
}

function wrapSelection(view, before, after = before) {
  const transaction = view.state.changeByRange((range) => ({
    changes: [
      { from: range.from, insert: before },
      { from: range.to, insert: after },
    ],
    range: EditorSelection.range(range.from + before.length, range.to + before.length),
  }));
  view.dispatch({ ...transaction, userEvent: "input.format" });
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
  view.dispatch({ changes, userEvent: "input.format" });
  view.focus();
  return true;
}

function indentAtCursor(view) {
  const ranges = view.state.selection.ranges;
  if (ranges.some((range) => !range.empty)) return indentMore(view);
  const transaction = view.state.changeByRange((range) => ({
    changes: { from: range.from, insert: "    " },
    range: EditorSelection.cursor(range.from + 4),
  }));
  view.dispatch({ ...transaction, scrollIntoView: true, userEvent: "input.indent" });
  return true;
}

function outdentAtCursor(view) {
  const ranges = view.state.selection.ranges;
  if (ranges.some((range) => !range.empty)) return indentLess(view);
  const changes = [];
  for (const range of ranges) {
    const line = view.state.doc.lineAt(range.head);
    const leading = view.state.sliceDoc(line.from, Math.min(range.head, line.from + 4));
    const count = leading.match(/^ {1,4}/)?.[0].length || 0;
    if (count) changes.push({ from: line.from, to: line.from + count });
  }
  if (changes.length) {
    view.dispatch({ changes, scrollIntoView: true, userEvent: "input.outdent" });
    return true;
  }
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

function button(label, title, run, requiresEditable = true) {
  const element = document.createElement("button");
  element.type = "button";
  element.className = "editor-toolbar-button";
  element.textContent = label;
  element.title = title;
  element.setAttribute("aria-label", title);
  element.dataset.requiresEditable = String(requiresEditable);
  element.addEventListener("mousedown", (event) => event.preventDefault());
  element.addEventListener("click", () => run());
  return element;
}

function toolbarGroup(...children) {
  const group = document.createElement("div");
  group.className = "editor-toolbar-group";
  group.append(...children);
  return group;
}

// The textarea remains a usable fallback when JavaScript or a style nonce is
// unavailable. Callers never need to build or download editor assets at runtime.
export function mount(textarea, initialLanguage = "cpp17", options = {}) {
  const nonce = document.querySelector('meta[name="csp-nonce"]')?.content;
  if (!nonce || !textarea) return null;
  let language = initialLanguage;
  const markdownMode = language === "markdown";
  const syntax = new Compartment();
  const editing = new Compartment();
  const wrapping = new Compartment();
  let wrapped = false;
  let disabled = false;
  let view;

  const shell = document.createElement("div");
  shell.className = "code-editor-shell";
  const toolbar = document.createElement("div");
  toolbar.className = "editor-toolbar";
  toolbar.setAttribute("aria-label", markdownMode ? "Markdown 快捷工具栏" : "代码编辑工具栏");
  const host = document.createElement("div");
  host.className = "code-editor";
  const status = document.createElement("div");
  status.className = "editor-statusbar";
  status.setAttribute("aria-live", "polite");
  const statusPosition = document.createElement("span");
  const statusSelection = document.createElement("span");
  const statusLanguage = document.createElement("span");
  status.append(statusPosition, statusSelection, statusLanguage);
  shell.append(toolbar, host, status);
  textarea.insertAdjacentElement("afterend", shell);

  const common = [
    button("撤销", "Ctrl/Cmd+Z：撤销", () => undo(view)),
    button("重做", "Ctrl/Cmd+Shift+Z：重做", () => redo(view)),
    button("查找", "Ctrl/Cmd+F：查找与替换", () => openSearchPanel(view)),
    button("补全", "Ctrl/Cmd+Space：词典与文档词补全", () => startCompletion(view)),
  ];
  const lineTools = [
    button("注释", "Ctrl/Cmd+/：切换注释", () => toggleComment(view)),
    button("复制行", "Shift+Alt+Down：复制当前行", () => copyLineDown(view)),
    button("删除行", "Shift+Ctrl+K：删除当前行", () => deleteLine(view)),
  ];
  if (markdownMode) {
    for (const command of markdownShortcuts) {
      common.push(button(command.label, command.title, () => command.run(view)));
    }
    if (typeof options.onPreview === "function") {
      common.push(button("预览", "Ctrl/Cmd+Enter：安全预览", () => options.onPreview(view.state.doc.toString())));
    }
  }
  const wrapButton = button("换行", "切换自动换行", () => {
    wrapped = !wrapped;
    view.dispatch({ effects: wrapping.reconfigure(wrapped ? EditorView.lineWrapping : []) });
    wrapButton.classList.toggle("active", wrapped);
    wrapButton.setAttribute("aria-pressed", String(wrapped));
  }, false);
  wrapButton.setAttribute("aria-pressed", "false");
  const expandButton = button("放大", "放大到全屏编辑", () => {
    const expanded = shell.classList.toggle("is-expanded");
    document.body.classList.toggle("editor-expanded-open", expanded);
    expandButton.textContent = expanded ? "缩小" : "放大";
    expandButton.setAttribute("aria-pressed", String(expanded));
    view.requestMeasure();
    view.focus();
  }, false);
  expandButton.setAttribute("aria-pressed", "false");
  toolbar.append(toolbarGroup(...common), toolbarGroup(...lineTools), toolbarGroup(wrapButton, expandButton));
  toolbar.append(Object.assign(document.createElement("span"), {
    className: "editor-hints",
    textContent: markdownMode ? "Tab 缩进 · Ctrl/Cmd+/ 注释 · Ctrl/Cmd+Enter 预览" : "Tab / Shift+Tab 缩进 · Ctrl/Cmd+/ 注释 · Ctrl/Cmd+Space 补全",
  }));

  function updateStatus(current = view) {
    if (!current) return;
    const head = current.state.selection.main.head;
    const line = current.state.doc.lineAt(head);
    const selected = current.state.selection.ranges.reduce((sum, range) => sum + (range.to - range.from), 0);
    statusPosition.textContent = `行 ${line.number}，列 ${head - line.from + 1}`;
    statusSelection.textContent = selected ? `已选 ${selected} 字符` : "未选择";
    statusLanguage.textContent = `${languageNames[language] || language}${disabled ? " · 只读" : ""}`;
  }

  try {
    const keymaps = [
      { key: "Tab", run: indentAtCursor },
      { key: "Shift-Tab", run: outdentAtCursor },
      { key: "Mod-/", run: toggleComment },
    ];
    if (markdownMode) {
      for (const command of markdownShortcuts) keymaps.push({ key: command.key, run: command.run });
      if (typeof options.onPreview === "function") {
        keymaps.push({ key: "Mod-Enter", run: () => {
          options.onPreview(view.state.doc.toString());
          return true;
        } });
      }
    }
    view = new EditorView({
      parent: host,
      state: EditorState.create({
        doc: textarea.value,
        extensions: [
          basicSetup,
          EditorView.cspNonce.of(nonce),
          EditorView.contentAttributes.of({
            "aria-label": options.ariaLabel || (markdownMode ? "Markdown 内容" : "源代码"),
            spellcheck: "false",
            autocapitalize: "off",
          }),
          EditorState.tabSize.of(4),
          indentUnit.of("    "),
          Prec.highest(keymap.of(keymaps)),
          syntax.of(languageSupport(language)),
          editing.of([EditorState.readOnly.of(false), EditorView.editable.of(true)]),
          wrapping.of([]),
          EditorView.updateListener.of((update) => {
            if (update.docChanged) {
              textarea.value = update.state.doc.toString();
              textarea.dispatchEvent(new Event("input", { bubbles: true }));
            }
            if (update.docChanged || update.selectionSet || update.focusChanged) updateStatus(view);
          }),
        ],
      }),
    });
    textarea.hidden = true;
    // HTML required validation cannot focus a hidden textarea. Server and
    // submission handlers enforce the same non-empty/size contract for both UIs.
    textarea.required = false;
    updateStatus(view);
    return {
      getValue: () => view.state.doc.toString(),
      setValue(value) { view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: String(value ?? "") } }); },
      setLanguage(value) {
        language = (languages[value] || value === "text") ? value : "cpp17";
        view.dispatch({ effects: syntax.reconfigure(languageSupport(language)) });
        updateStatus(view);
      },
      setDisabled(value) {
        disabled = value;
        view.dispatch({ effects: editing.reconfigure([EditorState.readOnly.of(value), EditorView.editable.of(!value)]) });
        for (const item of toolbar.querySelectorAll("button")) {
          if (item.dataset.requiresEditable !== "false") item.disabled = value;
        }
        updateStatus(view);
      },
      focus: () => view.focus(),
      requestMeasure: () => view.requestMeasure(),
      preview() { return options.onPreview?.(view.state.doc.toString()); },
      destroy() {
        view.destroy();
        shell.remove();
        document.body.classList.remove("editor-expanded-open");
        textarea.hidden = false;
        textarea.required = true;
      },
    };
  } catch {
    if (view) view.destroy();
    shell.remove();
    document.body.classList.remove("editor-expanded-open");
    textarea.hidden = false;
    return null;
  }
}
