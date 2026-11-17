/* 蜂窝代理: ProxyDashboard, ProxyLinksPanel and CheckupSheet of the macOS app. */
(function () {
  "use strict";
  const { icon, esc, api, toast, fmt } = App;

  const ACTION_TITLE = {
    start: "正在启动蜂窝代理", stop: "正在停止代理", probe: "正在检测出口 IP", reconnect: "正在重连并换 IP",
    reconnect_clean: "正在换干净 IP", reconnect_segment: "正在换到新网段", stop_reconnect: "正在停止后续尝试",
  };
  const PHASE_TEXT = {
    reconnecting: "正在重新注册蜂窝网络…", changed: "已检测到新的公网 IP", unchanged: "已自动重试，运营商仍分配了原 IP",
    new_segment: "已换到新网段", segment_reused: "未换到新网段：运营商只分配了最近用过的网段", stopped: "已停止后续尝试，保留当前 IP",
    recovered_unverified: "网络已恢复，未能可靠比较前后 IP", clean: "已换到干净 IP", clean_failed: "试了多次没换到干净 IP，保留最后一个",
    clean_unknown: "欺诈值查询失败，保留当前 IP", failed: "恢复未完成，请检查模块",
  };
  const segmentOf = (ip) => String(ip || "").split(".").slice(0, 3).join(".");
  const flagName = (f) => ({ proxy: "代理", vpn: "VPN", tor: "Tor", recent_abuse: "近期滥用", bot: "机器人流量" }[f] || f);

  function carrierLabel(prefix) {
    const mccmnc = String(prefix || "").slice(0, 5);
    if (["46000", "46002", "46004", "46007", "46008"].includes(mccmnc)) return "中国移动";
    if (["46001", "46006", "46009"].includes(mccmnc)) return "中国联通";
    if (["46003", "46005", "46011"].includes(mccmnc)) return "中国电信";
    const mcc = String(prefix || "").slice(0, 3);
    const map = { 454: "香港卡", 455: "澳门卡", 466: "台湾卡", 502: "马来西亚卡", 525: "新加坡卡", 440: "日本卡", 441: "日本卡", 450: "韩国卡", 515: "菲律宾卡" };
    if (map[mcc]) return map[mcc];
    if (/^31[0-6]/.test(mcc)) return "美国卡";
    if (!mcc) return "SIM 卡";
    return mcc.startsWith("2") ? "欧洲卡" : "境外卡";
  }
  App.carrierLabel = carrierLabel;
  function networkDetail(n) {
    const home = carrierLabel(n.home_plmn);
    const card = home.endsWith("卡") ? home : home + "卡";
    if (!n.serving_plmn) return card;
    const serving = n.serving_plmn.startsWith("460") ? carrierLabel(n.serving_plmn) : "PLMN " + n.serving_plmn + " ";
    return card + (n.roaming ? " · 漫游接入" : " · ") + serving + "网络";
  }

  function scoreChip(title, label, color) {
    return `<span class="row" style="gap:5px;display:inline-flex"><span class="chip mono" style="background:color-mix(in srgb, var(--${color}) 18%, transparent);color:var(--${color})">${esc(title)}</span><span class="small" style="color:var(--${color})">${esc(label)}</span></span>`;
  }
  function fraudBadge(v) {
    const parts = [];
    if (v.fraud_score != null) {
      const s = v.fraud_score;
      const byRisk = { "very high": ["极高风险", "red"], high: ["高风险", "red"], medium: ["中风险", "orange"], low: ["低风险", "green"] }[v.fraud_risk || ""];
      const [label, color] = byRisk || (s >= 75 ? ["高风险", "red"] : s >= 35 ? ["中风险", "orange"] : ["低风险", "green"]);
      parts.push(`<span title="Scamalytics 欺诈分数（0~100，越低越干净），经宽带查询，不花蜂窝流量；当前 IP 每 12 小时刷新一次">${scoreChip("Scamalytics " + s, label, color)}</span>`);
    }
    if (v.ipqs_score != null) {
      const s = v.ipqs_score;
      const [label, color] = s >= 85 ? ["高风险", "red"] : s >= 75 ? ["可疑", "orange"] : ["干净", "green"];
      const flags = (v.ipqs_flags || []).map(flagName).join("·");
      parts.push(`<span title="IPQualityScore 欺诈分数（0~100；75 以上可疑，85 以上高风险）">${scoreChip("IPQS " + s, flags ? label + " · " + flags : label, color)}</span>`);
    }
    return `<div class="row" style="gap:10px">${parts.join("")}</div>`;
  }
  App.fraudBadge = fraudBadge;

  function busyBanner(title, started, hint) {
    const secs = Math.max(0, Math.floor((Date.now() - started) / 1000));
    return `<div class="banner"><div class="spinner"></div><div><div class="b1">${esc(title)} · ${secs} 秒</div>${hint ? `<div class="b2">${esc(hint)}</div>` : ""}</div></div>`;
  }
  function issueBanner(issue) {
    const since = fmt.parseDate(issue.since);
    let elapsed = "";
    if (since) { const s = Math.max(0, Math.floor((Date.now() - since) / 1000)); elapsed = s < 60 ? ` · ${s} 秒` : ` · 已 ${Math.floor(s / 60)} 分钟`; }
    const lead = issue.level === "info" ? '<div class="spinner"></div>' : icon(issue.level === "error" ? "xoct" : "warn", issue.level === "error" ? "red" : "orange");
    return `<div class="banner ${issue.level === "error" ? "error" : issue.level === "warn" ? "warn" : ""}">${lead}<div><div class="b1">${esc(issue.title + elapsed)}</div><div class="b2">${esc(issue.detail)}</div></div></div>`;
  }
  App.busyBanner = busyBanner;
  App.issueBanner = issueBanner;

  function usageMeter(card) {
    const quota = (card.plan && card.plan.quota_mb) ? card.plan.quota_mb * 1048576 : 0;
    const used = card.cycle ? card.cycle.used : 0;
    const pct = card.cycle ? card.cycle.percent : 0;
    const color = pct >= 100 ? "var(--red)" : pct >= 80 ? "var(--orange)" : "var(--accent)";
    return `<div class="row" style="align-items:baseline;gap:6px"><span class="mono strong" style="font-size:18px">${fmt.bytes(used)}</span>${quota ? `<span class="small muted">/ ${fmt.bytes(quota)}</span>` : ""}</div>
      ${quota ? `<div style="height:5px;border-radius:5px;background:var(--line-2);overflow:hidden"><div style="height:100%;width:${Math.min(100, quota ? used / quota * 100 : 0)}%;background:${color}"></div></div>` : ""}
      <div class="tiny muted">${fmt.dateOnly(card.cycle && card.cycle.start)} 起 · 剩 ${fmt.daysLeft(card.cycle && card.cycle.end)} 天重置</div>`;
  }
  App.usageMeter = usageMeter;

  // ---- checkup ------------------------------------------------------------
  const lvlColor = (l) => ({ ok: "green", warn: "orange", fail: "red" }[l] || "muted");
  const lvlIcon = (l) => ({ ok: "check", warn: "warn", fail: "xoct" }[l] || "minus");
  App.openCheckup = function () {
    const m = App.modal(`<div class="row"><h3>${icon("stethoscope")}一键体检</h3><div class="grow"></div><button class="btn" data-a="again">重新体检</button><button class="btn primary" data-a="done">完成</button></div><div data-body class="col" style="margin-top:14px"></div>`, { wide: true });
    const body = m.el.querySelector("[data-body]");
    const again = m.el.querySelector('[data-a="again"]');
    m.el.querySelector('[data-a="done"]').onclick = m.close;
    let started = 0, timer = null;
    async function run() {
      again.disabled = true; again.textContent = "体检中…"; started = Date.now();
      const busy = () => { body.innerHTML = busyBanner("正在逐项检查：模块、卡片、网络、电脑网卡、代理、上网", started, "每个模块同时检查，一般 3–10 秒。"); };
      busy(); timer = setInterval(busy, 1000);
      try {
        const r = await api("api/checkup", { unscoped: true, timeout: 40 });
        clearInterval(timer);
        const cards = (r.modules || []).map((mod) => `<div class="card" style="background:color-mix(in srgb, var(--${lvlColor(mod.level)}) 7%, transparent);padding:14px">
          <div class="row">${icon(lvlIcon(mod.level), lvlColor(mod.level))}<span class="strong" style="font-size:15px">${esc(mod.name || "蜂窝模块")}</span><span class="grow"></span>${mod.seconds ? `<span class="small muted">${mod.seconds.toFixed(1)} 秒</span>` : ""}</div>
          <div class="strong ${lvlColor(mod.level)}" style="margin:8px 0">${esc(mod.summary)}</div><div class="divider"></div>
          ${(mod.items || []).map((it) => `<div class="row top" style="gap:8px;margin-top:8px">${icon(lvlIcon(it.level), lvlColor(it.level))}<span class="small strong" style="width:72px;flex:none">${esc(it.name)}</span><span class="small muted sel">${esc(it.detail)}</span></div>`).join("")}</div>`).join("");
        const push = r.push && r.push.level !== "off" ? `<div class="card row top" style="gap:8px;background:color-mix(in srgb, var(--${lvlColor(r.push.level)}) 7%, transparent)">${icon(lvlIcon(r.push.level), lvlColor(r.push.level))}<span class="small strong" style="width:72px;flex:none">${esc(r.push.name)}</span><span class="small muted sel">${esc(r.push.detail)}</span></div>` : "";
        body.innerHTML = cards + push;
        const bad = (r.modules || []).filter((x) => x.level !== "ok").length;
        toast(bad ? `体检完成：${bad} 个模块需要注意` : "体检完成：全部正常", bad ? "failure" : "success");
      } catch (e) {
        clearInterval(timer);
        body.innerHTML = `<div class="red">体检没能完成：${esc(e.message)}</div>`;
      }
      again.disabled = false; again.textContent = "重新体检";
    }
    again.onclick = run;
    run();
  };

  // ---- page ---------------------------------------------------------------
  App.pages.proxy = function (host) {
    const st = { status: null, usage: null, error: null, busy: false, action: null, legacy: null };
    host.innerHTML = `<div class="page">
      <div class="page-head"><div class="grow"><div class="eyebrow">CELLULAR GATEWAY</div><div class="title">蜂窝代理</div><div class="subtitle">宽带负责日常连接，SIM 负责指定应用的出口。</div></div>
        <button class="btn primary" data-a="checkup">${icon("stethoscope")}一键体检</button><span data-state></span></div>
      <div data-modules></div>
      <div class="card hero col" style="gap:14px" data-hero></div>
      <div data-links></div>
      <div data-friends></div>
      <div class="metrics" data-metrics></div>
      <div class="card soft col pad18" style="gap:10px" data-result></div>
      <div class="card pad18 col" style="gap:12px" data-history></div>
      <details class="card"><summary>连接方式与使用边界</summary><div class="col muted" style="margin-top:8px;gap:8px">
        <div>01&nbsp;&nbsp;&nbsp;在目标应用中配置 SOCKS5，并启用通过代理解析 DNS。</div>
        <div>02&nbsp;&nbsp;&nbsp;TCP。IPv4 优先；运营商提供 IPv6 时自动兜底（纯 IPv6 卡、只有 IPv6 的网站）。UDP / QUIC 暂不支持，指纹浏览器请把 WebRTC 设为替换或禁用。</div>
        <div>03&nbsp;&nbsp;&nbsp;模块断线后代理失败，不改走宽带；公网 IP 不保证独享或每次变化。</div>
        <div>04&nbsp;&nbsp;&nbsp;保持宽带为系统默认网络；DJOneHub 会把模块网卡排在宽带之后。</div>
        <div>05&nbsp;&nbsp;&nbsp;DNS：只走蜂窝。国内卡为运营商 DNS 与 223.5.5.5 / 119.29.29.29 并行；境外卡只用运营商 DNS；出口检测：myip.ipip.net / ip.3322.net / ddns.oray.com / ifconfig.me 并行；归属地：ipwho.is（会获知出口 IP）。位置为估算，非 GPS。</div>
      </div></details>
    </div>`;
    const $ = (s) => host.querySelector(s);
    $('[data-a="checkup"]').onclick = App.openCheckup;
    const avoidHours = () => Number(localStorage.getItem("djonehub.segmentAvoidHours") || 24);

    function status() {
      if (App.fleetMode) { const s = App.selected; return s && s.proxy ? s.proxy : null; }
      return st.legacy;
    }
    const unavailable = () => App.fleetMode && (!App.selected || !App.selected.connected || App.selected.needs_confirmation || !App.selected.proxy);
    const reconnecting = () => { const s = status(); return s && s.phase === "reconnecting"; };
    const disabled = () => st.busy || reconnecting() || !status() || unavailable();

    async function act(action, confirm, avoid) {
      const key = App.selectedID;
      if (action !== "stop_reconnect" && (st.busy || unavailable())) return;
      st.busy = true; st.action = { title: ACTION_TITLE[action] || "正在处理", started: Date.now() };
      toast(st.action.title + "…", "progress"); draw();
      try {
        const path = App.fleetMode ? `api/modules/${encodeURIComponent(key)}/proxy` : "api/proxy";
        const r = await api(path, { body: { action, confirm: !!confirm, avoid_hours: avoid }, timeout: 120 });
        if (!App.fleetMode) st.legacy = r;
        st.error = null;
        await App.refreshModules();
        const s = status() || r;
        const ip = s && s.public_ip;
        let done;
        if (s && s.error) done = s.error;
        else if (action === "start") done = s && s.running ? "代理已启动" + (ip ? "，出口 " + ip : "") : "代理没能启动，请看页面上的提示";
        else if (action === "stop") done = "代理已停止";
        else if (action === "probe") done = ip ? "出口 IP：" + ip : "没检测到出口 IP";
        else if (action === "stop_reconnect") done = "已停止后续尝试";
        else done = ip ? "完成，现在的出口 IP：" + ip : "已完成";
        toast(done, s && s.error ? "failure" : "success");
      } catch (e) {
        st.error = e.message;
        toast(st.action.title + "失败：" + e.message, "failure");
      }
      st.busy = false; st.action = null; draw();
    }

    function drawModules() {
      const el = $("[data-modules]");
      if (!App.fleetMode) { el.innerHTML = ""; return; }
      const running = App.modules.filter((m) => m.proxy && m.proxy.running).length;
      const sel = App.selected;
      if (!App.setHTML(el, `<div class="card col" style="gap:12px">
        <div class="row"><h3>模块与独立出口</h3><span class="grow"></span><span class="small muted">${running} 个代理运行中</span></div>
        ${App.modules.length ? "" : '<div class="muted">尚未接入模块。接入后自动识别，每台模块分配独立端口；原有绑定会保留。</div>'}
        <div class="grid">${App.modules.map((m) => {
          const line = !m.connected ? "未连接 · 端口保留" : m.needs_confirmation ? "需确认设备绑定" : (m.proxy && m.proxy.network_issue ? m.proxy.network_issue.title : (m.proxy && m.proxy.public_ip ? m.proxy.public_ip : "已连接 · 等待出口检测"));
          return `<button class="tile ${m.id === App.selectedID ? "active" : ""}" data-id="${esc(m.id)}"><div class="row"><span class="name">${esc(m.name)}</span><span class="grow"></span><span class="dot ${m.proxy && m.proxy.running ? "on" : ""}"></span></div>
            <span class="small mono">127.0.0.1:${m.port}</span><span class="small muted">${esc(line)}</span></button>`;
        }).join("")}</div>
        ${sel ? `<div class="row"><span class="small muted">${esc(sel.identity || "")}</span><span class="grow"></span><button class="btn" data-a="rename">重命名</button>${sel.needs_confirmation ? '<button class="btn" data-a="confirm">确认设备绑定</button>' : ""}</div>` : ""}
        <div class="small muted">每个端口固定绑定一个模块。模块断开时停止对应代理。拨号、短信、频段、流量、呼叫转移等页面都针对左上角选中的模块；来电和短信提醒覆盖全部模块。</div></div>`)) return;
      el.querySelectorAll(".tile").forEach((b) => { b.onclick = () => App.select(b.dataset.id); });
      const rn = el.querySelector('[data-a="rename"]');
      if (rn) rn.onclick = async () => {
        const name = await App.promptDialog("重命名模块", "模块名称", sel.name);
        if (name == null) return;
        try { await api(`api/modules/${encodeURIComponent(sel.id)}`, { method: "PATCH", body: { name }, unscoped: true }); await App.refreshModules(); }
        catch (e) { toast("重命名失败：" + e.message, "failure"); }
      };
      const cf = el.querySelector('[data-a="confirm"]');
      if (cf) cf.onclick = async () => {
        if (!(await App.confirmDialog("确认这个 USB 设备？", `将当前设备绑定到 ${sel.name} 的 127.0.0.1:${sel.port}。没有唯一序列号的设备重插后需要再次确认，避免使用错误 SIM。`, "确认绑定"))) return;
        toast("正在确认设备绑定…", "progress");
        try { await api(`api/modules/${encodeURIComponent(sel.id)}/confirm`, { body: { generation: sel.generation }, unscoped: true }); await App.refreshModules(); toast("已绑定 " + sel.name + "，十几秒后可以使用", "success"); }
        catch (e) { toast("绑定失败：" + e.message, "failure"); }
      };
    }

    function ipRecordLine(rec) {
      if (!rec.uses) return `<div class="strong accent" title="本机所有模块的记录里，这个 IP 以前从没用过">${icon("sparkles")} 第一次用这个 IP</div>`;
      const uses = rec.recent || [];
      const last = uses[0] && fmt.parseDate(uses[0].start);
      const first = fmt.parseDate(rec.first);
      const tip = uses.slice(0, 10).map((u) => {
        const s = fmt.parseDate(u.start), e = fmt.parseDate(u.end);
        let t = s ? fmt.dateTime(s) + (e ? "，用了 " + fmt.usageText(s, e) : "") : u.start;
        const who = [u.module, u.card].filter(Boolean).join(" · ");
        return who ? t + "（" + who + "）" : t;
      }).join("\n") + (rec.uses > uses.length ? `\n…更早还有 ${rec.uses - uses.length} 次` : "");
      return `<div title="${esc(tip)}"><span class="strong orange">${icon("history")} 以前用过 ${rec.uses} 次</span>${last ? `<span class="muted"> · 上次 ${fmt.agoText(last)}（${fmt.dateTime(last)}）</span>` : ""}${first && rec.uses > 1 ? `<span class="muted"> · 首次 ${fmt.dateOnly(first)}</span>` : ""}</div>`;
    }

    function visitEnd(v, s) {
      const last = fmt.parseDate(v.last_seen) || new Date();
      const hist = s.history || [];
      const current = s.running && v.ip === s.public_ip && hist.length && hist[hist.length - 1].first_seen === v.first_seen;
      return current ? new Date(Math.max(Date.now(), last)) : last;
    }

    function drawHero() {
      const s = status();
      const sel = App.selected;
      const awaiting = s && !s.running && s.auto_start;
      App.setHTML($("[data-state]"), `<span class="pill ${awaiting ? "orange" : ""}">${icon(s && s.running ? "dot" : awaiting ? "history" : "ring")}${s && s.running ? "监听中" : awaiting ? "等待恢复" : "已停止"}</span>`);
      const ip = s && s.public_ip;
      const hist = (s && s.history) || [];
      const lastVisit = hist[hist.length - 1];
      let used = "";
      if (lastVisit && lastVisit.ip === ip) {
        const start = fmt.parseDate(lastVisit.first_seen);
        if (start) used = `<div class="strong accent">${icon("hourglass")} 已使用 ${fmt.usageText(start, visitEnd(lastVisit, s))} · 自 ${fmt.dateTime(start)} 起</div>`;
      }
      const net = st.usage && st.usage.network;
      const scored = ip ? [...hist].reverse().find((v) => v.ip === ip && (v.fraud_score != null || v.ipqs_score != null)) : null;
      const checked = s && fmt.parseDate(s.checked_at);
      const address = sel ? `127.0.0.1:${sel.port}` : (s && s.address) || "127.0.0.1:10909";
      const d = disabled() ? "disabled" : "";
      if (!App.setHTML($("[data-hero]"), `<div class="row top"><div class="col grow" style="gap:8px">
          <div class="small muted">${ip && ip.includes(":") ? "蜂窝公网 IPv6（无 IPv4 出口）" : "蜂窝公网 IPv4"}</div>
          <div class="big-ip">${esc(ip || "尚未检测")}</div>${used}
          ${s && s.ip_record && ip ? ipRecordLine(s.ip_record) : ""}
          ${net && net.registered ? `<div class="row" style="gap:8px" title="${net.roaming ? "这张卡正在漫游，流量按卡所属运营商的漫游资费计费" : "这张卡在本地网络上，不是漫游流量"}"><span class="badge ${net.roaming ? "orange" : ""}">${net.roaming ? "漫游流量" : "本地流量"}</span><span class="muted">${esc(networkDetail(net))}</span></div>` : ""}
          <div class="muted sel">${esc((s && s.location) || "出口归属地：尚未获取")}</div>
          ${s && s.isp ? `<div class="small muted">${esc(s.isp)}</div>` : ""}
          ${scored ? fraudBadge(scored) : ""}
          ${s && s.public_ipv6 && s.public_ipv6 !== ip && s.running ? `<div class="small muted mono sel" title="只支持 IPv6 的网站会从这个地址访问；其余网站仍走上面的 IPv4 出口">IPv6 出口：${esc(s.public_ipv6)}</div>` : ""}
          ${s && s.ip_source ? `<div class="tiny muted">检测来源：${esc(s.ip_source)}</div>` : ""}
          ${checked ? `<div class="tiny muted">检测时间：${fmt.dateTime(checked, true)}</div>` : ""}
          ${s && s.location_error ? '<div class="small orange">归属地查询失败；IP 检测结果仍有效</div>' : ""}
        </div><button class="btn" data-a="probe" ${d}>${icon("probe")}检测出口</button></div>
        <div class="divider"></div>
        <div class="row"><span class="mono" style="font-size:14px">${icon("network")} ${esc(address)}</span><span class="grow"></span><button class="btn" data-a="copy">复制 SOCKS5 地址</button></div>
        <div class="row wrap">
          <button class="btn primary" data-a="toggle" style="min-width:150px;justify-content:center" ${d}>${icon(s && s.running ? "stop" : "play")}${s && s.running ? "停止代理" : "启动蜂窝代理"}</button>
          <button class="btn" data-a="reconnect" ${d}>${icon("cycle")}重连并尝试换 IP</button>
          <button class="btn" data-a="clean" ${d} title="换 IP 后自动查欺诈值，超过上限就继续换，最多 6 次；上限在 模块与网络 → IP 欺诈值 中设置">${icon("shield")}换干净 IP</button>
          <button class="btn" data-a="segment" ${d} title="反复开关射频，直到出口落在最近 ${avoidHours()} 小时内没用过的 /24 网段，最多 4 次">${icon("stack")}换到新网段</button>
          ${st.busy || reconnecting() ? '<div class="spinner"></div>' : ""}
        </div>
        ${st.action ? busyBanner(st.action.title, st.action.started, reconnecting() ? "模块正在重新注册网络，通常 10–60 秒" : "") : ""}
        ${s && s.network_issue ? issueBanner(s.network_issue) : ""}
        ${awaiting ? '<div class="small orange">蜂窝网络中断，代理已暂停；模块恢复后会自动重新开启。点“启动蜂窝代理”可立即重试。</div>' : ""}`)) return;
      const on = (a, fn) => { const b = $(`[data-hero] [data-a="${a}"]`); if (b) b.onclick = fn; };
      on("probe", () => act("probe"));
      on("copy", async () => { await App.copyText("socks5h://" + address); toast("已复制 socks5h://" + address, "success"); });
      on("toggle", () => act(s && s.running ? "stop" : "start"));
      on("reconnect", async () => { if (await App.confirmDialog("重连蜂窝网络？", "所选模块的代理连接将中断，短信与来电会暂时不可用；其他模块继续工作。IP 由运营商分配，不保证更换；拿到原 IP 时会自动再试一次；两次操作间隔至少 8 秒。", "断开并重连", true)) act("reconnect", true); });
      on("clean", async () => { if (await App.confirmDialog("换到干净 IP？", "每换一次就查一次新 IP 的欺诈值（填了 IPQS Key 时用 IPQS，否则用 Scamalytics），超过上限就继续换，最多 6 次，约 1–3 分钟。期间代理、短信和来电会中断。每次 IPQS 查询消耗 1 次额度。", "开始", true)) act("reconnect_clean", true); });
      on("segment", async () => { const h = avoidHours(); if (await App.confirmDialog("换到新网段？", `将反复开关射频，直到出口 IP 落在最近 ${h} 小时内没用过的 /24 网段，最多 4 次（约 1 分钟）。期间代理、短信和来电会中断。网段由运营商分配，试满 4 次仍可能换不到；可随时停止后续尝试。`, "开始", true)) act("reconnect_segment", true, h); });
    }

    function drawMetrics() {
      const s = status();
      const card = st.usage && (st.usage.cards || []).find((c) => c.current);
      const metric = (t, v) => `<div class="card metric"><div class="label">${esc(t)}</div><div class="value">${esc(v)}</div></div>`;
      const cardName = card ? (card.name || carrierLabel(card.imsi_prefix)) : "";
      App.setHTML($("[data-metrics]"), metric("模块接口", s && s.interface ? s.interface : "等待识别") + metric("活动连接", String((s && s.connections) || 0)) +
        (card ? `<div class="card metric col" style="gap:8px" title="按 SIM 卡统计的全部蜂窝流量；套餐和每月开始日在 设置 → 流量统计 中设置"><div class="row" style="gap:6px"><span class="label">${esc(cardName)}本期流量</span>${st.usage.network && st.usage.network.roaming ? '<span class="tiny strong orange">漫游</span>' : ""}</div>${usageMeter(card)}</div>`
          : metric("代理流量", fmt.bytes(((s && s.uploaded) || 0) + ((s && s.downloaded) || 0)))));
    }

    function drawResult() {
      const s = status() || {};
      const mode = s.reconnect_mode || "";
      const tried = s.reconnect_tried || [];
      const hist = s.history || [];
      let seenLine = "";
      if (["changed", "new_segment", "segment_reused", "stopped"].includes(s.phase) && s.public_ip) {
        const seen = hist.slice(0, -1).filter((v) => v.ip === s.public_ip).length;
        seenLine = `<div class="small ${seen ? "orange" : "muted"}">${seen ? `这个 IP 以前用过 ${seen} 次` : "这个 IP 以前没用过"}</div>`;
      }
      const hours = avoidHours();
      if (!App.setHTML($("[data-result]"), `<div class="row"><h3>${icon("cycle")}重连结果</h3><span class="grow"></span><label class="small muted">换网段时避开最近 <select class="field" data-hours>${[6, 12, 24, 48, 72].map((h) => `<option value="${h}" ${h === hours ? "selected" : ""}>${h} 小时</option>`).join("")}</select></label></div>
        <div class="muted">${esc(PHASE_TEXT[s.phase] || "尚未执行重连")}</div>
        ${s.reconnect_note && ["segment", "clean"].includes(mode) ? `<div class="${reconnecting() ? "orange" : "muted"}">${esc(s.reconnect_note)}</div>` : ""}
        ${reconnecting() && ["segment", "clean"].includes(mode) ? '<div><button class="btn" data-a="stopmore" title="当前这次开关射频完成后停止，保留拿到的 IP">停止后续尝试</button></div>' : ""}
        ${tried.length > 1 || (["segment", "clean"].includes(mode) && tried.length) ? `<div class="small muted mono sel">本次拿到过：${esc(tried.join(" → "))}</div>` : ""}
        ${s.previous_ip ? `<div class="small mono sel">上次出口&nbsp;&nbsp;${esc(s.previous_ip)}</div>` : ""}
        ${!reconnecting() && s.reconnect_seconds > 0 ? `<div class="small muted">用时 ${s.reconnect_seconds.toFixed(1)} 秒${(s.reconnect_attempts || 0) > 1 ? ` · 开关射频 ${s.reconnect_attempts} 次` : ""}</div>` : ""}
        ${seenLine}
        ${st.error ? `<div class="red sel">${esc(st.error)}</div>` : ""}
        ${s.error ? `<div class="orange sel">${esc(s.error)}</div>` : ""}`)) return;
      $("[data-hours]").onchange = (e) => { localStorage.setItem("djonehub.segmentAvoidHours", e.target.value); };
      const sm = $('[data-a="stopmore"]'); if (sm) sm.onclick = () => act("stop_reconnect");
    }

    function drawHistory() {
      const s = status() || {};
      const visits = s.history || [];
      const ip = s.public_ip || "";
      const prev = visits.slice(0, -1).filter((v) => v.ip === ip).length;
      const segCounts = {};
      visits.forEach((v) => { const k = segmentOf(v.ip); segCounts[k] = (segCounts[k] || 0) + 1; });
      const el = $("[data-history]");
      const scroll = el.querySelector(".history") ? el.querySelector(".history").scrollTop : 0;
      if (!App.setHTML(el, `<div class="row"><h3>${icon("history")}IP 使用历史</h3><span class="grow"></span><span class="small muted">本机保存 · 最近 500 段记录</span></div>
        ${ip && visits.length && visits[visits.length - 1].ip === ip ? `<div class="${prev ? "orange" : "muted"}">${prev ? `当前 IP 曾出现过 ${prev} 段记录` : "当前 IP 在保留记录中首次出现"}</div>` : ""}
        ${s.history_error ? `<div class="small orange">历史记录读取或保存失败：${esc(s.history_error)}</div>` : ""}
        ${!visits.length ? '<div class="muted">成功检测蜂窝出口后自动记录；连续检测到相同 IP 会合并。</div>' : `<div class="history">${visits.map((v, i) => ({ v, i })).reverse().map(({ v, i }) => {
          const start = fmt.parseDate(v.first_seen);
          const seg = segmentOf(v.ip);
          const segUses = segCounts[seg] || 1;
          const again = visits.slice(0, i).some((x) => x.ip === v.ip);
          return `<div class="item"><div class="row"><span class="mono sel" style="font-size:14px">${esc(v.ip)}</span>${i === visits.length - 1 ? '<span class="small accent">最近</span>' : ""}<span class="grow"></span>
            ${start ? `<span class="small strong accent">用了 ${fmt.usageText(start, visitEnd(v, s))}</span>` : ""}${again ? '<span class="small orange">再次出现</span>' : ""}</div>
            ${v.fraud_score != null || v.ipqs_score != null ? fraudBadge(v) : '<div class="small muted">欺诈值：查询中…</div>'}
            <div class="small ${segUses > 1 ? "orange" : "muted"}">网段 ${esc(seg)}.x${segUses > 1 ? ` · 记录中共 ${segUses} 个 IP` : ""}</div>
            <div class="small muted">${esc([v.location, v.isp].filter(Boolean).join(" · "))}</div>
            <div class="tiny muted">${fmt.dateTime(v.first_seen)} — ${fmt.dateTime(v.last_seen)}</div></div>`;
        }).join("")}</div>
        <div class="tiny muted">时间为成功检测到该 IP 的范围；代理运行时每 5 分钟自动复查一次出口，当前 IP 的用时实时累计。停止代理后记录仍保留。</div>`}`)) return;
      const list = el.querySelector(".history"); if (list) list.scrollTop = scroll;
    }

    function draw() { drawModules(); drawHero(); drawMetrics(); drawResult(); drawHistory(); }

    const links = App.linksPanel($("[data-links]"));
    const friends = App.friendsPanel($("[data-friends]"));
    let usageTick = 0;
    const live = App.livePage(host, async () => {
      if (!App.fleetMode) { try { st.legacy = await api("api/proxy"); } catch (e) { st.error = e.message; } }
      if (usageTick++ % 5 === 0) { try { st.usage = await api("api/usage"); } catch (_) {} }
      draw();
    }, 1000);
    return { destroy() { live.destroy(); links.destroy(); friends.destroy(); } };
  };

  // ---- 接入信息 (ProxyLinksPanel) ------------------------------------------
  App.linksPanel = function (el) {
    let links = [], share = null, copied = null, busy = false, msg = "";
    el.innerHTML = `<div class="card pad18 col" style="gap:12px;border-radius:18px;border:1px solid var(--line)">
      <div class="row"><h3>接入信息</h3><span class="grow"></span><span class="small muted">填进指纹浏览器的代理设置</span></div>
      <div data-rows class="col" style="gap:10px"><div class="small muted">正在读取…</div></div>
      <div class="tiny muted">代理类型选 SOCKS5，主机 127.0.0.1，端口填上面冒号后的数字，不用账号密码。换 IP 链接用 GET 或 POST 都行，不需要请求体；最多 12 秒就回复（指纹浏览器一般只等 15 秒），换得快直接返回新旧 IP，慢的先回“已开始切换”，指纹浏览器会自己检测新 IP；两次至少间隔 8 秒。</div>
      <div class="divider"></div>
      <div class="col" style="gap:8px">
        <div class="row"><span class="strong">局域网共享</span><label class="switch"><input type="checkbox" data-share disabled><span></span></label><span class="grow"></span><span data-sbusy></span></div>
        <div class="row" style="gap:8px"><input class="field" data-user placeholder="用户名" style="width:140px"><input class="field" data-pass placeholder="密码" style="width:180px"><button class="btn small" data-save>保存</button></div>
        <div class="tiny muted">打开后，同一局域网的手机、电脑可以用上面的“局域网代理”地址（SOCKS5，要填这里的用户名和密码）和“局域网换 IP”链接；这台电脑自己仍用 127.0.0.1，不用密码。</div>
        <div class="tiny orange" data-smsg></div>
      </div></div>`;
    const rowsEl = el.querySelector("[data-rows]");
    const shareEl = el.querySelector("[data-share]");
    const userEl = el.querySelector("[data-user]");
    const passEl = el.querySelector("[data-pass]");
    function row(label, value, key) {
      return `<div class="row" style="gap:10px"><span class="small muted" style="width:72px;flex:none">${esc(label)}</span><span class="mono sel" style="white-space:nowrap;overflow:hidden;text-overflow:ellipsis;min-width:0;font-size:13px">${esc(value)}</span><span class="grow"></span><button class="btn small" data-copy="${esc(key)}" data-value="${esc(value)}">${copied === key ? "已复制" : "复制"}</button></div>`;
    }
    function drawRows() {
      if (!links.length) { rowsEl.innerHTML = '<div class="small muted">正在读取…</div>'; return; }
      rowsEl.innerHTML = links.map((l) => `<div class="card col" style="gap:8px;padding:12px;background:var(--card-2)">
        ${links.length > 1 ? `<div class="row" style="gap:6px"><span class="tile" style="padding:0;border:0;background:none"><span class="dot ${l.connected ? "on" : ""}"></span></span><span class="strong">${esc(l.name || l.id)}</span>${l.public_ip ? `<span class="small muted mono">${esc(l.public_ip)}</span>` : ""}</div>` : ""}
        ${row("SOCKS5", l.socks, l.id + "socks")}${row("换 IP 链接", l.rotate_url, l.id + "rotate")}
        ${l.lan_socks && l.lan_rotate_url ? row("局域网代理", l.lan_socks, l.id + "lansocks") + row("局域网换 IP", l.lan_rotate_url, l.id + "lanrotate") : ""}</div>`).join("");
      rowsEl.querySelectorAll("[data-copy]").forEach((b) => { b.onclick = async () => { await App.copyText(b.dataset.value); copied = b.dataset.copy; drawRows(); setTimeout(() => { if (copied === b.dataset.copy) { copied = null; drawRows(); } }, 1500); }; });
    }
    function drawShare() {
      shareEl.checked = !!(share && share.enabled);
      shareEl.disabled = !share || busy;
      el.querySelector("[data-sbusy]").innerHTML = busy ? '<div class="spinner"></div>' : "";
      el.querySelector("[data-smsg]").textContent = msg;
    }
    async function saveShare(enabled) {
      busy = true; drawShare();
      try {
        share = await api("api/lan-share", { body: { enabled, username: userEl.value.trim(), password: passEl.value.trim() }, unscoped: true });
        userEl.value = share.username; passEl.value = share.password; msg = "";
        links = await api("api/proxy/links", { unscoped: true }); drawRows();
      } catch (e) { msg = e.message; }
      busy = false; drawShare();
    }
    shareEl.onchange = () => saveShare(shareEl.checked);
    el.querySelector("[data-save]").onclick = () => saveShare(!!(share && share.enabled));
    api("api/lan-share", { unscoped: true }).then((s) => { share = s; userEl.value = s.username; passEl.value = s.password; drawShare(); }).catch(() => {});
    let alive = true;
    const load = async () => { if (!alive) return; try { links = await api("api/proxy/links", { unscoped: true }); drawRows(); } catch (_) {} };
    load();
    const t = setInterval(load, 10000);
    return { destroy() { alive = false; clearInterval(t); } };
  };

  // ---- 共享给朋友 (friend accounts with traffic quotas) ------------------------
  const GB = 1073741824;
  const gbText = (n) => n >= GB ? (n / GB).toFixed(2) + " GB" : n >= 1048576 ? (n / 1048576).toFixed(1) + " MB" : Math.round(n / 1024) + " KB";
  App.friendsPanel = function (el) {
    let o = null, settings = null, copied = null, alive = true;
    function friendText(a) {
      const lines = [`${a.name} 你好，这是我给你开的代理：`, ""];
      const mods = (App.fleetMode ? App.modules : [{ id: "", name: "蜂窝代理", port: 10909 }]).filter((m) => !a.modules || !a.modules.length || a.modules.includes(m.id));
      for (const m of mods) {
        const pub = settings && settings.public_socks && settings.public_socks[m.id];
        const lan = o && o.address ? `${o.address}:${m.port + o.port_offset}` : "";
        lines.push(`【${m.name}】SOCKS5 ${pub || lan || "（地址见主人说明）"}`);
      }
      lines.push(`用户名：${a.username}`, `密码：${a.password}`);
      if (a.quota_bytes) lines.push(`流量：${gbText(a.quota_bytes)}${a.reset_day ? `，每月 ${a.reset_day} 日清零` : ""}`);
      if (a.expires) lines.push(`到期：${a.expires}`);
      const page = (settings && settings.public_page) || (o && o.page_url) || "";
      if (page) lines.push("", `查看剩余流量：${page}?user=${encodeURIComponent(a.username)}&pass=${encodeURIComponent(a.password)}`);
      return lines.join("\n");
    }
    function draw() {
      const list = (o && o.accounts) || [];
      const mods = App.fleetMode ? App.modules : [];
      App.setHTML(el, `<div class="card pad18 col" style="gap:12px;border-radius:18px;border:1px solid var(--line);margin-top:14px">
        <div class="row"><h3>共享给朋友</h3><span class="small muted">每人单独的账号和流量上限</span><span class="grow"></span><button class="btn small primary" data-add>${icon("plus")}添加朋友</button></div>
        ${!list.length ? '<div class="small muted">还没有朋友账号。添加后，朋友用自己的用户名和密码连接下面的共享端口，用多少记多少，到上限自动停。</div>' : ""}
        ${list.map((a) => {
          const pct = a.quota_bytes ? Math.min(100, a.used / a.quota_bytes * 100) : 0;
          const bad = a.status !== "正常", warn = !bad && pct >= 90;
          const color = bad ? "var(--red)" : warn ? "var(--orange)" : "var(--accent)";
          return `<div class="card col" style="gap:8px;padding:12px;background:var(--card-2)">
            <div class="row" style="gap:8px"><span class="strong">${esc(a.name)}</span><span class="small muted mono">${esc(a.username)}</span><span class="tiny" style="padding:1px 8px;border-radius:10px;background:rgba(128,128,128,0.14);color:${bad ? "var(--red)" : "inherit"}">${esc(a.status)}</span><span class="grow"></span>
              <label class="switch" title="启用"><input type="checkbox" data-on="${esc(a.id)}" ${a.enabled ? "checked" : ""}><span></span></label></div>
            ${a.quota_bytes ? `<div style="height:8px;border-radius:4px;background:var(--line)"><div style="height:8px;border-radius:4px;width:${pct.toFixed(1)}%;background:${color}"></div></div>` : ""}
            <div class="row small muted" style="gap:10px;flex-wrap:wrap"><span>已用 <b style="color:var(--text)">${gbText(a.used)}</b>${a.quota_bytes ? ` / ${gbText(a.quota_bytes)}（剩 ${gbText(Math.max(0, a.quota_bytes - a.used))}）` : "（不限量）"}</span>
              ${a.next_reset ? `<span>${new Date(a.next_reset).getMonth() + 1} 月 ${new Date(a.next_reset).getDate()} 日清零</span>` : ""}${a.expires ? `<span>到期 ${esc(a.expires)}</span>` : ""}
              ${a.modules && a.modules.length ? `<span>${esc(a.modules.map((id) => (mods.find((m) => m.id === id) || {}).name || id).join("、"))}</span>` : ""}${a.allow_rotate ? "<span>可换 IP</span>" : ""}
              ${a.last_seen ? `<span>最近使用 ${fmt.dateTime(a.last_seen)}</span>` : ""}</div>
            <div class="row" style="gap:6px"><button class="btn small" data-copy="${esc(a.id)}">${copied === a.id ? "已复制" : "复制给朋友"}</button><button class="btn small" data-edit="${esc(a.id)}">编辑</button><button class="btn small" data-reset="${esc(a.id)}">流量清零</button><span class="grow"></span><button class="btn small danger" data-del="${esc(a.id)}">删除</button></div></div>`;
        }).join("")}
        ${list.length ? `<div class="col" style="gap:6px">
          <div class="small strong">朋友怎么连</div>
          <div class="tiny muted">每个模块有一个共享端口 = 代理端口 + ${o ? o.port_offset : 10000}（${(mods.length ? mods : [{ name: "代理", port: 10909 }]).map((m) => `${esc(m.name)} ${m.port + (o ? o.port_offset : 10000)}`).join("，")}），必须用朋友账号登录，内网穿透过来也一样。局域网里直接用 ${esc((o && o.address) || "本机 IP")}:端口。用内网穿透给外面的朋友：把共享端口和流量页端口 ${o ? o.page_port : 7578} 映射出去，再把外网地址填在下面，“复制给朋友”就会用外网地址。不要把 ${mods.length ? mods.map((m) => m.port).join("、") : "10909"} 本身穿透出去，那个端口本机免密码、不计朋友流量。</div>
          ${(mods.length ? mods : [{ id: "", name: "代理" }]).map((m) => `<div class="row" style="gap:8px"><span class="small muted" style="width:120px;flex:none">${esc(m.name)} 外网地址</span><input class="field" data-pub="${esc(m.id)}" placeholder="例如 frp.example.com:6001" value="${esc((settings && settings.public_socks && settings.public_socks[m.id]) || "")}" style="flex:1"></div>`).join("")}
          <div class="row" style="gap:8px"><span class="small muted" style="width:120px;flex:none">流量页外网地址</span><input class="field" data-pubpage placeholder="例如 http://frp.example.com:6008/share" value="${esc((settings && settings.public_page) || "")}" style="flex:1"><button class="btn small" data-savepub>保存</button></div>
          <div class="tiny muted">流量按代理实际传输的数据计（上传加下载），运营商计费通常多 3%–5%，上限可以留一点余量。</div></div>` : ""}
      </div>`);
      el.querySelector("[data-add]").onclick = () => edit(null);
      el.querySelectorAll("[data-edit]").forEach((b) => { b.onclick = () => edit(list.find((a) => a.id === b.dataset.edit)); });
      el.querySelectorAll("[data-copy]").forEach((b) => { b.onclick = async () => { await App.copyText(friendText(list.find((a) => a.id === b.dataset.copy))); copied = b.dataset.copy; draw(); setTimeout(() => { copied = null; draw(); }, 1500); }; });
      el.querySelectorAll("[data-reset]").forEach((b) => { b.onclick = async () => {
        const a = list.find((x) => x.id === b.dataset.reset);
        if (!(await App.confirmDialog("把 " + a.name + " 的流量清零？", "已用流量归零，朋友可以重新用满 " + (a.quota_bytes ? gbText(a.quota_bytes) : "不限量") + "。", "清零"))) return;
        try { o = await api("api/share/reset", { body: { id: a.id }, unscoped: true }); toast("已清零", "success"); draw(); } catch (e) { toast(e.message, "failure"); }
      }; });
      el.querySelectorAll("[data-del]").forEach((b) => { b.onclick = async () => {
        const a = list.find((x) => x.id === b.dataset.del);
        if (!(await App.confirmDialog("删除 " + a.name + " 的账号？", "朋友马上不能再用这个账号，用量记录一起删除。", "删除", true))) return;
        try { o = await api("api/share/accounts?id=" + encodeURIComponent(a.id), { method: "DELETE", unscoped: true }); draw(); } catch (e) { toast(e.message, "failure"); }
      }; });
      el.querySelectorAll("[data-on]").forEach((c) => { c.onchange = async () => {
        const a = list.find((x) => x.id === c.dataset.on);
        try { o = await api("api/share/accounts", { body: Object.assign({}, a, { enabled: c.checked }), unscoped: true }); draw(); } catch (e) { toast(e.message, "failure"); draw(); }
      }; });
      const sp = el.querySelector("[data-savepub]");
      if (sp) sp.onclick = async () => {
        const pub = {}; el.querySelectorAll("[data-pub]").forEach((i) => { if (i.value.trim()) pub[i.dataset.pub] = i.value.trim(); });
        try { settings = await api("api/share/settings", { body: { public_socks: pub, public_page: el.querySelector("[data-pubpage]").value.trim() }, unscoped: true }); toast("已保存", "success"); } catch (e) { toast(e.message, "failure"); }
      };
    }
    function edit(a) {
      const mods = App.fleetMode ? App.modules : [];
      const v = a || { name: "", username: "", password: Math.random().toString(36).slice(2, 10), quota_bytes: 10 * GB, reset_day: 0, expires: "", modules: [], allow_rotate: false, enabled: true };
      const m = App.modal(`<h4>${a ? "编辑朋友账号" : "添加朋友"}</h4>
        <div class="col" style="gap:10px;margin:10px 0">
          <label class="col small" style="gap:4px">称呼<input class="field" data-f="name" value="${esc(v.name)}" placeholder="例如 小王"></label>
          <div class="row" style="gap:8px"><label class="col small grow" style="gap:4px">用户名<input class="field" data-f="username" value="${esc(v.username)}" placeholder="字母或数字"></label><label class="col small grow" style="gap:4px">密码<input class="field mono" data-f="password" value="${esc(v.password)}"></label></div>
          <div class="row" style="gap:8px"><label class="col small grow" style="gap:4px">流量上限（GB，0 = 不限）<input class="field" data-f="quota" type="number" min="0" step="0.5" value="${v.quota_bytes ? +(v.quota_bytes / GB).toFixed(2) : 0}"></label>
            <label class="col small grow" style="gap:4px">每月清零<select class="field" data-f="reset"><option value="0">不自动清零</option>${Array.from({ length: 28 }, (_, i) => `<option value="${i + 1}" ${v.reset_day === i + 1 ? "selected" : ""}>每月 ${i + 1} 日</option>`).join("")}</select></label></div>
          <label class="col small" style="gap:4px">到期日（可不填）<input class="field" data-f="expires" type="date" value="${esc(v.expires)}"></label>
          ${mods.length > 1 ? `<div class="col small" style="gap:4px">可用模块（都不勾 = 全部）<div class="row" style="gap:12px;flex-wrap:wrap">${mods.map((x) => `<label class="row" style="gap:5px"><input type="checkbox" data-mod="${esc(x.id)}" ${(v.modules || []).includes(x.id) ? "checked" : ""}>${esc(x.name)}</label>`).join("")}</div></div>` : ""}
          <label class="row small" style="gap:6px"><input type="checkbox" data-f="rotate" ${v.allow_rotate ? "checked" : ""}>允许朋友自己换 IP（会同时换掉你在这个模块上的 IP）</label>
          <div class="small red" data-err></div>
        </div>
        <div class="actions"><button class="btn" data-a="no">取消</button><button class="btn primary" data-a="ok">保存</button></div>`);
      const f = (k) => m.el.querySelector(`[data-f="${k}"]`);
      m.el.querySelector('[data-a="no"]').onclick = m.close;
      m.el.querySelector('[data-a="ok"]').onclick = async () => {
        const body = Object.assign({}, a || {}, {
          name: f("name").value.trim(), username: f("username").value.trim(), password: f("password").value.trim(),
          quota_bytes: Math.round(Math.max(0, parseFloat(f("quota").value) || 0) * GB), reset_day: +f("reset").value, expires: f("expires").value,
          modules: [...m.el.querySelectorAll("[data-mod]:checked")].map((c) => c.dataset.mod), allow_rotate: f("rotate").checked, enabled: a ? a.enabled : true,
        });
        try { o = await api("api/share/accounts", { body, unscoped: true }); m.close(); draw(); toast(a ? "已保存" : "已添加，点“复制给朋友”把连接信息发给他", "success"); }
        catch (e) { m.el.querySelector("[data-err]").textContent = e.message; }
      };
    }
    const load = async () => {
      if (!alive) return;
      try { o = await api("api/share/accounts", { unscoped: true }); if (!settings) settings = await api("api/share/settings", { unscoped: true }); draw(); } catch (_) {}
    };
    draw(); load();
    const t = setInterval(() => { if (!document.querySelector(".modal")) load(); }, 5000);
    return { destroy() { alive = false; clearInterval(t); } };
  };
})();
