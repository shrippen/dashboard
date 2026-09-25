// Turns <textarea data-code="yaml|css"> into a CodeMirror editor. The
// textarea stays the form field: CodeMirror writes back on submit.
document.addEventListener("DOMContentLoaded", () => {
  if (!window.CodeMirror) {
    return;
  }
  for (const area of document.querySelectorAll("textarea[data-code]")) {
    const editor = CodeMirror.fromTextArea(area, {
      mode: area.dataset.code,
      lineNumbers: true,
      indentUnit: 2,
      tabSize: 2,
      // YAML forbids tabs: indent with spaces.
      extraKeys: { Tab: (cm) => cm.execCommand("insertSoftTab") },
    });
    editor.getWrapperElement().classList.add(...area.classList);
    editor.getInputField().setAttribute("aria-label", area.getAttribute("aria-label") || "");
  }
});
