/* 模块与网络: MoreView with UsagePanel, FraudScoreSettingsPanel and BandOptimizerPanel. */
(function () {
  "use strict";
  const { icon, esc, api, toast, fmt } = App;

  const looksLikeFailure = (t) => ["失败", "错误", "无法", "不可用", "超时", "未通过", "error", "Error", "请", "没"].some((w) => String(t).includes(w));
  const say = (t) => toast(t, looksLikeFailure(t) ? "failure" : "success");
  const section = (title) => `<div class="strong muted" style="font-size:14px;margin-top:6px">${esc(title)}</div>`;
  const cardWrap = (inner, attrs) => `<div class="card" style="padding:0;border:1px solid var(--line);box-shadow:0 2px 8px rgba(0,0,0,0.08)" ${attrs || ""}>${inner}</div>`;
  const infoRow = (label, value, detail) => `<div style="padding:8px 14px"><div class="row" style="gap:8px"><span class="small muted">${esc(label)}</span><span class="grow"></span><span class="small strong sel" style="text-align:right">${esc(value)}</span></div>${detail ? `<div class="tiny faint" style="text-align:right">${esc(detail)}</div>` : ""}</div>`;
  const div = '<div class="divider" style="margin:0 0 0 14px"></div>';
  const mask = (v) => !v ? null : v.length <= 8 ? v : v.slice(0, 4) + " " + "•".repeat(Math.max(4, v.length - 8)) + " " + v.slice(-4);

  // ---- 流量统计 (UsagePanel) --------------------------------------------------
  function usagePanel(el) {
    let status = null, error = "";
    async function load() { try { status = await api("api/usage"); error = ""; } catch (e) { error = e.message; } draw(); }
    function planLine(c) {
      const p = c.plan || {};
      const parts = [`每月 ${p.cycle_day || 1} 日重置`, p.quota_mb > 0 ? "套餐 " + fmt.bytes(p.quota_mb * 1048576) : "未设置套餐流量"];
      if (p.quota_mb > 0) parts.push(p.auto_stop ? "用完自动暂停代理" : "用完只提醒");
      const first = fmt.parseDate(c.first_seen);
      if (first) parts.push(`自 ${first.getFullYear()}年${first.getMonth() + 1}月${first.getDate()}日 起统计`);
      return parts.join(" · ");
    }
    function draw() {
      if (!status) { el.innerHTML = `<div class="small muted" style="padding:12px 14px">${error ? esc(error) : "正在读取流量记录…"}</div>`; return; }
      const cards = status.cards || [];
      if (!App.setHTML(el, `<div class="col" style="gap:12px;padding:12px 14px">
        ${!cards.length ? '<div class="small muted">插卡并联网后自动开始统计。每张卡按 ICCID 单独记账，换卡后再插回来会接着累计。</div>' : ""}
        ${status.blocked ? `<div class="small red">${esc(status.blocked_note)}</div>` : ""}
        ${cards.map((c, i) => `${i ? '<div class="divider"></div>' : ""}<div class="col" style="gap:8px">
          <div class="row" style="gap:6px;align-items:baseline"><span class="strong">${esc(c.name || App.carrierLabel(c.imsi_prefix))}</span><span class="tiny mono muted">···${esc(String(c.iccid).slice(-4))}</span>${c.current ? '<span class="tiny strong" style="color:#fff;background:var(--accent);padding:1px 5px;border-radius:9px">当前</span>' : ""}<span class="grow"></span>
            <button class="btn small" data-plan="${esc(c.iccid)}">套餐设置</button><button class="btn small" data-cal="${esc(c.iccid)}">校准</button></div>
          ${App.usageMeter(c)}
          <div class="tiny muted">${esc(planLine(c))}</div>
          <div class="tiny muted">浏览器代理 ${fmt.bytes(c.cycle.proxy)} · 下行 ${fmt.bytes(c.cycle.rx)} · 上行 ${fmt.bytes(c.cycle.tx)} · 累计 ${fmt.bytes(c.total)}</div>
          ${c.cycle.local > 0 ? `<div class="tiny muted">另有 ${fmt.bytes(c.cycle.local)} 局域网广播未计入：电脑在模块网卡上发的设备发现包，不出模块、不产生流量费</div>` : ""}
          ${(c.history || []).length ? `<details><summary class="small">历史周期</summary><div class="col" style="gap:4px;margin-top:4px">${c.history.map((h) => `<div class="row tiny"><span>${fmt.dateOnly(h.start)} — ${fmt.dateOnly(h.end)}</span><span class="grow"></span><span class="mono ${c.plan.quota_mb > 0 && h.used >= c.plan.quota_mb * 1048576 ? "red" : "muted"}">${fmt.bytes(h.used)}</span></div>`).join("")}</div></details>` : ""}
        </div>`).join("")}
        ${status.error ? `<div class="small orange">${esc(status.error)}</div>` : ""}
        <div class="tiny muted">统计模块网卡上的全部蜂窝流量（含 DNS、出口检测、频段测速），按 IP 层计算，已扣除 USB 网卡包头。为估算值，以运营商账单为准；只统计这台模块上的用量。</div></div>`)) return;
      el.querySelectorAll("[data-plan]").forEach((b) => { b.onclick = () => editPlan(cards.find((c) => c.iccid === b.dataset.plan)); });
      el.querySelectorAll("[data-cal]").forEach((b) => { b.onclick = () => calibrate(cards.find((c) => c.iccid === b.dataset.cal)); });
    }
    function editPlan(c) {
      const p = c.plan || {};
      const gb = p.quota_mb > 0 ? (p.quota_mb % 1024 === 0 ? String(p.quota_mb / 1024) : (p.quota_mb / 1024).toFixed(2)) : "";
      const m = App.modal(`<h4>${esc(c.name || App.carrierLabel(c.imsi_prefix))} ···${esc(String(c.iccid).slice(-4))} 套餐设置</h4>
        <div class="col" style="gap:10px;margin-top:8px">
          <label class="row"><span style="width:130px">名称</span><input class="field grow" data-name placeholder="${esc(App.carrierLabel(c.imsi_prefix))}" value="${esc(c.name || "")}"></label>
          <label class="row"><span style="width:130px">每月开始日</span><select class="field" data-day>${Array.from({ length: 31 }, (_, i) => `<option value="${i + 1}" ${i + 1 === (p.cycle_day || 1) ? "selected" : ""}>${i + 1} 日</option>`).join("")}</select></label>
          <label class="row"><span style="width:130px">每月套餐流量（GB）</span><input class="field grow" data-gb placeholder="留空或 0 表示不限" value="${esc(gb)}"></label>
          <label class="row" style="gap:8px"><label class="switch"><input type="checkbox" data-stop ${p.auto_stop ? "checked" : ""}><span></span></label>流量用完时自动暂停代理</label>
          <div class="small muted">开始日填激活那天：例如 15 日激活，就是每月 15 日到下月 15 日为一期；没有该日期的月份按当月最后一天算。80% 和用完时会发通知。</div></div>
        <div class="actions"><button class="btn" data-a="no">取消</button><button class="btn primary" data-a="ok">保存</button></div>`);
      m.el.querySelector('[data-a="no"]').onclick = m.close;
      m.el.querySelector('[data-a="ok"]').onclick = async () => {
        const g = parseFloat(m.el.querySelector("[data-gb]").value) || 0;
        const quota = g > 0 ? Math.round(g * 1024) : 0;
        m.close();
        try { status = await api("api/usage", { body: { action: "plan", iccid: c.iccid, name: m.el.querySelector("[data-name]").value.trim(), cycle_day: Number(m.el.querySelector("[data-day]").value), quota_mb: quota, auto_stop: m.el.querySelector("[data-stop]").checked && quota > 0 } }); el._html = null; draw(); toast("已保存套餐设置", "success"); }
        catch (e) { toast("保存失败：" + e.message, "failure"); }
      };
    }
    async function calibrate(c) {
      const v = await App.promptDialog("按运营商数据校准", "本期已用（MB）", "", "校准");
      if (v == null) return;
      const mb = parseFloat(v);
      if (!(mb >= 0)) return toast("请填数字（MB）", "failure");
      try { status = await api("api/usage", { body: { action: "calibrate", iccid: c.iccid, used_mb: mb } }); el._html = null; draw(); toast("已校准", "success"); }
      catch (e) { toast("校准失败：" + e.message, "failure"); }
    }
    load();
    const t = setInterval(load, 15000);
    return () => clearInterval(t);
  }
  App.usagePanel = usagePanel;

  // ---- IP 欺诈值 (FraudScoreSettingsPanel) -------------------------------------
  function fraudPanel(el) {
    let settings = null, clean = null, editing = false, busy = false, message = "";
    function draw() {
      const configured = settings && settings.configured;
      el.innerHTML = `<div class="col" style="gap:10px;padding:12px 14px">
        <div class="tiny muted">每个出口 IP 都会经宽带查询 Scamalytics 欺诈值，不花蜂窝流量。填入 IPQualityScore 的 API Key 后会自动同时查询 IPQS，留空则不查。</div>
        <div class="row" style="gap:8px"><span class="strong">IPQS API Key</span><span class="grow"></span>
          ${configured && !editing ? `<span class="small mono muted">${esc(settings.key_hint || "已填写")}</span><button class="btn small" data-a="change">更换</button><button class="btn small danger" data-a="remove" ${busy ? "disabled" : ""}>移除</button>`
            : `<input class="field" type="password" data-key placeholder="粘贴 API Key，留空即不启用" style="width:260px"><button class="btn small" data-a="save">保存</button>${editing ? '<button class="btn small" data-a="cancel">取消</button>' : ""}`}
          ${busy ? '<div class="spinner"></div>' : ""}</div>
        ${configured ? (settings.credits != null ? `<div class="tiny muted">已启用 IPQS · 账户剩余额度 ${settings.credits} 次${settings.usage != null ? ` · 已用 ${settings.usage} 次` : ""}。只查新 IP、当前 IP 每天一次和最近 10 个历史 IP。</div>` : settings.error ? `<div class="tiny orange">已填写，但暂时无法连接 IPQS：${esc(settings.error)}</div>` : "") : ""}
        ${message ? `<div class="tiny orange">${esc(message)}</div>` : ""}
        ${clean ? `<div class="divider"></div><div class="col" style="gap:6px"><span class="strong">干净 IP 标准（代理页「换干净 IP」用）</span>
          <div class="row small" style="gap:18px"><label class="row" style="gap:6px">IPQS ≤ <input class="field" type="number" min="1" max="100" step="5" data-ipqs value="${clean.max_ipqs}" style="width:70px"></label>
          <label class="row" style="gap:6px">Scamalytics ≤ <input class="field" type="number" min="1" max="100" step="5" data-scam value="${clean.max_scamalytics}" style="width:70px"></label></div>
          <div class="tiny muted">${clean.source === "ipqs" ? "已填 IPQS Key：按 IPQS 判断（75 以上 IPQS 视为可疑）。" : "没填 IPQS Key：按 Scamalytics 判断。"}</div></div>` : ""}</div>`;
      const on = (a, fn) => { const b = el.querySelector(`[data-a="${a}"]`); if (b) b.onclick = fn; };
      const save = async (key) => { busy = true; draw(); try { settings = await api("api/fraud/ipqs", { body: { api_key: key }, timeout: 25, unscoped: true }); message = ""; editing = false; } catch (e) { message = e.message; } busy = false; draw(); };
      on("change", () => { editing = true; draw(); });
      on("remove", () => save(""));
      on("cancel", () => { editing = false; draw(); });
      on("save", () => { const v = el.querySelector("[data-key]").value.trim(); if (v) save(v); });
      const k = el.querySelector("[data-key]"); if (k) k.onkeydown = (e) => { if (e.key === "Enter" && k.value.trim()) save(k.value.trim()); };
      const lim = async () => {
        const next = { max_ipqs: Number(el.querySelector("[data-ipqs]").value) || clean.max_ipqs, max_scamalytics: Number(el.querySelector("[data-scam]").value) || clean.max_scamalytics };
        try { clean = await api("api/fraud/clean", { body: next, unscoped: true }); draw(); } catch (e) { message = e.message; draw(); }
      };
      const a = el.querySelector("[data-ipqs]"), b = el.querySelector("[data-scam]");
      if (a) a.onchange = lim; if (b) b.onchange = lim;
    }
    draw();
    api("api/fraud/ipqs", { unscoped: true }).then((s) => { settings = s; draw(); }).catch(() => {});
    api("api/fraud/clean", { unscoped: true }).then((s) => { clean = s; draw(); }).catch(() => {});
  }
  App.fraudPanel = fraudPanel;

  // ---- 频段优化 (BandOptimizerPanel) ------------------------------------------
  function bandPanel(el) {
    let status = null, error = "", busy = false, saveData = false, showResults = false, dismissed = localStorage.getItem("djonehub.bandBanner") || "";
    const opName = (p) => ({ 46000: "中国移动", 46002: "中国移动", 46004: "中国移动", 46007: "中国移动", 46008: "中国移动", 46001: "中国联通", 46006: "中国联通", 46009: "中国联通", 46003: "中国电信", 46005: "中国电信", 46011: "中国电信" }[p] || p || "未知运营商");
    const sinrLabel = (s) => s >= 13 ? "好" : s >= 5 ? "一般" : "差";
    function speedGain(results, band) {
      const best = (results || []).find((r) => r.band === band && r.status === "ok");
      if (!best) return null;
      const all = (results || []).find((r) => r.band === 0 && r.status === "ok");
      if (all && all.speed_mbps > 0 && best.speed_mbps > 0) return `实测 ${best.speed_mbps.toFixed(2)} MB/s，约为全部频段（${all.speed_mbps.toFixed(2)} MB/s）的 ${(best.speed_mbps / all.speed_mbps).toFixed(1)} 倍`;
      return `信噪比 ${best.sinr.toFixed(1)} dB`;
    }
    const finishTitle = (s) => s.phase === "done" ? (s.chosen > 0 ? `频段测试完成：已锁定 Band ${s.chosen}` : "频段测试完成：保持全部频段") : s.phase === "stopped" ? "频段测试已停止" : "频段测试未完成";
    async function act(action, extra) {
      busy = true; draw();
      try { status = await api("api/band", { body: Object.assign({ action }, extra || {}), timeout: 20 }); error = ""; } catch (e) { error = e.message; }
      busy = false; draw();
    }
    function draw() {
      const s = status, p = s && s.profile, running = s && s.running, locked = (s && s.locked_band) || 0;
      const radio = s && s.radio;
      const current = radio && radio.valid ? `当前 Band ${radio.band} · ${opName(radio.plmn)}` : "当前频段：读取中…";
      const quality = radio && radio.valid ? `信号 ${radio.rsrp} dBm · 信噪比 ${radio.sinr.toFixed(1)} dB（${sinrLabel(radio.sinr)}）· 质量 ${radio.rsrq} dB` : "";
      let banner = null;
      if (s && !s.running && ["done", "stopped", "failed"].includes(s.phase)) {
        let detail = s.note;
        if (s.phase === "done" && s.chosen > 0) { const g = speedGain(s.results, s.chosen); if (g) detail = g + "。" + s.note; }
        banner = { key: s.phase + ((p && p.tested_at) || s.note), ok: s.phase === "done", title: finishTitle(s), detail };
      }
      let mode;
      if (!p) mode = "还没测试这张卡。点“自动选最优频段”找出最快的频段。";
      else if (p.fallback) mode = `Band ${p.band} 曾因信号丢失，已自动切回全部频段；打开开关可重新锁定。`;
      else if (locked > 0 && p.manual) { const rec = p.manual ? (p.best || 0) : p.band; const g = speedGain(p.results, locked); mode = "你手动锁定了这个频段" + (g ? `（${g}）` : "") + "；测试推荐的是" + (rec > 0 ? `Band ${rec}` : "全部频段") + "。关闭开关即恢复全部频段。"; }
      else if (locked > 0) mode = (speedGain(p.results, locked) || "这张卡测得的最优频段") + "。关闭开关即恢复全部频段，测试结果会保留。";
      else if (p.band > 0) { const g = speedGain(p.results, p.band); mode = `这张卡测得最优是 Band ${p.band}` + (g ? `（${g}）` : "") + "，打开开关即可锁定。"; }
      else mode = "这张卡测试后，锁定单个频段没有明显更快，保持全部频段最稳。";
      const results = (s && s.results && s.results.length) ? s.results : ((p && p.results) || []);
      const recommended = p ? (p.manual ? (p.best || 0) : p.band) : null;
      const picked = (r) => r.band === (running ? -1 : (s && s.phase === "done" && !p ? s.chosen : recommended));
      const inUse = (r) => s && s.mask_known && (r.band === 0 ? locked === 0 : locked === r.band);
      const tested = p && fmt.parseDate(p.tested_at);
      const rowHTML = (r) => `<div class="row small" style="gap:8px"><span style="width:72px;${picked(r) ? "font-weight:700" : ""}">${r.band === 0 ? "全部频段" : "Band " + r.band}</span>
        ${r.status === "testing" ? '<div class="spinner"></div><span class="muted">测试中…</span>' : r.status === "ok" ? `<span class="tiny mono muted">${r.rsrp} dBm · ${r.sinr.toFixed(1)} dB</span>${r.band === 0 && r.serving_band > 0 ? `<span class="tiny muted">(B${r.serving_band})</span>` : ""}<span class="grow"></span>${r.speed_mbps > 0 ? `<span class="tiny mono strong">${r.speed_mbps.toFixed(2)} MB/s</span>` : ""}` : `<span class="orange">${esc(r.detail || "失败")}</span>`}
        <span class="grow"></span>${picked(r) ? '<span class="tiny strong green" style="padding:1px 5px;border-radius:9px;background:color-mix(in srgb,var(--green) 12%,transparent)">推荐</span>' : ""}
        ${!running ? (inUse(r) ? `<span class="tiny strong green" style="width:70px;text-align:right">${icon("check", "green")} 使用中</span>` : (r.band === 0 || r.status === "ok") ? `<button class="btn small" style="width:70px;justify-content:center" data-lock="${r.band}" ${busy || !(s && s.mask_known) ? "disabled" : ""}>${r.band === 0 ? "改用" : "锁定"}</button>` : '<span style="width:70px"></span>') : ""}</div>`;
      if (!App.setHTML(el, `<div class="col" style="gap:12px;padding:11px 14px">
        <div class="row"><div class="col grow" style="gap:3px"><span class="strong">${esc(current)}</span><span class="tiny ${radio && radio.valid && radio.sinr < 3 ? "orange" : "muted"}">${esc(quality)}</span></div>
          ${running ? '<div class="spinner"></div><button class="btn small" data-a="stop">停止测试</button>' : `<button class="btn primary small" data-a="optimize" ${!s || busy ? "disabled" : ""}>${p ? "重新测试" : "自动选最优频段"}</button>`}</div>
        ${banner && banner.key !== dismissed ? `<div class="banner ${banner.ok ? "" : "warn"}" style="${banner.ok ? "background:color-mix(in srgb,var(--green) 12%,transparent)" : ""}">${icon(banner.ok ? "check" : "warn", banner.ok ? "green" : "orange")}<div class="grow"><div class="b1">${esc(banner.title)}</div><div class="b2">${esc(banner.detail)}</div></div><button class="btn link small" data-a="dismiss">知道了</button></div>` : ""}
        <div class="row" style="padding:10px;border-radius:8px;background:rgba(128,128,128,0.06)"><div class="col grow" style="gap:3px"><span class="strong ${locked > 0 ? "green" : ""}">${!(s && s.mask_known) ? "正在读取模块频段设置…" : locked > 0 ? `已锁定 Band ${locked}` : "使用全部频段（模块自动选择）"}</span><span class="tiny ${p && p.fallback ? "orange" : "muted"}">${esc(mode)}</span></div>
          ${busy && !running ? '<div class="spinner"></div>' : ""}<label class="switch" title="锁定频段"><input type="checkbox" data-lockt ${locked > 0 ? "checked" : ""} ${running || busy || !(s && s.mask_known) || (locked === 0 && !(p && p.band)) ? "disabled" : ""}><span></span></label></div>
        ${running && s.note ? `<div class="orange">${esc(s.note)}</div>` : ""}
        ${results.length ? `<details ${running || showResults ? "open" : ""} data-res><summary class="small strong">${running ? "测试进度" : "上次测试结果" + (tested ? `（${fmt.dateTime(tested)}）` : "")}</summary><div class="col" style="gap:4px;margin-top:6px">${results.map(rowHTML).join("")}
          ${!running ? '<div class="tiny muted">“推荐”是测试按速度选的；觉得实际体验不好，可以点任意一行的“锁定”改用那个频段，或点“全部频段”那行恢复自动选择。切换时代理会断开几秒，出口 IP 可能变化。</div>' : ""}</div></details>` : ""}
        <label class="row" style="gap:8px"><label class="switch"><input type="checkbox" data-save ${saveData ? "checked" : ""} ${running ? "disabled" : ""}><span></span></label><span class="col" style="gap:1px"><span class="small">省流量测试：只看信号质量，不测速</span><span class="tiny muted">${s && s.roaming ? "默认关闭：按实测速度选频段；漫游卡每个频段约用 10~20 MB 漫游流量" : "默认关闭：按实测速度选频段，每个频段约用 10~20 MB"}</span></span></label>
        ${s && s.error ? `<div class="small red">${esc(s.error)}</div>` : ""}${error ? `<div class="small red">${esc(error)}</div>` : ""}
        <div class="tiny muted">结果按 SIM 卡保存，换卡后自动应用；锁定后若 30 秒无信号会自动切回全部频段。测试期间代理、短信和来电会中断，出口 IP 会变化。</div></div>`)) return;
      const on = (a, fn) => { const b = el.querySelector(`[data-a="${a}"]`); if (b) b.onclick = fn; };
      on("stop", () => act("stop"));
      on("dismiss", () => { dismissed = banner.key; localStorage.setItem("djonehub.bandBanner", dismissed); el._html = null; draw(); });
      on("optimize", async () => { if (await App.confirmDialog(p ? "重新测试频段？" : "自动选最优频段？", "将按当前运营商逐个锁定频段测试（约 2~5 分钟），期间代理、短信和来电会中断，出口 IP 会变化。" + (saveData ? "省流量模式只比较信号质量。" : "每个频段会测速约 5 秒。") + "完成后会通知你。", "开始")) act("optimize", { save_data: saveData }); });
      const lt = el.querySelector("[data-lockt]"); if (lt) lt.onchange = () => act(lt.checked ? "lock" : "unlock");
      const sv = el.querySelector("[data-save]"); if (sv) sv.onchange = () => { saveData = sv.checked; };
      const det = el.querySelector("[data-res]"); if (det) det.ontoggle = () => { showResults = det.open; };
      el.querySelectorAll("[data-lock]").forEach((b) => { b.onclick = () => { const band = Number(b.dataset.lock); act(band === 0 ? "unlock" : "lock", band === 0 ? {} : { band }); }; });
    }
    const load = async () => { try { status = await api("api/band"); } catch (_) {} draw(); };
    load();
    const t = setInterval(load, 4000);
    return () => clearInterval(t);
  }

  // ---- page -----------------------------------------------------------------
  App.pages.module = function (host) {
    const st = { platform: null, policy: null, esim: null, health: null, notes: {}, probe: null, probeBusy: false, diag: null, diagOpen: false, diagBusy: false, switching: null, switchResult: null, gps: null };
    host.innerHTML = `<div class="page" style="gap:10px;max-width:900px">
      ${section("网络")}${cardWrap('<div data-net></div>')}
      ${section("流量统计")}${cardWrap('<div data-usage></div>')}
      ${section("IP 欺诈值")}${cardWrap('<div data-fraud></div>')}
      ${section("频段优化")}${cardWrap('<div data-band></div>')}
      ${section("eSIM / 卡片")}${cardWrap('<div data-esim></div>')}
      ${section("AT 调试")}${cardWrap(`<div class="row" style="padding:10px 14px;gap:8px"><input class="field grow mono" data-at value="AT+CSQ" placeholder="AT 指令"><button class="btn primary" data-a="at">发送</button></div><pre data-atout class="small mono muted sel" style="margin:0;padding:0 14px;white-space:pre-wrap"></pre>`)}
    </div>`;
    const $ = (s) => host.querySelector(s);
    const stopUsage = usagePanel($("[data-usage]"));
    fraudPanel($("[data-fraud]"));
    const stopBand = bandPanel($("[data-band]"));

    // AT
    const atIn = $("[data-at]"), atOut = $("[data-atout]"), atBtn = $('[data-a="at"]');
    async function runAT() {
      const cmd = atIn.value.trim(); if (!cmd) return;
      atBtn.disabled = true; atBtn.innerHTML = '<div class="spinner"></div>等待回复…';
      try { const r = await api("api/at", { body: { command: cmd }, timeout: 25 }); atOut.textContent = r.response; }
      catch (e) { atOut.textContent = e.message; }
      atOut.style.padding = "10px 14px";
      atBtn.disabled = false; atBtn.textContent = "发送";
    }
    atBtn.onclick = runAT;
    atIn.onkeydown = (e) => { if (e.key === "Enter") runAT(); };

    // 网络
    function drawNet() {
      const native = st.platform && st.platform.network_policy_native;
      const gps = st.gps;
      const fix = gps && gps.last_fix;
      const d = st.diag;
      let diagHTML = "";
      if (st.diagOpen) {
        if (d) {
          const route = d.default_route ? [d.default_route.interface, d.default_route.gateway].filter(Boolean).join(" -> ") : "";
          diagHTML = div + infoRow("USB 网卡", d.usb_network_present ? "已识别" : "未识别") + (route ? div + infoRow("默认出口", route) : "") + div + infoRow("usbnet", d.usbnet_mode || "--") +
            div + infoRow("蜂窝数据", (d.active_contexts || []).length ? "已激活 " + d.active_contexts.join(", ") : "未激活") + div + infoRow("蜂窝 IP", (d.pdp_addresses || []).join(" · ") || "无") +
            div + infoRow("APN", (d.pdp_contexts || []).map((c) => [c.id, c.apn].filter((x) => x != null && x !== "").join(":")).join(" · ") || "无") +
            (d.usb_device ? div + infoRow("USB 枚举", [d.usb_device.vendor, d.usb_device.product, d.usb_device.mode].filter(Boolean).join(" ") || "--", [d.usb_device.vendor_id, d.usb_device.product_id].filter(Boolean).join(":")) : "") +
            ((d.mac_interfaces || []).length ? div + d.mac_interfaces.map((i) => `<div class="row" style="padding:6px 14px;gap:8px"><span class="small strong">${esc(i.name || "--")}</span><span class="tiny muted">${esc([i.kind, i.ipv4].filter(Boolean).join(" · "))}</span><span class="grow"></span><span class="tiny strong ${i.status === "active" ? "green" : "muted"}">${i.status === "active" ? "active" : "inactive"}</span></div>`).join("") : "") +
            (d.errors && Object.keys(d.errors).length ? div + `<div class="tiny muted" style="padding:6px 14px">读取部分诊断失败：${esc(Object.values(d.errors).join("；"))}</div>` : "");
        } else if (st.diagBusy) diagHTML = div + '<div class="row small muted" style="padding:10px 14px;gap:8px"><div class="spinner"></div>正在读取网络诊断…</div>';
      }
      $("[data-net]").innerHTML = `${native ? `<div class="row" style="padding:10px 14px;gap:10px"><span class="col grow" style="gap:2px"><span class="strong">允许 4G 上网</span><span class="tiny muted">关闭后强制禁止电脑使用 4G；短信和来电监控不受影响</span></span><label class="switch"><input type="checkbox" data-policy ${st.policy && st.policy.force_off ? "" : "checked"}><span></span></label></div>${div}` : ""}
        <div class="row" style="padding:10px 14px;gap:10px">${icon("map", "accent")}<span class="col grow" style="gap:2px"><span class="strong">GPS 定位</span><span class="tiny muted">${gps ? (gps.enabled ? (fix && fix.latitude ? `${esc(fix.latitude)}, ${esc(fix.longitude)} · 卫星 ${esc(fix.satellites)} · HDOP ${esc(fix.hdop)}` : "已开启，等待定位（室外更快）") : "未开启") : "读取中…"}</span></span>
          ${fix && fix.latitude ? `<a class="btn small" target="_blank" href="https://www.openstreetmap.org/?mlat=${encodeURIComponent(fix.latitude)}&mlon=${encodeURIComponent(fix.longitude)}#map=15/${encodeURIComponent(fix.latitude)}/${encodeURIComponent(fix.longitude)}">地图</a>` : ""}
          ${gps && gps.enabled ? '<button class="btn small" data-a="gpsrefresh">刷新</button><button class="btn small" data-a="gpsstop">关闭</button>' : '<button class="btn small" data-a="gpsstart">开启</button>'}</div>${div}
        <div class="row" style="padding:10px 14px;gap:8px"><button class="btn" data-a="chk4g">检查 4G 出口</button><button class="btn" data-a="chkproxy">检查代理出口</button><button class="btn danger" data-a="reboot">重启模块</button></div>${div}
        <button class="row" data-a="diag" style="width:100%;border:0;background:none;padding:11px 14px;gap:10px;cursor:pointer;text-align:left">${icon("network", "accent")}<span class="small strong grow">网络端口诊断</span>${st.diagBusy ? '<div class="spinner"></div>' : ""}<span class="faint">${st.diagOpen ? "▴" : "▾"}</span></button>${diagHTML}`;
      const on = (a, fn) => { const b = $(`[data-net] [data-a="${a}"]`); if (b) b.onclick = fn; };
      const check = async (path) => { try { const r = await api(path, { body: {}, timeout: 30 }); let t = r.summary || (r.ok ? "检查通过" : "检查未通过"); if (r.detail) t += "（" + r.detail + "）"; say(t); } catch (e) { say(e.message); } };
      on("chk4g", () => check("api/network/check-4g"));
      on("chkproxy", () => check("api/network/check-proxy"));
      on("reboot", async () => { if (!(await App.confirmDialog("重启模块？", "模块会断开约 20 秒，期间代理、短信和来电不可用。", "重启", true))) return; toast("正在重启模块…", "progress"); try { await api("api/network/reboot-module", { body: {} }); say("已发送模块重启指令"); } catch (e) { say(e.message); } });
      on("diag", async () => { st.diagOpen = !st.diagOpen; drawNet(); if (st.diagOpen && !st.diag) { st.diagBusy = true; drawNet(); try { st.diag = await api("api/network", { timeout: 20 }); } catch (e) { say(e.message); } st.diagBusy = false; drawNet(); } });
      const gpsCall = async (path) => { try { await api(path, { body: {}, timeout: 20 }); st.gps = await api("api/gps"); drawNet(); } catch (e) { say(e.message); } };
      on("gpsstart", () => gpsCall("api/gps/start")); on("gpsstop", () => gpsCall("api/gps/stop")); on("gpsrefresh", () => gpsCall("api/gps/refresh"));
      const pol = $("[data-policy]"); if (pol) pol.onchange = async () => { try { st.policy = await api("api/network/cellular-policy", { body: { force_off: !pol.checked } }); say(pol.checked ? "已允许 4G 上网" : "已禁止 4G 上网"); } catch (e) { say(e.message); } };
    }

    // eSIM
    const profiles = () => ((st.esim && st.esim.profiles) || []).flatMap((g) => g.profiles || []);
    const capFor = (iccid) => { if (!iccid || !st.esim || !st.esim.capabilities) return null; const k = /F$/i.test(iccid) ? iccid.slice(0, -1) : iccid; return st.esim.capabilities[k] || st.esim.capabilities[iccid] || null; };
    const pname = (p) => p.name || p.service_provider_name || p.iccid || "未命名 Profile";
    function drawESIM() {
      const e = st.esim, h = st.health;
      const chip = e && e.chip_info;
      let top = "";
      if (st.switching) top = `<div style="padding:10px 10px 0">${App.busyBanner("正在切换到 " + pname(st.switching.profile), st.switching.started, st.switching.stage)}</div>`;
      else if (st.switchResult) top = `<div style="padding:10px 10px 0"><div class="banner ${st.switchResult.ok ? "" : "warn"}">${icon(st.switchResult.ok ? "check" : "warn", st.switchResult.ok ? "green" : "orange")}<div class="grow b2" style="color:var(--text)">${esc(st.switchResult.text)}</div><button class="btn link" data-a="closeresult">${icon("x")}</button></div></div>`;
      if (h && h.network_issue) top += `<div style="padding:10px 10px 0">${App.issueBanner(h.network_issue)}</div>`;
      if (h && h.capability) top += `<div style="padding:10px 10px 0"><div class="banner ${h.capability.kind === "data_only" ? "warn" : ""}">${icon(h.capability.kind === "data_only" ? "antenna" : "message", h.capability.kind === "data_only" ? "orange" : "green")}<div><div class="b1">当前号码：${esc(h.capability.title)}</div><div class="b2">${esc(h.capability.detail)}</div></div></div></div>`;
      if (e && e.offline) top += `<div style="padding:10px 10px 0"><div class="banner warn">${icon("warn", "orange")}<div class="grow"><div class="b1">${esc(e.offline)}</div><div class="b2">正在自动重试，恢复后立即显示最新信息。</div></div><div class="spinner"></div></div></div>`;
      else if (!e) top += `<div style="padding:10px 10px 0">${App.busyBanner("正在读取卡片信息", st.esimStarted || Date.now(), "第一次读卡要逐个读出卡里的号码，约 10–60 秒；读过之后再打开会立即显示。")}</div>`;
      else if (e.refreshing) top += '<div class="row small muted" style="padding:10px 14px 0;gap:8px"><div class="spinner"></div>先显示上次读到的信息，正在重新读卡核对…</div>';
      const cardType = e ? (e.message || (chip && chip.sku_name) || "查询中…") : "查询中…";
      let rows = infoRow("卡片类型", cardType);
      if (chip && chip.firmware) rows += div + infoRow("固件", chip.firmware);
      if (chip && chip.serial_number) rows += div + infoRow("序列号", chip.serial_number);
      ((chip && chip.eids) || []).forEach((x) => { const parts = [x.aid && "AID " + x.aid, x.free_nvram && "可用 " + x.free_nvram, x.firmware && "固件 " + x.firmware].filter(Boolean); rows += div + infoRow("EID", x.eid || "--", parts.join(" · ")); });
      if (h) {
        if (h.message && !h.active_profile) rows += div + `<div class="small muted" style="padding:8px 14px">${esc(h.message)}</div>`;
        else if (h.active_profile || h.module_iccid) {
          rows += div + infoRow("当前启用", h.active_profile ? pname(h.active_profile) : "--") + div + infoRow("模块实际卡", mask(h.module_iccid) || "--", h.imsi ? "IMSI " + h.imsi : "") +
            div + infoRow("蜂窝注册", [h.operator, h.network_mode, h.registration].filter(Boolean).join(" · ") || "--", h.registered ? "" : "等待网络注册") +
            div + infoRow("信号", h.signal_dbm != null ? h.signal_dbm + " dBm" : "--", h.registered ? "模块已接管当前 Profile" : "");
        }
      }
      profiles().forEach((p) => {
        const cap = capFor(p.iccid);
        const enabled = p.state === 1;
        rows += div + `<div class="col" style="padding:6px 14px;gap:6px"><div class="row" style="gap:8px"><span class="col grow" style="gap:1px"><span class="small strong">${esc(pname(p))}</span><span class="tiny muted mono">${esc(p.iccid || "")}</span></span>
          ${cap ? `<span class="tiny strong" style="padding:2px 6px;border-radius:9px;color:var(--${cap.kind === "data_only" ? "orange" : "green"});background:color-mix(in srgb,var(--${cap.kind === "data_only" ? "orange" : "green"}) 14%,transparent)" title="${esc(cap.detail)}">${cap.kind === "data_only" ? "仅流量" : "可收短信"}</span>` : ""}
          ${enabled ? '<span class="tiny strong accent">使用中</span>' : ""}</div>
          <div class="row" style="gap:8px">${st.switching && st.switching.profile.iccid === p.iccid ? '<div class="spinner"></div><span class="small muted">切换中…</span>' : !enabled ? `<button class="btn small" data-sw="${esc(p.iccid)}" ${st.switching || (e && e.offline) ? "disabled" : ""}>切换</button>` : ""}
            <button class="btn small" data-rn="${esc(p.iccid)}">重命名</button><button class="btn small" data-note="${esc(p.iccid)}">模块资料</button><button class="btn small danger" data-del="${esc(p.iccid)}" ${enabled ? "disabled" : ""}>删除</button></div></div>`;
      });
      const pr = st.probe;
      const probeRow = (label, ok, a, b) => `<div class="row tiny" style="gap:6px">${icon(ok ? "check" : "ring", ok ? "green" : "muted")}<span class="muted">${label}</span><span class="grow"></span><span>${ok ? a : b}</span></div>`;
      rows += div + `<div class="col" style="padding:10px 14px;gap:6px"><div class="row" style="gap:8px"><button class="btn" data-a="probe">通讯录检测</button><button class="btn" data-a="download">下载新 Profile</button></div>
        ${st.probeBusy ? '<div class="small muted">正在检测卡内通讯录能力，不会写入联系人…</div>' : ""}
        ${pr ? `<div class="col" style="gap:4px;margin-top:4px">${probeRow("SIM 通讯录", pr.storage_supported, "支持 SM 卡内存储", "未发现 SM 卡内存储")}${probeRow("当前卡片", pr.storage_selected, "已安全选中 SM 存储", "无法选中 SM 存储")}${probeRow("读取能力", pr.read_supported, "模块支持读取卡内联系人", "模块未确认读取命令")}${probeRow("写入接口", pr.write_supported, "模块声明支持写入接口", "模块未确认写入命令")}</div>` : ""}</div>`;
      if (!App.setHTML($("[data-esim]"), top + rows)) return;
      const el = $("[data-esim]");
      const find = (iccid) => profiles().find((p) => p.iccid === iccid);
      el.querySelectorAll("[data-sw]").forEach((b) => { b.onclick = () => switchProfile(find(b.dataset.sw)); });
      el.querySelectorAll("[data-rn]").forEach((b) => { b.onclick = async () => { const p = find(b.dataset.rn); const name = await App.promptDialog("重命名 Profile", "新名称", p.name || ""); if (name == null) return; toast("正在修改名称…", "progress"); try { await api("api/esim/profile", { method: "PATCH", body: { iccid: p.iccid, name }, timeout: 60 }); say("Profile 名称已修改"); loadESIM(); } catch (x) { say(x.message); } }; });
      el.querySelectorAll("[data-note]").forEach((b) => { b.onclick = () => editNote(find(b.dataset.note)); });
      el.querySelectorAll("[data-del]").forEach((b) => { b.onclick = async () => { const p = find(b.dataset.del); if (!(await App.confirmDialog("确认删除 Profile？", "删除不可恢复，确定删除当前 Profile 吗？", "删除", true))) return; toast("正在删除 " + pname(p) + "…", "progress"); try { await api("api/esim/profile", { method: "DELETE", body: { iccid: p.iccid }, timeout: 90 }); say("Profile 已删除"); loadESIM(); } catch (x) { say(x.message); } }; });
      const on = (a, fn) => { const b = el.querySelector(`[data-a="${a}"]`); if (b) b.onclick = fn; };
      on("closeresult", () => { st.switchResult = null; drawESIM(); });
      on("probe", async () => { st.probeBusy = true; drawESIM(); try { st.probe = await api("api/esim/phonebook/probe", { body: {}, timeout: 30 }); } catch (x) { say(x.message); } st.probeBusy = false; drawESIM(); });
      on("download", downloadSheet);
    }
    function editNote(p) {
      const n = st.notes[p.iccid] || {};
      const m = App.modal(`<h4>模块资料 · ${esc(pname(p))}</h4><div class="col" style="gap:10px;margin-top:8px">
        <input class="field" data-label placeholder="标签（例如：工作号）" value="${esc(n.label || "")}"><input class="field" data-phone placeholder="号码" value="${esc(n.phone || "")}"><input class="field" data-tags placeholder="备注" value="${esc(n.tags || "")}"></div>
        <div class="actions"><button class="btn" data-a="no">取消</button><button class="btn primary" data-a="ok">保存</button></div>`);
      m.el.querySelector('[data-a="no"]').onclick = m.close;
      m.el.querySelector('[data-a="ok"]').onclick = async () => {
        const body = { iccid: p.iccid, label: m.el.querySelector("[data-label]").value.trim(), phone: m.el.querySelector("[data-phone]").value.trim(), tags: m.el.querySelector("[data-tags]").value.trim() };
        m.close();
        try { await api("api/esim/notes", { method: "PUT", body }); say("模块资料已保存"); const r = await api("api/esim/notes"); st.notes = (r && r.notes) || {}; } catch (x) { say(x.message); }
      };
    }
    function downloadSheet() {
      const m = App.modal(`<h4>下载新 Profile</h4><div class="col" style="gap:10px;margin-top:8px">
        <input class="field" data-code placeholder="激活码（LPA:1$SM-DP+地址$匹配 ID），粘贴后自动拆分">
        <input class="field" data-smdp placeholder="SM-DP+ 地址"><input class="field" data-mid placeholder="匹配 ID（Matching ID）"><input class="field" data-cc placeholder="确认码（没有可不填）">
        <div class="small muted">下载约 1–2 分钟，期间不要拔模块。下载后需要手动切换才会启用。</div><div class="small red" data-err></div></div>
        <div class="actions"><button class="btn" data-a="no">取消</button><button class="btn primary" data-a="ok">下载</button></div>`, { wide: true, sticky: true });
      const code = m.el.querySelector("[data-code]");
      code.oninput = () => { const v = code.value.trim().replace(/^LPA:/i, ""); const parts = v.split("$"); if (parts.length >= 3) { m.el.querySelector("[data-smdp]").value = parts[1]; m.el.querySelector("[data-mid]").value = parts[2]; } };
      m.el.querySelector('[data-a="no"]').onclick = m.close;
      const ok = m.el.querySelector('[data-a="ok"]');
      ok.onclick = async () => {
        const smdp = m.el.querySelector("[data-smdp]").value.trim(), mid = m.el.querySelector("[data-mid]").value.trim();
        if (!smdp) { m.el.querySelector("[data-err]").textContent = "请填写 SM-DP+ 地址"; return; }
        ok.disabled = true; ok.innerHTML = '<div class="spinner"></div>下载中…';
        try {
          const r = await api("api/esim/download", { body: { smdp, matching_id: mid, confirmation_code: m.el.querySelector("[data-cc]").value.trim(), imei: "", aid: "" }, timeout: 240 });
          m.close(); st.switchResult = { text: (r && r.message) || "Profile 下载完成", ok: true }; loadESIM();
        } catch (x) { m.el.querySelector("[data-err]").textContent = x.message; ok.disabled = false; ok.textContent = "下载"; }
      };
    }
    const sameICCID = (a, b) => { const t = (s) => String(s || "").replace(/[Ff ]+$/, ""); return t(a) && t(a) === t(b); };
    async function switchProfile(p) {
      st.switching = { profile: p, started: Date.now(), stage: "正在给卡发送切换指令（约 10–60 秒）" };
      st.switchResult = null;
      toast("正在切换到 " + pname(p) + "…", "progress");
      drawESIM();
      const tick = setInterval(drawESIM, 1000);
      try {
        try { await api("api/esim/switch", { body: { iccid: p.iccid }, timeout: 150 }); }
        catch (x) { if (!/超时/.test(x.message)) { st.switchResult = { text: "切换失败：" + x.message, ok: false }; return; } }
        st.switching.stage = "卡已切换，模块正在重启并注册新号码（约 1–3 分钟）";
        const deadline = Date.now() + 300000;
        let onCard = false;
        while (Date.now() < deadline) {
          await new Promise((r) => setTimeout(r, 5000));
          let h; try { h = await api("api/esim/health", { timeout: 20 }); } catch (_) { continue; }
          if (sameICCID(h.module_iccid, p.iccid)) {
            if (!onCard) { onCard = true; st.switching.stage = "模块已读到新号码，正在注册网络"; }
            if (h.registered) {
              st.health = h;
              const secs = Math.round((Date.now() - st.switching.started) / 1000);
              const net = [h.operator, h.registration].filter(Boolean).join(" · ");
              st.switchResult = { text: "已切换到 " + pname(p) + (net ? "，" + net : "") + `，用时 ${secs} 秒。代理会自动恢复。`, ok: true };
              return;
            }
          }
        }
        try { st.health = await api("api/esim/health", { timeout: 20 }); } catch (_) {}
        const issue = st.health && st.health.network_issue ? "：" + st.health.network_issue.title + "。" + st.health.network_issue.detail : null;
        st.switchResult = { text: onCard ? "已切换到 " + pname(p) + (issue || "，但 5 分钟内没注册上网络：可能没开通、已停机或当地没有信号。") : "5 分钟内没读到新号码。可以点「重启模块」再看看。", ok: false };
      } finally {
        clearInterval(tick);
        if (st.switchResult) toast(st.switchResult.text, st.switchResult.ok ? "success" : "failure");
        st.switching = null; loadESIM();
      }
    }
    let alive = true;
    async function loadESIM() {
      st.esimStarted = Date.now();
      for (let tries = 0; alive && tries < 200; tries++) {
        try { st.esim = await api("api/esim", { timeout: 60 }); } catch (_) {}
        drawESIM();
        if (st.esim && !st.esim.offline && !st.esim.refreshing) return;
        await new Promise((r) => setTimeout(r, 3000));
      }
    }

    drawNet(); drawESIM();
    Promise.all([
      api("api/platform", { unscoped: true }).then((p) => { st.platform = p; }).catch(() => {}),
      api("api/network/cellular-policy").then((p) => { st.policy = p; }).catch(() => {}),
      api("api/gps").then((g) => { st.gps = g; }).catch(() => {}),
      api("api/esim/health", { timeout: 20 }).then((h) => { st.health = h; }).catch(() => {}),
      api("api/esim/notes").then((r) => { st.notes = (r && r.notes) || {}; }).catch(() => {}),
    ]).then(() => { drawNet(); drawESIM(); });
    loadESIM();
    const timer = setInterval(async () => {
      try { st.gps = await api("api/gps"); } catch (_) {}
      if (!st.switching) { try { st.health = await api("api/esim/health", { timeout: 20 }); } catch (_) {} }
      if (!st.diagOpen) drawNet();
      drawESIM();
    }, 5000);
    return { destroy() { alive = false; clearInterval(timer); stopUsage(); stopBand(); } };
  };
})();
