"use strict";
// LogDirector web GUI. No framework, no innerHTML: every value from the server goes
// through textContent, so nothing it returns can inject markup.

(() => {
  const POLL_MS = 2000;
  const state = { prefill: {}, topo: { list: [], gid: null, action: null }, session: null, tab: "logs", timer: null, cfg: null, cfgMode: "form", cfgInfo: null, usersTab: 0,
    target: null, nodes: [], clustered: false }; // target: address of the node being driven, null = this one
  const root = document.getElementById("app");

  // Theme: Auto follows the system (prefers-color-scheme); Light and Dark force one.  The choice is kept in this browser only.
  const THEMES = [["auto", "Auto"], ["light", "Light"], ["dark", "Dark"]];
  const themeGet = () => { try { const t = localStorage.getItem("theme"); return THEMES.some(([k]) => k === t) ? t : "auto"; } catch (_) { return "auto"; } };
  const themeApply = (t) => { if (t === "auto") delete document.documentElement.dataset.theme; else document.documentElement.dataset.theme = t; };
  themeApply(themeGet());
  function themeToggle() {
    const svg = (tag, a) => { const e = document.createElementNS("http://www.w3.org/2000/svg", tag); for (const k in a) e.setAttribute(k, a[k]); return e; };
    const ICON = {
      auto: () => [svg("circle", { cx: 8, cy: 8, r: 6 }), svg("path", { d: "M8 2a6 6 0 0 1 0 12z", fill: "currentColor" })],
      light: () => [svg("circle", { cx: 8, cy: 8, r: 3 }), ...[0, 45, 90, 135, 180, 225, 270, 315].map((a) => svg("path", { d: "M8 1.2v1.6", transform: "rotate(" + a + " 8 8)" }))],
      dark: () => [svg("path", { d: "M13.4 9.6A5.6 5.6 0 0 1 6.4 2.6a5.6 5.6 0 1 0 7 7z" })],
    };
    const btn = h("button", { class: "theme-btn", type: "button" });
    const draw = () => {
      const t = themeGet(), i = THEMES.findIndex(([k]) => k === t), next = THEMES[(i + 1) % THEMES.length];
      const s = svg("svg", { viewBox: "0 0 16 16", width: 16, height: 16, fill: "none", stroke: "currentColor", "stroke-width": 1.5, "stroke-linecap": "round", "aria-hidden": "true" });
      for (const e of ICON[t]()) s.append(e);
      btn.replaceChildren(s);
      btn.title = "Theme: " + THEMES[i][1] + (t === "auto" ? " (follows the system)" : "") + ". Click for " + next[1] + ".";
      btn.setAttribute("aria-label", btn.title);
    };
    btn.addEventListener("click", () => {
      const next = THEMES[(THEMES.findIndex(([k]) => k === themeGet()) + 1) % THEMES.length][0];
      try { if (next === "auto") localStorage.removeItem("theme"); else localStorage.setItem("theme", next); } catch (_) { /* no storage: lasts until reload */ }
      themeApply(next); draw();
    });
    draw();
    return h("div", { class: "nav-theme" }, btn);
  }

  // A small eye (struck through while the value is shown), drawn with DOM calls: no innerHTML.
  function eyeIcon(struck) {
    const NS = "http://www.w3.org/2000/svg";
    const el = (n, a) => { const e = document.createElementNS(NS, n); for (const k in a) e.setAttribute(k, a[k]); return e; };
    const svg = el("svg", { viewBox: "0 0 24 24", width: "14", height: "14", fill: "none", stroke: "currentColor", "stroke-width": "2", "stroke-linecap": "round", "stroke-linejoin": "round", "aria-hidden": "true" });
    svg.append(el("path", { d: "M1.5 12S5.5 5 12 5s10.5 7 10.5 7-4 7-10.5 7S1.5 12 1.5 12z" }), el("circle", { cx: "12", cy: "12", r: "3" }));
    if (struck) svg.append(el("path", { d: "M3 3l18 18" }));
    return svg;
  }

  // Every password box except the sign-in one gets an eye button beside it, so a saved password, token or key can be read.
  (function revealPasswords() {
    const decorate = (inp) => {
      if (inp._rv || inp.getAttribute("autocomplete") === "current-password") return;
      inp._rv = true;
      const b = document.createElement("button");
      b.type = "button"; b.className = "reveal"; b.title = "Show";
      b.append(eyeIcon(false));
      b.setAttribute("aria-pressed", "false");
      b.setAttribute("aria-label", "Show " + (inp.getAttribute("aria-label") || "value"));
      b.addEventListener("click", (e) => {
        e.preventDefault(); e.stopPropagation();
        const on = inp.type === "password";
        inp.type = on ? "text" : "password";
        b.title = on ? "Hide" : "Show";
        b.replaceChildren(eyeIcon(on));
        b.setAttribute("aria-pressed", String(on));
      });
      const wrap = document.createElement("span");
      wrap.className = "reveal-wrap";
      inp.replaceWith(wrap);
      wrap.append(inp, b);
    };
    const scan = (n) => {
      if (n.nodeType !== 1) return;
      if (n.matches("input[type=password]")) decorate(n);
      else n.querySelectorAll("input[type=password]").forEach(decorate);
    };
    new MutationObserver((ms) => ms.forEach((m) => m.addedNodes.forEach(scan))).observe(root, { childList: true, subtree: true });
  })();

  // The daemon puts its version in BUILD when it serves this file. After an upgrade the daemon restarts with a new
  // interface; a page left open notices (the session says which version answers) and reloads itself, but not while
  // a dialog is open, a field is being typed in, or a Configure page is open (it may hold changes not yet saved).
  const BUILD = "@@BUILD@@";
  let reloading = false;
  function checkBuild(v) {
    if (reloading || BUILD.charAt(0) === "@" || !v || String(v) === BUILD) return;
    reloading = true;
    const go = () => {
      const a = document.activeElement, typing = a && (a.tagName === "TEXTAREA" || a.tagName === "SELECT" || (a.tagName === "INPUT" && a.type !== "checkbox" && a.type !== "radio" && a.type !== "button"));
      if (document.querySelector("dialog[open]") || typing || String(state.tab).startsWith("cfg")) { setTimeout(go, 3000); return; }
      location.reload();
    };
    go();
  }
  setInterval(() => {
    if (!state.session || document.hidden || reloading) return;
    api("GET", "/api/session", undefined, true).then((s) => checkBuild(s && s.version)).catch(() => { /* the daemon is restarting: look again next time */ });
  }, 15000);

  // ── DOM helper ────────────────────────────────────────────────────────────
  function h(tag, attrs, ...kids) {
    const el = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v === false || v == null) continue;
      if (k === "class") el.className = v;
      else if (k.startsWith("on")) { el.addEventListener(k.slice(2), v); (el.__on = el.__on || {})[k.slice(2)] = v; }
      else if (k === "value") el.value = v;
      else if (v === true) el.setAttribute(k, "");
      else el.setAttribute(k, v);
    }
    for (const kid of kids.flat(Infinity)) {
      if (kid == null || kid === false) continue;
      el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
    }
    return el;
  }
  const clear = (el) => { while (el.firstChild) el.removeChild(el.firstChild); return el; };
  const DOT_DEFAULT = 36;   // Topology: a gateway with more senders than settings.topology_squares_above draws each as a small square
  const EXP_SEEN = 900, EXP_QUIET = 300, EXP_LATE = 600;   // Topology: an sender is drawn while its last message is at most EXP_SEEN seconds old: green, amber after EXP_QUIET, red after EXP_LATE
  // A filter box: words must all match (AND, also written out); OR makes alternatives; a ! before a word (!dns), or NOT, means "does not match".
  // "phx AND !dns" and "phx !dns" both find what has phx and not dns; "phx OR sea" finds either.  `match(test)` asks test(word) for each word.
  function parseFilter(text) {
    const groups = [[]];
    let pend = false;   // a "not" waits for the word it applies to
    for (const raw of String(text || "").toLowerCase().split(/\s+/).filter(Boolean)) {
      if (raw === "and") { pend = false; continue; }
      if (raw === "or") { pend = false; if (groups[groups.length - 1].length) groups.push([]); continue; }
      if (raw === "not") { pend = !pend; continue; }
      const w = raw.replace(/^!+/, ""), neg = ((raw.length - w.length) % 2 === 1) !== pend;
      pend = false;
      if (w) groups[groups.length - 1].push({ w, neg });
    }
    const gs = groups.filter((g) => g.length);
    return { empty: !gs.length, match: (test) => !gs.length || gs.some((g) => g.every((t) => !!test(t.w) !== t.neg)) };
  }
  // morph makes the children of dst the same as the children of src, reusing the elements that are already there (only
  // their text and attributes are patched).  A page that is redrawn on a timer uses it so that what the browser keeps
  // about an element survives the redraw: the position of a horizontal scroll bar, a text selection.  Emptying a
  // container and building it again, which these pages did, destroys every scroll box in it, so a scroll bar that had been
  // moved sprang back and the bars flashed on every redraw.  Only for markup without event handlers: the handlers are
  // not copied.  An inline style is skipped too (the page's policy allows it only through the CSSOM, so the caller sets it).
  function morph(dst, src) {
    const from = Array.from(src.childNodes);
    from.forEach((s, i) => {
      const d = dst.childNodes[i];
      if (!d) { dst.appendChild(s); return; }
      if (d.nodeType !== s.nodeType || d.nodeName !== s.nodeName) { dst.replaceChild(s, d); return; }
      if (s.nodeType !== 1) { if (d.nodeValue !== s.nodeValue) d.nodeValue = s.nodeValue; return; }
      for (const at of Array.from(d.attributes)) if (at.name !== "style" && !s.hasAttribute(at.name)) d.removeAttribute(at.name);
      for (const at of Array.from(s.attributes)) if (at.name !== "style" && d.getAttribute(at.name) !== at.value) d.setAttribute(at.name, at.value);
      // handlers: the new element's replace the old ones (they close over the new data)
      const dOn = d.__on || {}, sOn = s.__on || {};
      for (const ev of Object.keys(dOn)) if (dOn[ev] !== sOn[ev]) d.removeEventListener(ev, dOn[ev]);
      for (const ev of Object.keys(sOn)) if (dOn[ev] !== sOn[ev]) d.addEventListener(ev, sOn[ev]);
      d.__on = sOn;
      // form state is a property, not an attribute; what the person is typing into is left alone
      if (d.nodeName === "INPUT" && d !== document.activeElement) { if (d.type === "checkbox" || d.type === "radio") d.checked = s.checked; else if (d.value !== s.value) d.value = s.value; }
      if (d.nodeName === "INPUT" || d.nodeName === "BUTTON") d.disabled = s.disabled;
      morph(d, s);
    });
    while (dst.childNodes.length > from.length) dst.removeChild(dst.lastChild);
  }
  // fill(el, ...kids): what `clear(el).append(...kids)` does, but el keeps the elements it already has (see morph), so the
  // scroll boxes inside it are not destroyed and re-created by every poll.
  const fill = (el, ...kids) => { morph(el, h("div", {}, ...kids)); return el; };
  // tc(text): a card heading in title case ("Upload a release" -> "Upload a Release").  Short joining words stay small (but not
  // first); a word that already has a capital after its first letter (BGP, NTS, GUI) is left as it is, and so is what follows a
  // hyphen ("Kiss-o'-death", "Policy-Based").
  const SMALL_WORDS = new Set(["a", "an", "and", "as", "at", "but", "by", "for", "in", "nor", "of", "on", "or", "per", "the", "to", "via", "vs"]);
  const tc = (t) => (typeof t !== "string" ? t : t.replace(/(?<![\w'’-])[A-Za-z][A-Za-z'’]*/g, (w, i) => ((i > 0 && SMALL_WORDS.has(w)) || /[A-Z]/.test(w.slice(1)) ? w : w[0].toUpperCase() + w.slice(1))));
  // Width (in chart units) the y-axis labels need: the chart's left margin grows with the longest label so big
  // numbers are never cut off.  ~6.6 units a character at the 11px axis font, plus the gap and a little edge room.
  const axisMargin = (labels, min) => Math.max(min, Math.ceil(Math.max(0, ...labels.map((t) => String(t).length)) * 6.6) + 14);
  const pill = (text, kind) => h("span", { class: "pill " + (kind || "") }, text);
  // what lldpd is doing on a node (GET /api/lldp), in a few words; the same as `logdir --lldp`
  const lldpWords = (st) => !st.installed ? "lldpd is not installed on this node"
    : st.deferred ? (st.running ? "Running, managed in AnyName on this host" : "Stopped: AnyName is on this host and decides")
    : st.running ? "Running" : st.enabled ? "Stopped: see the log" : "Stopped";

  // ── API ───────────────────────────────────────────────────────────────────
  // Everything follows the node picked in the top bar (relayed through the node
  // you are logged in to) except the login itself.  The picker's own list of
  // nodes is asked of the login node explicitly (api(..., true)).
  const LOCAL_API = /^\/api\/(login|logout|session|proxy|tshoot\/download)(\/|\?|$)/;
  // The Node menu's "Cluster" entry: Monitor ▸ Node shows every node's numbers added together (the node you are logged in to
  // asks the others), and Monitor ▸ Capture captures on every node.  Only those pages offer it, and only their requests change;
  // anything else a page asks goes to this node as before.
  const CLUSTER = "*cluster";
  // Node and Capture also offer each gateway: the nodes that serve it, together like Cluster.
  const GW = "*gw:";
  const GW_TABS = ["host", "capture"];   // the pages that offer the gateways
  const isGw = () => typeof state.target === "string" && state.target.startsWith(GW);
  const gwTitle = (g) => g.name || String(g.vip4 || g.vip6 || "").split("/")[0] || "gateway " + g.group_id;
  const gwItems = () => (state.gws || []).map((g) => {
    const n = state.nodes.filter((p) => !(g.excluded_nodes || []).includes(p.node_id)).length;
    return n ? [GW + g.group_id, "Gateway " + gwTitle(g) + " (" + n + ")", false] : null;
  }).filter(Boolean);
  const pickedGw = () => (isGw() ? (state.gws || []).find((g) => String(g.group_id) === state.target.slice(GW.length)) || null : null);
  const many = () => state.target === CLUSTER || isGw();   // a page about several nodes: every node, or a gateway's own
  const CLUSTER_TABS = ["host", "capture"];
  const CLUSTER_API = [[/^\/api\/host(\?|$)/, "/api/clusterhost"]];
  function route(path) {
    if (isGw()) { const to = /^\/api\/host(\?|$)/.test(path) ? "/api/clusterhost" : ""; return to ? path.replace(/^\/api\/[a-z]+/, to) + (path.includes("?") ? "&" : "?") + "gw=" + encodeURIComponent(state.target.slice(GW.length)) : path; }
    if (state.target === CLUSTER) {
      for (const [re, to] of CLUSTER_API) if (re.test(path)) return path.replace(/^\/api\/[a-z]+/, to);
      return path;
    }
    if (!state.target || LOCAL_API.test(path)) return path;
    return "/api/proxy?node=" + encodeURIComponent(state.target) + "&path=" + encodeURIComponent(path);
  }
  // signal: an AbortSignal that drops the request (the daemon then stops working on it); fetch throws an AbortError
  async function api(method, path, body, local, signal) {
    const orig = path;
    if (!local) path = route(path);
    const headers = {};
    if (body !== undefined) headers["Content-Type"] = "application/json";
    if (state.session && method !== "GET") headers["X-CSRF-Token"] = state.session.csrf;
    const res = await fetch(path, {
      method, headers, credentials: "same-origin", signal,
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    let data = null;
    try { data = await res.json(); } catch (_) { /* non-JSON error */ }
    if (res.status === 401 && orig !== "/api/login") {
      stopAllTimers();
      state.session = null;
      showLogin();
      throw new Error("unauthenticated");
    }
    if (!res.ok) {
      const err = new Error((data && data.error) || res.statusText || "request failed");
      err.status = res.status;
      err.data = data;
      throw err;
    }
    return data;
  }

  // Upload raw bytes (release archive); the CSRF token still applies.
  async function apiUpload(path, file) {
    const res = await fetch(route(path), {
      method: "POST", credentials: "same-origin",
      headers: { "Content-Type": "application/octet-stream", "X-CSRF-Token": state.session.csrf },
      body: file,
    });
    let data = null;
    try { data = await res.json(); } catch (_) { /* non-JSON error */ }
    if (res.status === 401) { stopAllTimers(); state.session = null; showLogin(); throw new Error("unauthenticated"); }
    if (!res.ok) throw new Error((data && data.error) || res.statusText || "upload failed");
    return data;
  }

  // Download a file the daemon builds (a capture): fetched with the session, named by the server's Content-Disposition.
  async function apiDownload(path, fallback) {
    const res = await fetch(route(path), { credentials: "same-origin" });
    if (res.status === 401) { stopAllTimers(); state.session = null; showLogin(); throw new Error("unauthenticated"); }
    if (!res.ok) {
      let data = null;
      try { data = await res.json(); } catch (_) { /* not JSON */ }
      throw new Error((data && data.error) || res.statusText || "download failed");
    }
    const m = /filename="?([^";]+)"?/.exec(res.headers.get("Content-Disposition") || "");
    const a = h("a", { href: URL.createObjectURL(await res.blob()), download: m ? m[1] : fallback });
    document.body.append(a); a.click(); a.remove();
    setTimeout(() => URL.revokeObjectURL(a.href), 4000);
  }

  // ── login ─────────────────────────────────────────────────────────────────
  function showLogin(message) {
    stopAllTimers();
    // Already showing the form (a late 401 from a request still in flight): leave
    // it alone, so nothing being typed is wiped.
    if (!message && root.querySelector("form input[name=username]")) return;
    clear(root);
    const err = h("div", { class: "notice bad hidden", role: "alert" });
    if (message) { err.textContent = message; err.classList.remove("hidden"); }
    const user = h("input", { type: "text", name: "username", autocomplete: "username", required: true, autofocus: true, autocapitalize: "none", spellcheck: "false" });
    const pass = h("input", { type: "password", name: "password", autocomplete: "current-password", required: true });
    const btn = h("button", { class: "btn primary", type: "submit" }, "Log in");
    let lockTimer = null;
    const setLocked = (on) => { user.disabled = pass.disabled = btn.disabled = on; };
    // Locked out: disable the form and count down to the end of the lockout.
    function lockFor(secs) {
      const end = Date.now() + secs * 1000;
      setLocked(true);
      clearInterval(lockTimer);
      const tick = () => {
        const left = Math.ceil((end - Date.now()) / 1000);
        if (!form.isConnected) { clearInterval(lockTimer); return; }
        if (left <= 0) {
          clearInterval(lockTimer);
          setLocked(false);
          err.classList.add("hidden");
          user.focus();
          return;
        }
        err.textContent = "Too many failed attempts.";
        err.classList.remove("hidden");
      };
      tick();
      lockTimer = setInterval(tick, 1000);
    }
    const form = h("form", {
      onsubmit: async (e) => {
        e.preventDefault();
        btn.disabled = true;
        err.classList.add("hidden");
        try {
          state.session = await api("POST", "/api/login", { username: user.value, password: pass.value });
          checkBuild(state.session.version);
          pass.value = "";
          showShell();
        } catch (ex) {
          pass.value = "";
          const d = ex.data || {};
          if (ex.status === 429 && d.retry_after) { lockFor(d.retry_after); return; }
          err.textContent = ex.message;
          err.classList.remove("hidden");
          pass.focus();
          btn.disabled = false;
        }
      },
    },
      h("label", { class: "f" }, "Username", user),
      h("label", { class: "f" }, "Password", pass),
      btn);
    const ssoBox = h("div", { class: "sso-buttons hidden" });
    root.append(h("div", { class: "login-wrap" }, h("div", { class: "login" },
      h("div", { class: "card" }, h("div", { class: "body" },
        h("h1", {}, "LogDirector"),
        h("p", { class: "sub" }, "Sign in with your system account"),
        err, form, ssoBox)))));
    user.focus();
    // back from a failed single sign-on: say why
    try {
      const sp = new URLSearchParams(location.search);
      if (sp.get("sso_error")) { err.textContent = sp.get("sso_error"); err.classList.remove("hidden"); history.replaceState(null, "", location.pathname); }
    } catch (_) { /* no message */ }
    // already locked out (page reloaded)? show it before anything is typed
    fetch("/api/login/state", { credentials: "same-origin" }).then((r) => r.json()).then((d) => {
      if (d.locked && d.retry_after) lockFor(d.retry_after);
      // the providers that are set up: a full-page trip to the provider and back (no script on the way)
      for (const p of d.sso || []) ssoBox.append(h("a", { class: "btn sso", href: "/api/sso/start?p=" + encodeURIComponent(p.id) }, "Sign in with " + p.title));
      if ((d.sso || []).length) ssoBox.classList.remove("hidden");
    }).catch(() => { /* the form still works */ });
  }

  // ── shell / navigation ────────────────────────────────────────────────────
  // Sidebar groups: one open at a time; the group holding the current page is open.
  // Topology comes first; its items are the gateways themselves (filled in from the daemon).
  const TOPO = "Topology";
  const LOGS = "Logs";   // its own entry above Topology: no list under it
  const NAV_GROUPS = [
    ["Configure", [["cfg_general", "Settings"], ["cfg_groups", "Gateways"], ["cfg_mcp", "MCP"], ["cfg_privacy", "Privacy"], ["cfg_senders", "Senders"], ["cfg_shipping", "Shipping"], ["cfg_web", "Web GUI"], ["cfg_monitoring", "Monitoring"], ["cfg_alerts", "Alerts"], ["cfg_backup", "Backup"], ["anycast", "Anycast"], ["users", "Users"]]],
    ["Operate", [["node", "Node"], ["cluster", "Cluster"], ["anycastop", "Anycast"], ["scheduler", "Scheduler"], ["history", "History"], ["updates", "Upgrade"]]],
    ["Monitor", [["senders", "Senders"], ["shipping", "Shipping"], ["mcpusage", "MCP"], ["host", "Node"], ["gateways", "Gateways"], ["nodes", "Cluster"], ["anycaststatus", "Anycast"], ["alerts", "Alerts"], ["capture", "Capture"], ["log", "Log"]]],
  ];
  const TABS = [["logs", LOGS], ["topology", TOPO]].concat(NAV_GROUPS.flatMap(([, items]) => items));
  const groupOf = (id) => (id === "logs" ? LOGS : id === "topology" ? TOPO : (NAV_GROUPS.find(([, items]) => items.some(([i]) => i === id)) || [null])[0]);
  const bareIP = (a) => (a || "").split("/")[0];

  // The gateways listed under Topology: bare address, a status dot, nothing else.
  async function refreshTopoNav() {
    try {
      // always the node you signed in to, and the gateway's state for the cluster as a whole (from its nodes), so the dot does not change with the Node menu
      const r = await api("GET", "/api/canvas?own=1", undefined, true);
      state.topo.list = (r.data || []).map((g) => ({ id: g.group_id, label: g.name || bareIP(g.vip4) || bareIP(g.vip6) || "gateway " + g.group_id,
        status: g.cluster_status || g.status, title: "Group " + g.group_id + (g.name ? " · " + (bareIP(g.vip4) || bareIP(g.vip6)) : "") + " — " + (g.cluster_status ? g.cluster_detail : g.detail) }));
    } catch (_) { return; }
    state.topo.list.sort((a, b) => a.label.localeCompare(b.label, undefined, { numeric: true, sensitivity: "base" }) || a.id - b.id);   // alphabetical (10.0.0.2 before 10.0.0.10)
    if (!state.topo.list.some((g) => g.id === state.topo.gid)) state.topo.gid = state.topo.list.length ? state.topo.list[0].id : null;
    renderTopoNav();
  }
  // A red warning triangle on Monitor ▸ Alerts (and on the Monitor heading, which may be folded) while the node picked in the top bar has an alert
  // that is open, not acknowledged and not silenced.
  const alertMark = () => {
    const m = h("span", { class: "alert-mark", role: "img", title: "There is an alert that needs attention", "aria-label": "alert" });
    m.innerHTML = '<svg viewBox="0 0 16 16" width="15" height="15" aria-hidden="true"><path d="M8 1.4 15 14H1z" fill="#fff" stroke="#d32f2f" stroke-width="1.6" stroke-linejoin="round"/><path d="M8 5.9v3.9M8 11.3v1" stroke="#d32f2f" stroke-width="1.7" stroke-linecap="round"/></svg>';
    return m;
  };
  async function refreshAlertMark() {
    let on = false;
    try {
      const v = (await api("GET", "/api/alerts")).data;
      on = (v.alerts || []).some((a) => !a.closed && !a.acked && !a.silenced_until);
    } catch (_) { return; }
    const marks = [root.querySelector('.nav-item[data-tab="alerts"]'), root.querySelector('.nav-group[data-group="Monitor"] .nav-group-label')];
    for (const el of marks) {
      if (!el) continue;
      const old = el.querySelector(".alert-mark");
      if (on && !old) el.append(alertMark());
      else if (!on && old) old.remove();
    }
  }
  function renderTopoNav() {
    const box = root.querySelector('.nav-group[data-group="' + TOPO + '"] .nav-items');
    if (!box) return;
    const here = state.tab === "topology";
    clear(box).append(...state.topo.list.map((g) => h("button", { type: "button", class: "nav-item", title: g.title, "data-gw": g.id,
        "aria-current": here && g.id === state.topo.gid ? "page" : null,
        onclick: () => { state.topo.gid = g.id; selectTab("topology"); },
        oncontextmenu: rowMenu([["Rename", () => { state.topo.gid = g.id; state.topo.action = "rename"; selectTab("topology"); }],
          ["MCP", () => selectTab("cfg_mcp")],
          ["Delete", () => { state.topo.gid = g.id; state.topo.action = "delete"; selectTab("topology"); }, "danger"]]) },
      h("span", { class: "dot st-" + g.status }), g.label)),
      h("button", { type: "button", class: "nav-item add", onclick: () => { state.topo.action = "new"; selectTab("topology"); } }, "＋ New gateway"));
  }

  function openNavGroup(name) {
    for (const g of root.querySelectorAll(".nav-group")) {
      const open = g.dataset.group === name;
      g.querySelector(".nav-group-label").classList.toggle("open", open);
      g.querySelector(".nav-group-label").setAttribute("aria-expanded", String(open));
      g.querySelector(".nav-items").classList.toggle("hidden", !open);
    }
  }

  function showShell() {
    stopAllTimers();
    clear(root);
    const s = state.session;
    const groups = [[LOGS, []], [TOPO, []]].concat(NAV_GROUPS).map(([name, items]) => h("div", { class: "nav-group" + (name === LOGS ? " single" : ""), "data-group": name },
      h("button", { type: "button", class: "nav-group-label", "aria-expanded": "false",
        onclick: () => { if (name === LOGS) selectTab("logs"); else if (name === TOPO) selectTab("topology"); else openNavGroup(name); } },
        h("span", { class: "nav-chevron" }), name),
      h("div", { class: "nav-items hidden" },
        items.map(([id, label]) => h("button", { type: "button", class: "nav-item", "data-tab": id, onclick: () => selectTab(id) }, label)))));
    const nav = h("nav", { class: "side", "aria-label": "Sections" },
      h("div", { class: "mark" }, h("div", { class: "brand" }, "LogDirector"),
        h("div", { class: "muted small" }, "v" + s.version + (s.hostname && s.hostname.toLowerCase() !== "logdir" ? " · " + s.hostname : ""))),
      h("div", { class: "nav-section" }, groups),
      themeToggle(),
      h("div", { class: "nav-footer" },
        h("div", { class: "who" }, "Signed in as ", h("strong", {}, s.user)),
        h("button", { class: "nav-signout", type: "button", onclick: logout }, "Sign out")));
    root.append(helpControls());
    root.append(h("div", { class: "shell" }, nav,
      h("div", { class: "content" },
        h("div", { id: "topbar", class: "topbar hidden" }),
        h("div", { id: "banner", class: "banner" }),
        h("main", { id: "main" }))));
    state.target = null;
    state.nodes = [];
    state.clustered = false;
    document.body.classList.remove("remote");
    certBanner();
    refreshTopoNav();
    refreshNodes();
    state.topoTimer = setInterval(() => { if (!document.hidden) refreshTopoNav(); }, 4000);
    state.nodesTimer = setInterval(() => { if (!document.hidden) refreshNodes(); }, 6000);
    refreshAlertMark();
    state.alertTimer = setInterval(() => { if (!document.hidden) refreshAlertMark(); }, 5000);
    let wanted = location.hash.slice(1);
    if (wanted === "canvas") wanted = "topology"; // the page's old name: keep old bookmarks working
    if (wanted === "neighbors") wanted = "nodes"; // likewise
    if (wanted === "power" || wanted === "gateway") wanted = "node"; // Power and the short-lived Gateway page became Operate ▸ Node
    if (wanted === "bgpstatus") wanted = "anycaststatus"; // Monitor ▸ BGP became Monitor ▸ Anycast
    if (wanted === "bgp") wanted = "anycast"; // likewise Configure ▸ BGP
    if (wanted === "certificate") wanted = "cfg_web"; // moved under Configure ▸ Web GUI
    if (wanted === "config") wanted = "cfg_general"; // Configure ▸ Settings was split into one page per tab
    if (wanted === "cfg_cluster") { wanted = "cfg_general"; state.settingsTab = "cluster"; } // moved under Configure ▸ Settings
    selectTab(TABS.some(([id]) => id === wanted) ? wanted : "logs");
  }

  async function logout() {
    try { await api("POST", "/api/logout", {}); } catch (_) { /* already gone */ }
    stopAllTimers();
    state.target = null;
    document.body.classList.remove("remote");
    state.session = null;
    showLogin();
  }

  function selectTab(id) {
    const prev = VIEWS[state.tab];
    if (prev && prev.leave) prev.leave();   // e.g. the Logs page drops a search still running
    state.tab = id;
    if (state.target === CLUSTER && !CLUSTER_TABS.includes(id)) state.target = null;   // the other pages are about one node: back to this one
    if (isGw() && !GW_TABS.includes(id)) state.target = null;
    renderTopbar();   // the Cluster entry is on the menu for Statistics and Monitor ▸ Node only
    history.replaceState(null, "", "#" + id);
    for (const b of root.querySelectorAll("nav.side .nav-item[data-tab]")) {
      if (b.dataset.tab === id) b.setAttribute("aria-current", "page"); else b.removeAttribute("aria-current");
    }
    const grp = groupOf(id);
    if (grp) openNavGroup(grp);
    renderTopoNav();
    stopPolling();
    const main = clear(document.getElementById("main"));
    const view = VIEWS[id];
    view.mount(main);
    if (document.getElementById("help-panel") && document.getElementById("help-panel").classList.contains("open")) fillHelp();
    if (view.poll) {
      startPolling(view.poll);
    }
  }

  // ── help: the "?" in the top right, a slide-out panel for the current page ──
  // Content is data in help.js (LOGDIR_HELP); this builds it with DOM calls only.
  function richText(str) {
    const out = [];
    for (const part of String(str).split(/(\*\*[^*]+\*\*|`[^`]+`|\*[^*]+\*)/)) {
      if (!part) continue;
      if (part.startsWith("**")) out.push(h("strong", {}, part.slice(2, -2)));
      else if (part.startsWith("`")) out.push(h("code", {}, part.slice(1, -1)));
      else if (part.startsWith("*") && part.length > 2) out.push(h("em", {}, part.slice(1, -1)));
      else out.push(part);
    }
    return out;
  }
  function helpBody(topic) {
    const t = LOGDIR_HELP[topic] || LOGDIR_HELP._default;
    const kids = t.body.map((blk) => {
      if (typeof blk === "string") return h("p", {}, richText(blk));
      if (blk[0] === "h") return h("h3", {}, blk[1]);
      if (blk[0] === "ul") return h("ul", {}, blk[1].map((li) => h("li", {}, richText(li))));
      if (blk[0] === "ol") return h("ol", {}, blk[1].map((li) => h("li", {}, richText(li))));
      if (blk[0] === "note") return h("p", { class: "help-note" }, richText(blk[1]));
      if (blk[0] === "fields") return h("dl", { class: "help-fields" }, blk[1].flatMap(([name, text]) => [h("dt", {}, name), h("dd", {}, richText(text))]));
      if (blk[0] === "cli") return h("div", {}, h("h3", {}, "Command line"), h("pre", { class: "help-cli" }, blk[1]));
      return null;
    });
    return [t.title, kids];
  }
  function helpControls() {
    const overlay = h("div", { class: "help-overlay hidden", id: "help-overlay" });
    const title = h("h2", { id: "help-title" });
    const body = h("div", { class: "help-panel-body", id: "help-body" });
    // Find: every match in the page's help is marked; Enter goes to the next one, Shift+Enter to the one before, Esc empties the box
    const find = h("input", { type: "text", class: "help-find", id: "help-find", placeholder: "Find", "aria-label": "Find in help", autocomplete: "off", spellcheck: "false",
      oninput: () => markHelp(true),
      onkeydown: (ev) => {
        if (ev.key === "Enter") { ev.preventDefault(); stepHelp(ev.shiftKey ? -1 : 1); }
        else if (ev.key === "Escape" && find.value) { ev.stopPropagation(); find.value = ""; markHelp(true); }
      } });
    const panel = h("aside", { class: "help-panel", id: "help-panel", "aria-hidden": "true", "aria-label": "Help" },
      h("div", { class: "help-panel-header" }, title),
      h("div", { class: "help-find-row" }, find, h("span", { class: "help-find-count muted small", id: "help-find-count" })), body);
    const btn = h("button", { class: "help-btn", id: "help-btn", type: "button", title: "Help for this page", "aria-label": "Help",
      onclick: () => setHelp(!panel.classList.contains("open")) }, "?");
    overlay.addEventListener("click", () => setHelp(false));
    return h("div", {}, btn, overlay, panel);
  }
  // Opening (and every page change while open) fills the panel for the current page.
  function setHelp(open) {
    const panel = document.getElementById("help-panel"), overlay = document.getElementById("help-overlay");
    if (!panel) return;
    if (open) fillHelp();
    panel.classList.toggle("open", !!open);
    overlay.classList.toggle("hidden", !open);
    panel.setAttribute("aria-hidden", open ? "false" : "true");
    const b = document.getElementById("help-btn");
    b.setAttribute("aria-label", open ? "Close help" : "Help");
    b.title = open ? "Close help (Esc)" : "Help for this page";
  }
  let helpHits = [], helpAt = 0;
  function markHelp(jump) {
    const body = document.getElementById("help-body"), box = document.getElementById("help-find"), count = document.getElementById("help-find-count");
    if (!body || !box) return;
    for (const m of body.querySelectorAll("mark.help-hit")) m.replaceWith(document.createTextNode(m.textContent));   // take the old marks out
    body.normalize();
    helpHits = []; helpAt = 0;
    const q = box.value.toLowerCase();
    if (q) {
      const walker = document.createTreeWalker(body, NodeFilter.SHOW_TEXT);
      const texts = [];
      for (let n = walker.nextNode(); n; n = walker.nextNode()) texts.push(n);
      for (const node of texts) {
        let cur = node;
        for (let i = cur.data.toLowerCase().indexOf(q); i >= 0; i = cur.data.toLowerCase().indexOf(q)) {
          const hit = cur.splitText(i), rest = hit.splitText(q.length);
          const m = h("mark", { class: "help-hit" }, hit.data);
          hit.replaceWith(m);
          helpHits.push(m);
          cur = rest;
        }
      }
    }
    count.textContent = q ? (helpHits.length ? "1 / " + helpHits.length : "0") : "";
    if (helpHits.length) stepHelp(0, jump);
  }
  // the current match gets its own colour and is scrolled into view
  function stepHelp(d, jump = true) {
    if (!helpHits.length) return;
    helpHits[helpAt].classList.remove("cur");
    helpAt = (helpAt + d + helpHits.length) % helpHits.length;
    const m = helpHits[helpAt];
    m.classList.add("cur");
    document.getElementById("help-find-count").textContent = (helpAt + 1) + " / " + helpHits.length;
    if (jump) m.scrollIntoView({ block: "center" });
  }
  // Which panel the help shows: one for every page, and for every tab and sub-tab of a page that has them.
  function helpTopicKey() {
    const k = (() => {
      switch (state.tab) {
      case "cfg_general": return state.settingsTab === "cluster" ? "config_cluster" : state.settingsTab === "upgrades" ? "config_upgrades" : state.settingsTab === "dns" ? "config_dns" : state.settingsTab === "general" ? "config_general" : "config_logs";
      case "cfg_groups": return "config_groups";
      case "cfg_senders": return "config_senders";
      case "cfg_shipping": return "config_shipping";
      case "cfg_web": return "config_web";
      case "anycast": return "anycast_" + (state.anycastTab === 1 ? "ospf" : "bgp");
      case "anycaststatus": return "anycaststatus_" + (state.anycastMonTab === 1 ? "ospf" : "bgp");
      case "anycastop": return "anycastop_" + (state.anycastOpTab === 1 ? "ospf" : "bgp");
      case "cfg_alerts": return "config_alerts";
      case "cfg_backup": return "config_backup";
      case "cfg_mcp": return "config_mcp";
      case "cfg_privacy": return "config_privacy";
      case "cfg_monitoring": return state.monTab === 1 ? "config_metrics" : "config_snmp";
      case "users": return state.usersTab === 1 ? "users_ldap" : state.usersTab === 2 ? "users_sso" + (state.ssoTab >= 0 ? state.ssoTab : 0) : "users_local";
      default: return state.tab;
      }
    })();
    return LOGDIR_HELP[k] ? k : state.tab;
  }
  // a tab inside a page was changed: if the help is open, show the panel of that tab
  function refreshHelp() {
    const pn = document.getElementById("help-panel");
    if (pn && pn.classList.contains("open")) fillHelp();
  }
  function fillHelp() {
    const topic = helpTopicKey();
    const [t, kids] = helpBody(topic);
    document.getElementById("help-title").textContent = t;
    const body = clear(document.getElementById("help-body"));
    body.append(...kids);
    body.scrollTop = 0;
    markHelp(false);   // a word typed in Find stays marked on the next page's help
  }
  document.addEventListener("keydown", (ev) => { if (ev.key === "Escape") setHelp(false); });

  // ── node picker (top right) ────────────────────────────────────────────────
  const selfNode = () => state.nodes.find((n) => n.self) || null;
  const currentNode = () => state.nodes.find((n) => n.addr === state.target) || null;
  // the host name, or the address too when two nodes share a name
  const nodeLabel = (n) => {
    if (!n) return "this node";
    if (!n.hostname) return n.addr;
    return state.nodes.filter((x) => x.hostname === n.hostname).length > 1 ? n.hostname + " (" + n.addr + ")" : n.hostname;
  };

  async function refreshNodes() {
    let v;
    try { v = (await api("GET", "/api/cluster", undefined, true)).data; } catch (_) { return; }
    state.clustered = !!(v && v.enabled && (v.peers || []).length > 1);
    state.nodes = state.clustered ? v.peers : [];
    if (GW_TABS.includes(state.tab) && state.nodes.length >= 2 && Date.now() - (state.gwsAt || 0) > 20000) {   // the gateways for the Node menu
      state.gwsAt = Date.now();
      try { state.gws = ((await api("GET", "/api/config", undefined, true)).config || {}).groups || []; } catch (_) { /* the last list stays */ }
    }
    if (state.target === CLUSTER || isGw() ? state.nodes.length < 2 : state.target && !state.nodes.some((n) => n.addr === state.target && !n.self)) setTarget(null);
    renderTopbar();
  }

  // The filter box takes the focus when a gateway's drawing is opened, and again for each gateway picked in the sidebar, but not from another
  // box being typed in or an open dialog, and never again for the same gateway (the drawing and the bar are redrawn all the time).
  function focusTopoFilter(gid) {
    if (state.topoFocusGid === gid) return;
    const f = document.querySelector("input.cv-filter");
    if (!f) return;
    state.topoFocusGid = gid;
    const ae = document.activeElement;
    if (document.querySelector("dialog[open]") || (ae && ae !== f && /^(INPUT|TEXTAREA|SELECT)$/.test(ae.tagName))) return;
    f.focus();
  }
  function renderTopbar() {
    const bar = document.getElementById("topbar");
    if (!bar) return;
    // The nodes are asked for every few seconds; the bar is only redrawn when what it shows changed, and not at all while one of its
    // pickers has the focus (redrawing it shut an open list).
    const view = [state.tab, state.target];
    const sig = JSON.stringify([view, GW_TABS.includes(state.tab) ? gwItems() : null, state.nodes.map((n) => [n.addr, nodeLabel(n), n.is_primary, n.reachable, !!n.self])]);
    if (bar.dataset.sig === sig) return;
    if (bar.contains(document.activeElement) && bar.dataset.view === JSON.stringify(view)) return;
    bar.dataset.sig = sig;
    bar.dataset.view = JSON.stringify(view);
    clear(bar);
    const rows = [];
    if (state.tab === "topology") {   // the filter of the Topology map sits at the left of the bar, level with the node picker
      const fin = h("input", { type: "text", class: "cv-filter", value: state.topoFilter || "", placeholder: "Filter senders", autocomplete: "off", spellcheck: "false", "aria-label": "Filter senders",
        oninput: () => { state.topoFilter = fin.value; if (state.onTopoFilter) state.onTopoFilter(); } });
      rows.push(h("div", { class: "tbrow tbfilter" }, fin));
      setTimeout(() => focusTopoFilter(state.topo.gid), 0);   // once for each gateway: later redraws of the bar never take the focus from what is being typed
    } else state.topoFocusGid = null;
    if (state.nodes.length >= 2) {
      const sel = h("select", { id: "node-pick", "aria-label": "Node to configure and monitor", onchange: (e) => setTarget(e.target.value || null) });
      const self = selfNode();
      sel.append(h("option", { value: "" }, nodeLabel(self) + " (this node)"));
      for (const n of state.nodes.filter((x) => !x.self)) {
        const o = h("option", { value: n.addr }, nodeLabel(n) + (n.is_primary ? " · primary" : "") + (n.reachable ? "" : " · unreachable"));
        if (!n.reachable && n.addr !== state.target) o.disabled = true;
        sel.append(o);
      }
      if (GW_TABS.includes(state.tab)) for (const [value, text] of gwItems()) sel.append(h("option", { value }, text));   // nodes, then gateways, then the whole cluster
      if (CLUSTER_TABS.includes(state.tab)) sel.append(h("option", { value: CLUSTER }, "Cluster (" + state.nodes.length + ")"));
      sel.value = state.target || "";
      rows.push(h("div", { class: "tbrow tbnode" }, h("label", { for: "node-pick", class: "muted small" }, "Node"), sel));
    }
    bar.classList.toggle("hidden", rows.length === 0);
    bar.append(...rows);
  }

  function setTarget(addr) {
    if ((addr || null) === state.target) return;
    state.target = addr || null;
    document.body.classList.toggle("remote", !!state.target && state.target !== CLUSTER && !isGw());   // another node's page is marked; the cluster's is no node
    renderTopbar();
    state.topo.action = null;   // the gateway picked stays picked: the sidebar list is the signed-in node's, whichever node is driven
    refreshTopoNav();
    certBanner();
    if (document.getElementById("main")) selectTab(state.tab);
  }

  function startPolling(fn) {
    const tick = async () => { if (!document.hidden) { try { await fn(); } catch (_) { /* shown by view */ } } };
    tick();
    state.timer = setInterval(tick, POLL_MS);
  }
  function stopPolling() { if (state.timer) clearInterval(state.timer); state.timer = null; }
  // Every timer the signed-in shell owns.  A session that ends by itself (an
  // update restarts the daemon, an idle timeout) must stop the sidebar and node
  // pollers too: they kept getting 401 and redrew the login form every few
  // seconds, wiping what was being typed (it looked like the form blinking).
  function stopAllTimers() {
    stopPolling();
    if (state.topoTimer) clearInterval(state.topoTimer);
    if (state.nodesTimer) clearInterval(state.nodesTimer);
    if (state.alertTimer) clearInterval(state.alertTimer);
    state.topoTimer = state.nodesTimer = state.alertTimer = null;
  }
  document.addEventListener("visibilitychange", () => {
    if (!document.hidden && state.timer && VIEWS[state.tab].poll) VIEWS[state.tab].poll().catch(() => {});
  });

  // partialLast: the range ends inside the last bucket (more than one bucket, so there is something left to draw)
  const partialLast = (d, len) => len > 2 && d.to < d.start + len * d.step;
  // the canary soak: a node that took a new update first shows an hourglass until the others are let go (sec = seconds left)
  const soakTip = (sec) => "soaking: " + sec + "s remaining";
  const soakMark = (sec) => (sec > 0 ? [" ", h("span", { class: "soak", title: soakTip(sec), "aria-label": soakTip(sec) }, "⏳")] : null);
  // right-click menu items are written plain: no … after a label that opens a dialog
  function plain(l) { return typeof l === "string" ? l.replace(/…(?=$| \()/, "") : l; }
  function errorBox(el, msg) {
    clear(el).append(h("div", { class: "notice bad", role: "alert" }, msg));
  }

  // ── views ─────────────────────────────────────────────────────────────────
  const VIEWS = {};


  // Topology  (CLI: logdir --canvas …)
  // One drawing per gateway: the circle is the gateway and its shared address, the cluster's nodes are on its left
  // and its anycast addresses on its right.  Every change is saved as soon as it is made.
  VIEWS.topology = (() => {
    const SVGNS = "http://www.w3.org/2000/svg";
    const sv = (tag, attrs, ...kids) => {
      const el = document.createElementNS(SVGNS, tag);
      for (const [k, v] of Object.entries(attrs || {})) {
        if (v == null || v === false) continue;
        if (k.startsWith("on")) el.addEventListener(k.slice(2), v); else el.setAttribute(k, v);
      }
      for (const kid of kids.flat(Infinity)) if (kid != null) el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
      return el;
    };
        const CV_ZOOM_MIN = 0.2, CV_ZOOM_MAX = 3;   // what the mouse wheel can zoom the drawing to
    const CV_MIN_SCALE = 0.65;   // the drawing shrinks to fit the window down to this, then scrolls
    const cv = { cfg: null, orig: null, gid: null, sel: null, multi: null, view: [], loaded: false, drawn: null, ro: null };
    let svgHost, legendEl, pending = 0;

    const trunc = (t, n) => (t.length > n ? t.slice(0, n - 1) + "…" : t);
    // cut a text to a pixel width (measured in the font the drawing uses), ending in … when it had to be cut
    let measureCtx = null;
    const fitPx = (t, maxPx, font) => {
      try { measureCtx = measureCtx || document.createElement("canvas").getContext("2d"); } catch (e) { measureCtx = null; }
      if (!measureCtx) return trunc(t, Math.floor(maxPx / 7.5));
      measureCtx.font = font;
      if (measureCtx.measureText(t).width <= maxPx) return t;
      let lo = 0, hi = t.length;
      while (lo < hi) { const m = (lo + hi + 1) >> 1; if (measureCtx.measureText(t.slice(0, m) + "…").width <= maxPx) lo = m; else hi = m - 1; }
      return t.slice(0, lo).trimEnd() + "…";
    };
    const groups = () => cv.cfg.groups;
    const curGroup = () => groups().find((g) => g.group_id === cv.gid) || null;
    // the cluster nodes currently serving a gateway, for the circle's tooltip
    // "Online for 2h 5m - 1 failure": the daemon notes when each item last flipped between working and
    // failing and how many failures it has seen (u = {up, since, failures}); nothing for grey or paused items
    const spanText = (secs) => {
      const s = Math.max(0, Math.floor(secs)), d = Math.floor(s / 86400), hh = Math.floor(s / 3600) % 24, m = Math.floor(s / 60) % 60;
      return d ? d + "d " + hh + "h" : hh ? hh + "h " + m + "m" : m ? m + "m " + (s % 60) + "s" : s + "s";
    };
    const upText = (u) => !u ? "" : (u.up ? "Online for " : "Down for ") + spanText(Date.now() / 1000 - u.since) + " - " + (u.failures || 0) + ((u.failures || 0) === 1 ? " failure" : " failures");
    const upLine = (u) => (u ? "\n" + upText(u) : "");
    const membersText = (ms) => "Serving nodes: " + (ms.length || "none yet");   // how many, not which: each node's own tooltip says what it is
    // The gateway circle's tooltip: its name, how long it has been up, each address family's state, and how many
    // nodes serve it.  The overall status line is added only when it says more than the family lines do (a
    // pause, a gateway with one family, a start still waiting); it is the same words as the two together otherwise.
    const gwTip = (g, st, dual) => {
      const fam = (af) => (st.fams[af] ? st.fams[af].detail : "not running");
      const together = dual ? "IPv4: " + fam("v4") + "; IPv6: " + fam("v6") : null;
      return ["Gateway " + gwLabel(g), st.why && st.why !== together ? st.why : "", st.clusterUp > 0 ? "Online for " + spanText(st.clusterUp) : upText(st.uptime),
        ...(dual ? [["IPv4", "v4"], ["IPv6", "v6"]].map(([n, af]) => n + " — " + fam(af)) : []),
        (g.more_vip4 || []).length + (g.more_vip6 || []).length ? "Also answers on " + [...(g.more_vip4 || []), ...(g.more_vip6 || [])].join(", ") : "",
        membersText(st.members), "Clients: " + st.mcpClients, st.vmac && st.vmac.verdict !== "off" && !(st.why || "").startsWith("Virtual MACs:") ? vmacLine(st.vmac) : ""].filter(Boolean).join("\n");
    };
    const gwAddr = (g) => bareIP(g.vip4) || bareIP(g.vip6) || "gateway " + g.group_id;
    const gwLabel = (g) => g.name || gwAddr(g);

    // The daemon works out every colour (the CLI shows the same view); anything
    // not in it yet — an unsaved shape — is grey.
    function statusOf(g) {
      const v = (cv.view || []).find((x) => x.group_id === g.group_id);
      const fams = {};
      if (v) for (const f of v.families || []) fams[f.af] = f;
      const members = (v ? v.members || [] : []).map((m) => (m.via_local ? { ...m, via_addr: v.via_addr } : m));   // the serving node's own entry says which node it is
      let circle = v ? v.status : "idle", why = v ? v.detail : "Starting — not running yet";
      // what is staged wins over the last live view, so the pause / resume shows at once
      const via = !!(v && v.via);   // this node does not serve the gateway: the picture is that of a node that does, so nothing local colours it
      const nodePaused = !via && !!(v && v.node_paused);
      const gwPaused = !via && !!(g.paused || g.paused_all);
      const me = ((v && v.nodes) || []).find((n) => n.self);
      const removed = !via && !!(me && me.node_id && (g.excluded_nodes || []).includes(me.node_id));   // this node was removed from the gateway
      if (gwPaused || nodePaused || removed) { circle = "paused"; why = g.paused_all ? "Paused on all nodes — nothing is serving it until resumed" : removed && !g.paused && !nodePaused ? "This node was removed from the gateway — it does not serve it; the other nodes carry on" : nodePaused && !g.paused ? "This node is paused (Operate ▸ Node) — it is not serving; the other nodes carry on" : "Paused on this node — it is not serving; the other nodes carry on"; }
      else if (!via && circle === "paused") { circle = "idle"; why = "Resuming…"; }
      if (via) why = "As " + v.via + " sees it (it serves the gateway; this node does not): " + why;
      return { via: (v && v.via) || "", mcpClients: v ? v.mcp_clients || 0 : 0, circle, why, fams, members, anycast: v ? v.anycast || [] : [], nodes: v ? v.nodes || [] : [], vmac: v && !via ? v.vmac || null : null, uptime: v && !gwPaused && !nodePaused && !removed ? v.uptime : null, clusterUp: v && !gwPaused && !removed ? v.cluster_up_sec || 0 : 0 };
    }
    const scopeWord = (sc) => (sc === "all" ? "all nodes" : "this node");
    // Pause / resume: saved at once like every other change.
    function quick(change) { change(); commit(); }
    // Toggle one entry in a list held on `obj` (deleting the list when it empties).
    function toggleIn(obj, key, val, on) {
      const l = (obj[key] || []).filter((x) => x !== val);
      obj[key] = on ? l.concat(val) : l;
      if (!obj[key].length) delete obj[key];
    }
    function pauseGateway(g, scope, pause) {
      const k = scope === "all" ? "paused_all" : "paused";
      if (!pause) { quick(() => { delete g[k]; }); return; }
      confirmDialog("Pause gateway " + gwLabel(g) + " on " + scopeWord(scope) + "?",
        scope === "all"
          ? "Every node stops serving the gateway — it gives up its address and stops answering on it. Resume it when you are done."
          : "This node stops serving the gateway — it gives up its address and stops answering on it — so it can be taken offline. The other cluster nodes carry on. Resume it here when you are done.",
        "Pause gateway", () => quick(() => { g[k] = true; }), "btn primary");
    }
    // An IPv6 address in one spelling, to compare the gateway's with the one a node saw (2001:0db8::1 and 2001:db8::1 are the same).
    const addrKey = (a) => {
      a = bareIP(a).toLowerCase();
      if (!a.includes(":")) return a;
      const [l, r] = a.split("::"), L = l ? l.split(":") : [], R = r === undefined ? [] : r ? r.split(":") : [];
      const mid = r === undefined ? [] : Array(Math.max(0, 8 - L.length - R.length)).fill("0");
      return [...L, ...mid, ...R].map((x) => String(parseInt(x || "0", 16))).join(":");
    };
    // The senders drawn on a gateway: the ones heard from lately (on any node) whose messages came in on one of its addresses, one entry for each,
    // however many nodes hear it.  The daemon's node status lists every sender with the addresses it sends to (/api/logs/senders).
    function exportersOf(g) {
      const mine = new Set([g.vip4, g.vip6, ...(g.extra_vips || [])].filter(Boolean).map(addrKey));
      const now = Date.now() / 1000, by = new Map();
      for (const e of cv.exp || []) {
        const to = (e.to || []).filter((a) => mine.has(addrKey(a)));
        if (!to.length || now - e.last_seen > EXP_SEEN) continue;
        let x = by.get(e.addr);
        if (!x) { x = { addr: e.addr, name: e.name || "", host: e.host || "", rev: e.rev || "", rdns: e.rdns || "", format: e.via || "?", last: 0, first: 0, to: new Set(), nodes: new Set(), records: 0, datagrams: 0, rate: 0 }; by.set(e.addr, x); }
        for (const a of to) x.to.add(a);
        if (!x.host && e.host) x.host = e.host;
        if (!x.rev && e.rev) { x.rev = e.rev; x.rdns = e.rdns || ""; }
        if (e.node) x.nodes.add(e.node);
        x.last = Math.max(x.last, e.last_seen); x.first = x.first ? Math.min(x.first, e.first_seen) : e.first_seen;
        x.records += e.messages || 0; x.datagrams += e.bytes || 0;
        if (e.last_seen >= x.last) x.format = e.via || x.format;
      }
      const out = [...by.values()];
      for (const x of out) x.status = now - x.last > EXP_LATE ? "bad" : now - x.last > EXP_QUIET ? "warn" : "ok";
      return out.sort((a, b) => String(expLabel(a)).localeCompare(String(expLabel(b)), undefined, { numeric: true }));
    }
    // the top line of a sender's square: the host part of the name its address reverse-looks-up to, else the name of its Configure ▸ Senders entry, else the host name its messages give, else the address;
    // the second line is the address (when it is not the top line); how it sends (udp, tcp, tls) is in the tooltip
    const expLabel = (x) => x.rev || x.name || x.host || x.addr;
    const expSub = (x) => (expLabel(x) === x.addr ? "" : x.addr);
    const expTip = (x) => [(expLabel(x) !== x.addr ? expLabel(x) + " — " : "Sender ") + x.addr, x.rdns ? "DNS name: " + x.rdns : "", x.name && x.name !== x.rev ? "Senders entry: " + x.name : "", x.host && x.host !== x.rev && x.host !== x.name ? "Its messages say: " + x.host : "", "Sends by " + x.format, "Sends to " + [...x.to].join(", "),
      "Last message " + spanText(Date.now() / 1000 - x.last) + " ago" + (x.status === "bad" ? " (silent for a while: gone from here after 15 minutes)" : x.status === "warn" ? " (quiet)" : ""), x.nodes.size ? "Heard by " + [...x.nodes].join(", ") : "",
      x.first ? "First heard " + spanText(Date.now() / 1000 - x.first) + " ago" : "", x.records.toLocaleString() + " messages, " + x.datagrams.toLocaleString() + " bytes", "Click: this sender's messages"].filter(Boolean).join("\n");
    // ── drawing ──────────────────────────────────────────────────────────
    function draw() {
      cv.stale = 0;
      // the drawing is rebuilt on every poll: keep where the person had scrolled it to (the same gateway only)
      const oldWrap = svgHost.querySelector(".cv-wrap");
      const keep = oldWrap && cv.drawn === cv.gid ? { x: oldWrap.scrollLeft, y: oldWrap.scrollTop } : { x: 0, y: 0 };
      const g = curGroup();
      if (!g) { clear(svgHost); svgHost.append(h("div", { class: "empty" }, "No gateway yet. Click “＋ New gateway” under Topology in the sidebar to draw one.")); return; }
      const st = statusOf(g);
      // The circle shows bare addresses (no /prefix) and grows until the longest one fits,
      // up to a full 39-character IPv6 address (monospace 12px ≈ 7.3px per character).
      const ip4 = (g.vip4 || "").split("/")[0], ip6 = (g.vip6 || "").split("/")[0];
      const dual = !!(ip4 && ip6);
      const textW = Math.max(Math.max(ip4.length, ip6.length, 9) * 6.2, 118);
      const R = Math.max(50, Math.ceil(Math.hypot((textW + 22) / 2, dual ? 28 : 20)));
      const own = g.extra_vips || [], anys = own, AH = 40, AG = 8;      // anycast pills sit to the right of the circle, the cluster's nodes to the left
      const AW = Math.max(170, Math.ceil(Math.max(0, ...anys.map((a) => a.length)) * 7.3 + 28));  // wide enough for the longest address in full (monospace 12px)
      const waiting = cv.nodeWait || {};
      // a node removed from the gateway is not drawn (right-click the gateway ▸ Add node brings it back); the saved list decides, so it goes at once
      const nodes = (st.nodes || []).filter((n) => !(g.excluded_nodes || []).includes(n.node_id)).map((n) => {   // a pause or resume just asked for, not yet reported back
        const w = waiting[n.addr];
        if (!w) return n;
        if (!!n.node_paused === w.want || Date.now() > w.until) { delete waiting[n.addr]; return n; }
        return { ...n, status: w.want ? "paused" : "idle", label: w.want ? "pausing…" : "resuming…" };
      }).sort((a, b) => {   // the order the gateway was given by dragging (node_order), then by name: picking another node in the Node menu does not move them
        const ord = g.node_order || [], ia = ord.indexOf(a.node_id), ib = ord.indexOf(b.node_id);
        if (ia !== ib && (ia >= 0 || ib >= 0)) return ia < 0 ? 1 : ib < 0 ? -1 : ia - ib;
        return String(a.name || a.addr).localeCompare(String(b.name || b.addr), undefined, { numeric: true }) || String(a.node_id || "").localeCompare(String(b.node_id || ""));
      }), NW = 190, NSL = 14;      // a node is a parallelogram: its name and what it is doing
      const CG = 25, SIDE_GAP = 70;   // SIDE_GAP: room between the circle and the nodes on its left, the anycast pills on its right
      const CY = R + 12, PER = 4;      // each side is four rows tall; the next ones go in a new column further out
      const sideH = (n) => Math.min(PER, n) * (AH + AG) - (n ? AG : 0);
      const aCols = Math.ceil(anys.length / PER), nCols = Math.ceil(nodes.length / PER);
      const anyH = sideH(anys.length), nodeH = sideH(nodes.length);
      const anyTop = Math.max(8, CY - anyH / 2), nodeTop = Math.max(8, CY - nodeH / 2);
      const anyAt = (k) => ({ x: R + SIDE_GAP + Math.floor(k / PER) * (AW + CG), y: anyTop + (k % PER) * (AH + AG) });   // x from the circle's centre
      const nodeAt = (k) => ({ x: -(R + SIDE_GAP + NW + Math.floor(k / PER) * (NW + CG)), y: nodeTop + (k % PER) * (AH + AG) });
      const rightX = anys.length ? R + SIDE_GAP + aCols * AW + (aCols - 1) * CG : 0, leftX = nodes.length ? R + SIDE_GAP + nCols * NW + (nCols - 1) * CG : 0;
      const sideBottom = Math.max(anys.length ? anyTop + anyH : 0, nodes.length ? nodeTop + nodeH : 0);
      // the gateway's senders: a row of squares under the circle (and under the nodes and pills when those reach lower).  An sender is drawn when it has
      // been heard lately and its datagrams came in on one of the gateway's addresses; not heard, not drawn.
      // The filter box: only the senders whose name, address, format, destination or node holds every word typed are drawn.  Dragging is not offered.
      const flt = parseFilter(state.topoFilter), words = { length: flt.empty ? 0 : 1 };
      const allExps = exportersOf(g);
      const expMatch = (x) => { const t = [x.host, x.name, x.addr, x.format, [...x.to].join(" "), [...x.nodes].join(" ")].join(" ").toLowerCase(); return flt.match((w) => t.includes(w)); };
      // Up to DOT_FROM senders each is a square with its name.  More than that, each is a small square joined to the gateway, and the full-size square
      // comes when the pointer is on it, or when the filter matches it (the matches are drawn full size above the others).
      const DOT_FROM = ((cv.cfg && cv.cfg.settings) || {}).topology_squares_above || DOT_DEFAULT, dotMode = allExps.length > DOT_FROM;
      const problem = (x) => x.status !== "ok";   // quiet for a while (amber or red): drawn full size while the others are small
      const devs = !dotMode ? allExps.filter(expMatch) : words.length ? allExps.filter(expMatch) : allExps.filter(problem), COLW = 190, SW = 160, SH = 46, DEV_ROW = 6, RG = 20;   // at most six across, then a new row
      const fullSet = new Set(devs), dotted = dotMode ? allExps.filter((x) => !fullSet.has(x)) : [];
      const DS = 12, DP = 18, DOT_COLS = 48;   // a small square, the distance from one to the next, the most across
      const SY = Math.max(CY + R, sideBottom) + 70, devRows = Math.ceil(devs.length / DEV_ROW), perRow = Math.min(DEV_ROW, devs.length);
      const W = Math.max(2 * R + 140, 2 * Math.max(rightX, leftX) + 20, perRow * COLW + 20, dotted.length ? Math.min(40, dotted.length) * DP + 40 : 0);
      const dotCols = Math.max(1, Math.min(dotted.length, DOT_COLS, Math.floor((W - 40) / DP)));
      const bigBottom = SY + devRows * SH + (devRows - 1) * RG, dotTop = devs.length ? bigBottom + 36 : SY, dotRows = Math.ceil(dotted.length / dotCols);
      const H = dotted.length ? dotTop + dotRows * DP + 12 : devs.length ? bigBottom + 12 : Math.max(CY + R, sideBottom) + 16;
      const dotAt = (k) => ({ x: (W - (dotCols * DP - (DP - DS))) / 2 + (k % dotCols) * DP, y: dotTop + Math.floor(k / dotCols) * DP });
      const cx = W / 2;
      // the slot of sender k: x and y of its square (a short last row is centred)
      const devAt = (k) => { const r = Math.floor(k / DEV_ROW), n = Math.min(DEV_ROW, devs.length - r * DEV_ROW); return { x: (W - n * COLW) / 2 + (k % DEV_ROW) * COLW + (COLW - SW) / 2, y: SY + r * (SH + RG) }; };
      const svg = sv("svg", { class: "cv", viewBox: `0 0 ${W} ${H}`, width: W, height: H, role: "group", "aria-label": "Gateway diagram", tabindex: "0",
        onkeydown: (e) => { if ((e.key === "Delete" || e.key === "Backspace") && cv.sel && !/INPUT|SELECT|TEXTAREA/.test(e.target.tagName)) { e.preventDefault(); delSel(); } },
        onclick: (e) => { if (e.target === svg) { cv.sel = null; cv.multi = null; refresh(); } } });
      const sel = (kind, addr) => cv.sel && cv.sel.kind === kind && cv.sel.addr === addr;
      const pick = (kind, addr) => (e) => {
        e.stopPropagation();
        cv.multi = null; cv.sel = { kind, addr };
        refresh();
        const el = svgHost.querySelector("svg"); if (el) el.focus({ preventScroll: true }); // keep the Delete key working after the redraw
      };
      const label = (x, y, t, cls) => sv("text", { x, y, "text-anchor": "middle", class: cls || "" }, t);
      const lines = [];
      const shapes = [];
      // Drag an anycast address or a node to another place.  The shape follows the pointer, a dashed outline shows where it will land, and letting go saves the new
      // order at once.  Nothing is redrawn while it is held (the poll skips cv.drag); Escape puts it back.
      const dragStart = (sp) => (e) => {
        if (e.button !== 0 || cv.drag || sp.n < 2) return;
        const shape = e.currentTarget, x0 = e.clientX, y0 = e.clientY;
        const els = sp.group ? [...svg.querySelectorAll("[data-col]")].filter((el) => el.getAttribute("data-col") === sp.group) : [shape];
        let on = false, target = sp.index, ghost = null;
        const unitsPerPx = () => 1 / ((svg.getBoundingClientRect().width / W) || 1);
        const lift = els.filter((el) => el.classList.contains("shape"));   // the shapes that travel: drawn last, so on top
        const setGhost = (idx) => { if (ghost) ghost.remove(); ghost = sp.ghost(idx); svg.insertBefore(ghost, lift[0]); };   // over the others, under the held one
        const end = () => { window.removeEventListener("pointermove", move); window.removeEventListener("pointerup", up); window.removeEventListener("pointercancel", cancel); window.removeEventListener("keydown", key, true); };
        const move = (ev) => {
          if (!on) {
            if (Math.hypot(ev.clientX - x0, ev.clientY - y0) < 6) return;
            on = true; cv.drag = true;
            shape.classList.add("dragging"); svg.classList.add("dragging");
            for (const el of lift) svg.appendChild(el);
            setGhost(target);
          }
          let t;
          if (sp.axis === "xy") {   // a grid of slots (the anycast addresses, four to a column): the pointer picks the nearest slot
            const u = unitsPerPx(), dx = (ev.clientX - x0) * u, dy = (ev.clientY - y0) * u;
            for (const el of els) el.style.transform = `translate(${dx}px, ${dy}px)`;
            let best = Infinity; t = 0;
            for (let k = 0; k < sp.n; k++) { const p = sp.slot(k), dist = Math.hypot(p.x - (sp.pos.x + dx), p.y - (sp.pos.y + dy)); if (dist < best) { best = dist; t = k; } }
          } else {
            const d = (sp.axis === "x" ? ev.clientX - x0 : ev.clientY - y0) * unitsPerPx();
            t = Math.max(0, Math.min(sp.n - 1, Math.round((sp.pos + d - sp.origin) / sp.step)));
            for (const el of els) el.style.transform = sp.axis === "x" ? `translate(${d}px, 0)` : `translate(0, ${d}px)`;
          }
          if (t !== target) { target = t; setGhost(t); }
        };
        const finish = (drop) => {
          end();
          if (!on) return;
          cv.drag = false;
          // the click that follows the button release must not select (and redraw) anything
          const swallow = (ev) => { ev.stopPropagation(); ev.preventDefault(); };
          window.addEventListener("click", swallow, true);
          setTimeout(() => window.removeEventListener("click", swallow, true), 0);
          if (drop && target !== sp.index) sp.drop(target); else draw();
        };
        const up = () => finish(true);
        const cancel = () => finish(false);
        const key = (ev) => { if (ev.key === "Escape") { ev.stopPropagation(); finish(false); } };
        window.addEventListener("pointermove", move);
        window.addEventListener("pointerup", up);
        window.addEventListener("pointercancel", cancel);
        window.addEventListener("keydown", key, true);
      };
      const reorder = (list, from, to) => { const [it] = list.splice(from, 1); list.splice(to, 0, it); };
      shapes.push(sv("g", { class: "shape st-" + st.circle + (sel("gw") ? " sel" : ""), tabindex: "0", role: "button", "aria-label": "Gateway " + gwLabel(g), onclick: pick("gw"), oncontextmenu: rightClick("gw"),
        onkeydown: (e) => { if (e.key === "Enter") pick("gw")(e); } },
        sv("title", {}, gwTip(g, st, dual)),
        sv("circle", { cx, cy: CY, r: R }),
        ...(dual
          ? [label(cx, CY - 14, g.name ? g.name.slice(0, 22) : g.interface + " · group " + g.group_id, "t1"), label(cx, CY + 4, ip4, "t2"), label(cx, CY + 22, ip6, "t2")]
          : [label(cx, CY - 2, g.name ? g.name.slice(0, 22) : g.interface + " · group " + g.group_id, "t1"), label(cx, CY + 16, ip4 || ip6, "t2")])));
      anys.forEach((addr, i) => {
        const at = anyAt(i), x = cx + at.x, y = at.y;
        const a = st.anycast.find((z) => z.addr === addr);
        // A pill is never more alarming than its gateway: green while announced,
        // otherwise it takes the gateway's own colour only when that is amber, red or paused.
        const pzAll = (g.paused_vips || []).includes(addr), pzHere = !st.via && (g.paused_vips_here || []).includes(addr), pz = pzAll || pzHere;
        const c = pz ? "paused" : !a ? "idle" : a.up ? (a.status === "warn" || a.status === "bad" ? a.status : "ok") : ["warn", "bad", "paused"].includes(st.circle) ? st.circle : "idle";
        const why = pz ? "Paused on " + (pzAll ? "all nodes" : "this node") + ": not announced until resumed"
          : !st.via && (g.paused || g.paused_all) ? "Gateway paused on " + (g.paused_all ? "all nodes" : "this node") + ": not announced"
          : !a ? "Applying…" : a.up ? "Announced from " + (st.via || "this node") + " (on lo)" + (a.carried && a.carried.length ? " — kept up by " + a.carried.map((n) => { const x = groups().find((z) => z.group_id === n); return x ? gwLabel(x) : "group " + n; }).join(", ") + (a.reason ? ", because this node cannot answer on it here (" + a.reason + ")" : "") : "") + (a.detail ? " — " + a.detail : "") : "Withdrawn on " + (st.via || "this node") + (a.reason ? ": " + a.reason : "");
        lines.push(sv("line", { x1: cx + R, y1: CY, x2: x, y2: y + AH / 2, class: "edge" }));
        shapes.push(sv("g", { class: "shape drag st-" + c + (sel("any", addr) ? " sel" : ""), tabindex: "0", role: "button", "aria-label": "Anycast address " + addr, onclick: pick("any", addr), oncontextmenu: rightClick("any", addr),
          onpointerdown: dragStart({ axis: "xy", n: own.length, index: i, pos: { x, y }, slot: (k) => ({ x: cx + anyAt(k).x, y: anyAt(k).y }),
            ghost: (k) => sv("rect", { class: "dropslot", x: cx + anyAt(k).x, y: anyAt(k).y, width: AW, height: AH, rx: 18 }),
            drop: (k) => { reorder(g.extra_vips, i, k); cv.sel = { kind: "any", addr }; commit(); } }),
          onkeydown: (e) => { if (e.key === "Enter") pick("any", addr)(e); } },
          sv("title", {}, "Anycast " + addr + " — " + why + (a && !g.paused && !g.paused_all && !pz ? (a.cluster_up_sec > 0 ? "\nOnline for " + spanText(a.cluster_up_sec) : upLine(a.uptime)) : "")),
          sv("rect", { x, y, width: AW, height: AH, rx: 18 }),
          label(x + AW / 2, y + 17, addr, "ip"),
          label(x + AW / 2, y + 32, a && a.up && !pz && a.status === "warn" ? "anycast · neighbor down" : a && a.up && !pz && a.status === "bad" ? (a.bgp === "disabled" ? "anycast · " + (/^OSPF/.test(a.detail || "") ? "OSPF" : "BGP") + " disabled" : /OSPF/.test(a.detail || "") ? "anycast · no OSPF neighbor" : "anycast · no BGP session") : c === "ok" ? "anycast · announced" : c === "paused" && pz ? "paused · " + (pzAll ? "all nodes" : "this node") : c === "paused" ? "anycast · paused" : a && !a.up ? "anycast · withdrawn" : "anycast", "t2")));
      });
      // the cluster's nodes, left of the circle, as parallelograms: drag one to another place (the order is saved with the gateway)
      const nodeIds = nodes.map((n) => n.node_id);
      nodes.forEach((n, i) => {
        const at = nodeAt(i), x = cx + at.x, y = at.y, mid = x + NW / 2;
        lines.push(sv("line", { x1: cx - R, y1: CY, x2: x + NW - NSL / 2, y2: y + AH / 2, class: "edge" + (n.status === "bad" ? " down" : "") }));
        shapes.push(sv("g", { class: "shape drag st-" + n.status, tabindex: "0", role: "button", "aria-label": "Cluster node " + n.name, oncontextmenu: nodeMenu(n),
          onpointerdown: dragStart({ axis: "xy", n: nodeIds.every(Boolean) ? nodes.length : 0, index: i, pos: { x, y }, slot: (k) => ({ x: cx + nodeAt(k).x, y: nodeAt(k).y }),
            ghost: (k) => sv("rect", { class: "dropslot", x: cx + nodeAt(k).x, y: nodeAt(k).y, width: NW, height: AH, rx: 4 }),
            drop: (k) => { const ids = nodeIds.slice(); reorder(ids, i, k); g.node_order = ids; commit(); } }),
          onkeydown: (e) => { if (e.key === "Enter") nodeMenu(n)(e); } },
          sv("title", {}, nodeTip(n)),
          sv("polygon", { points: `${x + NSL},${y} ${x + NW},${y} ${x + NW - NSL},${y + AH} ${x},${y + AH}` }),
          (() => {   // the name, then the star (this node), and the dot (controller) and triangle (primary) raised to the star's height
            const ctl = isController(n), prim = n.role === "primary", hi = (ctl ? " •" : "") + (prim ? " ▴" : ""), nm = fitPx(n.name, NW - 2 * NSL - 16 - (n.self ? 9 : 0) - hi.length * 6, "600 13px system-ui, sans-serif");
            // the dot is blue when this node is only the IPv6 controller of a gateway whose IPv4 controller is another node
            return sv("text", { x: mid, y: y + 17, "text-anchor": "middle", class: "t1" }, nm + (n.self ? " *" : ""),
              ctl ? sv("tspan", { dy: "-4", class: controllerKind(n) === "v6" ? "agc6" : "" }, " •") : null,
              prim ? sv("tspan", ctl ? {} : { dy: "-4" }, " ▴") : null);
          })(),
          label(mid, y + 32, fitPx(n.label, NW - 2 * NSL - 12, "400 11px system-ui, sans-serif"), "t2"),
          n.soak_sec > 0 ? sv("text", { x: x + NSL + 8, y: y + 17, class: "t1 soak" }, sv("title", {}, soakTip(n.soak_sec)), "⏳") : null));
      });
      const goLogs = (x) => () => { state.prefill.logs = x.addr; selectTab("logs"); };   // a click opens the Logs page on that sender's messages
      devs.forEach((x, i) => {
        const { x: px, y } = devAt(i), mx = px + SW / 2;
        lines.push(sv("line", { x1: cx, y1: CY + R, x2: mx, y2: y, class: "edge" + (x.status === "bad" ? " down" : "") }));
        shapes.push(sv("g", { class: "shape st-" + x.status, tabindex: "0", role: "button", "aria-label": "Sender " + expLabel(x), onclick: goLogs(x), oncontextmenu: rowMenu(netToolMenu(x.addr)),
          onkeydown: (e) => { if (e.key === "Enter") goLogs(x)(e); } },
          sv("title", {}, expTip(x)),
          sv("rect", { x: px, y, width: SW, height: SH, rx: 4 }),
          label(mx, y + 19, fitPx(expLabel(x), SW - 20, "600 13px system-ui, sans-serif"), "t1"),
          label(mx, y + 35, fitPx(expSub(x), SW - 20, "400 11px system-ui, sans-serif"), "t2")));
      });
      // the small squares (more than DOT_FROM senders): each joined to the gateway; one full-size square, drawn last so nothing covers it, is shown
      // beside the one the pointer (or the keyboard) is on
      const hov = sv("g", { class: "shape hovcard", visibility: "hidden" }, sv("rect", { x: 0, y: 0, width: SW, height: SH, rx: 4 }), label(SW / 2, 19, "", "t1"), label(SW / 2, 35, "", "t2"));
      const hovT1 = hov.querySelector("text.t1"), hovT2 = hov.querySelector("text.t2");
      const hideHov = () => hov.setAttribute("visibility", "hidden");
      dotted.forEach((x, i) => {
        const { x: px, y } = dotAt(i), mx = px + DS / 2, c = x.status;
        const showHov = () => {
          const hx = Math.max(4, Math.min(W - SW - 4, mx - SW / 2)), hy = Math.max(2, y - SH - 6);   // above the dot, so the dots beside and below it stay in reach
          hov.setAttribute("class", "shape hovcard st-" + c);
          hov.setAttribute("transform", `translate(${hx} ${hy})`);
          hovT1.textContent = fitPx(expLabel(x), SW - 20, "600 13px system-ui, sans-serif");
          hovT2.textContent = fitPx(expSub(x), SW - 20, "400 11px system-ui, sans-serif");
          hov.setAttribute("visibility", "visible");
        };
        lines.push(sv("line", { x1: cx, y1: CY + R, x2: mx, y2: y, class: "edge thin" + (x.status === "bad" ? " down" : "") }));
        shapes.push(sv("g", { class: "shape dotsq st-" + c, tabindex: "0", role: "button", "aria-label": "Sender " + expLabel(x),
          onclick: goLogs(x), oncontextmenu: rowMenu(netToolMenu(x.addr)), onmouseenter: showHov, onmouseleave: hideHov, onfocus: showHov, onblur: hideHov,
          onkeydown: (e) => { if (e.key === "Enter") goLogs(x)(e); } },
          sv("title", {}, expTip(x)),
          sv("rect", { x: px, y, width: DS, height: DS, rx: 2 })));
      });
      svg.append(...lines, ...shapes);
      if (dotted.length) svg.append(hov);
      focusTopoFilter(g.group_id);
      // The scrolling box (.cv-wrap) is made once and kept: only the drawing inside it is swapped on each refresh, so its
      // scrollbars neither blink nor get pulled from under the pointer while they are being dragged.
      let wrap = oldWrap && svgHost.contains(oldWrap) ? oldWrap : null;
      if (!wrap) {
        clear(svgHost);
        wrap = h("div", { class: "cv-wrap" });
        svgHost.append(wrap);
        wrap.addEventListener("wheel", (e) => { if (cv.zoomBy) cv.zoomBy(e); }, { passive: false });
        // A double-click on the empty drawing fits it again. Timed by hand from the two presses: the first click redraws
        // the drawing (it deselects), and the browser then does not report the pair as a double-click.
        let lastDown = null;
        wrap.addEventListener("dragstart", (e) => e.preventDefault());   // never a native drag of selected text or an image
        // Drag the empty drawing to move around it, in any direction (the box scrolls sideways, the page up and down).
        wrap.addEventListener("pointerdown", (e) => {
          const t = e.target;
          if (e.button !== 0 || cv.drag || !(t === wrap || (t.tagName && t.tagName.toLowerCase() === "svg"))) return;   // only the empty background
          const now = Date.now();
          if (lastDown && now - lastDown.t < 400 && Math.hypot(e.clientX - lastDown.x, e.clientY - lastDown.y) < 6 && cv.zoom && cv.unzoom) { lastDown = null; cv.unzoom(); return; }
          lastDown = { t: now, x: e.clientX, y: e.clientY };
          let vert = null;
          for (let el = wrap; el; el = el.parentElement) {
            const oy = getComputedStyle(el).overflowY;
            if (el.scrollHeight > el.clientHeight && (oy === "auto" || oy === "scroll")) { vert = el; break; }
          }
          const x0 = e.clientX, y0 = e.clientY, sl0 = wrap.scrollLeft, st0 = vert ? vert.scrollTop : 0;
          let moved = false;
          const move = (ev) => {
            if (!moved) {
              if (Math.hypot(ev.clientX - x0, ev.clientY - y0) < 4) return;
              moved = true; wrap.classList.add("panning");
            }
            wrap.scrollLeft = sl0 - (ev.clientX - x0);
            if (vert) vert.scrollTop = st0 - (ev.clientY - y0);
          };
          const end = () => {
            window.removeEventListener("pointermove", move); window.removeEventListener("pointerup", end); window.removeEventListener("pointercancel", end);
            wrap.classList.remove("panning");
            if (!moved) return;
            // the click that follows the release must not deselect (and redraw) anything
            const swallow = (ev) => { ev.stopPropagation(); ev.preventDefault(); };
            window.addEventListener("click", swallow, true);
            setTimeout(() => window.removeEventListener("click", swallow, true), 0);
          };
          window.addEventListener("pointermove", move); window.addEventListener("pointerup", end); window.addEventListener("pointercancel", end);
        });
        if (cv.ro) cv.ro.disconnect();
        if (window.ResizeObserver) { cv.ro = new ResizeObserver(() => { if (cv.fit) cv.fit(); }); cv.ro.observe(wrap); }
      }
      // The size: fitted to the window it is in (never larger than drawn, never smaller than CV_MIN_SCALE: a wider one
      // scrolls), until the mouse wheel zooms it; then cv.zoom (kept across the refreshes, reset for another gateway)
      // holds the scale, and a double-click on the empty drawing goes back to fitting.
      if (cv.zoomGid !== cv.gid) { cv.zoom = null; cv.zoomGid = cv.gid; }
      const fitScale = () => Math.max(CV_MIN_SCALE, Math.min(1, (wrap.clientWidth || W) / W));
      const fit = () => {
        const s = cv.zoom || fitScale();
        svg.style.width = Math.round(W * s) + "px"; svg.style.height = Math.round(H * s) + "px";   // CSSOM, allowed by the CSP
        wrap.style.minHeight = Math.round(H * fitScale()) + "px";   // zooming out must not shrink the box out from under the pointer
      };
      if (!cv.resizeBound) {   // a taller or shorter window changes how far the rows are spread: draw again once it has settled
        cv.resizeBound = true;
        let t = null;
        window.addEventListener("resize", () => { clearTimeout(t); t = setTimeout(() => { if (svgHost && svgHost.isConnected && cv.loaded && !cv.drag) draw(); }, 200); });
      }
      fit();                       // sized before it goes in, so the box never sees it at another size (that would clamp the scroll)
      wrap.replaceChildren(svg);   // one step: no frame without a drawing
      cv.fit = fit;
      cv.zoomBy = (e) => {
        e.preventDefault();   // the wheel over the drawing zooms it instead of scrolling the page
        const unit = e.deltaMode === 1 ? 16 : e.deltaMode === 2 ? 100 : 1;
        const r0 = svg.getBoundingClientRect();
        const s0 = r0.width / W || 1;
        const s1 = Math.max(CV_ZOOM_MIN, Math.min(CV_ZOOM_MAX, s0 * Math.exp((-e.deltaY * unit) * 0.0015)));
        if (Math.abs(s1 - s0) < 0.001) return;
        const ux = (e.clientX - r0.left) / s0, uy = (e.clientY - r0.top) / s0;   // the point under the pointer, in drawing units
        cv.zoom = s1; fit();
        const r1 = svg.getBoundingClientRect();
        wrap.scrollLeft += r1.left + ux * s1 - e.clientX;   // keep that point under the pointer
        let rest = r1.top + uy * s1 - e.clientY;             // … vertically: the drawing grows taller than its box, so the page scrolls
        for (let el = wrap; el && rest; el = el.parentElement) {
          if (el.scrollHeight <= el.clientHeight) continue;
          const oy = getComputedStyle(el).overflowY;
          if (oy !== "auto" && oy !== "scroll") continue;
          const was = el.scrollTop; el.scrollTop += rest; rest -= el.scrollTop - was;
        }
      };
      cv.unzoom = () => { cv.zoom = null; fit(); wrap.scrollLeft = 0; wrap.scrollTop = 0; };
      wrap.scrollLeft = keep.x; wrap.scrollTop = keep.y;
      cv.drawn = cv.gid;
    }

    // ── popup dialogs and the right-click menu ───────────────────────────
    let dlg = null, menuEl = null;
    function closeEditor() {
      if (!dlg) return;
      const d = dlg; dlg = null;
      try { d.close(); } catch (_) { /* already closed */ }
      d.remove();
    }
    function openDialog(title, ...kids) {
      closeEditor(); closeMenu();
      const d = h("dialog", { class: "cv-dialog", "aria-label": title }, h("h2", {}, title), ...kids);
      d.addEventListener("close", () => { if (dlg === d) { dlg = null; d.remove(); } });
      dlg = d;
      root.append(d);
      d.showModal();
      return d;
    }
    // onOk returns null (done: saved), an error text (kept open), or "" with noSave (done, nothing to save)
    function form(title, fields, onOk, okLabel, noSave) {
      const els = {}, hints = {};
      const grid = h("div", { class: "grid" }, fields.map((f) => {
        els[f.k] = f.check
          ? h("input", { type: "checkbox", checked: !!f.v })
          : f.multi
          ? h("select", { multiple: true, size: String(Math.min(Math.max(f.options.length, 3), 10)) }, f.options.map((o) => h("option", { value: o, selected: o === f.v }, o)))   // Shift-click and Ctrl-click pick several
          : f.options
          ? h("select", {}, f.options.map((o) => h("option", { value: o, selected: o === f.v }, o)))
          : h("input", { type: f.secret ? "password" : "text", autocomplete: f.secret ? "new-password" : "off", spellcheck: "false", value: f.v || "", disabled: f.disabled, placeholder: f.ph || "" });
        hints[f.k] = h("span", { class: "hint" }, f.hint || "");
        // a field may react when it is left (f.on): it gets the fields, and note(key, text) to say something under a field
        if (f.on) els[f.k].addEventListener("change", () => f.on(els, (k, t) => { const o = fields.find((x) => x.k === k); hints[k].textContent = (o && o.hint ? o.hint : "") + (t ? (o && o.hint ? " " : "") + t : ""); }));
        if (f.check) return h("label", { class: "f check wide" }, els[f.k], f.l);
        // a list of many: a filter box above it (words, AND, OR, ! and NOT) that narrows the list; what was picked stays picked
        let flt = null, fin = null;
        if (f.multi) {
          const sel = els[f.k], all = f.options, chosen = new Set(), count = h("span", { class: "hint" }, "");
          const paint = () => {
            for (const o of sel.options) { if (o.selected) chosen.add(o.value); else chosen.delete(o.value); }
            const q = parseFilter(fin.value), keep = all.filter((o) => q.match((w) => (o + " " + ((f.hay && f.hay[o]) || "")).toLowerCase().includes(w)));
            clear(sel).append(...keep.map((o) => h("option", { value: o, selected: chosen.has(o) }, o)));
            count.textContent = q.empty ? "" : keep.length + " of " + all.length + " shown" + (chosen.size ? ", " + chosen.size + " picked" : "");
          };
          fin = h("input", { type: "text", class: "listfilter", autocomplete: "off", spellcheck: "false", placeholder: "Filter: phx AND !dns, sea OR nyc", "aria-label": "Filter " + f.l,
            oninput: paint, onkeydown: (e) => { if (e.key === "Enter") e.preventDefault(); } });
          sel.addEventListener("change", () => { for (const o of sel.options) { if (o.selected) chosen.add(o.value); else chosen.delete(o.value); } });
          els[f.k]._picked = () => { for (const o of sel.options) { if (o.selected) chosen.add(o.value); else chosen.delete(o.value); } return all.filter((o) => chosen.has(o)); };
          flt = h("div", { class: "listfilterbox" }, fin, count);
        }
        return h("label", { class: "f" + (fields.length === 1 || f.wide ? " wide" : "") }, f.l, els[f.k], flt, f.hint || f.on ? hints[f.k] : null);
      }));
      for (const f of fields) if (f.on && f.initOn) f.on(els, () => {});
      const err = h("div", { "aria-live": "polite" });
      const submit = (e) => {
        e.preventDefault();
        const vals = {};
        for (const f of fields) vals[f.k] = f.check ? (els[f.k].checked ? "1" : "") : f.multi ? els[f.k]._picked().join("\n") : els[f.k].value.trim();
        const m = onOk(vals);
        if (m) { errorBox(err, m); return; }
        closeEditor();
        if (!noSave) commit();
      };
      openDialog(title, h("form", { onsubmit: submit }, grid, err,
        h("div", { class: "toolbar" }, h("button", { class: "btn primary", type: "submit" }, okLabel || "OK"),
          h("button", { class: "btn", type: "button", onclick: closeEditor }, "Cancel"))));
      const first = fields.find((f) => !f.disabled);
      if (first) els[first.k].focus();
    }
    function confirmDialog(title, text, okLabel, onOk, cls) {
      const go = h("button", { class: cls || "btn danger", type: "button", onclick: () => { closeEditor(); onOk(); } }, okLabel);
      openDialog(title, h("p", {}, text),
        h("div", { class: "toolbar" }, go, h("button", { class: "btn", type: "button", onclick: closeEditor }, "Cancel")));
      go.focus();
    }

    function closeMenu() {
      if (!menuEl) return;
      closeSub();
      menuEl.remove(); menuEl = null;
      document.removeEventListener("click", outsideClick, true);
      document.removeEventListener("keydown", menuKey, true);
      window.removeEventListener("blur", closeMenu);
      window.removeEventListener("resize", closeMenu);
    }
    let subEl = null, sub2El = null;   // the second menu, and a third one beside it for a sub-item that has sub-items of its own
    function closeSub2() {
      if (sub2El) { sub2El.remove(); sub2El = null; }
      if (subEl) subEl.querySelectorAll("button.open").forEach((b) => b.classList.remove("open"));
    }
    function closeSub() {
      if (sub2El) { sub2El.remove(); sub2El = null; }
      if (subEl) { subEl.remove(); subEl = null; }
      // the parent item's "open" highlight must go with its submenu, or it stays lit after the pointer moves on
      if (menuEl) menuEl.querySelectorAll("button.open").forEach((b) => b.classList.remove("open"));
    }
    const outsideClick = (e) => { if (!(menuEl && menuEl.contains(e.target)) && !(subEl && subEl.contains(e.target)) && !(sub2El && sub2El.contains(e.target))) closeMenu(); };
    function menuKey(e) {
      if (!menuEl) return;
      if (e.key === "Escape") { e.preventDefault(); closeMenu(); return; }
      const cur = document.activeElement;
      if (e.key === "ArrowRight" && cur && cur.dataset && cur.dataset.sub) { e.preventDefault(); cur.click(); return; }
      if (e.key === "ArrowLeft" && sub2El && sub2El.contains(cur)) { e.preventDefault(); const p = subEl.querySelector("button.open"); closeSub2(); if (p) p.focus(); return; }
      if (e.key === "ArrowLeft" && subEl && subEl.contains(cur)) { e.preventDefault(); const p = menuEl.querySelector("button.open"); closeSub(); if (p) p.focus(); return; }
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        const box = sub2El && sub2El.contains(cur) ? sub2El : subEl && subEl.contains(cur) ? subEl : menuEl;
        const items = [...box.querySelectorAll("button")];
        const i = items.indexOf(cur);
        items[(i + (e.key === "ArrowDown" ? 1 : items.length - 1)) % items.length].focus();
      }
    }
    // items: [label, handler, "danger"?, subitems?]. An item with subitems opens a second menu beside the first
    // when the pointer rests on it (or on click / the right arrow key); the sub-items are [label, handler, why-not?, subitems?] (a reason greys one out; a sub-item with subitems opens a third menu).
    let menuAt = { x: 0, y: 0 };
    function openMenu(e, items) {
      e.preventDefault(); e.stopPropagation();
      closeMenu();
      menuAt = { x: e.clientX || 0, y: e.clientY || 0 };
      const openSub2 = (btn, subs2) => {
        closeSub2();
        btn.classList.add("open");
        sub2El = h("div", { class: "cv-menu cv-sub", role: "menu" }, subs2.map(([label, fn, off]) => off
          ? h("button", { type: "button", role: "menuitem", disabled: true, title: off }, plain(label))
          : h("button", { type: "button", role: "menuitem", onclick: () => { closeMenu(); fn(); } }, plain(label))));
        root.append(sub2El);
        const pr = subEl.getBoundingClientRect(), br = btn.getBoundingClientRect(), sr = sub2El.getBoundingClientRect();
        const left = pr.right + sr.width + 4 <= window.innerWidth ? pr.right - 2 : Math.max(4, pr.left - sr.width + 2);
        sub2El.style.left = left + "px";
        sub2El.style.top = Math.max(4, Math.min(br.top - 4, window.innerHeight - sr.height - 4)) + "px";
      };
      const openSub = (btn, subs) => {
        closeSub();
        menuEl.querySelectorAll("button.open").forEach((b) => b.classList.remove("open"));
        btn.classList.add("open");
        subEl = h("div", { class: "cv-menu cv-sub", role: "menu" }, subs.map(([label, fn, off, subs2]) => {
          if (subs2 && !off) {
            const b = h("button", { type: "button", role: "menuitem", class: "has-sub", "aria-haspopup": "true", "data-sub": "1", onclick: () => { openSub2(b, subs2); sub2El.querySelector("button").focus(); } }, plain(label), h("span", { class: "arrow" }, "▸"));
            b.addEventListener("mouseenter", () => openSub2(b, subs2));
            return b;
          }
          return off
            ? h("button", { type: "button", role: "menuitem", disabled: true, title: off, onmouseenter: closeSub2 }, plain(label))
            : h("button", { type: "button", role: "menuitem", onmouseenter: closeSub2, onclick: () => { closeMenu(); fn(); } }, plain(label));
        }));
        root.append(subEl);
        const pr = menuEl.getBoundingClientRect(), br = btn.getBoundingClientRect(), sr = subEl.getBoundingClientRect();
        const left = pr.right + sr.width + 4 <= window.innerWidth ? pr.right - 2 : Math.max(4, pr.left - sr.width + 2);
        subEl.style.left = left + "px";
        subEl.style.top = Math.max(4, Math.min(br.top - 4, window.innerHeight - sr.height - 4)) + "px";
      };
      menuEl = h("div", { class: "cv-menu", role: "menu" }, items.map((it) => {
        if (it === "-") return h("div", { class: "sep", role: "separator" });
        const [label0, fn, cls, subs, off] = it;
        const label = plain(label0);
        if (!subs) {
          if (off) return h("button", { type: "button", role: "menuitem", class: cls || "", disabled: true, title: off, onmouseenter: closeSub }, label);
          return h("button", { type: "button", role: "menuitem", class: cls || "", onmouseenter: closeSub, onclick: () => { closeMenu(); fn(); } }, label);
        }
        if (off) return h("button", { type: "button", role: "menuitem", class: "has-sub", disabled: true, title: off, onmouseenter: closeSub }, label);
        const btn = h("button", { type: "button", role: "menuitem", class: "has-sub", "aria-haspopup": "true", "data-sub": "1", onclick: () => { openSub(btn, subs); subEl.querySelector("button").focus(); } }, label, h("span", { class: "arrow" }, "▸"));
        btn.addEventListener("mouseenter", () => openSub(btn, subs));
        return btn;
      }));
      root.append(menuEl);
      const r = menuEl.getBoundingClientRect();
      // keyboard-opened menus report 0,0: fall back to the element's corner
      const x = e.clientX || (e.target.getBoundingClientRect ? e.target.getBoundingClientRect().left : 0);
      const y = e.clientY || (e.target.getBoundingClientRect ? e.target.getBoundingClientRect().bottom : 0);
      menuEl.style.left = Math.max(4, Math.min(x, window.innerWidth - r.width - 4)) + "px";   // CSSOM, allowed by the CSP
      menuEl.style.top = Math.max(4, Math.min(y, window.innerHeight - r.height - 4)) + "px";
      menuEl.querySelector("button").focus();
      document.addEventListener("click", outsideClick, true);
      document.addEventListener("keydown", menuKey, true);
      window.addEventListener("blur", closeMenu);
      window.addEventListener("resize", closeMenu);
    }
    // "Pause ▸ This node / All nodes" and "Resume ▸ …": a menu that opens beside this one while the pointer is on the item.
    // `scopeOn(scope)` says whether the thing is paused in that scope; only the scopes that can still change are offered.
    const pauseItems = (noun, scopeOn, act, whyNot) => {
      const subs = (pause) => [["node", "This node"], ["all", "All nodes"]].filter(([sc]) => scopeOn(sc) !== pause).map(([sc, t]) => [t, () => act(sc, pause)]);
      const items = [];
      // never both: a paused item offers only Resume (for the scopes it is paused in), otherwise only Pause
      if (scopeOn("node") || scopeOn("all")) items.push(["Resume" + (noun ? " " + noun : ""), null, "", subs(false)]);
      else items.push(["Pause" + (noun ? " " + noun : ""), null, "", subs(true), whyNot || ""]);
      return items;
    };
    const byName = (a, b) => a.localeCompare(b, undefined, { numeric: true, sensitivity: "base" });
    const gwItems = (g) => [["Statistics", () => selectTab("mcpusage")],
      "-",
      ["Edit", null, "", [["Gateway", () => gatewayForm(g)], ["MCP", () => selectTab("cfg_mcp")]]],
      ["Add", null, "", [nodeAddItem(g), ["Anycast address", () => anycastForm(g)]]],
      ...pauseItems("", (sc) => !!g[sc === "all" ? "paused_all" : "paused"], (sc, p) => pauseGateway(g, sc, p)), "-", ["Delete", delSel, "danger"]];
    // Pause / resume announcing one anycast address: on this node only (kept in this node's own settings) or on every node (shared)
    const anyPauseList = (g, scope) => (scope === "all" ? "paused_vips" : "paused_vips_here");
    const anyPaused = (g, addr, scope) => (g[anyPauseList(g, scope)] || []).includes(addr);
    function pauseAnycast(g, addr, scope, pause) {
      quick(() => {
        const k = anyPauseList(g, scope), l = (g[k] || []).filter((x) => x !== addr);
        g[k] = pause ? l.concat(addr) : l;
        if (!g[k].length) delete g[k];
      });
    }
    // Anycast addresses: right-click one ▸ Copy ▸ Anycast address / All anycast addresses; right-click another gateway ▸ Paste ▸ the same.
    // They are added to that gateway's own list (a pause stays where it was).  Copy also puts them on the system clipboard as plain
    // text.  Pasting on the gateway they came from is refused; an address the gateway already has, or that is any gateway's shared
    // address, is left out.
    let anyClip = null, anysClip = null;
    const copyAnycast = (g, addr) => { anyClip = { list: [addr], gid: g.group_id }; try { navigator.clipboard.writeText(addr); } catch (_) { /* the page keeps it for Paste */ } };
    const copyAllAnycast = (g) => { anysClip = { list: (g.extra_vips || []).slice(), gid: g.group_id }; try { navigator.clipboard.writeText(anysClip.list.join("\n")); } catch (_) { /* the page keeps it for Paste */ } };
    const anyPasteList = (g, clip) => (clip.list || []).map((x) => x.toLowerCase()).filter((a) => !(g.extra_vips || []).includes(a)
      && !groups().some((x) => [x.vip4, x.vip6, ...(x.more_vip4 || []), ...(x.more_vip6 || [])].some((z) => z && z.split("/")[0].toLowerCase() === a)));
    const pasteAnyWhy = (g, clip, none) => {
      if (!clip) return none;
      if (clip.gid === g.group_id) return "Those are on this gateway: paste them on another gateway";
      if (!clip.list.length) return "That gateway has no anycast addresses";
      if (!anyPasteList(g, clip).length) return clip.list.length === 1 ? "This gateway already has that address, or it is a gateway's own address" : "This gateway already has all of them, or they are gateways' own addresses";
      return "";
    };
    const pasteOneWhy = (g) => pasteAnyWhy(g, anyClip, "No anycast address copied yet: right-click an anycast address ▸ Copy ▸ Anycast address");
    const pasteAllWhy = (g) => pasteAnyWhy(g, anysClip, "No anycast addresses copied yet: right-click an anycast address ▸ Copy ▸ All anycast addresses");
    function pasteAnycast(g, clip) {
      if (pasteAnyWhy(g, clip, "none")) return;
      const add = anyPasteList(g, clip);
      quick(() => { g.extra_vips = (g.extra_vips || []).concat(add); cv.sel = { kind: "any", addr: add[0] }; });
    }
    const copyAnyItem = (g, addr) => ["Copy", null, "", [["Anycast address", () => copyAnycast(g, addr)], ["All anycast addresses", () => copyAllAnycast(g), (g.extra_vips || []).length > 1 ? "" : "This gateway has only this one"]]];
    const pasteAnySubs = (g) => [["Anycast address", () => pasteAnycast(g, anyClip), pasteOneWhy(g)], ["All anycast addresses", () => pasteAnycast(g, anysClip), pasteAllWhy(g)]];
    const anyItems = (g, addr) => [["Edit", () => anycastForm(g, addr)], copyAnyItem(g, addr),
      ...pauseItems("", (sc) => anyPaused(g, addr, sc), (sc, p) => pauseAnycast(g, addr, sc, p)), "-", ["Delete", delSel, "danger"]];
    // the cluster's nodes (the parallelograms left of the circle)
    const nodeTip = (n) => [
      n.name + (n.self ? " (this node)" : "") + " — " + n.detail,
      "Address: " + n.addr + (n.hostname && n.hostname !== n.name ? " (host " + n.hostname + ")" : ""),
      ...(n.addrs || []).filter((i) => (i.v4 || []).length + (i.v6 || []).length).map((i) => i.name + ": " + [...(i.v4 || []), ...(i.v6 || [])].join(", ")),   // Ethernet interfaces: IPv4 and IPv6 GUA
      "Role: " + (n.role === "primary" ? "primary" : "replica"),
      isController(n) ? (controllerKind(n) === "v6" ? "Gateway controller (IPv6)" : "Gateway controller") : "",
      n.self ? "Last seen: just now" : n.reachable ? "Last seen: " + (n.last_seen ? spanText(Date.now() / 1000 - n.last_seen) + " ago" : "just now") : "",
      n.uptime_sec > 0 ? "Up: " + spanText(n.uptime_sec) : "",
      n.version ? "Version: " + n.version + (n.version_differs ? " (not the same as this node's)" : "") : "",
      n.reachable || n.self ? (n.behind ? "Settings: behind the primary's" : "Settings: up to date") : "",
      n.host ? "Host: " + (n.host.cpu_pct >= 0 ? "CPU " + Math.round(n.host.cpu_pct) + "%, " : "") + "memory " + Math.round(n.host.mem_pct) + "%" + (n.host.disk_pct >= 0 ? ", disk " + Math.round(n.host.disk_pct) + "%" + (n.host.disk_mount ? " (" + n.host.disk_mount + ")" : "") : "") + " — amber above 85%" : "",
      n.updating ? "An update is in progress" : "", n.soak_sec > 0 ? soakTip(n.soak_sec) : "", n.update_failed ? "Update failed: " + n.update_failed : "",
    ].filter(Boolean).join("\n");
    // open another node (the Node menu, top right), optionally on one page; this node's own address stands for "this node"
    const openNode = (n, tab) => { const me = selfNode(); setTarget(me && n.addr === me.addr ? null : n.addr); if (tab) selectTab(tab); };
    // Pause or resume any reachable node from its shape: this node directly, another through the cluster relay (the same call the
    // Node page makes on the node picked in the Node menu).  Until that node reports back (up to a sync interval) the shape shows
    // "pausing…" / "resuming…" so the click is seen at once.
    const nodeCall = (n, method, path, body) => {
      const me = selfNode(), to = n.self || (me && n.addr === me.addr) ? null : n.addr;   // this node: straight, never through the relay
      return api(method, to ? "/api/proxy?node=" + encodeURIComponent(to) + "&path=" + encodeURIComponent(path) : path, body, true);
    };
    function pauseNode(n) {
      const want = !n.node_paused;
      const go = async () => {
        try { await nodeCall(n, "POST", "/api/nodepause", { paused: want }); }
        catch (e) { refresh(); return; }   // the menu item is greyed when the server would refuse; no message
        cv.nodeWait = cv.nodeWait || {};
        cv.nodeWait[n.addr] = { want, until: Date.now() + 15000 };
        draw(); refresh();
      };
      go();
    }
    // Rename a node: its operating system host name and the /etc/hosts entry (CLI: --node-rename)
    function renameNode(n) {
      const input = h("input", { type: "text", autocomplete: "off", spellcheck: "false", maxlength: "253", value: n.hostname || n.name || "", "aria-label": "Host name" });
      const err = h("div", { "aria-live": "polite" });
      const save = h("button", { class: "btn primary", type: "submit" }, "Rename");
      openDialog("Rename " + (n.name || n.addr),
        h("form", { onsubmit: async (ev) => {
          ev.preventDefault();
          const v = input.value.trim().toLowerCase();
          if (!v || v === (n.hostname || "")) { closeEditor(); return; }
          save.disabled = true;
          try { await nodeCall(n, "POST", "/api/noderename", { name: v }); }
          catch (e) { save.disabled = false; errorBox(err, e.message); return; }
          closeEditor(); refresh();
        } },
        h("label", { class: "f wide" }, "Host name", input),
        err,
        h("div", { class: "toolbar" }, save, h("button", { class: "btn", type: "button", onclick: closeEditor }, "Cancel"))));
      input.focus(); input.select();
    }
    // Make a node the gateway controller for all groups (the Node page's button, aimed at the node whose shape was clicked): it takes
    // the role first, then the node that held it is asked to step down, so clients lose nothing.
    function makeController(n) {
      const go = async () => {
        try { await nodeCall(n, "POST", "/api/assert-agc", {}); } catch (e) { /* the drawing shows who the controller is */ }
        draw(); refresh();
      };
      go();
    }
    // Restart or shut down the host of a node, now (the Node page's Host section, aimed at the node whose shape was clicked).  When a
    // gateway would lose its last serving member the daemon says so, and the admin can go ahead anyway.
    function powerNode(n, action) {
      const verb = action === "restart" ? "Restart" : "Shut down", who = n.self ? "this node" : n.name;
      const run = async (force) => {
        try {
          await nodeCall(n, "POST", "/api/power", force ? { action, when: "now", force: true } : { action, when: "now" });
        } catch (e) {
          if (!force && /^not safe to /.test(e.message || "")) {
            confirmDialog("Go ahead anyway?", String(e.message).replace(/ \(to go ahead anyway.*$/, "") + " Clients of that gateway will be interrupted.", verb + " anyway", () => run(true));
            return;
          }
        }
      };
      if (action === "restart") { run(false); return; }   // no popup: the daemon itself refuses (and asks) when a gateway would lose its last serving member
      confirmDialog(verb + " the host of " + who + "?", (action === "restart" ? "The host reboots" : "The host powers off and stays off until it is powered on again") +
        (n.self ? (action === "restart" ? ", and you lose access to this page until it is back. " : ". You lose access to this page. ") : ". ") +
        "Its gateways stop serving and the other nodes take over.", verb + " host", () => run(false));
    }
    // Remove a node from the gateway shown (a shared setting, kept in the gateway's own settings): it serves nothing of it, whichever
    // node you look from, and is no longer drawn.  The gateway's right-click ▸ Add node brings it back.  The last node serving it
    // cannot be removed (pause the gateway instead).
    function nodeMember(n) {
      const g = curGroup();
      if (!g) return;
      const left = (statusOf(g).nodes || []).filter((x) => x.node_id && x.node_id !== n.node_id && !(g.excluded_nodes || []).includes(x.node_id)).length;
      if (!left) return;
      confirmDialog("Remove " + (n.self ? "this node" : n.name) + " from gateway " + gwLabel(g) + "?",
        "It stops serving the gateway — it gives up the address and stops answering on it — and the other nodes carry on. Other gateways are not affected. Add it back from the same menu.",
        "Remove node", () => quick(() => toggleIn(g, "excluded_nodes", n.node_id, true)), "btn primary");
    }
    const memberItem = (n) => (curGroup() && n.node_id ? [["Remove from this gateway", () => nodeMember(n)]] : []);
    // the nodes removed from a gateway, by name (an entry for a node that has left the cluster can still be cleared)
    const removedNodes = (g) => {
      const nodes = statusOf(g).nodes || [];
      return (g.excluded_nodes || []).map((id) => {
        const n = nodes.find((x) => x.node_id === id);
        return { id, gone: !!nodes.length && !n, label: n ? n.name + (n.self ? " (this node)" : "") : "A node that left the cluster (" + id.slice(0, 8) + ")" };
      });
    };
    // Add ▸ Node lists the nodes that were taken off the gateway and are still in the cluster.  An entry for a node that has left the
    // cluster is not a node that can be added: one item, "Forget nodes that left the cluster", clears them all from this gateway.
    const nodeSubs = (g) => {
      const l = removedNodes(g), live = l.filter((r) => !r.gone), gone = l.filter((r) => r.gone);
      if (!live.length && !gone.length) return ["Node", null, "Every node already serves this gateway"];
      return ["Node", null, "", [
        ...live.map((r) => [r.label, () => quick(() => toggleIn(g, "excluded_nodes", r.id, false))]),
        ...(gone.length ? [["Forget " + gone.length + " node" + (gone.length > 1 ? "s" : "") + " that left the cluster", () => quick(() => { g.excluded_nodes = (g.excluded_nodes || []).filter((id) => !gone.some((r) => r.id === id)); if (!g.excluded_nodes.length) delete g.excluded_nodes; })]] : [])]];
    };
    const nodeAddItem = nodeSubs;
    // The last node in service cannot be paused: the item is greyed out, with the reason as its tooltip.  A node that is not answering or
    // is paused is out of service.
    const lastNodeWhy = (n) => {
      const gs = groups(), all = gs.length ? statusOf(gs[0]).nodes || [] : [];
      if (!all.some((x) => x.addr !== n.addr && x.reachable && !x.node_paused)) return "The only node in service (the others are paused or not answering): pausing it would stop every gateway";
      // the last node serving a gateway: every other node is paused, not answering or not serving it
      const serving = (x) => x.reachable && !x.node_paused && (x.status === "ok" || x.status === "warn");
      for (const g of gs) {
        const ns = statusOf(g).nodes || [];
        if (ns.some((x) => x.addr === n.addr && serving(x)) && !ns.some((x) => x.addr !== n.addr && serving(x))) return "The only node serving gateway " + gwLabel(g) + ": pausing it would stop that gateway";
      }
      return "";
    };
    // Make controller is greyed on the node that already holds the role for every gateway it is a member of.  A member is
    // matched to a node by its address on the gateway's subnet (this node's own member is marked local).
    // the node whose picture a gateway shows when the signed-in node does not serve it: its own member is flagged via_local
    const viaAddr = (m) => m.via_addr || "";
    const isController = (n, all) => {  // all: holds the role for every gateway (else: for at least one)
      const ips = new Set((n.addrs || []).flatMap((i) => [...(i.v4 || []), ...(i.v6 || [])].map((x) => String(x).split("/")[0])));
      const withMembers = groups().map((g) => statusOf(g).members || []).filter((ms) => ms.length);
      const isIt = (m) => !n.node_paused && (m.via_local ? n.addr === viaAddr(m) : m.local ? n.self : ips.has(m.ip));   // a paused node is never the controller
      const has = (ms) => ms.some((m) => m.agc && isIt(m));
      // all: for every gateway this node is a member of (one it is not on, another subnet, never gives it the role)
      const mine = withMembers.filter((ms) => ms.some(isIt));
      return all ? mine.length > 0 && mine.every(has) : withMembers.some(has);
    };
    // Which kind of controller a node is: "v6" when it is only the IPv6 controller of every gateway it controls and that
    // gateway's IPv4 controller is another node (so the drawing can tell the two apart), "" otherwise (the plain dot).
    const controllerKind = (n) => {
      const ips = new Set((n.addrs || []).flatMap((i) => [...(i.v4 || []), ...(i.v6 || [])].map((x) => String(x).split("/")[0])));
      const isIt = (m) => !n.node_paused && (m.via_local ? n.addr === viaAddr(m) : m.local ? n.self : ips.has(m.ip));
      let v6 = false;
      for (const g of groups()) {
        const ms = statusOf(g).members || [];
        const mine = ms.find((m) => m.agc && isIt(m));
        if (!mine) continue;
        const afs = (mine.agc_afs || "").split("+").filter(Boolean);
        const other4 = ms.find((m) => (m.agc_afs || "").split("+").includes("v4"));
        if (afs.length === 1 && afs[0] === "v6" && other4 && other4 !== mine) v6 = true; else return "";
      }
      return v6 ? "v6" : "";
    };
    const controllerWhy = (n) => (n.node_paused ? "Paused: resume it first" : isController(n, true) ? "Already the controller" : "");
    // Make a node the cluster's primary (the Cluster page's Promote button, aimed at the node whose shape was clicked): no gateway is
    // touched, so no question is asked.
    async function makePrimary(n) {
      try { await nodeCall(n, "POST", "/api/cluster/promote", {}); } catch (e) { /* the star-dot-triangle marks show the result */ }
      draw(); refresh();
    }
    const nodeMenu = (n) => (e) => {
      const pause = [n.node_paused ? "Resume" : "Pause", () => pauseNode(n), "", null, n.node_paused ? "" : lastNodeWhy(n)];
      const rm = memberItem(n).map(([, f]) => ["Remove", f]);
      openMenu(e, n.self || n.reachable
        ? [["Statistics", () => (n.self ? selectTab("host") : openNode(n, "host"))], ["Rename", () => renameNode(n)], "-", ["Controller", () => makeController(n), "", null, controllerWhy(n)], ["Primary", () => makePrimary(n), "", null, n.node_paused ? "Paused: resume it first" : n.role === "primary" ? "Already the primary" : ""], "-", ...(n.self ? [] : [["Open", () => openNode(n)]]), pause, ["Restart", () => powerNode(n, "restart")], "-", ["Shut down", () => powerNode(n, "shutdown")], ...rm]
        : [["Cluster page", () => selectTab("cluster")], ...rm]);
    };
    // right-click on a shape: select it, then show what can be done with it
    const rightClick = (kind, addr) => (e) => {
      const g = curGroup();
      cv.multi = null; cv.sel = { kind, addr }; refresh();
      openMenu(e, kind === "gw" ? gwItems(g) : anyItems(g, addr));
    };

    function nextGroupId() { const used = new Set(groups().map((g) => g.group_id)); for (let i = 154; i < 256; i++) if (!used.has(i)) return i; return 0; }

    function renameGateway(g) {
      form("Rename gateway", [{ k: "name", l: "Name (optional)", v: g.name || "", ph: "e.g. Core" }], (v) => {
        if ((v.name || "").length > 40) return "The name can be at most 40 characters.";
        if (v.name.trim()) g.name = v.name.trim(); else delete g.name;
        return null;
      }, "Rename");
    }
    function gatewayForm(g) {
      const base = g || groups()[0];
      form(g ? "Edit gateway" : "New gateway", [
        { k: "id", l: "Group number", v: String(g ? g.group_id : nextGroupId()), disabled: !!g },
        { k: "name", l: "Name", v: g ? (g.name || "") : "", ph: "optional, e.g. Core" },
        { k: "fqdn", l: "VIP FQDN", v: g ? (g.fqdn || "") : "", ph: "optional, vip.example.com" },
        { k: "afqdn", l: "Anycast FQDN", v: g ? (g.anycast_fqdn || "") : "", ph: "optional, any.example.com" },
        { k: "vip4", l: "IPv4 address / prefix", v: g ? g.vip4 : "", ph: "10.0.0.1/24" },
        { k: "vip6", l: "IPv6 address / prefix", v: g ? g.vip6 : "", ph: "optional, 2001:db8::1/64" },
        { k: "more4", l: "More IPv4 addresses", v: g ? (g.more_vip4 || []).join(", ") : "", ph: "optional, same subnet: 10.0.0.2, 10.0.0.3" },
        { k: "more6", l: "More IPv6 addresses", v: g ? (g.more_vip6 || []).join(", ") : "", ph: "optional, same subnet: 2001:db8::2" },
        { k: "iface", l: "Listening interface", v: g ? g.interface : (base ? base.interface : "eth0") },
      ], (v) => {
        if ((v.name || "").length > 40) return "The name can be at most 40 characters.";
        if ([v.fqdn, v.afqdn].some((n) => n && !/^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*\.?$/.test(n.trim()))) return "A name is a host name alone, e.g. vip.example.com.";
        if (!v.vip4 && !v.vip6) return "Give the gateway an IPv4 or IPv6 address, e.g. 10.0.0.1/24.";
        if (v.vip4 && !/^\d+\.\d+\.\d+\.\d+\/\d+$/.test(v.vip4)) return "The IPv4 address needs a prefix length, e.g. 10.0.0.1/24.";
        if (v.vip6 && !(v.vip6.includes(":") && /^[0-9a-fA-F:.]+\/\d+$/.test(v.vip6))) return "The IPv6 address needs a prefix length, e.g. 2001:db8::1/64.";
        if (!v.iface) return "Choose the network interface the gateway runs on.";
        const more4 = (v.more4 || "").split(/[\s,;]+/).filter(Boolean), more6 = (v.more6 || "").split(/[\s,;]+/).filter(Boolean);
        if (more4.some((a) => !/^\d+\.\d+\.\d+\.\d+$/.test(a))) return "More IPv4 addresses are plain addresses without a prefix, e.g. 10.0.0.2, 10.0.0.3.";
        if (more6.some((a) => !a.includes(":") || !/^[0-9a-fA-F:.]+$/.test(a))) return "More IPv6 addresses are plain addresses without a prefix, e.g. 2001:db8::2.";
        if ((more4.length && !v.vip4) || (more6.length && !v.vip6)) return "Further addresses need the gateway's own address of the same kind first.";
        const taken = groups().find((x) => x !== g && [x.vip4, x.vip6, ...(x.more_vip4 || []), ...(x.more_vip6 || [])].some((a) => a && [...more4, ...more6].includes(a.split("/")[0])));
        if (taken) return "Gateway " + gwLabel(taken) + " already uses one of those addresses.";
        const clash = groups().find((x) => x !== g && [x.vip4, x.vip6].some((a) => a && [v.vip4, v.vip6].some((b) => b && a.split("/")[0] === b.split("/")[0])));
        if (clash) return "Gateway " + gwLabel(clash) + " already uses that address.";
        if (g) {
          g.vip4 = v.vip4; g.vip6 = v.vip6; g.interface = v.iface;
          if (more4.length) g.more_vip4 = more4; else delete g.more_vip4;
          if (more6.length) g.more_vip6 = more6; else delete g.more_vip6;
          if (v.name.trim()) g.name = v.name.trim(); else delete g.name;
          if ((v.fqdn || "").trim()) g.fqdn = v.fqdn.trim().toLowerCase(); else delete g.fqdn;
          if ((v.afqdn || "").trim()) g.anycast_fqdn = v.afqdn.trim().toLowerCase(); else delete g.anycast_fqdn;
          return null;
        }
        const id = Number(v.id);
        if (!(id >= 1 && id <= 255) || groups().some((x) => x.group_id === id)) return "Pick an unused group number between 1 and 255.";
        const gkey = [...crypto.getRandomValues(new Uint8Array(16))].map((x) => x.toString(16).padStart(2, "0")).join("");   // a new gateway starts with a random key, as the daemon's own add does
        groups().push({ ...(v.name.trim() ? { name: v.name.trim() } : {}), ...((v.fqdn || "").trim() ? { fqdn: v.fqdn.trim().toLowerCase() } : {}), ...((v.afqdn || "").trim() ? { anycast_fqdn: v.afqdn.trim().toLowerCase() } : {}), group_id: id, interface: v.iface, vip4: v.vip4, vip6: v.vip6, ...(more4.length ? { more_vip4: more4 } : {}), ...(more6.length ? { more_vip6: more6 } : {}), real_macs: true, key: gkey });
        setGid(id); cv.sel = { kind: "gw" };
        // the first gateway also gets the one MCP key (Configure ▸ MCP shows it)
        const st = (cv.cfg.settings = cv.cfg.settings || {});
        if (!(st.mcp_keys || []).length) {
          const mk = randomKey();
          st.mcp_keys = [{ name: "default", access: "ro", key: mk, created: Math.floor(Date.now() / 1000) }];
          mcpBaseURL = vipBase(cv.cfg);
        }
        return null;
      }, g ? "Save" : "Add gateway");
    }

    function anycastForm(g, edit) {
      form(edit ? "Edit anycast address" : "Add anycast address", [
        { k: "addr", l: "Anycast address", v: edit || "", ph: "203.0.113.123 or 2001:db8:123::1",
          hint: "Any subnet. Held on every node." },
      ], (v) => {
        const a = (v.addr || "").trim().replace(/\/(32|128)$/, "").toLowerCase();
        if (!a) return "Enter an IPv4 or IPv6 address.";
        if (!/^[0-9a-f:.]+$/.test(a) || !(a.includes(":") || /^\d+\.\d+\.\d+\.\d+$/.test(a))) return "That has to be a plain IPv4 or IPv6 address, without a subnet.";
        if (/^(127\.|0\.|22\d\.|23\d\.)/.test(a) || /^(::1?|fe80:|ff)/.test(a)) return "Loopback, link-local and multicast addresses cannot be used.";
        // the same anycast address on several gateways is how an anycast service is run; only this gateway's own list and every gateway's shared address are refused
        const own = (g.extra_vips || []).includes(a) && !(a === edit) ? g : null;
        const shared = groups().find((x) => [x.vip4, x.vip6, ...(x.more_vip4 || []), ...(x.more_vip6 || [])].some((z) => z && z.split("/")[0].toLowerCase() === a));
        if (own || shared) return "Gateway " + gwLabel(own || shared) + " already uses that address.";
        g.extra_vips = g.extra_vips || [];
        if (edit) g.extra_vips[g.extra_vips.indexOf(edit)] = a; else g.extra_vips.push(a);
        cv.sel = { kind: "any", addr: a };
        return null;
      }, edit ? "Save" : "Add address");
    }

    function delSel() {
      const g = curGroup(), s = cv.sel;
      if (!g || !s) return;
      const finish = () => { cv.sel = null; cv.multi = null; closeEditor(); commit(); };
      if (s.kind === "gw") {
        confirmDialog("Delete gateway " + gwLabel(g) + "?",
          "This removes its shared address from the configuration. You can restore it later from History.", "Delete gateway", () => {
            cv.cfg.groups = groups().filter((x) => x !== g);
            setGid(cv.cfg.groups.length ? cv.cfg.groups[0].group_id : null);
            finish();
          });
      } else if (s.kind === "any") {
        g.extra_vips = (g.extra_vips || []).filter((a) => a !== s.addr);
        if (!g.extra_vips.length) delete g.extra_vips;
        finish();
      }
    }

    // ── saving ───────────────────────────────────────────────────────────
    // Every change is saved as soon as it is made.  If the daemon refuses it, the
    // picture goes back to what is saved and the reason is shown.
    function setGid(id) { cv.gid = id; state.topo.gid = id; }
    const fresh = (cfg, base) => {
      cv.base = base || cv.base;   // what the daemon says the saved configuration is: a save made from an older one is refused
      cv.orig = JSON.parse(JSON.stringify(cfg));
      if (pending === 0) { cv.cfg = cfg; cv.sel = cv.sel && curGroup() ? cv.sel : null; }
      if (!groups().some((g) => g.group_id === cv.gid)) setGid(groups().length ? groups()[0].group_id : null);
    };
    async function save() {
      const sent = JSON.stringify(groups().map((g) => g.group_id));
      try {
        const r = await api("PUT", "/api/config", { config: cv.cfg, note: "Topology", base: cv.base });
        pending--; fresh(r.config, r.base);
      } catch (ex) {
        pending--;
        if (ex.message === "unauthenticated") return;
        if (ex.status === 409) { // the saved configuration is newer than this page's copy (an import, another page, another node): take it, nothing was overwritten
          try { const r = await api("GET", "/api/config"); cv.cfg = r.config; cv.orig = JSON.parse(JSON.stringify(r.config)); cv.base = r.base; cv.sel = null; cv.multi = null; if (!curGroup()) setGid(groups().length ? groups()[0].group_id : null); } catch (_) { /* the next load reads it */ }
        } else if (ex.status) { // refused: show what is really saved
          if (pending === 0) { cv.cfg = JSON.parse(JSON.stringify(cv.orig)); cv.sel = null; if (!curGroup()) setGid(groups().length ? groups()[0].group_id : null); }
        } else {
          // The connection dropped (a gateway change can briefly reset the network).
          // Look at what the node actually has before reporting a failure.
          try {
            const r = await api("GET", "/api/config");
            if (JSON.stringify(r.config.groups.map((g) => g.group_id)) === sent) { fresh(r.config); }
          } catch (_) { /* the next refresh shows what is saved */ }
        }
      }
      refresh();
      refreshTopoNav();
      poll().catch(() => {});
    }
    let queue = Promise.resolve();
    function commit() { refresh(); pending++; queue = queue.then(save); }

    function refresh() { draw(); if (legendEl) legendEl.classList.toggle("hidden", !groups().length); }

    async function load() {
      const r = await api("GET", "/api/config");
      cv.cfg = r.config; cv.orig = JSON.parse(JSON.stringify(r.config)); cv.base = r.base;
      cv.loaded = true;
      cv.gid = state.topo.gid;
      if (!groups().some((g) => g.group_id === cv.gid)) setGid(groups().length ? groups()[0].group_id : null);
    }

    async function poll() {
      if (!cv.loaded) return;
      try {
        const r = await api("GET", "/api/canvas");
        cv.view = r.data || [];
      } catch (ex) { if (ex.message === "unauthenticated") throw ex; }
      try { cv.exp = ((await api("GET", "/api/logs/senders")).data || {}).senders || []; } catch (ex) { if (ex.message === "unauthenticated") throw ex; cv.exp = []; }   // no collector on this node: none drawn
      // Redrawing replaces every shape, and the browser's tooltip (the shape's <title>) only appears after the
      // pointer has rested on the same element for about a second: a redraw every poll while the pointer is on a
      // shape kept resetting it.  So leave the drawing alone while a shape is hovered and catch up when the
      // pointer leaves (the next poll, or the mouseout below, whichever comes first) — but never keep a stale
      // picture for more than 10 s, so colours stay true for a pointer left resting on the canvas.
      if (cv.drag) return;   // a shape is being dragged: leave the drawing alone
      if (svgHost && svgHost.querySelector(".shape:hover")) {
        if (!cv.stale) cv.stale = Date.now();
        if (Date.now() - cv.stale < 10000) return;
      }
      draw();
    }

    return {
      mount(main) {
        svgHost = h("div", { class: "cv-card",
          onmouseout: (e) => { if (cv.stale && !cv.drag && !(e.relatedTarget && e.relatedTarget.closest && e.relatedTarget.closest(".shape"))) draw(); },
          // right-click on empty canvas: no menu (the shapes and tabs have their own); it only clears the selection
          oncontextmenu: (e) => { e.preventDefault(); cv.sel = null; refresh(); } });
        const legend = legendEl = h("div", { class: "cv-legend muted small hidden" },
          h("span", { class: "dot st-ok" }), "working ", h("span", { class: "dot st-warn" }), "degraded ", h("span", { class: "dot st-bad" }), "down ", h("span", { class: "dot st-idle" }), "not yet known ", h("span", { class: "dot st-paused" }), "paused");
        state.onTopoFilter = () => { if (cv.loaded) refresh(); };
        main.append(svgHost, legend);
        cv.loaded = false;
        load().then(() => {
          cv.loaded = true; refresh(); poll().catch(() => {});
          if (state.topo.action === "new") { state.topo.action = null; gatewayForm(null); }
          else if (state.topo.action === "rename") { state.topo.action = null; const g = curGroup(); if (g) renameGateway(g); }
          else if (state.topo.action === "delete") { state.topo.action = null; if (curGroup()) { cv.sel = { kind: "gw" }; delSel(); } }
        })
          .catch((ex) => { });
      },
      poll,
    };
  })();

  // Gateways  (CLI: --show-gateways, --show-neighbors)
  VIEWS.gateways = (() => {
    let body;
    return {
      mount(main) {
        body = h("div", {});
        main.append(body, h("p", { class: "hint" }, "★ marks this node"));
      },
      async poll() {
        const r = await api("GET", "/api/gateways");
        const out = h("div", {});   // built aside, then patched into the page (see morph)
        if (!r.data.length) {
          out.append(h("div", { class: "card" }, h("div", { class: "empty" }, "No gateway groups running.")));
          morph(body, out);
          return;
        }
        for (const g of r.data) {
          const rows = g.members.map((m) => h("tr", { class: m.local ? "local" : "" },
            h("td", { class: "nname", title: m.name || null }, m.name || "–", m.local ? " ★" : ""),
            h("td", { class: "mono" }, m.ip),
            h("td", { class: "num" }, m.priority),
            h("td", { class: "num" }, m.slot || "–"),
            h("td", { class: "num" }, m.weight),
            h("td", {}, pill(m.role, m.role === "AGC" ? "ok" : m.role === "AFN" ? "info" : "")),
            h("td", {}, pill(m.state.toUpperCase(), m.state === "expired" ? "bad" : m.state === "active" || m.state === "forward" ? "ok" : "warn"),
              m.preempt ? " " : null, m.preempt ? pill("preempt") : null),
            h("td", { class: "num", title: "average gap between this node's hellos / the largest gap in the last 30 to 60 seconds, counting a silence in progress" }, m.local || !m.hello_avg_ms ? "–" : m.hello_avg_ms + " / " + m.hello_max_ms),
            h("td", { class: "mono" }, m.vmac || "–"),
            h("td", {}, pill(m.service_up ? "answering" : "not answering", m.service_up ? "ok" : "warn"))));
          out.append(h("div", { class: "card" },
            h("header", { class: "bar" }, h("h2", {}, g.name ? g.name + " · Group " + g.group_id : "Group " + g.group_id),
              h("span", { class: "muted gw-vip" }, "VIP ", h("span", { class: "mono" }, g.vip)),
              g.agc ? h("span", { class: "muted" }, "AGC ", h("span", { class: "mono" }, g.agc)) : null,
              h("span", { class: "grow" }),
              g.members.some((m) => m.local && m.real_macs) ? null
                : h("button", { class: "btn hdr", type: "button", title: "Check that replies addressed to this gateway's virtual MAC reach this node", onclick: () => vmacTestPopup(g.group_id, g.name || "Group " + g.group_id) }, "Test")),
            h("div", { class: "scroll" }, h("table", { class: "tight" },
              h("thead", {}, h("tr", {}, ["Node name", "Node IP", "Pri", "Slot", "Weight", "Role", "State", "Hello avg / max (ms)", "vMAC", "Service"].map((t, i) => h("th", { class: [2, 3, 4, 7].includes(i) ? "num" : "" }, t)))),
              h("tbody", {}, rows)))));
        }
        morph(body, out);
      },
    };
  })();

  // Nodes  (CLI: --cluster-status): the cluster's members (the gateway protocol's neighbours are on Gateways)
  VIEWS.nodes = (() => {
    let body;
    return {
      mount(main) { body = h("div", {}); main.append(body, h("p", { class: "hint" }, "★ marks this node")); },
      async poll() {
        const c = await api("GET", "/api/cluster").catch(() => null);
        const out = h("div", {});
        if (c && c.data) {
          const v = c.data;
          out.append(h("div", { class: "card" },
            h("header", {}, h("h2", {}, tc("Cluster members")),
              v.conflict ? pill("conflict", "bad") : null),
            membersTable(v, null, true)));
        }
        morph(body, out);
      },
    };
  })();

  // ── configuration  (CLI: --show-config, --configure) ──  // ── configuration  (CLI: --show-config, --configure) ──────────────────────
  const LB = ["roundrobin", "weighted", "hostpinned", "failover"];
  const GROUP_FIELDS = [
    { shared: true, k: "name", l: "Name", t: "text", hint: "Optional label." },
    { shared: true, k: "group_id", l: "Group ID", t: "int", min: 1, max: 255, def: 1 },
    { k: "interface", l: "Interface", t: "text" },
    { shared: true, k: "fqdn", l: "VIP FQDN", t: "text", hint: "Optional, e.g. vip.example.com." },
    { shared: true, k: "anycast_fqdn", l: "Anycast FQDN", t: "text", hint: "Optional, e.g. any.example.com." },
    { shared: true, k: "vip4", l: "IPv4 VIP/prefix", t: "text", hint: "e.g. 10.0.0.1/24 — empty to disable" },
    { shared: true, k: "vip6", l: "IPv6 VIP/prefix", t: "text", hint: "e.g. 2001:db8::1/64 — empty to disable" },
    { shared: true, k: "more_vip4", l: "More IPv4 addresses", t: "rows", rows: "morevip4", wide: true, hint: "Same subnet, one per row." },
    { shared: true, k: "more_vip6", l: "More IPv6 addresses", t: "rows", rows: "morevip6", wide: true, hint: "Same subnet, one per row." },
    { k: "priority", l: "Priority", t: "int", min: 0, max: 255, def: 100 },
    { shared: true, k: "lb_method", l: "LB method", t: "select", options: LB },
    { k: "weight", l: "Weight", t: "int", min: 0, max: 255, def: 100 },
    { shared: true, k: "hello_ms", l: "Hello interval (ms)", t: "int", min: 1, max: 65535, def: 150 },
    { shared: true, k: "hold_ms", l: "Hold time (ms)", t: "int", min: 1, max: 65535, def: 750 },
    { shared: true, k: "max_afns", l: "Max forwarders", t: "int", min: 1, max: 255, def: 128 },
    { shared: true, k: "key", l: "HMAC shared key", t: "text", weakKey: true },
    { k: "preempt", l: "Preemption", t: "bool" },
    { shared: true, k: "real_macs", l: "Use real MAC addresses (no virtual MACs)", t: "bool" },
    { shared: true, k: "neighbors", l: "Neighbors (unicast mode)", t: "rows", rows: "neighbors", wide: true, hint: "Empty: multicast." },
  ];
  const SSO_FIELDS = [
    { shared: true, k: "sso_base_url", brk: true, l: "Address of this console", t: "text" },
    { shared: true, k: "sso_google_client_id", brk: true, l: "Google client ID", t: "text" },
    { shared: true, k: "sso_google_client_secret", l: "Google client secret", t: "secret" },
    { shared: true, k: "sso_entra_tenant", brk: true, l: "Microsoft tenant ID", t: "text" },
    { shared: true, k: "sso_entra_client_id", l: "Microsoft client ID", t: "text" },
    { shared: true, k: "sso_entra_client_secret", l: "Microsoft client secret", t: "secret" },
    { shared: true, k: "sso_oidc_name", brk: true, l: "OpenID Connect button name", t: "text" },
    { shared: true, k: "sso_oidc_issuer", l: "OpenID Connect issuer", t: "text" },
    { shared: true, k: "sso_oidc_client_id", l: "OpenID Connect client ID", t: "text" },
    { shared: true, k: "sso_oidc_client_secret", l: "OpenID Connect client secret", t: "secret" },
    { shared: true, k: "sso_oidc_skip_verify", l: "Do not check the provider's certificate", t: "bool" },
    { shared: true, k: "sso_users", l: "Users", t: "rows", rows: "ssousers", wide: true },
  ];

  // Configure ▸ Users ▸ SSO: a tab for the settings every provider shares (the console's address, who may sign in) and one for each provider
  const SSO_TABS = [
    ["General", ["sso_base_url", "sso_users"]],
    ["Google", ["sso_google_client_id", "sso_google_client_secret"]],
    ["Microsoft", ["sso_entra_tenant", "sso_entra_client_id", "sso_entra_client_secret"]],
    ["OpenID Connect", ["sso_oidc_name", "sso_oidc_issuer", "sso_oidc_client_id", "sso_oidc_client_secret", "sso_oidc_skip_verify"]],
  ];
  function ssoTabs(obj) {
    const sets = SSO_TABS.map(([, keys]) => fieldset(SSO_FIELDS.filter((f) => keys.includes(f.k)).map((f) => ({ ...f, brk: false })), obj));
    const panes = sets.map((fs) => h("div", { role: "tabpanel" }, fs.el));
    const buttons = SSO_TABS.map(([name], i) => h("button", { type: "button", role: "tab", class: "dtab sub", onclick: () => show(i) }, name));
    function show(i, focus) {
      state.ssoTab = i;
      refreshHelp();
      panes.forEach((pn, j) => { pn.hidden = j !== i; });
      buttons.forEach((b, j) => { b.setAttribute("aria-selected", j === i ? "true" : "false"); b.tabIndex = j === i ? 0 : -1; b.classList.toggle("on", j === i); });
      if (focus) buttons[i].focus();
    }
    const strip = h("div", { class: "dtabs sub", role: "tablist", "aria-label": "Single sign-on" }, buttons);
    strip.addEventListener("keydown", (e) => {
      const d = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
      if (!d) return;
      e.preventDefault();
      show((state.ssoTab + d + SSO_TABS.length) % SSO_TABS.length, true);
    });
    show(state.ssoTab >= 0 && state.ssoTab < SSO_TABS.length ? state.ssoTab : 0);
    return { el: h("div", {}, strip, ...panes), get: () => Object.assign({}, ...sets.map((fs) => fs.get())) };
  }

  // Configure ▸ Users ▸ LDAP: sign-in with a directory (Active Directory, Samba 4), and a Test button that says what happens
  const LDAP_FIELDS = [
    { shared: true, k: "ldap_servers", l: "LDAP servers", t: "rows", rows: "ldapservers", wide: true },
    { shared: true, k: "ldap_skip_verify", l: "Do not check the server's certificate", t: "bool" },
    { shared: true, k: "ldap_bind_dn", l: "Bind account", t: "text" },
    { shared: true, k: "ldap_bind_password", l: "Bind password", t: "secret" },
    { shared: true, k: "ldap_base_dn", l: "Search base", t: "text" },
    { shared: true, k: "ldap_rw_group", l: "Full-access group", t: "text" },
    { shared: true, k: "ldap_ro_group", l: "Read-only group", t: "text" },
  ];
  function ldapTab(obj) {
    const fs = fieldset(LDAP_FIELDS, obj);
    // two lines: bind account, bind password and search base; then the full-access and the read-only group
    fs.el.classList.add("ldap6");
    [...fs.el.querySelectorAll(":scope > label.f:not(.check)")].forEach((el, i) => el.classList.add(i < 3 ? "span2" : "span3"));
    const user = h("input", { type: "text", autocomplete: "off", spellcheck: "false", "aria-label": "User name to test", placeholder: "alice" });
    const pass = h("input", { type: "password", autocomplete: "new-password", "aria-label": "Password to test", placeholder: "optional" });
    const out = h("pre", { class: "ldap-out hidden", role: "status", "aria-live": "polite" });
    const btn = h("button", { type: "button", class: "btn" }, "Test");
    const run = async () => {
      btn.disabled = true;
      out.classList.remove("hidden");
      out.textContent = "Trying…";
      try {
        const d = (await api("POST", "/api/ldap/test", { username: user.value.trim(), password: pass.value }, true)).data;
        out.textContent = (d.ok ? "OK" + (d.read_only ? " (read-only access)" : "") : "Not accepted") + "\n" + d.steps.map((x) => "  " + x).join("\n");
      } catch (e) { out.textContent = "Could not test: " + (e.message || e); }
      btn.disabled = false;
    };
    btn.addEventListener("click", run);
    for (const i of [user, pass]) i.addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); run(); } });
    const test = h("div", { class: "ldap-test" },
      h("h3", {}, "Test"),
      h("div", { class: "ldap-test-row" },
        h("label", { class: "f" }, "User name", user), h("label", { class: "f" }, "Password", pass), btn),
      out);
    test.addEventListener("change", (e) => e.stopPropagation());   // the test boxes are not settings
    return { el: h("div", {}, fs.el, test), get: fs.get };
  }

  const WEB_FIELDS = [
    { k: "listen", l: "Listen address", t: "port", def: "11154", hint: "Port, or address:port." },
    { shared: true, k: "group", l: "Login groups", t: "text", hint: "Separated by commas." },
    { shared: true, k: "read_group", l: "Read-only groups", t: "text", hint: "Same form. Empty: nobody." },
    { k: "pam_service", l: "PAM service", t: "text" },
    { k: "session_idle_minutes", l: "Session idle timeout (min)", t: "int", min: 1, def: 30 },
    { shared: true, k: "max_failed_logins", l: "Failed logins before lockout", t: "int", min: 1, max: 1000, def: 3 },
    { shared: true, k: "failed_login_window_minutes", l: "…within this many minutes", t: "int", min: 1, max: 10080, def: 1 },
    { shared: true, k: "lockout_minutes", l: "Lockout lasts (minutes)", t: "int", min: 1, max: 10080, def: 15 },
    { shared: true, k: "min_password_length", l: "Minimum password length", t: "int", min: 0, max: 128, def: 8, zeroDef: true },
  ];
  const SNMP_FIELDS = [
    { k: "location", l: "Location", t: "text" },
    { shared: true, k: "snmp_contact", l: "Contact", t: "text" },
    { shared: true, k: "snmp_communities", l: "Community strings", t: "rows", rows: "community", wide: true },
    { shared: true, k: "snmp_acl", l: "Allowed sources", t: "rows", rows: "snmpacl", wide: true },
  ];
  const PROM_FIELDS = [
    { shared: true, k: "metrics_acl", l: "Allowed sources", t: "rows", rows: "metricsacl", wide: true },
  ];
  // Configure ▸ Settings ▸ General ▸ Neighbor discovery (lldp.go)
  // Configure ▸ Settings ▸ General ▸ Topology (settings.topology_squares_above): how many senders a gateway may have before the drawing turns each into a small square
  const TOPO_FIELDS = [
    { shared: true, k: "topology_squares_above", l: "Small square limit", unit: "senders", t: "int", min: 1, max: 5000, def: 36, hint: "Above this many, senders become squares." },
  ];
  // Configure ▸ Settings ▸ General ▸ Log collector (settings.logs): where the messages are kept, for how long, and the ports the collector listens on
  const COLLECT_FIELDS = [
    { k: "log_dir", l: "Storage folder", t: "text", hint: "This node only. Applied at restart." },
    { shared: true, k: "retention_days", l: "Keep", unit: "days", t: "int", min: 0, max: 36500, def: 0, hint: "0: until the disk cap removes the oldest." },
    { shared: true, k: "dir_max_percent", l: "Disk cap", unit: "%", t: "int", min: 1, max: 100, def: 90 },
    { shared: true, k: "mem_max_percent", l: "Memory cap", unit: "%", t: "int", min: 1, max: 100, def: 70, hint: "Closed hours kept in memory for searches." },
    { shared: true, k: "list_limit", l: "Messages listed", t: "int", min: 100, max: 5000, def: 1000, hint: "On the Logs page: the newest this many." },
    { shared: true, k: "follow", l: "Follow new messages", t: "bool", hint: "Logs page: new ones show every 2 seconds." },
    { shared: true, k: "udp_port", l: "UDP port", t: "int", min: -1, max: 65535, def: 514, hint: "-1 turns it off." },
    { shared: true, k: "tcp_port", l: "TCP port", t: "int", min: -1, max: 65535, def: 514, hint: "-1 turns it off." },
    { shared: true, k: "tls_port", l: "TLS port", t: "int", min: -1, max: 65535, def: 6514, hint: "-1 turns it off. Uses the GUI certificate." },
  ];
  const LLDP_FIELDS = [
    { shared: true, k: "lldp", l: "Run LLDP on every node", t: "bool" },
  ];
  // Configure ▸ Senders (settings.logs.senders): what may send syslog; the collector listens as soon as there is one
  const SENDER_FIELDS = [
    { shared: true, k: "senders", l: "Senders", t: "rows", rows: "senders", wide: true, hint: "Name and address; anything else is refused" },
  ];
  // Configure ▸ Senders ▸ Pattern alerts (settings.logs.alerts)
  const LOGALERT_FIELDS = [
    { shared: true, k: "logalerts", l: "Pattern alerts", t: "rows", rows: "logalerts", wide: true, hint: "Opens when enough messages match" },
  ];
  const MCP_FIELDS = [
    { shared: true, k: "mcp_port", l: "Port", t: "int", min: 1, max: 65535, def: 443 },
    { shared: true, k: "mcp_max_result_mb", l: "Largest answer (MB)", t: "int", min: 1, max: 8, def: 1 },
    { shared: true, k: "mcp_log", l: "Log all MCP requests and replies", t: "bool" },
    { shared: true, k: "mcp_log_mb", unit: "MB", l: "Log size limit", t: "int", min: 1, max: 1000, def: 50 },
    { shared: true, k: "mcp_log_entry_kb", unit: "KB", l: "Entry size limit", t: "int", min: 1, max: 8192, def: 256 },
    { shared: true, k: "mcp_acl", l: "Allowed sources", t: "rows", rows: "mcpacl", wide: true },
    { shared: true, k: "mcp_keys", l: "Keys", t: "mcpkeys", wide: true },
  ];
  const CLUSTER_FIELDS = [
    { k: "listen", l: "Binding address", t: "port", def: "11155", hint: "Port, or address:port." },
    { k: "self", l: "Advertised address", t: "text" },
    { k: "sync_interval_sec", l: "Sync interval (s)", t: "int", min: 1, max: 3600, def: 5 },
  ];
  // Configure ▸ Settings ▸ Daemon Log: the daemon's own log level, and where this node sends it (per node, never replicated)
  const SYSLOG_FIELDS = [
    { k: "enabled", l: "Send the log to a syslog server", t: "bool", hint: "" },
    { k: "protocol", l: "Protocol", t: "select", options: ["tcp", "udp", "tls"] },
    { k: "server", l: "Server", t: "text", hint: "Host or host:port." },
    { k: "facility", l: "Facility", t: "select", options: ["daemon", "user", "auth", "authpriv", "local0", "local1", "local2", "local3", "local4", "local5", "local6", "local7"] },
    { k: "format", l: "Message format", t: "select", options: ["rfc5424", "rfc3164"] },
    { k: "ca_file", l: "CA certificate file (tls)", t: "text", hint: "PEM file. Empty: system CAs." },
    { k: "tls_insecure", l: "Do not check the server's certificate (tls)", t: "bool" },
  ];
  // Configure ▸ Settings ▸ DNS: this node puts its own addresses into DNS (per node, never replicated)
  const DNSREG_FIELDS = [
    { k: "enabled", l: "Register this node's addresses in DNS", t: "bool" },
    { k: "key", l: "TSIG key", t: "secret", hint: "NAME:SECRET[:ALG], or a key file path." },
    { k: "domain", l: "Domain", t: "text", hint: "Empty: the resolver's search domain." },
    { k: "server", l: "Server", t: "text", hint: "Empty: the zone's primary (from the SOA)." },
    { k: "interval_sec", l: "Check every (s)", t: "int", min: 30, max: 86400, def: 300 },
    { k: "ttl", l: "TTL (s)", t: "int", min: 1, max: 604800, def: 900 },
    { k: "skip_interfaces", l: "Leave out interfaces", t: "text", hint: "Names or patterns, e.g. docker*, veth*" },
  ];
  const LOG_FIELDS = [{ k: "log_level", l: "Log level", t: "select", options: ["debug", "info", "warning", "error"] }];

  // Shown only when this node is part of a multi-node cluster.
  const sharedTag = (f) => (f.shared && state.clustered
    ? h("span", { class: "pill info tiny", title: "Replicated from the cluster's primary. Changes made here are accepted by the primary first." }, "shared") : null);

  // A small table for a list of lines made of a few columns (servers, clients, keys): rows to edit in place, drag to
  // reorder, a filter box, paging, a and a right-click menu (Edit, Copy, Paste, Add row, Move, Delete).
  // spec: {cols: [{name, ph, opts?}], parse(line) -> [cell, ...], format(cells) -> [line, ...], mark(cells) -> true when the row is
  // not complete (outlined and not saved), empty: text for no rows}.
  const rowClips = {};
  // the short lists (a handful of rows) have no filter box and no paging; the long ones (clients, domains, users) do
  const SMALL_TABLES = new Set(["ldapservers", "neighbors", "morevip4", "morevip6", "community", "snmpacl", "metricsacl", "mcpacl"]);
  // A table cell whose text is cut by its box (an ellipsis) shows the whole text as a tooltip when the pointer reaches it; a cell with a tooltip of its own keeps it.
  function cutTip(e) {
    const td = e.target.closest && e.target.closest("td");
    if (td && !td.title && td.scrollWidth > td.clientWidth) td.title = td.textContent.trim();
  }

  // A field of a table whose text does not fit its box shows the whole text as a tooltip.  It is checked when the pointer arrives, the box gets
  // the focus or its text changes, so a box that is wide enough has none.  A secret is never put in a tooltip.
  const tipCtx = document.createElement("canvas").getContext("2d");
  function tipWhenCut(el, text) {
    const set = () => {
      const t = text();
      let cut = false;
      if (t && el.tagName === "SELECT") {   // a select's text is not scrollable: measure it against the room inside the box (less the arrow)
        const cs = getComputedStyle(el);
        tipCtx.font = `${cs.fontStyle} ${cs.fontWeight} ${cs.fontSize} ${cs.fontFamily}`;
        cut = tipCtx.measureText(t).width > el.clientWidth - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight) - 24;
      } else if (t) cut = el.scrollWidth > el.clientWidth;
      if (cut) el.title = t; else el.removeAttribute("title");
    };
    for (const ev of ["mouseenter", "focus", "input", "change"]) el.addEventListener(ev, set);
  }

  function rowTable(spec, lines) {
    const N = spec.cols.length;
    const small = SMALL_TABLES.has(spec.key);
    const data = (lines || []).map((l) => { const v = spec.parse(l); return { v, off: !!(spec.isOff && spec.isOff(l)), ...(spec.track ? { orig: String(v[0] || "").trim() } : {}) }; });
    let page = 0, size = small ? 1e9 : 50, dragging = null;
    const tbody = h("tbody", {});
    const tbl = h("table", { class: "policy zones rows t-" + spec.key + (N === 1 ? " one" : "") },
      h("thead", {}, h("tr", {}, h("th", { "aria-label": "Row number and order" }, "#"), ...spec.cols.map((c) => h("th", {}, c.name)))), tbody);
    const empty = h("div", { class: "policy-empty" }, "");
    const wrap = h("div", { class: "policy-wrap" }, tbl, empty);
    const filter = h("input", { type: "text", class: "policy-filter", placeholder: "Filter rows", "aria-label": "Filter rows", autocomplete: "off", spellcheck: "false" });
    const count = h("span", { class: "policy-count muted" }, "");
    const sizeSel = h("select", { "aria-label": "Rows per page" }, [25, 50, 100, 250, 500].map((n) => h("option", { value: String(n) }, n + " per page")));
    sizeSel.value = String(size);
    const mkBtn = (label, title) => h("button", { type: "button", class: "btn small", title, "aria-label": title }, label);
    const first = mkBtn("«", "First page"), prev = mkBtn("‹", "Previous page"), next = mkBtn("›", "Next page"), last = mkBtn("»", "Last page");
    const pageIn = h("input", { type: "number", min: "1", class: "policy-page", "aria-label": "Page number" });
    const pages = h("span", { class: "muted" }, "");
    const pager = h("span", { class: "policy-pager" }, first, prev, pageIn, pages, next, last);
    const bar = h("div", { class: "policy-bar" }, filter, count, h("span", { class: "grow" }), pager, sizeSel);
    const box = h("div", { class: "policy-box rows" + (spec.wide ? " wide" : "") }, ...(small ? [] : [bar]), wrap);
    bar.addEventListener("change", (e) => e.stopPropagation());   // the filter and paging controls are not settings
    const changed = () => tbl.dispatchEvent(new Event("change", { bubbles: true }));
    const plain = spec.cols.map((c) => !c.secret);   // the filter never looks into a password or a token
    const visible = () => { const flt = parseFilter(filter.value); return flt.empty ? data : data.filter((r) => flt.match((w) => r.v.some((x, i) => plain[i] && x.toLowerCase().includes(w)))); };
    const clearMarks = () => tbody.querySelectorAll(".drop-above, .drop-below").forEach((x) => x.classList.remove("drop-above", "drop-below"));
    const trOf = (row) => [...tbody.children].find((t) => t._row === row);
    const cells = (row) => row.v.map((x) => x.trim());
    const names = () => { const n = {}; for (const r of data) { const x = r.v[0].trim(); if (x) n[x] = (n[x] || 0) + 1; } return n; };   // how many rows carry each first-cell name
    const mark = (tr, row) => { tr.classList.toggle("incomplete", !!spec.mark(cells(row))); };

    function render(follow) {
      const vis = visible();
      const n = Math.max(1, Math.ceil(vis.length / size));
      if (follow) { const i = vis.indexOf(follow); if (i >= 0) page = Math.floor(i / size); }
      page = Math.min(Math.max(page, 0), n - 1);
      const from = page * size;
      tbody.replaceChildren(...vis.slice(from, from + size).map(mk));
      const filtered = vis.length !== data.length;
      count.textContent = vis.length
        ? `${from + 1}–${Math.min(from + size, vis.length)} of ${vis.length}${filtered ? ` (filtered from ${data.length})` : ""}`
        : (filtered ? `0 of ${data.length}` : "0 rows");
      pager.hidden = n < 2;
      pageIn.value = String(page + 1);
      pageIn.max = String(n);
      pages.textContent = "of " + n;
      first.disabled = prev.disabled = page === 0;
      next.disabled = last.disabled = page >= n - 1;
      empty.hidden = vis.length > 0;
      empty.textContent = data.length ? "No rows match the filter." : (spec.empty || "No rows.") + " Right-click here ▸ Add row.";
    }
    function moveTo(row, target, after) {
      if (row === target) return;
      data.splice(data.indexOf(row), 1);
      const j = data.indexOf(target);
      data.splice(after ? j + 1 : j, 0, row);
      render(row);
      changed();
    }
    function mk(row) {
      const tr = h("tr", { class: row.off ? "off" : "" });
      tr._row = row;
      const grip = h("span", { class: "grip-h", draggable: "true", title: "Drag to reorder", "aria-hidden": "true" }, "⋮⋮");
      tr.append(h("td", { class: "grip" }, h("span", { class: "rownum" }, String(data.indexOf(row) + 1)), grip));
      spec.cols.forEach((c, i) => {
        let inp;
        if (c.opts) {
          const opts = c.opts.includes(row.v[i]) || !row.v[i] ? c.opts : [...c.opts, row.v[i]];   // a value this list does not know is kept
          inp = h("select", { "aria-label": c.name }, opts.map((o) => h("option", { value: o }, o)));
          if (!row.v[i]) row.v[i] = c.opts[0];
          inp.value = row.v[i];
          inp.addEventListener("change", () => { row.v[i] = inp.value; mark(tr, row); });
        } else {
          inp = h("input", { type: c.secret ? "password" : "text", autocomplete: c.secret ? "new-password" : "off", spellcheck: "false", "aria-label": c.name, placeholder: c.ph || "" });
          if (!row.v[i] && c.def) row.v[i] = c.def;   // the default shown as the value, not left empty
          inp.value = row.v[i] || "";
          inp.addEventListener("input", () => { row.v[i] = inp.value; mark(tr, row); });
          if (c.list) {   // a column that offers a list of names while typing (the datalist is filled when the box gets the focus: the names may be unsaved)
            inp.setAttribute("list", c.list);
            inp.addEventListener("focus", () => { if (spec.onList) spec.onList(c.list); });
          }
        }
        if (!c.secret) tipWhenCut(inp, () => (inp.tagName === "SELECT" ? (inp.selectedOptions[0] ? inp.selectedOptions[0].textContent : "") : inp.value));
        tr.append(h("td", {}, inp));
      });
      grip.addEventListener("dragstart", (e) => {
        dragging = row;
        e.dataTransfer.effectAllowed = "move";
        try { e.dataTransfer.setData("text/plain", "row"); e.dataTransfer.setDragImage(tr, 0, 0); } catch (x) { /* a browser without drag images */ }
        tr.classList.add("dragging");
      });
      grip.addEventListener("dragend", () => { dragging = null; tr.classList.remove("dragging"); clearMarks(); });
      tr.addEventListener("dragover", (e) => {
        if (!dragging || dragging === row) return;
        e.preventDefault();
        e.dataTransfer.dropEffect = "move";
        const r = tr.getBoundingClientRect();
        clearMarks();
        tr.classList.add(e.clientY > r.top + r.height / 2 ? "drop-below" : "drop-above");
      });
      tr.addEventListener("drop", (e) => {
        if (!dragging || dragging === row) return;
        e.preventDefault();
        const below = tr.classList.contains("drop-below");
        clearMarks();
        const d = dragging;
        dragging = null;
        moveTo(d, row, below);
      });
      tr.addEventListener("contextmenu", (e) => rowMenu(menuFor(row))(e));
      tr.addEventListener("keydown", (e) => { if (e.key === "Escape" && row.fresh && isBlank(row)) { e.stopPropagation(); if (document.activeElement) document.activeElement.blur(); dropBlank(row); } });
      tr.addEventListener("focusout", () => setTimeout(() => { if (!tr.contains(document.activeElement)) dropBlank(row); }, 0));
      mark(tr, row);
      return tr;
    }
    // a row added with Add and left empty (Esc, or a click elsewhere) goes away again; a select counts as empty while it shows its first choice
    const isBlank = (row) => row.v.every((x, i) => (spec.cols[i].opts ? x === spec.cols[i].opts[0] : !x.trim() || x === spec.cols[i].def));
    function dropBlank(row) {
      if (!row.fresh || !data.includes(row) || !isBlank(row)) return false;
      const ae = document.activeElement, atr = ae && ae.closest ? ae.closest("tr") : null;
      const keep = atr && atr._row && atr._row !== row && tbody.contains(atr) ? { row: atr._row, col: [...atr.querySelectorAll("input, select")].indexOf(ae), a: ae.selectionStart, b: ae.selectionEnd } : null;
      data.splice(data.indexOf(row), 1);
      render();
      if (keep) { focusRow(keep.row, keep.col); const el = (trOf(keep.row) || {}).querySelectorAll ? trOf(keep.row).querySelectorAll("input, select")[keep.col] : null; try { if (el && keep.a != null) el.setSelectionRange(keep.a, keep.b); } catch (x) { /* a select */ } }
      return true;
    }
    function insert(vals, after) {
      for (const r of data.filter((x) => x.fresh && isBlank(x))) data.splice(data.indexOf(r), 1);
      if (after && !data.includes(after)) after = null;
      const row = { v: Array.from({ length: N }, (_, i) => vals[i] || ""), fresh: !vals.some((x) => x) };
      data.splice(after ? data.indexOf(after) + 1 : data.length, 0, row);
      filter.value = "";
      render(row);
      return row;
    }
    const focusRow = (row, col) => { const t = trOf(row); const el = t && t.querySelectorAll("input, select")[col || 0]; if (el) el.focus(); };
    // pasted rows: the cells separated by tabs, one row to a line (the lines of a spreadsheet), or one line of the list's own form
    const parseText = (txt) => txt.split(/\r?\n/).filter((l) => l.trim()).map((l) => (l.includes("\t") ? l.split("\t").map((x) => x.trim()) : spec.parse(l.trim())));
    async function paste(after) {
      let list = [];
      try { list = parseText(await navigator.clipboard.readText()); } catch (e) { /* no permission: the last copied row is used */ }
      if (!list.length && rowClips[spec.key]) list = [rowClips[spec.key]];
      if (!list.length) return;
      let at = after;
      for (const v of list) at = insert(v, at);
      changed();
    }
    function menuFor(row) {
      const vis = visible(), i = vis.indexOf(row), di = data.indexOf(row);
      return [
        ["Edit", () => focusRow(row, 0)],
        ["Copy", () => { rowClips[spec.key] = cells(row).map((x, i) => (spec.cols[i].secret ? "" : x)); try { navigator.clipboard.writeText(rowClips[spec.key].join("\t")); } catch (e) { /* the row stays in the page's own clipboard */ } }],
        ["Paste", () => paste(row)],
        ["Add", () => focusRow(insert([], row), 0)],
        ...(spec.isOff ? [[row.off ? "Enable" : "Disable", () => { row.off = !row.off; render(); changed(); }]] : []),
        ...(i > 0 ? [["Move up", () => moveTo(row, vis[i - 1], false)]] : []),
        ...(i >= 0 && i < vis.length - 1 ? [["Move down", () => moveTo(row, vis[i + 1], true)]] : []),
        ...(di > 0 ? [["Move to top", () => moveTo(row, data[0], false)]] : []),
        ...(di < data.length - 1 ? [["Move to bottom", () => moveTo(row, data[data.length - 1], true)]] : []),
        ["Delete", () => { data.splice(data.indexOf(row), 1); render(); changed(); }, "danger"],
      ];
    }
    wrap.addEventListener("contextmenu", rowMenu([
      ["Add", () => focusRow(insert([], null), 0)],
      ["Paste", () => paste(data[data.length - 1])],
    ]));
    filter.addEventListener("input", () => { page = 0; render(); });
    sizeSel.addEventListener("change", () => { size = +sizeSel.value || 50; page = 0; render(); });
    first.addEventListener("click", () => { page = 0; render(); });
    prev.addEventListener("click", () => { page--; render(); });
    next.addEventListener("click", () => { page++; render(); });
    last.addEventListener("click", () => { page = 1e9; render(); });
    pageIn.addEventListener("change", () => { page = (parseInt(pageIn.value, 10) || 1) - 1; render(); });
    pageIn.addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); pageIn.blur(); } });
    filter.addEventListener("keydown", (e) => { if (e.key === "Enter") e.preventDefault(); });
    render();
    return {
      el: box,
      // a tracked table (spec.track): the rows that had one name and now have another (original -> now); a new, deleted, unnamed or doubly named row has none
      renames: () => { const n = names(); return new Map(data.filter((r) => r.orig && r.v[0].trim() && r.v[0].trim() !== r.orig && n[r.v[0].trim()] === 1).map((r) => [r.orig, r.v[0].trim()])); },
      // what each row is called now is what it was called: renames start again from here
      commit: () => { const n = names(); for (const r of data) if (r.v[0].trim() && n[r.v[0].trim()] === 1) r.orig = r.v[0].trim(); },
      // a column's values that are keys of the map take the map's value (the Login column follows a renamed login)
      retarget: (col, map) => { let k = 0; for (const r of data) { const x = (r.v[col] || "").trim(); if (map.has(x)) { r.v[col] = map.get(x); k++; } } if (k) render(); return k; },
      setFilter: (t) => { filter.value = t; page = 0; render(); },   // the filter box, as if typed
      // the words of a column of words (the Tags column): every word in use, and a way to rename or remove one in every row (fn gives the new word, or "" to drop it)
      words: (col) => data.flatMap((r) => (r.v[col] || "").split(/[\s,]+/).filter(Boolean)),
      mapWords: (col, fn) => { let k = 0; for (const r of data) { const w = (r.v[col] || "").split(/[\s,]+/).filter(Boolean), n = [...new Set(w.map(fn).filter(Boolean))]; if (n.join(" ") !== w.join(" ")) { r.v[col] = n.join(" "); k++; } } if (k) { render(); changed(); } return k; },
      get: () => (spec.obj ? data.filter((r) => !(spec.drop || spec.mark)(cells(r))).map((r) => spec.obj(cells(r)))
        : data.flatMap((r) => ((spec.drop || spec.mark)(cells(r)) ? [] : spec.format(cells(r)).map((x) => (r.off ? x + " disabled" : x))))),
    };
  }

  // one value to a row, one row to a list entry (a row with several, separated by commas or spaces, is saved as several entries)
  const oneCol = (key, name, ph, empty) => ({
    key, empty, cols: [{ name, ph }],
    parse: (line) => [String(line).trim()],
    format: (c) => c[0].split(/[\s,]+/).filter(Boolean),
    mark: (c) => !c[0],
  });
  // the lists that are tables
  const ROW_TABLES = {
    snmpacl: oneCol("snmpacl", "Address or network", "10.1.0.0/24, 192.0.2.5, 2001:db8::/32", "Nobody may ask."),
    metricsacl: oneCol("metricsacl", "Address or network", "10.1.0.0/24, 192.0.2.5, 2001:db8::/32", "Nobody may read /metrics."),
    // MCP access: "10.1.0.0/24" (read-only), "10.1.1.50 rw", "10.1.0.0/24 strict" (an address or network, ro or rw, a privacy level)
    mcpacl: {
      key: "mcpacl", wide: true, empty: "No sources allowed without a key.",
      cols: [{ name: "Address or network", ph: "10.1.1.50, 10.1.0.0/24" }, { name: "Access", opts: ["ro", "rw"] }, { name: "Privacy", opts: ["default", "none", "standard", "strict"] }, { name: "Expires (UTC)", ph: "never, 2026-12-31 or 2026-12-31 17:00" }, { name: "Notes", ph: "what this source is for", note: true }],
      parse: (line) => {
        const [body, ...nt] = String(line).split("#");   // "10.1.1.50 rw # laptop": what it says, and its note
        const w = body.trim().split(/\s+/), f = w.map((x) => x.toLowerCase());
        const lv = f.slice(1).find((x) => ["none", "standard", "strict"].includes(x));
        const un = w.slice(1).find((x) => x.toLowerCase().startsWith("until="));
        return [w[0] || "", f.slice(1).includes("rw") ? "rw" : "ro", lv || "default", un ? un.slice(6).replace("T", " ").replace(/Z$/, "") : "", nt.join("#").trim()];
      },
      format: (c) => c[0].split(/[\s,]+/).filter(Boolean).map((a) => a + (c[1] === "rw" ? " rw" : "") + (c[2] && c[2] !== "default" ? " " + c[2] : "") + (c[3] && c[3].trim() ? " until=" + c[3].trim().replace(/\s+/, "T") : "") + ((c[4] || "").replace(/[#\r\n]/g, " ").trim() ? " # " + c[4].replace(/[#\r\n]/g, " ").trim() : "")),
      mark: (c) => !c[0],
      isOff: (l) => /\sdisabled\s*$/i.test(String(l).split("#")[0]),   // a row switched off with right-click ▸ Disable stays in the list but allows nothing
    },
    neighbors: oneCol("neighbors", "Neighbor IP address", "10.0.0.2", "No neighbors: multicast."),
    morevip4: oneCol("morevip4", "IPv4 address", "10.0.0.11", "No more IPv4 addresses."),
    morevip6: oneCol("morevip6", "IPv6 address", "2001:db8::11", "No more IPv6 addresses."),
    // a community string may contain commas: one string to a row, never split
    community: { ...oneCol("community", "Community string", "", "No community strings: SNMP is off."), format: (c) => [c[0]] },
    // Configure ▸ Senders: one sender to a row (settings.logs.senders): what is allowed to send syslog
    senders: {
      key: "senders", empty: "No senders: no message is accepted.", wide: true,
      cols: [{ name: "Name", ph: "core-sw1" }, { name: "Address", ph: "10.0.0.2, 10.9.0.0/16 or sw1.example.com" }, { name: "Accept", opts: ["yes", "no"] }],
      parse: (d) => [d.name || "", d.address || "", d.disabled ? "no" : "yes"],
      format: (c) => [{ name: c[0].trim(), address: c[1].trim(), ...(c[2] === "no" ? { disabled: true } : {}) }],
      mark: (c) => !c[0].trim() || !c[1].trim(),
      drop: (c) => !c[0].trim() && !c[1].trim(),
    },
    // Configure ▸ Senders: one pattern alert to a row (settings.logs.alerts)
    logalerts: {
      key: "logalerts", empty: "No pattern alerts.", wide: true,
      cols: [{ name: "Name", ph: "ssh brute force" }, { name: "Search words", ph: "failed password and not root" }, { name: "Host", ph: "any (sw*)" },
        { name: "Severity", opts: ["any", "emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"] }, { name: "Count", ph: "5" }, { name: "Minutes", ph: "10" }, { name: "On", opts: ["yes", "no"] }],
      parse: (d) => [d.name || "", d.query || "", d.host || "", d.min_severity == null ? "any" : ["emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"][d.min_severity], String(d.count || 1), String(d.window_min || 5), d.disabled ? "no" : "yes"],
      format: (c) => [{ name: c[0].trim(), query: c[1].trim(), ...(c[2].trim() ? { host: c[2].trim() } : {}),
        ...(c[3] !== "any" ? { min_severity: ["emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"].indexOf(c[3]) } : {}),
        count: Math.max(1, parseInt(c[4], 10) || 1), window_min: Math.max(1, parseInt(c[5], 10) || 5), ...(c[6] === "no" ? { disabled: true } : {}) }],
      mark: (c) => !c[0].trim() || (!c[1].trim() && !c[2].trim() && c[3] === "any"),
      drop: (c) => !c[0].trim() && !c[1].trim() && !c[2].trim(),
    },
    // single sign-on users: "alice@example.com rw"
    ldapservers: oneCol("ldapservers", "Server", "ldaps://dc1.example.lan:636", "No directory servers yet."),
    ssousers: {
      key: "ssousers", empty: "Nobody may sign in with single sign-on yet.",
      cols: [{ name: "E-mail address", ph: "alice@example.com" }, { name: "Access", opts: ["ro", "rw"] }, { name: "Provider", opts: ["any", "google", "microsoft", "oidc"] }],
      parse: (line) => {
        const f = line.trim().split(/\s+/);
        let level = "ro", prov = "any";
        for (const t of f.slice(1).map((x) => x.toLowerCase())) {
          if (["rw", "read-write", "readwrite", "admin"].includes(t)) level = "rw";
          else if (["ro", "read-only", "readonly", "read"].includes(t)) level = "ro";
          else if (t === "google") prov = "google";
          else if (["microsoft", "entra", "azure", "ms"].includes(t)) prov = "microsoft";
          else if (["oidc", "openid", "generic"].includes(t)) prov = "oidc";
          else if (["any", "both"].includes(t)) prov = "any";
        }
        return [f[0] || "", level, prov];
      },
      format: (c) => [c[0] + " " + (c[1] || "ro") + (c[2] && c[2] !== "any" ? " " + c[2] : "")],
      mark: (c) => !c[0],
    },
  };

  // Build inputs for one object; returns {el, get()} where get() reads values back with types.
  // With asCards, every {section} spec starts a card of its own (title in the header, optional note above the fields).
  // the gateway keys everybody knows, and the shortest key not called short (keep in step with gkey.go)
  const WELL_KNOWN_KEYS = ["", "dnsdir", "ntpdir", "logdir", "dgw", "ddgw", "change-me", "changeme", "secret", "password", "key", "test", "default"];
  const isWeakKey = (k) => k.length < 12 || WELL_KNOWN_KEYS.includes(k.trim().toLowerCase());

  // The address an MCP client connects to: the first gateway's shared (VIP) address on the MCP port (443 unless set), not the
  // address of the node the page was opened on.  Set when a page that knows the configuration is drawn.
  let mcpBaseURL = "";
  const mcpURL = () => (mcpBaseURL || location.origin) + "/mcp";
  function vipBase(cfg) {
    const gs = ((cfg && cfg.groups) || []).slice().sort((a, b) => a.group_id - b.group_id);
    const g = gs.find((x) => x.vip4) || gs.find((x) => x.vip6);
    if (!g) return "";
    const ip = (g.vip4 || g.vip6).split("/")[0];
    const port = (cfg.settings && cfg.settings.mcp_port) || 443;   // the MCP listener on the gateway address, not the GUI's port
    return "https://" + (ip.includes(":") ? "[" + ip + "]" : ip) + (port === 443 ? "" : ":" + port);
  }
  const MCP_NAME_RE = /^[A-Za-z0-9._-]{1,40}$/;
  function randomKey() {
    const b = new Uint8Array(32);
    crypto.getRandomValues(b);
    return "logdir_" + btoa(String.fromCharCode(...b)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  }
  function dialog(title, body) {
    const d = h("dialog", { class: "cv-dialog", "aria-label": title });
    const close = () => { try { d.close(); } catch (_) { /* closed */ } d.remove(); };
    d.addEventListener("close", () => d.remove());
    d.append(h("h2", {}, title), ...body(close));
    root.append(d);
    d.showModal();
    return d;
  }
  // Configure ▸ Privacy: what an MCP caller is not told (mcpprivacy.go).  The default level, and what the standard and the
  // strict level each hide.  Only what differs from the built-in rules is saved, so an untouched page leaves nothing in the file.
  const PRIV_STD = { public_ips: true, private_ips: false, ip_keep: 0, public_asns: true, private_asns: false, serials: "tail", macs: "show", emails: true, contacts: true, words: [] };
  const PRIV_STRICT = { public_ips: true, private_ips: true, ip_keep: 0, public_asns: true, private_asns: true, serials: "hide", macs: "hide", emails: true, contacts: true, words: [] };
  function privacyForm(cur) {
    cur = cur || {};
    const section = (title, ...kids) => h("div", { class: "card" }, h("header", {}, h("h2", {}, tc(title))), h("div", { class: "body" }, ...kids));
    const defSel = h("select", { "aria-label": "Default privacy level" }, [["standard", "standard"], ["none", "none — nothing is hidden"], ["strict", "strict"]].map(([v, l]) => h("option", { value: v }, l)));
    defSel.value = cur.default || "standard";
    const card = (title, base, saved) => {
      const r = { ...base, ...(saved || {}), words: (saved && saved.words) || [] };
      const box = (k, label) => { const i = h("input", { type: "checkbox", checked: !!r[k] }); i.addEventListener("change", () => { r[k] = i.checked; }); return h("label", { class: "f check" }, i, label); };
      const sel = (k, label, opts, hint) => {
        const i = h("select", { "aria-label": label }, opts.map(([v, l]) => h("option", { value: String(v) }, l)));
        i.value = String(r[k]);
        i.addEventListener("change", () => { r[k] = typeof base[k] === "number" ? Number(i.value) : i.value; });
        return h("label", { class: "f" }, h("span", {}, label), i, hint ? h("span", { class: "hint" }, hint) : null);
      };
      const words = h("textarea", { rows: 4, spellcheck: "false", autocomplete: "off", "aria-label": "Words always hidden", placeholder: "one per line, e.g. your company or site names" });
      words.value = r.words.join("\n");
      words.addEventListener("change", () => { r.words = words.value.split(/\r?\n/).map((x) => x.trim()).filter(Boolean); });
      const el = section(title,
        h("div", { class: "grid" },
          box("public_ips", "Public IP addresses"), box("private_ips", "Private IP addresses (10.x, 172.16-31.x, 192.168.x, fc00::/7 …)"),
          box("public_asns", "Public AS numbers"), box("private_asns", "Private AS numbers"),
          box("emails", "E-mail addresses"), box("contacts", "SNMP contact and location")),
        h("div", { class: "grid gap-top" },
          sel("ip_keep", "IP addresses", [[0, "hidden (x.x.x.x)"], [1, "first 1 (203.x.x.x)"], [2, "first 2 (203.0.x.x)"], [3, "first 3 (203.0.114.x)"], [-1, "last 1 (x.x.x.7)"], [-2, "last 2 (x.x.114.7)"], [-3, "last 3 (x.0.114.7)"]]),
          sel("serials", "Serial numbers", [["show", "shown"], ["tail", "last four characters (****1234)"], ["hide", "hidden"]]),
          sel("macs", "MAC addresses", [["show", "shown"], ["oui", "OUI part shown (00:1a:2b:xx:xx:xx)"], ["nic", "NIC part shown (xx:xx:xx:3c:4d:5e)"], ["hide", "hidden"]])),
        h("label", { class: "f wide gap-top" }, h("span", {}, "Words always hidden"), words));
      return { el, get: () => ({ ...r, words: r.words.slice() }) };
    };
    const std = card("Standard level hides", PRIV_STD, cur.standard), strict = card("Strict level hides", PRIV_STRICT, cur.strict);
    const same = (a, b) => JSON.stringify(Object.keys(b).sort().map((k) => [k, a[k]])) === JSON.stringify(Object.keys(b).sort().map((k) => [k, b[k]]));
    const intro = section("Default level", h("label", { class: "f" }, h("span", {}, "Level for a source or key that has none of its own"), defSel));
    return {
      el: h("div", {}, intro, std.el, strict.el),
      get() {
        const o = {};
        if (defSel.value !== "standard") o.default = defSel.value;
        if (!same(std.get(), PRIV_STD)) o.standard = std.get();
        if (!same(strict.get(), PRIV_STRICT)) o.strict = strict.get();
        return Object.keys(o).length ? o : undefined;
      },
    };
  }
  // The MCP keys (Configure ▸ MCP): a name, ro or rw, and the key (kept, so Show can display it again; an old key has
  // only its hash).  Add key makes a random key here, shows it, and sends it with the next save.  A change is a "change" event,
  // so the page saves it like any other field.
  function mcpKeysEditor(list) {
    const keys = (list || []).map((k) => ({ ...k }));
    const el = h("div", { class: "mcpkeys" });
    const changed = () => { draw(); el.dispatchEvent(new Event("change", { bubbles: true })); };
    const NAME_RE = MCP_NAME_RE;
    const day = (t) => (t ? new Date(t * 1000).toLocaleDateString([], { year: "numeric", month: "short", day: "numeric" }) : "");
    const when = (t) => (t ? new Date(t * 1000).toISOString().slice(0, 16).replace("T", " ") : "");   // UTC
    const expired = (k) => !!k.expires && k.expires * 1000 <= Date.now();
    // "2026-12-31" is the end of that day, "2026-12-31 17:00" that minute; all UTC; "" is never; null is not a time
    const readWhen = (s) => {
      s = s.trim();
      if (!s || /^never$/i.test(s)) return 0;
      const m = /^(\d{4})-(\d{2})-(\d{2})(?:[T ](\d{2}):(\d{2}))?Z?$/.exec(s);
      if (!m) return null;
      const t = m[4] ? Date.UTC(+m[1], m[2] - 1, +m[3], +m[4], +m[5]) : Date.UTC(+m[1], m[2] - 1, +m[3], 23, 59);
      return Number.isNaN(t) ? null : Math.floor(t / 1000);
    };
    const shown = (k) => !!k.key && k.key !== "(hidden)";   // a key made before keys were kept has a hash only; a read-only account is not given it
    // a new key under the same name and access: whatever used the old one is refused from the next save
    function replaceKey(k) {
      if (!confirm("Replace the key " + k.name + "?\n\nWhatever uses the old key can no longer connect to /mcp.")) return;
      const key = randomKey();
      k.key = key; delete k.hash; k.created = Math.floor(Date.now() / 1000);
      changed();
    }
    // the same look as the address list above (rowTable): a numbered table, edited in place, right-click for the rest
    function draw() {
      const tbody = h("tbody", {}, keys.map((k, i) => {
        const name = h("input", { type: "text", autocomplete: "off", spellcheck: "false", maxlength: "40", "aria-label": "Name", value: k.name,
          title: "Who or what holds the key; renaming it does not change the key" });
        name.addEventListener("change", (ev) => {
          const n = name.value.trim();
          if (!NAME_RE.test(n) || keys.some((x) => x !== k && x.name.toLowerCase() === n.toLowerCase())) { ev.stopPropagation(); name.value = k.name; return; }   // not saved: the old name comes back
          k.name = n;
        });
        const acc = h("select", { "aria-label": "Access of " + k.name }, ["ro", "rw"].map((a) => h("option", { value: a }, a)));
        acc.value = k.access || "ro";
        acc.addEventListener("change", () => { k.access = acc.value; });
        const priv = h("select", { "aria-label": "Privacy of " + k.name }, ["default", "none", "standard", "strict"].map((a) => h("option", { value: a }, a)));
        priv.value = k.privacy || "default";
        priv.addEventListener("change", () => { if (priv.value === "default") delete k.privacy; else k.privacy = priv.value; });
        // the key in a password box like every other secret (the eye is added to it like to the others); an old key has none to show
        const keyIn = h("input", { type: shown(k) ? "password" : "text", readonly: true, autocomplete: "off", spellcheck: "false", "aria-label": "Key of " + k.name, class: "mono",
          value: shown(k) ? k.key : k.key || "", placeholder: "not kept: Replace key" });
        const exp = h("input", { type: "text", autocomplete: "off", spellcheck: "false", maxlength: "20", "aria-label": "Expires (UTC) of " + k.name, value: when(k.expires), placeholder: "never",
          title: "UTC. Empty: never. 2026-12-31 is the end of that day; 2026-12-31 17:00 that minute. After it the key is refused; the row stays." });
        exp.addEventListener("change", (ev) => {
          const t = readWhen(exp.value);
          if (t === null) { ev.stopPropagation(); exp.value = when(k.expires); return; }   // not a time: the old one comes back
          if (t) k.expires = t; else delete k.expires;
          changed();
        });
        const notes = h("input", { type: "text", autocomplete: "off", spellcheck: "false", maxlength: "200", "aria-label": "Notes for " + k.name, value: k.notes || "", placeholder: "what this key is for",
          title: "A note for yourself: what the key is for. It changes nothing." });
        notes.addEventListener("input", () => { const n = notes.value.replace(/[\r\n]+/g, " ").trim(); if (n) k.notes = n; else delete k.notes; });
        const tr = h("tr", { class: k.disabled || expired(k) ? "off" : "", title: expired(k) ? "Expired " + when(k.expires) + " UTC: refused" : "" },
          h("td", { class: "grip" }, h("span", { class: "rownum" }, String(i + 1))),
          h("td", {}, name),
          h("td", {}, acc),
          h("td", {}, priv),
          h("td", { class: "ctext" }, day(k.created)),
          h("td", {}, exp),
          h("td", {}, notes),
          h("td", {}, keyIn));
        tr.addEventListener("contextmenu", rowMenu([
          ["Add key", addKey],
          ["Copy key", () => { try { navigator.clipboard.writeText(k.key); } catch (_) { /* nothing to copy to */ } }, "", null, shown(k) ? "" : "No key is kept for this row: Replace key"],
          ["Replace key", () => replaceKey(k)],
          [k.disabled ? "Enable" : "Disable", () => { if (k.disabled) delete k.disabled; else k.disabled = true; changed(); }],
          ["Delete", () => { if (confirm("Delete the key " + k.name + "?\n\nWhatever uses it can no longer connect to /mcp.")) { keys.splice(keys.indexOf(k), 1); changed(); } }, "danger"],
        ]));
        return tr;
      }));
      const tbl = h("table", { class: "policy zones rows mcpkeytab" },
        h("thead", {}, h("tr", {}, h("th", { "aria-label": "Row number" }, "#"), h("th", {}, "Name"), h("th", {}, "Access"), h("th", {}, "Privacy"), h("th", {}, "Added"), h("th", {}, "Expires (UTC)"), h("th", {}, "Notes"), h("th", {}, "Key"))), tbody);
      const empty = h("div", { class: "policy-empty" }, "No keys: only the sources above may use /mcp. Right-click here ▸ Add key.");
      empty.hidden = keys.length > 0;
      const wrap = h("div", { class: "policy-wrap" }, tbl, empty);
      wrap.addEventListener("contextmenu", (e) => { if (!e.target.closest("tbody tr")) rowMenu([["Add key", addKey]])(e); });
      clear(el).append(h("div", { class: "policy-box rows wide" }, wrap));
    }
    function addKey() {
      const name = h("input", { type: "text", autocomplete: "off", spellcheck: "false", maxlength: "40", placeholder: "e.g. laptop, claude-ops", "aria-label": "Name" });
      const access = h("select", { "aria-label": "Access" }, h("option", { value: "ro" }, "ro — read only"), h("option", { value: "rw" }, "rw — read and change"));
      const err = h("div", { "aria-live": "polite" });
      dialog("Add an MCP key", (close) => [h("form", { onsubmit: (ev) => {
        ev.preventDefault();
        const n = name.value.trim();
        if (!NAME_RE.test(n)) { errorBox(err, "Give it a name of up to 40 letters, digits, dots, dashes and underscores: who or what will hold it."); return; }
        if (keys.some((k) => k.name.toLowerCase() === n.toLowerCase())) { errorBox(err, "There is already a key named " + n + "."); return; }
        const key = randomKey();
        const entry = { name: n, access: access.value, key, created: Math.floor(Date.now() / 1000) };
        keys.push(entry);
        close();
        changed();
      } },
      h("label", { class: "f wide" }, "Name", name, h("span", { class: "hint" }, "Who or what holds the key. Its changes are logged and kept in the configuration history as mcp:NAME@ADDRESS.")),
      h("label", { class: "f wide" }, "Access", access),
      err,
      h("div", { class: "toolbar" }, h("button", { class: "btn primary", type: "submit" }, "Create key"), h("button", { class: "btn", type: "button", onclick: close }, "Cancel")))]);
      name.focus();
    }
    draw();
    return { el, get: () => keys.map((k) => ({ name: k.name, access: k.access || "ro", ...(k.privacy ? { privacy: k.privacy } : {}), ...(k.disabled ? { disabled: true } : {}), ...(k.expires ? { expires: k.expires } : {}), ...(k.notes ? { notes: k.notes } : {}), ...(k.key ? { key: k.key } : { hash: k.hash }), created: k.created })) };
  }

  // the range under a number field, with its default, on one line: "10 to 600 s. Default 30."
  function rangeText(f) {
    if (f.t !== "int" && f.t !== "float") return "";
    const u = f.unit ? " " + f.unit : "";
    const r = f.range ? f.range : f.min != null && f.max != null ? f.min + " to " + f.max + u : f.min != null ? f.min + " or more" + u : f.max != null ? "up to " + f.max + u : "";
    const d = f.def != null ? "Default " + f.def + "." : "";
    return [r ? r + "." : "", d].filter(Boolean).join(" ");
  }
  function fieldset(specs, obj, asCards) {
    const getters = {}, tables = {};
    let grid = h("div", { class: "grid" });
    const wrap = asCards ? h("div", {}) : grid;
    for (const f of specs) {
      if (f.section) {
        if (!asCards) { // a heading inside the one grid
          grid.append(h("div", { class: "fsec" }, h("h3", {}, tc(f.section)), f.note ? h("p", { class: "hint nomargin" }, f.note) : null));
          continue;
        }
        grid = h("div", { class: f.cols === 3 ? "grid c3" : "grid" });
        wrap.append(h("div", { class: "card" }, h("header", {}, h("h2", {}, tc(f.section))),
          h("div", { class: "body" }, f.note ? h("p", { class: "hint" }, f.note) : null, grid)));
        continue;
      }
      const v = obj[f.k];
      let input, get;
      if (f.t === "bool") {
        input = h("input", { type: "checkbox", checked: !!v });
        get = () => input.checked;
        grid.append(h("label", { class: "f check" }, input, f.l, sharedTag(f)));
        getters[f.k] = get;
        continue;
      }
      if (f.t === "mcpkeys") {
        const mk = mcpKeysEditor(v || []);
        getters[f.k] = mk.get;
        grid.append(h("div", { class: "f wide" }, h("span", {}, f.l, sharedTag(f)), mk.el));   // what keys are and do is in the help, like the address list above
        continue;
      }
      if (f.t === "rows") {
        const rt = rowTable(ROW_TABLES[f.rows], v || []);
        getters[f.k] = rt.get;
        tables[f.k] = rt;
        grid.append(h("div", { class: "f wide" }, h("span", {}, f.l, sharedTag(f)), rt.el, f.hint ? h("span", { class: "hint" }, f.hint) : null));
        continue;
      }
      if (f.t === "select") {
        input = h("select", {}, f.options.map((o) => h("option", { value: o, selected: o === v }, o)));
        get = () => input.value;
      } else if (f.t === "list") {
        input = h("textarea", { rows: 3, spellcheck: "false" });
        input.value = (v || []).join("\n");
        get = () => input.value.split("\n").map((s) => s.trim()).filter(Boolean);
      } else if (f.t === "port") {
        // a listen address kept as ":11154" in the file is shown as the port alone; one with a host in front is shown as it is
        input = h("input", { type: "text", inputmode: "numeric", autocomplete: "off", spellcheck: "false" });
        input.value = v == null || v === "" ? (f.def || "") : String(v).replace(/^:(\d+)$/, "$1");
        get = () => { const t = input.value.trim(); return /^\d+$/.test(t) ? ":" + t : t; };
      } else if (f.t === "secret") {
        input = h("input", { type: "password", autocomplete: "new-password", spellcheck: "false" });
        input.value = v == null ? "" : v;
        get = () => input.value.trim();
      } else if (f.t === "int" || f.t === "float") {
        input = h("input", { type: "number", step: f.t === "float" ? "any" : "1", min: f.min, max: f.max });
        input.value = v == null || v === "" || (v === 0 && f.def != null && (f.min > 0 || f.zeroDef)) ? (f.def == null ? "" : f.def) : v;   // empty or "the default" is shown as the default itself
        get = () => (input.value === "" ? 0 : Number(input.value));
      } else {
        input = h("input", { type: "text", autocomplete: "off", spellcheck: "false" });
        input.value = v == null ? "" : v;
        get = () => (f.raw ? input.value : input.value.trim());   // raw: kept exactly as typed
      }
      getters[f.k] = get;
      // a well-known or short gateway key: a few words of red text beside the field, no more (it follows what is typed)
      const weak = f.weakKey ? h("span", { class: "hint weak-key" }) : null;
      if (weak) {
        const upd = () => { weak.textContent = isWeakKey(input.value) ? "Well known or short: anyone on the network can take over this gateway." : ""; };
        input.addEventListener("input", upd); upd();
      }
      grid.append(h("label", { class: "f" + (f.wide ? " wide" : "") + (f.brk ? " brk" : "") }, h("span", {}, f.l, sharedTag(f)), input, (f.hint || rangeText(f)) ? h("span", { class: "hint" }, [f.hint, rangeText(f)].filter(Boolean).join(" ")) : null, weak));
    }
    return { el: wrap, table: (k) => tables[k], get: () => Object.fromEntries(Object.entries(getters).map(([k, g]) => [k, g()])) };
  }

  // Configure ▸ Settings, Gateways, MCP, Senders, Web GUI, Monitoring, Alerts and Backup: one page each, all made from the one form
  // (every page shows its own panel and saves through the same call).
  // Configure ▸ Alerts: where alerts are sent, and the rules.  The rules come from the node (its catalog), the settings go back
  // in the settings.alerts block; only a rule that was touched is written out in full, so the others keep following the defaults.
  const ALERT_FIX = {};
  const alertsForm = (acfg) => {
    acfg = acfg || {};
    const saved = acfg.rules || {}, dirty = new Set(), rows = {};
    const webhook = h("input", { type: "text", value: acfg.webhook || "", placeholder: "https://hooks.example.com/…", spellcheck: "false", autocomplete: "off", "aria-label": "Webhook address" });
    const repeat = h("input", { type: "number", min: "0", max: "720", value: String(acfg.repeat_hours || 0), "aria-label": "Repeat every (hours)" });
    const testOut = h("span", { class: "hint" });
    const test = h("button", { type: "button", class: "btn", onclick: async () => {
      testOut.textContent = "Sending…";
      try { await api("POST", "/api/alerts/test", {}); testOut.textContent = "The webhook took the message."; } catch (e) { testOut.textContent = "Not sent: " + e.message; }
    } }, "Send test");
    const rulesBox = h("div", {});
    const num = (v, max) => h("input", { type: "number", min: "0", max: String(max || 100000), step: "any", value: String(v), class: "arule-n" });
    const sel = (opts, v) => h("select", {}, opts.map(([k, l]) => h("option", { value: k, selected: k === v }, l)));
    async function fill() {
      let rules;
      try { rules = (await api("GET", "/api/alerts")).data.rules; } catch (e) { clear(rulesBox).append(h("div", { class: "hint" }, "The rules could not be read from this node: " + e.message)); return; }
      const tbody = h("tbody", {});
      const GROUPS = [["gateways", "Gateways"], ["cfg", "Certificates and backups"], ["host", "Host"]];
      const groupOf = (r) => (r.go === "cfg_web" || r.go === "cfg_backup" ? "cfg" : r.go || "host");
      const unitSpan = (t) => h("span", { class: "arule-u" }, t);
      const cell = (...k) => h("td", {}, h("div", { class: "arule-c" }, ...k));
      const dash = () => h("td", {}, h("span", { class: "muted", "aria-label": "Not used" }, "–"));
      for (const [gk, gname] of GROUPS) {
        const list = rules.filter((r) => groupOf(r) === gk);
        if (!list.length) continue;
        tbody.append(h("tr", { class: "agroup" }, h("th", { colspan: "7", scope: "colgroup" }, gname)));
        for (const r of list) {
          const c = { en: h("input", { type: "checkbox", checked: r.enabled, "aria-label": "On: " + r.title }), sev: sel([["info", "Info"], ["warning", "Warning"], ["critical", "Critical"]], r.severity_now),
            clearMode: sel([["checks", "checks"], ["minutes", "min"]], r.clear_mode), clear: num(r.clear) };
          c.sev.setAttribute("aria-label", "Severity: " + r.title); c.clear.setAttribute("aria-label", "Clear after: " + r.title);
          c.clearMode.setAttribute("aria-label", "Clear after, counted in: " + r.title);
          c.sev.className = "arule-sev";
          const sevSync = () => { c.sev.dataset.sev = c.sev.value; };
          c.sev.addEventListener("change", sevSync); sevSync();
          let when, thr, forc;
          if (r.kind === "updown") {
            c.mode = sel([["in_a_row", "Checks in a row"], ["minutes", "Minutes down"], ["m_of_n", "Checks of last"]], r.mode);
            c.mode.setAttribute("aria-label", "Alert when: " + r.title);
            c.after = num(r.after); c.of = num(r.of, 100);
            c.after.setAttribute("aria-label", "Threshold: " + r.title); c.of.setAttribute("aria-label", "Of the last checks: " + r.title);
            const ofBox = h("span", { class: "arule-c" }, unitSpan("of"), c.of);
            const unit = unitSpan("");
            const sync = () => { ofBox.hidden = c.mode.value !== "m_of_n"; unit.textContent = c.mode.value === "minutes" ? "min" : "checks"; };
            c.mode.addEventListener("change", sync); sync();
            when = cell(c.mode); thr = cell(c.after, ofBox, unit); forc = dash();
          } else {
            c.thr = num(r.threshold_now); c.forMin = num(r.for_minutes_now, 10080);
            c.thr.setAttribute("aria-label", "Threshold: " + r.title); c.forMin.setAttribute("aria-label", "For minutes: " + r.title);
            when = h("td", {}, h("span", { class: "arule-dir" }, r.below ? "Value below" : "Value above")); thr = cell(c.thr, unitSpan(r.unit)); forc = cell(c.forMin, unitSpan("min"));
          }
          const tr = h("tr", { "data-rule": r.id }, h("td", { class: "arule-on" }, c.en),
            h("td", {}, h("div", { class: "arule-t" }, r.title), h("div", { class: "muted small" }, r.subject ? "One alert per " + r.subject : "This node")),
            h("td", {}, c.sev), when, thr, forc, cell(c.clear, c.clearMode));
          const offSync = () => tr.classList.toggle("off", !c.en.checked);
          c.en.addEventListener("change", offSync); offSync();
          tr.addEventListener("change", () => dirty.add(r.id));
          rows[r.id] = { r, c };
          tbody.append(tr);
        }
      }
      const cols = ["2.4rem", "", "8.6rem", "10.5rem", "12.5rem", "6rem", "11rem"];
      clear(rulesBox).append(h("div", { class: "scroll" }, h("table", { class: "arules" },
        h("colgroup", {}, cols.map((w) => h("col", w ? { class: "w-" + w.replace(".", "_") } : {}))),
        h("thead", {}, h("tr", {}, ["On", "Rule", "Severity", "Alert when", "Threshold", "For", "Clear after"].map((x) => h("th", {}, x)))), tbody)));
      if (state.alertRule && rows[state.alertRule]) {
        const tr = rulesBox.querySelector('tr[data-rule="' + state.alertRule + '"]');
        if (tr) { tr.classList.add("flash"); tr.scrollIntoView({ block: "center" }); }
      }
      state.alertRule = null;
    }
    fill();
    const ruleOf = (id) => {
      const { r, c } = rows[id];
      const o = { enabled: c.en.checked, severity: c.sev.value, clear_mode: c.clearMode.value, clear: Number(c.clear.value) };
      if (r.kind === "updown") { o.mode = c.mode.value; o.after = Number(c.after.value); if (o.mode === "m_of_n") o.of = Number(c.of.value); }
      else { o.threshold = Number(c.thr.value); o.for_minutes = Number(c.forMin.value); }
      return o;
    };
    return {
      el: h("div", {},
        h("div", { class: "grid" }, h("label", { class: "f wide" }, "Webhook address", webhook), h("label", { class: "f" }, "Repeat every (hours)", repeat)),
        h("div", { class: "toolbar" }, test, testOut), rulesBox),
      get() {
        const rules = {};
        for (const [id, old] of Object.entries(saved)) if (!dirty.has(id)) rules[id] = old;
        for (const id of dirty) if (rows[id]) rules[id] = ruleOf(id);
        const a = {};
        if (webhook.value.trim()) a.webhook = webhook.value.trim();
        if (Number(repeat.value) > 0) a.repeat_hours = Number(repeat.value);
        if (Object.keys(rules).length) a.rules = rules;
        return Object.keys(a).length ? a : undefined;
      },
    };
  };

  // Configure ▸ Backup: three cards.  The servers are the shared settings (the settings.backup block); when backups run is a
  // Scheduler task (action "backup"), shown and created from the first card.  There is no archive password (since v188):
  // an old one in the settings is kept as it is, to open the archives it made.  The status and the restore
  // are those of the node picked in the top bar.  Every explanation is in the help.
  // the task the "Schedule daily backups" button adds (02:30 UTC; each node at its own time within five minutes)
  const BACKUP_TASK = { id: "backup", name: "Back up every node", action: "backup", freq: "daily", at: "02:30", until: "02:35" };
  // the columns of the table of backup servers (the server entries are part of the same shared block)
  const BACKUP_COLS = [
    ["name", "Name", "text", "Backup 1"], ["host", "Server", "text", "backup.example.com"], ["port", "Port", "int", "22"],
    ["user", "User", "text", "logdir"], ["dir", "Directory", "text", "backups/logdir"], ["keep", "Keep", "int", "14"],
  ];
  // setSchedule(list) tells the config page the Scheduler's task list changed, so its next save carries the new one
  const backupForm = (bcfg, setSchedule) => {
    bcfg = bcfg || {};
    const notSetting = (el) => { el.addEventListener("change", (e) => e.stopPropagation()); return el; };
    const at = (u) => (u ? new Date(u * 1000).toLocaleString([], { dateStyle: "medium", timeStyle: "short" }) : "never");
    const bytes = (n) => (n > 1048576 ? (n / 1048576).toFixed(1) + " MB" : Math.max(1, Math.round(n / 1024)) + " kB");
    let last = null;   // the last /api/backup answer

    // ── Schedule: when this node last backed up and will next; the buttons under it ──
    const lastEl = h("dd", {}, "");
    const nextEl = h("dd", {}, "");
    const runBtn = h("button", { type: "button", class: "btn" }, "Back up now");
    const runErr = h("span", { class: "bad-text small" });
    runBtn.addEventListener("click", async () => {
      runBtn.disabled = true; runErr.textContent = "";
      try { await api("POST", "/api/backup/run", {}); start(); show(); } catch (e) { runBtn.disabled = false; runErr.textContent = e.message; }
    });
    const toSched = h("button", { type: "button", class: "btn" }, "Open Scheduler");
    toSched.addEventListener("click", () => selectTab("scheduler"));
    const addTask = h("button", { type: "button", class: "btn", hidden: true }, "Schedule daily backups");
    addTask.addEventListener("click", async () => {
      addTask.disabled = true; addTask.textContent = "Scheduling…"; runErr.textContent = "";
      try {
        await api("POST", "/api/schedule/task", { task: BACKUP_TASK });
        try { const c = await api("GET", "/api/config"); if (setSchedule) setSchedule(c.config.dns.schedule); } catch (_) { /* the next load reads it */ }
        show();
      } catch (e) { runErr.textContent = e.message; }
      finally { addTask.disabled = false; addTask.textContent = "Schedule daily backups"; }
    });
    async function showWhen() {
      let tasks;
      try { tasks = ((await api("GET", "/api/schedule")).data.tasks || []).filter((t) => t.action === "backup"); } catch (_) { return; }
      const on = tasks.filter((t) => !t.disabled && t.applies);
      const next = on.map((t) => t.next_run).filter(Boolean).sort((a, b) => a - b)[0];
      clear(nextEl).append(on.length ? at(next) + " · " + on.map((t) => t.describe).join("; ")
        : h("span", { class: "bad-text" }, tasks.length ? "Not scheduled (the task is off)" : "Not scheduled"));
      addTask.hidden = tasks.length > 0;
    }
    const schedule = section("Schedule",
      h("dl", { class: "kv" }, h("dt", {}, "Last backup"), lastEl, h("dt", {}, "Next backup"), nextEl),
      h("div", { class: "toolbar nomargin" }, runBtn, addTask, toSched, runErr));
    // ── Servers: one row per server with its status under it; click a row to pick it, the toolbar acts on that one ──
    const initial = (bcfg.targets || []).slice();
    if (bcfg.host || bcfg.user || bcfg.dir) initial.unshift({ host: bcfg.host, port: bcfg.port, user: bcfg.user, dir: bcfg.dir, keep: bcfg.keep });   // the single server of older versions
    const tbody = h("tbody", {});
    const rows = () => [...tbody.children].filter((tr) => tr._get);
    let sel = null;   // the picked row
    const addBtn = h("button", { type: "button", class: "btn" }, "Add a server");
    const testBtn = h("button", { type: "button", class: "btn" }, "Test");
    const keyBtn = h("button", { type: "button", class: "btn", "aria-expanded": "false" }, "Install key");
    const delBtn = h("button", { type: "button", class: "btn" }, "Remove");
    // Install key: the account's password, used once and kept nowhere
    const pw = h("input", { type: "password", autocomplete: "new-password", "aria-label": "Password of the account on the server" });
    const installBtn = h("button", { type: "button", class: "btn" }, "Install");
    const keyOut = h("span", { class: "small" });
    const keyForm = h("div", { class: "toolbar tight bk-keyform", hidden: true }, pw, installBtn, keyOut);
    const closeKey = () => { keyForm.hidden = true; keyBtn.setAttribute("aria-expanded", "false"); pw.value = ""; keyOut.textContent = ""; };
    const pickRow = (tr) => {
      if (sel !== tr) {
        if (sel) { sel.classList.remove("sel"); sel._statRow.classList.remove("sel"); }
        sel = tr && tr.isConnected ? tr : null;
        if (sel) { sel.classList.add("sel"); sel._statRow.classList.add("sel"); }
        closeKey();
      }
      testBtn.disabled = keyBtn.disabled = delBtn.disabled = !sel;
      addBtn.disabled = rows().length >= 8;
    };
    const addRow = (t) => {
      t = t || {};
      const ins = {};
      const tr = h("tr", {});
      for (const [k, l, ty, ph] of BACKUP_COLS) {
        ins[k] = h("input", { type: ty === "int" ? "number" : "text", min: ty === "int" ? "0" : undefined, max: k === "port" ? "65535" : k === "keep" ? "365" : undefined,
          placeholder: ph, spellcheck: "false", autocomplete: "off", "aria-label": l, class: "bk-" + k });
        ins[k].value = t[k] == null || t[k] === 0 ? "" : t[k];
        tr.append(h("td", {}, ins[k]));
      }
      const stat = h("span", {});
      const msg = h("span", {});
      const statRow = h("tr", { class: "bk-statrow" }, h("td", { colspan: String(BACKUP_COLS.length), class: "small" }, stat, msg));
      tr._statRow = statRow;
      tr._ins = ins;
      tr._index = () => rows().indexOf(tr);
      tr._say = (bad, x) => { msg.textContent = x; msg.className = bad ? "bad-text" : ""; };
      for (const r of [tr, statRow]) {
        r.addEventListener("focusin", () => pickRow(tr));
        r.addEventListener("mousedown", () => pickRow(tr));
      }
      tr._get = () => {
        const o = {};
        for (const [k, , ty] of BACKUP_COLS) {
          const v = ins[k].value.trim();
          if (v === "" || (ty === "int" && Number(v) === 0)) continue;
          o[k] = ty === "int" ? Number(v) : v;
        }
        return o;
      };
      // the status of the saved server at this place in the list (nothing while the row is not saved yet)
      tr._status = (ts) => {
        const t = ts[tr._index()];
        clear(stat);
        if (!t || t.host !== ins.host.value.trim() || t.user !== ins.user.value.trim()) return;
        const s = t.status || {};
        if (s.last_ok) stat.append(h("span", { class: "muted", title: (s.last_file || "") + (s.last_size ? ", " + bytes(s.last_size) : "") }, "Last: " + at(s.last_ok)));
        else stat.append(h("span", { class: "muted" }, "No backup yet"));
        const n = (s.remote || []).length;
        if (n) stat.append(h("span", { class: "muted", title: s.remote.slice().reverse().join("\n") }, " · " + n + " on the server"));
        if (s.last_error && !(msg.className === "bad-text" && msg.textContent)) stat.append(h("span", { class: "bad-text" }, " · Failed: " + s.last_error));
      };
      tbody.append(tr, statRow);
      if (last) tr._status(last.targets || []);
      return tr;
    };
    initial.forEach(addRow);
    if (!initial.length) addRow();
    addBtn.addEventListener("click", () => { if (rows().length < 8) { const tr = addRow(); pickRow(tr); tr._ins.name.focus(); } });
    testBtn.addEventListener("click", async () => {
      const tr = sel; if (!tr) return;
      testBtn.disabled = true; testBtn.textContent = "Testing…"; tr._say(false, "");
      try { await api("POST", "/api/backup/test", { index: tr._index() }); tr._say(false, " · Connected"); show(); }
      catch (e) { tr._say(true, " · Failed: " + e.message); show(); }
      finally { testBtn.textContent = "Test"; testBtn.disabled = !sel; }
    });
    keyBtn.addEventListener("click", () => {
      if (!sel) return;
      if (!keyForm.hidden) { closeKey(); return; }
      keyForm.hidden = false; keyBtn.setAttribute("aria-expanded", "true");
      pw.placeholder = "Password of " + (sel._ins.user.value.trim() || "the user") + "@" + (sel._ins.host.value.trim() || "the server");
      pw.focus();
    });
    installBtn.addEventListener("click", async () => {
      const tr = sel; if (!tr) return;
      installBtn.disabled = true; installBtn.textContent = "Installing…"; keyOut.textContent = ""; keyOut.className = "small";
      try {
        const r = (await api("POST", "/api/backup/install", { index: tr._index(), password: pw.value })).data;
        pw.value = "";
        keyOut.textContent = (r.added ? "Installed for " + r.added + " of " + r.nodes + " node(s)." : "Already installed.") + (r.connected ? "" : " Login still fails: " + r.test_error) + ((r.missing || []).length ? " No key from " + r.missing.join(", ") + "." : "");
        if (!r.connected || (r.missing || []).length) keyOut.className = "small bad-text";
        show();
      } catch (e) { keyOut.textContent = e.message; keyOut.className = "small bad-text"; }
      finally { installBtn.disabled = false; installBtn.textContent = "Install"; }
    });
    pw.addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); installBtn.click(); } else if (e.key === "Escape") { closeKey(); keyBtn.focus(); } });
    delBtn.addEventListener("click", () => {
      const tr = sel; if (!tr) return;
      pickRow(null);
      tr._statRow.remove(); tr.remove();
      tbody.dispatchEvent(new Event("change", { bubbles: true }));
      pickRow(null);   // the Add button again
    });
    // Use the cluster nodes: every node makes the account, the directory and the keys itself and becomes a server of the list
    const clusterBtn = h("button", { type: "button", class: "btn", title: "Every node keeps the backups of the others. The account, keys and directory are made on each." }, "Use the cluster nodes");
    const clusterOut = h("span", { class: "small bad-text" });
    clusterBtn.addEventListener("click", async () => {
      clusterBtn.disabled = true; clusterBtn.textContent = "Setting up…"; clusterOut.textContent = "";
      try {
        const r = (await api("POST", "/api/backup/cluster", {})).data;
        // the server saved the new list: show it, and make it what this page holds, without another save
        pickRow(null);
        clear(tbody);
        (r.targets || []).forEach(addRow);
        if (!rows().length) addRow();
        pickRow(null);
        if (state.cfg && state.cfg.settings) state.cfg.settings.backup = { ...(state.cfg.settings.backup || {}), targets: r.targets || [] };
        const bad = (r.nodes || []).filter((n) => n.error).map((n) => n.node + ": " + n.error);
        if (r.full) bad.push(r.full + " node(s) not added: at most 8 servers");
        clusterOut.textContent = bad.join(" · ");
        show();
      } catch (e) { clusterOut.textContent = e.message; }
      finally { clusterBtn.disabled = false; clusterBtn.textContent = "Use the cluster nodes"; }
    });
    pickRow(null);
    const bar = notSetting(h("div", { class: "toolbar tight" }, addBtn, testBtn, keyBtn, delBtn, ...(state.clustered ? [clusterBtn, clusterOut] : [])));
    notSetting(keyForm);
    const table = h("div", { class: "scroll" }, h("table", { class: "tight bk-table" },
      h("thead", {}, h("tr", {}, BACKUP_COLS.map((c) => h("th", {}, c[1])))), tbody));
    const servers = section("Servers", bar, keyForm, table);
    { const tag = sharedTag({ shared: true }); if (tag) servers.querySelector("h2").append(" ", tag); }   // the list is the cluster's

    // ── this node's status, in the two cards above ──
    let timer = null;
    const stop = () => { if (timer) { clearInterval(timer); timer = null; } };
    function start() { if (!timer) timer = setInterval(() => show(true), 3000); }
    async function show(fromTimer) {
      if (fromTimer === true && !schedule.isConnected) { stop(); return; }
      let v;
      try { v = (await api("GET", "/api/backup")).data; } catch (e) { runErr.textContent = e.message; stop(); return; }
      last = v;
      const st = v.status || {};
      runBtn.disabled = !!v.running; runBtn.textContent = v.running ? "Backing up…" : "Back up now";
      lastEl.textContent = at(st.last_ok) + (v.retry ? " · a failed server is tried again " + at(v.retry) : "");
      showWhen();
      rows().forEach((tr) => tr._status(v.targets || []));
      restoreFrom(v);
      if (v.running) start(); else stop();
    }
    // a saved edit can change the servers: read their status again once the save is through
    let again = null;
    const later = () => { clearTimeout(again); again = setTimeout(show, 1500); };

    // ── Restore: pick where the backup is and which one, then Restore (one confirmation, then the node restarts) ──
    const rbody = h("div", {});
    const restore = notSetting(section("Restore", rbody));
    // the configured servers are the first choices and are read from the status, so a server added on this page shows up
    // here at once; picking one lists its backups straight away (only Another server… needs List backups)
    let restoreFrom = () => {};
    function restoreUI() {
      // why the backups could not be listed, or the restore failed: a line of its own under the controls
      const rerr = h("span", {});
      const lerr = h("span", {});
      const errLine = h("div", { class: "bad-text small bk-err" }, lerr, rerr);
      const otherOpt = h("option", { value: "other" }, "Another server");
      const src = h("select", { "aria-label": "Where the backup is" }, otherOpt, h("option", { value: "upload" }, "A file from this computer"));
      let chosen = false;   // the person picked a source: do not move it
      const o = { host: h("input", { type: "text", placeholder: "Server", spellcheck: "false", "aria-label": "Server" }), port: h("input", { type: "number", placeholder: "22", class: "arule-n", "aria-label": "SSH port" }),
        user: h("input", { type: "text", placeholder: "User", spellcheck: "false", "aria-label": "User" }), dir: h("input", { type: "text", placeholder: "Directory", spellcheck: "false", "aria-label": "Directory" }),
        pw: h("input", { type: "password", autocomplete: "new-password", placeholder: "Account password", "aria-label": "Password of the account on that server" }) };
      const otherBox = h("div", { class: "toolbar tight", hidden: true }, o.host, o.port, o.user, o.dir, o.pw);
      const fileIn = h("input", { type: "file", accept: ".netbak", hidden: true, "aria-label": "Backup file" });
      const list = h("select", { "aria-label": "Backup to restore", hidden: true });
      const listBtn = h("button", { type: "button", class: "btn" }, "List backups");
      // the list shows this node's backups; "All nodes" adds the other nodes' (to rebuild a lost node on this one)
      const allIn = h("input", { type: "checkbox" });
      const allBox = h("label", { class: "check", hidden: true }, allIn, " All nodes");
      let files = [], myNode = "";
      const showFiles = () => {
        const mine = files.filter((f) => allIn.checked || f.node === myNode);
        clear(list).append(...mine.map((f) => h("option", { value: f.name }, (allIn.checked ? f.node + " — " : "") + new Date(f.time).toLocaleString([], { dateStyle: "medium", timeStyle: "short" }))));
        list.hidden = !mine.length;
        allBox.hidden = !files.length;   // only once there is a list to widen
        lerr.textContent = mine.length ? "" : files.length ? "No backups of this node there: tick All nodes to see the others." : "No backups there.";
        canGo();
      };
      allIn.addEventListener("change", showFiles);
      const go = h("button", { type: "button", class: "btn danger", disabled: true }, "Restore");
      // Restore is for a picked backup: a listed one, or a chosen file
      const canGo = () => { go.disabled = src.value === "upload" ? !fileIn.files.length : (list.hidden || list.disabled || !list.value); };
      fileIn.addEventListener("change", canGo);
      list.addEventListener("change", canGo);
      let seq = 0;
      async function listFiles() {
        const n = ++seq;
        rerr.textContent = ""; lerr.textContent = "";
        listBtn.disabled = true; listBtn.textContent = "Listing…";
        clear(list).append(h("option", { value: "" }, "Listing…")); list.disabled = true; list.hidden = false; canGo();
        try {
          const r = (await api("POST", "/api/backup/files", serverArg())).data.files || [];
          if (n !== seq) return;
          files = r;
          list.disabled = false;
          showFiles();
        } catch (e) { if (n === seq) { files = []; list.hidden = true; allBox.hidden = true; lerr.textContent = "Cannot list the backups: " + e.message; } }
        finally { if (n === seq) { list.disabled = false; listBtn.disabled = false; listBtn.textContent = "List backups"; canGo(); } }
      }
      listBtn.addEventListener("click", listFiles);
      go.addEventListener("click", async () => {
        go.disabled = true; go.textContent = "Opening…"; rerr.textContent = "";
        let prepared = false;
        try {
          let body;
          if (src.value === "upload") {
            if (!fileIn.files[0]) throw new Error("Choose the backup file first.");
            await apiUpload("/api/backup/upload", fileIn.files[0]);
            body = { source: "upload" };
          } else {
            if (list.hidden || list.disabled || !list.value) throw new Error(src.value === "other" ? "List the backups and pick one." : "Pick a backup.");
            body = { source: "server", file: list.value, ...serverArg() };
          }
          const r = (await api("POST", "/api/backup/restore/prepare", body)).data;
          prepared = true;
          o.pw.value = "";
          // one question; for another node's backup it is the "are you sure" one
          const newer = r.newer ? "\n\nIt was made by a newer version (" + r.version + ") than this node runs." : "";
          const ok = confirm(r.same_node
            ? "Restore the backup of " + r.node + " made " + r.created + "?\n\nThis node's settings and keys are replaced and it restarts. The old ones are kept next to its state directory." + newer
            : "Are you sure? This is a backup of " + r.node + " (made " + r.created + "), not of this node (" + (myNode || "this node") + ").\n\nThis node becomes " + r.node + ": its identity, keys and certificates, and it restarts. Do this only to replace " + r.node + " when it is gone, never while it is still running." + newer);
          if (!ok) throw new Error("");   // declined: drop what was opened
          await api("POST", "/api/backup/restore/apply", {});
          go.textContent = "Restarting…";
          const wait = async () => { try { const r2 = await fetch("/api/session", { credentials: "same-origin" }); if (r2.status < 500) { location.reload(); return; } } catch (_) { /* still down */ } setTimeout(wait, 3000); };
          setTimeout(wait, 6000);
          return;   // the button stays as it is until the page reloads
        } catch (e) {
          rerr.textContent = e.message;
          if (prepared) api("POST", "/api/backup/restore/cancel", {}).catch(() => {});
        }
        go.textContent = "Restore"; canGo();
      });
      function serverArg() {
        if (src.value.startsWith("s")) return { index: Number(src.value.slice(1)) };
        return { host: o.host.value.trim(), port: Number(o.port.value) || 0, user: o.user.value.trim(), dir: o.dir.value.trim(), server_password: o.pw.value };
      }
      const sync = () => {
        const other = src.value === "other";
        seq++;
        otherBox.hidden = !other; fileIn.hidden = src.value !== "upload"; listBtn.hidden = !other; list.hidden = true; list.disabled = false; rerr.textContent = ""; lerr.textContent = "";
        allBox.hidden = true; files = [];
        canGo();
        if (src.value.startsWith("s")) listFiles();
      };
      src.addEventListener("change", () => { chosen = true; sync(); });
      let known = null;
      restoreFrom = (v) => {
        myNode = v.node || "";
        const tl = v.targets || [];
        const sig = JSON.stringify(tl.map((t) => [t.label, t.host, t.user]));
        if (sig === known) return;
        known = sig;
        const was = src.value;
        [...src.options].filter((op) => op.value.startsWith("s")).forEach((op) => op.remove());
        tl.forEach((t, i) => src.insertBefore(h("option", { value: "s" + i }, t.label), otherOpt));
        if (chosen && [...src.options].some((op) => op.value === was)) src.value = was;
        else { chosen = chosen && !was.startsWith("s"); src.value = !chosen && tl.length ? "s0" : (was.startsWith("s") ? "other" : was); }
        sync();
      };
      sync();
      rbody.replaceChildren(h("div", { class: "bk-restore" }, h("div", { class: "toolbar nomargin" }, src, listBtn, list, fileIn, allBox, go), otherBox, errLine));
    }
    restoreUI();
    show();

    const el = h("div", {}, schedule, servers, restore);
    el.addEventListener("change", later);
    return {
      el,
      get() {
        const b = bcfg.password ? { password: bcfg.password } : {};   // the old archive password, kept to open old archives
        const ts = rows().map((tr) => tr._get()).filter((t) => Object.keys(t).length);
        if (ts.length) b.targets = ts;
        return Object.keys(b).length ? b : undefined;
      },
    };
  };

  // ── Configure ▸ Shipping (settings.logs.destinations, ld_ship.go): where every node sends on what it received ─────────────
  const SHIP_TYPES = [["syslog", "Syslog (RFC 5424)"], ["json_tcp", "JSON over TCP"], ["splunk_hec", "Splunk HEC"], ["elastic", "Elasticsearch"], ["loki", "Loki"]];
  const SHIP_SEVS = [["", "Any severity"], ["crit", "Critical or worse"], ["err", "Error or worse"], ["warning", "Warning or worse"], ["warning=", "Warning only"], ["notice", "Notice or worse"], ["info", "Info or worse"]];
  const SHIP_SEVN = ["emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"];
  const SHIP_STARTS = [["now", "Now"], ["1h", "1 hour back"], ["24h", "24 hours back"], ["7d", "7 days back"], ["oldest", "The oldest kept"]];
  const shipHttp = (t) => t === "splunk_hec" || t === "elastic" || t === "loki";
  // the filter as the Logs page writes it: words plus host=, program= and source= (several source= are several senders)
  const shipFilterText = (d) => [d.host && "host=" + d.host, d.program && "program=" + d.program, ...(d.senders || []).map((s) => "source=" + s), d.query].filter(Boolean).join(" ");
  function shipFilterParse(txt) {
    const o = { host: "", program: "", senders: [], query: [] };
    for (const w of String(txt || "").trim().split(/\s+/).filter(Boolean)) {
      const m = /^(host|program|prog|source|src)=(.+)$/i.exec(w);
      if (!m) o.query.push(w);
      else if (/^host$/i.test(m[1])) o.host = m[2];
      else if (/^(program|prog)$/i.test(m[1])) o.program = m[2];
      else o.senders.push(m[2]);
    }
    return { ...(o.host ? { host: o.host } : {}), ...(o.program ? { program: o.program } : {}), ...(o.senders.length ? { senders: o.senders } : {}), ...(o.query.length ? { query: o.query.join(" ") } : {}) };
  }
  const shipSevValue = (d) => (d.min_severity == null ? "" : SHIP_SEVN[d.min_severity] + (d.sev_exact ? "=" : ""));
  const shipSevFields = (v) => {
    if (!v) return {};
    const exact = v.endsWith("="), n = SHIP_SEVN.indexOf(v.replace(/=$/, ""));
    return n < 0 ? {} : { min_severity: n, ...(exact ? { sev_exact: true } : {}) };
  };
  const shipTarget = (d) => (shipHttp(d.type) ? d.url || "" : (d.type === "syslog" ? (d.protocol || "tcp") + "://" : (d.protocol || "tcp") + "://") + (d.server || ""));
  // open the Logs page on what a destination sends (one sender at most: the Logs page filters by one)
  function shipPreview(d) {
    const f = { ...d, senders: (d.senders || []).slice(0, 1) };
    state.prefill.logsq = { text: shipFilterText(f), severity: shipSevValue(d) };
    selectTab("logs");
  }
  // the popup of one destination; save(d) stores it (an error text keeps the popup open)
  function shipEditor(cur, isNew, save, names) {
    cur = cur || { type: "syslog" };
    const inp = (v, ph, type) => h("input", { type: type || "text", value: v == null ? "" : String(v), placeholder: ph || "", spellcheck: "false", autocomplete: type === "password" ? "new-password" : "off" });
    const sel = (opts, v) => { const s = h("select", {}, opts.map(([k, l]) => h("option", { value: k }, l))); s.value = v; if (s.value !== v && v) { s.append(h("option", { value: v }, v)); s.value = v; } return s; };
    const fld = (label, el, hint) => h("label", { class: "f" }, label, el, hint ? h("span", { class: "hint" }, hint) : null);
    const name = inp(cur.name, "siem"), type = sel(SHIP_TYPES, cur.type || "syslog");
    const proto = sel([["tcp", "TCP"], ["udp", "UDP"], ["tls", "TLS"]], cur.protocol || "tcp");
    const server = inp(cur.server, "10.5.0.20 or log.example.com:6514"), url = inp(cur.url, "https://splunk.example.com:8088");
    const token = inp(cur.token, "", "password"), index = inp(cur.index, "logdir");
    const filter = inp(shipFilterText(cur), "failed password host=fw* source=fw1"), sev = sel(SHIP_SEVS, shipSevValue(cur));
    const start = sel(SHIP_STARTS, cur.start || "now"), on = h("input", { type: "checkbox", checked: !cur.disabled });
    const rate = inp(cur.rate_limit || "", "no limit", "number"), behind = inp(cur.max_behind_hours || "", "24", "number");
    const raw = h("input", { type: "checkbox", checked: !!cur.raw }), spoof = h("input", { type: "checkbox", checked: !!cur.spoof_source });
    const ca = h("textarea", { rows: "4", spellcheck: "false", placeholder: "-----BEGIN CERTIFICATE-----", class: "mono" });
    ca.value = cur.ca_pem || "";
    rate.min = "0"; behind.min = "0";
    const fServer = fld("Server", server, "Port 514 (6514 for TLS) unless given."), fProto = fld("Protocol", proto), fUrl = fld("URL", url), fToken = fld("Token", token), fIndex = fld("Index", index);
    const fSpoof = h("label", { class: "f check wide" }, spoof, "Send from the sender's address (UDP, IPv4)");
    const fCA = fld("CA certificate (PEM)", ca, "Empty: the system's.");
    const sync = () => {
      const t = type.value, http = shipHttp(t);
      fServer.hidden = fProto.hidden = http;
      fUrl.hidden = fToken.hidden = !http;
      fIndex.hidden = t !== "elastic" && t !== "splunk_hec";
      proto.querySelector("option[value=udp]").hidden = t !== "syslog";
      if (t !== "syslog" && proto.value === "udp") proto.value = "tcp";
      fSpoof.hidden = !(t === "syslog" && proto.value === "udp");
      fCA.hidden = !(http || proto.value === "tls");
      url.placeholder = { splunk_hec: "https://splunk.example.com:8088", elastic: "https://es.example.com:9200", loki: "http://loki.example.com:3100" }[t] || "";
    };
    type.addEventListener("change", sync); proto.addEventListener("change", sync);
    const read = () => {
      const t = type.value, http = shipHttp(t), d = { name: name.value.trim(), type: t };
      if (http) { if (url.value.trim()) d.url = url.value.trim(); if (token.value) d.token = token.value; } else { d.server = server.value.trim(); if (proto.value !== "tcp") d.protocol = proto.value; }
      if ((t === "elastic" || t === "splunk_hec") && index.value.trim()) d.index = index.value.trim();
      if ((http || proto.value === "tls") && ca.value.trim()) d.ca_pem = ca.value.trim();
      Object.assign(d, shipFilterParse(filter.value), shipSevFields(sev.value));
      if (start.value !== "now") d.start = start.value;
      if (Number(behind.value) > 0) d.max_behind_hours = Math.floor(Number(behind.value));
      if (Number(rate.value) > 0) d.rate_limit = Math.floor(Number(rate.value));
      if (raw.checked) d.raw = true;
      if (spoof.checked && !fSpoof.hidden) d.spoof_source = true;
      if (!on.checked) d.disabled = true;
      return d;
    };
    const check = (d) => {
      if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(d.name)) return "Name: letters, digits, . _ or - (up to 64).";
      if ((names || []).some((n) => n.toLowerCase() === d.name.toLowerCase())) return "Another destination is called " + d.name + ".";
      if (shipHttp(d.type) ? !/^https?:\/\/[^\s/]+/.test(d.url || "") : !d.server) return shipHttp(d.type) ? "URL: https://host:port." : "Server: a host or an address.";
      if (d.type === "splunk_hec" && !d.token) return "Splunk HEC needs the token.";
      return "";
    };
    const err = h("span", { class: "bad-text" }), testMsg = h("span", { class: "muted small" });
    dialog(isNew ? "New destination" : "Edit destination " + cur.name, (close) => {
      const testBtn = h("button", { type: "button", class: "btn", onclick: async () => {
        const d = read(), m = check(d);
        err.textContent = m; testMsg.textContent = "";
        if (m) return;
        testBtn.disabled = true; testMsg.textContent = "Sending…";
        try {
          const r = (await api("POST", "/api/logs/shipping/test", { dest: d })).data.results[0];
          testMsg.textContent = r.ok ? "Sent." + (r.note ? " " + r.note : "") : ""; err.textContent = r.ok ? "" : "Test failed: " + r.error;
        } catch (e) { testMsg.textContent = ""; err.textContent = e.message; }
        testBtn.disabled = false;
      } }, "Test");
      const form = h("form", { class: "shipform", onsubmit: (e) => {
        e.preventDefault();
        const d = read(), m = check(d) || save(d);
        if (m) { err.textContent = m; return; }
        close();
      } },
      h("div", { class: "grid" }, fld("Name", name), fld("Type", type), fServer, fProto, fUrl, fToken, fIndex,
        h("label", { class: "f wide" }, "Filter", filter, h("span", { class: "hint" }, "As on the Logs page. Empty: all.")), fld("Severity", sev),
        fld("Start", start, isNew ? "Where it starts." : "Only for a new one."), h("label", { class: "f check wide" }, on, "Enabled")),
      h("details", { class: "shipadv" }, h("summary", {}, "Advanced"),
        h("div", { class: "grid" }, fld("Rate limit (messages/s)", rate, "Empty: no limit."), fld("Max behind (hours)", behind, "Older ones are skipped."),
          h("label", { class: "f check wide" }, raw, "Also send the original bytes"), fSpoof, fCA)),
      h("div", { class: "toolbar right" }, err, testMsg, h("span", { class: "grow" }), testBtn,
        h("button", { type: "button", class: "btn", onclick: () => { const d = read(); close(); shipPreview(d); } }, "Preview"),
        h("button", { type: "submit", class: "btn primary" }, "Save"), h("button", { type: "button", class: "btn", onclick: close }, "Cancel")));
      if (cur.advanced || cur.raw || cur.spoof_source || cur.ca_pem || cur.rate_limit || cur.max_behind_hours) form.querySelector("details").open = true;
      return [form];
    });
    sync();
    name.focus();
  }
  // the table of destinations: get() is what is saved; a change sends a change event up, which saves the page
  function shippingForm(list) {
    const data = (list || []).map((d) => ({ ...d }));
    const box = h("div", {});
    const changed = () => box.dispatchEvent(new Event("change", { bubbles: true }));
    const names = (except) => data.filter((d) => d !== except).map((d) => d.name);
    const edit = (d) => shipEditor(d, false, (nd) => { data[data.indexOf(d)] = nd; render(); changed(); return ""; }, names(d));
    const add = (pre) => shipEditor(pre, true, (nd) => { data.push(nd); render(); changed(); return ""; }, names());
    function menu(d) {
      return [["Edit", () => edit(d)], ["Preview", () => shipPreview(d)], ["Duplicate", () => add({ ...d, name: d.name + "-copy" })],
        [d.disabled ? "Enable" : "Disable", () => { if (d.disabled) delete d.disabled; else d.disabled = true; render(); changed(); }],
        ["Monitor", () => selectTab("shipping")], "-",
        ["Delete", () => { data.splice(data.indexOf(d), 1); render(); changed(); }, "danger"]];
    }
    function render() {
      const sevText = (d) => (SHIP_SEVS.find((x) => x[0] === shipSevValue(d)) || ["", shipSevValue(d)])[1];
      clear(box).append(h("div", { class: "toolbar nomargin" }, h("button", { type: "button", class: "btn", onclick: () => add() }, "＋ New destination")),
        data.length ? h("div", { class: "scroll" }, h("table", { class: "shipcfg" },
          h("thead", {}, h("tr", {}, ["Name", "Type", "Target", "Filter", "Severity", "On"].map((x) => h("th", {}, x)))),
          h("tbody", {}, data.map((d) => h("tr", { class: d.disabled ? "off" : "", oncontextmenu: rowMenu(menu(d)), ondblclick: () => edit(d) },
            h("td", {}, h("button", { type: "button", class: "qlink", onclick: () => edit(d) }, d.name)),
            h("td", {}, (SHIP_TYPES.find((t) => t[0] === d.type) || ["", d.type])[1]),
            h("td", { class: "mono" }, shipTarget(d)),
            h("td", { class: "mono" }, shipFilterText(d) || "everything"),
            h("td", {}, sevText(d)),
            h("td", {}, d.disabled ? "no" : "yes"))))))
          : h("div", { class: "empty" }, "No destinations: nothing is shipped."));
    }
    render();
    // sent here from the Logs page or Monitor ▸ Senders: a new destination with that filter
    if (state.prefill.ship) { const p = state.prefill.ship; state.prefill.ship = null; setTimeout(() => add(p), 0); }
    if (state.prefill.shipEdit) { const n = state.prefill.shipEdit; state.prefill.shipEdit = null; const d = data.find((x) => x.name === n); if (d) setTimeout(() => edit(d), 0); }
    return { el: box, get: () => data };
  }

  const makeConfigView = (only) => {
    let holder, status, collect = null, refreshSNMP = () => {}, refreshSyslog = () => {}, refreshDNSReg = () => {}, refreshLLDP = () => {};
    const tab = only;

    // Action results ("Saved", "Restored" …) are not announced; only errors, warnings and hints are.
    const say = (kind, ...msg) => kind !== "bad" ? clear(status) : clear(status).append(h("div", { class: "notice " + kind, role: kind === "bad" ? "alert" : "status" }, ...msg));

    function renderForm(cfg) {
      const logs = fieldset(LOG_FIELDS, cfg);
      const set = cfg.settings = cfg.settings || {};
      mcpBaseURL = vipBase(cfg);
      const syslog = fieldset(SYSLOG_FIELDS, cfg.syslog || {});
      const dnsreg = fieldset(DNSREG_FIELDS, Object.assign({}, cfg.dns_register || {}, { skip_interfaces: ((cfg.dns_register || {}).skip_interfaces || []).join(", ") }));
      const web = fieldset(WEB_FIELDS, cfg.web);
      const sso = ssoTabs(cfg.web);
      const ldap = ldapTab(cfg.web);
      const senders = fieldset(SENDER_FIELDS, { senders: (set.logs || {}).senders });
      const logAlerts = fieldset(LOGALERT_FIELDS, { logalerts: (set.logs || {}).alerts });
      const ship = shippingForm((set.logs || {}).destinations);
      const cluster = fieldset(CLUSTER_FIELDS, cfg.cluster || {});
      const snmp = fieldset(SNMP_FIELDS, { snmp_communities: set.snmp_communities, snmp_acl: set.snmp_acl, snmp_contact: set.snmp_contact, location: (cfg.snmp || {}).location });
      const prom = fieldset(PROM_FIELDS, { metrics_acl: set.metrics_acl });
      const mcp = fieldset(MCP_FIELDS, { mcp_port: set.mcp_port, mcp_max_result_mb: set.mcp_max_result_mb, mcp_log: !!set.mcp_log, mcp_log_mb: set.mcp_log_mb, mcp_log_entry_kb: set.mcp_log_entry_kb, mcp_acl: set.mcp_acl, mcp_keys: set.mcp_keys });
      const lldp = fieldset(LLDP_FIELDS, { lldp: !!set.lldp });
      const topo = fieldset(TOPO_FIELDS, { topology_squares_above: set.topology_squares_above });
      const topoShared = () => { const n = topo.get().topology_squares_above; return n > 0 && n !== 36 ? { topology_squares_above: n } : {}; };
      const lg = set.logs || {};
      const logSet = fieldset(COLLECT_FIELDS, { log_dir: cfg.log_dir || "", retention_days: lg.retention_days || 0, dir_max_percent: lg.dir_max_percent || 90, mem_max_percent: lg.mem_max_percent || 70, list_limit: lg.list_limit || 1000, follow: !!lg.follow,
        udp_port: lg.udp_port || 514, tcp_port: lg.tcp_port || 514, tls_port: lg.tls_port || 6514 });
      // settings.logs has other fields (enabled, max_line) that this page has no boxes for: they are carried through as they were; the senders, the
      // alerts and the boxes above are written (a port or a cap at its default is not)
      // the folder in use is the value shown, not an empty box: asked of the node being configured (a proxied call when another node is picked)
      let logDefaultDir = "";
      (async () => {
        try {
          const n = (((await api("GET", "/api/logs/status?local=1")).data || {}).nodes || [])[0];
          if (!n) return;
          logDefaultDir = n.default_dir || "";
          const span = [...logSet.el.querySelectorAll("label span")].find((e) => e.textContent === "Storage folder");
          const inp = span && span.parentElement.querySelector("input");
          if (inp && !inp.value.trim()) inp.value = n.dir || logDefaultDir;
        } catch (_) { /* the box stays as it is */ }
      })();
      const logsShared = () => {
        const snd = senders.get().senders || [], al = logAlerts.get().logalerts || [], v = logSet.get();
        const num = (x, def) => { x = Math.trunc(Number(x)); return Number.isFinite(x) && x !== def ? x : 0; };
        const dst = ship.get();
        const { senders: _s, alerts: _a, destinations: _ds, retention_days: _r, dir_max_percent: _d, mem_max_percent: _m, list_limit: _n, follow: _f, udp_port: _u, tcp_port: _t, tls_port: _l, ...rest } = set.logs || {};
        const pct = Number(v.dir_max_percent) > 0 && Number(v.dir_max_percent) !== 90 ? Math.min(100, Math.floor(Number(v.dir_max_percent))) : 0;
        const mpct = Number(v.mem_max_percent) > 0 && Number(v.mem_max_percent) !== 70 ? Math.min(100, Math.floor(Number(v.mem_max_percent))) : 0;
        const out = { ...rest, ...(snd.length ? { senders: snd } : {}), ...(al.length ? { alerts: al } : {}), ...(dst.length ? { destinations: dst } : {}), ...(num(v.retention_days, 0) > 0 ? { retention_days: num(v.retention_days, 0) } : {}),
          ...(num(v.list_limit, 1000) ? { list_limit: Math.min(5000, Math.max(100, num(v.list_limit, 1000))) } : {}), ...(v.follow ? { follow: true } : {}), ...(pct ? { dir_max_percent: pct } : {}), ...(mpct ? { mem_max_percent: mpct } : {}), ...(num(v.udp_port, 514) ? { udp_port: num(v.udp_port, 514) } : {}), ...(num(v.tcp_port, 514) ? { tcp_port: num(v.tcp_port, 514) } : {}),
          ...(num(v.tls_port, 6514) ? { tls_port: num(v.tls_port, 6514) } : {}) };
        return Object.keys(out).length ? { logs: out } : {};
      };
      const priv = privacyForm(set.privacy);
      const privShared = () => { const p = priv.get(); return p ? { privacy: p } : {}; };
      const lldpShared = () => ({ lldp: lldp.get().lldp });
      // what lldpd is doing on this node, beside the box (refreshed after every save); a few words, never a banner
      const lldpState = h("span", { class: "muted small" });
      const lldpBox = lldp.el.querySelector("input[type=checkbox]");
      refreshLLDP = async () => {
        try {
          const st = (await api("GET", "/api/lldp")).data;
          const words = lldpWords(st);
          clear(lldpState).append(...(st.running ? [pill("ACTIVE", "ok tiny")] : []), words === "Running" ? "" : " " + words);
          lldpBox.disabled = !!st.deferred;   // another program on this host decides
        } catch (_) { clear(lldpState); }
      };
      refreshLLDP();
      // lldpd starts or stops a moment after a save, and can be changed behind the page's back: ask again every few seconds
      // while the card is on screen (the tab shown, the window visible); the timer ends once the page is drawn again or left
      const lldpTimer = setInterval(() => {
        if (!lldpState.isConnected) { clearInterval(lldpTimer); return; }
        if (lldpState.offsetParent !== null && !document.hidden) refreshLLDP();
      }, 3000);
      const alerts = alertsForm(set.alerts);
      const alertsShared = () => { const a = alerts.get(); return a ? { alerts: a } : {}; };
      const backup = backupForm(set.backup, (sch) => { set.schedule = sch; if (state.cfg && state.cfg.settings) state.cfg.settings.schedule = sch; });
      const backupShared = () => { const b = backup.get(); return b ? { backup: b } : {}; };
      // what snmpd is doing, under the form (refreshed after every save)
      const snmpShared = () => { const v = snmp.get(), m = prom.get(); return { snmp_communities: v.snmp_communities, snmp_acl: v.snmp_acl, snmp_contact: v.snmp_contact, metrics_acl: m.metrics_acl, mcp_acl: mcp.get().mcp_acl, mcp_keys: mcp.get().mcp_keys, ...(mcp.get().mcp_port ? { mcp_port: mcp.get().mcp_port } : {}), ...(mcp.get().mcp_max_result_mb > 1 ? { mcp_max_result_mb: mcp.get().mcp_max_result_mb } : {}), ...(mcp.get().mcp_log ? { mcp_log: true } : {}), ...(mcp.get().mcp_log_mb > 0 ? { mcp_log_mb: mcp.get().mcp_log_mb } : {}), ...(mcp.get().mcp_log_entry_kb > 0 ? { mcp_log_entry_kb: mcp.get().mcp_log_entry_kb } : {}) }; };
      const snmpLocal = () => { const v = snmp.get(); return { location: v.location }; };
      const snmpPill = h("span");   // ACTIVE beside the heading while snmpd is running
      refreshSNMP = async () => {
        try {
          const st = (await api("GET", "/api/snmp")).data;
          clear(snmpPill);
          if (st.enabled && st.installed && st.running) snmpPill.append(pill("ACTIVE", "ok tiny"));
        } catch (_) { clear(snmpPill); }  // no tag when the node cannot be asked
      };
      refreshSNMP();
      const groupsEl = h("div", {});
      const groups = []; // {el, get}
      const addGroup = (g) => {
        const fs = fieldset(GROUP_FIELDS, g);
        const entry = { fs, orig: g };
        const title = h("h3", {}, "Group");
        const card = h("div", { class: "card" },
          h("header", {}, title),
          h("div", { class: "body" }, fs.el));
        entry.card = card;
        const gid = fs.el.querySelector("input[type=number]");
        const retitle = () => { title.textContent = "Group " + gid.value; };
        gid.addEventListener("input", retitle); retitle();
        groups.push(entry);
        groupsEl.append(card);
      };
      cfg.groups.forEach(addGroup);
      const section = (title, ...kids) => h("div", { class: "card" }, h("header", {}, h("h2", {}, tc(title))), h("div", { class: "body" }, ...kids));
      collect = () => ({
        ...logs.get(),
        // keep what this form has no fields for (pauses, removed nodes, the order of the nodes, anycast addresses)
        groups: groups.map((g) => ({ ...(g.orig.paused ? { paused: true } : {}), ...(g.orig.paused_all ? { paused_all: true } : {}), ...(g.orig.excluded_nodes ? { excluded_nodes: g.orig.excluded_nodes } : {}), ...(g.orig.node_order ? { node_order: g.orig.node_order } : {}), ...(g.orig.paused_vips ? { paused_vips: g.orig.paused_vips } : {}), ...(g.orig.paused_vips_here ? { paused_vips_here: g.orig.paused_vips_here } : {}), ...(g.orig.extra_vips ? { extra_vips: g.orig.extra_vips } : {}), ...g.fs.get() })),
        settings: { ...(set.schedule ? { schedule: set.schedule } : {}), ...snmpShared(), ...lldpShared(), ...topoShared(), ...logsShared(), ...privShared(), ...alertsShared(), ...backupShared() },
        // the certificate is managed under Web GUI; keep any file paths already in the config
        web: { cert_file: cfg.web.cert_file || "", key_file: cfg.web.key_file || "", ...web.get(), ...sso.get(), ...ldap.get() },
        cluster: cluster.get(),
        snmp: snmpLocal(),
        ...(() => { const v = syslog.get(); return v.enabled || v.server ? { syslog: v } : {}; })(), // empty settings leave no key
        ...(() => {
          const v = dnsreg.get();
          const o = { enabled: v.enabled, key: v.key, domain: v.domain, server: v.server, interval_sec: v.interval_sec === 300 ? 0 : v.interval_sec, ttl: v.ttl === 900 ? 0 : v.ttl,
            skip_interfaces: String(v.skip_interfaces || "").split(",").map((x) => x.trim()).filter(Boolean) };   // the defaults are not written
          return o.enabled || o.key || o.domain || o.server || o.interval_sec || o.ttl || o.skip_interfaces.length ? { dns_register: o } : {};
        })(),
        ...(cfg.bgp ? { bgp: cfg.bgp } : {}), // edited on the Anycast page
        ...(cfg.ospf ? { ospf: cfg.ospf } : {}), // likewise
        ...(cfg.node_paused ? { node_paused: true } : {}), // set on the Node page
        ...(cfg.log_listen ? { log_listen: cfg.log_listen } : {}), // this node's extra log addresses: set in the configuration file
        ...((logSet.get().log_dir || "").trim() && logSet.get().log_dir.trim() !== logDefaultDir ? { log_dir: logSet.get().log_dir.trim() } : {}), // this node's log folder (the default one is not written)
      });
      // every committed edit (leaving a field, picking an option, ticking a box) is saved and applied
      // Automatic updates are not part of the config file: switching them is its own call, made at once
      const updatesCard = () => {
        if (state.autoUpdate == null) return null;
        const cb = h("input", { type: "checkbox", checked: state.autoUpdate });
        cb.addEventListener("change", async (ev) => {
          ev.stopPropagation(); // not a config edit
          const on = cb.checked;
          if (on && !confirm("Turn automatic updates on?\n\nEvery node will build and install any newer staged release, one node at a time.")) { cb.checked = false; return; }
          try { await api("POST", "/api/update/auto", { enabled: on }); state.autoUpdate = on; say("ok"); }
          catch (e) { cb.checked = !on; say("bad", "Not changed: " + (e.message || e)); }
        });
        // the repository "Fetch from online" downloads from: shared, saved when the field is left; empty goes back to the default
        const DEFAULT_REPO = "https://github.com/micush/logdir";
        const repoIn = h("input", { type: "text", value: state.updateRepo ? "https://github.com/" + state.updateRepo : DEFAULT_REPO, spellcheck: "false", autocomplete: "off", "aria-label": "Online repository" });
        const repoErr = h("span", { class: "notice bad", role: "alert" });
        repoIn.addEventListener("change", async (ev) => {
          ev.stopPropagation(); // not a config edit
          clear(repoErr);
          try {
            await api("POST", "/api/update/repo", { repo: repoIn.value.trim() });
            state.updateRepo = ((await api("GET", "/api/update")).data.intent.repo) || "";
            repoIn.value = state.updateRepo ? "https://github.com/" + state.updateRepo : DEFAULT_REPO;
          } catch (e) { repoErr.textContent = e.message || String(e); }
        });
        return section("Upgrades", h("label", { class: "f check" }, cb, "Update every node automatically when a newer release is staged (one node at a time)"),
          h("label", { class: "f repo" }, h("span", {}, "Online repository"), repoIn, repoErr),
          h("p", { class: "hint" }, "Upload a release on the Upgrade page; with this on, each node then builds and installs it in turn and rolls back by itself if the new version does not stay up."));
      };
      // the GUI certificate lives under Web GUI; its own controls save through their own calls, never through the settings form
      const certBox = h("div", {});
      certBox.addEventListener("change", (e) => e.stopPropagation());
      VIEWS.certificate.mount(certBox);
      // the Monitoring page is two tabs: SNMP and Metrics (every card stays in the page, so a save reads them all)
      const monitoringTabs = () => {
        const TABS = [
          ["SNMP", (() => { const c = section("SNMP", snmp.el); c.querySelector("h2").append(snmpPill); return c; })()],
          ["Metrics", section("Metrics", prom.el, h("p", { class: "muted small" },
            h("a", { href: "/api/metrics/dashboard.json", download: "logdir-grafana-dashboard.json" }, "Grafana dashboard example"), "\u00a0\u00a0-\u00a0\u00a0",
            h("a", { href: "/api/metrics/alerts.yml", download: "logdir-prometheus-alerts.yml" }, "Prometheus alert rules example")))],
        ];
        const panes = TABS.map(([, el]) => h("div", { role: "tabpanel" }, el));
        const buttons = TABS.map(([name], i) => h("button", { type: "button", role: "tab", class: "dtab", onclick: () => show(i) }, name));
        function show(i, focus) {
          state.monTab = i;
          refreshHelp();
          panes.forEach((p, j) => { p.hidden = j !== i; });
          buttons.forEach((b, j) => { b.setAttribute("aria-selected", j === i ? "true" : "false"); b.tabIndex = j === i ? 0 : -1; b.classList.toggle("on", j === i); });
          if (focus) buttons[i].focus();
        }
        const strip = h("div", { class: "dtabs", role: "tablist", "aria-label": "Monitoring settings" }, buttons);
        strip.addEventListener("keydown", (e) => {
          const d = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
          if (!d) return;
          e.preventDefault();
          show((state.monTab + d + TABS.length) % TABS.length, true);
        });
        show(state.monTab >= 0 && state.monTab < TABS.length ? state.monTab : 0);
        return h("div", {}, strip, ...panes);
      };
      // Syslog: the state of the connection is shown under the settings, with a button that sends a test message
      const syslogState = h("div", { class: "hint" });
      const syslogMsg = h("span", { class: "hint" });
      refreshSyslog = async () => {
        try {
          const st = (await api("GET", "/api/syslog")).data.status;
          clear(syslogState);
          if (!st.enabled) { syslogState.append("Forwarding is off."); return; }
          syslogState.append(pill(st.connected ? "connected" : "not connected", st.connected ? "ok tiny" : "bad tiny"),
            " " + st.protocol + " to " + st.address + " · " + st.sent + " sent, " + st.dropped + " dropped" + (st.last_error ? " · " + st.last_error : ""));
        } catch (_) { clear(syslogState); }
      };
      refreshSyslog();
      const syslogTest = h("button", { class: "btn", type: "button" }, "Send test message");
      syslogTest.addEventListener("click", async (ev) => {
        ev.stopPropagation(); // not a config edit
        clear(syslogMsg); syslogTest.disabled = true;
        try { await api("POST", "/api/syslog/test", {}); syslogMsg.textContent = "Delivered."; }
        catch (e) { if (e.message === "unauthenticated") throw e; syslogMsg.textContent = "Not delivered: " + (e.message || e); }
        syslogTest.disabled = false;
        refreshSyslog();
      });
      // DNS registration: what the last run did is shown under the settings, with buttons that run it now or take the records out
      const dnsState = h("div", { class: "hint" });
      const dnsMsg = h("span", { class: "hint" });
      refreshDNSReg = async () => {
        try {
          const st = (await api("GET", "/api/dnsreg")).data.status;
          clear(dnsState);
          if (!st.last_run) { dnsState.append(st.enabled ? "Registering shortly." : "Registration is off."); return; }
          const when = new Date(st.last_run).toLocaleString();
          dnsState.append(pill(st.failed ? "failed" : "ok", st.failed ? "bad tiny" : "ok tiny"),
            " " + (st.action === "remove" ? "Removal" : "Last check") + " " + when + " · " + st.existing + " right, " + st.updated + " updated" + (st.removed ? ", " + st.removed + " removed" : "") + ", " + st.failed + " failed" + (st.server ? " · server " + st.server : ""));
          const bad = (st.records || []).filter((x) => x.result === "failed").slice(0, 5);
          if (st.last_error && !bad.length) dnsState.append(h("div", {}, st.last_error));   // a run that failed before any record: the one error
          for (const r of bad) dnsState.append(h("div", {}, r.type + " " + r.name + ": " + (r.error || "failed")));
        } catch (_) { clear(dnsState); }
      };
      refreshDNSReg();
      const dnsBtn = (label, path, done) => {
        const b = h("button", { class: "btn", type: "button" }, label);
        b.addEventListener("click", async (ev) => {
          ev.stopPropagation(); // not a config edit
          if (path.endsWith("/remove") && b.dataset.sure !== "1") {   // a second click confirms
            b.dataset.sure = "1"; b.textContent = "Click again to remove";
            setTimeout(() => { b.dataset.sure = ""; b.textContent = label; }, 4000);
            return;
          }
          b.dataset.sure = ""; b.textContent = label;
          clear(dnsMsg); b.disabled = true; dnsMsg.textContent = "Working…";
          try { await api("POST", path, {}); dnsMsg.textContent = done; }
          catch (e) { if (e.message === "unauthenticated") throw e; dnsMsg.textContent = "Not done: " + (e.message || e); }
          b.disabled = false;
          refreshDNSReg();
        });
        return b;
      };
      // the Settings page is tabs (every card stays in the page, so a save reads all of them)
      const settingsTabs = () => {
        const TABS = [
          ["general", "General", h("div", {}, section("Neighbor discovery", lldp.el, h("p", { class: "f" }, lldpState)), section("Topology", topo.el), section("Log collector", logSet.el))],
          ["logs", "Daemon Log", h("div", {}, section("Log level", logs.el),
            section("Syslog", syslog.el, h("div", { class: "toolbar" }, syslogTest, syslogMsg, syslogState),
              h("p", { class: "hint" }, "Every line the log shows is also sent to the server, from this node only: the setting is not copied to the other nodes. A server that is down never holds anything up; the newest messages wait in a queue and the oldest are dropped if it fills.")))],
          ["dns", "DNS", h("div", {}, section("DNS registration", dnsreg.el, h("div", { class: "toolbar" }, dnsBtn("Register now", "/api/dnsreg/now", "Checked."), dnsBtn("Remove records", "/api/dnsreg/remove", "Removed."), dnsMsg, dnsState),
            h("p", { class: "hint" }, "This node puts its own addresses into DNS: its first address of each family as its host name, every interface as host-interface, and a PTR record for each address. The names given to the gateways are registered too: a gateway's VIP FQDN holds its shared addresses and its Anycast FQDN its anycast addresses, each address with a PTR record back to its name (set them under Topology ▸ Edit gateway); the shared addresses are never registered under this node's own name. Register now and Remove records use the saved settings. Turning this off leaves the records where they are; Remove records takes them out (turn this off first, or the next check puts them back). The setting is not copied to the other nodes.")))],
          ["upgrades", "Upgrades", updatesCard() || section("Upgrades", h("p", { class: "hint" }, "The upgrade settings cannot be read right now."))],
          ["cluster", "Cluster", section("Cluster", cluster.el)],
        ];
        const panes = TABS.map(([, , el]) => h("div", { role: "tabpanel" }, el));
        const buttons = TABS.map(([, name], i) => h("button", { type: "button", role: "tab", class: "dtab", onclick: () => show(i) }, name));
        function show(i, focus) {
          state.settingsTab = TABS[i][0];
          refreshHelp();
          panes.forEach((p, j) => { p.hidden = j !== i; });
          buttons.forEach((b, j) => { b.setAttribute("aria-selected", j === i ? "true" : "false"); b.tabIndex = j === i ? 0 : -1; b.classList.toggle("on", j === i); });
          if (focus) buttons[i].focus();
        }
        const strip = h("div", { class: "dtabs", role: "tablist", "aria-label": "Settings" }, buttons);
        strip.addEventListener("keydown", (e) => {
          const d = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
          if (!d) return;
          e.preventDefault();
          const cur = Math.max(0, TABS.findIndex((t) => t[0] === state.settingsTab));
          show((cur + d + TABS.length) % TABS.length, true);
        });
        show(Math.max(0, TABS.findIndex((t) => t[0] === state.settingsTab)));
        return h("div", {}, strip, ...panes);
      };
      // one card per tab; every tab stays in the page (the hidden ones are just not shown) so a save reads all of them
      const panels = [
        ["general", "Settings", settingsTabs()],
        ["groups", "Gateway groups", h("div", {}, groupsEl)],
        ["senders", "Senders", h("div", {}, section("Senders", senders.el), section("Pattern alerts", logAlerts.el,
          h("p", { class: "hint" }, "Every minute each node counts the messages it received that match, in the minutes given. Reaching the count opens an alert; it closes when the count falls under. Words: and, or, not (or !word); the host takes * wildcards; severity is that severe or worse.")))],
        ["shipping", "Shipping", section("Destinations", ship.el)],
        ["web", "Web GUI", h("div", {}, section("Web GUI", web.el), certBox)],
        ["sso", "SSO", section("Single sign-on",
          sso.el)],
        ["ldap", "LDAP", section("LDAP sign-in", ldap.el)],
        ["mcp", "MCP", (() => { const c = section("MCP", mcp.el, h("p", { class: "muted small mono" }, mcpURL())); c.classList.add("mcpcard"); return c; })()],
        ["privacy", "Privacy", priv.el],
        ["monitoring", "Monitoring", monitoringTabs()],
        ["alerts", "Alerts", section("Alerts", alerts.el)],
        ["backup", "Backup", backup.el],
      ];
      for (const [pid, , el] of panels) el.hidden = pid !== tab;
      const form = h("div", {}, ...panels.map((p) => p[2]));
      form.addEventListener("change", () => autosave());
      return form;
    }

    function draw() {
      clear(holder).append(renderForm(state.cfg));
    }

    async function load() {
      try {
        const c = await api("GET", "/api/cluster");
        state.clustered = !!(c.data.enabled && c.data.peers.length > 1);
      } catch (e) { if (e.message === "unauthenticated") throw e; }
      try { const u = (await api("GET", "/api/update")).data.intent; state.autoUpdate = !!u.auto_all; state.updateRepo = u.repo || ""; } catch (e) { if (e.message === "unauthenticated") throw e; state.autoUpdate = null; }
      const r = await api("GET", "/api/config");
      state.cfg = r.config;
      state.cfgInfo = r;
      state.cfgBase = r.base;
      clear(status);
      if (!r.exists) say("info", "No config file at " + r.path + " yet — the first change creates it.");
      draw();
    }

    // Saving is automatic: there is no Save or Apply button.  One save runs at a
    // time; an edit made while one is running triggers another straight after.
    let busy = false, again = false;
    async function autosave() {
      if (busy) { again = true; return; }
      busy = true;
      try { do { again = false; await save(); } while (again); } finally { busy = false; }
    }

    async function save() {
      let cfg;
      try { cfg = collect(); } catch (e) { say("bad", e.message); return; }
      const was = state.cfg && state.cfg.web ? state.cfg.web : {};
      if (cfg.web.listen !== was.listen &&
        !confirm("Change the GUI listen address to " + cfg.web.listen + "?\n\nThe GUI restarts and every session ends. If the address is wrong you will lose access to this page.")) {
        draw(); // put the old value back
        return;
      }
      try {
        const r = await api("PUT", "/api/config", { config: cfg, base: state.cfgBase });
        state.cfg = r.config;
        state.cfgBase = r.base;
        clear(status);
        state.cfgInfo.exists = true;
        setTimeout(() => refreshSNMP(), 1500);   // snmpd was (re)started by the save
        setTimeout(() => refreshSyslog(), 800);  // so was the connection to the syslog server
        setTimeout(() => refreshDNSReg(), 6000); // and the DNS registration loop checks a few seconds after it starts
        setTimeout(() => refreshLLDP(), 1500);   // and lldpd started or stopped
      } catch (e) {
        if (e.message === "unauthenticated") return;
        if (e.status === 409) {   // the saved configuration is newer than this page's copy: take it (as the message says), or every later save is refused too
          try { await load(); } catch (_) { /* the message below still tells what happened */ }
        }
        say("bad", "Not saved: " + e.message);
      }
    }

    return {
      mount(main) {
        status = h("div", { "aria-live": "polite" });
        holder = h("div", {});
        main.append(status, holder);
        load().catch((e) => { if (e.message !== "unauthenticated") say("bad", e.message); });
      },
    };
  };
  for (const id of ["general", "groups", "mcp", "privacy", "senders", "shipping", "web", "sso", "ldap", "monitoring", "alerts", "backup"]) VIEWS["cfg_" + id] = makeConfigView(id);

  // ── shared helpers for the management pages ───────────────────────────────
  // Nothing is said when every node answered (the Node menu says "Cluster"); when one could not, which and why, since the
  // numbers are then missing its share
  const clusterNote = (c) => {
    const lost = c && c.nodes ? c.nodes.filter((n) => !n.ok) : [];
    if (!lost.length) return [];
    return [h("div", { class: "notice bad", role: "alert" }, "Not included in these numbers: " + lost.map((n) => n.name + " (" + n.error + ")").join(", "))];
  };
  // A right-click menu for a table row: items are [label, handler, "danger"?]. Reuses the Topology menu's look.
  let rowMenuEl = null, rowSubEl = null;
  const closeRowSub = () => { if (rowSubEl) { rowSubEl.remove(); rowSubEl = null; } if (rowMenuEl) rowMenuEl.querySelectorAll(".has-sub.open").forEach((b) => b.classList.remove("open")); };
  // an item whose handler is an array is a sub-menu: [label, [[label, handler], ...]]; it opens beside the item
  const openRowSub = (btn, subItems) => {
    closeRowSub();
    btn.classList.add("open");
    rowSubEl = h("div", { class: "cv-menu", role: "menu" }, subItems.map((it) => h("button", { type: "button", role: "menuitem", onclick: () => { closeRowMenu(); it[1](); } }, plain(it[0]))));
    document.body.append(rowSubEl);
    const br = btn.getBoundingClientRect(), r = rowSubEl.getBoundingClientRect();
    const left = br.right + r.width + 4 <= window.innerWidth ? br.right - 2 : Math.max(4, br.left - r.width + 2);
    rowSubEl.style.left = left + "px";
    rowSubEl.style.top = Math.max(4, Math.min(br.top - 4, window.innerHeight - r.height - 4)) + "px";
  };
  const rowMenuClick = (e) => {
    const b = e.target.closest ? e.target.closest("button.has-sub") : null;
    if (b && rowMenuEl && rowMenuEl.contains(b)) { e.stopPropagation(); e.preventDefault(); b.dispatchEvent(new Event("opensub")); return; }
    closeRowMenu();
  };
  const closeRowMenu = () => {
    if (!rowMenuEl) return;
    closeRowSub();
    rowMenuEl.remove(); rowMenuEl = null;
    document.removeEventListener("click", rowMenuClick, true);
    document.removeEventListener("keydown", rowMenuKey, true);
    window.removeEventListener("blur", closeRowMenu);
    window.removeEventListener("resize", closeRowMenu);
  };
  const rowMenuKey = (e) => {
    if (e.key === "Escape") { e.preventDefault(); closeRowMenu(); return; }
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    e.preventDefault();
    const items = [...rowMenuEl.querySelectorAll("button")], i = items.indexOf(document.activeElement);
    if (e.key === "ArrowRight" && document.activeElement.classList.contains("has-sub")) { document.activeElement.dispatchEvent(new Event("opensub")); if (rowSubEl) rowSubEl.querySelector("button").focus(); return; }
    items[(i + (e.key === "ArrowDown" ? 1 : items.length - 1)) % items.length].focus();
  };
  const rowMenu = (items) => (e) => {
    e.preventDefault(); e.stopPropagation();
    closeRowMenu();
    // "-" between items draws a line between groups
    rowMenuEl = h("div", { class: "cv-menu", role: "menu" }, items.map((it) => {
      if (it === "-") return h("div", { class: "sep", role: "separator" });
      if (Array.isArray(it[1])) {
        const b = h("button", { type: "button", role: "menuitem", class: "has-sub", "aria-haspopup": "true" }, plain(it[0]), h("span", {}, "▸"));
        b.addEventListener("opensub", () => openRowSub(b, it[1]));
        b.addEventListener("mouseenter", () => openRowSub(b, it[1]));
        return b;
      }
      const b = h("button", { type: "button", role: "menuitem", class: it[2] || "", onclick: () => { closeRowMenu(); it[1](); } }, plain(it[0]));
      b.addEventListener("mouseenter", closeRowSub);
      return b;
    }));
    document.body.append(rowMenuEl);
    const r = rowMenuEl.getBoundingClientRect(), b = e.target.getBoundingClientRect ? e.target.getBoundingClientRect() : { left: 0, bottom: 0 };
    const x = e.clientX || b.left, y = e.clientY || b.bottom;
    rowMenuEl.style.left = Math.max(4, Math.min(x, window.innerWidth - r.width - 4)) + "px";
    rowMenuEl.style.top = Math.max(4, Math.min(y, window.innerHeight - r.height - 4)) + "px";
    rowMenuEl.querySelector("button").focus();
    document.addEventListener("click", rowMenuClick, true);
    document.addEventListener("keydown", rowMenuKey, true);
    window.addEventListener("blur", closeRowMenu);
    window.addEventListener("resize", closeRowMenu);
  };
  // ── network tools on an address: right-click ▸ Ping, Traceroute, Scan, Lookup, Whois ──
  // Ping, Traceroute and Scan run on the node picked in the Node menu and are polled until they are done; Lookup and Whois answer at once.
  const netToolTitles = { ping: "Ping", trace: "Traceroute", scan: "Scan", lookup: "Lookup", whois: "Whois" };
  const whoisRows = (w) => [["Name", w.name], ["IPv4", w.ipv4], ["IPv6", w.ipv6], ["Network", w.network], ["Net name", w.netname], ["Organisation", w.org], ["Country", w.country], ["Origin AS", w.origin],
    ["Registrar", w.registrar], ["Registrant", w.registrant], ["Registered", w.registered], ["Updated", w.updated], ["Expires", w.expires], ["Name servers", (w.name_servers || []).join(", ")], ["Server", w.server]].filter((r) => r[1]);
  function netToolDialog(kind, target) {
    let stop = false, timer = 0;
    const out = h("pre", { class: "mono nettool-out" }, "running...");
    const hops = h("div", {});
    const say = (t) => { out.textContent = t; };
    const hopTable = (list) => clear(hops).append(...(list && list.length ? [h("table", { class: "qflows" },
      h("thead", {}, h("tr", {}, ["#", "Hop", "Loss", "Sent", "Last", "Avg", "Best", "Worst"].map((x, i) => h("th", { class: i > 1 ? "num" : "" }, x)))),
      h("tbody", {}, list.map((p, i) => h("tr", {}, h("td", {}, String(i + 1)), h("td", { class: "mono" }, p.name ? p.name + " " : "", h("span", { class: p.name ? "muted small" : "" }, p.addr || "???")),
        h("td", { class: "num" }, (p.loss || 0).toFixed(0) + "%"), h("td", { class: "num" }, String(p.sent)), ...[p.last, p.avg, p.best, p.worst].map((v) => h("td", { class: "num" }, (v || 0).toFixed(1)))))))] : []));
    const poll = async (getPath, startBody, startPath) => {
      try {
        let j = (await api("POST", startPath, startBody)).data;
        while (!stop) {
          if (j.hops) hopTable(j.hops);
          if (j.state === "running") say(j.text || "running...");
          else { say(j.text || (j.state === "done" ? "(no output)" : j.state)); return; }
          await new Promise((r) => { timer = setTimeout(r, 1000); });
          if (stop) return;
          j = (await api("GET", getPath)).data;
        }
      } catch (e) { say("Error: " + e.message); }
    };
    const q = encodeURIComponent(target);
    (async () => {
      try {
        if (kind === "ping") await poll("/api/nettool?kind=ping&client=" + q, { kind: "ping", client: target }, "/api/nettool");
        else if (kind === "trace") await poll("/api/nettool?kind=trace&client=" + q, { kind: "trace", client: target }, "/api/nettool");
        else if (kind === "scan") await poll("/api/scan?client=" + q, { client: target }, "/api/scan");
        else if (kind === "lookup") {
          const r = (await api("GET", "/api/hostlookup?lookup=" + q)).data;
          say(r.found ? (r.kind === "name" ? target + " is " + r.name : target + " is " + r.addr) : r.error || "nothing found");
        } else {
          const w = (await api("GET", "/api/whois?lookup=" + q)).data;
          const rows = whoisRows(w);
          say(rows.length ? rows.map((r) => r[0].padEnd(14) + r[1]).join("\n") : w.note || "no registration data found");
        }
      } catch (e) { say("Error: " + e.message); }
    })();
    const d = dialog(netToolTitles[kind] + " " + target, (close) => {
      const end = () => { stop = true; clearTimeout(timer); close(); };
      return [hops, out, h("div", { class: "toolbar nettool-bar" }, h("button", { class: "btn", type: "button", onclick: end }, "Close"))];
    });
    d.classList.add("nettool");   // the output box scrolls by itself (style.css), so the popup itself keeps no room for a scrollbar
    d.addEventListener("close", () => { stop = true; clearTimeout(timer); });
  }
  // the menu entries for one host; a list of addresses (a conversation) gets a submenu per address
  const netToolItems = (a) => Object.keys(netToolTitles).map((k) => [netToolTitles[k], () => netToolDialog(k, a)]);
  const netToolMenu = (...addrs) => {
    const list = addrs.filter(Boolean);
    return list.length === 1 ? netToolItems(list[0]) : list.map((a) => [a, netToolItems(a)]);
  };
  const when = (t) => (t && !String(t).startsWith("0001") ? new Date(t).toLocaleString() : "–");
  // The cluster's member table: the Cluster page passes a remove handler (right-click a node ▸ Remove), the Monitor page none.
  // The Monitor page (named) has the node's name and its addresses in two columns; the cluster address is the name's tooltip.
  const membersTable = (v, onRemove, named) => {
    const roleOf = (p) => (p.is_primary ? "primary" : (p.role || "replica"));
    const rows = v.peers.map((p) => h("tr", Object.assign({ class: p.self ? "local" : "" }, onRemove && !p.self ? { tabindex: "0", oncontextmenu: rowMenu([["Delete", () => onRemove(p), "danger"]]) } : {}),
      named ? h("td", { class: "nname", title: p.addr }, p.hostname || p.addr, p.self ? " ★" : "", soakMark(p.soak_sec)) : h("td", { class: "mono" }, p.addr, p.self ? " ★" : "", soakMark(p.soak_sec)),
      named ? h("td", { class: "mono" }, (p.ips && p.ips.length) ? p.ips.map((ip) => h("div", {}, ip)) : "–") : null,
      h("td", {}, pill(roleOf(p), p.is_primary ? "ok" : "info")),
      h("td", {}, p.reachable ? pill("yes", "ok") : p.restarting ? pill("restarting", "info") : pill("NO", "bad"), p.updating || p.restarting ? [" ", pill("updating", "warn")] : null),
      h("td", { class: "num" }, p.reachable ? p.epoch : "–"),
      h("td", { class: "num" }, p.version || "–"), h("td", { class: "num" }, p.source_version || "–"),
      h("td", {}, p.self ? "now" : when(p.last_seen)),
      h("td", { class: "wrap muted" }, p.error || "")));
    return h("div", { class: "scroll" }, h("table", { class: named ? "tight" : null },
      h("thead", {}, h("tr", {}, (named ? ["Node name", "Node IP"] : ["Node"]).concat(["Role", "Reachable", "Epoch", "Running", "Source", "Last seen", ""]).map((t, i) => h("th", { class: i >= (named ? 4 : 3) && i <= (named ? 6 : 5) ? "num" : "" }, t)))),
      h("tbody", {}, rows)));
  };
  const say = (el, kind, ...msg) => kind !== "bad" ? clear(el) : clear(el).append(h("div", { class: "notice " + kind, role: kind === "bad" ? "alert" : "status" }, ...msg));
  const fail = (el) => (e) => { if (e.message !== "unauthenticated") say(el, "bad", e.message); };
  const kv = (rows) => h("dl", { class: "kv" }, rows.filter(Boolean).map(([k, v]) => [h("dt", {}, k), h("dd", {}, v)]));
  const section = (title, ...kids) => h("div", { class: "card" }, h("header", {}, h("h2", {}, tc(title))), h("div", { class: "body" }, ...kids));
  const copyBtn = (getText) => h("button", { class: "btn small", type: "button", onclick: async (ev) => {
    try { await navigator.clipboard.writeText(getText()); ev.target.textContent = "Copied"; setTimeout(() => { ev.target.textContent = "Copy"; }, 1500); }
    catch (_) { ev.target.textContent = "Select and copy manually"; }
  } }, "Copy");
  function readFileInto(input, target) {
    input.addEventListener("change", () => {
      const f = input.files && input.files[0];
      if (!f) return;
      const rd = new FileReader();
      rd.onload = () => { target.value = String(rd.result); };
      rd.readAsText(f);
    });
  }
  // Any answer as plain text instead of JSON: a list of flat records is a table, a record is "name  value" lines, a nested one is indented.
  function plainText(v) {
    const scalar = (x) => x == null || typeof x !== "object";
    const word = (k) => { k = String(k).replace(/[_-]+/g, " "); return k.charAt(0).toUpperCase() + k.slice(1); };
    const cell = (x) => (x == null ? "" : Array.isArray(x) ? x.map(cell).join(", ") : typeof x === "object" ? Object.entries(x).map((e) => e[0] + "=" + cell(e[1])).join(" ") : String(x));
    const table = (rows, cols) => {
      const grid = [cols.map(word)].concat(rows.map((r) => cols.map((c) => cell(r[c]))));
      const w = cols.map((_, i) => Math.max(...grid.map((r) => r[i].length)));
      return grid.map((r) => r.map((c, i) => c.padEnd(w[i])).join("  ").trimEnd()).join("\n");
    };
    const flat = (o) => o && typeof o === "object" && !Array.isArray(o) && Object.values(o).every((x) => scalar(x) || (Array.isArray(x) && x.every(scalar)));
    const ind = (t, p) => t.split("\n").map((l) => (l ? p + l : l)).join("\n");
    function go(x) {
      if (x == null) return "";
      if (scalar(x)) return String(x);
      if (Array.isArray(x)) {
        if (!x.length) return "None.";
        if (x.every(scalar)) return x.join("\n");
        if (x.every(flat)) {
          const cols = [];
          x.forEach((r) => Object.keys(r).forEach((k) => { if (!cols.includes(k)) cols.push(k); }));
          return table(x, cols);
        }
        return x.map(go).join("\n\n");
      }
      const keys = Object.keys(x).filter((k) => x[k] != null && x[k] !== "");
      if (!keys.length) return "None.";
      const w = Math.max(...keys.filter((k) => scalar(x[k]) || (Array.isArray(x[k]) && x[k].every(scalar))).map((k) => word(k).length), 0) + 2;
      return keys.map((k) => {
        const e = x[k];
        if (scalar(e) || (Array.isArray(e) && e.every(scalar))) return word(k).padEnd(w) + cell(e);
        return word(k) + "\n" + ind(go(e), "  ");
      }).join("\n");
    }
    return go(v);
  }
  function saveText(name, text) {
    const a = h("a", { href: URL.createObjectURL(new Blob([text], { type: "text/plain" })), download: name });
    document.body.append(a); a.click(); a.remove();
  }
  // The small download icon on the cards of Monitor ▸ MCP and Monitor ▸ Node: the card's numbers as a CSV file, built in the
  // browser from what the page already holds.  rows() runs at the click (the first row is the header).  A text cell that starts with
  // = + - or @ is written with a leading apostrophe, so a spreadsheet cannot read a name that came from the network as a formula.
  const csvCell = (v) => {
    if (v == null) return "";
    let s = String(v);
    if (typeof v !== "number" && /^[=+\-@\t\r]/.test(s)) s = "'" + s;
    return /[",\r\n]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s;
  };
  const csvIso = (sec) => new Date(sec * 1000).toISOString();
  const csvNum = (v) => (typeof v === "number" && isFinite(v) ? Math.round(v * 1000) / 1000 : "");
  function saveCsv(name, rows) {
    const url = URL.createObjectURL(new Blob([rows.map((r) => r.map(csvCell).join(",")).join("\r\n") + "\r\n"], { type: "text/csv;charset=utf-8" }));
    const a = h("a", { href: url, download: name });
    document.body.append(a); a.click(); a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 10000);
  }
  function csvButton(title, rows) {
    const ns = "http://www.w3.org/2000/svg", svg = document.createElementNS(ns, "svg"), p = document.createElementNS(ns, "path");
    for (const [k, v] of Object.entries({ viewBox: "0 0 24 24", "aria-hidden": "true", fill: "none", stroke: "currentColor", "stroke-width": "2", "stroke-linecap": "round", "stroke-linejoin": "round" })) svg.setAttribute(k, v);
    p.setAttribute("d", "M12 3v12m0 0l-4.5-4.5M12 15l4.5-4.5M4 20h16");
    svg.append(p);
    const file = "logdir-" + title.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "") + ".csv";
    return h("button", { type: "button", class: "btn small csv", title: "Download as CSV", "aria-label": "Download " + title + " as CSV", onclick: () => saveCsv(file, rows()) }, svg);
  }
  // Expiry warning under the tabs (certificate state is cheap to check once per login).
  async function certBanner() {
    const slot = document.getElementById("banner");
    if (!slot) return;
    try {
      const c = (await api("GET", "/api/tls")).data;
      clear(slot);
      if (c.expired) slot.append(h("div", { class: "notice bad", role: "alert" }, "The GUI certificate has expired. Install a new one under Configure ▸ Web GUI."));
    } catch (_) { /* not critical */ }
  }

  // ── History  (CLI: --versions, --version-show/-diff/-snapshot/-restore/-export, --config-import) ──
  VIEWS.history = (() => {
    let tbody, panel, status, picked = [], list = [], sel = null, actBtns = [];
    const actor = (v) => v.actor + (v.manual ? " · manual" : "");

    // The actions work on the highlighted row, so the table has no buttons of its own.
    function select(id) {
      sel = id;
      for (const tr of tbody.querySelectorAll("tr[data-id]")) tr.classList.toggle("sel", tr.dataset.id === id);
      for (const b of actBtns) b.disabled = !b.dataset.always && (id === null || (b.dataset.needs === "previous" && !previous()));
    }
    const current = () => list.find((v) => v.id === sel) || null;
    const previous = () => { const i = list.findIndex((v) => v.id === sel); return i >= 0 && i + 1 < list.length ? list[i + 1] : null; };
    function download(id) {
      const a = h("a", { href: route("/api/versions/download?id=" + encodeURIComponent(id)), download: "" });
      document.body.append(a); a.click(); a.remove();
    }

    function drawDiff(d) {
      const rows = (d.lines || []).map((l) => h("tr", { class: "dl " + ({ "+": "add", "-": "del", "@": "skip" }[l.op] || "ctx") },
        h("td", { class: "op" }, l.op === "@" ? "" : l.op), h("td", { class: "mono ln" }, l.op === "@" ? "… " + l.text : l.text)));
      clear(panel).append(h("div", { class: "card" },
        h("header", {}, h("h2", {}, tc("Changes")), h("span", { class: "mono muted" }, d.a.id + " → " + d.b.id),
          h("span", { class: "spacer" }), h("button", { class: "btn small", type: "button", onclick: () => clear(panel) }, "Close")),
        h("div", { class: "body" },
          d.same ? h("div", { class: "empty" }, "The two versions are identical.") : [
            h("ul", { class: "changes" }, (d.sections || []).map((s) => h("li", {}, h("strong", {}, s.label + ": "), s.detail))),
            h("div", { class: "scroll" }, h("table", { class: "diff" }, h("tbody", {}, rows)))])));
      panel.scrollIntoView({ block: "nearest" });
    }
    async function diff(a, b) {
      try { drawDiff((await api("GET", "/api/versions/diff?a=" + encodeURIComponent(a) + "&b=" + encodeURIComponent(b))).data); }
      catch (e) { fail(status)(e); }
    }
    async function view(id) {
      try {
        const r = (await api("GET", "/api/versions/get?id=" + encodeURIComponent(id))).data;
        clear(panel).append(h("div", { class: "card" },
          h("header", {}, h("h2", {}, "Version " + id), h("span", { class: "muted" }, when(r.meta.at) + " · " + r.meta.actor),
            h("span", { class: "spacer" }), h("button", { class: "btn small", type: "button", onclick: () => clear(panel) }, "Close")),
          h("div", { class: "body" }, h("pre", { class: "help" }, JSON.stringify(r.config, null, 2)))));
      } catch (e) { fail(status)(e); }
    }
    async function restore(v) {
      if (!confirm("Restore version " + v.id + " (" + when(v.at) + ") and apply it now?\n\nThe current configuration is saved as a new version first, so you can come back.")) return;
      try { await api("POST", "/api/versions/restore", { id: v.id }); say(status, "ok", "Version " + v.id + " restored and applied."); await load(); }
      catch (e) { fail(status)(e); }
    }
    async function load() {
      list = (await api("GET", "/api/versions")).data;
      picked = picked.filter((id) => list.some((v) => v.id === id));
      if (sel !== null && !list.some((v) => v.id === sel)) sel = null;
      clear(tbody);
      if (!list.length) { tbody.append(h("tr", {}, h("td", { colspan: 4, class: "empty" }, "Nothing recorded yet. A version is saved every time the configuration changes."))); select(null); return; }
      list.forEach((v, i) => {
        const cb = h("input", { type: "checkbox", "aria-label": "Compare version " + v.id, checked: picked.includes(v.id), onchange: () => {
          picked = cb.checked ? [...picked, v.id].slice(-2) : picked.filter((x) => x !== v.id);
          for (const o of tbody.querySelectorAll("input[type=checkbox]")) o.checked = picked.includes(o.dataset.id);
        } });
        cb.dataset.id = v.id;
        const tr = h("tr", { "data-id": v.id, tabindex: "0", "aria-label": "Version " + v.id },
          h("td", {}, cb),
          h("td", { class: "nowrap" }, when(v.at), i === 0 ? [" ", pill("latest", "info")] : null),
          h("td", {}, actor(v)),
          h("td", { class: "wrap" }, v.summary, v.note ? h("div", { class: "muted" }, "“" + v.note + "”") : null));
        tr.addEventListener("click", () => select(v.id));
        tr.addEventListener("keydown", (e) => { if (e.key === "Enter" || e.key === " ") { if (e.target === tr) { e.preventDefault(); select(v.id); } } });
        tbody.append(tr);
      });
      select(sel);
    }
    return {
      mount(main) {
        status = h("div", { "aria-live": "polite" });
        panel = h("div", {});
        tbody = h("tbody", {});
        const file = h("input", { type: "file", accept: ".json,application/json", class: "hidden", "aria-label": "Configuration file" });
        file.addEventListener("change", async () => {
          const f = file.files[0]; file.value = "";
          if (!f) return;
          let cfg;
          try { cfg = JSON.parse(await f.text()); } catch (e) { say(status, "bad", "Not valid JSON: " + e.message); return; }
          if (!confirm("Replace the whole configuration with " + f.name + " and apply it now?")) return;
          try { await api("POST", "/api/versions/upload", { config: cfg, note: "uploaded " + f.name }); say(status, "ok", "Configuration from " + f.name + " applied."); await load(); }
          catch (e) { fail(status)(e); }
        });
        const act = (label, needs, fn) => { const b = h("button", { class: "btn small", type: "button", disabled: true, onclick: fn }, label); if (needs) b.dataset.needs = needs; return b; };
        actBtns = [
          h("button", { class: "btn small", type: "button", "data-always": "1", title: "Record the current configuration as a version now", onclick: async () => {
            try { await api("POST", "/api/versions/snapshot", {}); say(status, "ok", "Snapshot saved."); await load(); } catch (e) { fail(status)(e); }
          } }, "Snapshot now"),
          act("View", null, () => { if (sel) view(sel); }),
          act("Diff vs live", null, () => { if (sel) diff(sel, "current"); }),
          act("Diff vs previous", "previous", () => { const p = previous(); if (p) diff(p.id, sel); }),
          h("button", { class: "btn small", type: "button", "data-always": "1", title: "Tick two versions to compare them", onclick: () => { if (picked.length === 2) diff(picked[1], picked[0]); else say(status, "info", "Tick two versions to compare them."); } }, "Compare ticked"),
          act("Restore", null, () => { const v = current(); if (v) restore(v); }),
          act("Download", null, () => { if (sel) download(sel); }),
          h("button", { class: "btn small", type: "button", "data-always": "1", onclick: () => file.click() }, "Upload"),
        ];
        sel = null;
        main.append(
          file,
          status,
          // the buttons belong to the list: they act on the highlighted version
          h("div", { class: "card" },
            h("header", { class: "bar", role: "group", "aria-label": "Versions" }, ...actBtns),
            h("div", { class: "scroll" }, h("table", {},
              h("thead", {}, h("tr", {}, ["", "When", "Who", "What changed"].map((t) => h("th", {}, t)))), tbody))),
          panel);
        load().catch(fail(status));
      },
    };
  })();

  // ── Certificate  (CLI: --tls-status/-install/-csr/-revert/-regenerate) ─────
  VIEWS.certificate = (() => {
    let info, status, csrBox, tools;
    const sourceLabel = { files: "Files set in the configuration file", installed: "Uploaded on this page", cluster: "Copied from the cluster's main node", "self-signed": "Created automatically by logdir" };
    const fold = (title, ...kids) => h("details", { class: "card fold" }, h("summary", {}, title), h("div", { class: "body" }, ...kids));

    function drawInfo(c) {
      if (tools) tools.classList.toggle("hidden", c.source === "files");
      const left = c.expired ? pill("Expired", "bad") : pill(c.days_left + " days left", c.expires_soon ? "warn" : "ok");
      clear(info).append(h("div", { class: "card" },
        h("header", {}, h("h2", {}, tc("Current certificate")), pill(c.source === "self-signed" ? "Not trusted by browsers" : "Custom", c.source === "self-signed" ? "warn" : "ok"), c.error ? pill("problem", "bad") : left),
        h("div", { class: "body" }, c.error ? h("div", { class: "notice bad" }, c.error) : kv([
          ["Where it comes from", sourceLabel[c.source] || c.source],
          ["Issued to", c.subject], ["Issued by", c.issuer + (c.self_signed ? " (itself)" : "")],
          ["Valid for", [...new Set([...c.dns_names, ...c.ip_addresses])].join(", ") || "–"],
          ["Valid", when(c.not_before) + " → " + when(c.not_after)],
          ["SHA-256", h("span", { class: "mono wrapall" }, c.fingerprint)],
          c.source === "files" && c.cert_file ? ["Files", h("span", { class: "mono" }, c.cert_file + ", " + c.key_file)] : null,
          c.installed_by ? ["Installed", "by " + c.installed_by + " at " + when(c.installed_at)] : null,
          ["Installed certificates in chain", String(c.chain_length)]]),
          c.source === "self-signed" ? h("p", { class: "hint" }, "Your browser shows a security warning for this certificate. Upload one from your own certificate authority below to get rid of it.") : null,
          c.source === "files" ? h("p", { class: "hint" }, "This certificate comes from files named in the configuration file, which override anything uploaded here. To manage it on this page instead, remove web.cert_file and web.key_file from the configuration file.") : null)));
      clear(csrBox);
      if (c.pending_csr) {
        const ta = h("textarea", { rows: 8, readonly: true, class: "pem", "aria-label": "Pending CSR" });
        ta.value = c.pending_csr;
        csrBox.append(
          ta, h("div", { class: "toolbar" }, copyBtn(() => ta.value),
            h("button", { class: "btn small", type: "button", onclick: () => saveText("logdir.csr", ta.value) }, "Download"),
            h("button", { class: "btn small", type: "button", onclick: async () => {
              if (!confirm("Throw away the pending request?")) return;
              try { await api("POST", "/api/tls/csr/cancel", {}); await load(); } catch (e) { fail(status)(e); }
            } }, "Discard")));
      }
    }
    async function load() { drawInfo((await api("GET", "/api/tls")).data); }

    return {
      mount(main) {
        status = h("div", { "aria-live": "polite" });
        info = h("div", {});
        csrBox = h("div", {});
        const cert = h("textarea", { rows: 7, spellcheck: "false", class: "pem", placeholder: "-----BEGIN CERTIFICATE-----  (your certificate first, then any intermediate ones)", "aria-label": "Certificate PEM" });
        const key = h("textarea", { rows: 7, spellcheck: "false", class: "pem", placeholder: "-----BEGIN PRIVATE KEY-----  (leave empty if you created a request below)", "aria-label": "Private key PEM", autocomplete: "off" });
        const certFile = h("input", { type: "file", accept: ".pem,.crt,.cer,.txt", "aria-label": "Certificate file" });
        const keyFile = h("input", { type: "file", accept: ".pem,.key,.txt", "aria-label": "Key file" });
        readFileInto(certFile, cert); readFileInto(keyFile, key);
        const cn = h("input", { type: "text", placeholder: "gw.example.com", autocomplete: "off", "aria-label": "Common name" });
        const names = h("textarea", { rows: 3, spellcheck: "false", placeholder: "gw2.example.com", "aria-label": "Other names" });
        const ips = h("textarea", { rows: 2, spellcheck: "false", placeholder: "10.0.0.5", "aria-label": "IP addresses" });
        const lines = (t) => t.value.split("\n").map((s) => s.trim()).filter(Boolean);
        tools = h("div", {},
          section("Upload your own certificate",
            h("div", { class: "grid2" },
              h("label", { class: "f" }, h("span", {}, "Certificate", sharedTag({ shared: true })), cert, certFile),
              h("label", { class: "f" }, h("span", {}, "Private key", sharedTag({ shared: true })), key, keyFile)),
            h("div", { class: "toolbar" }, h("button", { class: "btn primary", type: "button", onclick: async (ev) => {
              ev.target.disabled = true;
              try {
                const r = (await api("POST", "/api/tls/install", { cert_pem: cert.value, key_pem: key.value })).data;
                cert.value = ""; key.value = "";
                say(status, r.warnings && r.warnings.length ? "warn" : "ok", "Certificate installed. ", ...(r.warnings || []).map((w) => h("div", {}, "• " + w)));
                drawInfo(r.info);
              } catch (e) { fail(status)(e); } finally { ev.target.disabled = false; }
            } }, "Use this certificate"))),
          fold("Don't have a certificate yet? Create a request for one",
            h("p", { class: "hint" }, "Your certificate authority needs a request first. Fill this in and send the result to them. The private key is created and kept on this server; when the signed certificate comes back, upload it above without a key."),
            h("div", { class: "grid" }, h("label", { class: "f" }, "Main name", cn), h("label", { class: "f wide" }, "Other names (one per line)", names), h("label", { class: "f wide" }, "IP addresses (one per line, optional)", ips)),
            h("div", { class: "toolbar" }, h("button", { class: "btn", type: "button", onclick: async () => {
              try { await api("POST", "/api/tls/csr", { cn: cn.value.trim(), dns: lines(names), ips: lines(ips) }); say(status, "ok", "Request created."); await load(); } catch (e) { fail(status)(e); }
            } }, "Create request")), csrBox),
          fold("Go back to the automatic certificate",
            h("p", { class: "hint" }, "LogDirector can make its own certificate. Browsers do not trust it, but connections are still encrypted."),
            h("div", { class: "toolbar" },
              h("button", { class: "btn warn", type: "button", onclick: async () => {
                if (!confirm("Stop using the uploaded certificate and go back to the automatic one?")) return;
                try { await api("POST", "/api/tls/revert", {}); say(status, "ok", "Back to the automatic certificate."); await load(); } catch (e) { fail(status)(e); }
              } }, "Stop using the uploaded certificate"),
              h("button", { class: "btn", type: "button", onclick: async () => {
                try { await api("POST", "/api/tls/regenerate", {}); say(status, "ok", "A fresh automatic certificate was created."); await load(); } catch (e) { fail(status)(e); }
              } }, "Make a fresh automatic certificate"))));
        main.append(status, info, tools);
        load().catch(fail(status));
      },
    };
  })();

  // ── Cluster  (CLI: --cluster-status/-token/-join/-promote/-remove/-unremove/-leave/-sync) ──
  VIEWS.cluster = (() => {
    let dyn, status, tokenBox, joinBox, clearBtn;

    async function act(path, body, confirmText, okText) {
      if (confirmText && !confirm(confirmText)) return;
      try { await api("POST", path, body || {}); say(status, "ok", okText); await VIEWS.cluster.poll(); } catch (e) { fail(status)(e); }
    }

    function draw(v) { const real = dyn; dyn = h("div", {}); try { build(v); } finally { morph(real, dyn); dyn = real; } }
    // built into a detached copy and morphed in: the page keeps its elements (and so its scroll boxes) from one poll to the next
    function build(v) {
      if (v.conflict) dyn.append(h("div", { class: "notice bad", role: "alert" }, "Conflict: " + v.conflict));
      for (const w of v.warnings) dyn.append(h("div", { class: "notice bad", role: "alert" }, w));
      if (v.last_sync_error) dyn.append(h("div", { class: "notice bad", role: "alert" }, "Last sync failed: " + v.last_sync_error));
      const stat = (k, val, small) => h("div", { class: "stat" }, h("div", { class: "k" }, k), h("div", { class: "v" + (small ? " small" : "") }, val));
      dyn.append(h("div", { class: "stats" },
        stat("This node", v.self, true), stat("Role", v.role.toUpperCase()), stat("Epoch", v.epoch),
        stat("Primary", v.primary_addr, true), stat("Shared settings rev.", v.shared_rev)));
      dyn.append(h("div", { class: "card" },
        h("header", {}, h("h2", {}, tc("Members"))),
        membersTable(v, (p) => act("/api/cluster/peers/delete", { addr: p.addr }, "Delete " + p.addr + " from the cluster? Nothing is kept about it. A copy that is still running resets itself to a single-node cluster.", p.addr + " deleted."))));
      if (v.removed.length) dyn.append(h("div", { class: "card" }, h("header", {}, h("h2", {}, tc("Removed nodes"))), h("div", { class: "body" },
        h("p", { class: "hint" }, "Nodes removed from the cluster before (on the command line, with --cluster-remove). They cannot be re-added by gossip. Delete one to forget it entirely."),
        v.removed.map((a) => h("div", { class: "toolbar" }, h("span", { class: "mono" }, a),
          h("button", { class: "btn small", type: "button", onclick: () => act("/api/cluster/peers/delete", { addr: a }, null, a + " deleted.") }, "Delete"))))));
      // join form only makes sense for an untouched node
      joinBox.classList.toggle("hidden", !v.joinable);
      leaveBtn.classList.toggle("hidden", v.joinable);
      promoteBtn.classList.toggle("hidden", v.role === "primary");
      clearBtn.disabled = !v.join_codes;
      clearBtn.title = v.join_codes ? v.join_codes + " unused" : "No unused join codes";
      lastView = v;
    }
    let leaveBtn, promoteBtn, lastView = null;

    return {
      mount(main) {
        status = h("div", { "aria-live": "polite" });
        dyn = h("div", {});
        tokenBox = h("div", {});
        const code = h("textarea", { rows: 3, spellcheck: "false", class: "pem", placeholder: "logdir-join-v1:…", "aria-label": "Join code" });
        joinBox = section("Join an existing cluster",
          h("p", { class: "hint" }, "Paste a join code created on a member (“Create join code” there). This node's shared settings — the settings block, VIPs, keys and timers — are replaced by the cluster's; its interface, priority, weight, preemption and neighbours stay its own. A snapshot of the current configuration is saved first."),
          code, h("div", { class: "toolbar" }, h("button", { class: "btn primary", type: "button", onclick: async (ev) => {
            if (!confirm("Join this node to the cluster? Its shared settings will be replaced by the cluster's.")) return;
            ev.target.disabled = true;
            try { await api("POST", "/api/cluster/join", { code: code.value.trim() }); code.value = ""; say(status, "ok", "Joined the cluster."); await VIEWS.cluster.poll(); }
            catch (e) { fail(status)(e); } finally { ev.target.disabled = false; }
          } }, "Join cluster")));
        leaveBtn = h("button", { class: "btn danger", type: "button", onclick: () => act("/api/cluster/leave", {}, "Leave the cluster? This node keeps its settings and becomes its own single-node cluster.", "Left the cluster.") }, "Leave cluster");
        // withdraws the join codes this node handed out that nobody used (there can be 20 at most); the code shown above goes with them
        clearBtn = h("button", { class: "btn", type: "button", disabled: true, onclick: async () => {
          if (!confirm("Clear this node's unused join codes?\n\nA code created before no longer works.")) return;
          try { await api("POST", "/api/cluster/token/clear", {}); clear(tokenBox); clear(status); await VIEWS.cluster.poll(); } catch (e) { fail(status)(e); }
        } }, "Clear join codes");
        promoteBtn = h("button", { class: "btn warn", type: "button", onclick: () => act("/api/cluster/promote", {}, "Promote THIS node to primary?\n\nDo this only if the current primary is gone for good (or you are moving the role on purpose). Every other member follows the new primary.", "This node is now the primary.") }, "Promote this node");
        main.append(status,
          h("div", { class: "toolbar" },
            h("button", { class: "btn", type: "button", onclick: async () => {
              try {
                const r = (await api("POST", "/api/cluster/token", {})).data;
                const ta = h("textarea", { rows: 4, readonly: true, class: "pem", "aria-label": "Join code" }); ta.value = r.code;
                clear(tokenBox).append(h("div", { class: "card" }, h("header", {}, h("h2", {}, tc("Join code")), h("span", { class: "muted" }, "single use, valid until " + when(r.expires))),
                  h("div", { class: "body" }, ta, h("div", { class: "toolbar" }, copyBtn(() => ta.value),
                    h("span", { class: "hint" }, "Paste it on the new node (Cluster → Join) or run: logdir --cluster-join '<code>'")))));
                VIEWS.cluster.poll().catch(() => {});   // Clear join codes counts the new one at once
              } catch (e) { fail(status)(e); }
            } }, "Create join code"),
            clearBtn,
            h("button", { class: "btn", type: "button", onclick: () => act("/api/cluster/sync", {}, null, "Synced.") }, "Sync now"),
            h("span", { class: "spacer" }), promoteBtn, leaveBtn),
          tokenBox, dyn, h("p", { class: "hint" }, "★ marks this node"), joinBox);
        joinBox.classList.add("hidden"); leaveBtn.classList.add("hidden"); promoteBtn.classList.add("hidden");
      },
      async poll() { draw((await api("GET", "/api/cluster")).data); },
    };
  })();

  // ── Upgrade  (CLI: --update-status/-upload/-apply/-push/-cancel/-auto/-history) ──
  VIEWS.updates = (() => {
    let dyn, status, stats, selected = new Set(), histPage = 0;
    const HIST_PAGE = 25;
    const stat = (k, val, small) => h("div", { class: "stat" }, h("div", { class: "k" }, k), h("div", { class: "v" + (small ? " small" : "") }, val));

    async function act(path, body, confirmText, okText) {
      if (confirmText && !confirm(confirmText)) return;
      try { await api("POST", path, body || {}); say(status, "ok", okText); await VIEWS.updates.poll(); } catch (e) {
        // updating this node now would leave a gateway unserved: say so and let the admin decide
        if (path === "/api/update/apply" && /^not safe to update/.test(e.message || "") && confirm(e.message.replace(/ \(to update anyway.*$/, "") + "\n\nUpdate anyway? Clients of that gateway will be interrupted.")) {
          try { await api("POST", path, { force: true }); say(status, "ok", okText); await VIEWS.updates.poll(); } catch (e2) { fail(status)(e2); }
          return;
        }
        fail(status)(e);
      }
    }

    function draw(v) { const real = dyn; dyn = h("div", {}); try { build(v); } finally { morph(real, dyn); dyn = real; } }
    // built into a detached copy and morphed in: the page keeps its elements (and so its scroll boxes) from one poll to the next
    function build(v) {
      if (!v.toolchain) dyn.append(h("div", { class: "notice bad", role: "alert" }, "No Go toolchain (≥ 1.24) was found on this node, so it cannot build updates itself. Re-run install.sh, which keeps one under /usr/local/share/logdir/go."));
      const newer = v.source_version && Number(v.source_version) > Number(v.running);
      fill(stats, 
        stat("Running", "v" + v.running), stat("Staged source", v.source_version ? "v" + v.source_version : "none"),
        stat("Auto-update", v.intent.auto_all ? "ON" : "off"),
        stat("This node", v.busy ? (v.phase || "working…") : newer ? "update available" : "up to date", true));
      const addrs = new Set(v.nodes.map((n) => n.addr));
      for (const a of [...selected]) if (!addrs.has(a)) selected.delete(a);
      const picked = v.clustered ? [...selected] : [];
      const go = h("button", { class: "btn primary hdr", type: "button", disabled: v.intent.auto_all || !newer || v.busy || !v.toolchain, title: v.intent.auto_all ? "Automatic updates are on: every node updates itself. Turn them off in Configure ▸ General ▸ Updates to update by hand." : null, onclick: () => picked.length
        ? act("/api/update/push", { nodes: picked }, "Update " + picked.length + " selected node" + (picked.length === 1 ? "" : "s") + " to v" + v.source_version + "?\n\nThey build and restart one at a time.", "Queued " + picked.length + " node(s).")
        : act("/api/update/apply", {}, "Build v" + v.source_version + " on this node and restart into it?\n\nThe node is briefly unavailable. If the new version fails to stay up it is rolled back automatically.", "Building v" + v.source_version + " — this node restarts when it is done.") },
        picked.length ? "Update " + picked.length + " selected" : "Update this node now");
      const rows = v.nodes.map((n) => {
        const state = n.updating || n.restarting ? pill("updating", "warn") : n.failed ? pill("failed v" + n.failed, "bad") : n.behind ? pill("behind", "warn") : pill("ok", "ok");
        const cb = h("td", {}, h("input", { type: "checkbox", checked: selected.has(n.addr), disabled: v.intent.auto_all, "aria-label": "Select " + n.addr, onchange: (ev) => { if (ev.target.checked) selected.add(n.addr); else selected.delete(n.addr); VIEWS.updates.poll(); } }));
        return h("tr", { class: n.self ? "local" : "" }, v.clustered ? cb : null,
          h("td", { class: "mono" }, n.addr, n.self ? " ★" : "", soakMark(n.soak_sec)),
          h("td", {}, n.running || "–"), h("td", {}, n.source || "–"),
          h("td", {}, n.reachable ? pill("yes", "ok") : n.restarting ? pill("restarting", "info") : pill("NO", "bad")),
          h("td", {}, n.queued ? pill(v.intent.auto_all ? "auto" : "queued", "info") : ""), h("td", {}, state));
      });
      const heads = (v.clustered ? [""] : []).concat(["Node", "Running", "Source", "Reachable", "Queued", "State"]);
      const off = v.clustered ? 1 : 0;
      dyn.append(h("div", { class: "card" }, h("header", { class: "bar" }, h("h2", {}, tc("Nodes")), h("span", { class: "grow" }), go),
        h("div", { class: "scroll" }, h("table", { class: "evencols" }, h("thead", {}, h("tr", {}, heads.map((t, i) => h("th", { class: t === "Node" ? "w-node" : t === "Running" || t === "Source" ? "w-ver" : t === "" ? "w-cb" : "w-pill" }, t)))), h("tbody", {}, rows)))));
      const row = (e) => h("tr", {}, h("td", {}, when(e.at)), h("td", { class: "mono" }, e.node), h("td", {}, pill(e.kind, e.kind === "failed" || e.kind === "rolled-back" ? "bad" : e.kind === "applied" ? "ok" : "info")),
        h("td", { class: "num" }, (e.from ? e.from + " → " : "") + (e.to || "")), h("td", { class: "wrap" }, [e.detail, e.by ? " (by " + e.by + ")" : ""].join("")));
      const pages = Math.max(1, Math.ceil(v.history.length / HIST_PAGE));
      histPage = Math.min(Math.max(histPage, 0), pages - 1);   // newest first; page 0 is the newest
      const shown = v.history.slice(histPage * HIST_PAGE, (histPage + 1) * HIST_PAGE);
      const go2 = (d) => () => { histPage += d; VIEWS.updates.poll(); };
      const pager = pages > 1 ? h("span", { class: "pager" },
        h("span", { class: "muted small" }, "Events " + (histPage * HIST_PAGE + 1) + "–" + (histPage * HIST_PAGE + shown.length) + " of " + v.history.length),
        h("button", { class: "btn small", type: "button", disabled: histPage === 0, onclick: go2(-1) }, "‹ Newer"),
        h("span", { class: "muted small" }, "Page " + (histPage + 1) + " of " + pages),
        h("button", { class: "btn small", type: "button", disabled: histPage >= pages - 1, onclick: go2(1) }, "Older ›")) : null;
      dyn.append(h("div", { class: "card" }, h("header", { class: "bar" }, h("h2", {}, tc("History")), h("span", { class: "grow" }), pager),
        shown.length ? h("div", { class: "scroll" }, h("table", {}, h("thead", {}, h("tr", {}, ["When", "Node", "Event", "Version", "Detail"].map((t) => h("th", {}, t)))), h("tbody", {}, shown.map(row))))
          : h("div", { class: "empty" }, "No update activity yet.")));
    }

    return {
      mount(main) {
        status = h("div", { "aria-live": "polite" });
        dyn = h("div", {});
        stats = h("div", { class: "stats" });
        const file = h("input", { type: "file", accept: ".tgz,.tar.gz,.zip,application/gzip,application/zip", "aria-label": "Release archive", disabled: true });
        const note = h("span", { class: "muted small", role: "status", "aria-live": "polite" });
        const online = h("input", { type: "checkbox", checked: true, "aria-label": "Fetch from online", title: "Download the newest release from GitHub instead of using a file", onchange: () => { file.disabled = online.checked; note.textContent = ""; } });
        const up = h("button", { class: "btn primary hdr", type: "button", onclick: async (ev) => {
          const f = file.files[0];
          if (!online.checked && !f) { note.textContent = "Choose a release archive first, or tick Fetch from online."; return; }
          note.textContent = ""; note.classList.remove("bad-text"); clear(status);
          ev.target.disabled = true;
          try {
            if (online.checked) {
              note.textContent = "Downloading…";
              const r = await api("POST", "/api/update/fetch");
              note.textContent = "Staged v" + r.data.version + ".";
            } else {
              const r = await apiUpload("/api/update/upload", f); file.value = ""; note.textContent = "Staged v" + r.data.version + ".";
            }
            await VIEWS.updates.poll();
          } catch (e) { if (e.message !== "unauthenticated") { note.textContent = e.message; note.classList.add("bad-text"); } } finally { ev.target.disabled = false; }
        } }, "Upload");
        main.append(stats, status,
          h("div", { class: "card" }, h("header", { class: "bar" }, h("h2", {}, tc("Upload a release")), h("span", { class: "grow" }), up),
            h("div", { class: "body" },
              h("p", { class: "hint" }, "Select a release archive. It is checked and staged on this node; other cluster members pull it from here."),
              h("div", { class: "toolbar tight" }, file, h("label", { class: "check" }, online, " Fetch from online"), note))),
          dyn);
      },
      async poll() { draw((await api("GET", "/api/update")).data); },
      poll_ms: 2000,
    };
  })();

  // ── Node, Monitor  (CLI: --host) ─────────────────────────────────────────────
  // CPU, memory, disk and network use of the machine, sampled every 10 s by the daemon and
  // kept in memory for 30 days.  The picked node (top-bar Node menu) is the one shown.
  const senderPick = { sel: null };   // the sender the Senders page's Top senders chart is narrowed to (null: all)
  const QCHARTS = {};   // the Node page's line chart, which the Senders page draws its charts with
  VIEWS.host = (() => {
    const SVGNS = "http://www.w3.org/2000/svg";
    const sv = (tag, attrs, ...kids) => {
      const el = document.createElementNS(SVGNS, tag);
      for (const [k, v] of Object.entries(attrs || {})) {
        if (v == null || v === false) continue;
        if (k.startsWith("on")) el.addEventListener(k.slice(2), v); else el.setAttribute(k, v);
      }
      for (const kid of kids.flat(Infinity)) if (kid != null) el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
      return el;
    };
    const RANGES = [["1h", "Last Hour"], ["1d", "Last Day"], ["7d", "Last Week"], ["30d", "Last Month"], ["custom", "Custom"]];
    const REFRESH_MS = 10000;
    const pc = (v) => (v == null || v < 0 ? "–" : v.toFixed(v < 10 ? 1 : 0) + "%");
    const bits = (v) => (v == null || v < 0 ? "–" : v >= 1e9 ? (v / 1e9).toFixed(2) + " Gb/s" : v >= 1e6 ? (v / 1e6).toFixed(v >= 1e8 ? 0 : 2) + " Mb/s" : v >= 1e3 ? (v / 1e3).toFixed(v >= 1e5 ? 0 : 1) + " kb/s" : Math.round(v) + " b/s");
    const bytes = (v) => { let f = Number(v) || 0; for (const u of ["B", "KiB", "MiB", "GiB", "TiB"]) { if (f < 1024 || u === "TiB") return (u === "B" ? f : f.toFixed(1)) + " " + u; f /= 1024; } return ""; };
    const niceMax = (v, floor) => {
      if (v <= floor) return floor;
      const p = Math.pow(10, Math.floor(Math.log10(v)));
      for (const m of [1, 2, 2.5, 5, 10]) if (m * p >= v) return m * p;
      return 10 * p;
    };
    const stamp = (sec, step, long) => {
      const d = new Date(sec * 1000);
      const hm = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
      return step >= 3600 || long ? d.toLocaleDateString([], { month: "short", day: "numeric" }) + " " + hm : hm;
    };

    let status, bar, cnote, tiles, charts, tables, neigh, foot, rangeBtns, fromIn, toIn, customRow;
    let range = "1h", lastAt = 0, seq = 0;

    // One line chart: series = [{ label, cls, values, text(i) }]; values below 0 are gaps.
    // With o.pick ({ sel }, kept by the caller so a refresh keeps it) a click on a legend pill shows that series alone, and a second
    // click on it shows them all again.
    function lineChart(title, d, all, o) {
      if (!o.pick) return drawLine(title, d, all, o, null);
      let card;
      const make = () => {
        const sel = all.find((s) => s.label === o.pick.sel) || null;   // the first of that name, should two series share one
        const pick = { all, sel, set: (s) => { o.pick.sel = sel === s ? null : s.label; const nc = make(); card.replaceWith(nc); card = nc; } };
        return drawLine(title, d, sel ? [sel] : all, o, pick);
      };
      return (card = make());
    }
    function drawLine(title, d, series, o, pick) {
      const n = d.n != null ? d.n : d.cpu.length;
      const peak = Math.max(0, ...series.flatMap((s) => s.values));
      const max = o.fixedMax || niceMax(peak, o.floor || 10);
      const W = 620, H = 230, L = axisMargin([0, 1, 2, 3, 4].map((g) => o.axis((max * g) / 4)), 62), R = 12, T = 12, B = 28, pw = W - L - R, ph = H - T - B;
      const x = (i) => L + (n <= 1 ? pw / 2 : (pw * i) / (n - 1));
      const y = (v) => T + ph - (ph * Math.min(v, max)) / max;
      const svg = sv("svg", { class: "qchart", viewBox: `0 0 ${W} ${H}`, role: "img", "aria-label": title + " over time" });
      for (let g = 0; g <= 4; g++) {
        const v = (max * g) / 4;
        svg.append(sv("line", { class: "qgrid", x1: L, x2: W - R, y1: y(v), y2: y(v) }), sv("text", { class: "qax", x: L - 6, y: y(v) + 4, "text-anchor": "end" }, o.axis(v)));
      }
      const ticks = Math.min(8, n);
      const long = d.step >= 600 && d.to - d.from > 20 * 3600;
      for (let k = 0; k < ticks; k++) {
        const i = ticks === 1 ? 0 : Math.round((k * (n - 1)) / (ticks - 1));
        svg.append(sv("text", { class: "qax", x: x(i), y: H - 8, "text-anchor": k === 0 ? "start" : k === ticks - 1 ? "end" : "middle" }, stamp(d.start + i * d.step, d.step, long)));
      }
      for (const s of series) {
        let path = "", pen = false;
        s.values.forEach((v, i) => { if (v < 0) { pen = false; return; } path += (pen ? "L" : "M") + x(i).toFixed(1) + "," + y(v).toFixed(1); pen = true; });
        if (path) svg.append(sv("path", { class: "qline " + s.cls, d: path }));
      }
      const cross = sv("line", { class: "qcross hidden", y1: T, y2: T + ph });
      const dots = series.map((s) => sv("circle", { class: "qdot hidden " + s.cls, r: 4, cx: 0, cy: 0 }));
      const tip = sv("g", { class: "qtip hidden" });
      const tipBg = sv("rect", { class: "qtipbg", rx: 6, width: 250, height: 20 + 18 * (series.length + 1) });
      const tipHead = sv("text", { class: "qtiph", x: 10, y: 17 });
      const tipRows = series.map((s, k) => sv("g", { transform: `translate(10 ${36 + 18 * k})` }, sv("rect", { class: s.cls + " sw", width: 10, height: 10, y: -9, rx: 2 }), sv("text", { class: "qtipt", x: 16, y: 0 }, "")));
      tip.append(tipBg, tipHead, ...tipRows);
      const hit = sv("rect", { class: "qhit", x: L, y: T, width: pw, height: ph,
        onmousemove: (e) => {
          const r = svg.getBoundingClientRect();
          const px = ((e.clientX - r.left) / r.width) * W;
          const i = Math.max(0, Math.min(n - 1, Math.round(((px - L) / pw) * (n - 1))));
          cross.setAttribute("x1", x(i)); cross.setAttribute("x2", x(i)); cross.classList.remove("hidden");
          series.forEach((s, k) => { const v = s.values[i]; dots[k].classList.toggle("hidden", v < 0); dots[k].setAttribute("cx", x(i)); dots[k].setAttribute("cy", v < 0 ? 0 : y(v)); });
          tipHead.textContent = new Date((d.start + i * d.step) * 1000).toLocaleString();
          series.forEach((s, k) => { tipRows[k].lastChild.textContent = s.label + ": " + (s.values[i] < 0 ? "no data" : s.text(i)); });
          const tx = x(i) > W / 2 ? x(i) - 260 : x(i) + 12;
          tip.setAttribute("transform", `translate(${tx} ${T + 6})`); tip.classList.remove("hidden");
        },
        onmouseleave: () => { cross.classList.add("hidden"); tip.classList.add("hidden"); dots.forEach((c) => c.classList.add("hidden")); } });
      svg.append(cross, ...dots, hit, tip);
      const legend = series.length > 1 || o.legend ? h("div", { class: "qlegend" }, (pick ? pick.all : series).map((s) => pick
        ? h("button", { type: "button", class: "qchip solo " + s.cls, title: pick.sel === s ? "Show all" : "Show only " + s.label, "aria-pressed": pick.sel == null ? null : String(pick.sel === s), onclick: () => pick.set(s) }, h("span", { class: "sw" }), s.label)
        : h("span", { class: "qchip " + s.cls }, h("span", { class: "sw" }), s.label))) : null;
      // the chart's numbers: one row per sample, the busiest sample of the interval beside the average where there is one; a gap is an empty cell
      const sampleRows = () => {
        const ss = pick ? pick.all : series;
        const cell = (v) => (v < 0 ? "" : csvNum(v));
        return [["Time (UTC)", ...ss.flatMap((s) => [s.label + " (" + o.unit + ")", ...(s.peaks ? [s.label + " peak (" + o.unit + ")"] : [])])],
          ...Array.from({ length: n }, (_, i) => [csvIso(d.start + i * d.step), ...ss.flatMap((s) => [cell(s.values[i]), ...(s.peaks ? [cell(s.peaks[i])] : [])])])];
      };
      return h("div", { class: "card" }, h("header", { class: "bar" }, h("h2", {}, tc(title)), h("span", { class: "grow" }), csvButton(title, sampleRows)), h("div", { class: "body" }, o.legendBelow ? null : legend, svg, o.legendBelow ? legend : null));
    }

    QCHARTS.lineChart = lineChart;

    function draw(d) {
      const n = d.now;
      clear(status);
      if (!n.at) { clear(tiles); clear(charts).append(h("div", { class: "empty" }, "No sample yet: the daemon has only just started.")); clear(tables); return; }
      const tile = (cls, label, value, sub) => h("div", { class: "qtile " + cls }, h("div", { class: "qv" }, value), h("div", { class: "qs" }, sub || "\u00a0"), h("div", { class: "ql" }, label));
      // the fullest real disk; tiny system mounts (under 1 GiB) only count when nothing bigger exists
      const big = (n.fs || []).filter((f) => f.total >= 1 << 30);
      const topFS = (big.length ? big : n.fs || []).reduce((a, f) => (!a || f.pct > a.pct ? f : a), null);
      clear(tiles).append(
        tile("t1", "CPU", pc(n.cpu_pct), n.cores + (n.cores === 1 ? " core" : " cores") + " · load " + n.load[0].toFixed(2)),
        tile("t3", "Memory", pc(n.mem_pct), bytes(n.mem_used) + " of " + bytes(n.mem_total)),
        tile("t4", "Disk space", topFS ? pc(topFS.pct) : "–", topFS ? topFS.mount + " · " + bytes(topFS.used) + " of " + bytes(topFS.total) : ""),
        tile("t2", "Disk I/O", pc(n.io_pct), "busiest disk, time busy"),
        tile("t5", "Network in", bits(n.rx_bps), n.net_util_pct >= 0 ? "busiest link " + pc(n.net_util_pct) : "link speed unknown"),
        tile("t6", "Network out", bits(n.tx_bps), ""));
      const cpuT = (i) => pc(d.cpu[i]) + " (peak " + pc(d.cpu_max[i]) + ")";
      const fsCls = ["t4", "t5", "t6", "t7", "t8", "t-other"];
      const ioT = (i) => pc(d.io[i]) + " (peak " + pc(d.io_max[i]) + ")";
      const diskSeries = [{ label: "I/O busy", cls: "t2", values: d.io, peaks: d.io_max, text: ioT }].concat((d.fs || []).map((f, k) => ({ label: f.mount + " used", cls: fsCls[k % fsCls.length], values: f.pct, text: (i) => pc(f.pct[i]) })));
      clear(charts).append(
        lineChart("CPU utilization", d, [{ label: "CPU", cls: "t1", values: d.cpu, peaks: d.cpu_max, text: cpuT }], { unit: "%", floor: 10, axis: (v) => (Number.isInteger(v) ? v : v.toFixed(1)) + "%" }),
        lineChart("Memory utilization", d, [{ label: "Memory", cls: "t3", values: d.mem, text: (i) => pc(d.mem[i]) }], { unit: "%", floor: 10, axis: (v) => (Number.isInteger(v) ? v : v.toFixed(1)) + "%" }),
        lineChart("Disk utilization", d, diskSeries, { unit: "%", fixedMax: 100, axis: (v) => (Number.isInteger(v) ? v : v.toFixed(1)) + "%", legend: true }),
        lineChart("Network utilization", d, [
          { label: "In", cls: "t5", values: d.rx, peaks: d.rx_max, text: (i) => bits(d.rx[i]) + " (peak " + bits(d.rx_max[i]) + ")" },
          { label: "Out", cls: "t6", values: d.tx, peaks: d.tx_max, text: (i) => bits(d.tx[i]) + " (peak " + bits(d.tx_max[i]) + ")" }], { unit: "bits/s", floor: 1000, axis: (v) => bits(v) }));
      const fsRows = (n.fs || []).map((f) => h("tr", {}, h("td", { class: "mono" }, f.mount), h("td", {}, f.device + " · " + f.type), h("td", { class: "num" }, bytes(f.used) + " of " + bytes(f.total)), h("td", { class: "num" }, pc(f.pct))));
      const ifList = (n.ifaces || []).filter((i) => !/(^| )lo$/.test(i.name));   // "lo", or "node lo" in the cluster view
      const ifRows = ifList.map((i) => h("tr", { class: i.counted ? null : "muted" }, h("td", { class: "mono" }, i.name), h("td", { title: i.counted ? null : "Not in the totals" }, i.up ? "up" : "down", i.counted ? "" : " *"),
        h("td", { class: "num" }, i.speed ? i.speed + " Mb/s" : "–"), h("td", { class: "num" }, bits(i.rx_bps)), h("td", { class: "num" }, bits(i.tx_bps)), h("td", { class: "num" }, i.util_pct >= 0 ? pc(i.util_pct) : "–")));
      fill(tables, 
        h("div", { class: "card" }, h("header", { class: "bar" }, h("h2", {}, tc("Filesystems")), h("span", { class: "grow" }), fsRows.length ? csvButton("Filesystems", () => [["Mount", "Device", "Type", "Used (bytes)", "Total (bytes)", "Use (%)"],
          ...(n.fs || []).map((f) => [f.mount, f.device, f.type, f.used, f.total, csvNum(f.pct)])]) : null), h("div", { class: "body flush" }, fsRows.length
          ? h("table", {}, h("thead", {}, h("tr", {}, h("th", {}, "Mount"), h("th", {}, "Device"), h("th", { class: "num" }, "Used"), h("th", { class: "num" }, "Use"))), h("tbody", {}, fsRows))
          : h("div", { class: "empty" }, "No disk filesystems found."))),
        h("div", { class: "card" }, h("header", { class: "bar" }, h("h2", {}, tc("Network interfaces")), h("span", { class: "grow" }), ifRows.length ? csvButton("Network interfaces", () => [["Interface", "State", "Counted in totals", "Speed (Mb/s)", "In (bits/s)", "Out (bits/s)", "Link use (%)"],
          ...ifList.map((i) => [i.name, i.up ? "up" : "down", i.counted ? "yes" : "no", i.speed || "", csvNum(i.rx_bps), csvNum(i.tx_bps), i.util_pct >= 0 ? csvNum(i.util_pct) : ""])]) : null), h("div", { class: "body flush" }, ifRows.length
          ? h("div", { class: "scroll" }, h("table", {}, h("thead", {}, h("tr", {}, h("th", {}, "Interface"), h("th", {}, "State"), h("th", { class: "num" }, "Speed"), h("th", { class: "num" }, "In"), h("th", { class: "num" }, "Out"), h("th", { class: "num" }, "Link use"))), h("tbody", {}, ifRows)))
          : h("div", { class: "empty" }, "No interfaces found."))));
      clear(cnote).append(...clusterNote(d.cluster));   // which nodes are added together, when the Node menu says Cluster
      foot.hidden = true;
    }

    async function load() {
      const my = ++seq;
      try {
        let from = range, to = "";
        if (range === "custom") {
          const a = new Date(fromIn.value), b = new Date(toIn.value);
          if (isNaN(a) || isNaN(b) || a >= b) throw new Error("Pick a start that is before the end.");
          from = String(Math.floor(a / 1000)); to = String(Math.floor(b / 1000));
        }
        const r = await api("GET", "/api/host?from=" + encodeURIComponent(from) + (to ? "&to=" + encodeURIComponent(to) : ""));
        if (my !== seq) return;
        lastAt = Date.now();
        draw(r.data);
      } catch (e) { fail(status)(e); }
      loadNeighbors(my);
    }

    // LLDP neighbors of the picked node: the switch and port each interface is plugged into.  Not for the cluster as one
    // (that is a sum), and not while LLDP is off and lldpd is not running.
    async function loadNeighbors(my) {
      if (state.target === CLUSTER) { clear(neigh); return; }
      let st;
      try { st = (await api("GET", "/api/lldp")).data; } catch (e) { if (e.message === "unauthenticated") throw e; clear(neigh); return; }
      if (my !== seq) return;
      if (!st.enabled && !st.running) { clear(neigh); return; }
      const ns = st.neighbors || [];
      const rows = ns.map((n) => h("tr", {},
        h("td", { class: "mono" }, n.interface),
        h("td", { title: n.system_descr || null }, n.system_name || n.chassis_id || "–"),
        h("td", { title: n.port_descr && n.port_descr !== n.port_id ? n.port_id : null }, n.port_descr || n.port_id || "–"),
        h("td", {}, (n.vlans || []).join(", ") || "–"),
        h("td", { class: "mono" }, (n.mgmt_ips || []).join(", ") || "–"),
        h("td", { class: "num" }, n.age || "–")));
      const body = st.error ? h("div", { class: "empty bad-text" }, st.error)
        : !st.running ? h("div", { class: "empty" }, lldpWords(st))
        : rows.length ? h("div", { class: "scroll" }, h("table", {}, h("thead", {}, h("tr", {}, ["Interface", "Switch", "Port", "VLAN", "Management", "Seen"].map((t, i) => h("th", { class: i === 5 ? "num" : null }, t)))), h("tbody", {}, rows)))
        : h("div", { class: "empty" }, "None seen yet.");
      const dl = !st.error && st.running && ns.length ? csvButton("LLDP neighbors", () => [["Interface", "Switch", "Port", "VLAN", "Management", "Seen"],
        ...ns.map((n) => [n.interface, n.system_name || n.chassis_id || "", n.port_descr || n.port_id || "", (n.vlans || []).join("; "), (n.mgmt_ips || []).join("; "), n.age || ""])]) : null;
      clear(neigh).append(h("div", { class: "card" }, h("header", { class: "bar" }, h("h2", {}, tc("LLDP neighbors")), h("span", { class: "grow" }), dl), h("div", { class: "body flush" }, body)));
    }

    function setRange(r) {
      range = r;
      for (const [id, b] of rangeBtns) b.setAttribute("aria-pressed", id === r ? "true" : "false");
      customRow.classList.toggle("hidden", r !== "custom");
      if (r === "custom" && !fromIn.value) {
        const f = (d) => new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
        toIn.value = f(new Date()); fromIn.value = f(new Date(Date.now() - 6 * 3600 * 1000));
      }
      if (r !== "custom") load();
    }

    return {
      mount(main) {
        status = h("div", { "aria-live": "polite" });
        rangeBtns = new Map(RANGES.map(([id, label]) => [id, h("button", { type: "button", class: "seg", "aria-pressed": id === range ? "true" : "false", onclick: () => setRange(id) }, label)]));
        fromIn = h("input", { type: "datetime-local", "aria-label": "From" });
        toIn = h("input", { type: "datetime-local", "aria-label": "To" });
        customRow = h("span", { class: "qcustom hidden" }, fromIn, " to ", toIn,
          h("button", { class: "btn", type: "button", onclick: load }, "Apply"), h("span", { class: "muted small" }, " only the last 30 days exist"));
        bar = h("div", { class: "toolbar qbar" }, h("span", { class: "segs", role: "group", "aria-label": "Time range" }, [...rangeBtns.values()]), customRow);
        tiles = h("div", { class: "qtiles" });
        charts = h("div", { class: "hostcharts" });
        tables = h("div", { class: "hosttables" });
        neigh = h("div", {});
        foot = h("p", { class: "hint" });
        cnote = h("div", {});
        main.append(status, bar, cnote, tiles, charts, tables, neigh, foot);
        setRange(range);
      },
      async poll() {
        if (range === "custom") return;
        if (Date.now() - lastAt >= REFRESH_MS) await load();
      },
    };
  })();

  // ── Log, Monitor  (CLI: --log) ───────────────────────────────────────────────
  VIEWS.log = (() => {
    let status, level, span, count, search, live, info, box, first, timer, tsBtn, tsHold = 0, seq = 0, shown = [];
    // the filters survive a change of node (and of page): the view is built again each time, from these
    const kept = { level: "", span: "1h", count: "1000", q: "", live: true };
    const sel = (label, opts, val, onchange) => {
      const el = h("select", { "aria-label": label, onchange }, opts.map(([v, t]) => h("option", { value: v }, t)));
      el.value = val;
      return el;
    };
    const stamp = (t) => {
      const d = new Date(t);
      if (isNaN(d)) return t || "";
      const day = d.toDateString() === new Date().toDateString() ? "" : d.toLocaleDateString([], { month: "short", day: "numeric" }) + " ";
      return day + d.toLocaleTimeString([], { hour12: false });
    };
    const words = () => search.value.trim().split(/\s+/).filter(Boolean);
    // the message with the search words marked
    const marked = (msg) => {
      const w = words();
      if (!w.length) return msg;
      const re = new RegExp("(" + w.map((x) => x.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join("|") + ")", "ig");
      return msg.split(re).map((part, i) => (i % 2 ? h("mark", {}, part) : part));
    };
    function draw(r) {
      const stick = box.scrollHeight - box.scrollTop - box.clientHeight < 40; // at the bottom: follow new lines
      shown = r.lines;
      clear(box);
      if (!r.lines.length) box.append(h("div", { class: "empty" }, r.scanned ? "No log line matches these filters." : "The log is empty."));
      for (const l of r.lines) {
        box.append(h("div", { class: "logline lv-" + l.level.toLowerCase() },
          h("span", { class: "lt" }, stamp(l.time)), h("span", { class: "ll" }, l.level), h("span", { class: "lm" }, marked(l.msg))));
      }
      if (stick || first) box.scrollTop = box.scrollHeight;
      clear(info).append(r.matched + " matching line" + (r.matched === 1 ? "" : "s") + " of " + r.scanned + " on disk" +
        (r.truncated ? "; showing the newest " + r.lines.length + " (older ones are cut: narrow the filters or raise the line count)" : "") +
        (r.oldest ? ". The log reaches back to " + when(r.oldest) + "." : "."));
    }
    async function load() {
      const my = ++seq;
      const q = new URLSearchParams({ level: level.value, q: search.value.trim(), since: span.value, n: count.value });
      try {
        const r = (await api("GET", "/api/log?" + q.toString())).data;
        if (my !== seq) return; // a newer request is on its way
        if (Date.now() > tsHold) clear(status);   // not while the troubleshooting bundle is saying something
        draw(r);
        first = false;
      } catch (e) { fail(status)(e); }
    }
    // the troubleshooting bundle: every node's logs, configuration and state in one .tgz, asked of the node signed in to
    async function tshoot() {
      tsBtn.disabled = true; tsHold = Infinity;
      say(status, "info", "Collecting from every node… this takes up to a minute.");
      try {
        await apiDownload("/api/tshoot/download?capture=1", "logdir-tshoot.tgz");
        say(status, "info", "Downloaded. Passwords, the gateway key, tokens and join codes are removed; README.txt in it lists what it holds.");
      } catch (e) { fail(status)(e); }
      tsBtn.disabled = false; tsHold = Date.now() + 20000;
    }
    function download() {
      const text = shown.map((l) => l.raw).join("\n") + "\n";
      const a = h("a", { href: URL.createObjectURL(new Blob([text], { type: "text/plain" })), download: "logdir-log.txt" });
      document.body.append(a); a.click(); a.remove();
      setTimeout(() => URL.revokeObjectURL(a.href), 1000);
    }
    return {
      mount(main) {
        first = true;
        status = h("div", { "aria-live": "polite" });
        level = sel("Level", [["", "All levels"], ["info", "Info and above"], ["warn", "Warnings and errors"], ["error", "Errors only"]], kept.level, () => { kept.level = level.value; load(); });
        span = sel("Time range", [["15m", "Last 15 minutes"], ["1h", "Last hour"], ["6h", "Last 6 hours"], ["1d", "Last 24 hours"], ["7d", "Last 7 days"], ["all", "All of it"]], kept.span, () => { kept.span = span.value; load(); });
        count = sel("Lines", [["200", "200 lines"], ["1000", "1000 lines"], ["5000", "5000 lines"]], kept.count, () => { kept.count = count.value; load(); });
        search = h("input", { type: "search", placeholder: "Filter words (AND, OR, !not)…", "aria-label": "Filter text", spellcheck: "false", class: "grow",
          oninput: () => { kept.q = search.value; clearTimeout(timer); timer = setTimeout(load, 300); } });
        if (state.prefill.log != null) { kept.q = state.prefill.log; state.prefill.log = null; }   // sent here from a server's menu
        search.value = kept.q;
        live = h("input", { type: "checkbox", checked: kept.live, onchange: () => { kept.live = live.checked; } });
        tsBtn = h("button", { class: "btn", type: "button", onclick: tshoot }, "tshoot");
        info = h("div", { class: "hint nomargin" });
        box = h("div", { class: "logbox", tabindex: "0", role: "log", "aria-label": "Log lines" });
        main.append(status, section("Log",
          h("div", { class: "toolbar tight logbar" }, search, level, span, count,
            h("label", { class: "opt" }, live, " Live")),
          h("div", { class: "toolbar tight" },
            h("button", { class: "btn", type: "button", onclick: load }, "Refresh"),
            h("button", { class: "btn", type: "button", onclick: download }, "Download"),
            tsBtn),
          box, info));
      },
      async poll() {
        if (first || live.checked) await load();
      },
      poll_ms: 3000,
    };
  })();

  // ── Logs  (CLI: --logs, --logs-search; API: GET /api/logs/search, /tail, /stats, /senders) ──────────
  // The syslog the cluster collects: how much came in over time, the newest messages that match what is typed (every node is asked
  // and the answers are merged by time), the busiest hosts and programs (click one to look at just that), and the senders.
  VIEWS.logs = (() => {
    const SVGNS = "http://www.w3.org/2000/svg";
    const sv = (tag, attrs, ...kids) => {
      const el = document.createElementNS(SVGNS, tag);
      for (const [k, v] of Object.entries(attrs || {})) {
        if (v == null || v === false) continue;
        if (k.startsWith("on")) el.addEventListener(k.slice(2), v); else el.setAttribute(k, v);
      }
      for (const kid of kids.flat(Infinity)) if (kid != null) el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
      return el;
    };
    const RANGES = [["15m", "15 Min"], ["1h", "1 Hour"], ["6h", "6 Hours"], ["1d", "1 Day"], ["7d", "7 Days"], ["30d", "30 Days"], ["all", "All"]];
    const SEVS = [["", "Any severity"], ["crit", "Critical or worse"], ["err", "Error or worse"], ["warning", "Warning or worse"], ["warning=", "Warning only"], ["notice", "Notice or worse"], ["info", "Info or worse"]];
    const REFRESH_MS = 15000, FOLLOW_MS = 2000, FOLLOW_KEEP = 2000;
    // what the page keeps when it is left and come back to
    const kept = { range: "15m", severity: "", host: "", program: "", source: "", q: "", page: 0, size: 25, field: "" };
    const opened = new Set();   // messages whose fields are shown (by time, sender and text)
    const SEVNAMES = ["emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"];
    const count = (v) => (Number(v) || 0).toLocaleString();
    const niceMax = (v) => {
      if (v <= 0) return 1;
      const p = Math.pow(10, Math.floor(Math.log10(v)));
      for (const m of [1, 2, 2.5, 5, 10]) if (m * p >= v) return m * p;
      return 10 * p;
    };
    const stamp = (sec, long) => {
      const d = new Date(sec * 1000);
      const hm = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
      return long ? d.toLocaleDateString([], { month: "short", day: "numeric" }) + " " + hm : hm;
    };
    const ago = (u) => { const s = Math.max(0, Math.floor(Date.now() / 1000 - u)); return s < 90 ? s + " s ago" : s < 5400 ? Math.round(s / 60) + " min ago" : s < 172800 ? Math.round(s / 3600) + " h ago" : Math.round(s / 86400) + " d ago"; };
    const clock = (ms, withDate) => {
      const d = new Date(ms), p2 = (n) => String(n).padStart(2, "0");
      return (withDate ? d.getFullYear() + "-" + p2(d.getMonth() + 1) + "-" + p2(d.getDate()) + " " : "") + p2(d.getHours()) + ":" + p2(d.getMinutes()) + ":" + p2(d.getSeconds()) + "." + String(d.getMilliseconds()).padStart(3, "0");
    };

    let tiles, chartBox, msgBox, hostBox, qIn, rangeSel, sevSel;
    let lastAt = 0, seq = 0, inflight = null, inflightKey = "", senders = [], stats = null, res = null, msgs = [], following = false, followAt = 0, followBusy = false, lastW = 0;

    const params = (extra) => {
      const p = new URLSearchParams({ range: kept.range });
      if (kept.q) p.set("q", kept.q);
      if (kept.host) p.set("host", kept.host);
      if (kept.program) p.set("program", kept.program);
      if (kept.severity) p.set("severity", kept.severity);
      if (kept.source) p.set("source", kept.source);
      for (const [k, v] of Object.entries(extra || {})) p.set(k, v);
      return p;
    };
    // The one filter box: words (and, or, not, !word) plus host=sw*, program=sshd and source=10.1.1.1, which narrow like the row menu does.
    const filterText = () => [kept.host && "host=" + kept.host, kept.program && "program=" + kept.program, kept.source && "source=" + kept.source, kept.q].filter(Boolean).join(" ");
    function readFilter() {
      const rest = [];
      kept.host = kept.program = kept.source = "";
      for (const w of qIn.value.trim().split(/\s+/).filter(Boolean)) {
        const m = /^(host|program|prog|source|src)=(.+)$/i.exec(w);
        if (!m) rest.push(w);
        else if (/^host$/i.test(m[1])) kept.host = m[2];
        else if (/^(program|prog)$/i.test(m[1])) kept.program = m[2];
        else kept.source = m[2];
      }
      kept.q = rest.join(" ");
    }
    function set(next) {
      Object.assign(kept, next); kept.page = 0;
      qIn.value = filterText(); sevSel.value = kept.severity;
      load();
    }
    // a new shipping destination with what the page is filtered on now, or, with no filter, this message's host and program (Configure ▸ Shipping)
    function shipLikeThis(m) {
      const f = shipFilterParse(filterText());
      if (!Object.keys(f).length) { if (m.host) f.host = m.host; if (m.program) f.program = m.program; }
      state.prefill.ship = { type: "syslog", ...f, ...shipSevFields(kept.severity) };
      selectTab("cfg_shipping");
    }
    const sevCls = (n) => "sev sev-" + (SEVNAMES.indexOf(n) < 0 ? 6 : SEVNAMES.indexOf(n));
    const showTime = () => kept.range !== "15m" && kept.range !== "1h" && kept.range !== "6h";

    function messageMenu(m) {
      const own = [["Only this host", () => set({ host: m.host || "" })], ["Only this program", () => set({ program: m.program || "" })],
        ["This severity or worse", () => set({ severity: m.severity })], ["Only this sender", () => set({ source: m.source || "" })],
        ["Copy message", () => { try { navigator.clipboard.writeText(m.message); } catch (_) { /* no clipboard */ } }],
        ["Ship messages like this…", () => shipLikeThis(m)]].filter((it) => !(it[0] === "Only this host" && !m.host) && !(it[0] === "Only this program" && !m.program));
      return m.source ? [...own, "-", ...netToolMenu(m.source)] : own;
    }
    // the host column: what the sender is called (the first part of its DNS name, else its name in Configure ▸ Senders), the same name the Senders
    // page and the charts use; a sender that is not in the list shows the host name its message gives, else its DNS name.  The name the message
    // gives itself (a firewall calls itself "cush", the DNS says fw1-mgmt) is in the tooltip.  The address has its own column.
    function hostCell(m) {
      const e = senders.find((x) => x.addr === m.source), nm = (e && (e.rev || e.name)) || "";
      if (nm) return h("button", { type: "button", class: "qlink", title: "Only this sender" + (m.host && m.host !== m.source && m.host.toLowerCase() !== nm.toLowerCase() ? " (the message says its host is " + m.host + ")" : ""), onclick: () => set({ source: m.source }) }, nm);
      if (m.host && m.host !== m.source) return h("button", { type: "button", class: "qlink", title: "Only this host", onclick: () => set({ host: m.host }) }, m.host);
      return m.source_name ? h("button", { type: "button", class: "qlink", title: "Only this sender", onclick: () => set({ source: m.source }) }, m.source_name) : "–";
    }
    // a field as a search word: name=value, a value with spaces or quotes as a * wildcard (the search splits at spaces)
    const fieldWord = (f) => f.k + "=" + (String(f.v).replace(/[\s"]+/g, "*") || "*");
    function addWord(w) { kept.q = (kept.q ? kept.q + " " : "") + w; set({}); }
    function fieldMenu(f) {
      return [["Only this value", () => addWord(fieldWord(f))], ["Not this value", () => addWord("not " + fieldWord(f))],
        ["Top values of " + f.k, () => { kept.field = f.k; set({}); }], "-",
        ["Copy value", () => { try { navigator.clipboard.writeText(f.v); } catch (_) { /* no clipboard */ } }]];
    }
    const msgKey = (m) => m.t + "|" + (m.source || "") + "|" + m.message;
    function fieldsRow(m, cols) {
      return h("tr", { class: "qfrow" }, h("td", { colspan: String(cols) }, h("div", { class: "qfchips" },
        m.fields.map((f) => h("button", { type: "button", class: "qchip", title: "Only this value (right-click for more)", oncontextmenu: rowMenu(fieldMenu(f)), onclick: () => addWord(fieldWord(f)) },
          h("span", { class: "qk" }, f.k), "=", h("span", { class: "qv mono" }, f.v === "" ? "\u00a0" : f.v))))));
    }
    function row(m, multi) {
      const prog = m.program ? h("button", { type: "button", class: "qlink", title: "Only this program", onclick: () => set({ program: m.program }) }, m.program, m.pid ? h("span", { class: "muted" }, "[" + m.pid + "]") : null) : null;
      return h("tr", { oncontextmenu: rowMenu(messageMenu(m)), title: m.source ? "Sender " + m.source + (m.source_name ? " (" + m.source_name + ")" : "") : null },
        h("td", { class: "mono nowrap", title: m.time }, clock(m.t, showTime())),
        h("td", {}, h("span", { class: sevCls(m.severity) }, m.severity)),
        h("td", {}, hostCell(m)),
        h("td", { class: "mono" }, m.source ? h("button", { type: "button", class: "qlink mono", title: "Only this sender", onclick: () => set({ source: m.source }) }, m.source) : "–"),
        h("td", {}, prog || "–"),
        h("td", { class: "qmsg" }, m.message, m.fields && m.fields.length ? h("button", { type: "button", class: "qlink qfbtn", "aria-expanded": String(opened.has(msgKey(m))), title: opened.has(msgKey(m)) ? "Hide the fields" : "Show the fields read from this message",
          onclick: () => { const k = msgKey(m); if (opened.has(k)) opened.delete(k); else opened.add(k); drawMessages(); } }, m.fields.length + " field" + (m.fields.length === 1 ? "" : "s")) : null),
        multi ? h("td", { class: "mono muted small" }, m.node || "") : null);
    }
    function drawMessages() {
      const r = res || { nodes: 1, node_errors: [] };
      const multi = (r.nodes || 1) > 1;
      const typingNow = document.activeElement === qIn, at0 = qIn.selectionStart, at1 = qIn.selectionEnd;   // the box is in this card's header, built again on every report
      const all = msgs.slice().reverse();   // the newest first
      // paging: the messages asked for are in the page; this shows a screenful of them at a time (while following, always the newest)
      const size = kept.size, pageCount = Math.max(1, Math.ceil(all.length / size));
      if (following) kept.page = 0;
      kept.page = Math.min(Math.max(kept.page, 0), pageCount - 1);
      const from = kept.page * size, list = all.slice(from, from + size);
      const go = (p) => () => { kept.page = p; drawMessages(); };
      const mkBtn = (label, title, dis, p) => h("button", { type: "button", class: "btn small", title, "aria-label": title, disabled: dis, onclick: go(p) }, label);
      const pageIn = h("input", { type: "number", min: "1", max: String(pageCount), class: "policy-page", "aria-label": "Page number", value: String(kept.page + 1),
        onchange: (e) => { kept.page = (parseInt(e.target.value, 10) || 1) - 1; drawMessages(); } });
      const sizeSel = h("select", { "aria-label": "Messages per page", onchange: (e) => { kept.size = Number(e.target.value); kept.page = 0; drawMessages(); } },
        [25, 50, 100, 250, 500].map((n) => h("option", { value: String(n) }, n + " per page")));
      sizeSel.value = String(kept.size);
      const pager = h("span", { class: "policy-pager" }, mkBtn("«", "First page", kept.page === 0, 0), mkBtn("‹", "Previous page", kept.page === 0, kept.page - 1), pageIn, h("span", { class: "muted" }, "of " + pageCount),
        mkBtn("›", "Next page", kept.page >= pageCount - 1, kept.page + 1), mkBtn("»", "Last page", kept.page >= pageCount - 1, pageCount - 1));
      pager.hidden = pageCount < 2;
      const body = list.length
        ? h("div", { class: "scroll" }, h("table", { class: "qlogs" },
          h("thead", {}, h("tr", {}, ["Time", "Severity", "Host", "Address", "Program", "Message", multi ? "Node" : null].filter((x) => x != null).map((x) => h("th", {}, x)))),
          h("tbody", {}, list.map((m) => opened.has(msgKey(m)) && m.fields && m.fields.length ? [row(m, multi), fieldsRow(m, multi ? 7 : 6)] : row(m, multi)))))
        : h("div", { class: "empty" }, "No message matches in this range.");
      const nodeErr = (r.node_errors || []).length ? h("span", { class: "bad-text small", title: r.node_errors.map((e) => e.node + ": " + e.error).join("\n") }, " " + r.node_errors.length + " of " + r.nodes + " nodes did not answer") : null;
      const note = r.note && !nodeErr ? h("span", { class: "muted small", title: r.note }, "ⓘ") : null;
      const csv = () => [["Time (UTC)", "Severity", "Facility", "Host", "Program", "PID", "Message", "Sender", "Sender DNS name", "Via", "Node"], ...all.map((m) => [m.time, m.severity, m.facility, m.host || "", m.program || "", m.pid || "", m.message, m.source || "", m.source_name || "", m.via || "", m.node || ""])];
      clear(msgBox).append(h("div", { class: "card" }, h("header", { class: "bar" }, h("h2", {}, tc(following ? "Messages (following)" : "Messages")), nodeErr, note, h("span", { class: "grow" }),
        h("span", { class: "qfiltbox" }, qIn), h("span", { class: "grow" }),
        h("span", { class: "muted small" }, (all.length ? count(from + 1) + "–" + count(from + list.length) + " of " : "") + count(all.length) + (r.truncated && !following ? " newest" : "")), pager, all.length ? sizeSel : null, all.length ? csvButton("Log messages", csv) : null), h("div", { class: "body flush" }, body)));
      if (typingNow) { qIn.focus(); try { qIn.setSelectionRange(at0, at1); } catch (_) { /* not a text box */ } }
    }
    function drawTiles() {
      const s = stats, sev = (s && s.by_severity) || {};
      const err = ["emerg", "alert", "crit", "err"].reduce((a, k) => a + (sev[k] || 0), 0);
      const from = s ? s.from / 1000 : 0;
      const live = new Set(senders.filter((e) => e.last_seen >= from).map((e) => e.addr));
      // the colours say what the tile is: green messages, red errors, amber warnings, blue senders
      // a tile is a button: Messages, Errors and Warnings filter the list to that severity (Messages: any), Senders opens Monitor ▸ Senders
      const tile = (cls, label, value, sub, title, onpick, pressed) => h("button", { type: "button", class: "qtile pick " + cls, title, "aria-label": title, "aria-pressed": pressed == null ? null : String(pressed), onclick: onpick },
        h("div", { class: "qv" }, value), h("div", { class: "qs" }, sub || " "), h("div", { class: "ql" }, label));
      clear(tiles).append(
        tile("t6", "Messages", s ? count(s.total) : "–", s ? "in this range" : "", "Show messages of any severity", () => set({ severity: "" })),
        tile("t8", "Errors", s ? count(err) : "–", "error or worse", "Show only errors or worse", () => set({ severity: "err" }), kept.severity === "err"),
        tile("t4", "Warnings", s ? count(sev.warning || 0) : "–", "warning", "Show only warnings", () => set({ severity: "warning=" }), kept.severity === "warning="),
        tile("t1", "Senders", String(live.size), "sent in this range", "Open Monitor ▸ Senders", () => selectTab("senders")));
    }
    // ── the chart: messages per interval ──
    function chart() {
      const s = stats;
      const pts = (s && s.series) || [];
      if (!pts.length || pts.every((p) => !p.count)) return h("div", { class: "empty" }, "No messages in this range.");
      const step = s.step_s || 60, n = pts.length;
      const max = niceMax(Math.max(...pts.map((p) => p.count)));
      const W = Math.max(480, Math.round((chartBox.clientWidth || 794) - 34)), H = 200, L = axisMargin([0, 1, 2, 3, 4].map((g) => count(Math.round((max * g) / 4))), 44), R = 12, T = 12, B = 28, pw = W - L - R, ph = H - T - B;
      const y = (v) => T + ph - (ph * Math.min(v, max)) / max;
      const svg = sv("svg", { class: "qchart", viewBox: `0 0 ${W} ${H}`, role: "img", "aria-label": "Messages over time" });
      for (let g = 0; g <= 4; g++) {
        const v = (max * g) / 4;
        svg.append(sv("line", { class: "qgrid", x1: L, x2: W - R, y1: y(v), y2: y(v) }), sv("text", { class: "qax", x: L - 6, y: y(v) + 4, "text-anchor": "end" }, count(Math.round(v))));
      }
      const bw = pw / n, long = s.to - s.from > 20 * 3600 * 1000;
      pts.forEach((p, i) => {
        const bh = ph - (y(p.count) - T);
        if (p.count > 0) svg.append(sv("rect", { class: "qbar-fill t1", x: (L + bw * i + bw * 0.08).toFixed(1), y: y(p.count).toFixed(1), width: Math.max(1, bw * 0.84).toFixed(1), height: Math.max(1, bh).toFixed(1) },
          sv("title", {}, new Date(p.t * 1000).toLocaleString() + "\n" + count(p.count) + " message" + (p.count === 1 ? "" : "s"))));
      });
      // labels sit on round clock times, at most eight, so the gap between neighbours is always the same
      const NICE = [60, 120, 300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400, 172800, 604800].filter((v) => v >= step && v % step === 0);
      const iv = NICE.find((v) => (pts[n - 1].t - pts[0].t) / v <= 7) || NICE[NICE.length - 1] || step;
      let tickAt = [];
      for (let i = 0; i < n; i++) { const d = new Date(pts[i].t * 1000); if (((d.getHours() * 3600 + d.getMinutes() * 60 + d.getSeconds()) % iv === 0) && (iv < 86400 || (d.getHours() === 0 && d.getMinutes() === 0))) tickAt.push(i); }
      if (tickAt.length < 2) tickAt = [0, n - 1];
      for (const i of tickAt) {
        const xi = L + bw * i + bw / 2;
        svg.append(sv("text", { class: "qax", x: xi, y: H - 8, "text-anchor": xi < L + 22 ? "start" : xi > W - R - 22 ? "end" : "middle" }, stamp(pts[i].t, long)));
      }
      return { svg, csv: () => [["Time (UTC)", "Messages"], ...pts.map((p) => [csvIso(p.t), p.count])] };
    }
    function drawChart() {
      lastW = chartBox.clientWidth;
      const c = chart();
      clear(chartBox).append(h("div", { class: "card" }, h("header", { class: "bar" }, h("h2", {}, "Messages over time"), h("span", { class: "grow" }), c.csv ? csvButton("Log messages over time", c.csv) : null),
        h("div", { class: "body" }, c.csv ? c.svg : c)));
    }
    function topCard(title, key, rows, pick, opt) {
      opt = opt || {};
      const total = (stats && stats.total) || 0;
      const body = rows && rows.length
        ? h("div", { class: "scroll" }, h("table", {}, h("thead", {}, h("tr", {}, h("th", {}, opt.col || title.replace("Busiest ", "").replace(/^./, (c) => c.toUpperCase())), h("th", { class: "num" }, "Messages"), h("th", { class: "num" }, "Share"))),
          h("tbody", {}, rows.map((x) => h("tr", {}, h("td", {}, h("button", { type: "button", class: "qlink", title: "Only this " + key, onclick: () => pick(x.name) }, x.name)),
            h("td", { class: "num" }, count(x.count)), h("td", { class: "num" }, total ? (x.count * 100 / total).toFixed(1) + "%" : "–"))))))
        : h("div", { class: "empty" }, "None in this range.");
      return h("div", { class: "card" }, h("header", { class: "bar" }, h("h2", {}, opt.head || tc(title)), h("span", { class: "grow" }),
        rows && rows.length ? csvButton(title, () => [[key, "Messages"], ...rows.map((x) => [x.name, x.count])]) : null), h("div", { class: "body flush" }, body));
    }
    function drawTops() {
      clear(hostBox).append(topCard("Busiest hosts", "host", stats && stats.top_hosts, (n) => set({ host: n })), topCard("Busiest programs", "program", stats && stats.top_programs, (n) => set({ program: n })));
      if (kept.field && stats) {
        const card = topCard("Values of " + kept.field, kept.field, stats.top_values, (v) => addWord(fieldWord({ k: kept.field, v })),
          { col: "Value", head: [tc("Values of") + " ", h("span", { class: "mono" }, kept.field)] });
        const bar = card.querySelector("header");
        bar.append(h("button", { type: "button", class: "btn small", title: "Close", "aria-label": "Close the values of " + kept.field, onclick: () => { kept.field = ""; set({}); } }, "×"));
        if (stats.values > (stats.top_values || []).length) bar.querySelector("h2").append(h("span", { class: "muted small" }, " " + count(stats.values) + " different"));
        hostBox.append(card);
      }
    }
    // load: the messages, the counts and the senders.  A new load drops the one still running (typing a filter starts one at every
    // pause; over 30 days each can take seconds, and the daemon stops reading when its request is dropped).
    async function load() {
      const qs = params({ fields: "1" }).toString(), sq = params({ series: "1", top: "10", ...(kept.field ? { field: kept.field } : {}) }).toString();
      if (inflight && inflightKey === qs + "&" + sq) return;   // the same again (Enter after the pause already started it): let it finish
      const my = ++seq;
      if (inflight) inflight.abort();
      const ctl = inflight = new AbortController();
      inflightKey = qs + "&" + sq;
      try {
        const [rep, st, sn] = await Promise.all([api("GET", "/api/logs/search?" + qs, undefined, false, ctl.signal),
          api("GET", "/api/logs/stats?" + sq, undefined, false, ctl.signal),
          api("GET", "/api/logs/senders", undefined, false, ctl.signal)]);
        if (my !== seq) return;
        res = rep.data; msgs = res.messages || []; stats = st.data; senders = (sn.data && sn.data.senders) || [];
        if (!!rep.follow !== following) { following = !!rep.follow; followAt = 0; }   // Configure ▸ Settings ▸ Log collector ▸ Follow new messages
        lastAt = Date.now();
        setBad("");
        drawTiles(); drawChart(); drawMessages(); drawTops();
      } catch (e) {
        if (e.message === "unauthenticated" || e.name === "AbortError") return;
        setBad(e.message);
      } finally { if (inflight === ctl) inflight = null; }
    }
    // following: what came since the newest message, every couple of seconds
    async function followStep() {
      if (followBusy || !following) return;
      followBusy = true; followAt = Date.now();
      try {
        const last = msgs.length ? msgs[msgs.length - 1].t : Date.now() - 60000;
        const p = new URLSearchParams({ after: String(last), limit: "500", fields: "1" });
        for (const k of ["q", "host", "program", "severity"]) if (kept[k]) p.set(k, kept[k]);
        const r = (await api("GET", "/api/logs/tail?" + p.toString())).data;
        if (!following) return;
        setBad("");
        const fresh = (r.messages || []).filter((m) => !kept.source || m.source === kept.source);
        if (fresh.length) { msgs = msgs.concat(fresh).slice(-FOLLOW_KEEP); res = { ...(res || {}), node_errors: r.node_errors, nodes: r.nodes }; drawMessages(); }
      } catch (e) { if (e.message !== "unauthenticated") setBad(e.message); } finally { followBusy = false; }
    }
    function setBad(msg) { qIn.classList.toggle("qbad", !!msg); qIn.title = msg || ""; qIn.setAttribute("aria-invalid", msg ? "true" : "false"); }
    function apply() { readFilter(); kept.page = 0; load(); }

    return {
      mount(main) {
        senders = []; stats = null; res = null; msgs = []; following = false;
        let typed = 0;
        qIn = h("input", { type: "text", class: "qfilter qlogq", placeholder: "failed password and not root   host=sw* program=sshd", "aria-label": "Filter messages", spellcheck: "false", autocomplete: "off",
          onkeydown: (e) => { if (e.key === "Enter") { clearTimeout(typed); apply(); } }, oninput: () => { clearTimeout(typed); typed = setTimeout(apply, 600); } });
        if (state.prefill.logs != null) { kept.source = state.prefill.logs; state.prefill.logs = null; }   // sent here from the Topology page: one sender's messages
        if (state.prefill.logsq != null) {   // sent here by Preview on Configure ▸ Shipping: what a destination sends
          const p = state.prefill.logsq; state.prefill.logsq = null;
          qIn.value = p.text; readFilter(); kept.severity = p.severity || ""; kept.page = 0;
        }
        qIn.value = filterText();
        rangeSel = h("select", { "aria-label": "Time range", onchange: () => { kept.range = rangeSel.value; kept.page = 0; load(); } }, RANGES.map(([id, text]) => h("option", { value: id }, text)));
        rangeSel.value = kept.range;
        sevSel = h("select", { "aria-label": "Severity", onchange: () => { kept.severity = sevSel.value; kept.page = 0; load(); } }, SEVS.map(([id, text]) => h("option", { value: id }, text)));
        sevSel.value = kept.severity;
        tiles = h("div", { class: "qtiles" }); chartBox = h("div", {}); msgBox = h("div", {}); hostBox = h("div", { class: "logtops" });
        if (window.ResizeObserver) { let tm = 0; new ResizeObserver(() => { clearTimeout(tm); tm = setTimeout(() => { if (stats && Math.abs(chartBox.clientWidth - lastW) > 24) drawChart(); }, 150); }).observe(chartBox); }
        const bar = h("div", { class: "toolbar qbar qlogbar" }, rangeSel, sevSel);
        main.append(bar, tiles, h("div", { class: "hosttables flowwide" }, chartBox, msgBox, hostBox));
        drawMessages();   // so the filter box is there even when the first report fails
        load();
      },
      async poll() {
        if (following) { if (Date.now() - followAt >= FOLLOW_MS) await followStep(); return; }
        if (Date.now() - lastAt >= REFRESH_MS && document.activeElement !== qIn && !inflight) await load();   // never on top of one still running
      },
      leave() { if (inflight) inflight.abort(); inflight = null; },
    };
  })();

  // ── Shipping, Monitor  (CLI: --logs-ship; API: GET /api/logs/shipping) ────────────────────────
  // Every destination on every node: is it keeping up?  Right-click a row for a test message, to send again from a time, or to skip to now.
  VIEWS.shipping = (() => {
    const REFRESH_MS = 5000;
    let box, bad, lastAt = 0, seq = 0, sig = "";
    const count = (v) => (Number(v) || 0).toLocaleString();
    const ago = (u) => { const s = Math.max(0, Math.floor(Date.now() / 1000 - u)); return s < 90 ? s + " s ago" : s < 5400 ? Math.round(s / 60) + " min ago" : s < 172800 ? Math.round(s / 3600) + " h ago" : Math.round(s / 86400) + " d ago"; };
    const behind = (s) => (!s ? "–" : s < 90 ? s + " s" : s < 5400 ? Math.round(s / 60) + " min" : s < 172800 ? (s / 3600).toFixed(1) + " h" : (s / 86400).toFixed(1) + " d");
    const STATE = { ok: ["ok", "OK"], behind: ["warn", "BEHIND"], error: ["bad", "ERROR"], disabled: ["", "OFF"], starting: ["info", "STARTING"] };
    async function act(kind, body, cell) {
      cell.textContent = "…"; cell.className = "muted small";
      try {
        const r = (await api("POST", "/api/logs/shipping/" + kind, body)).data;
        const fails = (r.results || []).filter((x) => !x.ok).map((x) => x.node + ": " + x.error).concat((r.node_errors || []).map((x) => x.node + ": " + x.error));
        cell.textContent = fails.length ? fails.join("; ") : kind === "test" ? "Test sent." : "Moved.";
        cell.className = fails.length ? "bad-text small" : "muted small";
      } catch (e) { if (e.message !== "unauthenticated") { cell.textContent = e.message; cell.className = "bad-text small"; } }
      sig = ""; load();
    }
    function resend(d, cell) {
      dialog("Send again: " + d.name + " on " + d.node, (close) => {
        const from = h("select", {}, [["1h", "The last hour"], ["6h", "The last 6 hours"], ["24h", "The last 24 hours"], ["7d", "The last 7 days"], ["oldest", "Everything kept"]].map(([v, l]) => h("option", { value: v }, l)));
        return [h("form", { onsubmit: (e) => { e.preventDefault(); close(); act("reset", { name: d.name, node: d.addr || d.node, from: from.value }, cell); } },
          h("div", { class: "grid" }, h("label", { class: "f wide" }, "Send again from", from)),
          h("div", { class: "toolbar right" }, h("span", { class: "grow" }), h("button", { type: "submit", class: "btn primary" }, "Send again"), h("button", { type: "button", class: "btn", onclick: close }, "Cancel")))];
      });
    }
    function draw(r) {
      const rows = r.destinations || [];
      const errs = r.node_errors || [];
      const s = JSON.stringify([rows.map((d) => [d.name, d.node, d.state, d.sent, d.per_min, d.behind_s, d.skipped, d.last_sent, d.last_error, d.note]), errs]);
      if (s === sig) return;
      sig = s;
      const notes = new Map([...box.querySelectorAll("td[data-k]")].map((c) => [c.dataset.k, [c.textContent, c.className]]));
      const csv = () => [["Destination", "Node", "Type", "Target", "State", "Sent", "Per minute", "Behind (s)", "Skipped", "Last sent (UTC)", "Last error"],
        ...rows.map((d) => [d.name, d.node, d.type, d.target, d.state, d.sent, d.per_min, d.behind_s, d.skipped, d.last_sent ? new Date(d.last_sent * 1000).toISOString() : "", d.last_error || ""])];
      const body = rows.length ? h("div", { class: "scroll" }, h("table", { class: "shipmon" },
        h("thead", {}, h("tr", {}, ["Destination", "Node", "Type", "Target", "State", "Per min", "Sent", "Behind", "Last sent", "Skipped", ""].map((x, i) => h("th", { class: i >= 5 && i <= 9 && i !== 8 ? "num" : "" }, x)))),
        h("tbody", {}, rows.map((d) => {
          const k = d.name + "\u0000" + (d.addr || d.node), st = STATE[d.state] || ["", d.state.toUpperCase()];
          const cell = h("td", { "data-k": k, class: (notes.get(k) || [])[1] || "muted small" }, (notes.get(k) || [""])[0]);
          const tip = [d.last_error ? "Last error" + (d.error_at ? " (" + ago(d.error_at) + ")" : "") + ": " + d.last_error : "", d.note, "Position: " + d.cursor].filter(Boolean).join("\n");
          return h("tr", { class: st[0] === "bad" ? "bad" : st[0] === "warn" ? "warn" : d.state === "disabled" ? "off" : "", title: tip,
            oncontextmenu: rowMenu([["Send a test message", () => act("test", { name: d.name, node: d.addr || d.node }, cell)], ["Send again from…", () => resend(d, cell)],
              ["Skip to now", () => act("reset", { name: d.name, node: d.addr || d.node, from: "now" }, cell)], "-", ["Edit", () => { state.prefill.shipEdit = d.name; selectTab("cfg_shipping"); }]]) },
            h("td", {}, d.name), h("td", { title: d.addr || "" }, d.node), h("td", {}, d.type), h("td", { class: "mono" }, d.target), h("td", {}, pill(st[1], st[0] + " tiny")),
            h("td", { class: "num" }, count(d.per_min)), h("td", { class: "num" }, count(d.sent)), h("td", { class: "num" }, behind(d.behind_s)),
            h("td", {}, d.last_sent ? ago(d.last_sent) : "never"), h("td", { class: "num" }, d.skipped ? count(d.skipped) : "–"), cell);
        }))))
        : h("div", { class: "empty" }, "No destinations. Add one on Configure ▸ Shipping.");
      const nodeErr = errs.length ? h("span", { class: "bad-text small", title: errs.map((e) => e.node + ": " + e.error).join("\n") }, errs.length + " of " + r.nodes + " nodes did not answer") : null;
      clear(box).append(h("div", { class: "card" }, h("header", { class: "bar" }, h("h2", {}, tc("Shipping")), nodeErr, h("span", { class: "grow" }), rows.length ? csvButton("Shipping", csv) : null), h("div", { class: "body flush" }, body)));
    }
    async function load() {
      const my = ++seq;
      try {
        const r = (await api("GET", "/api/logs/shipping")).data;
        if (my !== seq) return;
        lastAt = Date.now();
        bad.textContent = "";
        draw(r);
      } catch (e) { if (e.message !== "unauthenticated") bad.textContent = e.message; }
    }
    return {
      mount(main) {
        sig = "";
        bad = h("p", { class: "bad-text" });
        box = h("div", {});
        main.append(bad, box);
        load();
      },
      async poll() { if (Date.now() - lastAt >= REFRESH_MS) await load(); },
    };
  })();

  // ── Senders, Monitor  (API: GET /api/logs/senders) ───────────────────────────────
  // Who is sending syslog to the cluster: one row to a sender, its messages added up across the nodes that heard it.
  VIEWS.senders = (() => {
    const REFRESH_MS = 5000;
    const RANGES = [["1h", "Last Hour"], ["1d", "Last Day"], ["7d", "Last Week"], ["30d", "Last Month"], ["custom", "Custom"]];
    const RANGE_WORDS = { "1h": "last hour", "1d": "last day", "7d": "last week", "30d": "last month", custom: "this range" };
    const SEV_COLORS = [["info", "Debug, info, notice", "t6"], ["warning", "Warning", "t4"], ["error", "Error or worse", "t8"]];   // green, amber, red
    let box, bad, seq = 0, lastAt = 0;
    let range = "1h", rangeBtns, fromIn, toIn, customRow, tilesEl, chartsEl, stats = null, statsAt = 0, statsSeq = 0;
    const count = (v) => (Number(v) || 0).toLocaleString();
    const ago = (u) => { const s = Math.max(0, Math.floor(Date.now() / 1000 - u)); return s < 90 ? s + " s ago" : s < 5400 ? Math.round(s / 60) + " min ago" : s < 172800 ? Math.round(s / 3600) + " h ago" : Math.round(s / 86400) + " d ago"; };
    const only = (addr) => { state.prefill.logs = addr; selectTab("logs"); };   // the Logs page, on that sender's messages
    // the filter, pager and CSV button are made once (a refresh only swaps the rows, so typing in the box is not interrupted)
    const kept = { q: "", page: 0, size: 25 };
    let rows = [], card = null, filter, countEl, pageIn, pagesEl, pager, sizeSel, bodyEl, csv, btns;
    const hay = (e) => [e.rev, e.name, e.rdns, e.addr, e.host, e.via, e.node].filter(Boolean).join(" ").toLowerCase();
    const visible = () => { const flt = parseFilter(kept.q); return flt.empty ? rows : rows.filter((e) => flt.match((w) => hay(e).includes(w))); };
    function build() {
      const mkBtn = (label, title, to) => h("button", { type: "button", class: "btn small", title, "aria-label": title, onclick: () => { kept.page = to(); render(); } }, label);
      btns = [mkBtn("«", "First page", () => 0), mkBtn("‹", "Previous page", () => kept.page - 1), mkBtn("›", "Next page", () => kept.page + 1), mkBtn("»", "Last page", () => 1e9)];
      filter = h("input", { type: "text", class: "policy-filter", placeholder: "Filter rows", "aria-label": "Filter senders", autocomplete: "off", spellcheck: "false", value: kept.q,
        oninput: () => { kept.q = filter.value; kept.page = 0; render(); } });
      countEl = h("span", { class: "policy-count muted" }, "");
      pageIn = h("input", { type: "number", min: "1", class: "policy-page", "aria-label": "Page number", onchange: (e) => { kept.page = (parseInt(e.target.value, 10) || 1) - 1; render(); } });
      pagesEl = h("span", { class: "muted" }, "");
      pager = h("span", { class: "policy-pager" }, btns[0], btns[1], pageIn, pagesEl, btns[2], btns[3]);
      sizeSel = h("select", { "aria-label": "Senders per page", onchange: (e) => { kept.size = Number(e.target.value); kept.page = 0; render(); } },
        [25, 50, 100, 250, 500].map((n) => h("option", { value: String(n) }, n + " per page")));
      sizeSel.value = String(kept.size);
      csv = csvButton("Log senders", () => [["Sender", "Name", "DNS name", "Host", "Via", "First seen (UTC)", "Last seen (UTC)", "Messages", "Bytes", "Node"],
        ...visible().map((e) => [e.addr, e.name || "", e.rdns || "", e.host || "", e.via || "", csvIso(e.first_seen), csvIso(e.last_seen), e.messages, e.bytes, e.node || ""])]);
      bodyEl = h("div", { class: "body flush" });
      card = h("div", { class: "card" }, h("header", { class: "bar" }, h("h2", {}, tc("Senders")), h("span", { class: "grow" }), csv),
        h("div", { class: "body" }, h("div", { class: "policy-bar" }, filter, countEl, h("span", { class: "grow" }), pager, sizeSel)), bodyEl);
      return card;
    }
    function render() {
      const vis = visible(), size = kept.size, n = Math.max(1, Math.ceil(vis.length / size));
      kept.page = Math.min(Math.max(kept.page, 0), n - 1);
      const from = kept.page * size, list = vis.slice(from, from + size), filtered = vis.length !== rows.length;
      countEl.textContent = vis.length ? (from + 1) + "–" + Math.min(from + size, vis.length) + " of " + vis.length + (filtered ? " (filtered from " + rows.length + ")" : "") : (filtered ? "0 of " + rows.length : "");
      pager.hidden = n < 2;
      pageIn.value = String(kept.page + 1); pageIn.max = String(n); pagesEl.textContent = "of " + n;
      btns[0].disabled = btns[1].disabled = kept.page === 0;
      btns[2].disabled = btns[3].disabled = kept.page >= n - 1;
      csv.hidden = !vis.length;
      clear(bodyEl).append(list.length
        ? h("div", { class: "scroll" }, h("table", {},
          h("thead", {}, h("tr", {}, ["Name", "Address", "Proto", "Last seen", "Messages", "Node"].map((x, i) => h("th", { class: i === 4 ? "num" : "" }, x)))),
          h("tbody", {}, list.map((e) => h("tr", { class: Date.now() / 1000 - e.last_seen > 600 ? "muted" : "", oncontextmenu: rowMenu([["Only this sender", () => only(e.addr)], ["Ship this sender…", () => { state.prefill.ship = { type: "syslog", senders: [e.addr] }; selectTab("cfg_shipping"); }], "-", ...netToolMenu(e.addr)]) },
            h("td", { title: [e.rdns ? "DNS name: " + e.rdns : "", e.name && e.name !== e.rev ? "Senders entry: " + e.name : "", e.host ? "Its messages say: " + e.host : ""].filter(Boolean).join("\n") }, e.rev || e.name || e.host || "–"),
            h("td", { class: "mono" }, e.addr), h("td", {}, e.via || "–"),
            h("td", {}, ago(e.last_seen)), h("td", { class: "num" }, count(e.messages)), h("td", { class: "mono" }, e.node || "–"))))))
        : h("div", { class: "empty" }, rows.length ? "No sender matches." : "No sender has sent anything yet."));
    }
    const nameOf = (addr) => { const e = rows.find((x) => x.addr === addr); return (e && (e.rev || e.name || e.host)) || addr; };
    // the charts and tiles are not worth a fresh scan of the stored messages every 5 s: a day or more is read once a minute
    const statsEvery = () => (range === "1h" ? 15000 : 60000);
    const rate = (v) => (v >= 100 ? Math.round(v) : v >= 10 ? v.toFixed(1) : v.toFixed(2).replace(/0$/, "")) + "/s";
    function drawStats() {
      if (!tilesEl) return;
      const st = stats;
      if (!st) { clear(tilesEl); clear(chartsEl); return; }
      const secs = Math.max(1, (st.to - st.from) / 1000), pts = st.series || [], step = st.step_s || 60, per = step / 60;
      const tile = (cls, label, value, sub, title) => h("div", { class: "qtile qtrunc " + cls, title: title || null }, h("div", { class: "qv trunc" }, value), h("div", { class: "qs trunc" }, sub || "\u00a0"), h("div", { class: "ql" }, label));
      const peak = pts.reduce((m, p) => Math.max(m, p.count), 0) / step;
      const errs = ["emerg", "alert", "crit", "err"].reduce((a, k) => a + ((st.by_severity || {})[k] || 0), 0);
      const silent = rows.filter((e) => Date.now() / 1000 - e.last_seen > 600);
      const top = (st.top_sources || [])[0];
      clear(tilesEl).append(
        tile("t1", "Senders heard", count(st.sources || 0), "of " + count(rows.length) + " known"),
        tile("t3", "Message rate", rate(st.total / secs), "peak " + rate(peak)),
        tile("t2", "Messages", count(st.total), RANGE_WORDS[range]),
        tile(errs ? "t8" : "t6", "Errors or worse", st.total ? (errs / st.total * 100).toFixed(errs / st.total < 0.1 ? 1 : 0) + "%" : "–", count(errs) + " of " + count(st.total)),
        tile(silent.length ? "t4" : "t6", "Silent", count(silent.length), silent.length ? "none for 10 min" : "all heard in 10 min", silent.map((e) => e.rev || e.name || e.addr).join(", ")),
        tile("t5", "Busiest", top ? nameOf(top.name) : "–", top && st.total ? Math.round(top.count / st.total * 100) + "% of messages" : "", top ? nameOf(top.name) + " (" + top.name + ")" : ""));
      if (!pts.length) { clear(chartsEl).append(h("div", { class: "empty" }, "No messages in this range.")); return; }
      const d = { n: pts.length, start: pts[0].t, step, from: st.from / 1000, to: st.to / 1000 };
      const perMin = (a) => a.map((c) => c / per);
      const fmt = (v) => (v >= 100 ? Math.round(v) : v.toFixed(v < 10 ? 1 : 0)).toLocaleString();
      const unit = "messages per minute", axis = (v) => (Number.isInteger(v) ? v : v.toFixed(1)).toLocaleString();
      const topS = (st.top_sources || []).filter((c) => (st.source_series || {})[c.name]).slice(0, 5);
      const cls = ["t1", "t2", "t3", "t4", "t5"];
      clear(chartsEl).append(
        QCHARTS.lineChart("Messages per minute", d, [{ label: "All senders", cls: "t1", values: perMin(pts.map((p) => p.count)), text: (i) => fmt(pts[i].count / per) + " per minute" }], { unit, floor: 10, axis }),
        QCHARTS.lineChart("Top senders", d, topS.map((c, k) => { const v = perMin(st.source_series[c.name]); return { label: nameOf(c.name), cls: cls[k], values: v, text: (i) => fmt(v[i]) + " per minute" }; }), { unit, floor: 10, axis, legend: true, legendBelow: true, pick: senderPick }),
        QCHARTS.lineChart("By severity", d, SEV_COLORS.map(([k, label, c]) => { const v = perMin((st.severity_series || {})[k] || pts.map(() => 0)); return { label, cls: c, values: v, text: (i) => fmt(v[i]) + " per minute" }; }), { unit, floor: 10, axis, legend: true }),
        QCHARTS.lineChart("Senders heard", d, [{ label: "Senders", cls: "t3", values: (st.heard || []).map(Number), text: (i) => count(st.heard[i]) + (st.heard[i] === 1 ? " sender" : " senders") }], { unit: "senders", floor: 4, axis: (v) => (Number.isInteger(v) ? v : v.toFixed(1)) }));
    }
    async function loadStats() {
      const my = ++statsSeq;
      try {
        let qs = "range=" + range;
        if (range === "custom") {
          const a = new Date(fromIn.value), b = new Date(toIn.value);
          if (isNaN(a) || isNaN(b) || a >= b) throw new Error("Pick a start that is before the end.");
          qs = "from=" + Math.floor(a / 1000) + "&to=" + Math.floor(b / 1000);
        }
        const r = await api("GET", "/api/logs/stats?" + qs + "&series=1&top=20");
        if (my !== statsSeq) return;
        statsAt = Date.now(); stats = r.data; drawStats();
      } catch (e) { if (my === statsSeq) { stats = null; clear(tilesEl); fail(clear(chartsEl))(e); } }
    }
    function setRange(r) {
      range = r;
      for (const [id, b] of rangeBtns) b.setAttribute("aria-pressed", id === r ? "true" : "false");
      customRow.classList.toggle("hidden", r !== "custom");
      if (r === "custom" && !fromIn.value) {
        const f = (d) => new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
        toIn.value = f(new Date()); fromIn.value = f(new Date(Date.now() - 6 * 3600 * 1000));
      }
      if (r !== "custom") loadStats();
    }
    function draw(senders) {
      // one row per sender: a sender heard by two nodes (its messages move with the active gateway) is one, with the messages added up and the nodes listed
      const by = new Map();
      for (const e of senders.slice().sort((a, b) => b.last_seen - a.last_seen)) {
        const m = by.get(e.addr);
        if (!m) { by.set(e.addr, { ...e, nodes: e.node ? [e.node] : [] }); continue; }
        m.messages += e.messages || 0; m.bytes += e.bytes || 0;
        m.first_seen = Math.min(m.first_seen || e.first_seen, e.first_seen || m.first_seen);
        if (!m.name && e.name) m.name = e.name;
        if (!m.host && e.host) m.host = e.host;
        if (!m.rdns && e.rdns) m.rdns = e.rdns;
        if (!m.rev && e.rev) m.rev = e.rev;
        if (e.node && !m.nodes.includes(e.node)) m.nodes.push(e.node);
      }
      // alphabetical by the name shown in the first column (10.0.0.2 before 10.0.0.10), then by address
      const nm = (e) => String(e.rev || e.name || e.host || e.addr || "");
      rows = [...by.values()].map((e) => ({ ...e, node: e.nodes.join(", ") }))
        .sort((a, b) => nm(a).localeCompare(nm(b), undefined, { numeric: true, sensitivity: "base" }) || String(a.addr).localeCompare(String(b.addr), undefined, { numeric: true }));
      if (!card) box.append(build());
      render();
      if (stats) drawStats();   // the names and the silent count come from this list
    }
    async function load() {
      const my = ++seq;
      try {
        const r = await api("GET", "/api/logs/senders");
        if (my !== seq) return;
        lastAt = Date.now();
        clear(bad);
        draw((r.data && r.data.senders) || []);
      } catch (e) { fail(bad)(e); }
      if (range !== "custom" && Date.now() - statsAt >= statsEvery()) loadStats();
    }
    return {
      mount(main) {
        bad = h("div", { "aria-live": "polite" }); box = h("div", {}); card = null; stats = null; statsAt = 0;
        rangeBtns = new Map(RANGES.map(([id, label]) => [id, h("button", { type: "button", class: "seg", "aria-pressed": id === range ? "true" : "false", onclick: () => setRange(id) }, label)]));
        fromIn = h("input", { type: "datetime-local", "aria-label": "From" });
        toIn = h("input", { type: "datetime-local", "aria-label": "To" });
        customRow = h("span", { class: "qcustom hidden" }, fromIn, " to ", toIn,
          h("button", { class: "btn", type: "button", onclick: loadStats }, "Apply"), h("span", { class: "muted small" }, " only the last 30 days exist"));
        const bar = h("div", { class: "toolbar qbar" }, h("span", { class: "segs", role: "group", "aria-label": "Time range" }, [...rangeBtns.values()]), customRow);
        tilesEl = h("div", { class: "qtiles" }); chartsEl = h("div", { class: "hostcharts" });
        main.append(bad, bar, tilesEl, chartsEl, box);
        load();
        setRange(range);
      },
      async poll() { if (Date.now() - lastAt >= REFRESH_MS) await load(); },
    };
  })();

  // ── MCP, Monitor  (API: GET /api/mcpusage) ──────────────────────────────────────
  // What the MCP endpoint is used for on the picked node, laid out like Monitor ▸ Node: a time range, tiles, charts per hour, and cards
  // with calls by key, by source address, by tool and by device, then the newest calls.  Counted by the daemon (mcpusage.go);
  // nothing a caller sent or was answered is kept.
  VIEWS.mcpusage = (() => {
    const SVGNS = "http://www.w3.org/2000/svg";
    const sv = (tag, attrs, ...kids) => {
      const el = document.createElementNS(SVGNS, tag);
      for (const [k, v] of Object.entries(attrs || {})) if (v != null && v !== false) el.setAttribute(k, v);
      for (const kid of kids.flat(Infinity)) if (kid != null) el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
      return el;
    };
    const RANGES = [["1h", "Last Hour"], ["24h", "Last Day"], ["7d", "Last Week"]];
    const REFRESH_MS = 5000;
    const kept = { win: "24h" };
    let status, bar, tiles, charts, tables, recent, foot, rangeBtns, win = kept.win, seq = 0, lastAt = 0, last = "";
    const size = (n) => (n < 1024 ? n + " B" : n < 1048576 ? (n / 1024).toFixed(1) + " KB" : n < 1073741824 ? (n / 1048576).toFixed(1) + " MB" : (n / 1073741824).toFixed(2) + " GB");
    const at = (u) => (u ? new Date(u * 1000).toLocaleString() : "–");
    const num = (n) => n.toLocaleString();
    const hm = (u, long) => new Date(u * 1000).toLocaleString([], long ? { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false } : { hour: "2-digit", minute: "2-digit", hour12: false });
    // csv: the rows of the card's download, or null when it has none to give
    const card = (title, csv, ...kids) => h("div", { class: "card" }, h("header", { class: "bar" }, h("h2", {}, tc(title)), h("span", { class: "grow" }), csv ? csvButton(title, csv) : null), h("div", { class: "body flush" }, ...kids));
    const sumCsv = (first, rows) => () => [[first, "Calls", "Errors", "Refused", "Changes", "Data (bytes)", "Avg ms", "Last (UTC)"],
      ...rows.map((r) => [r.name, r.calls, r.errors, r.refused, r.writes, r.bytes, r.avg_ms, r.last ? csvIso(r.last) : ""])];
    const COLS = [["Calls", (r) => num(r.calls), true], ["Errors", (r) => (r.errors ? num(r.errors) : "–"), true], ["Refused", (r) => (r.refused ? num(r.refused) : "–"), true],
      ["Changes", (r) => (r.writes ? num(r.writes) : "–"), true], ["Data", (r) => size(r.bytes), true], ["Avg ms", (r) => num(r.avg_ms), true], ["Last", (r) => at(r.last), false]];
    function sumCard(title, first, rows) {
      return card(title, rows.length ? sumCsv(first, rows) : null, rows.length
        ? h("div", { class: "scroll" }, h("table", {},
          h("thead", {}, h("tr", {}, h("th", {}, first), COLS.map((c) => h("th", { class: c[2] ? "num" : "" }, c[0])))),
          h("tbody", {}, rows.map((r) => h("tr", {}, h("td", { class: "mono" }, r.name), COLS.map((c) => h("td", { class: c[2] ? "num" : "" }, c[1](r))))))))
        : h("div", { class: "empty" }, "No calls in this time range."));
    }
    // bars per hour; red is the part that failed
    function bars(title, hours, cls, pick, fail2, text, csv) {
      const W = 620, H = 230, L = 12, R = 12, T = 12, B = 28, n = hours.length, max = Math.max(1, ...hours.map(pick));
      const pw = W - L - R, ph = H - T - B, bw = pw / Math.max(1, n);
      const svg = sv("svg", { class: "qchart", viewBox: "0 0 " + W + " " + H, role: "img", "aria-label": title });
      for (let g = 0; g <= 4; g++) svg.append(sv("line", { class: "qgrid", x1: L, x2: W - R, y1: T + ph - (ph * g) / 4, y2: T + ph - (ph * g) / 4 }));
      hours.forEach((x, i) => {
        const v = pick(x), hh = Math.round((v / max) * ph), eh = fail2 ? Math.round((fail2(x) / max) * ph) : 0;
        svg.append(sv("g", {}, sv("title", {}, hm(x.t, true) + ": " + text(x)),
          sv("rect", { class: "ubar " + cls, x: (L + i * bw + 0.5).toFixed(1), y: T + ph - hh, width: Math.max(1, bw - 1).toFixed(1), height: hh }),
          eh ? sv("rect", { class: "uerr t3", x: (L + i * bw + 0.5).toFixed(1), y: T + ph - eh, width: Math.max(1, bw - 1).toFixed(1), height: eh }) : null));
      });
      svg.append(sv("text", { class: "qax", x: L, y: H - 8 }, hm(hours[0].t, n > 24)), sv("text", { class: "qax", x: W - R, y: H - 8, "text-anchor": "end" }, hm(hours[n - 1].t, n > 24)),
        sv("text", { class: "qax", x: W - R, y: T + 10, "text-anchor": "end" }, "most " + max.toLocaleString()));
      return h("div", { class: "card" }, h("header", { class: "bar" }, h("h2", {}, tc(title)), h("span", { class: "grow" }), csvButton(title, csv)), h("div", { class: "body" }, svg));
    }
    function draw(r) {
      const sig = JSON.stringify(r);
      if (sig === last) return;
      last = sig;
      const t = r.totals;
      const tile = (cls, label, value, sub) => h("div", { class: "qtile " + cls }, h("div", { class: "qv" }, value), h("div", { class: "qs" }, sub || "\u00a0"), h("div", { class: "ql" }, label));
      const per = r.window_hours ? t.calls / r.window_hours : 0;
      clear(tiles).append(
        tile("t1", "Calls", num(t.calls), per ? (per < 10 ? per.toFixed(1) : Math.round(per)) + " an hour" : ""),
        tile("t3", "Errors", num(t.errors), t.calls ? Math.round((t.errors * 100) / t.calls) + "% of calls" : ""),
        tile("t2", "Refused", num(t.refused), "read-only callers asking to change"),
        tile("t4", "Changes", num(t.writes), "that went through"),
        tile("t5", "Data returned", size(t.bytes), "after privacy masking"),
        tile("t6", "Average time", num(t.avg_ms) + " ms", "per call on this node"));
      clear(charts).append(
        bars("Calls per hour", r.hours, "t1", (x) => x.calls, (x) => x.errors, (x) => x.calls + " call" + (x.calls === 1 ? "" : "s") + (x.errors ? ", " + x.errors + " failed" : ""),
          () => [["Hour (UTC)", "Calls", "Errors"], ...r.hours.map((x) => [csvIso(x.t), x.calls, x.errors])]),
        bars("Data returned per hour", r.hours, "t5", (x) => x.bytes, null, (x) => size(x.bytes),
          () => [["Hour (UTC)", "Data (bytes)"], ...r.hours.map((x) => [csvIso(x.t), x.bytes])]));
      clear(tables).append(sumCard("By key", "Key", r.by_key), sumCard("By source address", "Address", r.by_source), sumCard("By tool", "Tool", r.by_tool));
      clear(recent).append(card("Newest calls", r.recent.length ? () => [["Time (UTC)", "Key", "Address", "Tool", "Target", "Result", "Change", "Ms", "Data (bytes)"],
        ...r.recent.map((c) => [new Date(c.time).toISOString(), c.caller, c.addr, c.tool, c.target || "", c.result, c.write ? "yes" : "no", c.ms, c.bytes])] : null, r.recent.length
        ? h("div", { class: "scroll" }, h("table", {},
          h("thead", {}, h("tr", {}, ["Time", "Key", "Address", "Tool", "Target", "Result", "Ms", "Data"].map((x, i) => h("th", { class: i >= 6 ? "num" : "" }, x)))),
          h("tbody", {}, r.recent.map((c) => h("tr", {}, h("td", {}, new Date(c.time).toLocaleTimeString([], { hour12: false })), h("td", { class: "mono" }, c.caller), h("td", { class: "mono" }, c.addr),
            h("td", { class: "mono" }, c.tool), h("td", { class: "mono" }, c.target || ""), h("td", { class: c.result === "ok" ? "" : "bad" }, c.result + (c.write && c.result === "ok" ? " (change)" : "")),
            h("td", { class: "num" }, String(c.ms)), h("td", { class: "num" }, size(c.bytes)))))))
        : h("div", { class: "empty" }, "No calls since the daemon started.")));
      foot.textContent = r.note;
    }
    async function load() {
      const my = ++seq;
      try {
        const r = await api("GET", "/api/mcpusage?window=" + win);   // the answer is the report itself, not wrapped in {ok, data}
        if (my !== seq) return;
        lastAt = Date.now();
        clear(status);
        draw(r);
      } catch (e) { fail(status)(e); }
    }
    function setRange(id) {
      win = kept.win = id; last = "";
      for (const [k, b] of rangeBtns) b.setAttribute("aria-pressed", k === id ? "true" : "false");
      load();
    }
    return {
      mount(main) {
        last = ""; win = kept.win;
        status = h("div", { "aria-live": "polite" });
        rangeBtns = new Map(RANGES.map(([id, label]) => [id, h("button", { type: "button", class: "seg", "aria-pressed": id === win ? "true" : "false", onclick: () => setRange(id) }, label)]));
        bar = h("div", { class: "toolbar qbar" }, h("span", { class: "segs", role: "group", "aria-label": "Time range" }, [...rangeBtns.values()]));
        tiles = h("div", { class: "qtiles" }); charts = h("div", { class: "hostcharts" }); tables = h("div", { class: "hosttables" }); recent = h("div", {}); foot = h("p", { class: "hint" });
        main.append(status, bar, tiles, charts, tables, recent, foot);
        load();
      },
      async poll() { if (Date.now() - lastAt >= REFRESH_MS) await load(); },
    };
  })();

  // ── Capture, Monitor  (CLI: --capture, --capture-interfaces) ────────────────────
  // One node: a tcpdump-like capture on the node picked in the Node menu, shown live and downloadable as a .tgz (one .pcap in it).
  // Cluster: the same capture on every node at once for a chosen time, one .pcap per node in a .tgz.
  VIEWS.capture = (() => {
    let keptFilter = "";   // the filter text survives a change of node (the page is built again for each)
    let status, iface, filter, info, box, startBtn, dlBtn, ifs = null, cursor = 0, shown = 0, last = null, running = false;
    let job = null, jobBox, durSel, goBtn, dlJob;
    const ROWS = 1500;
    const cluster = many;
    const sizeText = (n) => (n < 1024 ? n + " bytes" : n < 1048576 ? Math.round(n / 1024) + " KB" : (n / 1048576).toFixed(1) + " MB");

    const ifaceSelect = () => h("select", { "aria-label": "Interface", class: "capif" });
    async function loadIfaces() {
      const r = (await api("GET", "/api/capture/interfaces")).data;
      ifs = r;
      const gw = new Set(r.gateway || []);
      const keep = iface.value;
      clear(iface).append(...r.interfaces.map((i) => h("option", { value: i.name }, i.name + (gw.has(i.name) ? " (gateway)" : "") + (i.up ? "" : " (down)"))));
      const pick = [keep, last && last.iface, ...(r.gateway || []), (r.interfaces.find((i) => i.up && i.name !== "lo") || {}).name, (r.interfaces[0] || {}).name].find((n) => n && r.interfaces.some((i) => i.name === n));
      if (pick) iface.value = pick;
    }
    function addRows(pk) {
      const stick = box.scrollHeight - box.scrollTop - box.clientHeight < 40;
      if (box.firstChild && box.firstChild.classList && box.firstChild.classList.contains("empty")) clear(box);
      for (const p of pk) {
        const arp = /^(ARP|ICMPv6 .*neighbor)/.test(p.summary) || p.summary.startsWith("ARP");
        box.append(h("div", { class: "capline" + (arp ? " arp" : ""), title: p.len + " bytes" },
          h("span", { class: "ct" }, p.time), h("span", { class: "cs" }, p.summary)));
      }
      shown += pk.length;
      while (box.childNodes.length > ROWS) { box.removeChild(box.firstChild); }
      if (stick) box.scrollTop = box.scrollHeight;
    }
    function empty(text) { clear(box).append(h("div", { class: "empty" }, text)); shown = 0; }
    function setButtons() {
      startBtn.textContent = running ? "Stop" : "Start";   // one button: it starts a capture, and stops a running one
      dlBtn.disabled = !(last && last.count > 0);
    }
    async function refresh(full) {
      const q = full ? 0 : cursor;
      const st = (await api("GET", "/api/capture/packets?since=" + q)).data;
      if (st.cursor < cursor || (last && st.iface !== last.iface && !full)) return refresh(true); // another capture began, or this one was reset
      if (full) { clear(box); shown = 0; }
      if (st.packets.length) addRows(st.packets);
      cursor = st.cursor;
      last = st;
      running = st.running;
      if (!shown && !box.firstChild) empty(st.iface ? "Waiting for packets on " + st.iface + (st.filter ? " matching: " + st.filter : "") + "…" : "Not capturing. Choose an interface and press Start.");
      clear(info).append(!st.iface ? "" :
        (st.running ? "Capturing on " : "Stopped on ") + st.iface + (st.filter ? " · filter: " + st.filter : "") + " · " + st.count + " packet" + (st.count === 1 ? "" : "s") + " kept" +
        (st.seen > st.matched ? " (" + st.seen + " seen, the filter turned away " + (st.seen - st.matched) + ")" : "") +
        " · " + sizeText(st.bytes) + " in the buffer, the newest 5000 or 32 MB");
      setButtons();
    }
    async function start() {
      if (running) return;   // Start is greyed out while capturing; Enter in the filter box does nothing either
      try {
        await api("POST", "/api/capture/start", { iface: iface.value, filter: filter.value.trim() });
        say(status, "ok"); cursor = 0; last = null; empty("Waiting for packets…");
        await refresh(true);
      } catch (e) { fail(status)(e); }
    }
    async function stop() { try { await api("POST", "/api/capture/stop", {}); await refresh(false); } catch (e) { fail(status)(e); } }
    async function clearAll() { try { await api("POST", "/api/capture/clear", {}); empty(running ? "Waiting for packets…" : "Cleared."); await refresh(false); } catch (e) { fail(status)(e); } }

    // ── every node ──
    function drawJob() {
      clear(jobBox);
      if (!job || job.none) return;
      // Nothing to say when it went well (the table does): only while it runs, and when a node could not capture
      const failed = (job.nodes || []).filter((n) => n.status !== "done").length;
      if (job.done && job.error) jobBox.append(h("div", { class: "notice bad", role: "alert" }, job.error));
      else if (job.done && failed) jobBox.append(h("div", { class: "notice bad", role: "alert" }, failed + " of " + job.nodes.length + " nodes could not capture (see the list); the others are in the download."));
      if (job.nodes && job.nodes.length) {
        jobBox.append(h("div", { class: "scroll" }, h("table", {},
          h("thead", {}, h("tr", {}, ["Node", "Interface", "Result", "Packets kept", "Seen", "Size", "Why not"].map((t, i) => h("th", { class: i >= 3 && i <= 5 ? "num" : "" }, t)))),
          h("tbody", {}, job.nodes.map((n) => h("tr", {},
            h("td", {}, n.name, n.self ? " ★" : ""), h("td", { class: "mono" }, n.iface),
            h("td", {}, pill(n.status, n.status === "done" ? "ok" : n.status === "error" ? "bad" : "")),
            h("td", { class: "num" }, n.status === "done" ? String(n.kept) : "–"), h("td", { class: "num" }, n.status === "done" ? String(n.seen) : "–"),
            h("td", { class: "num" }, n.status === "done" ? sizeText(n.bytes) : "–"),
            h("td", {}, n.error || "")))))));
      }
      dlJob.disabled = !(job.done && job.ready);
      goBtn.disabled = !job.done;
    }
    async function pollJob() {
      const r = (await api("GET", "/api/clustercapture/status")).data;
      job = r.none ? null : r;
      drawJob();
    }
    async function goAll() {
      try {
        const gw = pickedGw();
        await api("POST", "/api/clustercapture/start", gw ? { gw: String(gw.group_id), filter: filter.value.trim(), seconds: Number(durSel.value) } : { iface: iface.value, filter: filter.value.trim(), seconds: Number(durSel.value) });
        say(status, "ok");
        await pollJob();
      } catch (e) { fail(status)(e); }
    }

    return {
      async mount(main) {
        cursor = 0; shown = 0; last = null; running = false; job = null; ifs = null;
        status = h("div", { "aria-live": "polite" });
        iface = ifaceSelect();
        filter = h("input", { type: "text", class: "grow", placeholder: "Filter, e.g. host 10.20.0.205 and port 123", "aria-label": "Capture filter", spellcheck: "false", autocomplete: "off", maxlength: "300",
          onkeydown: (e) => { if (e.key === "Enter") { e.preventDefault(); (cluster() ? goAll : start)(); } } });
        filter.value = keptFilter;
        if (state.prefill.capture) { filter.value = state.prefill.capture; state.prefill.capture = null; }   // sent here from a server's menu: only Start is left
        keptFilter = filter.value;
        filter.addEventListener("input", () => { keptFilter = filter.value; });
        if (cluster()) {
          durSel = h("select", { "aria-label": "How long" }, [["5", "5 seconds"], ["10", "10 seconds"], ["30", "30 seconds"], ["60", "60 seconds"]].map(([v, t]) => h("option", { value: v }, t)));
          durSel.value = "10";
          goBtn = h("button", { class: "btn primary", type: "button", onclick: goAll }, "Capture on all nodes");
          dlJob = h("button", { class: "btn", type: "button", disabled: true, onclick: () => apiDownload("/api/clustercapture/download", "logdir-cluster-capture.tgz").catch(fail(status)) }, "Download .tgz");
          jobBox = h("div", {});
          const gw = pickedGw();
          if (gw) goBtn.textContent = "Capture on its nodes";
          main.append(status, section(gw ? "Capture on gateway " + gwTitle(gw) : "Capture on every node",
            h("div", { class: "toolbar tight logbar" }, gw ? h("span", { class: "mono small" }, gw.interface) : iface, filter, durSel, goBtn, dlJob), jobBox));
          if (!gw) await loadIfaces().catch(fail(status));
          await pollJob().catch(fail(status));
          return;
        }
        startBtn = h("button", { class: "btn primary", type: "button", onclick: () => (running ? stop() : start()) }, "Start");
        dlBtn = h("button", { class: "btn", type: "button", disabled: true, onclick: () => apiDownload("/api/capture/download", "logdir-capture.tgz").catch(fail(status)) }, "Download .tgz");
        info = h("div", { class: "hint nomargin" });
        box = h("div", { class: "logbox capbox", tabindex: "0", role: "log", "aria-label": "Captured packets" });
        main.append(status, section("Packet capture",
          h("div", { class: "toolbar tight logbar" }, iface, filter, startBtn,
            h("button", { class: "btn", type: "button", onclick: clearAll }, "Clear"), dlBtn),
          box, info));
        empty("Not capturing. Choose an interface and press Start.");
        try { await loadIfaces(); await refresh(true); } catch (e) { fail(status)(e); }
      },
      async poll() {
        if (cluster()) { if (job && !job.done) await pollJob(); return; }
        if (running || (last && !last.iface)) await refresh(false);
      },
      poll_ms: 1000,
    };
  })();

  // ── Anycast, tab strip shared by Monitor and Operate: BGP | OSPF (Configure builds its own) ──
  function anycastTabs(key, label, panes) {
    const NAMES = ["BGP", "OSPF"];
    const buttons = NAMES.map((name, i) => h("button", { type: "button", role: "tab", class: "dtab", onclick: () => show(i) }, name));
    function show(i, focus) {
      state[key] = i;
      refreshHelp();
      panes.forEach((p, j) => { p.hidden = j !== i; });
      buttons.forEach((b, j) => { b.setAttribute("aria-selected", j === i ? "true" : "false"); b.tabIndex = j === i ? 0 : -1; b.classList.toggle("on", j === i); });
      if (focus) buttons[i].focus();
    }
    const strip = h("div", { class: "dtabs", role: "tablist", "aria-label": label }, buttons);
    strip.addEventListener("keydown", (e) => {
      const d = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
      if (!d) return;
      e.preventDefault();
      show((state[key] + d + NAMES.length) % NAMES.length, true);
    });
    show(state[key] >= 0 && state[key] < NAMES.length ? state[key] : 0);
    return strip;
  }
  const BOTH_BLOCKED = "BGP and OSPF are unavailable: AnyName is installed on this host.";

  // ── Anycast, Monitor  (CLI: --bgp, --ospf) ──────────────────────────────────────────────
  VIEWS.anycaststatus = (() => {
    let boxB, boxO;
    const stateKind = (st) => (st === "Established" ? "ok" : st === "Active" || st === "Connect" || st === "OpenSent" || st === "OpenConfirm" ? "warn" : "bad");
    const ospfKind = (st) => (st === "Full" ? "ok" : st === "Down" || st === "Attempt" ? "bad" : "warn");
    const bfdKind = (st) => (st === "up" ? "ok" : st === "init" ? "warn" : st === "down" ? "bad" : "");
    // the time beside a BFD pill: how long the router takes to drop this node once it goes silent
    const bfdTip = (t) => "The router drops this node after " + t.detect_ms + " ms without a BFD packet (" + t.mult + " × " + Math.round(t.detect_ms / t.mult) + " ms)." +
      (t.peer_detect_ms ? " This node drops the router after " + t.peer_detect_ms + " ms." : "");
    const applied = (a) => ["Last apply", h("span", {}, pill(a.ok ? "ok" : "failed", a.ok ? "ok" : "bad"), ...(a.ok ? [] : [" ", a.detail || "–"]))];

    function draw(st, os) {
      for (const [which, fn] of [["B", buildBGP], ["O", buildOSPF]]) {
        const real = which === "B" ? boxB : boxO, tmp = h("div", {});
        try { fn(tmp, st, os); } finally { morph(real, tmp); }
      }
    }
    function addresses(box, st) {
      box.append(section("Anycast addresses", h("div", { class: "scroll" }, h("table", {},
        h("thead", {}, h("tr", {}, ["Address", "State", "Why"].map((t) => h("th", {}, t)))),
        h("tbody", {}, st.addresses.length ? st.addresses.map((a) => h("tr", {},
          h("td", { class: "mono" }, a.addr),
          h("td", {}, a.up ? pill("announced", "ok") : pill("withdrawn", "")),
          h("td", {}, a.up ? "" : a.reason || "")))
          : h("tr", {}, h("td", { colspan: 3, class: "empty" }, "None: add an anycast address to a gateway on the Topology page.")))))));
    }
    // built into a detached copy and morphed in: the page keeps its elements (and so its scroll boxes) from one poll to the next
    function buildBGP(box, st) {
      const c = st.config;
      if (st.blocked) { box.append(section("BGP", h("div", { class: "muted" }, BOTH_BLOCKED))); return; }
      if (!c.asn) { box.append(section("BGP", h("div", { class: "muted" }, "BGP is off on this node."))); return; }
      if (c.disabled) { box.append(section("BGP", h("div", { class: "muted" }, "BGP is disabled on this node (AS " + c.asn + ")."))); return; }
      box.append(section("Status", kv([
        ["Local AS", String(c.asn) + (c.router_id ? " · router id " + c.router_id : "")],
        ["FRR", st.installed ? pill("installed", "ok") : pill("not installed", "bad")],
        st.installed ? ["bgpd", st.running ? pill("answering", "ok") : pill("not answering", "bad")] : null,
        applied(st.applied),
      ]), ...(st.notes || []).map((n) => h("div", { class: "muted" }, n))));
      const live = new Map(st.peers.map((p) => [p.peer, p]));
      const row = (n) => {
        const p = live.get(n.peer);
        return h("tr", {}, h("td", { class: "mono" }, n.peer), h("td", {}, String(n.remote_as)), h("td", {}, n.description || ""),
          h("td", {}, n.disabled ? pill("disabled", "") : p ? pill(p.state, stateKind(p.state)) : pill(st.running ? "not known yet" : "–", "")),
          h("td", {}, p && p.bfd ? pill(p.bfd, bfdKind(p.bfd)) : "–", p && p.bfd_timers ? h("span", { class: "hint", title: bfdTip(p.bfd_timers) }, " " + p.bfd_timers.detect_ms + " ms") : null),
          h("td", {}, p && p.state === "Established" ? p.uptime : "–"),
          h("td", {}, p && p.state === "Established" ? String(p.sent) : "–"));
      };
      box.append(section("Neighbors", h("div", { class: "scroll" }, h("table", {},
        h("thead", {}, h("tr", {}, ["Neighbor", "AS", "Description", "BGP", "BFD", "Up for", "Prefixes sent"].map((t) => h("th", {}, t)))),
        h("tbody", {}, c.neighbors.length ? c.neighbors.map(row) : h("tr", {}, h("td", { colspan: 7, class: "empty" }, "No neighbors yet: add one under Configure → Anycast → BGP.")))))));
      addresses(box, st);
    }
    function buildOSPF(box, st, os) {
      const oc = os.config;
      if (os.blocked) { box.append(section("OSPF", h("div", { class: "muted" }, BOTH_BLOCKED))); return; }
      if (!oc.interfaces.length) { box.append(section("OSPF", h("div", { class: "muted" }, "OSPF is off on this node."))); return; }
      if (oc.disabled) { box.append(section("OSPF", h("div", { class: "muted" }, "OSPF is disabled on this node."))); return; }
      box.append(section("Status", kv([
        ["Router ID", oc.router_id],
        ["FRR", os.installed ? pill("installed", "ok") : pill("not installed", "bad")],
        os.installed ? ["ospfd / ospf6d", os.running ? pill("answering", "ok") : pill("not answering", "bad")] : null,
        applied(os.applied),
      ]), ...(os.notes || []).filter((n) => !/^Nothing is announced/.test(n)).map((n) => h("div", { class: "muted" }, n))));
      const rows = [];
      for (const f of oc.interfaces) {
        const ps = os.peers.filter((p) => p.interface === f.name);
        if (f.disabled || !ps.length) {
          rows.push(h("tr", {}, h("td", { class: "mono" }, f.name), h("td", {}, f.area), h("td", {}, "–"), h("td", {}, "–"),
            h("td", {}, f.disabled ? pill("disabled", "") : pill(os.running ? "no neighbor" : "–", os.running ? "bad" : "")), h("td", {}, "–"), h("td", {}, "–")));
          continue;
        }
        for (const p of ps) {
          rows.push(h("tr", {}, h("td", { class: "mono" }, f.name), h("td", {}, f.area), h("td", { class: "mono" }, p.neighbor), h("td", {}, p.af === "ipv6" ? "OSPFv3" : "OSPFv2"),
            h("td", {}, pill(p.state, ospfKind(p.state))), h("td", {}, p.bfd ? pill(p.bfd, bfdKind(p.bfd)) : "–"), h("td", {}, p.state === "Full" && p.uptime ? p.uptime : "–")));
        }
      }
      box.append(section("Neighbors", h("div", { class: "scroll" }, h("table", {},
        h("thead", {}, h("tr", {}, ["Interface", "Area", "Neighbor", "Protocol", "State", "BFD", "Up for"].map((t) => h("th", {}, t)))),
        h("tbody", {}, rows)))));
      addresses(box, st);
    }

    const both = async () => { const [a, b] = await Promise.all([api("GET", "/api/bgp"), api("GET", "/api/ospf")]); return [a.data, b.data]; };
    return {
      async mount(main) {
        boxB = h("div", {}); boxO = h("div", {});
        const panes = [h("div", { role: "tabpanel" }, boxB), h("div", { role: "tabpanel" }, boxO)];
        main.append(anycastTabs("anycastMonTab", "Anycast routing", panes), ...panes);
        draw(...(await both()));
      },
      async poll() { draw(...(await both())); },
      poll_ms: 5000,
    };
  })();

  // ── Anycast, Configure  (CLI: --asn, --router-id, --bgp-neighbor-add/-del) ──────
  VIEWS.anycast = (() => {
    let status, settings, nbrs, cfg = null, sel = null, draft = null, rows = new Map(), chain = Promise.resolve();
    const clone = (o) => JSON.parse(JSON.stringify(o));

    function save(next, keepForm) {
      chain = chain.then(async () => {
        try { const st = (await api("PUT", "/api/bgp", next)).data; say(status, "ok"); cfg = st.config; return true; }
        catch (e) { fail(status)(e); if (!keepForm) await load(); return false; }
      });
      return chain;
    }

    function select(key) {
      sel = key;
      for (const [k, tr] of rows) tr.classList.toggle("sel", k === key);
    }

    // a password box with an eye beside it: click to see what is typed, click again to hide it
    function withEye(input) {
      const btn = h("button", { class: "pweye", type: "button", title: "Show password", "aria-label": "Show password", "aria-pressed": "false", onclick: () => {
        const show = input.type === "password";
        input.type = show ? "text" : "password";
        btn.setAttribute("aria-pressed", String(show));
        btn.title = show ? "Hide password" : "Show password"; btn.setAttribute("aria-label", btn.title);
        btn.classList.toggle("on", show);
      } }, "\u{1F441}");
      return h("div", { class: "pwwrap" }, input, btn);
    }

    function neighborRow(n, idx) {
      const as = h("input", { class: "as", type: "text", inputmode: "numeric", pattern: "[0-9]*", autocomplete: "off", value: String(n.remote_as), "aria-label": "AS of " + n.peer });
      const desc = h("input", { type: "text", value: n.description || "", maxlength: "60", "aria-label": "Description of " + n.peer });
      const pw = h("input", { type: "password", value: n.password || "", autocomplete: "new-password", "aria-label": "Password for " + n.peer });
      const mh = h("input", { class: "as", type: "text", inputmode: "numeric", pattern: "[0-9]*", autocomplete: "off", value: n.multihop > 1 ? String(n.multihop) : "", placeholder: "off", "aria-label": "Multihop of " + n.peer });
      // the address can be changed like the rest of the row; "was" tells the server it is the same neighbor
      const peer = h("input", { class: "mono", type: "text", value: n.peer, spellcheck: "false", autocomplete: "off", "aria-label": "Address of " + n.peer });
      const commit = async () => {
        const addr = peer.value.trim() || n.peer;
        const next = clone(cfg);
        next.neighbors[idx] = { peer: addr, remote_as: Number(as.value) || 0, description: desc.value.trim(), password: pw.value, multihop: Number(mh.value) || 0 };
        if (addr === n.peer) { peer.value = n.peer; save(next); return; }
        next.neighbors[idx].was = n.peer;
        if (await save(next)) { if (sel === n.peer) sel = addr; drawNeighbors(); }
      };
      for (const el of [peer, as, desc, pw, mh]) el.addEventListener("change", commit);
      // on/off for this one neighbor (the same as Operate ▸ Anycast): off shuts the session down and keeps the neighbor
      const on = h("input", { type: "checkbox", checked: !n.disabled, disabled: !!cfg.disabled, title: cfg.disabled ? "BGP is switched off on this node" : "Off: the session is shut down and nothing is announced to this neighbor", "aria-label": "Neighbor " + n.peer + " enabled" });
      on.addEventListener("change", () => {
        if (!on.checked && !confirm("Disable neighbor " + n.peer + "?\n\nThe session is shut down and nothing is announced to it. The neighbor is kept.")) { on.checked = true; return; }
        on.disabled = true;
        chain = chain.then(async () => {
          try { await api("POST", "/api/bgp/operate", { peer: n.peer, enabled: on.checked }); say(status, "ok"); } catch (e) { fail(status)(e); }
          await load();
        });
      });
      return h("tr", { "data-peer": n.peer }, h("td", {}, peer), h("td", {}, as), h("td", {}, desc), h("td", {}, withEye(pw)), h("td", {}, mh), h("td", {}, on));
    }

    // the new row: becomes a neighbor once it has an address and an AS
    function draftRow() {
      const peer = h("input", { type: "text", placeholder: "192.0.2.1 or 2001:db8::1", spellcheck: "false", "aria-label": "New neighbor address" });
      const as = h("input", { class: "as", type: "text", inputmode: "numeric", pattern: "[0-9]*", autocomplete: "off", placeholder: "AS", "aria-label": "New neighbor AS" });
      const desc = h("input", { type: "text", maxlength: "60", placeholder: "optional", "aria-label": "New neighbor description" });
      const pw = h("input", { type: "password", autocomplete: "new-password", placeholder: "optional", "aria-label": "New neighbor password" });
      const mh = h("input", { class: "as", type: "text", inputmode: "numeric", pattern: "[0-9]*", autocomplete: "off", placeholder: "off", "aria-label": "New neighbor multihop" });
      const commit = async () => {
        if (!peer.value.trim() || !(Number(as.value) > 0)) return; // not complete yet
        const next = clone(cfg);
        next.neighbors.push({ peer: peer.value.trim(), remote_as: Number(as.value), description: desc.value.trim(), password: pw.value, multihop: Number(mh.value) || 0 });
        if (await save(next, true)) { draft = null; sel = peer.value.trim(); drawNeighbors(); }
      };
      for (const el of [peer, as, desc, pw, mh]) el.addEventListener("change", commit);
      draft = { peer, focus: () => peer.focus() };
      return h("tr", { "data-peer": "" }, h("td", {}, peer), h("td", {}, as), h("td", {}, desc), h("td", {}, withEye(pw)), h("td", {}, mh), h("td", {}));
    }

    function drawNeighbors() {
      rows = new Map();
      const add = h("button", { class: "btn small", type: "button", "aria-label": "Add a neighbor", title: "Add a neighbor", onclick: () => {
        if (draft) { draft.focus(); return; }
        const tr = draftRow(); body.append(tr); rows.set("", tr); select(""); draft.focus();
        const e = body.querySelector(".empty"); if (e) e.closest("tr").remove();
      } }, "+");
      const del = h("button", { class: "btn small", type: "button", "aria-label": "Remove the selected neighbor", title: "Remove the selected neighbor", onclick: () => {
        if (sel === null) { say(status, "bad", "Click a neighbor first, then remove it."); return; }
        if (sel === "") { draft = null; drawNeighbors(); return; }
        if (!confirm("Remove neighbor " + sel + "?")) return;
        const next = clone(cfg); next.neighbors = next.neighbors.filter((x) => x.peer !== sel);
        sel = null; save(next).then(drawNeighbors);
      } }, "−");
      const body = h("tbody", {});
      cfg.neighbors.forEach((n, i) => { const tr = neighborRow(n, i); body.append(tr); rows.set(n.peer, tr); });
      draft = null;
      if (!cfg.neighbors.length) body.append(h("tr", {}, h("td", { colspan: 6, class: "empty" }, "No neighbors yet. Press + to add one.")));
      for (const [k, tr] of rows) { const pick = () => select(k); tr.addEventListener("click", pick); tr.addEventListener("focusin", pick); }
      if (sel !== null && !rows.has(sel)) sel = null;
      clear(nbrs).append(section("Neighbors",
        h("div", { class: "toolbar tight" }, add, del),
        h("div", { class: "scroll" }, h("table", {},
          h("thead", {}, h("tr", {}, ["Neighbor", "AS", "Description", "Password", "Multihop", "Enabled"].map((t) => h("th", {}, t)))), body))));
      select(sel);
    }

    function drawSettings() {
      const asn = h("input", { type: "text", inputmode: "numeric", pattern: "[0-9]*", autocomplete: "off", value: cfg.asn ? String(cfg.asn) : "", placeholder: "64512" });
      const rid = h("input", { type: "text", value: cfg.router_id || "", placeholder: "192.0.2.1 (optional)", spellcheck: "false" });
      const secs = (v, d, label) => h("input", { class: "narrow", type: "text", inputmode: "numeric", pattern: "[0-9]*", autocomplete: "off", value: String(v || d), "aria-label": label });
      const ka = secs(cfg.keepalive, 3, "Keepalive"), hold = secs(cfg.hold, 9, "Hold time");
      const prep = h("input", { type: "checkbox", checked: !!cfg.as_prepend });
      const needAS = () => { rid.disabled = !(Number(asn.value) > 0); };   // a router ID means nothing without an AS
      needAS();
      const commit = () => {
        if (!(Number(asn.value) > 0)) rid.value = "";   // clearing the AS clears the router ID with it
        needAS();
        const next = clone(cfg);
        next.asn = Number(asn.value) || 0; next.router_id = rid.value.trim();
        next.keepalive = Number(ka.value) || 0; next.hold = Number(hold.value) || 0;
        if (prep.checked) next.as_prepend = true; else delete next.as_prepend;
        save(next);
      };
      asn.addEventListener("input", needAS);
      for (const el of [asn, rid, ka, hold, prep]) el.addEventListener("change", commit);
      clear(settings).append(section("BGP",
        h("div", { class: "bgprow" },
          h("label", { class: "f" }, h("span", {}, "Local AS number"), asn),
          h("label", { class: "f" }, h("span", {}, "Router ID"), rid),
          h("label", { class: "f" }, h("span", {}, "Keepalive"), ka),
          h("label", { class: "f" }, h("span", {}, "Hold time"), hold),
          h("label", { class: "f" }, h("span", {}, "\u00a0"), h("span", { class: "opt" }, prep, " AS Prepend")))));
    }

    async function load() {
      const st = (await api("GET", "/api/bgp")).data;
      cfg = st.config;
      if (st.blocked) {
        clear(nbrs);
        clear(settings).append(section("BGP", h("div", { class: "muted" }, "BGP is unavailable: AnyName is installed on this host.")));
        return;
      }
      drawSettings(); drawNeighbors();
    }

    // ── OSPF tab ──
    let ocfg = null, ohost = [], osettings, oifs, osel = null, odraft = null, orows = new Map(), orid = null;

    function osave(next, keepForm) {
      chain = chain.then(async () => {
        try { const st = (await api("PUT", "/api/ospf", next)).data; say(status, "ok"); ocfg = st.config; ohost = st.host_interfaces || ohost; return true; }
        catch (e) { fail(status)(e); if (!keepForm) await oload(); return false; }
      });
      return chain;
    }

    function oselect(key) {
      osel = key;
      for (const [k, tr] of orows) tr.classList.toggle("sel", k === key);
    }

    const LINKS = [["", "Default"], ["point-to-point", "Point-to-point"], ["broadcast", "Broadcast"]];
    const linkSelect = (value, label) => h("select", { "aria-label": label }, ...LINKS.map(([v, t]) => h("option", { value: v, selected: v === (value || "") }, t)));
    const areaInput = (value, label) => h("input", { class: "as", type: "text", autocomplete: "off", value: value && value !== "0.0.0.0" ? value : "", placeholder: "0", spellcheck: "false", "aria-label": label });

    function interfaceRow(f, idx) {
      const area = areaInput(f.area, "Area of " + f.name);
      const link = linkSelect(f.network, "Link type of " + f.name);
      const key = h("input", { type: "password", value: f.password || "", autocomplete: "new-password", maxlength: "16", "aria-label": "Key for " + f.name });
      const commit = () => {
        const next = clone(ocfg);
        next.interfaces[idx] = { name: f.name, area: area.value.trim(), network: link.value, password: key.value };
        osave(next);
      };
      for (const el of [area, link, key]) el.addEventListener("change", commit);
      // on/off for this one interface (the same as Operate ▸ Anycast): off makes it passive, no neighbors, and keeps it
      const on = h("input", { type: "checkbox", checked: !f.disabled, disabled: !!ocfg.disabled, title: ocfg.disabled ? "OSPF is switched off on this node" : "Off: no hellos are sent and no neighbors are formed on this interface", "aria-label": "Interface " + f.name + " enabled" });
      on.addEventListener("change", () => {
        if (!on.checked && !confirm("Disable OSPF on " + f.name + "?\n\nNo hellos are sent and its neighbors go down. The interface is kept.")) { on.checked = true; return; }
        on.disabled = true;
        chain = chain.then(async () => {
          try { await api("POST", "/api/ospf/operate", { interface: f.name, enabled: on.checked }); say(status, "ok"); } catch (e) { fail(status)(e); }
          await oload();
        });
      });
      return h("tr", { "data-iface": f.name }, h("td", { class: "mono" }, f.name), h("td", {}, area), h("td", {}, link), h("td", {}, withEye(key)), h("td", {}, on));
    }

    // the new row: becomes an interface once it has a name
    function draftInterfaceRow() {
      const list = "ospf-host-ifs";
      const name = h("input", { type: "text", list, placeholder: "eth0", spellcheck: "false", autocomplete: "off", "aria-label": "New interface name" });
      const dl = h("datalist", { id: list }, ...ohost.filter((n) => !ocfg.interfaces.some((f) => f.name === n)).map((n) => h("option", { value: n })));
      const area = areaInput("", "New interface area");
      const link = linkSelect("", "New interface link type");
      const key = h("input", { type: "password", autocomplete: "new-password", placeholder: "optional", maxlength: "16", "aria-label": "New interface key" });
      const commit = async () => {
        if (!name.value.trim()) return; // not complete yet
        const rid = orid.value.trim();
        if (!rid) { say(status, "bad", "Enter the router ID first (an IPv4 address that is unique among your routers), then add the interface."); orid.focus(); return; }
        const next = clone(ocfg);
        next.router_id = rid;
        next.interfaces.push({ name: name.value.trim(), area: area.value.trim(), network: link.value, password: key.value });
        if (await osave(next, true)) { odraft = null; osel = name.value.trim(); drawOSPF(); }
      };
      for (const el of [name, area, link, key]) el.addEventListener("change", commit);
      odraft = { name, focus: () => name.focus() };
      return h("tr", { "data-iface": "" }, h("td", {}, name, dl), h("td", {}, area), h("td", {}, link), h("td", {}, withEye(key)), h("td", {}));
    }

    function drawOSPF() {
      // settings
      const rid = h("input", { type: "text", value: ocfg.router_id || "", placeholder: "192.0.2.10", spellcheck: "false", "aria-label": "OSPF router ID" });
      orid = rid;
      const num = (v, d, label) => h("input", { class: "narrow", type: "text", inputmode: "numeric", pattern: "[0-9]*", autocomplete: "off", value: String(v || d), "aria-label": label });
      const hello = num(ocfg.hello, 10, "Hello interval"), dead = num(ocfg.dead, 40, "Dead interval"), metric = num(ocfg.metric, 20, "Metric");
      const commit = () => {
        if (!ocfg.interfaces.length) return; // saved with the first interface (the router ID means nothing without one)
        const next = clone(ocfg);
        next.router_id = rid.value.trim();
        next.hello = Number(hello.value) || 0; next.dead = Number(dead.value) || 0; next.metric = Number(metric.value) || 0;
        if (next.hello === 10) next.hello = 0;
        if (next.dead === 40) next.dead = 0;
        if (next.metric === 20) next.metric = 0;
        osave(next);
      };
      for (const el of [rid, hello, dead, metric]) el.addEventListener("change", commit);
      clear(osettings).append(section("OSPF",
        h("div", { class: "bgprow" },
          h("label", { class: "f" }, h("span", {}, "Router ID"), rid),
          h("label", { class: "f" }, h("span", {}, "Hello interval"), hello),
          h("label", { class: "f" }, h("span", {}, "Dead interval"), dead),
          h("label", { class: "f" }, h("span", {}, "Metric"), metric))));

      // interfaces
      orows = new Map();
      const add = h("button", { class: "btn small", type: "button", "aria-label": "Add an interface", title: "Add an interface", onclick: () => {
        if (odraft) { odraft.focus(); return; }
        const tr = draftInterfaceRow(); body.append(tr); orows.set("", tr); oselect(""); odraft.focus();
        const e = body.querySelector(".empty"); if (e) e.closest("tr").remove();
      } }, "+");
      const del = h("button", { class: "btn small", type: "button", "aria-label": "Remove the selected interface", title: "Remove the selected interface", onclick: () => {
        if (osel === null) { say(status, "bad", "Click an interface first, then remove it."); return; }
        if (osel === "") { odraft = null; drawOSPF(); return; }
        if (!confirm("Remove interface " + osel + " from OSPF?")) return;
        const next = clone(ocfg); next.interfaces = next.interfaces.filter((x) => x.name !== osel);
        osel = null; osave(next).then(drawOSPF);
      } }, "−");
      const body = h("tbody", {});
      ocfg.interfaces.forEach((f, i) => { const tr = interfaceRow(f, i); body.append(tr); orows.set(f.name, tr); });
      odraft = null;
      if (!ocfg.interfaces.length) body.append(h("tr", {}, h("td", { colspan: 5, class: "empty" }, "No interfaces yet. Enter a router ID, then press + to add one.")));
      for (const [k, tr] of orows) { const pick = () => oselect(k); tr.addEventListener("click", pick); tr.addEventListener("focusin", pick); }
      if (osel !== null && !orows.has(osel)) osel = null;
      clear(oifs).append(section("Interfaces",
        h("div", { class: "toolbar tight" }, add, del),
        h("div", { class: "scroll" }, h("table", {},
          h("thead", {}, h("tr", {}, ["Interface", "Area", "Link type", "Key (OSPFv2)", "Enabled"].map((t) => h("th", {}, t)))), body))));
      oselect(osel);
    }

    async function oload() {
      const st = (await api("GET", "/api/ospf")).data;
      ocfg = st.config; ohost = st.host_interfaces || [];
      if (st.blocked) { clear(osettings).append(section("OSPF", h("div", { class: "muted" }, "OSPF is not available here: " + st.blocked + "."))); clear(oifs); return; }
      drawOSPF();
    }

    return {
      async mount(main) {
        status = h("div", { "aria-live": "polite" }); settings = h("div", {}); nbrs = h("div", {});
        osettings = h("div", {}); oifs = h("div", {});
        sel = null; draft = null; osel = null; odraft = null;
        // two tabs: BGP and OSPF.  Both stay in the page; the hidden one is just not shown.
        const TABS = [["BGP", h("div", {}, settings, nbrs)], ["OSPF", h("div", {}, osettings, oifs)]];
        const panes = TABS.map(([, el]) => h("div", { role: "tabpanel" }, el));
        const buttons = TABS.map(([name], i) => h("button", { type: "button", role: "tab", class: "dtab", onclick: () => show(i) }, name));
        function show(i, focus) {
          state.anycastTab = i;
          refreshHelp();
          panes.forEach((p, j) => { p.hidden = j !== i; });
          buttons.forEach((b, j) => { b.setAttribute("aria-selected", j === i ? "true" : "false"); b.tabIndex = j === i ? 0 : -1; b.classList.toggle("on", j === i); });
          if (focus) buttons[i].focus();
        }
        const strip = h("div", { class: "dtabs", role: "tablist", "aria-label": "Anycast routing" }, buttons);
        strip.addEventListener("keydown", (e) => {
          const d = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
          if (!d) return;
          e.preventDefault();
          show((state.anycastTab + d + TABS.length) % TABS.length, true);
        });
        show(state.anycastTab >= 0 && state.anycastTab < TABS.length ? state.anycastTab : 0);
        main.append(status, strip, ...panes);
        try { await load(); } catch (e) { fail(status)(e); }
        try { await oload(); } catch (e) { fail(status)(e); }
      },
    };
  })();

  // ── Anycast, Operate  (CLI: --bgp-disable/-enable, --bgp-neighbor-disable/-enable, --ospf-disable/-enable, --ospf-interface-disable/-enable) ──
  VIEWS.anycastop = (() => {
    let status, boxB, boxO;

    async function call(path, body) {
      try { await api("POST", path, body); clear(status); await refresh(); }
      catch (e) { fail(status)(e); }
    }
    const both = async () => { const [a, b] = await Promise.all([api("GET", "/api/bgp"), api("GET", "/api/ospf")]); return [a.data, b.data]; };
    async function refresh() { draw(...(await both())); }

    function draw(st, os) {
      for (const [real, fn] of [[boxB, buildBGP], [boxO, buildOSPF]]) {
        const tmp = h("div", {});
        try { fn(tmp, st, os); } finally { morph(real, tmp); }
      }
    }
    // built into a detached copy and morphed in: the page keeps its elements (and so its scroll boxes) from one poll to the next
    function buildBGP(box, st) {
      const c = st.config;
      if (st.blocked) { box.append(section("BGP", h("div", { class: "muted" }, BOTH_BLOCKED))); return; }
      if (!c.asn) { box.append(section("BGP", h("div", { class: "muted" }, "BGP is off on this node: set it up under Configure → Anycast → BGP."))); return; }
      const live = new Map(st.peers.map((p) => [p.peer, p]));
      const stateOf = (n) => {
        const p = live.get(n.peer);
        if (n.disabled) return pill("disabled", "");
        if (c.disabled) return pill("–", "");
        return p ? pill(p.state, p.state === "Established" ? "ok" : p.state === "Active" || p.state === "Connect" || p.state === "OpenSent" || p.state === "OpenConfirm" ? "warn" : "bad") : pill(st.running ? "not known yet" : "–", "");
      };
      const toggleBGP = h("button", { class: c.disabled ? "btn primary" : "btn danger", type: "button", onclick: () => {
        if (!c.disabled && !confirm("Disable BGP on this node?\n\nIts sessions go down and the anycast addresses are no longer announced from this node by BGP. The settings are kept.")) return;
        call("/api/bgp/operate", { enabled: !!c.disabled });
      } }, c.disabled ? "Enable BGP" : "Disable BGP");
      const row = (n) => h("tr", { tabindex: "0", oncontextmenu: rowMenu([[n.disabled ? "Enable" : "Disable", () => {
          if (c.disabled) return;
          if (!n.disabled && !confirm("Disable neighbor " + n.peer + "?\n\nThe session is shut down and nothing is announced to it. The neighbor is kept.")) return;
          call("/api/bgp/operate", { peer: n.peer, enabled: !!n.disabled });
        }]]) }, h("td", { class: "mono" }, n.peer), h("td", {}, String(n.remote_as)), h("td", {}, n.description || ""), h("td", {}, stateOf(n)));
      box.append(section("BGP",
        kv([["Local AS", String(c.asn)], ["State", c.disabled ? pill("disabled", "") : pill("enabled", "ok")]]),
        h("div", { class: "toolbar" }, toggleBGP)));
      box.append(section("Neighbors", h("div", { class: "scroll" }, h("table", {},
        h("thead", {}, h("tr", {}, ["Neighbor", "AS", "Description", "BGP"].map((t) => h("th", {}, t)))),
        h("tbody", {}, c.neighbors.length ? c.neighbors.map(row) : h("tr", {}, h("td", { colspan: 5, class: "empty" }, "No neighbors yet: add one under Configure → Anycast → BGP.")))))));
    }
    function buildOSPF(box, st, os) {
      const oc = os.config;
      if (os.blocked) { box.append(section("OSPF", h("div", { class: "muted" }, BOTH_BLOCKED))); return; }
      if (!oc.interfaces.length) { box.append(section("OSPF", h("div", { class: "muted" }, "OSPF is off on this node: set it up under Configure → Anycast → OSPF."))); return; }
      const full = (f) => os.peers.some((p) => p.interface === f.name && p.state === "Full");
      const stateOf = (f) => f.disabled ? pill("disabled", "") : oc.disabled ? pill("–", "") : os.running ? (full(f) ? pill("Full", "ok") : pill("no neighbor", "bad")) : pill("–", "");
      const toggleOSPF = h("button", { class: oc.disabled ? "btn primary" : "btn danger", type: "button", onclick: () => {
        if (!oc.disabled && !confirm("Disable OSPF on this node?\n\nIts neighbors go down and the anycast addresses are no longer announced from this node by OSPF. ospfd and ospf6d are stopped. The settings are kept.")) return;
        call("/api/ospf/operate", { enabled: !!oc.disabled });
      } }, oc.disabled ? "Enable OSPF" : "Disable OSPF");
      const row = (f) => h("tr", { tabindex: "0", oncontextmenu: rowMenu([[f.disabled ? "Enable" : "Disable", () => {
          if (oc.disabled) return;
          if (!f.disabled && !confirm("Disable OSPF on " + f.name + "?\n\nNo hellos are sent and its neighbors go down. The interface is kept.")) return;
          call("/api/ospf/operate", { interface: f.name, enabled: !!f.disabled });
        }]]) }, h("td", { class: "mono" }, f.name), h("td", {}, f.area), h("td", {}, f.network || "default"), h("td", {}, stateOf(f)));
      box.append(section("OSPF",
        kv([["Router ID", oc.router_id], ["State", oc.disabled ? pill("disabled", "") : pill("enabled", "ok")]]),
        h("div", { class: "toolbar" }, toggleOSPF)));
      box.append(section("Interfaces", h("div", { class: "scroll" }, h("table", {},
        h("thead", {}, h("tr", {}, ["Interface", "Area", "Link type", "OSPF"].map((t) => h("th", {}, t)))),
        h("tbody", {}, oc.interfaces.map(row))))));
    }

    return {
      async mount(main) {
        status = h("div", { "aria-live": "polite" }); boxB = h("div", {}); boxO = h("div", {});
        const panes = [h("div", { role: "tabpanel" }, boxB), h("div", { role: "tabpanel" }, boxO)];
        main.append(status, anycastTabs("anycastOpTab", "Anycast routing", panes), ...panes);
        try { await refresh(); } catch (e) { fail(status)(e); }
      },
      async poll() { await refresh(); },
      poll_ms: 5000,
    };
  })();

  // ── Scheduler  (CLI: --schedule, --schedule-history, --schedule-set, --schedule-del, --schedule-run) ──
  // ── Monitor ▸ Alerts  (CLI: none yet) ───────────────────────────────────────
  VIEWS.alerts = (() => {
    let status, box, note, last = "", showClosed = false;
    const SEV = { critical: "bad", warning: "warn", info: "info" };
    const at = (u) => (u ? new Date(u * 1000).toLocaleString() : "–");
    const dur = (s) => { s = Math.max(0, Math.round(s)); const d = Math.floor(s / 86400), hh = Math.floor(s % 86400 / 3600), m = Math.floor(s % 3600 / 60);
      return d ? d + "d " + hh + "h" : hh ? hh + "h " + m + "m" : m ? m + "m" : s + "s"; };
    const say2 = (t) => { note.textContent = t; setTimeout(() => { if (note.textContent === t) note.textContent = ""; }, 6000); };
    async function call(path, body, done) {
      try { const r = await api("POST", "/api/alerts/" + path, body); if (r.data && r.data.message) say2(r.data.message); clear(status); await VIEWS.alerts.poll(true); refreshAlertMark(); if (done) done(); } catch (e) { fail(status)(e); }
    }
    const copy = async (text) => { try { await navigator.clipboard.writeText(text); } catch (_) { /* no permission */ } };
    function draw(v) {
      const sig = JSON.stringify([v, showClosed]);
      if (sig === last) return;
      last = sig;
      const rules = Object.fromEntries(v.rules.map((r) => [r.id, r]));
      const real = box; box = h("div", {});
      try {
        const list = v.alerts.filter((a) => showClosed || !a.closed);
        const open = v.alerts.filter((a) => !a.closed);
        const text = (a) => "[" + a.severity + "] " + a.title + (a.subject ? " (" + a.subject + ")" : "") + ": " + a.message;
        const menu = (a) => {
          const d = rules[a.rule] || {};
          const more = [];
          if (!a.closed) for (const f of d.fix || []) more.push([ALERT_FIX[f] || f, () => call("fix", { id: a.id, action: f })]);
          if (d.go) more.push(["Go to " + (TABS.find((t) => t[0] === d.go) || [0, d.go])[1], () => selectTab(d.go)]);
          more.push(["Edit rule", () => { state.alertRule = a.rule; selectTab("cfg_alerts"); }]);
          const items = a.closed ? [["Copy", () => copy(text(a))], "-", ["More", more]] : [
            a.acked ? ["Unacknowledge", () => call("ack", { id: a.id, ack: false })] : ["Acknowledge", () => call("ack", { id: a.id, ack: true })],
            ["Silence", [["15 minutes", 15], ["1 hour", 60], ["4 hours", 240], ["1 day", 1440], ["7 days", 10080]].map(([l, m]) => [l, () => call("silence", { id: a.id, minutes: m })])],
            a.silenced_until ? ["Unsilence", () => call("silence", { id: a.id, minutes: 0 })] : null,
            ["Mark resolved", () => call("resolve", { id: a.id })],
            ["Copy", () => copy(text(a))], "-", ["More", more]].filter(Boolean);
          return rowMenu(items);
        };
        const stateCell = (a) => {
          if (a.closed) return [pill("closed", "ok"), " ", h("span", { class: "small muted" }, ({ cleared: "cleared by itself", manual: "marked resolved" + (a.resolved_by ? " by " + a.resolved_by : ""), "rule off": "rule turned off" })[a.resolution] || a.resolution)];
          const out = [];
          if (a.silenced_until) out.push(pill("silenced", ""), " ", h("span", { class: "small muted" }, "until " + at(a.silenced_until)));
          else if (a.acked) out.push(pill("acknowledged", "info"), " ", h("span", { class: "small muted" }, a.acked_by ? "by " + a.acked_by : ""));
          else if (a.recovering) out.push(pill("recovering", "warn"));
          else out.push(pill("open", SEV[a.severity] || ""));
          return out;
        };
        const row = (a) => h("tr", { tabindex: "0", class: a.closed ? "paused" : "", oncontextmenu: menu(a) },
          h("td", {}, pill(a.severity, SEV[a.severity] || "")),
          h("td", {}, a.title, a.subject ? h("div", { class: "mono small muted" }, a.subject) : null),
          h("td", { class: "wrap" }, a.message),
          h("td", { title: a.closed ? "closed " + at(a.closed) : "" }, at(a.opened), h("div", { class: "small muted" }, a.closed ? "lasted " + dur(a.closed - a.opened) : "for " + dur(v.now - a.opened))),
          h("td", {}, stateCell(a)),
          h("td", { class: "num" }, a.count > 1 ? "×" + a.count : ""));
        const closedBox = h("input", { type: "checkbox", checked: showClosed });
        closedBox.addEventListener("change", () => { showClosed = closedBox.checked; draw(v); });
        const crit = open.filter((a) => a.severity === "critical" && !a.silenced_until && !a.acked).length;
        box.append(section("Alerts of " + v.node,
          h("div", { class: "toolbar" }, h("label", { class: "nt-opt" }, closedBox, " Show closed"),
            h("span", { class: "muted small" }, open.length ? open.length + " open" + (crit ? ", " + crit + " critical" : "") : "Nothing is open.")),
          h("div", { class: "scroll" }, h("table", { class: "tight alerts" },
            h("thead", {}, h("tr", {}, ["Severity", "Alert", "What is wrong", "Since", "State", ""].map((x) => h("th", {}, x)))),
            h("tbody", {}, list.length ? list.map(row) : h("tr", {}, h("td", { colspan: 6, class: "empty" }, showClosed ? "No alerts yet." : "No open alerts.")))))));
        if (v.silences.length) box.append(section("Silences",
          h("div", { class: "scroll" }, h("table", { class: "tight" },
            h("thead", {}, h("tr", {}, ["Alert", "Subject", "Until", "By"].map((x) => h("th", {}, x)))),
            h("tbody", {}, v.silences.map((s) => h("tr", {}, h("td", {}, (rules[s.rule] || { title: s.rule || "any" }).title), h("td", { class: "mono" }, s.subject || "any"), h("td", {}, at(s.until)), h("td", {}, s.by || ""))))))));
      } finally { morph(real, box); box = real; }
    }
    return {
      async mount(main) {
        status = h("div", { "aria-live": "polite" }); note = h("div", { class: "hint", "aria-live": "polite" }); box = h("div", {}); last = "";
        main.append(status, note, box);
        try { draw((await api("GET", "/api/alerts")).data); } catch (e) { fail(status)(e); }
      },
      async poll(force) { if (force) last = ""; draw((await api("GET", "/api/alerts")).data); },
      poll_ms: 5000,
    };
  })();

  VIEWS.scheduler = (() => {
    let status, box, last = "", clip = null;
    try { clip = JSON.parse(localStorage.getItem("logdir_sched_clip") || "null"); } catch (_) { clip = null; }
    const WD = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];
    const NTH = [[1, "1st"], [2, "2nd"], [3, "3rd"], [4, "4th"], [-1, "last"]];
    const MON = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];
    let localTimes = false; // run times are UTC, like the schedules; the Local time box on the history shows them in this browser's zone
    try { localTimes = localStorage.getItem("sched_local_times") === "1"; } catch (_) { /* no storage */ }
    const when = (u) => (!u ? "–" : localTimes ? new Date(u * 1000).toLocaleString([], { timeZoneName: "short" }) : new Date(u * 1000).toISOString().slice(0, 19).replace("T", " ") + " UTC");

    async function call(path, body, done) {
      try { await api("POST", "/api/schedule/" + path, body); if (done) done(); clear(status); await VIEWS.scheduler.poll(true); } catch (e) { fail(status)(e); }
    }
    const nodeName = (id) => { const n = state.nodes.find((x) => x.node_id === id); return n ? (nodeLabel(n)) : id.slice(0, 8); };
    const actionLabel = (t, actions) => (t.action === "shell" ? "Shell command" : (actions || {})[t.action] || t.action);

    function copyTask(t) {
      clip = JSON.parse(JSON.stringify(t));
      try { localStorage.setItem("logdir_sched_clip", JSON.stringify(clip)); } catch (_) { /* no storage */ }
      VIEWS.scheduler.poll(true).catch(() => {});   // the Paste entries appear
      say(status, "info", "Copied “" + t.name + "”. Right-click ▸ Paste adds a copy.");
      setTimeout(() => clear(status), 2500);
    }
    function pasteTask(existing) {
      if (!clip) return;
      const names = new Set(existing.map((t) => t.name.toLowerCase()));
      let name = clip.name + " copy", i = 2;
      while (names.has(name.toLowerCase())) name = clip.name + " copy " + i++;
      const t = Object.assign({}, clip, { name }); delete t.id;
      call("task", { task: t });
    }

    function editor(task, existing) {
      const t = Object.assign({ freq: "daily", at: "12:00:00", action: "backup", window_sec: 0, timeout_sec: 300, days: [1], day: 1, nths: [], weekdays: [], month: 1, every: 1 }, task || {});
      if (t.nth && !(t.nths || []).length) { t.nths = [t.nth]; t.weekdays = [t.weekday || 0]; }
      const isNew = !task;
      const err = h("div", { "aria-live": "polite" });
      const d = h("dialog", { class: "cv-dialog sched-dialog", "aria-label": isNew ? "New task" : "Edit task" });
      const close = () => { try { d.close(); } catch (_) { /* closed */ } d.remove(); };
      d.addEventListener("close", () => d.remove());
      const name = h("input", { type: "text", maxlength: "80", value: t.name || "", "aria-label": "Name", autocomplete: "off" });
      const acts = Object.entries(editorActions);
      const action = h("select", { "aria-label": "Action" }, acts.map(([k, l]) => h("option", { value: k, selected: k === t.action }, l)), h("option", { value: "shell", selected: t.action === "shell" }, "Run a shell command"));
      const cmd = h("textarea", { rows: "4", spellcheck: "false", maxlength: "4096", "aria-label": "Command", class: "mono" }); cmd.value = t.command || "";
      const tmo = h("input", { type: "number", min: "1", max: "86400", value: String(t.timeout_sec || 300), "aria-label": "Time limit in seconds" });
      const ign = h("input", { type: "checkbox", checked: !!t.ignore_exit });
      const shellBox = h("div", {}, h("label", { class: "f wide" }, "Command", cmd),
        h("label", { class: "f" }, "Stop it after (seconds)", tmo),
        h("label", { class: "f check" }, ign, "Ignore the exit status"));
      const allNodes = h("input", { type: "radio", name: "sched-where", checked: !(t.nodes || []).length });
      const someNodes = h("input", { type: "radio", name: "sched-where", checked: (t.nodes || []).length > 0 });
      const nodeBoxes = state.nodes.map((n) => ({ n, el: h("input", { type: "checkbox", checked: (t.nodes || []).includes(n.node_id), disabled: !n.node_id }) }));
      const nodesList = h("div", { class: "sched-nodes" }, nodeBoxes.map(({ n, el }) => h("label", { class: "f check" }, el, nodeLabel(n) + (n.self ? " (this node)" : ""))));
      const allLabel = h("label", { class: "f check" }, allNodes, "Every node of the cluster");
      const where = state.nodes.length > 1
        ? h("div", { class: "f wide" }, h("span", {}, "Runs on"), allLabel, h("label", { class: "f check" }, someNodes, "Only these nodes:"), nodesList)
        : h("div", { class: "hint" }, "Runs on this node.");
      const dateIn = h("input", { type: "date", value: t.date || new Date(Date.now() + 86400000).toISOString().slice(0, 10), "aria-label": "Date (UTC)" });
      const freq = h("select", { "aria-label": "How often" }, [["once", "Once"], ["hourly", "Hourly"], ["daily", "Daily"], ["weekly", "Weekly"], ["monthly", "Monthly"], ["yearly", "Yearly"]].map(([k, l]) => h("option", { value: k, selected: k === t.freq }, l)));
      const every = h("input", { type: "number", min: "1", max: "720", value: String(t.every || 1), "aria-label": "Every how many" });
      const everyUnit = h("span", {});
      const at = h("input", { type: "time", step: "60", value: (t.at || "12:00:00").slice(0, 5), "aria-label": "Time of day" });
      const minute = h("input", { type: "number", min: "0", max: "59", value: String(+(t.at || "00:00:00").split(":")[1] || 0), "aria-label": "Minute past the hour" });
      const days = WD.map((w, i) => ({ i, el: h("input", { type: "checkbox", checked: (t.days || []).includes(i) }) }));
      const dayRow = h("div", { class: "sched-days" }, days.map(({ i, el }) => h("label", { class: "f check" }, el, WD[i].slice(0, 3))));
      const byDay = h("input", { type: "radio", name: "sched-by", checked: !(t.nths || []).length });
      const byNth = h("input", { type: "radio", name: "sched-by", checked: (t.nths || []).length > 0 });
      const dom = h("select", { "aria-label": "Day of the month" }, Array.from({ length: 31 }, (_, i) => h("option", { value: String(i + 1), selected: t.day === i + 1 }, String(i + 1))), h("option", { value: "-1", selected: t.day === -1 }, "last day"));
      const nths = NTH.map(([v, l]) => ({ v, el: h("input", { type: "checkbox", checked: (t.nths || []).includes(v) || (!(t.nths || []).length && v === 3) }) }));
      const nthRow = h("span", { class: "sched-days" }, nths.map(({ v, el }) => h("label", { class: "f check" }, el, NTH.find((x) => x[0] === v)[1])));
      const eachSel = h("select", { "aria-label": "Which of those days" }, h("option", { value: "first", selected: !t.each_day && !t.pick_day }, "first of those days only"), h("option", { value: "each", selected: !!t.each_day }, "each of those days"), h("option", { value: "pick", selected: !!t.pick_day }, "one of those days, chosen per node"));
      const wds = WD.map((w, i) => ({ i, el: h("input", { type: "checkbox", checked: (t.weekdays || []).includes(i) || (!(t.weekdays || []).length && i === 6) }) }));
      const wdRow = h("span", { class: "sched-days" }, wds.map(({ i, el }) => h("label", { class: "f check" }, el, WD[i].slice(0, 3))));
      const month = h("select", { "aria-label": "Month" }, MON.map((m, i) => h("option", { value: String(i + 1), selected: t.month === i + 1 }, m)));
      const monthRow = h("label", { class: "f" }, "Month", month);
      const months = MON.map((m, i) => ({ i, el: h("input", { type: "checkbox", checked: (t.months || []).includes(i + 1) }) }));
      const monthsRow = h("div", { class: "f wide", title: "Tick the months it runs in; none ticked means every month" }, h("span", {}, "Only in"),
        h("div", { class: "sched-days sched-months" }, months.map(({ i, el }) => h("label", { class: "f check" }, el, MON[i].slice(0, 3)))));
      const dayBox = h("div", { class: "f wide" }, h("span", {}, "On"),
        h("label", { class: "f check" }, byDay, "day", dom, " of the month"),
        h("div", { class: "f check sched-nthrow" }, byNth, "the", nthRow, wdRow, "of the month, run on", eachSel));
      // The offset window of an older version is shown as its To time when it fits (whole minutes, under a day, under an hour for an hourly task); otherwise it is kept as it was.
      let legacyWin = t.until ? 0 : t.window_sec || 0, toTime = t.until || ""; // with a To time the saved window_sec is only that To time worked out: clearing To must clear it
      if (!toTime && legacyWin > 0 && legacyWin % 60 === 0 && legacyWin < (t.freq === "hourly" ? 3600 : 86400)) {
        const [ah, am] = (t.at || "00:00:00").split(":").map(Number), e = ah * 60 + am + legacyWin / 60;
        toTime = String(Math.floor(e / 60) % 24).padStart(2, "0") + ":" + String(e % 60).padStart(2, "0"); legacyWin = 0;
      }
      const until = h("input", { type: "time", value: t.freq === "hourly" ? "" : toTime, "aria-label": "To (UTC)" });
      const untilMin = h("input", { type: "number", min: "0", max: "59", value: t.freq === "hourly" && toTime ? String(+toTime.split(":")[1]) : "", "aria-label": "To minute past the hour" });
      const enabled = h("input", { type: "checkbox", checked: !t.disabled });
      const rows = {
        shell: shellBox, date: h("label", { class: "f" }, "On (UTC)", dateIn), every: h("label", { class: "f" }, "Every", h("span", { class: "sched-every" }, every, everyUnit)),
        at: h("div", { class: "f sched-range" }, h("label", { class: "f" }, "At (UTC)", at), h("label", { class: "f" }, "To (optional)", until)), minute: h("div", { class: "f sched-range" }, h("label", { class: "f" }, "At minute", minute), h("label", { class: "f" }, "To minute (optional)", untilMin)), days: h("div", { class: "f wide" }, h("span", {}, "On"), dayRow), month: monthRow, day: dayBox, months: monthsRow,
      };
      const sync = () => {
        const f = freq.value;
        rows.shell.hidden = action.value !== "shell";
        const unitName = { hourly: "hour", daily: "day", weekly: "week", monthly: "month", yearly: "year" }[f];
        everyUnit.textContent = " " + unitName + (+every.value === 1 ? "" : "s");
        every.max = { hourly: 720, daily: 366, weekly: 52, monthly: 24, yearly: 10 }[f];
        rows.date.hidden = f !== "once"; rows.every.hidden = f === "once";
        rows.at.hidden = f === "hourly"; rows.minute.hidden = f !== "hourly";
        rows.months.hidden = !["daily", "weekly", "monthly"].includes(f); rows.days.hidden = f !== "weekly"; rows.month.hidden = f !== "yearly"; rows.day.hidden = f !== "monthly" && f !== "yearly";
        dom.disabled = !byDay.checked; nths.concat(wds).forEach((x) => { x.el.disabled = !byNth.checked; }); eachSel.disabled = !byNth.checked;
        // with auto-update on, the online update is for one node: the release staged on it reaches the others
        const oneNode = action.value === "update_online" && editorAuto && state.nodes.length > 1;
        allLabel.hidden = oneNode;
        if (oneNode) {
          someNodes.checked = true;
          const on = nodeBoxes.filter((x) => x.el.checked);
          if (!on.length) { const me = nodeBoxes.find((x) => x.n.self) || nodeBoxes[0]; if (me) me.el.checked = true; }
          else on.slice(1).forEach((x) => { x.el.checked = false; });
        }
        nodesList.hidden = !someNodes.checked;
      };
      [action, freq, every, byDay, byNth, allNodes, someNodes, until].forEach((el) => el.addEventListener("change", sync));
      nodeBoxes.forEach(({ el }) => el.addEventListener("change", () => { if (action.value === "update_online" && editorAuto && state.nodes.length > 1 && el.checked) nodeBoxes.forEach((x) => { if (x.el !== el) x.el.checked = false; }); }));
      every.addEventListener("input", sync); until.addEventListener("input", sync);
      const submit = (ev) => {
        ev.preventDefault();
        const f = freq.value;
        const task = { id: isNew ? "" : t.id, name: name.value.trim(), action: action.value, freq: f, every: f === "once" ? 1 : Math.max(1, +every.value || 1),
          window_sec: (f === "hourly" ? untilMin.value !== "" : until.value !== "") ? 0 : (f === "hourly" && legacyWin >= 3600 ? 0 : legacyWin), disabled: !enabled.checked };
        if (action.value === "shell") { task.command = cmd.value; task.timeout_sec = +tmo.value || 300; task.ignore_exit = ign.checked; }
        if (f === "hourly") { const m = Math.min(59, Math.max(0, +minute.value || 0)); task.at = "00:" + String(m).padStart(2, "0") + ":00"; } else task.at = at.value.length === 5 ? at.value + ":00" : at.value;
        if (f === "once") { if (!dateIn.value) { errorBox(err, "Pick the date."); return; } task.date = dateIn.value; }
        if (f === "weekly") task.days = days.filter((x) => x.el.checked).map((x) => x.i);
        if (f === "monthly" || f === "yearly") { if (byNth.checked) { task.nths = nths.filter((x) => x.el.checked).map((x) => x.v); task.weekdays = wds.filter((x) => x.el.checked).map((x) => x.i); if (!task.nths.length || !task.weekdays.length) { errorBox(err, "Pick at least one occurrence and one weekday."); return; } task.each_day = eachSel.value === "each"; task.pick_day = eachSel.value === "pick"; } else task.day = +dom.value; }
        if (f === "yearly") task.month = +month.value;
        if (["daily", "weekly", "monthly"].includes(f)) { const ms = months.filter((x) => x.el.checked).map((x) => x.i + 1); if (ms.length && ms.length < 12) task.months = ms; }
        if (f === "hourly" && untilMin.value !== "") {
          const um = Math.min(59, Math.max(0, +untilMin.value || 0));
          if (um === Math.min(59, Math.max(0, +minute.value || 0))) { errorBox(err, "The To minute must differ from the At minute."); return; }
          task.until = "00:" + String(um).padStart(2, "0");
        } else if (f !== "hourly" && until.value) task.until = until.value.slice(0, 5);
        if (state.nodes.length > 1 && someNodes.checked) task.nodes = nodeBoxes.filter((x) => x.el.checked).map((x) => x.n.node_id);
        if (state.nodes.length > 1 && someNodes.checked && !task.nodes.length) { errorBox(err, "Pick at least one node, or choose every node."); return; }
        if (!task.name) { errorBox(err, "Give the task a name."); return; }
        api("POST", "/api/schedule/task", { task }).then(() => { close(); VIEWS.scheduler.poll(true); }).catch((e) => { if (e.message !== "unauthenticated") errorBox(err, e.message); });
      };
      d.append(h("button", { class: "dlg-x", type: "button", "aria-label": "Close", title: "Close", onclick: close }, "✕"), h("h2", {}, isNew ? "New task" : "Edit task"),
        h("form", { onsubmit: submit },
          h("div", { class: "grid" },
            h("label", { class: "f wide" }, "Name", name),
            h("label", { class: "f wide" }, "Does", action), rows.shell, where,
            h("label", { class: "f" }, "How often", freq), rows.date, rows.every, rows.at, rows.minute, rows.days, rows.month, rows.day, rows.months,
            h("label", { class: "f check wide" }, enabled, "Enabled")),
          err,
          h("div", { class: "toolbar" }, h("button", { class: "btn primary", type: "submit" }, isNew ? "Add task" : "Save"), h("button", { class: "btn", type: "button", onclick: close }, "Cancel"))));
      root.append(d); sync(); d.showModal(); name.focus();
    }
    let editorActions = {}, editorAuto = false;

    const openRuns = new Set();   // history rows whose output is expanded: kept open when the page redraws
    function draw(st) {
      const sig = JSON.stringify([Object.assign({}, st, { now: 0 }), localTimes, openRuns.size, clip && clip.name, state.nodes.map((n) => [n.node_id, nodeLabel(n)])]);
      if (sig === last) return;
      last = sig;
      editorActions = st.actions || {}; editorAuto = !!st.auto_update;
      const tasks = st.tasks.map((v) => v.task);
      const real = box; box = h("div", {});
      try {
        const menu = (v) => rowMenu([["Edit", () => editor(v.task, tasks)], ["Copy", () => copyTask(v.task)], clip ? ["Paste", () => pasteTask(tasks)] : null, "-",
          ["Run now on", [["this node", () => { if (confirm("Run “" + v.name + "” on this node now?")) call("run", { id: v.id }); }],
            ["all nodes", () => { if (confirm("Run “" + v.name + "” on every node it is for now? The nodes start 1 minute apart, each when it is free.")) call("run", { id: v.id, all: true }); }]]],
          [v.disabled ? "Enable" : "Disable", () => call("task", { task: Object.assign({}, v.task, { disabled: !v.disabled }) })], "-",
          ["Delete", () => { if (confirm("Delete the task “" + v.name + "”?")) call("delete", { id: v.id }); }, "danger"]].filter(Boolean));
        const emptyMenu = rowMenu([["New task", () => editor(null, tasks)], clip ? ["Paste", () => pasteTask(tasks)] : null].filter(Boolean));
        const lastCell = (v) => v.last ? h("span", { title: v.last.output || "" }, pill(v.last.status, v.last.status === "ok" ? "ok" : v.last.status === "failed" ? "bad" : v.last.status === "running" ? "info" : "warn"), " ", when(v.last.start)) : h("span", { class: "muted" }, "never");
        const where = (v) => (v.task.nodes || []).length ? v.task.nodes.map(nodeName).join(", ") : "every node";
        const row = (v) => h("tr", { tabindex: "0", class: v.disabled ? "paused" : "", oncontextmenu: menu(v), ondblclick: () => editor(v.task, tasks) },
          h("td", {}, v.name), h("td", {}, actionLabel(v.task, st.actions), v.task.action === "shell" ? h("div", { class: "mono small muted sched-cmd", title: v.task.command }, v.task.command) : null),
          h("td", {}, where(v)), h("td", {}, v.describe),
          h("td", {}, v.disabled ? pill("disabled", "") : !v.applies ? h("span", { class: "muted" }, "not for this node") : v.running ? pill("running", "info") : v.queued ? pill("waiting for another task", "warn") : v.next_run ? h("span", { title: "scheduled for " + when(v.next) }, when(v.next_run)) : "–"),
          h("td", {}, lastCell(v)));
        box.append(section("Scheduled tasks",
          h("div", { class: "toolbar" }, h("button", { class: "btn primary", type: "button", onclick: () => editor(null, tasks) }, "New task")),
          h("div", { class: "scroll", oncontextmenu: emptyMenu }, h("table", { class: "sched" },
            h("thead", {}, h("tr", {}, ["Task", "Does", "Runs on", "Schedule", ["Next run", "(this node)"], ["Last run", "(this node)"]].map((x) => (Array.isArray(x) ? h("th", {}, x[0], h("br"), x[1]) : h("th", {}, x))))),
            h("tbody", {}, st.tasks.length ? st.tasks.map(row) : h("tr", {}, h("td", { colspan: 6, class: "empty" }, "No tasks yet. Add one with New task, or right-click here.")))))));
        const localBox = h("input", { type: "checkbox", checked: localTimes });
        localBox.addEventListener("change", () => { localTimes = localBox.checked; try { localStorage.setItem("sched_local_times", localTimes ? "1" : "0"); } catch (_) { /* no storage */ } last = ""; draw(st); });
        box.append(h("div", { class: "card" }, h("header", {}, h("h2", {}, tc("History of this node")), h("label", { class: "nt-opt sched-local", title: "Show the times in this browser's time zone instead of UTC" }, localBox, " Local time")), h("div", { class: "body" },
          h("div", { class: "scroll" }, h("table", { class: "sched" },
            h("thead", {}, h("tr", {}, ["Started", "Task", "Result", "Output"].map((x) => h("th", {}, x)))),
            h("tbody", {}, st.history.length ? st.history.slice(0, 60).map((r) => h("tr", {},
              h("td", {}, when(r.start)), h("td", {}, r.name),
              h("td", {}, pill(r.status, r.status === "ok" ? "ok" : r.status === "failed" ? "bad" : r.status === "running" ? "info" : "warn"), r.end && r.end > r.start ? h("span", { class: "small muted" }, " " + (r.end - r.start) + " s") : null),
              h("td", { class: "wrap" }, r.output ? h("details", { open: openRuns.has(r.task + "|" + r.start), ontoggle: (e) => { const k = r.task + "|" + r.start; if (e.target.open) openRuns.add(k); else openRuns.delete(k); } }, h("summary", {}, r.output.split("\n")[0].slice(0, 100)), h("pre", { class: "nt-out" }, r.output)) : ""))) : h("tr", {}, h("td", { colspan: 4, class: "empty" }, "Nothing has run on this node yet."))))))));
      } finally { morph(real, box); box = real; }
    }

    return {
      async mount(main) {
        status = h("div", { "aria-live": "polite" }); box = h("div", {}); last = "";
        main.append(status, box);
        try { draw((await api("GET", "/api/schedule")).data); } catch (e) { fail(status)(e); }
      },
      async poll(force) { if (force) last = ""; draw((await api("GET", "/api/schedule")).data); },
      poll_ms: 5000,
    };
  })();

  // ── Users  (CLI: --users, --user-add, --user-passwd, --user-expiry, --user-del) ──
  VIEWS.usersLocal = (() => {
    // Layout: the accounts in a list on the left, the chosen one (or the "new user" / "existing account" form) on the right.
    let status, listBox, detail, filterIn, users = [], others = [], group = "", readGroup = "", sel = null, mode = "user", otherList = null;
    const dateText = (u) => (u.expires ? new Date(u.expires * 1000).toISOString().slice(0, 10) : "never");
    const toUnix = (d) => (/^\d{4}-\d{2}-\d{2}$/.test(d) ? Math.floor(Date.UTC(+d.slice(0, 4), +d.slice(5, 7) - 1, +d.slice(8, 10), 12) / 1000) : 0);
    const me = () => (state.session && state.session.user) || "";
    const current = () => users.find((u) => u.name === sel) || null;
    // Access: full (may change things) or read-only; offered only while a read-only group is set
    const accessSel = (ro) => h("select", { "aria-label": "Access" }, h("option", { value: "full" }, "Full access"), h("option", { value: "ro", selected: ro ? "" : null }, "Read-only"));

    async function call(path, body, done) {
      try {
        const r = (await api("POST", "/api/users/" + path, body)).data;
        if (r.partial) say(status, "bad", r.message); else clear(status); // success shows in the list; only a node that missed it is worth saying
        if (done) done();
        await VIEWS.usersLocal.poll();
      } catch (e) { fail(status)(e); }
    }

    function drawList() {
      const real = listBox; listBox = h("div", { class: "ulist" });
      try {
        const flt = parseFilter(filterIn ? filterIn.value : "");
        const shown = users.filter((u) => flt.match((w) => u.name.toLowerCase().includes(w)));
        if (!users.length) listBox.append(h("div", { class: "empty" }, "Nobody is in the " + group + " group, so nobody can sign in."));
        else if (!shown.length) listBox.append(h("div", { class: "empty" }, "No user matches."));
        shown.forEach((u) => {
          const on = mode === "user" && u.name === sel;
          listBox.append(h("button", { type: "button", class: "uitem" + (on ? " on" : ""), "data-user": u.name,
            onclick: () => { sel = u.name; mode = "user"; drawList(); drawDetail(); } },
            h("span", { class: "uname" }, u.name, u.name === me() ? h("span", { class: "pill info tiny" }, "you") : null, u.read_only ? h("span", { class: "pill tiny" }, "read-only") : null),
            u.expired ? h("span", { class: "usub bad" }, "expired " + dateText(u)) : h("span", { class: "usub" }, u.expires ? "expires " + dateText(u) : "never expires")));
        });
      } finally { morph(real, listBox); listBox = real; }
    }

    let shown = "";   // what the detail panel is showing: the mode and the account
    function drawDetail() {
      shown = mode + "/" + (mode === "user" ? sel : "");
      clear(detail);
      if (mode === "new") {
        const name = h("input", { type: "text", autocomplete: "off", spellcheck: "false", autocapitalize: "none", maxlength: "32" });
        const pw = h("input", { type: "password", autocomplete: "new-password" });
        const exp = h("input", { type: "date" });
        const acc = readGroup ? accessSel(false) : null;
        const add = () => call("add", { username: name.value.trim(), password: pw.value, expires: toUnix(exp.value), read_only: !!acc && acc.value === "ro" }, () => { sel = name.value.trim(); mode = "user"; });
        pw.addEventListener("keydown", (e) => { if (e.key === "Enter") add(); });
        detail.append(h("header", {}, h("h2", {}, tc("New user"))), h("div", { class: "body" },
          h("div", { class: "grid c3" }, h("label", { class: "f" }, "Name", name), h("label", { class: "f" }, "Password", pw), h("label", { class: "f" }, "Expires (blank = never)", exp), acc ? h("label", { class: "f" }, "Access", acc) : null),
          h("div", { class: "toolbar" }, h("button", { class: "btn primary", type: "button", onclick: add }, "Add user"))));
        name.focus();
        return;
      }
      if (mode === "existing") {
        const who = h("input", { type: "text", list: "users-others", autocomplete: "off", spellcheck: "false", autocapitalize: "none", maxlength: "64" });
        const acc = readGroup ? accessSel(false) : null;
        const grant = () => call("grant", { username: who.value.trim(), read_only: !!acc && acc.value === "ro" }, () => { sel = who.value.trim(); mode = "user"; });
        who.addEventListener("keydown", (e) => { if (e.key === "Enter") grant(); });
        detail.append(h("header", {}, h("h2", {}, tc("Add an existing account"))), h("div", { class: "body" },
          h("p", { class: "muted small" }, "An account that already exists on this host, including one from a directory (Active Directory, LDAP through sssd), can be allowed to sign in here by adding it to the " + group + " group. A directory account keeps its password in the directory."),
          h("div", { class: "grid c3" }, h("label", { class: "f" }, "Name", who, otherList), acc ? h("label", { class: "f" }, "Access", acc) : null),
          h("div", { class: "toolbar" }, h("button", { class: "btn primary", type: "button", onclick: grant }, "Add to group"))));
        who.focus();
        return;
      }
      const u = current();
      if (!u) { detail.append(h("div", { class: "empty" }, users.length ? "Choose a user on the left." : "Add a user to get started.")); return; }
      const self = u.name === me();
      const pw = h("input", { type: "password", autocomplete: "new-password", "aria-label": "New password for " + u.name });
      const d = h("input", { type: "date", value: u.expires ? dateText(u) : "", "aria-label": "Expiry date for " + u.name });
      const acc = readGroup && !self ? accessSel(!!u.read_only) : null;
      const note = h("span", { class: "usaved", "aria-live": "polite" });
      // Save sends what changed: a new password if one was typed, the date if it differs (blank = never expires)
      const save = async () => {
        const was = u.expires ? dateText(u) : "";
        const accChanged = !!acc && (acc.value === "ro") !== !!u.read_only;
        if (!pw.value && d.value === was && !accChanged) { note.textContent = "Nothing to save"; return; }
        note.textContent = "";
        if (pw.value) {
          let ok = true;
          await call("password", { username: u.name, password: pw.value }, () => { pw.value = ""; });
          if (pw.value) ok = false;   // still there: the call failed and the message is shown above
          if (!ok) return;
        }
        if (d.value !== was) await call("expiry", { username: u.name, expires: d.value ? toUnix(d.value) : 0 });
        if (accChanged) await call("access", { username: u.name, read_only: acc.value === "ro" });
        drawDetail();
        const n = detail.querySelector(".usaved"); if (n) n.textContent = "Saved";
      };
      pw.addEventListener("keydown", (e) => { if (e.key === "Enter") save(); });
      const body = h("div", { class: "body ubody" },
        h("div", { class: "urow" }, h("label", { class: "f grow" }, "New password", pw)),
        h("div", { class: "urow" }, h("label", { class: "f grow" }, "Expires (blank = never)", d)),
        acc ? h("div", { class: "urow" }, h("label", { class: "f grow" }, "Access", acc)) : null);
      const left = [];
      if (!self) {
        left.push(h("button", { class: "btn", type: "button", onclick: () => {
          if (confirm("Disable " + u.name + "?\n\nThe account is kept; it just cannot sign in here any more.")) call("revoke", { username: u.name });
        } }, "Disable"),
        h("button", { class: "btn danger", type: "button", onclick: () => {
          if (confirm("Delete the account " + u.name + "?")) call("delete", { username: u.name });
        } }, "Delete"));
      }
      body.append(h("div", { class: "urow actions" }, ...left, h("span", { class: "grow" }), note, h("button", { class: "btn primary", type: "button", onclick: save }, "Save")));
      detail.append(h("header", {}, h("h2", {}, u.name)), body);
    }

    return {
      mount(main) {
        status = h("div", { "aria-live": "polite" });
        listBox = h("div", { class: "ulist" });
        detail = h("div", { class: "card udetail" });
        otherList = h("datalist", { id: "users-others" });
        filterIn = h("input", { type: "text", class: "ufilter", placeholder: "Filter", "aria-label": "Filter users", autocomplete: "off", spellcheck: "false", oninput: drawList });
        sel = null; mode = "user";
        const left = h("div", { class: "card ulistcard" },
          h("header", { class: "bar" }, h("h2", {}, tc("Users")), h("span", { class: "grow" }),
            h("button", { class: "btn primary small", type: "button", onclick: () => { mode = "new"; drawList(); drawDetail(); } }, "+ New")),
          h("div", { class: "ufilterbar" }, filterIn),
          listBox,
          h("div", { class: "ufoot" }, h("button", { class: "btn", type: "button", onclick: () => { mode = "existing"; drawList(); drawDetail(); } }, "Add an existing account")));
        main.append(status, h("div", { class: "usersplit" }, left, detail));
      },
      async poll() {
        const r = (await api("GET", "/api/users")).data;
        users = r.users || [];
        others = r.others || [];
        group = r.group;
        readGroup = r.read_group || "";
        if (otherList) clear(otherList).append(...others.map((n) => h("option", { value: n })));
        if (mode === "user" && !current()) sel = users.length ? users[0].name : null;
        drawList();
        // the panel is left alone on a poll so what is being typed survives, and redrawn when what it should show has changed
        if (!detail.firstChild || shown !== mode + "/" + (mode === "user" ? sel : "")) drawDetail();
      },
    };
  })();

  // Configure ▸ Users: two tabs, Local (the accounts of this host) and SSO (Google / Microsoft sign-in and who may use it)
  VIEWS.users = (() => {
    return {
      mount(main) {
        const TABS = [["Local", VIEWS.usersLocal], ["LDAP", VIEWS.cfg_ldap], ["SSO", VIEWS.cfg_sso]];
        const panes = TABS.map(([, v]) => { const p = h("div", { role: "tabpanel" }); v.mount(p); return p; });
        const buttons = TABS.map(([name], i) => h("button", { type: "button", role: "tab", class: "dtab", onclick: () => show(i) }, name));
        function show(i, focus) {
          state.usersTab = i;
          if (document.getElementById("help-panel") && document.getElementById("help-panel").classList.contains("open") && state.tab === "users") queueMicrotask(fillHelp);
          panes.forEach((p, j) => { p.hidden = j !== i; });
          buttons.forEach((b, j) => { b.setAttribute("aria-selected", j === i ? "true" : "false"); b.tabIndex = j === i ? 0 : -1; b.classList.toggle("on", j === i); });
          if (focus) buttons[i].focus();
        }
        const strip = h("div", { class: "dtabs", role: "tablist", "aria-label": "Users" }, buttons);
        strip.addEventListener("keydown", (e) => {
          const d = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
          if (!d) return;
          e.preventDefault();
          show((state.usersTab + d + TABS.length) % TABS.length, true);
        });
        main.append(strip, ...panes);
        show(state.usersTab >= 0 && state.usersTab < TABS.length ? state.usersTab : 0);
      },
      poll: () => VIEWS.usersLocal.poll(),
      poll_ms: 5000,
    };
  })();

  // ── Node, Operate  (CLI: --assert-agc, --node-pause/-resume/-status, --power) ──
  VIEWS.node = (() => {
    let status, pending, act, whenSel, mins, clock, takeBtn, npBtn, npPaused = false;
    const radio = (name, value, label, checked, ...extra) => {
      const r = h("input", { type: "radio", name, value, checked: !!checked });
      return h("label", { class: "opt" }, r, " ", label, ...extra);
    };
    const val = (name) => { const r = document.querySelector('input[name="' + name + '"]:checked'); return r ? r.value : ""; };

    const showNodePause = () => {
      npBtn.textContent = npPaused ? "Resume this node" : "Pause this node";
      npBtn.className = npPaused ? "btn primary" : "btn danger";
    };
    async function takeOver() {
      if (!confirm("Make this node the gateway controller for all groups?\n\nThe node that currently answers for the shared address is asked to step down once this node has taken the role.")) return;
      takeBtn.disabled = true;
      try {
        const r = await api("POST", "/api/assert-agc", {});
        if (!r.ok) say(status, "bad", r.error || "Could not take over as the gateway controller");
        else say(status, "info", "Asked to take over: " + (r.messages || []).join(" · "));   // what happened, per group and address family
      } catch (e) { fail(status)(e); }
      finally { takeBtn.disabled = false; }
    }
    async function toggleNodePause() {
      const want = !npPaused;
      if (want && !confirm("Pause this node?\n\nAll of its gateways stop serving and the other nodes take over. Clients are not interrupted as long as another node is serving. Resume it when you are done.")) return;
      try {
        await api("POST", "/api/nodepause", { paused: want });
        npPaused = want; showNodePause();
      } catch (e) { fail(status)(e); }
    }

    async function go(req) {
      const verb = req.action === "restart" ? "restart" : "shut down";
      const whenText = req.when === "now" ? "now" : req.when === "in" ? "in " + req.minutes + " minute(s)" : "at " + req.time;
      if (!confirm(verb.charAt(0).toUpperCase() + verb.slice(1) + " the whole host " + whenText + "?\n\n" +
        (req.action === "restart" ? "The host reboots and you lose access to this page until it is back." : "The host powers off and stays off until it is powered on again.") +
        (req.when === "now" ? "" : "\n\nThe gateway is not checked for cover at the scheduled time; make sure another member is serving by then."))) return;
      try {
        const r = (await api("POST", "/api/power", req)).data;
        say(status, "info", "Host " + (req.action === "restart" ? "restart" : "shutdown") + " " + r.when + ".");
        await VIEWS.node.poll();
      } catch (e) {
        // a gateway would lose its last serving member: say so and let the admin decide
        if (/^not safe to /.test(e.message || "") && confirm(e.message.replace(/ \(to go ahead anyway.*$/, "") + "\n\nGo ahead anyway? Clients of that gateway will be interrupted.")) {
          try { const r = (await api("POST", "/api/power", { ...req, force: true })).data; say(status, "info", "Host " + (req.action === "restart" ? "restart" : "shutdown") + " " + r.when + "."); await VIEWS.node.poll(); } catch (e2) { fail(status)(e2); }
          return;
        }
        fail(status)(e);
      }
    }

    function execute() {
      const req = { action: val("power-act") || "restart", when: val("power-when") || "now" };
      if (req.when === "in") {
        req.minutes = parseInt(mins.value, 10);
        if (!(req.minutes >= 1 && req.minutes <= 10080)) { say(status, "bad", "Enter a number of minutes between 1 and 10080."); return; }
      } else if (req.when === "at") {
        req.time = clock.value;
        if (!/^\d{2}:\d{2}$/.test(req.time)) { say(status, "bad", "Choose a time of day."); return; }
      }
      go(req);
    }

    return {
      mount(main) {
        status = h("div", { "aria-live": "polite" });
        pending = h("div", {});
        mins = h("input", { type: "number", min: "1", max: "10080", value: "5", class: "narrow", "aria-label": "Minutes", onfocus: () => { document.querySelector('input[name="power-when"][value="in"]').checked = true; } });
        clock = h("input", { type: "time", "aria-label": "Time of day", onfocus: () => { document.querySelector('input[name="power-when"][value="at"]').checked = true; } });
        takeBtn = h("button", { class: "btn warn", type: "button", onclick: takeOver }, "Make this node the gateway controller");
        npBtn = h("button", { class: "btn", type: "button", onclick: toggleNodePause }, "…");
        main.append(status,
          section("Gateway controller", h("div", { class: "toolbar" }, takeBtn)),
          section("Maintenance", h("div", { class: "toolbar" }, npBtn)),
          section("Host",
            h("fieldset", { class: "radios" }, h("legend", {}, "Action"),
              radio("power-act", "restart", "Restart host", true), radio("power-act", "shutdown", "Shut down host", false)),
            h("fieldset", { class: "radios" }, h("legend", {}, "When"),
              radio("power-when", "now", "Now", true),
              radio("power-when", "in", "In ", false, mins, " minutes"),
              radio("power-when", "at", "At ", false, clock, " (24-hour)"),
              pending),   // the scheduled-action line and Cancel it sit in this box
            h("div", { class: "toolbar" },
              h("button", { class: "btn danger", type: "button", onclick: execute }, "Execute"))));
      },
      async poll() {
        try { npPaused = !!(await api("GET", "/api/nodepause")).data.paused; showNodePause(); } catch (_) { /* keep the last state */ }
        const p = (await api("GET", "/api/power")).data;
        clear(pending);
        if (p.scheduled) {
          pending.append(h("div", { class: "muted" },
            "A " + p.action + " is scheduled for " + when(p.at) + ". ",
            h("button", { class: "linkbtn", type: "button", onclick: async () => {
              try { await api("POST", "/api/power", { action: "cancel" }); say(status, "info", "The scheduled " + p.action + " is cancelled."); await VIEWS.node.poll(); } catch (e) { fail(status)(e); }
            } }, "Cancel it")));
        }
      },
      poll_ms: 5000,
    };
  })();

  // ── boot ──────────────────────────────────────────────────────────────────
  (async () => {
    try {
      state.session = await api("GET", "/api/session");
      checkBuild(state.session.version);
      showShell();
    } catch (e) {
      if (e.message !== "unauthenticated") showLogin();
    }
  })();
})();
