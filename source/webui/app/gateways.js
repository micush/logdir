"use strict";
// LogDirector web GUI: the gateways page.

// The virtual-MAC test of one gateway on the node picked in the Node menu (CLI: --test-vmac --group N).  It takes a
// few seconds: the node asks the router from a throwaway virtual-MAC interface and waits for the answer.
const VMAC_WORDS = { delivered: ["Delivered", "ok"], "not-delivered": ["NOT delivered", "bad"], inconclusive: ["Inconclusive", "warn"], off: ["Real MACs in use", ""] };
function vmacTestPopup(gid, label) {
const out = h("div", {}, h("p", { class: "muted" }, "Testing…"));
dialog("Virtual MAC test · " + label, (close) => [out,
  h("div", { class: "toolbar" }, h("button", { class: "btn", type: "button", onclick: close }, "Close"))]);
(async () => {
  try {
    const r = ((await api("POST", "/api/vmactest", { group: gid })).data || [])[0];
    if (!r) { fill(out, h("p", { class: "bad-text" }, "No result.")); return; }
    const [word, kind] = VMAC_WORDS[r.verdict] || [r.verdict, ""];
    fill(out, kv([["Result", pill(word, kind)], ...(r.families || []).map((f) => [f.af === "v6" ? "IPv6" : "IPv4", f.detail])]),
      r.families && r.families.length ? null : h("p", {}, r.detail),
      r.verdict === "not-delivered" ? h("p", { class: "hint" }, "Turn on real MAC addresses for this gateway (Settings).") : null);
  } catch (e) { fill(out, h("p", { class: "bad-text" }, e.message)); }
})();
}

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
