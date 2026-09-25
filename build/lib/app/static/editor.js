/* Drag and drop of tiles. Editors reorder the board, others their own layout. */
(function () {
  "use strict";

  var d = document;

  function csrf() {
    return d.body.getAttribute("data-csrf") || "";
  }

  function layout(board) {
    var result = {};
    [].forEach.call(board.querySelectorAll("[data-sortable]"), function (list) {
      result[list.getAttribute("data-sortable")] = [].map.call(
        list.querySelectorAll(":scope > .tile-slot[data-placement]"),
        function (slot) { return Number(slot.getAttribute("data-placement")); }
      );
    });
    return result;
  }

  function save(board) {
    var body = { version: Number(board.getAttribute("data-version")), layout: layout(board) };
    fetch("/b/" + board.getAttribute("data-board") + "/arrange", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf() },
      body: JSON.stringify(body),
      credentials: "same-origin"
    }).then(function (res) {
      // The board version changed (or somebody else saved): reload for a consistent state.
      window.location.reload();
      return res;
    });
  }

  d.addEventListener("DOMContentLoaded", function () {
    var board = d.querySelector(".board[data-mode]");
    if (!board || !board.getAttribute("data-mode") || typeof Sortable === "undefined") {
      return;
    }

    // Editors may move tiles between sections; personal layouts only within one.
    var shared = board.getAttribute("data-mode") === "board" ? "tiles" : null;
    [].forEach.call(board.querySelectorAll("[data-sortable]"), function (list) {
      Sortable.create(list, {
        group: shared ? { name: shared } : "section-" + list.getAttribute("data-sortable"),
        animation: 120,
        draggable: ".tile-slot[data-placement]",
        filter: "[data-static], a, button, input, select",
        preventOnFilter: false,
        ghostClass: "sortable-ghost",
        onEnd: function () { save(board); }
      });
    });
  });
})();
