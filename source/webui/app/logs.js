"use strict";
// LogDirector web GUI: the logs page.

// apiStream: a GET whose answer comes in parts (…&stream=1, application/x-ndjson: one JSON line per part, each the whole answer so far,
// the last with "done": true).  onPart(line) is called for each; a daemon that answers in one piece (an older node, a proxy that
// waits for the end) gives one call.  Errors as api(); signal drops it.
async function apiStream(path, signal, onPart) {
  const res = await fetch(route(path + (path.includes("?") ? "&" : "?") + "stream=1"), { method: "GET", credentials: "same-origin", signal });
  if (res.status === 401) { stopAllTimers(); state.session = null; showLogin(); throw new Error("unauthenticated"); }
  if (!res.ok || !/ndjson/.test(res.headers.get("Content-Type") || "") || !res.body) {
    let data = null;
    try { data = await res.json(); } catch (_) { /* non-JSON error */ }
    if (!res.ok) { const err = new Error((data && data.error) || res.statusText || "request failed"); err.status = res.status; err.data = data; throw err; }
    if (data && data.ok && data.data) onPart({ ...data, done: true });
    return;
  }
  const rd = res.body.getReader(), dec = new TextDecoder();
  let buf = "";
  const take = (line) => { if (line.trim()) onPart(JSON.parse(line)); };
  for (;;) {
    const { value, done } = await rd.read();
    if (done) break;
    buf += dec.decode(value, { stream: true });
    let i;
    while ((i = buf.indexOf("\n")) >= 0) { take(buf.slice(0, i)); buf = buf.slice(i + 1); }
  }
  take(buf + dec.decode());
}

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
  const SEVS = [["", "Any severity"], ["crit", "Critical or worse"], ["err", "Error or worse"], ["warning", "Warning or worse"], ["warning=", "Warning only"], ["notice", "Notice or worse"], ["info", "Info or worse"],
    ["warning-", "Warning or less severe"], ["notice-", "Notice or less severe"], ["info-", "Info and debug"], ["debug=", "Debug only"]];
  const REFRESH_MS = 15000, FOLLOW_MS = 2000, FOLLOW_KEEP = 2000;
  // what the page keeps when it is left and come back to
  const kept = { range: "15m", severity: "", host: "", program: "", source: "", q: "", page: 0, size: 25, field: "", measure: "", over: false };
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

  let tiles, chartBox, msgBox, hostBox, valBox, qIn, rangeSel, sevSel, savedBtn, saveBtn, busyEl, busyText, savedMenu;
  // A load that takes longer than BUSY_MS shows "Searching… N s" with Stop in the toolbar, and what it is replacing is dimmed until
  // the new answer for that part comes in; a quick one shows nothing, so the page does not flicker.
  const BUSY_MS = 700;
  let busyTimer = 0, busyTick = 0, busyFrom = 0;
  const dimmed = () => [tiles, chartBox, hostBox, valBox, msgBox];
  function busyStart(auto) {
    busyFrom = Date.now();
    if (busyTick) {   // already showing (a new filter while the last was still out): keep it up, count from now
      if (!auto) for (const b of dimmed()) { b.classList.add("qstale"); b.setAttribute("aria-busy", "true"); }
      busyText.textContent = "Searching… 0 s";
      clearInterval(busyTick);
      busyTick = setInterval(() => { busyText.textContent = "Searching… " + Math.round((Date.now() - busyFrom) / 1000) + " s"; }, 1000);
      return;
    }
    busyEnd();
    busyTimer = setTimeout(() => {
      busyEl.hidden = false; busyEl.classList.remove("stopped");
      if (!auto) for (const b of dimmed()) { b.classList.add("qstale"); b.setAttribute("aria-busy", "true"); }   // a refresh keeps its results bright: they are still right
      const tick = () => { busyText.textContent = "Searching… " + Math.round((Date.now() - busyFrom) / 1000) + " s"; };
      tick(); busyTick = setInterval(tick, 1000);
    }, BUSY_MS);
  }
  function busyEnd(stopped) {
    clearTimeout(busyTimer); clearInterval(busyTick); busyTimer = busyTick = 0;
    for (const b of dimmed()) if (b) fresh(b);
    if (!busyEl) return;
    busyEl.hidden = !stopped;
    if (stopped) { busyEl.classList.add("stopped"); busyText.textContent = "Stopped"; }
  }
  const fresh = (...boxes) => { for (const b of boxes) { b.classList.remove("qstale"); b.removeAttribute("aria-busy"); } };
  let saved = [], valSer = null;   // the saved searches; the values card's lines over time ({ field, names, pts, cols })
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
    qIn.value = filterText(); sevSel.value = kept.severity; rangeSel.value = kept.range;
    drawSaved();
    load();
  }
  // ── saved searches (settings.logs.saved, shared): the bookmark lists them and opens one, the disk names what the page shows;
  // both sit right after the filter box, the bookmark first ──
  const savedOf = () => ({ q: kept.q, range: kept.range, severity: kept.severity, host: kept.host, program: kept.program, source: kept.source,
    field: kept.field, measure: kept.field ? kept.measure : "", over_time: !!(kept.field && kept.over) });
  const sameAs = (x) => { const c = savedOf(); return Object.keys(c).every((k) => (x[k] || (k === "range" ? "1h" : k === "over_time" ? false : "")) === c[k]); };
  function drawSaved() {
    if (!savedBtn) return;
    const cur = saved.find(sameAs);
    savedBtn.classList.toggle("cur", !!cur);   // a filled bookmark: what the page shows is a saved search
    savedBtn.title = cur ? "Saved search: " + cur.name : saved.length ? "Saved searches" : "No saved searches";
    if (saveBtn) saveBtn.hidden = !!(state.session && state.session.read_only);
    if (savedMenu) openSavedMenu();   // open while the list changed: draw it again
  }
  // the list under the bookmark: name, and what it holds below it; ✓ on the one the page shows. Esc, a click elsewhere, the
  // window losing focus or changing size close it; up and down move through it.
  function closeSavedMenu(refocus) {
    if (!savedMenu) return;
    savedMenu.remove(); savedMenu = null;
    savedBtn.setAttribute("aria-expanded", "false");
    document.removeEventListener("click", savedMenuClick, true);
    document.removeEventListener("keydown", savedMenuKey, true);
    window.removeEventListener("blur", closeSavedMenuNow);
    window.removeEventListener("resize", closeSavedMenuNow);
    if (refocus) savedBtn.focus();
  }
  const closeSavedMenuNow = () => closeSavedMenu(false);
  const savedMenuClick = (e) => { if (savedMenu && !savedMenu.contains(e.target) && !savedBtn.contains(e.target)) closeSavedMenu(false); };
  const savedMenuKey = (e) => {
    if (e.key === "Escape") { e.preventDefault(); closeSavedMenu(true); return; }
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    const items = [...savedMenu.querySelectorAll("button")];
    if (!items.length) return;
    e.preventDefault();
    const i = items.indexOf(document.activeElement);
    items[(i + (e.key === "ArrowDown" ? 1 : items.length - 1)) % items.length].focus();
  };
  function openSavedMenu() {
    const again = !!savedMenu;
    if (savedMenu) { savedMenu.remove(); savedMenu = null; }
    const cur = saved.find(sameAs);
    const list = saved.slice().sort((a, b) => a.name.localeCompare(b.name));
    savedMenu = h("div", { class: "cv-menu qsaved-menu", role: "menu", "aria-label": "Saved searches" }, list.length
      ? list.map((x) => h("button", { type: "button", role: "menuitemradio", "aria-checked": String(x === cur), class: x === cur ? "cur" : "",
        onclick: () => { closeSavedMenu(true); openSaved(x); } }, h("span", { class: "qsaved-name" }, x === cur ? "✓ " + x.name : x.name), h("span", { class: "muted small" }, savedText(x))))
      : [h("div", { class: "qsaved-none muted small" }, "No saved searches")]);
    document.body.append(savedMenu);
    const br = savedBtn.getBoundingClientRect(), r = savedMenu.getBoundingClientRect();
    savedMenu.style.left = Math.max(4, Math.min(br.right - r.width, window.innerWidth - r.width - 4)) + "px";   // right edge under the bookmark
    savedMenu.style.top = (br.bottom + r.height + 6 <= window.innerHeight ? br.bottom + 4 : Math.max(4, br.top - r.height - 4)) + "px";
    savedBtn.setAttribute("aria-expanded", "true");
    if (again) return;
    const first = savedMenu.querySelector("button.cur") || savedMenu.querySelector("button");
    if (first) first.focus();
    document.addEventListener("click", savedMenuClick, true);
    document.addEventListener("keydown", savedMenuKey, true);
    window.addEventListener("blur", closeSavedMenuNow);
    window.addEventListener("resize", closeSavedMenuNow);
  }
  function savedText(x) {
    const r = RANGES.find((y) => y[0] === (x.range || "1h")), sv = SEVS.find((y) => y[0] === (x.severity || ""));
    const filt = [x.q, x.host && "host=" + x.host, x.program && "program=" + x.program, x.source && "source=" + x.source].filter(Boolean).join(" ");
    return [filt || "every message", r ? r[1] : x.range, sv ? sv[1] : x.severity, x.field ? "values of " + x.field + (x.measure ? " (" + x.measure.replace(/^distinct:/, "different ").replace(/^sum:/, "sum of ") + ")" : "") + (x.over_time ? " over time" : "") : ""].filter(Boolean).join(" · ");
  }
  async function loadSaved() {
    try { saved = (await api("GET", "/api/logs/saved")).saved || []; } catch (_) { saved = []; }
    drawSaved();
  }
  function openSaved(x) {
    set({ q: x.q || "", range: x.range || "1h", severity: x.severity || "", host: x.host || "", program: x.program || "", source: x.source || "",
      field: x.field || "", measure: x.measure || "", over: !!x.over_time });
  }
  function saveDialog() {
    const cur = saved.find(sameAs);
    const name = h("input", { type: "text", value: cur ? cur.name : "", placeholder: "port scans", spellcheck: "false", autocomplete: "off", maxlength: "64" });
    const what = h("p", { class: "muted small" }, savedText(savedOf()));
    const hint = h("span", { class: "hint" });
    const err = h("span", { class: "bad-text" });
    const clash = () => saved.find((x) => x.name.toLowerCase() === name.value.trim().toLowerCase());
    const upd = () => { const c = clash(); hint.textContent = c && c !== cur ? "Replaces the saved search " + c.name + "." : c ? "Saves it again with what the page shows now." : ""; };
    name.oninput = upd; upd();
    const call = async (body, close) => {
      try { saved = (await api("PUT", "/api/logs/saved", body)).saved || []; drawSaved(); close(); }
      catch (e) { if (e.message !== "unauthenticated") err.textContent = e.message; }
    };
    dialog(cur ? "Saved search " + cur.name : "Save this search", (close) => {
      const form = h("form", { class: "shipform", onsubmit: (e) => {
        e.preventDefault();
        const n = name.value.trim();
        if (!n || n.length > 64) { err.textContent = "Name: 1 to 64 characters."; return; }
        call({ save: { name: n, ...savedOf() }, ...(cur && cur.name.toLowerCase() !== n.toLowerCase() ? { rename: cur.name } : {}) }, close);
      } },
      h("div", { class: "grid" }, h("label", { class: "f wide" }, "Name", name, hint)), what,
      h("div", { class: "toolbar right" }, err, h("span", { class: "grow" }),
        cur ? h("button", { type: "button", class: "btn danger", onclick: () => call({ delete: cur.name }, close) }, "Delete") : null,
        h("button", { type: "submit", class: "btn primary" }, "Save"), h("button", { type: "button", class: "btn", onclick: close }, "Cancel")));
      return [form];
    });
    name.focus();
  }
  // a new shipping destination with what the page is filtered on now, or, with no filter, this message's host and program (Configure ▸ Shipping)
  function shipLikeThis(m) {
    const f = shipFilterParse(filterText());
    if (!Object.keys(f).length) { if (m.host) f.host = m.host; if (m.program) f.program = m.program; }
    state.prefill.ship = { type: "syslog", ...f, ...shipSevFields(kept.severity) };
    selectTab("cfg_shipping");
  }
  // a new drop rule: the page's filter and severity, or that message's program and its first words
  function dropLikeThis(m) {
    let f = shipFilterParse(filterText());
    if (!Object.keys(f).length) {
      f = {};
      if (m.program) f.program = m.program;
      const words = [];
      for (const w of String(m.message || "").trim().split(/\s+/)) {
        if (words.length >= 4 || !/^[A-Za-z][A-Za-z_-]*:?$/.test(w)) break;
        words.push(w.replace(/:$/, "").toLowerCase());
      }
      if (words.length) f.query = words.join(" ");
      if (!f.program && !f.query && m.host) f.host = m.host;
    }
    state.prefill.drop = { name: (f.program || f.host || "noise") + (f.query ? " " + f.query.split(" ")[0] : ""), ...f, ...dropSevFields(kept.severity) };
    selectTab("cfg_senders");
  }
  const sevCls = (n) => "sev sev-" + (SEVNAMES.indexOf(n) < 0 ? 6 : SEVNAMES.indexOf(n));
  const showTime = () => kept.range !== "15m" && kept.range !== "1h" && kept.range !== "6h";

  function messageMenu(m) {
    const own = [["Only this host", () => set({ host: m.host || "" })], ["Only this program", () => set({ program: m.program || "" })],
      ["This severity or worse", () => set({ severity: m.severity })], ["Only this sender", () => set({ source: m.source || "" })],
      ["Copy message", () => { try { navigator.clipboard.writeText(m.message); } catch (_) { /* no clipboard */ } }],
      ["Ship messages like this…", () => shipLikeThis(m)], ["Drop messages like this…", () => dropLikeThis(m)]].filter((it) => !(it[0] === "Only this host" && !m.host) && !(it[0] === "Only this program" && !m.program));
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
      ["Top values of " + f.k, () => { kept.field = f.k; kept.measure = ""; set({}); }], "-",
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
    const sizeSel = h("select", { "aria-label": "Messages per page", title: "Messages per page", onchange: (e) => { kept.size = Number(e.target.value); kept.page = 0; drawMessages(); } },
      [25, 50, 100, 250, 500].map((n) => h("option", { value: String(n) }, String(n))));   // just the number: the header must stay on one line
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
    // nodes still out (the list or the counts), then how long each took
    const pending = [...new Set([...(r.pending || []), ...((stats && stats.pending) || [])])];
    const times = (r.node_ms || []).slice().sort((a, b) => b.ms - a.ms);
    const nodeSt = pending.length
      ? h("span", { class: "muted small", role: "status", title: "Still waiting for " + pending.join(", ") }, " " + ((r.nodes || 1) - pending.length) + " of " + (r.nodes || 1) + " nodes…")
      : multi && times.length ? h("span", { class: "muted small", title: times.map((x) => x.node + ": " + count(x.ms) + " ms").join("\n") }, " " + r.nodes + " nodes") : null;
    const csv = () => [["Time (UTC)", "Severity", "Facility", "Host", "Program", "PID", "Message", "Sender", "Sender DNS name", "Via", "Node"], ...all.map((m) => [m.time, m.severity, m.facility, m.host || "", m.program || "", m.pid || "", m.message, m.source || "", m.source_name || "", m.via || "", m.node || ""])];
    // one line: the filter box gives up width first (22rem down to 9rem), so the count, pages, page size and CSV stay beside it
    clear(msgBox).append(h("div", { class: "card" }, h("header", { class: "bar qmsgbar" }, h("h2", {}, tc(following ? "Messages (following)" : "Messages")), nodeErr, nodeSt, note, h("span", { class: "grow" }),
      h("span", { class: "qfiltbox" }, qIn), savedBtn, saveBtn, h("span", { class: "grow" }),
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
    if (kept.field && stats) hostBox.append(valuesCard());
  }
  // the fields the menus offer: those of the messages listed, and host, program, sender, severity; numeric: those whose values are numbers
  function knownFields() {
    const all = new Map();
    for (const m of msgs) for (const f of m.fields || []) {
      const num = f.v !== "" && Number.isFinite(Number(f.v));
      all.set(f.k, (all.get(f.k) ?? true) && num);
    }
    for (const k of ["host", "program", "sender", "severity"]) if (!all.has(k)) all.set(k, false);
    return all;
  }
  const fmtNum = (v) => (Number(v) || 0).toLocaleString(undefined, { maximumFractionDigits: 2 });
  // The values card: for each value of the field, its messages, and what the Count menu adds (how many different values of another
  // field, or the sum of its numbers); Over time draws the top five.
  function valuesCard() {
    const f = kept.field, rows = stats.top_values || [], total = stats.total || 0;
    const [kind, mf] = (() => { const m = stats.measure || ""; const i = m.indexOf(":"); return i < 0 ? ["", ""] : [m.slice(0, i), m.slice(i + 1)]; })();
    const fields = knownFields();
    const msel = h("select", { "aria-label": "Count per value", title: "What to count for each value", onchange: () => { kept.measure = msel.value; set({}); } },
      h("option", { value: "" }, "Messages"),
      [...fields.keys()].filter((k) => k !== f).sort().map((k) => h("option", { value: "distinct:" + k }, "Different " + k)),
      [...fields].filter(([k, num]) => num && k !== f).map(([k]) => k).sort().map((k) => h("option", { value: "sum:" + k }, "Sum of " + k)));
    if (kept.measure && ![...msel.options].some((o) => o.value === kept.measure)) msel.append(h("option", { value: kept.measure }, (kept.measure.startsWith("sum:") ? "Sum of " : "Different ") + kept.measure.replace(/^[a-z]+:/, "")));
    msel.value = kept.measure;
    const over = h("button", { type: "button", class: "btn small", "aria-pressed": String(kept.over), title: "Draw the five top values over time", onclick: () => { kept.over = !kept.over; set({}); } }, "Over time");
    const close = h("button", { type: "button", class: "btn small", title: "Close", "aria-label": "Close the values of " + f, onclick: () => { kept.field = ""; kept.measure = ""; kept.over = false; set({}); } }, "×");
    const mcol = kind === "distinct" ? "Different " + mf : kind === "sum" ? "Sum of " + mf : "";
    const mcell = (x) => {
      if (kind === "distinct") {
        const d = x.distinct || 0;
        return h("td", { class: "num", title: d > 128 ? "Estimated: about 1.6% either way" : null }, (d > 128 ? "≈ " : "") + count(d));
      }
      return h("td", { class: "num", title: x.numeric < x.count ? count(x.numeric) + " of " + count(x.count) + " messages have a number in " + mf : null }, fmtNum(x.sum));
    };
    const body = rows.length
      ? h("div", { class: "scroll" }, h("table", {}, h("thead", {}, h("tr", {}, h("th", {}, "Value"), h("th", { class: "num" }, "Messages"), mcol ? h("th", { class: "num" }, mcol) : null, h("th", { class: "num" }, "Share"))),
        h("tbody", {}, rows.map((x) => h("tr", {}, h("td", {}, h("button", { type: "button", class: "qlink", title: "Only this " + f, onclick: () => addWord(fieldWord({ k: f, v: x.name })) }, x.name)),
          h("td", { class: "num" }, count(x.count)), mcol ? mcell(x) : null, h("td", { class: "num" }, total ? (x.count * 100 / total).toFixed(1) + "%" : "–"))))))
      : h("div", { class: "empty" }, "No message in this range has " + f + ".");
    const csv = () => [[f, "Messages", ...(mcol ? [mcol] : [])], ...rows.map((x) => [x.name, x.count, ...(mcol ? [kind === "distinct" ? x.distinct || 0 : x.sum || 0] : [])])];
    const head = h("h2", {}, tc("Values of") + " ", h("span", { class: "mono" }, f));
    if (stats.values > rows.length) head.append(h("span", { class: "muted small" }, " " + count(stats.values) + " different"));
    return h("div", { class: "card" }, h("header", { class: "bar" }, head, h("span", { class: "grow" }), msel, over, rows.length ? csvButton("Values of " + f, csv) : null, close), h("div", { class: "body flush" }, body));
  }
  // the five top values over time: one line each
  function drawValues() {
    clear(valBox);
    if (!kept.field || !kept.over || !valSer || valSer.field !== kept.field) return;
    const { names, pts, cols } = valSer, n = pts.length;
    let body;
    if (!n || !names.length) body = h("div", { class: "empty" }, "No values to draw.");
    else {
      const max = niceMax(Math.max(1, ...names.flatMap((v) => cols[v] || [])));
      const W = Math.max(480, Math.round((valBox.clientWidth || 794) - 34)), H = 220, L = axisMargin([0, 1, 2, 3, 4].map((g) => count(Math.round((max * g) / 4))), 44), R = 12, T = 12, B = 28, pw = W - L - R, ph = H - T - B;
      const x = (i) => L + (n < 2 ? pw / 2 : (pw * i) / (n - 1)), y = (v) => T + ph - (ph * Math.min(v, max)) / max;
      const svg = sv("svg", { class: "qchart", viewBox: `0 0 ${W} ${H}`, role: "img", "aria-label": "Values of " + kept.field + " over time" });
      for (let g = 0; g <= 4; g++) {
        const v = (max * g) / 4;
        svg.append(sv("line", { class: "qgrid", x1: L, x2: W - R, y1: y(v), y2: y(v) }), sv("text", { class: "qax", x: L - 6, y: y(v) + 4, "text-anchor": "end" }, count(Math.round(v))));
      }
      const long = pts[n - 1].t - pts[0].t > 20 * 3600;
      names.forEach((v, j) => {
        const col = cols[v] || [], cls = "t" + (j + 1);
        svg.append(sv("path", { class: "qline " + cls, d: col.map((c, i) => (i ? "L" : "M") + x(i).toFixed(1) + " " + y(c).toFixed(1)).join(" ") }));
        col.forEach((c, i) => { if (c > 0) svg.append(sv("circle", { class: "qpt " + cls, cx: x(i).toFixed(1), cy: y(c).toFixed(1), r: n > 120 ? 1.5 : 2.5 }, sv("title", {}, v + "\n" + new Date(pts[i].t * 1000).toLocaleString() + "\n" + count(c) + " message" + (c === 1 ? "" : "s")))); });
      });
      const ticks = n < 2 ? [0] : [...new Set([0, 1, 2, 3, 4, 5].map((k) => Math.round((k * (n - 1)) / 5)))];
      for (const i of ticks) svg.append(sv("text", { class: "qax", x: x(i), y: H - 8, "text-anchor": i === 0 ? "start" : i === n - 1 ? "end" : "middle" }, stamp(pts[i].t, long)));
      const legend = h("div", { class: "qlegend" }, names.map((v, j) => h("span", { class: "qchip t" + (j + 1) }, h("span", { class: "sw" }), v)));
      body = h("div", {}, legend, svg);
    }
    const csv = () => [["Time (UTC)", ...names], ...pts.map((p, i) => [csvIso(p.t), ...names.map((v) => (cols[v] || [])[i] || 0)])];
    valBox.append(h("div", { class: "card" }, h("header", { class: "bar" }, h("h2", {}, tc("Values of") + " ", h("span", { class: "mono" }, kept.field), " over time"), h("span", { class: "grow" }),
      n ? csvButton("Values of " + kept.field + " over time", csv) : null), h("div", { class: "body" }, body)));
  }
  async function loadValues(my, signal) {
    if (!kept.field || !kept.over || !stats || stats.field !== kept.field) { valSer = null; drawValues(); return; }
    const names = (stats.top_values || []).slice(0, 5).map((x) => x.name);
    const p = params({ series: "1", field: kept.field });
    for (const v of names) p.append("values", v);
    const r = await api("GET", "/api/logs/stats?" + p.toString(), undefined, false, signal);
    if (my !== seq) return;
    valSer = { field: kept.field, names, pts: r.data.series || [], cols: r.data.value_series || {} };
    fresh(valBox);
    drawValues();
  }
  // load: the messages, the counts and the senders.  A new load drops the one still running (typing a filter starts one at every
  // pause; over 30 days each can take seconds, and the daemon stops reading when its request is dropped).
  async function load(auto) {
    const qs = params({ fields: "1" }).toString(), sq = params({ series: "1", top: "10", ...(kept.field ? { field: kept.field } : {}), ...(kept.field && kept.measure ? { measure: kept.measure } : {}) }).toString();
    if (inflight && inflightKey === qs + "&" + sq) return;   // the same again (Enter after the pause already started it): let it finish
    const my = ++seq;
    if (inflight) inflight.abort();
    const ctl = inflight = new AbortController();
    inflightKey = qs + "&" + sq;
    busyStart(auto);
    // The list and the counts come in parts, one as each node answers (a far node's part a little later): each part is the whole
    // answer so far and is drawn as it comes; the header says which nodes are still out.
    try {
      await Promise.all([
        apiStream("/api/logs/search?" + qs, ctl.signal, (rep) => {
          if (my !== seq) return;
          res = rep.data; msgs = res.messages || [];
          fresh(msgBox);
          if (rep.done && !!rep.follow !== following) { following = !!rep.follow; followAt = 0; }   // Configure ▸ Settings ▸ Log collector ▸ Follow new messages
          setBad("");
          drawMessages();
        }),
        apiStream("/api/logs/stats?" + sq, ctl.signal, (st) => {
          if (my !== seq) return;
          stats = st.data;
          fresh(tiles, chartBox, hostBox);
          drawTiles(); drawChart(); drawTops(); drawMessages();   // the header's "waiting for" covers both answers
        }),
        api("GET", "/api/logs/senders", undefined, false, ctl.signal).then((sn) => {
          if (my !== seq) return;
          senders = (sn.data && sn.data.senders) || [];
          if (stats) drawTiles();
        })]);
      if (my !== seq) return;
      await loadValues(my, ctl.signal);
      if (my !== seq) return;
      lastAt = Date.now();
    } catch (e) {
      if (e.message === "unauthenticated" || e.name === "AbortError") return;
      setBad(e.message);
    } finally { if (inflight === ctl) { inflight = null; busyEnd(ctl.stopped); } }
  }
  // Stop: drop the load that is running (the daemon stops reading); what was shown before stays, and the next change loads again
  function stop() {
    if (!inflight) return;
    inflight.stopped = true; inflight.abort();
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
  function apply() { readFilter(); kept.page = 0; drawSaved(); load(); }

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
        if (p.range) kept.range = p.range;
      }
      qIn.value = filterText();
      rangeSel = h("select", { "aria-label": "Time range", onchange: () => { kept.range = rangeSel.value; kept.page = 0; drawSaved(); load(); } }, RANGES.map(([id, text]) => h("option", { value: id }, text)));
      rangeSel.value = kept.range;
      sevSel = h("select", { "aria-label": "Severity", onchange: () => { kept.severity = sevSel.value; kept.page = 0; drawSaved(); load(); } }, SEVS.map(([id, text]) => h("option", { value: id }, text)));
      sevSel.value = kept.severity;
      // a floppy disk, drawn in the text colour
      const disk = sv("svg", { class: "qsave-ico", viewBox: "0 0 16 16", "aria-hidden": "true", focusable: "false" },
        sv("path", { d: "M2.5 1.5h9l3 3v9a1 1 0 0 1-1 1h-11a1 1 0 0 1-1-1v-11a1 1 0 0 1 1-1z" }),
        sv("path", { d: "M4.5 1.5v4h6v-4" }), sv("rect", { x: "4.5", y: "9", width: "7", height: "5.5", rx: ".5" }));
      saveBtn = h("button", { type: "button", class: "qsave", title: "Save what the page shows, under a name every user sees", "aria-label": "Save search", onclick: saveDialog }, disk);
      // a bookmark: the saved searches
      const mark = sv("svg", { class: "qsave-ico qmark", viewBox: "0 0 16 16", "aria-hidden": "true", focusable: "false" }, sv("path", { d: "M4 2.5h8v11.5l-4-3-4 3z" }));
      savedBtn = h("button", { type: "button", class: "qsave", "aria-label": "Saved searches", "aria-haspopup": "menu", "aria-expanded": "false",
        onclick: () => { if (savedMenu) closeSavedMenu(true); else openSavedMenu(); } }, mark);
      tiles = h("div", { class: "qtiles" }); chartBox = h("div", {}); msgBox = h("div", {}); hostBox = h("div", { class: "logtops" }); valBox = h("div", {});
      if (window.ResizeObserver) { let tm = 0; new ResizeObserver(() => { clearTimeout(tm); tm = setTimeout(() => { if (stats && Math.abs(chartBox.clientWidth - lastW) > 24) drawChart(); }, 150); }).observe(chartBox); }
      busyText = h("span", {});
      busyEl = h("span", { class: "qbusy", role: "status", hidden: true }, h("span", { class: "qspin", "aria-hidden": "true" }), busyText,
        h("button", { type: "button", class: "qstop", title: "Stop this search; what was shown before stays", "aria-label": "Stop search", onclick: stop }, "✕"));
      const bar = h("div", { class: "toolbar qbar qlogbar" }, rangeSel, sevSel, busyEl);
      main.append(bar, tiles, h("div", { class: "hosttables flowwide" }, chartBox, msgBox, hostBox, valBox));
      drawSaved();
      drawMessages();   // so the filter box is there even when the first report fails
      load();
      loadSaved();
    },
    async poll() {
      if (following) { if (Date.now() - followAt >= FOLLOW_MS) await followStep(); return; }
      if (Date.now() - lastAt >= REFRESH_MS && document.activeElement !== qIn && !inflight) await load(true);   // never on top of one still running
    },
    leave() { if (inflight) inflight.abort(); inflight = null; busyEnd(); closeSavedMenu(false); },
  };
})();
