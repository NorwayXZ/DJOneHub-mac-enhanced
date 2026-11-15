/* 通讯录: contacts kept by DJOneHub (Windows has no system address book). */
(function () {
  "use strict";
  const { icon, esc, api, toast } = App;

  // ---- shared contact store -------------------------------------------------
  const store = App.contacts = {
    list: [], loaded: false, listeners: new Set(),
    async load() {
      try { this.list = await api("api/contacts", { unscoped: true }) || []; this.loaded = true; } catch (_) {}
      this.listeners.forEach((fn) => fn());
      return this.list;
    },
    async save(list) {
      this.list = await api("api/contacts", { method: "PUT", body: list, unscoped: true }) || [];
      this.listeners.forEach((fn) => fn());
    },
    digits(p) { return String(p || "").replace(/[^\d+]/g, ""); },
    find(phone) {
      const d = this.digits(phone).replace(/^\+?86/, "");
      if (!d) return null;
      return this.list.find((c) => (c.phones || []).some((p) => {
        const q = this.digits(p).replace(/^\+?86/, "");
        return q && (q === d || (q.length >= 8 && d.length >= 8 && q.slice(-8) === d.slice(-8)));
      })) || null;
    },
    name(phone) { const c = this.find(phone); return c && c.name ? c.name : (phone || "未知号码"); },
  };
  store.load();

  App.avatar = function (name, size) {
    size = size || 36;
    const ch = String(name || "?").trim().charAt(0).toUpperCase() || "?";
    return `<span style="width:${size}px;height:${size}px;border-radius:50%;background:rgba(var(--accent-rgb),0.14);color:var(--accent);display:inline-flex;align-items:center;justify-content:center;font-weight:600;font-size:${Math.round(size * 0.38)}px;flex:none">${esc(ch)}</span>`;
  };

  function editContact(contact) {
    return new Promise((resolve) => {
      const c = contact || { id: "", name: "", phones: [""], note: "" };
      const m = App.modal(`<h4>${contact ? "编辑联系人" : "新建联系人"}</h4>
        <div class="col" style="gap:10px;margin-top:6px">
          <input class="field" data-name placeholder="姓名" value="${esc(c.name)}">
          <textarea class="field" data-phones rows="3" placeholder="电话号码，一行一个">${esc((c.phones || []).join("\n"))}</textarea>
          <input class="field" data-note placeholder="备注（可不填）" value="${esc(c.note || "")}">
        </div>
        <div class="actions">${contact ? '<button class="btn danger" data-a="del" style="margin-right:auto">删除</button>' : ""}<button class="btn" data-a="no">取消</button><button class="btn primary" data-a="ok">保存</button></div>`);
      m.el.querySelector("[data-name]").focus();
      m.el.querySelector('[data-a="no"]').onclick = () => { m.close(); resolve(null); };
      const del = m.el.querySelector('[data-a="del"]');
      if (del) del.onclick = () => { m.close(); resolve({ delete: true }); };
      m.el.querySelector('[data-a="ok"]').onclick = () => {
        const next = {
          id: c.id || ("c" + Date.now().toString(36) + Math.random().toString(36).slice(2, 6)),
          name: m.el.querySelector("[data-name]").value.trim(),
          phones: m.el.querySelector("[data-phones]").value.split(/\n|,|，/).map((s) => s.trim()).filter(Boolean),
          note: m.el.querySelector("[data-note]").value.trim(),
        };
        m.close(); resolve(next);
      };
    });
  }
  App.editContact = async function (contact) {
    const r = await editContact(contact);
    if (!r) return;
    let list = store.list.slice();
    if (r.delete) list = list.filter((x) => x.id !== contact.id);
    else if (contact) list = list.map((x) => (x.id === contact.id ? r : x));
    else list.push(r);
    try { await store.save(list); toast(r.delete ? "已删除联系人" : "已保存联系人", "success"); }
    catch (e) { toast("保存失败：" + e.message, "failure"); }
  };

  App.pages.contacts = function (host) {
    let search = "", selected = null;
    host.innerHTML = `<div style="display:flex;flex-direction:column;height:100vh">
      <div class="row" style="padding:10px 14px;gap:12px"><span style="font-size:17px;font-weight:600">通讯录</span><span class="small muted" data-count></span><span class="grow"></span>
        <div class="row" style="gap:6px;background:var(--field);border:1px solid var(--line-2);border-radius:9px;padding:0 9px;height:32px;width:250px">${icon("search", "muted")}<input data-search placeholder="搜索姓名或号码" style="border:0;background:none;outline:none;flex:1"></div>
        <button class="btn" data-a="new">${icon("plus")}新建</button></div>
      <div class="divider" style="margin:0"></div>
      <div style="display:flex;flex:1;min-height:0">
        <div data-list style="width:300px;flex:none;overflow-y:auto;border-right:1px solid var(--line);padding:6px 0"></div>
        <div data-detail style="flex:1;overflow-y:auto"></div>
      </div></div>`;
    const listEl = host.querySelector("[data-list]");
    const detailEl = host.querySelector("[data-detail]");
    host.querySelector("[data-search]").oninput = (e) => { search = e.target.value.trim().toLowerCase(); draw(); };
    host.querySelector('[data-a="new"]').onclick = () => App.editContact(null);

    function filtered() {
      const all = store.list.slice().sort((a, b) => (a.name || "").localeCompare(b.name || "", "zh-CN"));
      if (!search) return all;
      return all.filter((c) => (c.name || "").toLowerCase().includes(search) || (c.phones || []).some((p) => p.includes(search)));
    }
    function draw() {
      const list = filtered();
      host.querySelector("[data-count]").textContent = store.list.length ? store.list.length + " 位联系人" : "";
      if (!store.list.length) {
        listEl.innerHTML = `<div class="col muted" style="align-items:center;padding:60px 20px;gap:8px">${icon("person", "faint")}<div>通讯录为空</div><div class="small">点右上角“新建”添加联系人，短信和来电会显示名字。</div></div>`;
      } else if (!list.length) {
        listEl.innerHTML = '<div class="muted" style="padding:40px 20px;text-align:center">没有匹配的联系人</div>';
      } else {
        listEl.innerHTML = list.map((c) => `<button data-id="${esc(c.id)}" style="display:flex;align-items:center;gap:10px;width:100%;border:0;background:${selected && selected.id === c.id ? "rgba(var(--accent-rgb),0.16)" : "none"};padding:8px 14px;cursor:pointer;text-align:left">
          ${App.avatar(c.name, 34)}<span class="grow"><div class="strong">${esc(c.name || (c.phones || [])[0] || "未命名")}</div><div class="small muted">${esc((c.phones || [])[0] || "")}</div></span></button>`).join("");
        listEl.querySelectorAll("[data-id]").forEach((b) => { b.onclick = () => { selected = store.list.find((x) => x.id === b.dataset.id); draw(); }; });
      }
      if (selected && !store.list.some((x) => x.id === selected.id)) selected = null;
      if (!selected) { detailEl.innerHTML = `<div class="placeholder">选择左侧联系人</div>`; return; }
      const c = store.list.find((x) => x.id === selected.id) || selected;
      detailEl.innerHTML = `<div class="col" style="align-items:center;padding:32px 24px 12px;gap:10px">${App.avatar(c.name, 72)}<div style="font-size:18px;font-weight:600">${esc(c.name || "未命名")}</div>${c.note ? `<div class="small muted">${esc(c.note)}</div>` : ""}
        <button class="btn small" data-a="edit">${icon("pencil")}编辑</button></div>
        <div class="col" style="padding:0 24px 24px;gap:8px">${(c.phones || []).map((p) => `<div class="card row" style="padding:12px"><span class="grow"><div class="tiny muted">手机</div><div class="mono sel" style="font-size:14px">${esc(p)}</div></span>
          <button class="btn small" data-call="${esc(p)}">${icon("phone")}拨打</button><button class="btn small" data-sms="${esc(p)}">${icon("message")}短信</button></div>`).join("")}</div>`;
      detailEl.querySelector('[data-a="edit"]').onclick = () => App.editContact(c);
      detailEl.querySelectorAll("[data-call]").forEach((b) => { b.onclick = () => { App.pendingDial = b.dataset.call; App.show("dial"); }; });
      detailEl.querySelectorAll("[data-sms]").forEach((b) => { b.onclick = () => { App.pendingSMS = b.dataset.sms; App.show("messages"); }; });
    }
    const off = () => store.listeners.delete(draw);
    store.listeners.add(draw);
    draw();
    store.load();
    return { destroy: off };
  };
})();
