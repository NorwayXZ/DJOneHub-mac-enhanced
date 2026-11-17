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
      calls.host = s.host || null;
      calls.trayAudio = calls.active && calls.active.state === "active" ? await api("api/desktop/audio", { unscoped: true, timeout: 3 }).catch(() => null) : null;
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

  // Where the call's sound is: the tray app plays it through this PC.
  function audioLine(c) {
    if (c.state !== "active") return "";
    const t = calls.trayAudio;
    if (t && t.running) return `<div class="small green">${icon("wave")} 声音走这台电脑的麦克风和扬声器${recordingNote()}</div>`;
    if (t && t.error) return `<div class="small orange">${esc(t.error)}</div>`;
    if (!calls.host || !calls.host.enabled) return '<div class="small muted">托盘里的 DJOneHub 没有运行，声音不经过电脑。双击桌面上的 DJOneHub 即可。</div>';
    return `<div class="small muted">${t && t.detail ? esc(t.detail) : "正在接通电脑音频…"}</div>`;
  }
  function recordingNote() { return calls.host && calls.host.recording ? " · 正在录音" : ""; }

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
    const muted = !!(calls.host && calls.host.muted), recording = !!(calls.host && calls.host.recording);
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
      ${audioLine(c)}
      <div style="flex:1"></div>
      ${!ringing && keypad ? `<div class="col" style="gap:10px">${["123", "456", "789", "*0#"].map((r) => `<div class="row" style="gap:26px">${[...r].map((ch) => `<button data-dtmf="${ch}" style="width:58px;height:58px;border-radius:50%;border:0;background:rgba(128,128,128,0.12);font-size:22px;cursor:pointer">${ch}</button>`).join("")}</div>`).join("")}</div>` : ""}
      <div class="row" style="gap:${ringing ? 44 : 22}px;margin-bottom:20px">${ringing ? round("reject", "phoneDown", "拒接", "var(--red)", true) + round("answer", "phone", "接听", "var(--green)", true)
        : round("mute", muted ? "micOff" : "mic", muted ? "取消静音" : "静音", muted ? "var(--orange)" : "var(--text)") + round("record", "record", recording ? "停止录音" : "录音", recording ? "var(--red)" : "var(--text)") + round("hangup", "phoneDown", "挂断", "var(--red)", true) + round("keypad", "grid", "键盘", keypad ? "var(--green)" : "var(--text)")}</div>`);
    const on = (a, fn) => { const b = overlay.querySelector(`[data-a="${a}"]`); if (b) b.onclick = fn; };
    const post = (path, body) => api(path, { body: body || {}, timeout: 10 }).catch((e) => toast(e.message, "failure")).then(() => setTimeout(pollCalls, 300));
    on("reject", () => post("api/calls/reject"));
    on("answer", () => post("api/calls/answer"));
    on("hangup", () => post("api/calls/hangup"));
    on("mute", () => post("api/calls/audio/mute", { muted: !muted }));
    on("record", () => post("api/calls/audio/record", { action: recording ? "stop" : "start" }));
    on("keypad", () => { keypad = !keypad; overlay._html = null; drawOverlay(); });
    overlay.querySelectorAll("[data-dtmf]").forEach((b) => { b.onclick = () => api("api/calls/dtmf", { body: { digit: b.dataset.dtmf } }).catch(() => {}); });
  }
  setInterval(() => { if (calls.active) drawOverlay(); }, 1000);

  // ---- 国家/地区区号 ----------------------------------------------------------
  // Pick the country, type the number as people there write it; the leading
  // trunk 0 is dropped and the country code added, so "030 1234567" with
  // Germany dials +49301234567.
  const COUNTRIES = [
    ["CN", "中国", "86"], ["HK", "中国香港", "852"], ["MO", "中国澳门", "853"], ["TW", "中国台湾", "886"],
    ["US", "美国", "1"], ["CA", "加拿大", "1"], ["GB", "英国", "44"], ["DE", "德国", "49"], ["FR", "法国", "33"],
    ["IT", "意大利", "39"], ["ES", "西班牙", "34"], ["NL", "荷兰", "31"], ["BE", "比利时", "32"], ["CH", "瑞士", "41"],
    ["AT", "奥地利", "43"], ["IE", "爱尔兰", "353"], ["PT", "葡萄牙", "351"], ["SE", "瑞典", "46"], ["NO", "挪威", "47"],
    ["DK", "丹麦", "45"], ["FI", "芬兰", "358"], ["PL", "波兰", "48"], ["CZ", "捷克", "420"], ["GR", "希腊", "30"],
    ["HU", "匈牙利", "36"], ["RO", "罗马尼亚", "40"], ["TR", "土耳其", "90"], ["RU", "俄罗斯", "7"], ["UA", "乌克兰", "380"],
    ["JP", "日本", "81"], ["KR", "韩国", "82"], ["SG", "新加坡", "65"], ["MY", "马来西亚", "60"], ["TH", "泰国", "66"],
    ["VN", "越南", "84"], ["PH", "菲律宾", "63"], ["ID", "印度尼西亚", "62"], ["KH", "柬埔寨", "855"], ["MM", "缅甸", "95"],
    ["LA", "老挝", "856"], ["MN", "蒙古", "976"], ["IN", "印度", "91"], ["PK", "巴基斯坦", "92"], ["BD", "孟加拉国", "880"],
    ["NP", "尼泊尔", "977"], ["LK", "斯里兰卡", "94"], ["AE", "阿联酋", "971"], ["SA", "沙特阿拉伯", "966"], ["QA", "卡塔尔", "974"],
    ["KW", "科威特", "965"], ["IL", "以色列", "972"], ["KZ", "哈萨克斯坦", "7"], ["AU", "澳大利亚", "61"], ["NZ", "新西兰", "64"],
    ["BR", "巴西", "55"], ["MX", "墨西哥", "52"], ["AR", "阿根廷", "54"], ["CL", "智利", "56"], ["CO", "哥伦比亚", "57"],
    ["PE", "秘鲁", "51"], ["ZA", "南非", "27"], ["EG", "埃及", "20"], ["NG", "尼日利亚", "234"], ["KE", "肯尼亚", "254"], ["MA", "摩洛哥", "212"],
  ];
  const flag = (iso) => iso.replace(/./g, (c) => String.fromCodePoint(127397 + c.charCodeAt(0)));
  const COUNTRY_KEY = "djonehub.dial.country";
  let dialCountry = "CN";
  try { const v = localStorage.getItem(COUNTRY_KEY); if (v !== null) dialCountry = v; } catch (_) {}
  const countryOf = (iso) => COUNTRIES.find((c) => c[0] === iso) || null;
  // intlNumber is what the module dials for what was typed.
  function intlNumber(raw, iso) {
    const typed = String(raw || "").trim().replace(/[\s\-().]/g, "");
    const c = countryOf(iso);
    if (!c || !typed || /[*#]/.test(typed) || typed.startsWith("+")) return typed;
    if (typed.startsWith("00")) return "+" + typed.slice(2);
    if (typed.replace(/\D/g, "").length <= 5) return typed; // 110, 10086, 112: local short numbers
    let national = typed.replace(/\D/g, "");
    if (c[2] === "7" && /^8\d{10}$/.test(national)) national = national.slice(1);
    else if (c[2] !== "39") national = national.replace(/^0+/, "");
    return "+" + c[2] + national;
  }
  App.intlNumber = intlNumber;
  // The SMS composer shares the chosen country.
  App.dialCountry = () => dialCountry;
  App.dialCountryLabel = () => { const c = countryOf(dialCountry); return c ? flag(c[0]) + " +" + c[2] : "不加国家码"; };
  App.pickCountry = (done) => pickCountry(done);
  function pickCountry(done) {
    const m = App.modal(`<h4>拨打哪个国家/地区</h4><div class="col" style="gap:8px;margin-top:8px">
      <input class="field" data-q placeholder="搜索国家或区号">
      <div data-list class="col" style="max-height:360px;overflow:auto;gap:2px"></div></div>
      <div class="actions"><button class="btn" data-a="no">取消</button></div>`);
    const list = m.el.querySelector("[data-list]"), q = m.el.querySelector("[data-q]");
    const row = (iso, name, code) => `<button class="btn link" data-iso="${iso}" style="justify-content:flex-start;gap:10px;padding:8px 10px;${iso === dialCountry ? "background:var(--card-2);" : ""}"><span style="font-size:18px">${iso ? flag(iso) : "☎️"}</span><span class="grow" style="text-align:left;color:var(--text)">${esc(name)}</span><span class="muted mono">${code ? "+" + code : ""}</span></button>`;
    const draw = () => {
      const t = q.value.trim().replace(/^\+/, "");
      const hits = COUNTRIES.filter(([iso, name, code]) => !t || name.includes(t) || code.startsWith(t) || iso.toLowerCase() === t.toLowerCase());
      list.innerHTML = (!t ? row("", "不加国家码（号码原样拨出）", "") : "") + hits.map((c) => row(...c)).join("");
      list.querySelectorAll("[data-iso]").forEach((b) => { b.onclick = () => { dialCountry = b.dataset.iso; try { localStorage.setItem(COUNTRY_KEY, dialCountry); } catch (_) {} m.close(); done(); }; });
    };
    q.oninput = draw;
    m.el.querySelector('[data-a="no"]').onclick = m.close;
    draw(); setTimeout(() => q.focus(), 50);
  }

  // ---- 拨号 ------------------------------------------------------------------
  const KEYS = [["1", ""], ["2", "ABC"], ["3", "DEF"], ["4", "GHI"], ["5", "JKL"], ["6", "MNO"], ["7", "PQRS"], ["8", "TUV"], ["9", "WXYZ"], ["*", ""], ["0", "+"], ["#", ""]];
  App.pages.dial = function (host) {
    if (App.pendingDial) { calls.number = App.pendingDial; App.pendingDial = null; }
    host.innerHTML = `<div class="col" style="align-items:center;justify-content:center;min-height:100vh;gap:16px;padding:20px">
      <button class="btn" data-a="country" style="border-radius:20px;padding:6px 14px;gap:8px"></button>
      <div data-num style="font:300 34px var(--round);min-height:46px;max-width:100%;white-space:nowrap;overflow:hidden;text-overflow:ellipsis"></div>
      <div data-match class="accent small" style="min-height:18px;cursor:pointer"></div>
      <div data-intl class="small muted mono" style="min-height:18px;margin-top:-12px"></div>
      <div style="display:grid;grid-template-columns:repeat(3,76px);gap:12px 22px">${KEYS.map(([d, l]) => `<button data-k="${d}" style="width:76px;height:76px;border-radius:50%;border:0;background:var(--card-2);box-shadow:0 2px 6px rgba(0,0,0,0.12);cursor:pointer;display:flex;flex-direction:column;align-items:center;justify-content:center"><span style="font:400 27px var(--round)">${d}</span>${l ? `<span class="tiny muted" style="letter-spacing:1.5px">${l}</span>` : ""}</button>`).join("")}
        <span></span><button data-a="call" style="width:76px;height:76px;border-radius:50%;border:0;background:var(--green);color:#fff;cursor:pointer;display:flex;align-items:center;justify-content:center">${icon("phone")}</button>
        <button data-a="del" style="width:76px;height:76px;border:0;background:none;cursor:pointer;font-size:22px;color:var(--text-2)">⌫</button></div>
      <div data-err class="small red" style="text-align:center;max-width:420px"></div></div>`;
    const num = host.querySelector("[data-num]"), match = host.querySelector("[data-match]"), err = host.querySelector("[data-err]"), callBtn = host.querySelector('[data-a="call"]'), countryBtn = host.querySelector('[data-a="country"]'), intl = host.querySelector("[data-intl]");
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
      const c2 = countryOf(dialCountry);
      countryBtn.innerHTML = c2 ? `<span style="font-size:16px">${flag(c2[0])}</span>${esc(c2[1])} +${c2[2]} ▾` : "不加国家码 ▾";
      const target = intlNumber(calls.number, dialCountry);
      intl.textContent = target && target !== calls.number.trim() ? "将拨打 " + target : "";
    }
    countryBtn.onclick = () => pickCountry(draw);
    host.querySelectorAll("[data-k]").forEach((b) => { b.onclick = () => { calls.number += b.dataset.k; draw(); }; });
    host.querySelector('[data-a="del"]').onclick = () => { calls.number = calls.number.slice(0, -1); draw(); };
    callBtn.onclick = () => dial(intlNumber(calls.number, dialCountry));
    const onKey = (e) => {
      if (e.ctrlKey || e.altKey || e.metaKey || document.getElementById("modal-layer").innerHTML) return;
      if (/^[0-9*#+]$/.test(e.key)) { calls.number += e.key; draw(); }
      else if (e.key === "Backspace") { calls.number = calls.number.slice(0, -1); draw(); }
      else if (e.key === "Enter") dial(intlNumber(calls.number, dialCountry));
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
