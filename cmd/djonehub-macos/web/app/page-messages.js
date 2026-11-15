/* 短信: MessagesView, ThreadView and ComposeMessageView of the macOS app. */
(function () {
  "use strict";
  const { icon, esc, api, toast, fmt } = App;

  function shortTime(d) {
    if (!(d instanceof Date)) d = fmt.parseDate(d);
    if (!d) return "";
    const now = new Date();
    const p = (n) => String(n).padStart(2, "0");
    if (d.toDateString() === now.toDateString()) return p(d.getHours()) + ":" + p(d.getMinutes());
    if (d.getFullYear() === now.getFullYear()) return (d.getMonth() + 1) + "/" + d.getDate() + " " + p(d.getHours()) + ":" + p(d.getMinutes());
    return String(d.getFullYear()).slice(2) + "/" + (d.getMonth() + 1) + "/" + d.getDate();
  }
  App.smsShortTime = shortTime;

  // Messages sent from this app, kept per module like the macOS session list.
  const sentKey = () => "djonehub.sent." + (App.selectedID || "single");
  const loadSent = () => { try { return JSON.parse(localStorage.getItem(sentKey()) || "[]"); } catch (_) { return []; } };
  const saveSent = (list) => { try { localStorage.setItem(sentKey(), JSON.stringify(list.slice(-500))); } catch (_) {} };

  async function sendSMS(phone, message, module) {
    const headers = {};
    if (module) headers["X-DJOneHub-Module"] = module;
    await api("api/sms/send", { body: { phone, message }, headers, timeout: 30 });
    const list = loadSent();
    list.push({ phone, content: message, sent_at: new Date().toISOString() });
    saveSent(list);
  }

  function composer(initial, onSent) {
    const m = App.modal(`<div class="col" style="gap:0;margin:-20px">
      <div class="row" style="padding:12px 16px"><button class="btn link" data-a="cancel">取消</button><span class="grow" style="text-align:center;font-weight:600">新信息</span><button class="btn link strong" data-a="send">发送</button></div>
      <div class="divider" style="margin:0"></div>
      <div class="row" style="padding:9px 16px;gap:8px"><button class="btn link" data-a="pick" title="从通讯录选择">${icon("plus")}</button><span class="muted">收件人：</span><input data-to placeholder="输入号码或姓名" style="border:0;background:none;outline:none;flex:1" value="${esc(initial || "")}"></div>
      <div data-sugg></div>
      <div class="divider" style="margin:0"></div>
      <div style="height:200px"></div>
      <div class="small red" data-err style="padding:0 16px 6px"></div>
      <div class="divider" style="margin:0"></div>
      <div class="col" style="padding:9px 16px;gap:4px"><div class="row" style="gap:8px;align-items:flex-end">
        <textarea data-body rows="1" placeholder="短信内容" class="field" style="flex:1;border-radius:16px;padding:8px 12px;max-height:110px"></textarea>
        <button class="btn primary" data-a="send2" style="border-radius:50%;width:30px;height:30px;padding:0;justify-content:center">↑</button></div>
        <div class="tiny faint">短信将通过 4G 模块发送</div></div></div>`);
    const to = m.el.querySelector("[data-to]");
    const body = m.el.querySelector("[data-body]");
    const sugg = m.el.querySelector("[data-sugg]");
    (initial ? body : to).focus();
    to.oninput = () => {
      const q = to.value.trim().toLowerCase();
      const list = q ? App.contacts.list.filter((c) => (c.name || "").toLowerCase().includes(q) || (c.phones || []).some((p) => p.includes(q))).slice(0, 5) : [];
      sugg.innerHTML = list.length ? `<div class="divider" style="margin:0"></div><div class="row" style="padding:8px 16px;gap:8px;overflow-x:auto">${list.map((c) => `<button class="btn small" data-p="${esc((c.phones || [])[0] || c.name)}">${esc(c.name)} <span class="muted">${esc((c.phones || [])[0] || "")}</span></button>`).join("")}</div>` : "";
      sugg.querySelectorAll("[data-p]").forEach((b) => { b.onclick = () => { to.value = b.dataset.p; sugg.innerHTML = ""; body.focus(); }; });
    };
    m.el.querySelector('[data-a="pick"]').onclick = () => { to.value = ""; to.focus(); to.dispatchEvent(new Event("input")); if (!App.contacts.list.length) toast("通讯录为空，可在“通讯录”页添加", "info"); };
    m.el.querySelector('[data-a="cancel"]').onclick = m.close;
    let sending = false;
    async function send() {
      const phone = to.value.trim(), text = body.value.trim();
      if (!phone || !text || sending) return;
      sending = true;
      try { await sendSMS(phone, text); m.close(); toast("短信已发送", "success"); onSent(phone); }
      catch (e) { m.el.querySelector("[data-err]").textContent = e.message; }
      sending = false;
    }
    m.el.querySelector('[data-a="send"]').onclick = send;
    m.el.querySelector('[data-a="send2"]').onclick = send;
    body.onkeydown = (e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); send(); } };
  }

  App.pages.messages = function (host) {
    let received = [], selected = null, error = "", ownNumber = "", capability = null, loading = false;
    host.innerHTML = `<div style="display:flex;flex-direction:column;height:100vh">
      <div class="row" style="padding:10px 14px;gap:12px"><span style="font-size:17px;font-weight:600">短信</span><span class="small muted mono" data-own></span><span class="grow"></span>
        <span class="small red" data-err style="white-space:nowrap;overflow:hidden;text-overflow:ellipsis;max-width:360px"></span>
        <button class="btn link" data-a="compose" title="撰写新短信" style="color:var(--text)">${icon("pencil")}</button>
        <button class="btn link" data-a="clear" title="清空全部短信" style="color:var(--text)">${icon("trash")}</button>
        <button class="btn link" data-a="reload" title="刷新" style="color:var(--text)">${icon("refresh")}</button></div>
      <div class="divider" style="margin:0"></div>
      <label class="row small" style="padding:7px 14px;gap:8px;cursor:pointer"><label class="switch"><input type="checkbox" data-cleanup><span></span></label>读取后自动清理模块短信</label>
      <div class="divider" style="margin:0"></div>
      <div data-cap></div>
      <div data-body style="flex:1;min-height:0;display:flex;flex-direction:column"></div></div>`;
    const $ = (s) => host.querySelector(s);
    const bodyEl = $("[data-body]");

    function conversations() {
      const latest = {};
      const add = (sender, content, date, module) => {
        sender = sender || "未知号码";
        if (!latest[sender] || date > latest[sender].date) latest[sender] = { content, date, module: module || (latest[sender] && latest[sender].module) };
      };
      received.forEach((m) => add(m.sender, m.content, new Date(m.timestamp), m.module_name));
      loadSent().forEach((m) => add(m.phone, m.content, new Date(m.sent_at)));
      return Object.entries(latest).map(([sender, v]) => ({ sender, ...v })).sort((a, b) => b.date - a.date);
    }
    function thread(sender) {
      const items = received.filter((m) => (m.sender || "未知号码") === sender).map((m) => ({ sent: false, content: m.content, code: m.code, date: new Date(m.timestamp), module: m.module_id }))
        .concat(loadSent().filter((m) => m.phone === sender).map((m) => ({ sent: true, content: m.content, date: new Date(m.sent_at) })));
      return items.sort((a, b) => a.date - b.date);
    }

    function draw() {
      $("[data-own]").textContent = ownNumber;
      $("[data-err]").textContent = error;
      $("[data-cap]").innerHTML = capability && capability.kind === "data_only" ? `<div style="padding:10px 12px 0"><div class="banner warn">${icon("antenna", "orange")}<div><div class="b1">当前号码：${esc(capability.title)}</div><div class="b2">${esc(capability.detail)}</div></div></div></div>` : "";
      if (selected) return drawThread();
      const list = conversations();
      if (!list.length) {
        App.setHTML(bodyEl, `<div class="col muted" style="align-items:center;justify-content:center;flex:1;gap:8px">${icon("message", "faint")}<div>暂无短信</div>${capability && capability.kind === "data_only" ? '<div class="small orange">当前号码是流量卡，不会收到短信</div>' : ""}</div>`);
        return;
      }
      if (!App.setHTML(bodyEl, `<div style="overflow-y:auto;padding:12px;display:flex;flex-direction:column;gap:8px">${list.map((c) => `<button class="card row" data-s="${esc(c.sender)}" style="border:1px solid var(--line);cursor:pointer;text-align:left;padding:10px 12px;box-shadow:0 2px 8px rgba(0,0,0,0.08)">
          ${App.avatar(App.contacts.name(c.sender), 38)}<span class="grow col" style="gap:3px"><span class="row" style="gap:6px"><span class="strong" style="font-size:14px">${esc(App.contacts.name(c.sender))}</span>${c.module ? `<span class="tiny muted" style="padding:1px 5px;border-radius:9px;background:rgba(128,128,128,0.12)">${esc(c.module)}</span>` : ""}</span>
          <span class="small muted" style="white-space:nowrap;overflow:hidden;text-overflow:ellipsis">${esc(c.content)}</span></span><span class="small muted">${shortTime(c.date)}</span></button>`).join("")}</div>`)) return;
      bodyEl.querySelectorAll("[data-s]").forEach((b) => { b.onclick = () => { selected = b.dataset.s; bodyEl._html = null; draw(); }; });
    }
    function drawThread() {
      const items = thread(selected);
      const name = App.contacts.name(selected);
      const old = bodyEl.querySelector("[data-scroll]");
      const atBottom = !old || old.scrollHeight - old.scrollTop - old.clientHeight < 40;
      const draft = bodyEl.querySelector("[data-draft]") ? bodyEl.querySelector("[data-draft]").value : "";
      const html = `<div class="row" style="padding:10px 14px;gap:10px"><button class="btn link" data-a="back" style="color:var(--text)">${icon("back")}</button>${App.avatar(name, 26)}<span class="strong">${esc(name)}</span><span class="grow"></span><span class="small muted">${items.length} 条</span></div>
        <div class="divider" style="margin:0"></div>
        <div data-scroll style="flex:1;overflow-y:auto;padding:12px;display:flex;flex-direction:column;gap:7px">${items.map((it) => `<div class="row" style="justify-content:${it.sent ? "flex-end" : "flex-start"}">
          <div class="col sel" style="gap:4px;max-width:70%;padding:8px 12px;border-radius:18px;${it.sent ? "border-bottom-right-radius:4px;background:var(--accent);color:#fff;align-items:flex-end" : "border-bottom-left-radius:4px;background:var(--card-2)"}">
            <div style="white-space:pre-wrap;word-break:break-word;font-size:14px">${esc(it.content)}</div>
            ${it.code ? `<div class="row small strong" style="gap:8px">验证码 ${esc(it.code)}<button class="btn link small" data-code="${esc(it.code)}">复制</button></div>` : ""}
            <div class="tiny" style="opacity:0.65">${shortTime(it.date)}</div></div></div>`).join("")}</div>
        <div class="divider" style="margin:0"></div>
        <div class="row" style="padding:10px;gap:8px;align-items:flex-end"><textarea data-draft rows="1" class="field" placeholder="回复短信…" style="flex:1;border-radius:12px;padding:8px 12px;max-height:110px"></textarea>
          <button class="btn primary" data-a="reply" style="border-radius:50%;width:30px;height:30px;padding:0;justify-content:center">↑</button></div>`;
      if (!App.setHTML(bodyEl, html)) return;
      const draftEl = bodyEl.querySelector("[data-draft]");
      draftEl.value = draft;
      const sc = bodyEl.querySelector("[data-scroll]");
      if (atBottom) sc.scrollTop = sc.scrollHeight; else if (old) sc.scrollTop = old.scrollTop;
      bodyEl.querySelector('[data-a="back"]').onclick = () => { selected = null; bodyEl._html = null; draw(); };
      bodyEl.querySelectorAll("[data-code]").forEach((b) => { b.onclick = async () => { await App.copyText(b.dataset.code); toast("已复制验证码 " + b.dataset.code, "success"); }; });
      const reply = async () => {
        const text = draftEl.value.trim();
        if (!text) return;
        draftEl.value = "";
        const last = items.filter((i) => !i.sent && i.module).pop();
        try { await sendSMS(selected, text, last && last.module); bodyEl._html = null; draw(); }
        catch (e) { error = e.message; draftEl.value = text; draw(); }
      };
      bodyEl.querySelector('[data-a="reply"]').onclick = reply;
      draftEl.onkeydown = (e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); reply(); } };
    }

    async function load() {
      loading = true;
      try { received = await api("api/sms") || []; error = ""; } catch (e) { error = e.message; }
      loading = false;
      draw();
    }
    $('[data-a="compose"]').onclick = () => composer("", (phone) => { selected = phone; bodyEl._html = null; draw(); });
    $('[data-a="reload"]').onclick = () => { if (!loading) api("api/sms/refresh", { body: {} }).catch(() => {}).then(load); };
    $('[data-a="clear"]').onclick = async () => {
      if (!(await App.confirmDialog("清空全部短信？", "这会删除 SIM 卡和模块存储中的全部短信，无法恢复。", "删除", true))) return;
      try { await api("api/sms/clear-module", { body: {} }); received = []; selected = null; bodyEl._html = null; await load(); toast("已清空短信", "success"); }
      catch (e) { error = e.message; draw(); }
    };
    const cleanup = $("[data-cleanup]");
    cleanup.onchange = () => api("api/sms/settings", { method: "PATCH", body: { auto_cleanup_me: cleanup.checked } }).catch((e) => toast(e.message, "failure"));
    api("api/sim/identity").then((r) => { ownNumber = (r && r.phone_number) || ""; draw(); }).catch(() => {});
    api("api/sms/status").then((r) => { cleanup.checked = !!(r && r.auto_cleanup_me); }).catch(() => {});
    if (App.pendingSMS) { const p = App.pendingSMS; App.pendingSMS = null; setTimeout(() => composer(p, (phone) => { selected = phone; bodyEl._html = null; draw(); }), 0); }
    let round = 0;
    const tick = async () => {
      if (round++ % 6 === 0) { try { const s = await api("api/status"); capability = s && s.card_capability; } catch (_) {} }
      await load();
    };
    const offContacts = () => App.contacts.listeners.delete(draw);
    App.contacts.listeners.add(draw);
    tick();
    const timer = setInterval(tick, 5000);
    return { destroy() { clearInterval(timer); offContacts(); } };
  };
})();
