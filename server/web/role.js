// role.js – gemeinsames Rollen-Widget (Rechteverwaltung).
// Wird auf jeder Seite eingebunden (<script src="/role.js">). Holt die
// aktuelle Rolle vom Server, zeigt eine kleine Ecken-Badge zum Rollenwechsel
// und blendet auf der Startseite Kacheln aus, die die aktuelle Rolle nicht
// sehen darf. window.SCHIESSSTAND_ROLE steht danach fuer andere Seiten
// bereit (z.B. um den "annullieren"-Button oder Korrektur-UI ein-/auszublenden).

(function () {
  const ROLE_LABELS = { admin: 'Admin', developer: 'Entwickler', anwender: 'Anwender', revisor: 'Revisor' };
  const TILE_BY_PATH = {
    '/lanes': 'lanes', '/stammdaten': 'stammdaten', '/disciplines': 'disciplines',
    '/wettkampf': 'wettkampf', '/standaktion': 'standaktion', '/ergebnisse': 'ergebnisse',
    '/simulator': 'simulator', '/auswertung': 'auswertung', '/settings': 'settings',
    '/archiv': 'archiv',
  };

  window.SCHIESSSTAND_ROLE = { role_key: null, tiles: [], can_correct_results: false };

  function css() {
    const s = document.createElement('style');
    s.textContent = `
      #ssrole-badge{position:fixed;right:14px;bottom:14px;z-index:9000;
        background:#1b222b;color:#dce4ec;border:1px solid #2b3542;border-radius:20px;
        padding:7px 14px;font:13px system-ui,sans-serif;cursor:pointer;
        box-shadow:0 2px 10px rgba(0,0,0,.35);user-select:none}
      #ssrole-badge:hover{border-color:#4ab8a0}
      #ssrole-badge .dot{display:inline-block;width:7px;height:7px;border-radius:50%;
        background:#4ab8a0;margin-right:6px}
      #ssrole-overlay{position:fixed;inset:0;background:rgba(0,0,0,.55);z-index:9001;
        display:flex;align-items:center;justify-content:center}
      #ssrole-overlay.hidden{display:none}
      #ssrole-panel{background:#1b222b;color:#dce4ec;border:1px solid #2b3542;border-radius:10px;
        padding:20px 22px;width:280px;font-family:system-ui,sans-serif;
        box-shadow:0 8px 32px rgba(0,0,0,.5)}
      #ssrole-panel h3{font-size:13px;font-weight:700;letter-spacing:.5px;
        text-transform:uppercase;color:#75879a;margin:0 0 14px}
      #ssrole-panel label{display:block;font-size:11px;color:#75879a;margin:10px 0 4px}
      #ssrole-panel select,#ssrole-panel input{width:100%;background:#12161b;color:#dce4ec;
        border:1px solid #2b3542;border-radius:6px;padding:8px 10px;font-size:13px;box-sizing:border-box}
      #ssrole-panel select:focus,#ssrole-panel input:focus{outline:none;border-color:#4ab8a0}
      #ssrole-err{color:#e05c5c;font-size:12px;margin-top:8px;min-height:14px}
      #ssrole-actions{display:flex;gap:8px;margin-top:16px}
      #ssrole-actions button{flex:1;padding:8px;border-radius:6px;border:none;font-size:13px;cursor:pointer}
      #ssrole-submit{background:#4ab8a0;color:#0d1a16;font-weight:600}
      #ssrole-cancel{background:none;border:1px solid #2b3542!important;color:#75879a}
    `;
    document.head.appendChild(s);
  }

  function buildWidget() {
    const badge = document.createElement('div');
    badge.id = 'ssrole-badge';
    document.body.appendChild(badge);

    const overlay = document.createElement('div');
    overlay.id = 'ssrole-overlay';
    overlay.className = 'hidden';
    overlay.innerHTML = `
      <div id="ssrole-panel">
        <h3>Rolle wechseln</h3>
        <label for="ssrole-sel">Rolle</label>
        <select id="ssrole-sel">
          <option value="anwender">Anwender</option>
          <option value="admin">Admin</option>
          <option value="developer">Entwickler</option>
          <option value="revisor">Revisor</option>
        </select>
        <label for="ssrole-pw">Passwort</label>
        <input type="password" id="ssrole-pw" autocomplete="current-password">
        <div id="ssrole-err"></div>
        <div id="ssrole-actions">
          <button type="button" id="ssrole-cancel">Abbrechen</button>
          <button type="button" id="ssrole-submit">Wechseln</button>
        </div>
      </div>`;
    document.body.appendChild(overlay);

    badge.addEventListener('click', () => openOverlay(false));
    document.getElementById('ssrole-cancel').addEventListener('click', () => closeOverlay());
    document.getElementById('ssrole-submit').addEventListener('click', submitSwitch);
    document.getElementById('ssrole-pw').addEventListener('keydown', e => {
      if (e.key === 'Enter') submitSwitch();
    });
    overlay.addEventListener('click', e => { if (e.target === overlay) closeOverlay(); });

    return { badge, overlay };
  }

  let widget = null;
  let forcedLogin = false;

  function openOverlay(forced) {
    forcedLogin = forced;
    document.getElementById('ssrole-err').textContent = '';
    document.getElementById('ssrole-pw').value = '';
    document.getElementById('ssrole-cancel').style.display = forced ? 'none' : '';
    widget.overlay.classList.remove('hidden');
    document.getElementById('ssrole-pw').focus();
  }
  function closeOverlay() {
    if (forcedLogin) return; // ohne gueltige Rolle kein Abbrechen moeglich
    widget.overlay.classList.add('hidden');
  }

  async function submitSwitch() {
    const roleKey = document.getElementById('ssrole-sel').value;
    const password = document.getElementById('ssrole-pw').value;
    const errEl = document.getElementById('ssrole-err');
    errEl.textContent = '';
    try {
      const r = await fetch('/api/role/switch', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ role_key: roleKey, password }),
      });
      const j = await r.json().catch(() => ({}));
      if (!r.ok) { errEl.textContent = j.error || 'Fehler'; return; }
      location.reload();
    } catch (e) {
      errEl.textContent = 'Fehler: ' + e.message;
    }
  }

  function updateBadge(role) {
    widget.badge.innerHTML = `<span class="dot"></span>Rolle: ${ROLE_LABELS[role.role_key] || role.role_key}`;
  }

  function hideDisallowedTiles(role) {
    document.querySelectorAll('a.card[href]').forEach(a => {
      const path = a.getAttribute('href');
      if (path === '/benutzerverwaltung') {
        // nicht Teil der Kachel-Matrix, hart auf Admin beschraenkt (siehe roles.go)
        if (role.role_key !== 'admin') a.style.display = 'none';
        return;
      }
      const tileKey = TILE_BY_PATH[path];
      if (tileKey && role.tiles.indexOf(tileKey) === -1) {
        a.style.display = 'none';
      }
    });
  }

  async function init() {
    css();
    widget = buildWidget();
    try {
      const role = await fetch('/api/role').then(r => r.json());
      window.SCHIESSSTAND_ROLE = role;
      if (!role.role_key) {
        widget.badge.style.display = 'none';
        openOverlay(true);
        window.dispatchEvent(new CustomEvent('schiessstand-role-ready', { detail: role }));
        return;
      }
      updateBadge(role);
      hideDisallowedTiles(role);
      window.dispatchEvent(new CustomEvent('schiessstand-role-ready', { detail: role }));
    } catch (e) {
      console.error('Rolle konnte nicht geladen werden:', e);
    }
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
