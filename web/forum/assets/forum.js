/* Pursae Forum v0.2.5: optional enhancements. Every feature works without this file; it only
   counts topic views on cached pages, refreshes "who is online", and sends likes without a reload.
   The page's configuration is <script id="pf-config" type="application/json"> (forumHead). */
(function () {
  "use strict";
  var el = document.getElementById("pf-config");
  if (!el || !window.fetch) return;
  var cfg;
  try { cfg = JSON.parse(el.textContent || "{}"); } catch (e) { return; }
  var api = cfg.api || "";
  if (!api) return;

  function send(method, path, body) {
    var headers = { "Content-Type": "application/json" };
    if (cfg.nonce) headers["X-WP-Nonce"] = cfg.nonce;
    return fetch(api + path, { method: method, credentials: "same-origin", headers: headers,
      body: body ? JSON.stringify(body) : undefined }).then(function (r) {
      if (!r.ok) throw new Error(String(r.status));
      return r.json();
    });
  }
  function setAll(sel, text) {
    var list = document.querySelectorAll(sel);
    for (var i = 0; i < list.length; i++) list[i].textContent = text;
  }

  // Views: once per page load (the server drops repeats, robots and floods).
  if (cfg.topic) {
    send("POST", "topics/" + cfg.topic + "/view").then(function (d) {
      if (typeof d.views === "number") {
        var v = document.querySelectorAll("[data-pf-views-count]");
        for (var i = 0; i < v.length; i++) v[i].textContent = d.views;
      }
    }).catch(function () {});
  }

  // Who is online: mark this visitor, refresh the counts now and every two minutes.
  function presence() {
    send("POST", "presence", cfg.forum ? { forum: cfg.forum } : {}).then(function (d) {
      if (typeof d.online === "number") setAll('[data-pf-online="site"]', d.online);
      if (typeof d.forum_online === "number") setAll('[data-pf-online="forum"]', d.forum_online);
    }).catch(function () {});
  }
  presence();
  setInterval(function () { if (!document.hidden) presence(); }, 120000);

  // Likes without a reload (members only: the page carries the REST nonce).
  if (cfg.nonce) {
    document.addEventListener("submit", function (ev) {
      var form = ev.target;
      if (!form || !form.classList || !form.classList.contains("pf-like-form")) return;
      var btn = form.querySelector("[data-pf-like]");
      var action = form.querySelector('input[name="forum_action"]');
      if (!btn || !action) return;
      ev.preventDefault();
      var liked = action.value === "like";
      send(liked ? "POST" : "DELETE", "posts/" + btn.getAttribute("data-pf-like") + "/like").then(function (d) {
        btn.setAttribute("aria-pressed", d.liked ? "true" : "false");
        action.value = d.liked ? "unlike" : "like";
        var n = btn.querySelector(".pf-like-count");
        if (n && typeof d.likes === "number") n.textContent = d.likes;
      }).catch(function () { form.submit(); });
    });
  }
})();
