"use strict";
// LogDirector web GUI: the senders page.

// ── Node, Monitor  (CLI: --host) ─────────────────────────────────────────────
// CPU, memory, disk and network use of the machine, sampled every 10 s by the daemon and
// kept in memory for 30 days.  The picked node (top-bar Node menu) is the one shown.
const senderPick = { sel: null };   // the sender the Senders page's Top senders chart is narrowed to (null: all)

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
