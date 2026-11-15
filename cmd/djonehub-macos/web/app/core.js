/* DJOneHub app core: API access scoped to the selected module, the sidebar,
   the module switcher, toasts and dialogs. Pages register in App.pages. */
(function () {
  "use strict";

  const ICONS = {
    antenna: '<path d="M12 12v9"/><circle cx="12" cy="10" r="2"/><path d="M8.5 6.5a5 5 0 0 0 0 7M15.5 6.5a5 5 0 0 1 0 7M5.6 3.6a9 9 0 0 0 0 12.8M18.4 3.6a9 9 0 0 1 0 12.8"/>',
    grid: '<circle cx="6" cy="6" r="1.6" fill="currentColor"/><circle cx="12" cy="6" r="1.6" fill="currentColor"/><circle cx="18" cy="6" r="1.6" fill="currentColor"/><circle cx="6" cy="12" r="1.6" fill="currentColor"/><circle cx="12" cy="12" r="1.6" fill="currentColor"/><circle cx="18" cy="12" r="1.6" fill="currentColor"/><circle cx="6" cy="18" r="1.6" fill="currentColor"/><circle cx="12" cy="18" r="1.6" fill="currentColor"/><circle cx="18" cy="18" r="1.6" fill="currentColor"/>',
    clock: '<circle cx="12" cy="12" r="9" fill="currentColor" stroke="none"/><path d="M12 7v5l3 2" stroke="var(--bg)"/>',
    message: '<path d="M4 5h16a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H9l-5 4v-4H4a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1z" fill="currentColor" stroke="none"/>',
    person: '<circle cx="12" cy="12" r="9.5" fill="currentColor" stroke="none"/><circle cx="12" cy="10" r="3.2" fill="var(--bg)" stroke="none"/><path d="M6.5 18.3a6.5 6.5 0 0 1 11 0" stroke="var(--bg)" stroke-width="2.2"/>',
    drive: '<rect x="3" y="4" width="18" height="9" rx="2"/><path d="M12 13v4M5 20h14M17 8.5h.01"/>',
    download: '<rect x="3" y="3" width="18" height="18" rx="4"/><path d="M12 7v8M8.5 11.5 12 15l3.5-3.5"/>',
    gear: '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/>',
    tray: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 9h18"/>',
    stethoscope: '<path d="M6 3v6a4 4 0 0 0 8 0V3M10 13v2a5 5 0 0 0 10 0v-2"/><circle cx="20" cy="11" r="2"/>',
    dot: '<circle cx="12" cy="12" r="6" fill="currentColor" stroke="none"/>',
    ring: '<circle cx="12" cy="12" r="6"/>',
    history: '<path d="M3 12a9 9 0 1 0 3-6.7L3 8"/><path d="M3 3v5h5M12 7v5l3 2"/>',
    probe: '<circle cx="12" cy="12" r="9"/><path d="M9 15l6-6M10 9h5v5"/>',
    network: '<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18"/>',
    play: '<path d="M7 4.5v15l12-7.5z" fill="currentColor"/>',
    stop: '<rect x="6" y="6" width="12" height="12" rx="1.5" fill="currentColor"/>',
    cycle: '<path d="M4 12a8 8 0 0 1 13.7-5.7L20 8.5M20 4v4.5h-4.5M20 12a8 8 0 0 1-13.7 5.7L4 15.5M4 20v-4.5h4.5"/>',
    shield: '<path d="M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6z"/><path d="M8.5 12l2.5 2.5 4.5-5"/>',
    stack: '<path d="M12 3l9 5-9 5-9-5z"/><path d="M3 13l9 5 9-5M3 17.5l9 5 9-5"/>',
    hourglass: '<path d="M6 3h12M6 21h12M7 3c0 5 10 5 10 9s-10 4-10 9M17 3c0 5-10 5-10 9s10 4 10 9"/>',
    sparkles: '<path d="M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8z"/><path d="M19 15l.8 2.2L22 18l-2.2.8L19 21l-.8-2.2L16 18l2.2-.8z"/>',
    check: '<circle cx="12" cy="12" r="9.5" fill="currentColor" stroke="none"/><path d="M7.5 12.5l3 3 6-6.5" stroke="#fff"/>',
    warn: '<path d="M12 3l10 18H2z" fill="currentColor" stroke="none"/><path d="M12 10v5M12 18h.01" stroke="#fff"/>',
    xoct: '<path d="M8 3h8l5 5v8l-5 5H8l-5-5V8z" fill="currentColor" stroke="none"/><path d="M9 9l6 6M15 9l-6 6" stroke="#fff"/>',
    info: '<circle cx="12" cy="12" r="9.5" fill="currentColor" stroke="none"/><path d="M12 11v6M12 7.5h.01" stroke="#fff"/>',
    minus: '<circle cx="12" cy="12" r="9"/><path d="M8 12h8"/>',
    x: '<path d="M6 6l12 12M18 6L6 18"/>',
    chevrons: '<path d="M8 9l4-4 4 4M8 15l4 4 4-4"/>',
    copy: '<rect x="8" y="8" width="12" height="12" rx="2"/><path d="M16 8V5a1 1 0 0 0-1-1H5a1 1 0 0 0-1 1v10a1 1 0 0 0 1 1h3"/>',
    phone: '<path d="M5 3h4l2 5-2.5 1.5a11 11 0 0 0 6 6L16 13l5 2v4a2 2 0 0 1-2 2A16 16 0 0 1 3 5a2 2 0 0 1 2-2z" fill="currentColor" stroke="none"/>',
    phoneDown: '<path d="M3 13.5c5-4.5 13-4.5 18 0l-2 3-4-1.5v-2.5a10 10 0 0 0-6 0V15l-4 1.5z" fill="currentColor" stroke="none"/>',
    trash: '<path d="M4 7h16M10 11v6M14 11v6M6 7l1 13h10l1-13M9 7V4h6v3"/>',
    plus: '<path d="M12 5v14M5 12h14"/>',
    pencil: '<path d="M4 20h4L19 9l-4-4L4 16z"/>',
    send: '<path d="M4 12l16-8-6 16-2-6z" fill="currentColor" stroke="none"/>',
    search: '<circle cx="11" cy="11" r="6.5"/><path d="M20 20l-4.3-4.3"/>',
    back: '<path d="M15 5l-7 7 7 7"/>',
    sim: '<path d="M7 3h7l4 4v14H7z"/><rect x="10" y="11" width="5" height="6" rx="1"/>',
    map: '<path d="M9 4L3 6v14l6-2 6 2 6-2V4l-6 2z"/><path d="M9 4v14M15 6v14"/>',
    bell: '<path d="M6 16V11a6 6 0 0 1 12 0v5l2 2H4z"/><path d="M10 20a2 2 0 0 0 4 0"/>',
    lock: '<rect x="5" y="11" width="14" height="10" rx="2"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/>',
    refresh: '<path d="M20 12a8 8 0 1 1-2.3-5.7M20 4v5h-5"/>',
  };
  function icon(name, cls) {
    const body = ICONS[name] || ICONS.ring;
    return `<span class="icon ${cls || ""}"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">${body}</svg></span>`;
  }

  const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

  // ---- API ----------------------------------------------------------------
  const MODULE_HEADER = "X-DJOneHub-Module";
  async function api(path, opts) {
    opts = opts || {};
    const headers = Object.assign({}, opts.headers || {});
    if (App.fleetMode && App.selectedID && !opts.unscoped) headers[MODULE_HEADER] = App.selectedID;
    let body = opts.body;
    if (body !== undefined && typeof body !== "string" && !(body instanceof FormData)) {
      body = JSON.stringify(body);
      headers["Content-Type"] = "application/json";
    }
    const ctl = new AbortController();
    const timer = setTimeout(() => ctl.abort(), (opts.timeout || 8) * 1000);
    let res;
    try {
      res = await fetch("/" + path.replace(/^\//, ""), { method: opts.method || (body !== undefined ? "POST" : "GET"), headers, body, signal: ctl.signal });
    } catch (e) {
      throw new Error(e.name === "AbortError" ? "请求超时，请稍后重试" : "连不上 DJOneHub 后台服务");
    } finally {
      clearTimeout(timer);
    }
    const text = await res.text();
    let data = null;
    try { data = text ? JSON.parse(text) : null; } catch (_) { data = null; }
    if (!res.ok) {
      const msg = (data && (data.error || data.message)) || text || ("HTTP " + res.status);
      throw new Error(msg);
    }
    return data;
  }

  // ---- formatting ---------------------------------------------------------
  function parseDate(v) { const d = v ? new Date(v) : null; return d && !isNaN(d) ? d : null; }
  const pad = (n) => String(n).padStart(2, "0");
  function dateTime(v, withSeconds) {
    const d = v instanceof Date ? v : parseDate(v);
    if (!d) return v || "";
    const now = new Date();
    const date = (d.getFullYear() === now.getFullYear() ? "" : d.getFullYear() + "年") + (d.getMonth() + 1) + "月" + d.getDate() + "日";
    return date + " " + pad(d.getHours()) + ":" + pad(d.getMinutes()) + (withSeconds ? ":" + pad(d.getSeconds()) : "");
  }
  function dateOnly(v) { const d = v instanceof Date ? v : parseDate(v); return d ? (d.getMonth() + 1) + "月" + d.getDate() + "日" : ""; }
  function usageText(start, end) {
    const m = Math.max(0, Math.floor((end - start) / 60000));
    if (m === 0) return "不到 1 分钟";
    if (m < 60) return m + " 分钟";
    if (m < 1440) return Math.floor(m / 60) + " 小时" + (m % 60 ? " " + (m % 60) + " 分" : "");
    const h = Math.floor((m % 1440) / 60);
    return Math.floor(m / 1440) + " 天" + (h ? " " + h + " 小时" : "");
  }
  function agoText(d) {
    const m = Math.max(0, Math.floor((Date.now() - d) / 60000));
    if (m < 60) return Math.max(m, 1) + " 分钟前";
    if (m < 1440) return Math.floor(m / 60) + " 小时前";
    return Math.floor(m / 1440) + " 天前";
  }
  function bytes(n) {
    n = Number(n) || 0;
    const u = ["B", "KB", "MB", "GB", "TB"];
    let i = 0;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return (i === 0 ? n : n.toFixed(n >= 100 ? 0 : n >= 10 ? 1 : 2)) + " " + u[i];
  }
  function daysLeft(end) { const d = parseDate(end); return d ? Math.max(0, Math.ceil((d - Date.now()) / 86400000)) : 0; }

  // ---- toast --------------------------------------------------------------
  let toastTimer = null;
  function toast(text, kind) {
    kind = kind || "info";
    const layer = document.getElementById("toast-layer");
    const lead = kind === "progress" ? '<div class="spinner"></div>'
      : kind === "success" ? icon("check", "green") : kind === "failure" ? icon("warn", "orange") : icon("info", "accent");
    layer.innerHTML = `<div class="toast">${lead}<div class="text">${esc(text)}</div><button class="x" title="关闭">${icon("x")}</button></div>`;
    layer.querySelector(".x").onclick = () => { layer.innerHTML = ""; };
    clearTimeout(toastTimer);
    if (kind !== "progress") toastTimer = setTimeout(() => { layer.innerHTML = ""; }, kind === "failure" ? 7000 : 4000);
  }

  // ---- dialogs ------------------------------------------------------------
  function modal(html, opts) {
    const layer = document.getElementById("modal-layer");
    layer.innerHTML = `<div class="modal ${opts && opts.wide ? "wide" : ""}">${html}</div>`;
    const el = layer.firstElementChild;
    const close = () => { layer.innerHTML = ""; document.removeEventListener("keydown", onKey); };
    const onKey = (e) => { if (e.key === "Escape") close(); };
    document.addEventListener("keydown", onKey);
    layer.onclick = (e) => { if (e.target === layer && !(opts && opts.sticky)) close(); };
    return { el, close };
  }
  function confirmDialog(title, message, okText, destructive) {
    return new Promise((resolve) => {
      const m = modal(`<h4>${esc(title)}</h4><div class="msg">${esc(message)}</div>
        <div class="actions"><button class="btn" data-a="no">取消</button><button class="btn ${destructive ? "danger" : "primary"}" data-a="ok">${esc(okText || "确定")}</button></div>`);
      m.el.querySelector('[data-a="no"]').onclick = () => { m.close(); resolve(false); };
      m.el.querySelector('[data-a="ok"]').onclick = () => { m.close(); resolve(true); };
    });
  }
  function promptDialog(title, placeholder, value, okText) {
    return new Promise((resolve) => {
      const m = modal(`<h4>${esc(title)}</h4><input class="field" style="width:100%" placeholder="${esc(placeholder || "")}" value="${esc(value || "")}">
        <div class="actions"><button class="btn" data-a="no">取消</button><button class="btn primary" data-a="ok">${esc(okText || "保存")}</button></div>`);
      const input = m.el.querySelector("input");
      input.focus(); input.select();
      const ok = () => { m.close(); resolve(input.value); };
      input.onkeydown = (e) => { if (e.key === "Enter") ok(); };
      m.el.querySelector('[data-a="no"]').onclick = () => { m.close(); resolve(null); };
      m.el.querySelector('[data-a="ok"]').onclick = ok;
    });
  }
  async function copyText(text) {
    try { await navigator.clipboard.writeText(text); }
    catch (_) {
      const t = document.createElement("textarea"); t.value = text; document.body.appendChild(t); t.select(); document.execCommand("copy"); t.remove();
    }
  }

  // ---- shell --------------------------------------------------------------
  const NAV = [
    ["proxy", "蜂窝代理", "antenna"], ["dial", "拨号", "grid"], ["recents", "最近通话", "clock"],
    ["messages", "短信", "message"], ["contacts", "通讯录", "person"], ["module", "模块与网络", "drive"],
    ["updates", "软件更新", "download"], ["settings", "设置", "gear"],
  ];

  const App = window.App = {
    pages: {}, icon, esc, api, toast, modal, confirmDialog, promptDialog, copyText,
    fmt: { dateTime, dateOnly, usageText, agoText, bytes, daysLeft, parseDate },
    fleetMode: false, modules: [], fleetError: "",
    selectedID: localStorage.getItem("djonehub.selectedModule") || null,
    page: localStorage.getItem("djonehub.page") || "proxy",
    get selected() { return this.modules.find((m) => m.id === this.selectedID) || null; },
    listeners: new Set(),
    onModules(fn) { this.listeners.add(fn); return () => this.listeners.delete(fn); },

    async refreshModules() {
      try {
        const mode = await api("api/module-mode", { unscoped: true }).catch(() => null);
        if (!mode || !mode.enabled) { this.fleetMode = false; this.modules = []; }
        else {
          const fleet = await api("api/modules", { unscoped: true });
          this.fleetMode = true;
          this.modules = fleet.modules || [];
          this.fleetError = fleet.error || "";
          if (!this.selected || !this.selected.connected) {
            const pick = this.modules.find((m) => m.connected) || this.modules[0];
            if (pick && pick.id !== this.selectedID) this.select(pick.id, true);
          }
        }
      } catch (e) {
        this.fleetError = e.message;
      }
      this.renderSwitcher();
      this.listeners.forEach((fn) => { try { fn(); } catch (e) { console.error(e); } });
    },
    select(id, quiet) {
      const changed = id !== this.selectedID;
      this.selectedID = id;
      localStorage.setItem("djonehub.selectedModule", id);
      this.renderSwitcher();
      if (changed && !quiet) this.show(this.page);
      else if (changed) this.show(this.page);
    },
    renderSwitcher() {
      const host = document.getElementById("module-switcher");
      if (!this.fleetMode || !this.modules.length) { host.innerHTML = ""; return; }
      const sel = this.selected;
      const connected = this.modules.filter((m) => m.connected).length;
      const open = host.querySelector(".menu") !== null;
      host.innerHTML = `<button class="switcher-btn"><span class="dot ${sel && sel.connected ? "on" : ""}"></span>
        <span class="grow"><div class="t1">${esc(sel ? sel.name : "选择模块")}</div><div class="t2">${connected} 个模块已连接 · 点此切换</div></span>${icon("chevrons", "faint")}</button>`;
      const btn = host.querySelector(".switcher-btn");
      const showMenu = () => {
        const menu = document.createElement("div");
        menu.className = "menu";
        menu.innerHTML = this.modules.map((m) => `<button data-id="${esc(m.id)}" ${m.connected ? "" : "disabled"}>${esc(m.name)} · ${esc(moduleState(m))}</button>`).join("");
        host.appendChild(menu);
        menu.onclick = (e) => { const b = e.target.closest("button[data-id]"); if (b && !b.disabled) { this.select(b.dataset.id); } };
        setTimeout(() => document.addEventListener("click", function off(ev) { if (!host.contains(ev.target)) { menu.remove(); document.removeEventListener("click", off); } }), 0);
      };
      btn.onclick = () => { const m = host.querySelector(".menu"); if (m) m.remove(); else showMenu(); };
      if (open) showMenu();
    },
    renderNav() {
      const nav = document.getElementById("nav");
      nav.innerHTML = NAV.map(([id, label, ic]) => `<button class="nav-item ${id === this.page ? "active" : ""}" data-page="${id}">${icon(ic)}<span>${label}</span></button>`).join("");
      nav.onclick = (e) => { const b = e.target.closest(".nav-item"); if (b) this.show(b.dataset.page); };
    },
    current: null,
    show(page) {
      if (!this.pages[page]) page = "proxy";
      this.page = page;
      localStorage.setItem("djonehub.page", page);
      this.renderNav();
      if (this.current && this.current.destroy) { try { this.current.destroy(); } catch (_) {} }
      const content = document.getElementById("content");
      content.scrollTop = 0;
      content.innerHTML = "";
      const host = document.createElement("div");
      content.appendChild(host);
      this.current = this.pages[page](host) || null;
    },
    applyTheme() {
      const t = localStorage.getItem("djonehub.appearance") || "system";
      if (t === "system") document.documentElement.removeAttribute("data-theme");
      else document.documentElement.setAttribute("data-theme", t);
    },
    async start() {
      document.querySelectorAll("[data-icon]").forEach((el) => { el.outerHTML = icon(el.dataset.icon); });
      this.applyTheme();
      await this.refreshModules();
      this.show(this.page);
      setInterval(() => this.refreshModules(), 3000);
      const params = new URLSearchParams(location.search);
      if (params.get("page")) this.show(params.get("page"));
    },
  };

  function moduleState(m) {
    if (!m.connected) return "未连接";
    if (m.needs_confirmation) return "需确认绑定";
    if (m.proxy && m.proxy.public_ip) return m.proxy.public_ip;
    return "已连接";
  }
  App.moduleState = moduleState;

  // A page body helper: re-renders on a timer and on module refreshes, keeping scroll.
  App.livePage = function (host, render, everyMs) {
    let alive = true;
    const tick = async () => { if (alive) { try { await render(); } catch (e) { console.error(e); } } };
    const timer = everyMs ? setInterval(tick, everyMs) : null;
    const off = App.onModules(tick);
    tick();
    return { destroy() { alive = false; if (timer) clearInterval(timer); off(); } };
  };

  // Replaces el's HTML only when it changed, so 1-second redraws do not
  // swallow clicks or reset hover; reports whether it replaced anything.
  App.setHTML = function (el, html) {
    if (el._html === html) return false;
    el.innerHTML = html;
    el._html = html;
    return true;
  };

  App.placeholder = function (title) {
    return (host) => { host.innerHTML = `<div class="page"><div class="title">${esc(title)}</div><div class="placeholder">这一页正在制作中。</div></div>`; };
  };
})();
