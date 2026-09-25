/* Theme editor: colour pickers mirror text fields; the preview follows live. */
(function () {
  "use strict";

  var d = document;

  function applyPreview() {
    var preview = d.getElementById("theme-preview");
    if (!preview) {
      return;
    }
    var mode = preview.getAttribute("data-mode") || "dark";
    [].forEach.call(d.querySelectorAll("#theme-form [data-token]"), function (input) {
      if (input.getAttribute("data-mode") === mode) {
        preview.style.setProperty(input.getAttribute("data-token"), input.value);
      }
    });
  }

  d.addEventListener("DOMContentLoaded", function () {
    [].forEach.call(d.querySelectorAll("#theme-form input[type=color]"), function (picker) {
      var text = d.querySelector('#theme-form [name="' + picker.getAttribute("data-for") + '"]');
      picker.addEventListener("input", function () {
        text.value = picker.value;
        applyPreview();
      });
      text.addEventListener("input", applyPreview);
    });

    [].forEach.call(d.querySelectorAll("[data-preview-mode]"), function (btn) {
      btn.addEventListener("click", function () {
        var preview = d.getElementById("theme-preview");
        preview.setAttribute("data-mode", btn.getAttribute("data-preview-mode"));
        [].forEach.call(d.querySelectorAll("[data-preview-mode]"), function (b) {
          b.setAttribute("aria-pressed", String(b === btn));
        });
        applyPreview();
      });
    });

    applyPreview();
  });
})();
