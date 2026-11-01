import { api } from './api.js';
import { h, bar, poll, toast, copyText } from './ui.js';

const MAX_INPUT = 64 * 1024; // the server reads frames up to 1 MB
const ENTER_DELAY = 50; // ms between the text and Enter, so Claude doesn't take Enter as part of a paste
const HOLD_DELAY = 400, HOLD_EVERY = 100; // ms: holding ▲/▼ repeats after HOLD_DELAY, every HOLD_EVERY

// encodeMessage turns the input box into terminal bytes (without the final Enter). Control
// characters are dropped, which also stops the text from ending a bracketed paste early.
// Multi-line text goes as one bracketed paste so its newlines don't submit. Outside a paste a
// tab would press Tab, so a single line gets spaces instead.
export function encodeMessage(text) {
  const clean = text.replace(/\r\n?/g, '\n').replace(/[\x00-\x08\x0b-\x1f\x7f]/g, '');
  return clean.includes('\n') ? `\x1b[200~${clean}\x1b[201~` : clean.replace(/\t/g, ' ');
}

export function showSession(root, project, num) {
  const backHref = `#/p/${encodeURIComponent(project)}`;
  const titleEl = h('span', {}, `#${num}`);
  const stateEl = h('span', { class: 'badge' });
  const termEl = h('div', { class: 'term' });
  const liveBtn = h('button', { class: 'live', hidden: true, onclick: () => toLive() }, '↓ Live');
  const noticeText = h('span');
  const resumeBtn = h('button', { class: 'primary', hidden: true, onclick: () => resume() }, 'Resume');
  const notice = h('div', { class: 'notice', hidden: true }, noticeText, resumeBtn);
  const keep = (e) => e.preventDefault(); // tapping a button must not close the keyboard
  const input = h('textarea', { rows: 1, placeholder: 'Message Claude…', autocapitalize: 'off',
    autocomplete: 'off', autocorrect: 'off', spellcheck: 'false', enterkeyhint: 'enter' });
  // ▲/▼ send on pointerdown, then repeat while held; a keyboard-activated click (detail 0) sends too
  let holdDelay = null, holdRepeat = null;
  const stepBtn = (label, aria, bytes) => h('button', { 'aria-label': aria,
    onpointerdown: (e) => { keep(e); if (e.button !== 0) return; send(bytes); startHold(bytes); },
    onpointerup: stopHold, onpointercancel: stopHold, onpointerleave: stopHold,
    oncontextmenu: (e) => e.preventDefault(),
    onclick: (e) => { if (e.detail === 0) send(bytes); } }, label);
  const stepper = h('div', { class: 'stepper' }, stepBtn('▲', 'Up', '\x1b[A'), stepBtn('▼', 'Down', '\x1b[B'));
  // Enter with an empty box presses Enter (e.g. to pick a highlighted option); with text it sends it
  const enterBtn = h('button', { class: 'enter', onpointerdown: keep,
    onclick: () => (input.value.trim() ? submit() : send('\r')) }, 'Enter');
  const copyBtn = h('button', { class: 'hk', 'aria-label': 'Copy screen', onpointerdown: keep,
    onclick: () => copyScreen() }, '⧉');
  const escBtn = h('button', { class: 'hk', 'aria-label': 'Escape', onpointerdown: keep,
    onclick: () => send('\x1b') }, 'Esc');
  const screen = h('div', { class: 'session' },
    bar(h('a', { href: backHref, class: 'icon', 'aria-label': 'Back' }, '‹'), [titleEl, stateEl],
      h('span', { class: 'hbtns' }, copyBtn, escBtn)),
    h('div', { class: 'term-wrap' }, termEl, liveBtn, notice),
    h('div', { class: 'input' }, input, stepper, enterBtn));
  root.append(screen);
  document.documentElement.classList.add('session-lock'); // see layout()

  const term = new window.Terminal({ fontSize: 12, fontFamily: 'Menlo, monospace', scrollback: 0,
    cursorBlink: false, theme: { background: '#111111' } });
  const fit = new window.FitAddon.FitAddon();
  term.loadAddon(fit);
  term.open(termEl);
  term.textarea.setAttribute('inputmode', 'none'); // tapping the terminal doesn't open the keyboard
  fit.fit();
  const encoder = new TextEncoder();
  let ws = null, opened = false, closed = false, ended = false, retry = null, scrolled = 0, lastState = '';
  let offlineRetry = null; // the slow try while navigator.onLine is false
  let fails = 0; // closes in a row before the socket opened, with the server reachable
  let backoff = 0; // retries since the socket last opened

  // terminal replies (device attributes) and a hardware keyboard, e.g. on an iPad; typed keys
  // go back to live first, like the buttons
  term.onData((d) => { toLive(); sendBytes(d, true); });

  function connect() {
    if (closed || ended || ws) return;
    clearTimeout(retry);
    opened = false;
    ws = new WebSocket(api.streamURL(project, num, term.cols, term.rows));
    ws.binaryType = 'arraybuffer';
    ws.onopen = () => {
      opened = true;
      clearTimeout(offlineRetry);
      fails = 0;
      backoff = 0;
      notice.hidden = true;
      setScrolled(0);
      // Open live. tmux copy-mode belongs to the pane, not to one client: a client that left
      // scrolled back (this phone before a drop or navigation, or the Mac TUI) leaves the pane
      // there, and the attach would show it scrolled with ↓ Live hidden. The server's
      // Tmux.Scroll(sid, 0) runs `send-keys -X cancel`, harmless when the pane isn't in a mode;
      // it also returns a Mac TUI viewing history in this session to live, which is accepted.
      control({ type: 'scroll', lines: 0 });
      term.reset(); // tmux attach redraws the whole screen
    };
    ws.onmessage = (e) => {
      if (typeof e.data === 'string') {
        try { if (JSON.parse(e.data).type === 'copy_end') setScrolled(0); } catch { /* ignore */ }
        return;
      }
      term.write(new Uint8Array(e.data));
    };
    ws.onclose = async (e) => {
      ws = null;
      if (closed) return;
      if (e.reason === 'session ended' || (!opened && (lastState === 'gone' || lastState === 'done'))) {
        showEnded();
        return;
      }
      if (!opened) {
        // The socket failed before it opened. Either the server refused it (410: the tmux
        // session is gone) or the server can't be reached (offline, the phone just woke up,
        // Wi-Fi <-> cellular). The browser doesn't say which, so ask the server for the
        // project: if it answers, the stream was refused. Only those failures count, and the
        // state can say alive when it isn't (idle if `claude agents` fails), so give up after 3.
        showNotice('Reconnecting…');
        const ok = await reachable();
        if (closed || ended || ws) return; // stopped, ended or reconnected meanwhile
        if (ok && ++fails >= 3) {
          showEnded();
          return;
        }
      }
      scheduleRetry();
    };
  }

  // reachable reports whether the server answers, within 5 s. A 5xx may come from a proxy in
  // front of a server that is down, so it doesn't count.
  function reachable() {
    if (navigator.onLine === false) return Promise.resolve(false);
    const check = api.snapshot(project).then(() => true, (e) => !!e.status && e.status < 500);
    return Promise.race([check, new Promise((r) => setTimeout(() => r(false), 5000))]);
  }

  function showNotice(text) {
    noticeText.textContent = text;
    resumeBtn.hidden = true;
    notice.hidden = false;
  }

  // scheduleRetry reconnects after 1 s, 2 s, 4 s… up to 30 s. Offline it waits for `online`,
  // and also tries every 30 s, as navigator.onLine can say offline when the network is up.
  // Such a try never counts toward `fails`: reachable() is false while offline, and its
  // onclose calls scheduleRetry again, which re-arms it.
  function scheduleRetry() {
    clearTimeout(retry);
    if (navigator.onLine === false) {
      showNotice('Offline, waiting for network…');
      clearTimeout(offlineRetry);
      offlineRetry = setTimeout(connect, 30000);
      return;
    }
    showNotice('Reconnecting…');
    retry = setTimeout(connect, Math.min(30000, 1000 * 2 ** backoff++));
  }

  // reconnectNow skips the backoff, e.g. when the network comes back. connect() opens at
  // most one socket: it does nothing while `ws` is set.
  function reconnectNow() {
    if (closed || ended || ws) return;
    clearTimeout(retry);
    clearTimeout(offlineRetry);
    backoff = 0;
    connect();
  }

  function showEnded() {
    ended = true;
    fails = 0;
    backoff = 0;
    noticeText.textContent = 'Session ended';
    resumeBtn.hidden = false;
    notice.hidden = false;
  }

  async function resume() {
    try { await api.action(project, 'resume', { num }); } catch (e) { toast(e.message); return; }
    ended = false;
    lastState = '';
    connect();
  }

  function sendBytes(s, quiet) {
    if (ws && ws.readyState === WebSocket.OPEN) { ws.send(encoder.encode(s)); return true; }
    if (!quiet) toast('Not connected');
    return false;
  }
  function control(obj) {
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(obj));
  }
  // back at live (↓ Live, a button, send, a reconnect) also ends a momentum glide, which would
  // otherwise scroll back into tmux copy-mode while the user types
  function setScrolled(n) {
    scrolled = n;
    liveBtn.hidden = n === 0;
    if (n === 0) stopGlide();
  }
  // keys typed while scrolled back would go to tmux copy-mode; go back to live first
  function toLive() { if (scrolled) control({ type: 'scroll', lines: 0 }); setScrolled(0); }
  function send(bytes) { toLive(); sendBytes(bytes); }
  // leaving while scrolled back would leave the pane in copy-mode for the next client (see onopen)
  function leaveScroll() { if (scrolled) control({ type: 'scroll', lines: 0 }); }

  function submit() {
    const text = input.value;
    if (text.length > MAX_INPUT) { toast('Message too long'); return; }
    const bytes = encodeMessage(text);
    if (!bytes.replace(/\x1b\[20[01]~/g, '').trim()) return; // nothing left but control characters or blanks
    toLive();
    if (!sendBytes(bytes)) return;
    setTimeout(() => {
      if (!sendBytes('\r') || input.value !== text) return; // keep the text if Enter didn't go
      input.value = '';
      grow();
      updateEnter();
    }, ENTER_DELAY);
  }

  function stopHold() {
    clearTimeout(holdDelay);
    clearInterval(holdRepeat);
    holdDelay = holdRepeat = null;
  }
  function startHold(bytes) {
    stopHold();
    holdDelay = setTimeout(() => {
      holdRepeat = setInterval(() => { toLive(); if (!sendBytes(bytes, true)) stopHold(); }, HOLD_EVERY);
    }, HOLD_DELAY);
  }

  // the Enter button reads ➤ when the box has text; it glows while Claude waits for an answer
  function updateEnter() {
    const text = !!input.value.trim();
    enterBtn.textContent = text ? '➤' : 'Enter';
    enterBtn.setAttribute('aria-label', text ? 'Send' : 'Enter');
    enterBtn.classList.toggle('solid', text || lastState === 'waiting');
    enterBtn.classList.toggle('glow', !text && lastState === 'waiting');
  }

  function grow() {
    const border = input.offsetHeight - input.clientHeight; // so a grown box doesn't scroll by 2 px
    input.style.height = 'auto';
    input.style.height = `${Math.min(input.scrollHeight + border, 120)}px`;
  }
  input.addEventListener('input', grow);
  input.addEventListener('input', updateEnter);
  updateEnter();
  // copy screen: xterm has no touch selection, so show the visible rows as plain text in a
  // sheet, where native long-press selection works. Scrolled back, that's the tmux history view.
  let copyDlg = null;
  function screenText() {
    const buf = term.buffer.active;
    const lines = [];
    for (let i = 0; i < term.rows; i++) lines.push(buf.getLine(buf.viewportY + i)?.translateToString(true) ?? '');
    while (lines.length && !lines[lines.length - 1].trim()) lines.pop();
    return lines.join('\n');
  }
  function copyScreen() {
    const text = screenText();
    if (!text) { toast('Nothing to copy'); return; }
    const area = h('textarea', { readonly: true, wrap: 'off', autocapitalize: 'off', autocomplete: 'off',
      autocorrect: 'off', spellcheck: 'false' });
    area.value = text;
    const copyAll = () => Promise.resolve(copyText(text)).then((ok) =>
      toast(ok ? 'Copied' : "Couldn't copy, select the text and use Copy"));
    const dlg = h('dialog', { class: 'copy' }, area, h('menu', {},
      h('button', { onclick: () => dlg.close() }, 'Close'),
      h('button', { class: 'primary', onclick: copyAll }, 'Copy all')));
    dlg.addEventListener('close', () => { dlg.remove(); if (copyDlg === dlg) copyDlg = null; });
    copyDlg?.remove();
    copyDlg = dlg;
    document.body.append(dlg);
    dlg.showModal();
    // open at the top-left; the textarea is readonly, so its focus opens no keyboard
    area.setSelectionRange(0, 0);
    area.scrollTop = 0;
    area.scrollLeft = 0;
  }

  // swipe to scroll tmux history: finger down = back in time
  function scrollBy(delta) {
    const next = Math.min(0, scrolled + delta);
    if (next === scrolled) return;
    if (next === 0) { toLive(); return; }
    control({ type: 'scroll', lines: next - scrolled });
    setScrolled(next);
  }
  // A fling keeps scrolling after the finger lifts, iOS-style (pattern from m-terminal's
  // terminalTouch.ts): the release velocity, over the last 100 ms, decays exponentially
  // (τ 325 ms, UIScrollView's constant). Each frame adds whole lines at most once, so a fast
  // glide sends one scroll frame per animation frame. A new touch stops it.
  const DECAY_TAU = 325, MIN_FLING = 0.05, STOP_SPEED = 0.01, VELOCITY_WINDOW = 100; // ms, px/ms
  let touchY = null, acc = 0, samples = [], glide = 0;
  const lineH = () => Math.max(8, termEl.clientHeight / term.rows);
  // applyAcc scrolls by the whole lines in acc (px; finger down is positive)
  const applyAcc = () => {
    const lines = Math.trunc(acc / lineH());
    if (!lines) return;
    acc -= lines * lineH();
    scrollBy(-lines);
  };
  const stopGlide = () => { cancelAnimationFrame(glide); glide = 0; };
  const startGlide = (v) => {
    let prev = performance.now();
    const step = (now) => {
      glide = 0;
      const dt = now - prev;
      prev = now;
      acc += v * dt;
      applyAcc();
      v *= Math.exp(-dt / DECAY_TAU);
      if (v < 0 && scrolled === 0) return; // back at live
      if (Math.abs(v) > STOP_SPEED) glide = requestAnimationFrame(step);
    };
    glide = requestAnimationFrame(step);
  };
  const onTouchStart = (e) => {
    stopGlide();
    touchY = e.touches[0].clientY;
    acc = 0;
    samples = [{ y: touchY, t: performance.now() }];
  };
  const onTouchMove = (e) => {
    if (touchY === null) return;
    e.preventDefault();
    const y = e.touches[0].clientY;
    acc += y - touchY;
    touchY = y;
    const now = performance.now();
    samples.push({ y, t: now });
    while (samples.length > 1 && now - samples[0].t > VELOCITY_WINDOW) samples.shift();
    applyAcc();
  };
  const onTouchEnd = (e) => {
    if (touchY === null) return;
    touchY = null;
    if (e.type === 'touchcancel' || e.touches.length) return;
    // against the release time: a finger held still sends no touchmove, and must not fling
    const now = performance.now();
    const recent = samples.filter((s) => now - s.t <= VELOCITY_WINDOW);
    if (recent.length < 2) return;
    const first = recent[0], last = recent[recent.length - 1];
    if (last.t - first.t < 1) return;
    const v = (last.y - first.y) / (last.t - first.t);
    if (Math.abs(v) >= MIN_FLING && !(v < 0 && scrolled === 0)) startGlide(v);
  };
  const onWheel = (e) => { e.preventDefault(); e.stopPropagation(); scrollBy(e.deltaY < 0 ? -3 : 3); };
  termEl.addEventListener('touchstart', onTouchStart, { passive: true });
  termEl.addEventListener('touchmove', onTouchMove, { passive: false });
  termEl.addEventListener('touchend', onTouchEnd);
  termEl.addEventListener('touchcancel', onTouchEnd);
  termEl.addEventListener('wheel', onWheel, { passive: false, capture: true });

  // Fit the screen to the visual viewport (the keyboard shrinks it on iOS), then tell tmux.
  // Root cause of the screen staying shifted after the keyboard closes: focusing the textarea
  // makes iOS scroll the layout viewport (the document) to reveal it. On dismiss,
  // visualViewport.offsetTop/height update late or not at all and the document stays
  // scrolled, so a fixed screen placed at the old offsetTop sits shifted down by about the
  // keyboard height. So the document can't scroll while this screen is shown (html.session-lock),
  // and layout() is re-derived after the keyboard settles: with no keyboard it scrolls back to
  // 0 and drops the inline top/height, so CSS (top 0, 100dvh) applies.
  let resizeTimer = null;
  const settleTimers = [];
  const vv = window.visualViewport;
  const keyboardShown = () => !!vv && vv.height < window.innerHeight - 1;
  const layout = () => {
    if (keyboardShown()) {
      screen.style.height = `${vv.height}px`;
      screen.style.top = `${vv.offsetTop}px`;
    } else {
      if (window.scrollX || window.scrollY) window.scrollTo(0, 0);
      screen.style.height = '';
      screen.style.top = '';
    }
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(() => {
      fit.fit();
      control({ type: 'resize', cols: term.cols, rows: term.rows });
    }, 100);
  };
  // settle lays out now and again once iOS has finished moving the viewport
  const settle = () => {
    layout();
    settleTimers.forEach(clearTimeout);
    settleTimers.length = 0;
    settleTimers.push(setTimeout(layout, 100), setTimeout(layout, 400));
  };
  const onResize = () => { if (keyboardShown()) layout(); else settle(); };
  if (vv) { vv.addEventListener('resize', onResize); vv.addEventListener('scroll', onResize); }
  window.addEventListener('resize', onResize);
  input.addEventListener('focus', settle);
  input.addEventListener('focusout', settle);
  layout();

  // back in the foreground or back online: reconnect a dropped socket at once
  const onVis = () => {
    if (document.hidden) return;
    if (!ws) reconnectNow();
    else control({ type: 'resize', cols: term.cols, rows: term.rows }); // a dead socket fails and closes
  };
  document.addEventListener('visibilitychange', onVis);
  window.addEventListener('online', reconnectNow);
  window.addEventListener('pagehide', leaveScroll); // closing the tab or reloading

  const stopPoll = poll(async () => {
    const snap = await api.snapshot(project);
    const s = (snap.project.sessions || {})[num];
    if (!s) { toast(`No session #${num}`); location.hash = backHref; return; }
    const st = (snap.session_states || {})[num] || { state: '' };
    titleEl.textContent = s.title || `#${num}`;
    stateEl.className = `badge ${st.state}`;
    stateEl.textContent = st.state;
    lastState = st.state;
    escBtn.classList.toggle('stop', st.state === 'running');
    updateEnter();
    // resumed elsewhere; 'starting' has a tmux session, so the stream can attach. Never 'gone'.
    if (ended && ['running', 'waiting', 'starting'].includes(st.state)) { ended = false; connect(); }
  });

  connect();

  return {
    stop() {
      closed = true;
      clearTimeout(retry);
      clearTimeout(offlineRetry);
      clearTimeout(resizeTimer);
      settleTimers.forEach(clearTimeout);
      stopGlide();
      stopHold();
      stopPoll();
      if (vv) { vv.removeEventListener('resize', onResize); vv.removeEventListener('scroll', onResize); }
      window.removeEventListener('resize', onResize);
      input.removeEventListener('focus', settle);
      input.removeEventListener('focusout', settle);
      document.documentElement.classList.remove('session-lock');
      document.removeEventListener('visibilitychange', onVis);
      window.removeEventListener('online', reconnectNow);
      window.removeEventListener('pagehide', leaveScroll);
      leaveScroll();
      if (ws) ws.close(1000, 'bye');
      copyDlg?.remove();
      term.dispose();
    },
  };
}
