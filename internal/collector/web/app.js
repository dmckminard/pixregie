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
