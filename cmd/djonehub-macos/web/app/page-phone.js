/* 拨号, 最近通话 and the in-call screen (DialPadView, RecentsView, CallActiveView). */
(function () {
  "use strict";
  const { icon, esc, api, toast, fmt } = App;

  // ---- call state, polled app-wide like CallCenter ---------------------------
  const calls = App.calls = { active: null, history: [], number: "", dialing: false, error: "", online: true, listeners: new Set() };
  async function pollCalls() {
    try {
      const s = await api("api/calls/status", { timeout: 4 });
      calls.active = s.active || null;
      calls.history = s.history || [];
      calls.online = true;
    } catch (e) { calls.online = false; calls.error = e.message; }
    drawOverlay();
    calls.listeners.forEach((fn) => fn());
  }
  setInterval(pollCalls, 2000);
  setTimeout(pollCalls, 300);

  async function dial(number) {
    number = String(number || calls.number).trim();
    if (!number || calls.dialing) return;
    calls.dialing = true; calls.error = "";
    try { await api("api/calls/dial", { body: { number }, timeout: 15 }); calls.number = ""; setTimeout(pollCalls, 400); }
    catch (e) { calls.error = e.message; toast("拨号失败：" + e.message, "failure"); }
    calls.dialing = false;
    calls.listeners.forEach((fn) => fn());
  }
  App.dial = dial;

  // ---- in-call overlay ------------------------------------------------------
  let overlay = null, keypad = false;
  const STATE = { incoming: "来电…", waiting: "等待接听…", active: "通话中", dialing: "正在拨号…", alerting: "等待对方接听…", held: "通话保持" };
  function drawOverlay() {
    const c = calls.active;
    if (!c) { if (overlay) { overlay.remove(); overlay = null; keypad = false; } return; }
    if (!overlay) {
      overlay = document.createElement("div");
      overlay.style.cssText = "position:fixed;left:185px;right:0;top:0;bottom:0;background:var(--bg);z-index:40;display:flex;flex-direction:column;align-items:center;gap:18px;padding:40px 20px;animation:rise .22s ease-out";
      document.body.appendChild(overlay);
    }
    const ringing = c.state === "incoming" || c.state === "waiting";
    const inCall = ["active", "dialing", "alerting", "held"].includes(c.state);
    const start = fmt.parseDate(c.started_at);
    let dur = "";
    if (inCall && start) { const s = Math.max(0, Math.floor((Date.now() - start) / 1000)); const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), x = s % 60; dur = (h ? h + ":" : "") + String(m).padStart(2, "0") + ":" + String(x).padStart(2, "0"); }
    const round = (a, ic, label, color, filled) => `<button data-a="${a}" style="border:0;background:none;cursor:pointer;display:flex;flex-direction:column;align-items:center;gap:6px;color:var(--text)"><span style="width:64px;height:64px;border-radius:50%;display:flex;align-items:center;justify-content:center;background:${filled ? color : "rgba(128,128,128,0.14)"};color:${filled ? "#fff" : color}">${icon(ic)}</span><span class="small">${label}</span></button>`;
    App.setHTML(overlay, `<div style="flex:1"></div>
      <span style="width:96px;height:96px;border-radius:50%;background:rgba(128,128,128,0.12);display:flex;align-items:center;justify-content:center">${App.avatar(App.contacts.name(c.number), 96)}</span>
      <div style="font:600 28px var(--round)">${esc(App.contacts.name(c.number))}</div>
      <div class="muted">${esc(STATE[c.state] || "通话状态")}${c.module_name ? " · " + esc(c.module_name) : ""}</div>
      ${dur ? `<div class="mono" style="font-size:20px">${dur}</div>` : ""}
      <div class="small muted">Windows 版暂不支持通话声音：可以拨打、接听和挂断，声音不经过电脑。</div>
      <div style="flex:1"></div>
      ${!ringing && keypad ? `<div class="col" style="gap:10px">${["123", "456", "789", "*0#"].map((r) => `<div class="row" style="gap:26px">${[...r].map((ch) => `<button data-dtmf="${ch}" style="width:58px;height:58px;border-radius:50%;border:0;background:rgba(128,128,128,0.12);font-size:22px;cursor:pointer">${ch}</button>`).join("")}</div>`).join("")}</div>` : ""}
      <div class="row" style="gap:${ringing ? 44 : 22}px;margin-bottom:20px">${ringing ? round("reject", "phoneDown", "拒接", "var(--red)", true) + round("answer", "phone", "接听", "var(--green)", true)
        : round("hangup", "phoneDown", "挂断", "var(--red)", true) + round("keypad", "grid", "键盘", keypad ? "var(--green)" : "var(--text)")}</div>`);
    const on = (a, fn) => { const b = overlay.querySelector(`[data-a="${a}"]`); if (b) b.onclick = fn; };
    const post = (path, body) => api(path, { body: body || {}, timeout: 10 }).catch((e) => toast(e.message, "failure")).then(() => setTimeout(pollCalls, 300));
    on("reject", () => post("api/calls/reject"));
    on("answer", () => post("api/calls/answer"));
    on("hangup", () => post("api/calls/hangup"));
    on("keypad", () => { keypad = !keypad; overlay._html = null; drawOverlay(); });
    overlay.querySelectorAll("[data-dtmf]").forEach((b) => { b.onclick = () => api("api/calls/dtmf", { body: { digit: b.dataset.dtmf } }).catch(() => {}); });
  }
  setInterval(() => { if (calls.active) drawOverlay(); }, 1000);

  // ---- 拨号 ------------------------------------------------------------------
  const KEYS = [["1", ""], ["2", "ABC"], ["3", "DEF"], ["4", "GHI"], ["5", "JKL"], ["6", "MNO"], ["7", "PQRS"], ["8", "TUV"], ["9", "WXYZ"], ["*", ""], ["0", "+"], ["#", ""]];
  App.pages.dial = function (host) {
    if (App.pendingDial) { calls.number = App.pendingDial; App.pendingDial = null; }
    host.innerHTML = `<div class="col" style="align-items:center;justify-content:center;min-height:100vh;gap:16px;padding:20px">
      <div data-num style="font:300 34px var(--round);min-height:46px;max-width:100%;white-space:nowrap;overflow:hidden;text-overflow:ellipsis"></div>
      <div data-match class="accent small" style="min-height:18px;cursor:pointer"></div>
      <div style="display:grid;grid-template-columns:repeat(3,76px);gap:12px 22px">${KEYS.map(([d, l]) => `<button data-k="${d}" style="width:76px;height:76px;border-radius:50%;border:0;background:var(--card-2);box-shadow:0 2px 6px rgba(0,0,0,0.12);cursor:pointer;display:flex;flex-direction:column;align-items:center;justify-content:center"><span style="font:400 27px var(--round)">${d}</span>${l ? `<span class="tiny muted" style="letter-spacing:1.5px">${l}</span>` : ""}</button>`).join("")}
        <span></span><button data-a="call" style="width:76px;height:76px;border-radius:50%;border:0;background:var(--green);color:#fff;cursor:pointer;display:flex;align-items:center;justify-content:center">${icon("phone")}</button>
        <button data-a="del" style="width:76px;height:76px;border:0;background:none;cursor:pointer;font-size:22px;color:var(--text-2)">⌫</button></div>
      <div data-err class="small red" style="text-align:center;max-width:420px"></div></div>`;
    const num = host.querySelector("[data-num]"), match = host.querySelector("[data-match]"), err = host.querySelector("[data-err]"), callBtn = host.querySelector('[data-a="call"]');
    function draw() {
      num.textContent = calls.number || "输入号码";
      num.style.color = calls.number ? "var(--text)" : "var(--text-3)";
      const c = calls.number ? App.contacts.find(calls.number) : null;
      let sugg = [];
      if (!c && calls.number.length >= 2) sugg = App.contacts.list.filter((x) => (x.phones || []).some((p) => App.contacts.digits(p).startsWith(App.contacts.digits(calls.number)))).slice(0, 3);
      match.textContent = c ? c.name : sugg.map((x) => x.name).join("、");
      match.onclick = () => { if (!c && sugg[0]) { calls.number = (sugg[0].phones || [])[0] || ""; draw(); } };
      callBtn.disabled = !calls.number.trim() || calls.dialing;
      callBtn.style.opacity = calls.number.trim() ? 1 : 0.5;
      err.textContent = calls.error || (!calls.online ? "无法连接 DJOneHub 后台" : "");
    }
    host.querySelectorAll("[data-k]").forEach((b) => { b.onclick = () => { calls.number += b.dataset.k; draw(); }; });
    host.querySelector('[data-a="del"]').onclick = () => { calls.number = calls.number.slice(0, -1); draw(); };
    callBtn.onclick = () => dial();
    const onKey = (e) => {
      if (e.ctrlKey || e.altKey || e.metaKey || document.getElementById("modal-layer").innerHTML) return;
      if (/^[0-9*#+]$/.test(e.key)) { calls.number += e.key; draw(); }
      else if (e.key === "Backspace") { calls.number = calls.number.slice(0, -1); draw(); }
      else if (e.key === "Enter") dial();
    };
    document.addEventListener("keydown", onKey);
    calls.listeners.add(draw);
    draw();
    return { destroy() { document.removeEventListener("keydown", onKey); calls.listeners.delete(draw); } };
  };

  // ---- 最近通话 ---------------------------------------------------------------
  function dateText(d) {
    if (!d) return "";
    const now = new Date(), p = (n) => String(n).padStart(2, "0");
    if (d.toDateString() === now.toDateString()) return p(d.getHours()) + ":" + p(d.getMinutes());
    const y = new Date(now); y.setDate(now.getDate() - 1);
    if (d.toDateString() === y.toDateString()) return "昨天 " + p(d.getHours()) + ":" + p(d.getMinutes());
    return (d.getMonth() + 1) + "月" + d.getDate() + "日 " + p(d.getHours()) + ":" + p(d.getMinutes());
  }
  App.pages.recents = function (host) {
    function draw() {
      const list = calls.history;
      if (!list.length) { App.setHTML(host, `<div class="col muted" style="align-items:center;justify-content:center;min-height:100vh;gap:8px">${icon("phone", "faint")}<div>暂无通话记录</div></div>`); return; }
      if (!App.setHTML(host, `<div style="padding:12px;display:flex;flex-direction:column;gap:8px">${list.map((c) => {
        const start = fmt.parseDate(c.started_at), end = fmt.parseDate(c.ended_at);
        const [ic, color, label] = c.missed ? ["phoneDown", "red", "未接来电"] : c.direction === "outgoing" ? ["phone", "green", "已拨出"] : ["phone", "blue", "已接听"];
        let len = "";
        if (end && start) { const s = Math.max(0, Math.round((end - start) / 1000)); len = s >= 60 ? Math.floor(s / 60) + ":" + String(s % 60).padStart(2, "0") : s + " 秒"; }
        return `<div class="card row" style="border:1px solid var(--line);padding:10px 12px;gap:12px;box-shadow:0 2px 8px rgba(0,0,0,0.08)">
          <span style="color:var(--${color})" title="${label}">${icon(ic)}</span>
          <span class="grow col" style="gap:2px"><span class="strong ${c.missed ? "red" : ""}" style="font-size:14px">${esc(App.contacts.name(c.number))}</span><span class="small muted">${dateText(start)}${c.module_name ? " · " + esc(c.module_name) : ""}</span></span>
          <span class="small ${c.missed ? "red" : "muted"}">${c.missed ? "未接" : len}</span>
          <button class="btn small" data-n="${esc(c.number || "")}" ${c.number ? "" : "disabled"} title="拨号">${icon("phone")}</button></div>`;
      }).join("")}</div>`)) return;
      host.querySelectorAll("[data-n]").forEach((b) => { b.onclick = () => dial(b.dataset.n); });
    }
    calls.listeners.add(draw);
    App.contacts.listeners.add(draw);
    draw();
    return { destroy() { calls.listeners.delete(draw); App.contacts.listeners.delete(draw); } };
  };
})();
