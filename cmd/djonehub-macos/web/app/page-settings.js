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

  App.pages.settings = function (host) {
    const appearance = localStorage.getItem("djonehub.appearance") || "system";
    host.innerHTML = `<div style="display:flex;flex-direction:column;height:100vh"><div class="row" style="padding:10px 14px"><span style="font-size:17px;font-weight:600">设置</span></div><div class="divider" style="margin:0"></div>
      <div style="overflow-y:auto;flex:1"><div class="page" style="gap:10px;max-width:900px">
      ${section("短信转发")}${cardWrap('<div data-fwd></div>')}
      ${section("呼叫转移")}${cardWrap('<div data-cf></div>')}
      ${section("SIM 保号")}${cardWrap('<div data-ka></div>')}
      ${section("状态")}${cardWrap('<div data-st></div>')}
      ${section("通用")}${cardWrap(`<div class="row"><span class="small muted" style="width:80px">外观</span><div class="row" style="gap:0;border:1px solid var(--line-2);border-radius:7px;overflow:hidden">${[["system", "跟随系统"], ["light", "浅色"], ["dark", "深色"]].map(([v, l]) => `<button data-theme="${v}" style="border:0;padding:5px 14px;cursor:pointer;background:${v === appearance ? "var(--accent)" : "transparent"};color:${v === appearance ? "#fff" : "inherit"}">${l}</button>`).join("")}</div></div>`)}
      ${section("后台服务")}${cardWrap(`<div class="small muted">DJOneHub 作为 Windows 服务运行：开机自动启动，不用登录；关掉这个窗口后代理、短信转发和自动恢复照常工作。要完全停止，请在“服务”里停止“DJOneHub 蜂窝代理”，或卸载 DJOneHub。</div><div class="small muted" data-ver style="margin-top:8px"></div>`)}
      </div></div></div>`;
    forwardPanel(host.querySelector("[data-fwd]"));
    callForwardPanel(host.querySelector("[data-cf]"));
    keepAlivePanel(host.querySelector("[data-ka]"));
    const stopStatus = statusPanel(host.querySelector("[data-st]"));
    host.querySelectorAll("[data-theme]").forEach((b) => { b.onclick = () => { localStorage.setItem("djonehub.appearance", b.dataset.theme); App.applyTheme(); App.show("settings"); }; });
    api("api/platform", { unscoped: true }).then((p) => { host.querySelector("[data-ver]").textContent = "版本 " + (p.version || "") + " · " + (p.os || ""); }).catch(() => {});
    return { destroy: stopStatus };
  };

  // ---- 软件更新 ---------------------------------------------------------------
  App.pages.updates = function (host) {
    let current = "", release = null, message = "", busy = false;
    function draw() {
      const asset = release && (release.assets || []).find((a) => /Windows.*(Setup|amd64).*\.exe$/i.test(a.name)) || release && (release.assets || []).find((a) => /Windows-amd64.*\.zip$/i.test(a.name));
      const newer = release && current && release.tag_name.replace(/^v/, "") !== current;
      host.innerHTML = `<div class="page" style="padding:28px;gap:18px;max-width:900px;min-height:100vh">
        <div class="row"><span style="font:700 22px var(--round)">${icon("download", "accent")} 软件更新</span><span class="grow"></span><span class="small mono muted">${esc(current)}</span></div>
        <div class="muted">更新源：${REPO}</div>
        <div class="sel">${esc(message)}</div>
        <div class="row" style="gap:8px"><button class="btn" data-a="check" ${busy ? "disabled" : ""}>检查更新</button>${newer && asset ? `<a class="btn primary" href="${esc(asset.browser_download_url)}" target="_blank">${icon("download")}下载 ${esc(asset.name)}</a>` : ""}${busy ? '<div class="spinner"></div>' : ""}</div>
        ${release ? `<div class="divider"></div><div class="strong" style="font-size:15px">${esc(release.tag_name)}</div><div class="sel" style="white-space:pre-wrap;line-height:1.6">${esc(release.body || "此版本没有更新说明。")}</div>` : ""}
        <div class="grow"></div><div class="small muted">只从本仓库的 GitHub 发布页下载。下载后运行安装包即可覆盖升级，配置和绑定都会保留。</div></div>`;
      host.querySelector('[data-a="check"]').onclick = check;
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
    api("api/platform", { unscoped: true }).then((p) => { current = p.version || ""; draw(); check(); }).catch(() => { draw(); check(); });
    draw();
  };
})();
