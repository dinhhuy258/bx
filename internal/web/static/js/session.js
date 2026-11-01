import { api } from './api.js';
import { h, bar, poll, toast, copyText } from './ui.js';

const KEYS = [['1', '1'], ['2', '2'], ['3', '3'], ['↑', '\x1b[A'], ['↓', '\x1b[B'], ['⏎', '\r'],
  ['Esc', '\x1b'], ['Tab', '\t'], ['⇧Tab', '\x1b[Z'], ['^C', '\x03']];
const MAX_INPUT = 64 * 1024; // the server reads frames up to 1 MB
const ENTER_DELAY = 50; // ms between the text and Enter, so Claude doesn't take Enter as part of a paste

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
  const keys = h('div', { class: 'keys' }, KEYS.map(([label, bytes]) =>
    h('button', { onpointerdown: keep, onclick: () => send(bytes) }, label)));
  const input = h('textarea', { rows: 1, placeholder: 'Message Claude…', autocapitalize: 'off',
    autocomplete: 'off', autocorrect: 'off', spellcheck: 'false', enterkeyhint: 'enter' });
  const sendBtn = h('button', { class: 'primary', 'aria-label': 'Send', onpointerdown: keep, onclick: () => submit() }, '➤');
  const copyBtn = h('button', { class: 'icon', 'aria-label': 'Copy screen', onpointerdown: keep,
    onclick: () => copyScreen() }, '⧉');
  const screen = h('div', { class: 'session' },
    bar(h('a', { href: backHref, class: 'icon', 'aria-label': 'Back' }, '‹'), [titleEl, stateEl], copyBtn),
    h('div', { class: 'term-wrap' }, termEl, liveBtn, notice),
    keys, h('div', { class: 'input' }, input, sendBtn));
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
  let fails = 0; // closes in a row before the socket opened, with the server reachable
  let backoff = 0; // retries since the socket last opened

  // terminal replies (device attributes) and a hardware keyboard, e.g. on an iPad; typed keys
  // go back to live first, like the key row
  term.onData((d) => { toLive(); sendBytes(d, true); });

  function connect() {
    if (closed || ended || ws) return;
    clearTimeout(retry);
    opened = false;
    ws = new WebSocket(api.streamURL(project, num, term.cols, term.rows));
    ws.binaryType = 'arraybuffer';
    ws.onopen = () => {
      opened = true;
      fails = 0;
      backoff = 0;
      notice.hidden = true;
      setScrolled(0);
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

  // scheduleRetry reconnects after 1 s, 2 s, 4 s… up to 30 s. Offline it waits for `online`.
  function scheduleRetry() {
    clearTimeout(retry);
    if (navigator.onLine === false) {
      showNotice('Offline, waiting for network…');
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
  function setScrolled(n) { scrolled = n; liveBtn.hidden = n === 0; }
  // keys typed while scrolled back would go to tmux copy-mode; go back to live first
  function toLive() { if (scrolled) control({ type: 'scroll', lines: 0 }); setScrolled(0); }
  function send(bytes) { toLive(); sendBytes(bytes); }

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
    }, ENTER_DELAY);
  }

  function grow() {
    input.style.height = 'auto';
    input.style.height = `${Math.min(input.scrollHeight, 120)}px`;
  }
  input.addEventListener('input', grow);
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
  let touchY = null, acc = 0;
  const lineH = () => Math.max(8, termEl.clientHeight / term.rows);
  const onTouchStart = (e) => { touchY = e.touches[0].clientY; acc = 0; };
  const onTouchMove = (e) => {
    if (touchY === null) return;
    e.preventDefault();
    const y = e.touches[0].clientY;
    acc += y - touchY;
    touchY = y;
    const lines = Math.trunc(acc / lineH());
    if (!lines) return;
    acc -= lines * lineH();
    scrollBy(-lines);
  };
  const onTouchEnd = () => { touchY = null; };
  const onWheel = (e) => { e.preventDefault(); e.stopPropagation(); scrollBy(e.deltaY < 0 ? -3 : 3); };
  termEl.addEventListener('touchstart', onTouchStart, { passive: true });
  termEl.addEventListener('touchmove', onTouchMove, { passive: false });
  termEl.addEventListener('touchend', onTouchEnd);
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

  const stopPoll = poll(async () => {
    const snap = await api.snapshot(project);
    const s = (snap.project.sessions || {})[num];
    if (!s) { toast(`No session #${num}`); location.hash = backHref; return; }
    const st = (snap.session_states || {})[num] || { state: '' };
    titleEl.textContent = s.title || `#${num}`;
    stateEl.className = `badge ${st.state}`;
    stateEl.textContent = st.state;
    lastState = st.state;
    if (ended && (st.state === 'running' || st.state === 'waiting')) { ended = false; connect(); } // resumed elsewhere
  });

  connect();

  return {
    stop() {
      closed = true;
      clearTimeout(retry);
      clearTimeout(resizeTimer);
      settleTimers.forEach(clearTimeout);
      stopPoll();
      if (vv) { vv.removeEventListener('resize', onResize); vv.removeEventListener('scroll', onResize); }
      window.removeEventListener('resize', onResize);
      input.removeEventListener('focus', settle);
      input.removeEventListener('focusout', settle);
      document.documentElement.classList.remove('session-lock');
      document.removeEventListener('visibilitychange', onVis);
      window.removeEventListener('online', reconnectNow);
      if (ws) ws.close(1000, 'bye');
      copyDlg?.remove();
      term.dispose();
    },
  };
}
