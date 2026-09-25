/* dashboard — search, hotkeys, clocks, folding, confirmations. No framework. */
(function () {
  "use strict";

  var d = document;
  var CLOCK_TICK_MS = 1000;
  var SEARCH_KEY = "/";

  function csrf() {
    var meta = d.querySelector('meta[name="csrf"]');
    return meta ? meta.getAttribute("content") || "" : "";
  }

  // POST without reload (fold state), CSRF via header.
  function post(url, fields) {
    var body = new URLSearchParams(fields || {});
    return fetch(url, {
      method: "POST",
      headers: { "X-CSRF-Token": csrf(), "Content-Type": "application/x-www-form-urlencoded" },
      body: body,
      credentials: "same-origin"
    });
  }

  function isTyping(target) {
    var tag = (target && target.tagName) || "";
    return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || (target && target.isContentEditable);
  }

  // ── Search: filter link tiles; arrows pick a hit, Enter opens it or the web search ──
  var selected = 0;

  function visibleTiles() {
    return [].filter.call(d.querySelectorAll(".launch"), function (a) {
      return !a.closest(".tile-slot").hidden;
    });
  }

  function filter(query) {
    var q = query.trim().toLowerCase();
    var hits = 0;

    [].forEach.call(d.querySelectorAll(".tile-slot"), function (slot) {
      var link = slot.querySelector(".launch");
      if (!link) {
        slot.hidden = q !== "";
        return;
      }
      var match = !q || (link.getAttribute("data-search") || "").toLowerCase().indexOf(q) >= 0;
      slot.hidden = !match;
      link.classList.remove("is-first");
      if (match) {
        hits += 1;
      }
    });

    [].forEach.call(d.querySelectorAll(".dsec"), function (sec) {
      var any = sec.querySelector(".tile-slot:not([hidden])");
      sec.hidden = q !== "" && !any;
      if (q) {
        sec.classList.remove("is-collapsed");
      }
    });

    selected = 0;
    mark();

    var empty = d.querySelector(".search-empty");
    if (empty) {
      empty.hidden = !(q && hits === 0);
    }
  }

  // mark highlights the selected hit ("is-first" keeps its old name).
  function mark() {
    var input = d.getElementById("search");
    var searching = input && input.value.trim() !== "";
    visibleTiles().forEach(function (a, i) {
      a.classList.toggle("is-first", searching && i === selected);
    });
  }

  // move steps the selection by delta, wrapping at both ends.
  function move(delta) {
    var tiles = visibleTiles();
    if (!tiles.length) {
      return;
    }
    selected = (selected + delta + tiles.length) % tiles.length;
    mark();
    tiles[selected].scrollIntoView({ block: "nearest" });
  }

  function openSearch(input) {
    var q = input.value.trim();
    if (!q) {
      return;
    }

    var hit = visibleTiles()[selected];
    if (hit) {
      hit.click();
      return;
    }

    var engine = input.getAttribute("data-engine");
    if (engine) {
      window.location.href = engine.replace("{query}", encodeURIComponent(q));
    }
  }

  function setupSearch() {
    var input = d.getElementById("search");
    if (!input) {
      return;
    }

    input.addEventListener("input", function () {
      filter(input.value);
    });
    input.addEventListener("keydown", function (e) {
      if (e.key === "Enter") {
        e.preventDefault();
        openSearch(input);
      }
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        move(e.key === "ArrowDown" ? 1 : -1);
      }
      if (e.key === "Escape") {
        input.value = "";
        filter("");
        input.blur();
      }
    });
  }

  // ── Hotkeys: "/" focuses search, tile hotkeys open links ──
  function setupHotkeys() {
    d.addEventListener("keydown", function (e) {
      if (e.ctrlKey || e.metaKey || e.altKey || isTyping(e.target)) {
        return;
      }

      if (e.key === SEARCH_KEY) {
        var input = d.getElementById("search");
        if (input) {
          e.preventDefault();
          input.focus();
        }
        return;
      }

      var tile = d.querySelector('.launch[data-hotkey="' + CSS.escape(e.key) + '"]');
      if (tile) {
        e.preventDefault();
        tile.click();
      }
    });
  }

  // ── Context menu on links: new tab, same tab, copy address ──
  // Shift + right click keeps the browser's own menu.
  function setupContextMenu() {
    var menu = d.getElementById("ctx-menu");
    if (!menu) {
      return;
    }
    var target = "";

    function close() {
      menu.hidden = true;
    }

    d.addEventListener("contextmenu", function (e) {
      var link = e.target.closest && e.target.closest(".launch, .launch-items a");
      if (!link || e.shiftKey) {
        close();
        return;
      }
      e.preventDefault();
      target = link.href;
      menu.hidden = false;

      // Keep the menu inside the viewport.
      var x = Math.min(e.clientX, window.innerWidth - menu.offsetWidth - 4);
      var y = Math.min(e.clientY, window.innerHeight - menu.offsetHeight - 4);
      menu.style.left = Math.max(0, x) + "px";
      menu.style.top = Math.max(0, y) + "px";
      menu.querySelector("button").focus();
    });

    menu.addEventListener("click", function (e) {
      var btn = e.target.closest("button");
      if (!btn) {
        return;
      }
      var act = btn.getAttribute("data-act");
      if (act === "newtab") {
        window.open(target, "_blank", "noopener");
      } else if (act === "sametab") {
        window.location.href = target;
      } else if (act === "copy" && navigator.clipboard) {
        navigator.clipboard.writeText(target);
      }
      close();
    });

    d.addEventListener("click", function (e) {
      if (!menu.contains(e.target)) {
        close();
      }
    });
    d.addEventListener("keydown", function (e) {
      if (e.key === "Escape") {
        close();
      }
    });
    window.addEventListener("scroll", close, { passive: true });
    window.addEventListener("blur", close);
  }

  // ── Clocks ──
  function tick() {
    [].forEach.call(d.querySelectorAll(".clock"), function (el) {
      var zone = el.getAttribute("data-tz");
      var locale = el.getAttribute("data-locale") || undefined;
      var seconds = el.getAttribute("data-seconds") === "yes";
      var now = new Date();
      var time = { hour: "2-digit", minute: "2-digit", timeZone: zone };
      if (seconds) {
        time.second = "2-digit";
      }

      try {
        el.querySelector(".clock-time").textContent = now.toLocaleTimeString(locale, time);
        var date = el.querySelector(".clock-date");
        if (date) {
          date.textContent = now.toLocaleDateString(locale, { weekday: "long", day: "numeric", month: "long", timeZone: zone });
        }
      } catch (err) {
        el.querySelector(".clock-time").textContent = "?";
      }
    });
  }

  // ── Folding: instant in the page, stored in the personal overlay ──
  function setupFolding() {
    d.addEventListener("click", function (e) {
      var btn = e.target.closest && e.target.closest(".fold-btn");
      if (!btn) {
        return;
      }

      var sec = btn.closest(".dsec");
      var closed = sec.classList.toggle("is-collapsed");
      btn.setAttribute("aria-expanded", String(!closed));
      post(btn.getAttribute("data-fold"), { state: closed ? "closed" : "open" });
    });
  }

  // ── Confirm destructive forms (no inline handlers: CSP) ──
  function setupConfirm() {
    d.addEventListener("submit", function (e) {
      var form = e.target.closest && e.target.closest("form[data-confirm]");
      if (form && !window.confirm(form.getAttribute("data-confirm"))) {
        e.preventDefault();
      }
    }, true);
  }

  // ── Selects that submit their form on change (no inline handlers: CSP) ──
  function setupAutosubmit() {
    d.addEventListener("change", function (e) {
      var el = e.target.closest && e.target.closest("[data-autosubmit]");
      if (el && el.form) {
        el.form.requestSubmit();
      }
    });
  }

  d.addEventListener("DOMContentLoaded", function () {
    setupAutosubmit();
    setupSearch();
    setupHotkeys();
    setupFolding();
    setupContextMenu();
    setupConfirm();
    tick();
    window.setInterval(tick, CLOCK_TICK_MS);
  });
})();
