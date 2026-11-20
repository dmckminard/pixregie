'use strict';
// Untrusted strings are only ever inserted with textContent.
const STATUSES = ['working', 'waiting', 'stale', 'idle'];
const LABELS = { working: 'en cours', waiting: 'attend', stale: 'figée', idle: 'inactive' };

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function ago(iso) {
  const s = Math.max(0, Math.round((Date.now() - new Date(iso)) / 1000));
  if (s < 60) return s + ' s';
  if (s < 3600) return Math.round(s / 60) + ' min';
  return Math.round(s / 3600) + ' h';
}

function render(state) {
  const root = document.getElementById('machines');
  root.replaceChildren();
  const counts = Object.fromEntries(STATUSES.map(s => [s, 0]));
  const machines = state.machines || [];
  if (!machines.length) root.append(el('p', 'muted', 'Aucune machine connectée.'));
  for (const m of machines) {
    const sec = el('section', m.online ? '' : 'offline');
    const h = el('h2');
    h.append(el('span', '', m.name), el('span', 'muted', m.online ? 'en ligne' : 'hors ligne · ' + ago(m.last_seen)));
    sec.append(h);
    const list = m.sessions || [];
    if (!list.length) sec.append(el('div', 'empty muted', 'aucune session récente'));
    for (const s of list) {
      if (m.online) counts[s.status] = (counts[s.status] || 0) + 1;
      const row = el('div', 'row');
      const dot = el('div', 'dot ' + s.status);
      dot.title = LABELS[s.status] || s.status;
      row.append(dot, el('div', '', s.project), el('div', 'branch muted', s.branch || ''),
        el('div', '', s.tool || (LABELS[s.status] || '')), el('div', 'age muted', ago(s.modified)));
      sec.append(row);
    }
    root.append(sec);
  }
  const totals = document.getElementById('totals');
  totals.replaceChildren(...STATUSES.map(s => {
    const b = el('span', 'badge');
    b.append(el('span', 'dot ' + s), el('span', '', counts[s] + ' ' + LABELS[s]));
    return b;
  }));
  document.getElementById('updated').textContent = 'mis à jour ' + new Date().toLocaleTimeString();
}

// Live picture of the Pi's screen; hidden when the display has no renderer.
const shot = document.getElementById('shot');
shot.addEventListener('error', () => document.getElementById('screen').classList.add('off'));
shot.addEventListener('load', () => document.getElementById('screen').classList.remove('off'));

async function tick() {
  shot.src = 'screen.png?t=' + Date.now();
  try {
    const r = await fetch('v1/state', { cache: 'no-store' });
    if (r.ok) render(await r.json());
  } catch (_) { /* Pi rebooting: keep last view */ }
}
tick();
setInterval(tick, 2000);

// --- Settings and paired machines -----------------------------------------
// Changes are sent with the X-Pixregie header: the Pi refuses changes that
// do not carry it, which a page from another site cannot add.

async function post(path, body) {
  const r = await fetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Pixregie': '1' },
    body: JSON.stringify(body),
  });
  if (!r.ok) throw new Error((await r.text()).trim() || r.statusText);
}

function say(msg) {
  const m = document.getElementById('admin-msg');
  m.textContent = msg;
  clearTimeout(say.t);
  say.t = setTimeout(() => { m.textContent = ''; }, 4000);
}

async function act(fn, ok) {
  try { await fn(); say(ok); await loadAdmin(); } catch (e) { say('Erreur : ' + e.message); }
}

function renderPaired(list) {
  const root = document.getElementById('paired');
  root.replaceChildren();
  if (!list.length) root.append(el('p', 'muted small', 'Aucune machine associée.'));
  for (const m of list) {
    const row = el('div', 'prow');
    const dot = el('div', 'dot ' + (m.online ? 'working' : 'idle'));
    dot.title = m.online ? 'en ligne' : 'hors ligne';
    const kind = el('span', 'muted', m.kind === 'server' ? 'serveur' : 'machine');
    const rename = el('button', '', 'Renommer');
    rename.type = 'button';
    rename.addEventListener('click', () => {
      const nu = prompt('Nouveau nom pour « ' + m.name + ' » :', m.name);
      if (nu && nu.trim() && nu.trim() !== m.name) act(() => post('v1/admin/rename', { old: m.name, new: nu.trim() }), 'Renommée.');
    });
    row.append(dot, el('div', 'name', m.name), kind, rename);
    if (m.kind !== 'server') {
      const revoke = el('button', 'danger', 'Révoquer');
      revoke.type = 'button';
      revoke.addEventListener('click', () => {
        if (confirm('Révoquer « ' + m.name + ' » ? Elle ne pourra plus envoyer ses sessions avant d\'être associée à nouveau.'))
          act(() => post('v1/admin/revoke', { name: m.name }), 'Révoquée.');
      });
      row.append(revoke);
    }
    root.append(row);
  }
}

async function loadAdmin() {
  try {
    const r = await fetch('v1/admin', { cache: 'no-store' });
    if (!r.ok) return;
    const a = await r.json();
    document.getElementById('show-titles').checked = a.show_titles;
    document.getElementById('sleep-after').value = a.sleep_after;
    renderPaired(a.machines || []);
    renderUpgrade(a.upgrade || {});
  } catch (_) {
    // The board restarts at the end of an update: keep asking.
    if (renderUpgrade.pending) setTimeout(loadAdmin, 3000);
  }
}

// Pi update: the page asks, a root service on the Pi installs the latest
// signed release and restarts the board.
function renderUpgrade(u) {
  const info = document.getElementById('upgrade-info');
  const btn = document.getElementById('upgrade-btn');
  let txt = 'Version ' + (u.version || '?');
  if (u.state === 'requested' || u.state === 'running') {
    txt += ' · mise à jour en cours…';
  } else if (u.available) {
    txt += ' · ' + u.latest + ' disponible';
  } else if (u.latest) {
    txt += ' · à jour';
  }
  if (u.state === 'error') txt += ' · dernier essai en échec : ' + (u.message || 'voir journalctl -u pixregie-upgrade');
  info.textContent = txt;
  btn.hidden = !u.available || u.state === 'requested' || u.state === 'running';
  renderUpgrade.pending = u.state === 'requested' || u.state === 'running';
  // A fresh check runs on the Pi when the page opens: ask again shortly.
  if (!renderUpgrade.retried) {
    renderUpgrade.retried = true;
    setTimeout(loadAdmin, 5000);
  }
  if (renderUpgrade.pending) {
    clearTimeout(renderUpgrade.t);
    renderUpgrade.t = setTimeout(loadAdmin, 3000);
  }
}
document.getElementById('upgrade-btn').addEventListener('click', () => {
  if (confirm('Installer la dernière version sur le Pi ? L\'écran redémarre.')) {
    act(() => post('v1/admin/upgrade', {}), 'Mise à jour lancée.');
  }
});

document.getElementById('show-titles').addEventListener('change', (e) =>
  act(() => post('v1/admin/settings', { show_titles: e.target.checked }), 'Enregistré.'));
document.getElementById('sleep-after').addEventListener('change', (e) =>
  act(() => post('v1/admin/settings', { sleep_after: e.target.value }), 'Enregistré.'));
loadAdmin();
setInterval(loadAdmin, 15000);
