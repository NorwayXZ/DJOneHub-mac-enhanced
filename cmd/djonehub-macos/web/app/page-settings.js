/* 设置 and 软件更新: SettingsView (forwarding, call forwarding, SIM keep-alive,
   device status, appearance) and SoftwareUpdateView. */
(function () {
  "use strict";
  const { icon, esc, api, toast, fmt } = App;
  const section = (title) => `<div class="strong muted" style="font-size:14px;margin-top:6px">${esc(title)}</div>`;
  const cardWrap = (inner) => `<div class="card" style="padding:12px 14px;border:1px solid var(--line);box-shadow:0 2px 8px rgba(0,0,0,0.08)">${inner}</div>`;
  const REPO = "NorwayXZ/DJOneHub-mac-enhanced";

  // ---- 短信转发 ---------------------------------------------------------------
  function forwardPanel(el) {
    let s = null, editing = null, busy = null, message = null;
    const f = { token: "", chat: "", tapi: "", service: "pushplus", key: "" };
    function channel(id, title, configured, summary, fields) {
      return `<div class="col" style="gap:8px"><div class="row" style="gap:8px">${icon(configured ? "check" : "ring", configured ? "green" : "muted")}<span class="strong">${title}</span>
        ${configured && editing !== id ? `<span class="small mono muted" style="white-space:nowrap;overflow:hidden;text-overflow:ellipsis">${esc(summary)}</span>` : ""}<span class="grow"></span>
        ${busy === id ? '<div class="spinner"></div>' : ""}
        ${editing === id ? '<button class="btn small" data-a="cancel">取消</button>' : configured ? `<button class="btn small" data-test="${id}" ${busy ? "disabled" : ""}>发测试</button><button class="btn small" data-edit="${id}">修改</button><button class="btn small danger" data-off="${id}" ${busy ? "disabled" : ""}>关闭</button>` : `<button class="btn small" data-edit="${id}">设置</button>`}</div>
        ${editing === id ? `<div class="col" style="gap:8px">${fields}<div class="row"><span class="grow"></span><button class="btn primary small" data-save="${id}">保存并发测试消息</button></div></div>` : ""}</div>`;
    }
    function draw() {
      const tg = s && s.telegram_configured, wx = s && s.wechat_configured;
      el.innerHTML = `<div class="col" style="gap:12px"><div class="small muted">模块收到新短信后推送到你的手机，验证码放在标题最前面。两个渠道可以同时开。</div>
        ${channel("telegram", "Telegram", tg, [s && s.telegram_token_hint, s && s.telegram_chat_id && "Chat ID " + s.telegram_chat_id].filter(Boolean).join(" · "),
          `<input class="field" type="password" data-f="token" placeholder="机器人 Token（在 @BotFather 创建机器人后得到）"><input class="field" data-f="chat" placeholder="Chat ID（给 @userinfobot 发消息可查到）" value="${esc(f.chat)}"><input class="field" data-f="tapi" placeholder="接口地址（可不填；连不上 Telegram 时填反代地址）" value="${esc(f.tapi)}"><div class="tiny muted">先在 Telegram 里给你的机器人发一句话，它才能给你发消息。宽带连不上 Telegram 时会自动改走 4G。</div>`)}
        ${channel("wechat", "微信", wx, [s && s.wechat_service === "serverchan" ? "Server酱" : "PushPlus", s && s.wechat_key_hint].filter(Boolean).join(" · "),
          `<label class="row small" style="gap:6px"><input type="radio" name="svc" value="pushplus" ${f.service === "pushplus" ? "checked" : ""}>PushPlus（pushplus.plus）</label><label class="row small" style="gap:6px"><input type="radio" name="svc" value="serverchan" ${f.service === "serverchan" ? "checked" : ""}>Server酱（sct.ftqq.com）</label><input class="field" type="password" data-f="key" placeholder="${f.service === "serverchan" ? "SendKey" : "Token"}"><div class="tiny muted">在对应网站用微信扫码登录、关注公众号后复制 Token / SendKey，短信会由该公众号推送到微信。</div>`)}
        ${tg ? `<div class="small muted">${icon("cycle")} Telegram 可双向遥控：直接回复推送来的短信，就从收到它的卡回短信；发 /ip 看出口 IP，/rotate 换 IP，/clean 换干净 IP，/send 号码 内容 发短信，/esim 看和切换 eSIM 号码，/help 看全部。只听你这个 Chat ID 的指令。</div>` : ""}
        ${message ? `<div class="small ${message[1] ? "green" : "orange"}">${esc(message[0])}</div>` : ""}</div>`;
      el.querySelectorAll("[data-f]").forEach((i) => { i.oninput = () => { f[i.dataset.f] = i.value; }; });
      el.querySelectorAll('input[name="svc"]').forEach((r) => { r.onchange = () => { f.service = r.value; draw(); }; });
      el.querySelectorAll("[data-edit]").forEach((b) => { b.onclick = () => { f.token = ""; f.key = ""; f.chat = (s && s.telegram_chat_id) || ""; f.tapi = (s && s.telegram_api) || ""; f.service = (s && s.wechat_service) || "pushplus"; message = null; editing = b.dataset.edit; draw(); }; });
      const c = el.querySelector('[data-a="cancel"]'); if (c) c.onclick = () => { editing = null; draw(); };
      el.querySelectorAll("[data-test]").forEach((b) => { b.onclick = () => run(b.dataset.test, { channel: b.dataset.test }, true); });
      el.querySelectorAll("[data-off]").forEach((b) => { b.onclick = () => run(b.dataset.off, { channel: b.dataset.off }); });
      el.querySelectorAll("[data-save]").forEach((b) => { b.onclick = () => run(b.dataset.save, b.dataset.save === "telegram" ? { channel: "telegram", telegram_token: f.token, telegram_chat_id: f.chat, telegram_api: f.tapi } : { channel: "wechat", wechat_service: f.service, wechat_key: f.key }); });
    }
    async function run(id, body, test) {
      busy = id; draw();
      try {
        s = await api("api/forwarding/sms", { body: Object.assign({ test: !!test }, body), timeout: 40, unscoped: true });
        const on = id === "telegram" ? s.telegram_configured : s.wechat_configured;
        message = [test ? "测试消息已发出，请看手机" : on ? "已保存，测试消息已发出，请看手机" : "已关闭", true];
        editing = null;
      } catch (e) { message = [e.message, false]; }
      busy = null; draw();
    }
    draw();
    api("api/forwarding/sms", { unscoped: true }).then((r) => { s = r; draw(); }).catch(() => {});
  }

  // ---- 呼叫转移 ---------------------------------------------------------------
  const REASONS = { 0: "全部来电", 1: "占线时", 2: "无人接听时", 3: "关机或无信号时" };
  function callForwardPanel(el) {
    let status = null, loaded = false, busy = false, message = "", number = "", mode = "all";
    function draw() {
      const sel = App.selected;
      el.innerHTML = `<div class="col" style="gap:10px">${App.fleetMode && sel ? `<div class="small strong">${icon("sim")} 当前设置：${esc(sel.name)} 里的卡（左上角切换模块）</div>` : ""}
        <div class="small muted">在运营商网络里设置转移：来电直接打到你填的手机号，电脑关机或模块离线也照样转。费用按这张卡的运营商规则计算，漫游时转到国内号码可能按国际长途收费。</div>
        ${status ? `<div class="col" style="gap:4px">${status.saved ? `<div class="small strong">${icon("phone")} 已开启：${status.saved.mode === "all" ? "全部来电" : "没接 / 占线 / 关机时"} 转到 ${esc(status.saved.number)}</div>` : ""}
          ${(status.entries || []).map((e) => `<div class="row small" style="gap:6px"><span class="muted" style="width:96px">${REASONS[e.reason] || ""}</span><span>${e.active ? "转到 " + esc(e.number || "") + (e.reason === 2 && e.time > 0 ? `（响 ${e.time} 秒后）` : "") : "未转移"}</span></div>`).join("")}
          ${status.error ? `<div class="small orange">${esc(status.error)}</div>` : ""}</div>` : loaded ? '<div class="small orange">读取失败，点“刷新”重试</div>' : '<div class="row small muted" style="gap:6px"><div class="spinner"></div>正在向运营商查询当前设置…</div>'}
        <div class="row" style="gap:8px"><input class="field" data-num placeholder="转到的号码，例如 +8613800138000" style="width:240px" value="${esc(number)}"><select class="field" data-mode><option value="all" ${mode === "all" ? "selected" : ""}>全部来电都转</option><option value="conditional" ${mode === "conditional" ? "selected" : ""}>没接 / 占线 / 关机时才转</option></select><button class="btn" data-a="set" ${busy ? "disabled" : ""}>开启转移</button></div>
        <div class="row" style="gap:8px">${status && status.saved ? `<button class="btn small danger" data-a="clear" ${busy ? "disabled" : ""}>关闭转移，恢复原设置</button>` : ""}<button class="btn small" data-a="reload" ${busy ? "disabled" : ""}>刷新</button>${busy ? '<div class="spinner"></div><span class="small muted">正在和运营商通信，约 10–60 秒…</span>' : ""}</div>
        <div class="tiny muted">国内号码加 +86，例如 +8613800138000。开启前会保存这张卡原有的转移（如语音信箱），关闭时原样恢复。换卡后按新卡分别设置。</div>
        ${message ? `<div class="small orange">${esc(message)}</div>` : ""}</div>`;
      el.querySelector("[data-num]").oninput = (e) => { number = e.target.value; };
      el.querySelector("[data-mode]").onchange = (e) => { mode = e.target.value; };
      el.querySelector('[data-a="set"]').onclick = () => { if (number.trim()) act("set"); };
      const cl = el.querySelector('[data-a="clear"]'); if (cl) cl.onclick = () => act("clear");
      el.querySelector('[data-a="reload"]').onclick = load;
    }
    async function load() {
      busy = true; draw();
      try { status = await api("api/forwarding/calls", { timeout: 200 }); if (!number && status.saved) { number = status.saved.number; mode = status.saved.mode; } message = ""; }
      catch (e) { message = e.message; }
      busy = false; loaded = true; draw();
    }
    async function act(action) {
      busy = true; draw();
      try { status = await api("api/forwarding/calls", { body: { action, mode, number: number.trim() }, timeout: 400 }); message = status.error || ""; toast(action === "set" ? "呼叫转移已开启" : "已关闭转移", status.error ? "failure" : "success"); }
      catch (e) { message = e.message; }
      busy = false; draw();
    }
    load();
  }

  // ---- SIM 保号 ---------------------------------------------------------------
  function keepAlivePanel(el) {
    let status = null, busy = false, message = null;
    const form = { enabled: false, days: 90, to: "", text: "保号" };
    const short = (i) => i && i.length > 6 ? "…" + i.slice(-6) : i || "";
    const d = (v) => fmt.parseDate(v);
    const overdue = (c) => c.plan.enabled && d(c.next_due) && d(c.next_due) < new Date();
    function lastLine(c) {
      if (c.plan.last_error) return c.plan.last_error;
      const last = d(c.plan.last_done);
      if (!last) return c.plan.enabled ? "还没发过，开启后几分钟内会发第一次" : "未开启";
      let line = "上次 " + fmt.dateOnly(last);
      const next = d(c.next_due);
      if (c.plan.enabled && next) line += " · 下次 " + fmt.dateOnly(next) + (next < new Date() ? "（已到期）" : "");
      return line;
    }
    function apply(next) {
      status = next;
      const cur = (status.cards || []).find((c) => c.current);
      if (cur) { const p = cur.plan; form.enabled = p.enabled; form.days = p.every_days > 0 ? p.every_days : 90; form.to = p.to || ""; form.text = p.text || "保号"; }
    }
    function draw() {
      const cards = (status && status.cards) || [];
      const cur = cards.find((c) => c.current);
      const same = cards.filter((c) => c.on_card);
      const others = cards.filter((c) => !c.current && !c.on_card);
      el.innerHTML = `<div class="col" style="gap:10px"><div class="small muted">很多国外卡几个月不用就会被回收。开启后，插着这张卡的模块每隔一段时间自动发一条短信；手动发短信也算一次。到期时卡不在任何模块上，会通过 Telegram / 微信提醒你插卡。</div>
        ${cur ? `<div class="col" style="gap:8px"><div class="row"><span class="small strong">${icon("sim")} 当前卡 ${esc(short(cur.iccid))}${status.own_number ? " · " + esc(status.own_number) : ""}${App.fleetMode && App.selected ? " · " + esc(App.selected.name) : ""}</span><span class="grow"></span><label class="row small" style="gap:6px">开启保号<label class="switch"><input type="checkbox" data-k="enabled" ${form.enabled ? "checked" : ""}><span></span></label></label></div>
          <div class="row small" style="gap:8px"><label class="row" style="gap:6px">每隔<select class="field" data-k="days">${[30, 60, 90, 120, 150, 180].map((n) => `<option value="${n}" ${n === form.days ? "selected" : ""}>${n} 天</option>`).join("")}</select></label><input class="field" data-k="to" placeholder="发给（留空 = 发给本机号码）" style="width:210px" value="${esc(form.to)}"><input class="field" data-k="text" placeholder="内容" style="width:120px" value="${esc(form.text)}"></div>
          <div class="row" style="gap:8px"><button class="btn primary small" data-a="save" ${busy ? "disabled" : ""}>保存</button><button class="btn small" data-a="run" ${busy ? "disabled" : ""}>立即发一次</button>${busy ? '<div class="spinner"></div>' : ""}<span class="grow"></span><span class="tiny ${cur.plan.last_error ? "orange" : "muted"}">${esc(lastLine(cur))}</span></div></div>`
          : status ? '<div class="small orange">读不到 SIM 卡。</div>' : '<div class="row small muted" style="gap:6px"><div class="spinner"></div>正在读取…</div>'}
        ${same.length ? `<div class="small strong">同一张 eSIM 卡上的其他号码</div>${same.map((c) => `<div class="row small" style="gap:8px"><label class="switch"><input type="checkbox" data-prof="${esc(c.iccid)}" ${c.plan.enabled ? "checked" : ""} ${busy ? "disabled" : ""}><span></span></label><span>${esc((c.profile || "eSIM") + " · " + short(c.iccid))}</span><span class="grow"></span>
          ${status.rotating === c.iccid ? '<div class="spinner"></div><span class="tiny muted">正在切换并发送…</span>' : `<span class="tiny ${c.plan.last_error || overdue(c) ? "orange" : "muted"}">${esc(lastLine(c))}</span><button class="btn small" data-runp="${esc(c.iccid)}" ${busy || status.rotating ? "disabled" : ""}>立即保号</button>`}</div>`).join("")}
          <div class="tiny muted">这些号码平时没启用。到期后会在凌晨 3–6 点自动切过去发保号短信，再切回来（代理会断开几分钟）。「立即保号」现在就做一次。</div>` : ""}
        ${others.length ? `<div class="small strong">其他卡</div>${others.map((c) => `<div class="row small" style="gap:8px">${icon(c.plan.enabled ? "check" : "ring", c.plan.enabled ? "green" : "muted")}<span class="mono tiny">${esc(short(c.iccid) + (c.plan.phone ? " · " + c.plan.phone : ""))}</span><span class="grow"></span><span class="tiny ${overdue(c) ? "orange" : "muted"}">${esc(lastLine(c))}</span><button class="btn small" data-rm="${esc(c.iccid)}" ${busy ? "disabled" : ""}>移除</button></div>`).join("")}<div class="tiny muted">其他卡的保号由插着它的模块执行；插到任一模块上即可。</div>` : ""}
        ${message ? `<div class="small ${message[1] ? "green" : "orange"}">${esc(message[0])}</div>` : ""}</div>`;
      el.querySelectorAll("[data-k]").forEach((i) => { i.onchange = i.oninput = () => { form[i.dataset.k] = i.type === "checkbox" ? i.checked : i.dataset.k === "days" ? Number(i.value) : i.value; }; });
      const call = async (fn, ok) => { busy = true; draw(); try { apply(await fn()); message = ok ? [ok, true] : null; } catch (e) { message = [e.message, false]; } busy = false; draw(); };
      const save = (iccid, enabled, to, text) => api("api/sim/keepalive", { body: { iccid, enabled, every_days: form.days, to, text }, timeout: 15 });
      const sv = el.querySelector('[data-a="save"]'); if (sv) sv.onclick = () => call(() => save(cur.iccid, form.enabled, form.to, form.text), "已保存");
      const rn = el.querySelector('[data-a="run"]'); if (rn) rn.onclick = () => call(() => api("api/sim/keepalive", { body: { action: "run", iccid: "" }, timeout: 60 }), "保号短信已发出");
      el.querySelectorAll("[data-prof]").forEach((i) => { i.onchange = () => { const c = cards.find((x) => x.iccid === i.dataset.prof); call(() => save(c.iccid, i.checked, c.plan.to || "", c.plan.text || "保号"), "已保存"); }; });
      el.querySelectorAll("[data-runp]").forEach((b) => { b.onclick = () => call(() => api("api/sim/keepalive", { body: { action: "run", iccid: b.dataset.runp }, timeout: 60 }), "已开始：切换号码 → 发保号短信 → 切回，约 5–10 分钟"); });
      el.querySelectorAll("[data-rm]").forEach((b) => { b.onclick = () => call(() => api("api/sim/keepalive", { body: { action: "delete", iccid: b.dataset.rm }, timeout: 60 })); });
    }
    draw();
    api("api/sim/keepalive", { timeout: 40 }).then((r) => { apply(r); draw(); }).catch((e) => { message = [e.message, false]; draw(); });
  }

  // ---- 状态 -------------------------------------------------------------------
  function statusPanel(el) {
    let status = null, prev = null, rx = null, tx = null, traffic = null;
    const rate = (r) => r == null || r < 0 ? "--" : r >= 1048576 ? (r / 1048576).toFixed(1) + " MB/s" : r >= 1024 ? (r / 1024).toFixed(1) + " KB/s" : r.toFixed(0) + " B/s";
    const row = (l, v) => `<div class="row" style="padding:8px 0;gap:8px;border-bottom:1px solid var(--line)"><span class="small muted">${l}</span><span class="grow"></span><span class="small strong sel">${esc(v)}</span></div>`;
    function draw() {
      App.setHTML(el, row("运营商", (status && status.operator) || "--") + row("SIM 卡接入", status && status.sim_inserted != null ? (status.sim_inserted ? "已接入" : "未接入") : "--") +
        row("网络模式", (status && status.network_mode) || "--") + row("信号强度", status && status.signal_dbm != null ? status.signal_dbm + " dBm" : "--") +
        row("下载速度", rate(rx)) + row("上传速度", rate(tx)) + row("本次流量", traffic && traffic.available ? fmt.bytes(traffic.session_total_bytes) : "--") +
        '<div class="small muted" style="padding-top:8px">每 2 秒自动更新</div>');
    }
    async function sample() {
      try {
        const t = await api("api/network/traffic");
        if (prev && t.available && prev.available && t.sampled_at_ms > prev.sampled_at_ms) {
          const s = (t.sampled_at_ms - prev.sampled_at_ms) / 1000;
          rx = (t.rx_bytes - prev.rx_bytes) / s; tx = (t.tx_bytes - prev.tx_bytes) / s;
        }
        prev = t; traffic = t;
      } catch (_) {}
      draw();
    }
    draw();
    api("api/status").then((s) => { status = s; draw(); }).catch(() => {});
    sample();
    const t = setInterval(sample, 2000);
    const t2 = setInterval(() => api("api/status").then((s) => { status = s; }).catch(() => {}), 10000);
    return () => { clearInterval(t); clearInterval(t2); };
  }

  // ---- Windows 通知 / 铃声 (the tray app reads these) --------------------------
  const RINGTONES = [["Ring01", "铃声 1"], ["Ring02", "铃声 2"], ["Ring03", "铃声 3"], ["Ring04", "铃声 4"], ["Ring05", "铃声 5"], ["Ring06", "铃声 6"], ["Ring07", "铃声 7"], ["Ring08", "铃声 8"], ["Ring09", "铃声 9"], ["Ring10", "铃声 10"], ["Alarm01", "闹铃"], ["off", "静音"]];
  let desktop = null;
  async function saveDesktop(change) {
    const next = Object.assign({}, desktop || {}, change);
    desktop = await api("api/desktop/settings", { method: "PUT", body: next, unscoped: true });
  }
  const toggle = (key, on, label) => `<label class="row small" style="gap:8px;cursor:pointer"><input type="checkbox" data-dk="${key}" ${on ? "checked" : ""}>${label}</label>`;
  function notifyPanel(el) {
    function draw() {
      const s = desktop || { notifications: true, show_contents: false };
      el.innerHTML = `<div class="col" style="gap:8px">${toggle("notifications", s.notifications, "Windows 通知：新短信和来电")}${toggle("show_contents", s.show_contents, "在通知中显示短信内容和验证码")}${toggle("copy_code", s.copy_code !== false, "收到验证码自动复制（直接 Ctrl+V 粘贴）")}
        <div class="small muted">默认隐藏短信正文和验证码，只显示发件人。通知由右下角托盘里的 DJOneHub 发出，托盘退出后不再通知（代理照常运行）。</div></div>`;
      el.querySelectorAll("[data-dk]").forEach((c) => { c.onchange = () => saveDesktop({ [c.dataset.dk]: c.checked }).then(draw).catch((e) => toast(e.message, "failure")); });
    }
    draw();
    api("api/desktop/settings", { unscoped: true }).then((s) => { desktop = s; draw(); }).catch(() => {});
  }
  function ringtoneRow(el) {
    function draw() {
      const cur = (desktop && desktop.ringtone) || "Ring01";
      el.innerHTML = `<div class="row"><span class="small muted" style="width:80px">来电铃声</span><select class="field" data-ring style="width:auto;min-width:140px">${RINGTONES.map(([v, l]) => `<option value="${v}" ${v === cur ? "selected" : ""}>${l}</option>`).join("")}</select></div>`;
      el.querySelector("[data-ring]").onchange = (e) => saveDesktop({ ringtone: e.target.value }).then(() => toast("铃声已更换", "success")).catch((x) => toast(x.message, "failure"));
    }
    draw();
    api("api/desktop/settings", { unscoped: true }).then((s) => { desktop = s; draw(); }).catch(() => {});
  }

  // ---- 多模块代理 -------------------------------------------------------------
  function fleetPanel(el) {
    const n = App.modules.length;
    el.innerHTML = `<div class="col" style="gap:6px"><div class="strong">${icon("cable", "accent")} ${App.fleetMode ? "多模块模式已开启" : "当前使用单模块模式"}${n ? ` · ${n} 个模块` : ""}</div>
      <div class="small muted">每台模块使用独立后台和固定 SOCKS5 端口，从 10909 开始分配。设备断开后保留端口，不自动换用其他 SIM。</div>
      <div class="small muted">所有功能对每个模块都可用：左上角切换模块，拨号、短信发送、频段、流量、呼叫转移等页面针对选中的模块；来电、短信列表和推送覆盖全部模块，并标明来自哪个模块。电脑只有一套麦克风和扬声器，同一时间只能在电脑上接听一路通话。</div></div>`;
  }

  // ---- 通话支持 (ModuleSetupCard) ----------------------------------------------
  function moduleSetupPanel(el) {
    let s = null, busy = false, err = "", timer = null;
    const pending = (st) => ["initializing", "restarting", "verifying", "reconnecting"].includes(st);
    function draw() {
      const st = s && s.state;
      const ic = st === "ready" ? ["check", "green"] : st === "failed" || st === "unsupported" ? ["warn", "orange"] : pending(st) ? ["cycle", "accent"] : ["phone", "accent"];
      el.innerHTML = `<div class="col" style="gap:8px"><div class="row" style="gap:10px;align-items:flex-start">${icon(ic[0], ic[1])}<div class="col grow" style="gap:3px"><span class="strong small">${esc((s && s.summary) || "正在检查模块状态")}</span><span class="tiny muted mono" style="word-break:break-all">${esc((s && s.detail) || "新模块只需初始化一次；之后直接插入即可使用。")}</span></div>${busy || pending(st) ? '<div class="spinner"></div>' : `<button class="btn small" data-a="refresh">${icon("refresh")}</button>`}</div>
        ${s && s.can_initialize && !busy ? '<div><button class="btn primary small" data-a="init">启用通话支持</button></div>' : ""}
        ${s && s.backup_path ? '<div class="tiny muted">已备份原始模块配置</div>' : ""}
        <div class="tiny muted">通话时声音走这台电脑的麦克风和扬声器；通话录音保存在“文档\\DJOneHub\\通话录音”。</div>
        ${err ? `<div class="small red">${esc(err)}</div>` : ""}
        ${App.fleetMode && App.selected ? `<div class="tiny muted">针对 ${esc(App.selected.name)}（左上角切换模块）</div>` : ""}</div>`;
      const r = el.querySelector('[data-a="refresh"]'); if (r) r.onclick = load;
      const i = el.querySelector('[data-a="init"]'); if (i) i.onclick = () => App.confirmDialog("启用通话支持？", "兼容原始模块、旧 UAC 配置及其他工具留下的完整 USB 配置。将先备份当前配置，再补齐通话所需接口并重启模块；过程中 4G 会短暂断开。验证失败会自动恢复原始配置，并显示具体原因。", "启用", true).then((ok) => ok && init());
    }
    async function load() {
      busy = true; err = ""; draw();
      try { s = await api("api/module/setup", { timeout: 20 }); } catch (e) { err = e.message; }
      busy = false; draw();
      clearTimeout(timer);
      if (s && pending(s.state)) timer = setTimeout(load, 2000);
    }
    async function init() {
      busy = true; err = ""; draw();
      try { s = await api("api/module/setup", { body: { confirm: true }, timeout: 20 }); }
      catch (e) { try { s = await api("api/module/setup", { timeout: 20 }); if (!pending(s.state)) err = e.message; } catch (_) { err = e.message; } }
      busy = false; draw();
      if (s && pending(s.state)) timer = setTimeout(load, 2000);
    }
    load();
    return () => clearTimeout(timer);
  }

  // ---- 语音运行时 (VoiceRuntimeCard) -------------------------------------------
  function voiceRuntimePanel(el) {
    let s = null, busy = false, err = "";
    function draw() {
      const ok = s && s.runtime_installed;
      el.innerHTML = `<div class="col" style="gap:8px"><div class="row" style="gap:10px;align-items:flex-start">${icon(ok ? "wave" : "download", ok ? "green" : "accent")}<div class="col grow" style="gap:3px"><span class="strong small">${ok ? "语音运行时已就绪" : "尚未安装语音运行时"}</span><span class="tiny muted">${esc((s && s.runtime_detail) || "首次确认后从上游固定版本下载并校验，下载可能需要数分钟；模块重启后将从本机缓存自动恢复。")}</span></div>${busy ? '<div class="spinner"></div>' : `<button class="btn small" data-a="refresh">${icon("refresh")}</button>`}</div>
        ${s && !ok && !busy ? '<div><button class="btn primary small" data-a="get">确认并启用通话</button></div>' : ""}
        ${err ? `<div class="small red">${esc(err)}</div>` : ""}</div>`;
      const r = el.querySelector('[data-a="refresh"]'); if (r) r.onclick = load;
      const g = el.querySelector('[data-a="get"]'); if (g) g.onclick = () => App.confirmDialog("下载并启用语音运行时？", "将从 MaVo 官方固定版本直接下载模块侧语音运行时，校验 SHA-256 后保存到本机。DJOneHub 不随软件分发该运行时。首次通话时会临时部署到当前模块；不刷写固件。", "确认并启用", true).then((ok) => ok && provision());
    }
    async function load() { busy = true; err = ""; draw(); try { s = await api("api/voice/status", { timeout: 15 }); } catch (e) { err = e.message; } busy = false; draw(); }
    async function provision() {
      busy = true; err = ""; draw();
      try { s = await api("api/voice/provision", { body: {}, timeout: 200 }); }
      catch (e) { err = /timeout|超时/i.test(e.message) ? "语音运行时下载超过 3 分钟未完成，请检查网络后重试。" : e.message; }
      busy = false; draw();
    }
    load();
  }

  // ---- 服务控制 ---------------------------------------------------------------
  function quitPanel(el) {
    el.innerHTML = `<div class="row" style="gap:10px"><button class="btn danger" data-a="quit">${icon("power")}完全退出</button><span class="small muted">停止 4G 后台、代理、短信守护与通知服务</span></div>`;
    el.querySelector('[data-a="quit"]').onclick = () => App.confirmDialog("确认完全退出？", "将停止 DJOneHub 后台服务（代理、短信转发、自动恢复）和托盘通知。需要时双击桌面上的 DJOneHub 即可恢复（会请求管理员权限）。", "退出", true).then(async (ok) => {
      if (!ok) return;
      try { await api("api/desktop/quit", { body: {}, unscoped: true }); toast("正在退出 DJOneHub…", "success"); setTimeout(() => window.close(), 1500); }
      catch (e) { toast(e.message, "failure"); }
    });
  }

  App.pages.settings = function (host) {
    const appearance = localStorage.getItem("djonehub.appearance") || "system";
    host.innerHTML = `<div style="display:flex;flex-direction:column;height:100vh"><div class="row" style="padding:10px 14px"><span style="font-size:17px;font-weight:600">设置</span></div><div class="divider" style="margin:0"></div>
      <div style="overflow-y:auto;flex:1"><div class="page" style="gap:10px;max-width:900px">
      ${section("Windows 通知")}${cardWrap('<div data-nt></div>')}
      ${section("短信转发")}${cardWrap('<div data-fwd></div>')}
      ${section("呼叫转移")}${cardWrap('<div data-cf></div>')}
      ${section("SIM 保号")}${cardWrap('<div data-ka></div>')}
      ${section("多模块代理")}${cardWrap('<div data-fl></div>')}
      ${section("状态")}${cardWrap('<div data-st></div>')}
      ${section("通话支持")}${cardWrap('<div data-ms></div>')}
      ${section("语音运行时")}${cardWrap('<div data-vr></div>')}
      ${section("通用")}${cardWrap(`<div class="row"><span class="small muted" style="width:80px">外观</span><div class="row" style="gap:0;border:1px solid var(--line-2);border-radius:7px;overflow:hidden">${[["system", "跟随系统"], ["light", "浅色"], ["dark", "深色"]].map(([v, l]) => `<button data-theme="${v}" style="border:0;padding:5px 14px;cursor:pointer;background:${v === appearance ? "var(--accent)" : "transparent"};color:${v === appearance ? "#fff" : "inherit"}">${l}</button>`).join("")}</div></div><div class="divider" style="margin:10px 0"></div><div data-rt></div>`)}
      ${section("服务控制")}${cardWrap(`<div data-qt></div><div class="small muted" style="margin-top:10px">DJOneHub 作为 Windows 服务运行：开机自动启动，不用登录；关掉这个窗口后代理、短信转发和自动恢复照常工作。</div><div class="small muted" data-ver style="margin-top:8px"></div>`)}
      </div></div></div>`;
    forwardPanel(host.querySelector("[data-fwd]"));
    callForwardPanel(host.querySelector("[data-cf]"));
    keepAlivePanel(host.querySelector("[data-ka]"));
    const stopStatus = statusPanel(host.querySelector("[data-st]"));
    notifyPanel(host.querySelector("[data-nt]"));
    fleetPanel(host.querySelector("[data-fl]"));
    const stopSetup = moduleSetupPanel(host.querySelector("[data-ms]"));
    voiceRuntimePanel(host.querySelector("[data-vr]"));
    ringtoneRow(host.querySelector("[data-rt]"));
    quitPanel(host.querySelector("[data-qt]"));
    host.querySelectorAll("[data-theme]").forEach((b) => { b.onclick = () => { localStorage.setItem("djonehub.appearance", b.dataset.theme); App.applyTheme(); App.show("settings"); }; });
    api("api/platform", { unscoped: true }).then((p) => { host.querySelector("[data-ver]").textContent = "版本 " + (p.version || "") + " · " + (p.os || ""); }).catch(() => {});
    return { destroy: () => { stopStatus(); stopSetup(); } };
  };

  // ---- 软件更新 ---------------------------------------------------------------
  App.pages.updates = function (host) {
    let current = "", release = null, message = "", busy = false, upd = null, poll = null;
    function draw() {
      const asset = release && (release.assets || []).find((a) => /Windows.*(Setup|amd64).*\.exe$/i.test(a.name)) || release && (release.assets || []).find((a) => /Windows-amd64.*\.zip$/i.test(a.name));
      const newer = release && current && release.tag_name.replace(/^v/, "") !== current;
      const working = upd && (upd.state === "downloading" || upd.state === "installing");
      host.innerHTML = `<div class="page" style="padding:28px;gap:18px;max-width:900px;min-height:100vh">
        <div class="row"><span style="font:700 22px var(--round)">${icon("download", "accent")} 软件更新</span><span class="grow"></span><span class="small mono muted">${esc(current)}</span></div>
        <div class="muted">更新源：${REPO}</div>
        <div class="sel">${esc(message)}</div>
        <div class="row" style="gap:8px"><button class="btn" data-a="check" ${busy || working ? "disabled" : ""}>检查更新</button>${newer && asset && /Setup/i.test(asset.name) ? `<button class="btn primary" data-a="install" ${working ? "disabled" : ""}>${icon("download")}下载并安装 ${esc(release.tag_name)}</button>` : newer && asset ? `<a class="btn primary" href="${esc(asset.browser_download_url)}" target="_blank">${icon("download")}下载 ${esc(asset.name)}</a>` : ""}${busy || working ? '<div class="spinner"></div>' : ""}</div>
        ${upd && upd.state === "downloading" ? `<div class="col" style="gap:6px;max-width:420px"><div class="small">正在下载 ${esc(upd.tag)}… ${Math.round((upd.progress || 0) * 100)}%</div><div style="height:6px;border-radius:3px;background:var(--line)"><div style="height:6px;border-radius:3px;background:var(--accent);width:${Math.round((upd.progress || 0) * 100)}%"></div></div></div>` : ""}
        ${upd && upd.state === "installing" ? '<div class="small strong">安装包校验通过，正在安装。窗口会关闭，安装完成后自动重新打开（约半分钟），代理会短暂中断。</div>' : ""}
        ${upd && upd.state === "failed" ? `<div class="small red">更新失败：${esc(upd.error || "")}</div>` : ""}
        ${release ? `<div class="divider"></div><div class="strong" style="font-size:15px">${esc(release.tag_name)}</div><div class="sel" style="white-space:pre-wrap;line-height:1.6">${esc(release.body || "此版本没有更新说明。")}</div>` : ""}
        <div class="grow"></div><div class="small muted">只从本仓库的 GitHub 发布页下载，校验 SHA-256 后自动安装；配置和绑定都会保留。有通话或模块操作时会等它结束。</div></div>`;
      host.querySelector('[data-a="check"]').onclick = check;
      const ins = host.querySelector('[data-a="install"]');
      if (ins) ins.onclick = install;
    }
    async function install() {
      try { await api("api/update/windows", { body: { tag: release.tag_name }, unscoped: true, timeout: 30 }); upd = { state: "downloading", tag: release.tag_name, progress: 0 }; draw(); watch(); }
      catch (e) { toast(e.message, "failure"); }
    }
    function watch() {
      clearInterval(poll);
      poll = setInterval(async () => {
        if (!host.isConnected) { clearInterval(poll); return; }
        try { upd = await api("api/update/windows", { unscoped: true, timeout: 4 }); } catch (_) { /* the service is restarting */ }
        if (current && upd && upd.state === "idle") { clearInterval(poll); }
        draw();
      }, 1000);
    }
    async function check() {
      busy = true; message = "正在检查…"; draw();
      try {
        const r = await fetch(`https://api.github.com/repos/${REPO}/releases?per_page=10`, { headers: { Accept: "application/vnd.github+json" } });
        if (!r.ok) throw new Error("GitHub 返回 " + r.status);
        const list = await r.json();
        release = list.find((x) => !x.draft && (x.assets || []).some((a) => /Windows/i.test(a.name))) || list[0];
        const latest = release ? release.tag_name.replace(/^v/, "") : "";
        message = !release ? "没有找到发布版本" : latest === current ? "已是最新版本 " + current : "有新版本 " + latest + "（当前 " + current + "）";
      } catch (e) { message = "检查失败：" + e.message; }
      busy = false; draw();
    }
    api("api/update/windows", { unscoped: true }).then((u) => { upd = u; if (u && (u.state === "downloading" || u.state === "installing")) watch(); }).catch(() => {});
    api("api/platform", { unscoped: true }).then((p) => { current = p.version || ""; draw(); check(); }).catch(() => { draw(); check(); });
    draw();
  };
})();
