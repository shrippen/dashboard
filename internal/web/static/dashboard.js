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

  // ── Command palette (Ctrl+K) and shortcut help (?) ──
  var PALETTE_MAX = 12;
  var paletteItems = null;
  var paletteSel = 0;

  function paletteMatches(q) {
    q = q.trim().toLowerCase();
    return (paletteItems || []).filter(function (it) {
      return !q || (it.title + " " + (it.detail || "") + " " + it.url).toLowerCase().indexOf(q) >= 0;
    }).slice(0, PALETTE_MAX);
  }

  function renderPalette() {
    var list = d.getElementById("palette-list");
    var q = d.getElementById("palette-q").value;
    var hits = paletteMatches(q);
    paletteSel = Math.min(paletteSel, Math.max(hits.length - 1, 0));
    list.innerHTML = "";
    hits.forEach(function (it, i) {
      var li = d.createElement("li");
      li.setAttribute("role", "option");
      li.setAttribute("aria-selected", String(i === paletteSel));
      li.dataset.kind = it.kind;
      var a = d.createElement("a");
      a.href = it.url;
      if (it.kind === "link") {
        a.target = "_blank";
        a.rel = "noopener noreferrer";
      }
      a.textContent = it.title;
      li.appendChild(a);
      if (it.detail) {
        var small = d.createElement("small");
        small.textContent = it.detail;
        li.appendChild(small);
      }
      list.appendChild(li);
    });
  }

  function openPalette() {
    var dlg = d.getElementById("palette");
    if (!dlg || dlg.open) {
      return;
    }
    dlg.showModal();
    var input = d.getElementById("palette-q");
    input.value = "";
    paletteSel = 0;
    if (paletteItems) {
      renderPalette();
      return;
    }
    fetch("/palette.json", { credentials: "same-origin" }).then(function (r) {
      return r.ok ? r.json() : [];
    }).then(function (items) {
      paletteItems = items || [];
      renderPalette();
    });
  }

  function setupPalette() {
    var dlg = d.getElementById("palette");
    if (!dlg) {
      return;
    }
    var input = d.getElementById("palette-q");
    input.addEventListener("input", function () {
      paletteSel = 0;
      renderPalette();
    });
    input.addEventListener("keydown", function (e) {
      var count = d.getElementById("palette-list").children.length;
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        if (count) {
          paletteSel = (paletteSel + (e.key === "ArrowDown" ? 1 : -1) + count) % count;
          renderPalette();
        }
      }
      if (e.key === "Enter") {
        e.preventDefault();
        var link = d.querySelector('#palette-list li[aria-selected="true"] a');
        if (link) {
          link.click();
          dlg.close();
        }
      }
    });
    d.addEventListener("click", function (e) {
      if (e.target.closest && e.target.closest('[data-open="palette"]')) {
        openPalette();
      }
    });
    d.addEventListener("keydown", function (e) {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        openPalette();
        return;
      }
      if (e.key === "?" && !isTyping(e.target)) {
        var help = d.getElementById("shortcuts");
        if (help && !help.open) {
          e.preventDefault();
          help.showModal();
        }
      }
    });
  }

  // ── Click counting for "frequently used" (beacon: never delays navigation) ──
  function setupClicks() {
    d.addEventListener("click", function (e) {
      var link = e.target.closest && e.target.closest("[data-click]");
      if (!link || !navigator.sendBeacon) {
        return;
      }
      var form = new FormData();
      form.append("csrf", csrf());
      navigator.sendBeacon("/clicks/" + link.getAttribute("data-click"), form);
    });
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

  // ── Header menus (<details>): one open at a time, closed by outside click or Esc ──
  function setupMenus() {
    d.addEventListener("click", function (e) {
      d.querySelectorAll("details.menu[open]").forEach(function (m) {
        if (!m.contains(e.target)) {
          m.open = false;
        }
      });
    });
    d.addEventListener("keydown", function (e) {
      if (e.key === "Escape") {
        d.querySelectorAll("details.menu[open]").forEach(function (m) { m.open = false; });
      }
    });
  }

  // ── Wall display: fullscreen on first tap, rotate boards, dim at night ──
  var KIOSK_DIM_CHECK_MS = 60000;

  function inDim(spec, hour) {
    var parts = spec.split("-");
    var from = +parts[0], to = +parts[1];
    return from <= to ? hour >= from && hour < to : hour >= from || hour < to;
  }

  function setupKiosk() {
    var body = d.body;
    if (!body.classList.contains("is-kiosk")) {
      return;
    }
    d.addEventListener("click", function () {
      if (!d.fullscreenElement && d.documentElement.requestFullscreen) {
        d.documentElement.requestFullscreen().catch(function () {});
      }
    }, { once: true });

    var next = body.dataset.kioskNext, every = +body.dataset.kioskEvery;
    if (next && every > 0) {
      window.setTimeout(function () { window.location.href = next; }, every * 1000);
    }

    var dim = body.dataset.kioskDim;
    if (!dim) {
      return;
    }
    var check = function () { body.classList.toggle("is-dim", inDim(dim, new Date().getHours())); };
    check();
    window.setInterval(check, KIOSK_DIM_CHECK_MS);
  }

  // ── Offline view: service worker keeps the last state, banner says so ──
  function setupOffline() {
    if ("serviceWorker" in navigator) {
      navigator.serviceWorker.register("/sw.js").catch(function () {});
    }
    var meta = d.querySelector('meta[name="offline-note"]');
    if (!meta) {
      return;
    }
    var note = d.createElement("p");
    note.className = "offline-note";
    note.textContent = meta.content;
    note.hidden = navigator.onLine;
    d.body.prepend(note);
    window.addEventListener("online", function () { note.hidden = true; });
    window.addEventListener("offline", function () { note.hidden = false; });
  }

  d.addEventListener("DOMContentLoaded", function () {
    setupAutosubmit();
    setupMenus();
    setupKiosk();
    setupOffline();
    setupSearch();
    setupHotkeys();
    setupFolding();
    setupContextMenu();
    setupPalette();
    setupClicks();
    setupConfirm();
    tick();
    window.setInterval(tick, CLOCK_TICK_MS);
  });
})();
