/* hauth kiosk UI. No frameworks, no build step, no runtime network access
   beyond this origin. xterm.js is loaded from vendored files. */
"use strict";

(function () {
  const cardsEl = document.getElementById("cards");
  const connEl = document.getElementById("conn");
  const tpl = document.getElementById("card-tpl");

  // name -> { root, view, term, ws, fit, termOpen }
  const cards = new Map();

  const sourceLabels = {
    none: "not logged in",
    store: "logged in",
    "file-external": "managed externally",
    env: "managed externally",
  };

  async function api(path, body) {
    const res = await fetch(path, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body || {}),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || res.statusText);
    return data;
  }

  async function refreshStatus() {
    try {
      const res = await fetch("/api/status");
      if (!res.ok) return;
      render(await res.json());
    } catch {
      /* transient; SSE reconnect will retry */
    }
  }

  function render(views) {
    if (!Array.isArray(views) || views.length === 0) {
      cardsEl.innerHTML = '<p class="empty">no harnesses configured</p>';
      cards.clear();
      return;
    }
    const seen = new Set();
    for (const view of views) {
      seen.add(view.name);
      let card = cards.get(view.name);
      if (!card) {
        const root = tpl.content.firstElementChild.cloneNode(true);
        cardsEl.appendChild(root);
        card = { root, view: null, term: null, ws: null, fit: null, termOpen: false };
        cards.set(view.name, card);
        wireCard(card, view.name);
      }
      updateCard(card, view);
    }
    for (const [name, card] of cards) {
      if (!seen.has(name)) {
        card.root.remove();
        teardownTerminal(card);
        cards.delete(name);
      }
    }
  }

  function wireCard(card, name) {
    const q = (sel) => card.root.querySelector(sel);
    q(".cancel").addEventListener("click", () => {
      const id = card.view && card.view.session_id;
      if (id) api(`/api/sessions/${encodeURIComponent(id)}/cancel`).catch(showError);
    });
    q(".toggle-term").addEventListener("click", () => {
      card.termOpen = !card.termOpen;
      q(".term").hidden = !card.termOpen;
      if (card.termOpen) attachTerminal(card);
      else teardownTerminal(card);
    });
    q(".input-row").addEventListener("submit", (ev) => {
      ev.preventDefault();
      const input = q(".session-input");
      const id = card.view && card.view.session_id;
      if (!id || !input.value) return;
      api(`/api/sessions/${encodeURIComponent(id)}/input`, { text: input.value })
        .then(() => { input.value = ""; })
        .catch(showError);
    });
    q(".code").addEventListener("click", () => copyText(q(".code"), q(".code").textContent));
    q(".url").addEventListener("click", () => {
      copyText(q(".url"), q(".url").href);
      /* default navigation still happens; copy is a bonus for phones */
    });
    card.name = name;
  }

  function updateCard(card, view) {
    const prevSession = card.view && card.view.session_id;
    card.view = view;
    const q = (sel) => card.root.querySelector(sel);

    q(".name").textContent = view.name;
    const dot = q(".dot");
    dot.className = "dot " + (view.validity || "unknown");
    dot.title = "validity: " + (view.validity || "unknown");
    const badge = q(".badge");
    badge.textContent = sourceLabels[view.source] || view.source;
    badge.className = "badge " + view.source;
    q(".detail").textContent = view.detail || "";
    q(".account").textContent = view.account ? "account: " + view.account : "";
    q(".lease").textContent = view.lease_holder ? "credentials refreshed by " + view.lease_holder : "";

    renderActions(card, view);
    renderSession(card, view, prevSession);
  }

  function renderActions(card, view) {
    const actions = card.root.querySelector(".actions");
    actions.textContent = "";
    const busy = Boolean(view.session_id && view.session_state === "running");

    if (view.can_install) {
      const btn = document.createElement("button");
      btn.type = "button";
      btn.classList.add("primary");
      btn.textContent = "Install";
      btn.disabled = busy;
      btn.addEventListener("click", () => {
        /* Install output is plain terminal noise; open the panel so
           progress is visible. */
        card.termOpen = true;
        api(`/api/harness/${encodeURIComponent(view.name)}/install`)
          .then(refreshStatus)
          .catch(showError);
      });
      actions.appendChild(btn);
    }

    if (view.can_login) {
      (view.methods || []).forEach((m, i) => {
        const btn = document.createElement("button");
        btn.type = "button";
        btn.textContent = m.title || m.id;
        if (i === 0) btn.classList.add("primary");
        btn.disabled = busy;
        btn.addEventListener("click", () =>
          api(`/api/harness/${encodeURIComponent(view.name)}/login`, { method: m.id }).catch(showError));
        actions.appendChild(btn);
      });
    }

    if (view.check_is_expensive) {
      const btn = document.createElement("button");
      btn.type = "button";
      btn.textContent = "Check";
      btn.addEventListener("click", () => {
        btn.disabled = true;
        api(`/api/harness/${encodeURIComponent(view.name)}/check`)
          .then(refreshStatus)
          .catch(showError)
          .finally(() => { btn.disabled = false; });
      });
      actions.appendChild(btn);
    }

    if (view.source === "store" || view.source === "file-external") {
      const relogin = document.createElement("button");
      relogin.type = "button";
      relogin.textContent = "Re-login";
      relogin.disabled = busy || !view.can_login;
      relogin.addEventListener("click", () =>
        api(`/api/harness/${encodeURIComponent(view.name)}/relogin`).catch(showError));
      actions.appendChild(relogin);
    }

    const logout = document.createElement("button");
    logout.type = "button";
    logout.classList.add("danger");
    logout.textContent = "Log out";
    logout.disabled = !view.can_logout || busy;
    logout.addEventListener("click", () => {
      const external = view.source === "file-external";
      const msg = external
        ? "These credential files were not created by hauth. Log out and remove them anyway?"
        : "Log out and remove stored credentials for " + view.name + "?";
      if (window.confirm(msg))
        api(`/api/harness/${encodeURIComponent(view.name)}/logout`).catch(showError);
    });
    actions.appendChild(logout);

    if (view.can_adopt) {
      const adopt = document.createElement("button");
      adopt.type = "button";
      adopt.textContent = "Adopt into store";
      adopt.addEventListener("click", () => {
        if (window.confirm(
          "Adopt these externally created credentials into the hauth store? " +
          "They will be restored into every workspace of this user."))
          api(`/api/harness/${encodeURIComponent(view.name)}/adopt`).catch(showError);
      });
      actions.appendChild(adopt);
    }

    const notes = [];
    if (!view.installed && !view.can_install)
      notes.push("CLI not installed (mise not found)");
    if (!view.can_logout && view.logout_reason) notes.push("log out unavailable: " + view.logout_reason);
    if (!view.can_login && view.login_reason && view.login_reason !== "CLI not installed")
      notes.push("log in unavailable: " + view.login_reason);
    for (const text of notes) {
      const p = document.createElement("p");
      p.className = "note";
      p.textContent = text;
      actions.appendChild(p);
    }
  }

  function renderSession(card, view, prevSession) {
    const q = (sel) => card.root.querySelector(sel);
    const panel = q(".session");
    if (!view.session_id) {
      panel.hidden = true;
      teardownTerminal(card);
      q(".url").textContent = "";
      q(".url").removeAttribute("href");
      q(".code").textContent = "";
      q(".prompt").textContent = "";
      card.termOpen = false;
      q(".term").hidden = true;
      return;
    }
    panel.hidden = false;
    q(".term").hidden = !card.termOpen;
    q(".session-state").textContent = view.session_state || "running";
    const form = q(".input-row");
    const inputKind = activeInputKind(view);
    form.hidden = inputKind === "none";
    if (!form.hidden) {
      const input = q(".session-input");
      input.placeholder = inputKind === "token" ? "paste token" :
        inputKind === "code" ? "paste code" : "type input";
      input.type = inputKind === "token" ? "password" : "text";
    }
    if (prevSession !== view.session_id) {
      teardownTerminal(card);
      q(".url").textContent = "";
      q(".url").removeAttribute("href");
      q(".code").textContent = "";
      q(".prompt").textContent = "";
      if (card.termOpen) attachTerminal(card);
    }
  }

  function activeInputKind(view) {
    /* The server does not say which method runs; assume the first method's
       input kind unless any method requires input. */
    const methods = view.methods || [];
    for (const m of methods) if (m.input && m.input !== "none") return m.input;
    return methods.length ? methods[0].input || "none" : "none";
  }

  function handleSessionEvent(ev) {
    for (const card of cards.values()) {
      if (!card.view || card.view.session_id !== ev.session_id) continue;
      const q = (sel) => card.root.querySelector(sel);
      if (ev.type === "session_url" && ev.url) {
        const a = q(".url");
        a.textContent = ev.url;
        a.href = ev.url;
      } else if (ev.type === "session_code" && ev.code) {
        q(".code").textContent = ev.code;
      } else if (ev.type === "session_prompt" && ev.prompt) {
        q(".prompt").textContent = ev.prompt;
      } else if (ev.type === "session_state") {
        q(".session-state").textContent = ev.state || "";
        if (ev.state === "exited" || ev.state === "canceled") refreshStatus();
      }
      return;
    }
    /* Session not on any card yet (login just started): refetch. */
    refreshStatus();
  }

  function attachTerminal(card) {
    const holder = card.root.querySelector(".term");
    if (!card.view || !card.view.session_id) return;
    teardownTerminal(card);
    const term = new Terminal({
      fontSize: 13,
      convertEol: false,
      cursorBlink: true,
      theme: { background: "#000000" },
    });
    const fit = new FitAddon.FitAddon();
    term.loadAddon(fit);
    term.open(holder);
    fit.fit();

    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(`${proto}//${location.host}/ws/sessions/${encodeURIComponent(card.view.session_id)}`);
    ws.binaryType = "arraybuffer";
    const enc = new TextEncoder();
    const sendResize = () => {
      if (ws.readyState === WebSocket.OPEN)
        ws.send(JSON.stringify({ type: "resize", rows: term.rows, cols: term.cols }));
    };
    ws.onopen = sendResize;
    ws.onmessage = (e) => {
      if (typeof e.data === "string") return;
      term.write(new Uint8Array(e.data));
    };
    ws.onclose = () => term.write("\r\n[session closed]\r\n");
    term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(enc.encode(data));
    });
    term.onResize(sendResize);
    const onWinResize = () => { fit.fit(); };
    window.addEventListener("resize", onWinResize);

    card.term = term;
    card.ws = ws;
    card.fit = fit;
    card.onWinResize = onWinResize;
  }

  function teardownTerminal(card) {
    if (card.ws) { try { card.ws.close(); } catch { /* already closed */ } }
    if (card.term) card.term.dispose();
    if (card.onWinResize) window.removeEventListener("resize", card.onWinResize);
    card.term = card.ws = card.fit = card.onWinResize = null;
  }

  function copyText(el, text) {
    if (!text) return;
    const done = () => {
      el.classList.add("copied");
      setTimeout(() => el.classList.remove("copied"), 1200);
    };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(done, () => fallbackCopy(text, done));
    } else {
      fallbackCopy(text, done);
    }
  }

  function fallbackCopy(text, done) {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand("copy"); done(); } catch { /* nothing left to try */ }
    ta.remove();
  }

  function showError(err) {
    console.error(err);
    window.alert(err.message || String(err));
  }

  function connect() {
    const es = new EventSource("/events");
    es.onopen = () => connEl.classList.add("live");
    es.onerror = () => connEl.classList.remove("live");
    es.onmessage = (e) => {
      let msg;
      try { msg = JSON.parse(e.data); } catch { return; }
      if (msg.type === "snapshot") render(msg.statuses);
      else if (msg.type === "status_changed") refreshStatus();
      else if (msg.type && msg.type.startsWith("session_")) handleSessionEvent(msg);
    };
  }

  refreshStatus();
  connect();
})();
