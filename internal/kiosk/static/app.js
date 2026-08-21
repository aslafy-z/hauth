"use strict";

/* hauth kiosk: compact status list + focused sheet.
   The sheet shows harness detail and actions; starting a login turns it
   into a guided stepper fed by SSE events (url, code, prompt, state). */

const $ = (sel, el) => (el || document).querySelector(sel);

const listEl = $("#list");
const sheetEl = $("#sheet");
const sheetName = $("#sheet-name");
const sheetBadge = $("#sheet-badge");
const sheetBody = $("#sheet-body");
const connEl = $("#conn");

const state = {
  statuses: [],
  selected: null,
  returnFocus: null,
  // per-harness login session as seen through events
  sessions: {},
  // harnesses with an auto-check in flight
  checking: {},
  term: null,
};

/* ---------- api ---------- */

async function api(path, body) {
  const res = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: body ? JSON.stringify(body) : null,
  });
  if (!res.ok) {
    let msg = res.statusText;
    try { msg = (await res.json()).error || msg; } catch { /* not json */ }
    throw new Error(msg);
  }
  try { return await res.json(); } catch { return {}; }
}

let refreshTimer = null;
function refreshSoon() {
  clearTimeout(refreshTimer);
  refreshTimer = setTimeout(refreshStatus, 250);
}

async function refreshStatus() {
  try {
    const res = await fetch("/api/status");
    state.statuses = (await res.json()) || [];
    seedSessions();
    render();
  } catch { /* stream reconnect will retry */ }
}

function view(name) {
  return state.statuses.find((h) => h.name === name);
}

/* ---------- wording ---------- */

function sourceWording(h) {
  switch (h.source) {
    case "env": return `Managed externally (${h.detail})`;
    case "file-external":
      return h.detail && h.detail !== "files present, not written by hauth"
        ? `Managed externally (${h.detail})`
        : "Managed externally (local files)";
    case "store": return "Logged in, synced to the store";
    default: return h.installed === false ? "CLI not installed" : "Not logged in";
  }
}

function shortState(h) {
  const s = state.sessions[h.name];
  if (s && s.state === "running") return "logging in…";
  if (h.source === "none") return h.installed === false ? "not installed" : "not logged in";
  const src = { env: "env", "file-external": "external", store: "synced" }[h.source] || h.source;
  const val = { valid: "valid", invalid: "invalid" }[h.validity];
  return val ? `${src} · ${val}` : src;
}

function dotClass(h) {
  if (h.source === "none") return "dot none";
  return `dot ${h.validity === "valid" ? "valid" : h.validity === "invalid" ? "invalid" : ""}`;
}

function relTime(iso) {
  const t = new Date(iso).getTime();
  if (!t) return "";
  const s = Math.max(0, (Date.now() - t) / 1000);
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  return `${Math.floor(s / 86400)} d ago`;
}

/* ---------- rendering: list ---------- */

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null) e.textContent = text;
  return e;
}

function renderList() {
  listEl.textContent = "";
  if (!state.statuses.length) {
    listEl.append(el("p", "empty", "No harnesses registered."));
    return;
  }
  for (const h of state.statuses) {
    const row = el("button", "row");
    row.type = "button";
    row.dataset.name = h.name;
    row.append(el("span", dotClass(h)));
    const name = el("span", "name", h.name);
    if (h.account) name.append(el("span", "account", h.account));
    row.append(name);
    const st = el("span", "state", shortState(h));
    if (state.sessions[h.name] && state.sessions[h.name].state === "running") st.classList.add("busy");
    row.append(st);
    row.append(el("span", "chev", "›"));
    row.addEventListener("click", () => openSheet(h.name, row));
    listEl.append(row);
  }
}

/* ---------- sheet ---------- */

function openSheet(name, focusOrigin) {
  state.selected = name;
  state.returnFocus = focusOrigin || null;
  document.body.classList.add("sheet-open");
  renderSheet();
  $("#sheet-close").focus({ preventScroll: true });
}

function closeSheet() {
  document.body.classList.remove("sheet-open");
  state.selected = null;
  teardownTerm();
  if (state.returnFocus) state.returnFocus.focus({ preventScroll: true });
}

$("#sheet-close").addEventListener("click", closeSheet);
$("#backdrop").addEventListener("click", closeSheet);
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape" && state.selected) closeSheet();
});

function renderSheet() {
  const h = view(state.selected);
  if (!h) { closeSheet(); return; }
  sheetName.textContent = h.name;
  sheetBadge.textContent = h.refresh;
  const s = state.sessions[h.name];
  if (s) renderFlow(h, s); else renderDetail(h);
}

/* ---------- sheet: detail ---------- */

function actionBtn(label, cls, fn) {
  const b = el("button", `btn ${cls || ""}`, label);
  b.addEventListener("click", async () => {
    b.disabled = true;
    try { await fn(); } catch (err) { toastError(err); }
    b.disabled = false;
  });
  return b;
}

function toastError(err) {
  const n = el("div", "note", String(err.message || err));
  sheetBody.prepend(n);
  setTimeout(() => n.remove(), 6000);
}

function startLogin(h, methodID) {
  return api(`/api/harness/${h.name}/login`, { method: methodID }).then((r) => {
    state.sessions[h.name] = { id: r.session_id, state: "running", input: methodInput(h, methodID) };
    renderSheet();
    // Backfill from HarnessView in case early events raced the stream.
    setTimeout(refreshSoon, 900);
  });
}

function methodInput(h, id) {
  const m = (h.methods || []).find((m) => m.id === id) || (h.methods || [])[0];
  return m ? m.input : "none";
}

const autoChecked = {};

function autoCheck(h) {
  // Opening a harness is intent enough to verify it: run the expensive
  // probe once, in the background, instead of waiting for a tap.
  if (!h.check_is_expensive || h.validity !== "unknown") return;
  if (state.checking[h.name]) return;
  // A probe that still comes back unknown must not re-trigger on every
  // render; back off for the server's own cache window.
  if (Date.now() - (autoChecked[h.name] || 0) < 5 * 60 * 1000) return;
  state.checking[h.name] = true;
  api(`/api/harness/${h.name}/check`)
    .catch(() => {})
    .finally(async () => {
      autoChecked[h.name] = Date.now();
      delete state.checking[h.name];
      await refreshStatus();
    });
}

function renderDetail(h) {
  teardownTerm();
  sheetBody.textContent = "";
  autoCheck(h);

  const line = el("div", "status-line");
  line.append(el("span", dotClass(h)), el("span", null, sourceWording(h)));
  sheetBody.append(line);

  const kv = el("dl", "kv");
  const add = (k, v, mono) => {
    if (!v) return;
    const r = el("div");
    r.append(el("span", "k", k), el("span", mono ? "mono" : null, v));
    kv.append(r);
  };
  add("account", h.account, true);
  add("validity", state.checking[h.name] ? "checking…" : h.validity);
  add("checked", relTime(h.checked_at));
  add("sync", h.refresh === "none" ? "nothing to sync" : h.refresh, true);
  add("lease", h.lease_holder ? `held by ${h.lease_holder}` : "", true);
  sheetBody.append(kv);

  if (h.login_reason && !h.can_login) sheetBody.append(el("p", "note", h.login_reason));
  if (h.logout_reason && !h.can_logout) sheetBody.append(el("p", "note", h.logout_reason));

  const actions = el("div", "actions");
  if (h.can_install) {
    actions.append(actionBtn("Install CLI", "primary", async () => {
      const r = await api(`/api/harness/${h.name}/install`);
      state.sessions[h.name] = { id: r.session_id, state: "running", install: true, input: "none" };
      renderSheet();
      toggleTerm(true);
    }));
  }
  if (h.can_login && h.installed !== false) {
    const methods = h.methods || [];
    methods.forEach((m, i) => {
      const label = i === 0 ? "Log in" : m.title;
      actions.append(actionBtn(label, i === 0 ? "primary" : "", () => startLogin(h, m.id)));
    });
  }
  if (h.check_is_expensive && !state.checking[h.name]) {
    actions.append(actionBtn("Re-check", "", async () => {
      await api(`/api/harness/${h.name}/check`);
      await refreshStatus();
    }));
  }
  if (h.can_adopt) {
    actions.append(actionBtn("Adopt into store", "", async () => {
      if (!confirm(`Push ${h.name}'s existing credential files to the store? hauth did not create them.`)) return;
      await api(`/api/harness/${h.name}/adopt`);
      await refreshStatus();
    }));
  }
  if (h.can_logout) {
    actions.append(actionBtn("Log out", "danger", async () => {
      const extra = h.source === "file-external" ? " These files were not created by hauth." : "";
      if (!confirm(`Log ${h.name} out and remove its local credentials?${extra}`)) return;
      await api(`/api/harness/${h.name}/logout`);
      await refreshStatus();
    }));
  }
  sheetBody.append(actions);
}

/* ---------- sheet: login flow ---------- */

function step(n, title, cls) {
  const li = el("li", `step ${cls}`);
  li.dataset.n = String(n);
  li.append(el("h3", null, title));
  return li;
}

function copyButton(text, cls) {
  const b = el("button", `copy ${cls || ""}`);
  b.type = "button";
  b.title = "Tap to copy";
  const span = el("span", null, text);
  const ico = el("span", "ico", "⧉");
  b.append(span, ico);
  b.addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      const ta = document.createElement("textarea");
      ta.value = text;
      document.body.append(ta);
      ta.select();
      document.execCommand("copy");
      ta.remove();
    }
    b.classList.add("copied");
    ico.textContent = "✓";
    setTimeout(() => { b.classList.remove("copied"); ico.textContent = "⧉"; }, 1600);
  });
  return b;
}

function renderFlow(h, s) {
  sheetBody.textContent = "";
  const finished = s.state !== "running";
  const succeeded = s.state === "succeeded";

  if (s.install) {
    const w = el("div", "waiting");
    if (!finished) w.append(el("span", "spinner"));
    w.append(el("span", null, finished ? "" : `Installing ${h.name}…`));
    sheetBody.append(w);
  } else {
    const steps = el("ol", "steps");
    const wantsInput = s.input && s.input !== "none";

    // 1: the sign-in link
    const st1 = step(1, "Open on any device", s.url ? "done" : finished ? "pending" : "on");
    if (s.url) {
      st1.append(copyButton(s.url));
      const a = el("a", "open-link", "open here instead");
      a.href = s.url;
      a.target = "_blank";
      a.rel = "noopener noreferrer";
      st1.append(a);
    } else if (!finished) {
      const w = el("div", "waiting");
      w.append(el("span", "spinner"), el("span", null, "Waiting for the sign-in link…"));
      st1.append(w);
    }
    steps.append(st1);

    // 2: the one-time code shown by the CLI, if the flow has one
    let n = 2;
    if (s.code) {
      const st2 = step(n++, "Enter this code", s.prompt || succeeded ? "done" : finished ? "" : "on");
      st2.append(copyButton(s.code, "code"));
      steps.append(st2);
    }

    // 3: paste back into the CLI, when the method takes input
    if (wantsInput) {
      const st3 = step(n++, "Paste the code back here", s.prompt && !finished ? "on" : succeeded ? "done" : finished ? "" : "pending");
      if (!finished) {
        const form = el("form", "input-row");
        const input = document.createElement("input");
        input.placeholder = s.input === "token" ? "token" : "code";
        input.autocomplete = "off";
        input.autocapitalize = "off";
        input.spellcheck = false;
        const send = el("button", "btn primary", "Send");
        send.type = "submit";
        form.append(input, send);
        form.addEventListener("submit", async (e) => {
          e.preventDefault();
          if (!input.value) return;
          try {
            await api(`/api/sessions/${s.id}/input`, { text: input.value });
            input.value = "";
          } catch (err) { toastError(err); }
        });
        st3.append(form);
        if (s.prompt) setTimeout(() => input.focus({ preventScroll: true }), 50);
      }
      steps.append(st3);
    }

    // last: the CLI confirms
    const last = step(n, s.install ? "Done" : "The CLI confirms", finished ? (succeeded ? "done" : "") : s.url ? "on" : "pending");
    if (!finished) {
      const w = el("div", "waiting");
      w.append(el("span", "spinner"), el("span", null, "Waiting for the CLI…"));
      last.append(w);
    }
    steps.append(last);
    sheetBody.append(steps);
  }

  if (finished) {
    const ok = s.state === "succeeded";
    const msg = s.install
      ? (ok ? "Installed" : "Install failed, open the terminal for details")
      : (ok ? "Logged in. Credentials synced to the store." : s.state === "canceled" ? "Login canceled." : "Login failed, open the terminal for details.");
    const out = el("div", `outcome ${ok ? "ok" : "bad"}`, (ok ? "✓ " : "✗ ") + msg);
    sheetBody.append(out);
    const done = el("div", "actions");
    done.append(actionBtn("Done", "primary", async () => {
      delete state.sessions[h.name];
      teardownTerm();
      await refreshStatus();
      renderSheet();
    }));
    sheetBody.append(done);
  }

  const tools = el("div", "session-tools");
  const termBtn = el("button", "linkish", state.termOpen ? "▾ terminal" : "▸ terminal");
  termBtn.addEventListener("click", () => toggleTerm());
  tools.append(termBtn);
  if (!finished) {
    const cancel = el("button", "linkish", "cancel session");
    cancel.style.color = "var(--danger)";
    cancel.addEventListener("click", () => api(`/api/sessions/${s.id}/cancel`).catch(toastError));
    tools.append(cancel);
  }
  sheetBody.append(tools);

  const termHost = el("div", "term");
  termHost.id = "term-host";
  termHost.hidden = !state.termOpen;
  sheetBody.append(termHost);
  if (state.termOpen) {
    sheetBody.append(keybar());
    mountTerm(s, termHost);
  }
}

/* ---------- terminal ---------- */

// On-screen keys for TUIs on phones: arrows, Enter, Esc are unreachable
// from mobile keyboards. Each sends the raw sequence to the pty.
const TERM_KEYS = [
  ["esc", "\x1b"],
  ["tab", "\t"],
  ["^C", "\x03"],
  ["◀", "\x1b[D"],
  ["▼", "\x1b[B"],
  ["▲", "\x1b[A"],
  ["▶", "\x1b[C"],
  ["⏎", "\r"],
];

function keybar() {
  const bar = el("div", "keybar");
  for (const [label, seq] of TERM_KEYS) {
    const b = el("button", "key", label);
    b.type = "button";
    // pointerdown + preventDefault keeps focus (and the soft keyboard)
    // where it is instead of jumping to the button.
    b.addEventListener("pointerdown", (e) => {
      e.preventDefault();
      const t = state.term;
      if (t && t.ws.readyState === 1) t.ws.send(new TextEncoder().encode(seq));
    });
    bar.append(b);
  }
  return bar;
}

function toggleTerm(force) {
  state.termOpen = force !== undefined ? force : !state.termOpen;
  if (!state.termOpen) teardownTerm();
  renderSheet();
}

function mountTerm(s, host) {
  if (state.term && state.term.sid === s.id) {
    host.append(state.term.el);
    state.term.fit.fit();
    return;
  }
  teardownTerm();
  const holder = el("div");
  host.append(holder);
  const term = new Terminal({
    fontSize: 12,
    fontFamily: "ui-monospace, SF Mono, Menlo, monospace",
    theme: { background: "#0a0d12" },
    convertEol: false,
    cursorBlink: false,
    disableStdin: false,
    rows: 18,
  });
  const fit = new FitAddon.FitAddon();
  term.loadAddon(fit);
  term.open(holder);
  fit.fit();
  const proto = location.protocol === "https:" ? "wss" : "ws";
  const ws = new WebSocket(`${proto}://${location.host}/ws/sessions/${s.id}`);
  ws.binaryType = "arraybuffer";
  ws.onmessage = (e) => term.write(new Uint8Array(e.data));
  ws.onopen = () => sendResize();
  term.onData((d) => { if (ws.readyState === 1) ws.send(new TextEncoder().encode(d)); });
  const sendResize = () => {
    if (ws.readyState === 1) ws.send(JSON.stringify({ type: "resize", rows: term.rows, cols: term.cols }));
  };
  term.onResize(sendResize);
  const ro = new ResizeObserver(() => fit.fit());
  ro.observe(host);
  state.term = { sid: s.id, term, ws, fit, ro, el: holder };
}

function teardownTerm() {
  if (!state.term) { state.termOpen = false; return; }
  try { state.term.ws.close(); } catch { /* already closed */ }
  state.term.ro.disconnect();
  state.term.term.dispose();
  state.term = null;
  state.termOpen = false;
}

/* ---------- events ---------- */

function session(harness) {
  return (state.sessions[harness] ||= { state: "running", input: "none" });
}

function connect() {
  const es = new EventSource("/events");
  es.onopen = () => { connEl.className = "conn live"; };
  es.onerror = () => { connEl.className = "conn lost"; };
  es.onmessage = (e) => {
    let ev;
    try { ev = JSON.parse(e.data); } catch { return; }
    switch (ev.type) {
      case "snapshot":
        state.statuses = ev.statuses || [];
        seedSessions();
        render();
        break;
      case "status_changed":
        refreshSoon();
        break;
      case "session_url":
        session(ev.harness).id = ev.session_id;
        session(ev.harness).url = ev.url;
        render();
        break;
      case "session_code":
        session(ev.harness).id = ev.session_id;
        session(ev.harness).code = ev.code;
        render();
        break;
      case "session_prompt":
        session(ev.harness).id = ev.session_id;
        session(ev.harness).prompt = ev.prompt || true;
        render();
        break;
      case "session_state": {
        const s = session(ev.harness);
        s.id = ev.session_id;
        s.state = ev.state;
        render();
        break;
      }
    }
  };
}

// Rebuild or backfill session records from server state: live events are
// not replayed, so a reload mid-login (or an event that raced the stream
// connect) recovers from the artifacts carried in HarnessView.
function seedSessions() {
  for (const h of state.statuses) {
    if (!h.session_id) continue;
    const s = state.sessions[h.name];
    if (!s && h.session_state === "running") {
      state.sessions[h.name] = {
        id: h.session_id,
        state: h.session_state,
        input: methodInput(h),
        url: h.session_url,
        code: h.session_code,
        prompt: h.session_prompt,
      };
    } else if (s && s.id === h.session_id) {
      s.url ||= h.session_url;
      s.code ||= h.session_code;
      s.prompt ||= h.session_prompt;
      if (s.state === "running" && h.session_state) s.state = h.session_state;
    }
  }
}

function render() {
  renderList();
  if (state.selected) renderSheet();
}

// Keep the sheet clear of the soft keyboard. Android shrinks the layout
// viewport (interactive-widget=resizes-content); iOS only shrinks the
// visual viewport, so mirror the difference into --kb and keep the
// focused field visible.
if (window.visualViewport) {
  const vv = window.visualViewport;
  const applyInset = () => {
    const kb = Math.max(0, window.innerHeight - vv.height - vv.offsetTop);
    document.documentElement.style.setProperty("--kb", `${kb}px`);
    if (kb > 0 && document.activeElement && document.activeElement.tagName === "INPUT") {
      document.activeElement.scrollIntoView({ block: "nearest" });
    }
  };
  vv.addEventListener("resize", applyInset);
  vv.addEventListener("scroll", applyInset);
}

refreshStatus();
connect();
