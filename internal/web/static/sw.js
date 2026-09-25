// Offline view: board pages and their fragments are fetched from the
// network first; the last good answer is kept and served when offline.
// Logout clears it (Clear-Site-Data).
"use strict";

var CACHE = "dashboard-last";
var KEEP = [/^\/$/, /^\/boards\/\d+$/, /^\/widget-fragments\//, /^\/static\//, /^\/icons\//, /^\/theme\//];

function keepable(url) {
  if (url.origin !== self.location.origin || url.searchParams.has("edit") || url.searchParams.has("layout")) {
    return false;
  }
  return KEEP.some(function (re) { return re.test(url.pathname); });
}

self.addEventListener("install", function () { self.skipWaiting(); });
self.addEventListener("activate", function (e) { e.waitUntil(self.clients.claim()); });

self.addEventListener("fetch", function (e) {
  var req = e.request;
  if (req.method !== "GET" || !keepable(new URL(req.url))) {
    return;
  }
  e.respondWith(fetch(req).then(function (res) {
    if (res.ok && res.type === "basic" && !res.redirected) {
      var copy = res.clone();
      caches.open(CACHE).then(function (c) { c.put(req, copy); });
    }
    return res;
  }).catch(function () {
    return caches.match(req).then(function (hit) { return hit || Response.error(); });
  }));
});
