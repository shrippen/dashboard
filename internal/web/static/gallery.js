/* Gallery and library: filter cards by search text and "only my connections",
   open the reuse dialog of a set-up tile. */
(function () {
  "use strict";

  var d = document;

  // filter hides cards that don't match, then groups left empty.
  function filter() {
    var q = d.getElementById("gal-q").value.trim().toLowerCase();
    var box = d.getElementById("gal-mine");
    var mine = box ? box.checked : false;
    var any = false;
    [].forEach.call(d.querySelectorAll(".gal-group"), function (group) {
      var shown = 0;
      [].forEach.call(group.querySelectorAll("[data-q]"), function (card) {
        var hit = (!q || (card.getAttribute("data-q") || "").toLowerCase().indexOf(q) >= 0) &&
          (!mine || card.hasAttribute("data-mine"));
        card.hidden = !hit;
        shown += hit ? 1 : 0;
      });
      group.hidden = shown === 0;
      any = any || shown > 0;
    });
    d.querySelector(".gal-none").hidden = any;
  }

  // Registered once: this script stays loaded across soft page changes.
  d.addEventListener("click", function (e) {
    var opener = e.target.closest("[data-open]:not([data-open=\"palette\"])");
    if (!opener) {
      return;
    }
    var dialog = d.getElementById(opener.getAttribute("data-open"));
    if (dialog && dialog.showModal && !dialog.open) {
      dialog.showModal();
    }
  });

  window.andonPage(function () {
    var q = d.getElementById("gal-q");
    if (!q) {
      return;
    }
    q.addEventListener("input", filter);
    var box = d.getElementById("gal-mine");
    if (box) {
      box.addEventListener("change", filter);
    }
  });
})();
