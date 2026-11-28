"use strict";
// LogDirector web GUI: the topology page.

const DOT_DEFAULT = 36;   // Topology: a gateway with more senders than settings.topology_squares_above draws each as a small square
const EXP_SEEN = 900, EXP_QUIET = 300, EXP_LATE = 600;   // Topology: an sender is drawn while its last message is at most EXP_SEEN seconds old: green, amber after EXP_QUIET, red after EXP_LATE

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
  // the last virtual-MAC test of a gateway on this node, for its tooltip (a "not delivered" is already in the status line)
const vmacLine = (v) => "Virtual MACs: " + ({ delivered: "delivered", "not-delivered": "NOT delivered", inconclusive: "test inconclusive" }[v.verdict] || v.verdict) +
  (v.at ? " (tested " + spanText(Date.now() / 1000 - v.at) + " ago)" : "") + (v.verdict === "delivered" ? "" : " — " + v.detail);
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
