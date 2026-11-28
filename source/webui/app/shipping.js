"use strict";
// LogDirector web GUI: the shipping page.

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
