import { basicSetup } from "codemirror";
import { Compartment, EditorState } from "@codemirror/state";
import { EditorView } from "@codemirror/view";
import { cpp } from "@codemirror/lang-cpp";
import { go } from "@codemirror/lang-go";
import { java } from "@codemirror/lang-java";
import { python } from "@codemirror/lang-python";

const languages = { cpp17: cpp, go, java, python3: python };

// The textarea remains a usable fallback when JavaScript or a style nonce is
// unavailable. Callers never need to build or download editor assets at runtime.
export function mount(textarea, language = "cpp17") {
  const nonce = document.querySelector('meta[name="csp-nonce"]')?.content;
  if (!nonce || !textarea) return null;
  const syntax = new Compartment();
  const editing = new Compartment();
  const host = document.createElement("div");
  host.className = "code-editor";
  textarea.insertAdjacentElement("afterend", host);
  let view;
  try {
    view = new EditorView({
      parent: host,
      state: EditorState.create({
        doc: textarea.value,
        extensions: [
          basicSetup,
          EditorView.cspNonce.of(nonce),
          EditorView.contentAttributes.of({ "aria-label": "源代码", spellcheck: "false" }),
          syntax.of(languages[language] ? languages[language]() : []),
          editing.of([EditorState.readOnly.of(false), EditorView.editable.of(true)]),
          EditorView.updateListener.of((update) => {
            if (update.docChanged) textarea.value = update.state.doc.toString();
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
      setLanguage(value) { view.dispatch({ effects: syntax.reconfigure(languages[value] ? languages[value]() : []) }); },
      setDisabled(disabled) { view.dispatch({ effects: editing.reconfigure([EditorState.readOnly.of(disabled), EditorView.editable.of(!disabled)]) }); },
      focus: () => view.focus(),
      destroy() { view.destroy(); host.remove(); textarea.hidden = false; textarea.required = true; },
    };
  } catch {
    if (view) view.destroy();
    host.remove();
    textarea.hidden = false;
    return null;
  }
}
