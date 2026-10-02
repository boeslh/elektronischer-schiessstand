// role.js – gemeinsames Rollen-Widget (Rechteverwaltung).
// Wird auf jeder Seite eingebunden (<script src="/role.js">). Holt die
// aktuelle Rolle vom Server, zeigt eine kleine Ecken-Badge zum Rollenwechsel
// und blendet auf der Startseite Kacheln aus, die die aktuelle Rolle nicht
// sehen darf. window.SCHIESSSTAND_ROLE steht danach fuer andere Seiten
// bereit (z.B. um den "annullieren"-Button oder Korrektur-UI ein-/auszublenden).

(function () {
  // Wird beim Start aus /api/roles/public geladen (siehe fetchPublicRoles) -
  // enthaelt so auch selbst angelegte Rollen (Benutzerverwaltung), nicht nur
  // die 4 fest eingebauten.
  let ROLE_LABELS = {};
  const LAST_ROLE_KEY = 'ss_last_role';
  const TILE_BY_PATH = {
    '/lanes': 'lanes', '/stammdaten': 'stammdaten', '/disciplines': 'disciplines',
    '/wettkampf': 'wettkampf', '/standaktion': 'standaktion', '/ergebnisse': 'ergebnisse',
    '/simulator': 'simulator', '/geraetekonfiguration': 'simulator', '/entwicklung': 'simulator', '/auswertung': 'auswertung', '/settings': 'settings',
    '/archiv': 'archiv',
    '/preisschiessen': 'preisschiessen', '/preisschiessen-liste': 'preisschiessen',
    '/preisschiessen-bearbeiten': 'preisschiessen',
    '/vereinsabende': 'preisschiessen', '/vereinsabende-liste': 'preisschiessen',
    '/anzeigen': 'anzeigen',
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
        <h3 id="ssrole-title">Rolle wechseln</h3>
        <div id="ssrole-hint" style="display:none;font-size:12px;color:#75879a;margin:-6px 0 8px;line-height:1.4"></div>
        <label for="ssrole-sel">Rolle</label>
        <select id="ssrole-sel"><!-- wird per JS aus /api/roles/public befuellt --></select>
        <div id="ssrole-pw-wrap">
          <label for="ssrole-pw">Passwort</label>
          <input type="password" id="ssrole-pw" autocomplete="current-password">
        </div>
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
    document.getElementById('ssrole-sel').addEventListener('change', updatePwField);
    document.getElementById('ssrole-pw').addEventListener('keydown', e => {
      if (e.key === 'Enter') submitSwitch();
    });
    overlay.addEventListener('click', e => { if (e.target === overlay) closeOverlay(); });

    return { badge, overlay };
  }

  let widget = null;
  let forcedLogin = false;
  let HAS_PASSWORD = {}; // role_key -> bool (aus /api/roles/public)

  // Passwortfeld nur fuer Rollen mit Passwort anzeigen
  function updatePwField() {
    const key = document.getElementById('ssrole-sel').value;
    const needs = HAS_PASSWORD[key] !== false;
    document.getElementById('ssrole-pw-wrap').style.display = needs ? '' : 'none';
    if (needs) document.getElementById('ssrole-pw').focus();
  }

  // hint: Erklaerung oben im Dialog (z.B. beim Start einer neuen Sitzung),
  // cancelLabel: Beschriftung von "Abbrechen" (z.B. "Als Anwender weiter").
  function openOverlay(forced, hint, cancelLabel) {
    forcedLogin = forced;
    document.getElementById('ssrole-err').textContent = '';
    document.getElementById('ssrole-pw').value = '';
    document.getElementById('ssrole-title').textContent = hint ? 'Anmelden' : 'Rolle wechseln';
    const hintEl = document.getElementById('ssrole-hint');
    hintEl.textContent = hint || '';
    hintEl.style.display = hint ? '' : 'none';
    const cancel = document.getElementById('ssrole-cancel');
    cancel.textContent = cancelLabel || 'Abbrechen';
    cancel.style.display = forced ? 'none' : '';
    widget.overlay.classList.remove('hidden');
    updatePwField();
  }
  let keepRoleOnCancel = null; // neue Sitzung: "Als ... weiter" merkt sich diese Rolle

  function closeOverlay() {
    if (forcedLogin) return; // ohne gueltige Rolle kein Abbrechen moeglich
    widget.overlay.classList.add('hidden');
    if (keepRoleOnCancel) {
      // bewusst bei der aktuellen Rolle geblieben - beim naechsten Start nicht erneut fragen
      try { localStorage.setItem(LAST_ROLE_KEY, keepRoleOnCancel); } catch (e) { /* z.B. privater Modus */ }
      keepRoleOnCancel = null;
    }
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
      try { localStorage.setItem(LAST_ROLE_KEY, roleKey); } catch (e) { /* z.B. privater Modus */ }
      location.reload();
    } catch (e) {
      errEl.textContent = 'Fehler: ' + e.message;
    }
  }

  // fetchPublicRoles laedt die aktuelle Rollenliste (inkl. selbst angelegter
  // Rollen, siehe Benutzerverwaltung) und befuellt das Auswahlfeld - dabei
  // wird die zuletzt gewaehlte Rolle aus localStorage vorausgewaehlt, sofern
  // sie noch existiert.
  async function fetchPublicRoles() {
    try {
      const roles = await fetch('/api/roles/public').then(r => r.json());
      ROLE_LABELS = {};
      HAS_PASSWORD = {};
      const sel = document.getElementById('ssrole-sel');
      sel.innerHTML = '';
      (roles || []).forEach(rl => {
        ROLE_LABELS[rl.role_key] = rl.display_name;
        HAS_PASSWORD[rl.role_key] = !!rl.has_password;
        const opt = document.createElement('option');
        opt.value = rl.role_key;
        opt.textContent = rl.display_name;
        sel.appendChild(opt);
      });
      let lastRole = null;
      try { lastRole = localStorage.getItem(LAST_ROLE_KEY); } catch (e) { /* z.B. privater Modus */ }
      if (lastRole && (roles || []).some(rl => rl.role_key === lastRole)) {
        sel.value = lastRole;
      }
    } catch (e) {
      console.error('Rollenliste konnte nicht geladen werden:', e);
    }
  }

  function updateBadge(role) {
    widget.badge.innerHTML = `<span class="dot"></span>Rolle: ${ROLE_LABELS[role.role_key] || role.role_key}`;
  }

  function hideDisallowedTiles(role) {
    document.querySelectorAll('a.card[href]').forEach(a => {
      const path = a.getAttribute('href');
      if (path === '/benutzerverwaltung' || path === '/import-export') {
        // nicht Teil der Kachel-Matrix, hart auf Admin beschraenkt (siehe roles.go)
        if (role.role_key !== 'admin') a.style.display = 'none';
        return;
      }
      const tileKey = TILE_BY_PATH[path];
      if (tileKey && (role.tiles || []).indexOf(tileKey) === -1) {
        a.style.display = 'none';
      }
    });
  }

  async function init() {
    css();
    widget = buildWidget();
    try {
      const [role] = await Promise.all([
        fetch('/api/role').then(r => r.json()),
        fetchPublicRoles(),
      ]);
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

      // Neue Browser-Sitzung (automatisch als Anwender): die zuletzt benutzte
      // Rolle anbieten. Ohne Passwort direkt uebernehmen, mit Passwort den
      // Anmeldedialog zeigen - dort kann auch eine andere Rolle (z.B.
      // Anwender) gewaehlt werden, "Als ... weiter" bleibt bei der aktuellen.
      if (role.new_session) {
        let last = null;
        try { last = localStorage.getItem(LAST_ROLE_KEY); } catch (e) { /* z.B. privater Modus */ }
        if (last && last !== role.role_key && ROLE_LABELS[last] !== undefined) {
          if (!HAS_PASSWORD[last]) {
            const r = await fetch('/api/role/switch', {
              method: 'POST', headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ role_key: last, password: '' }),
            });
            if (r.ok) { location.reload(); return; }
          } else {
            document.getElementById('ssrole-sel').value = last;
            const cur = ROLE_LABELS[role.role_key] || role.role_key;
            keepRoleOnCancel = role.role_key;
            openOverlay(false,
              `Zuletzt wurde hier als „${ROLE_LABELS[last]}“ gearbeitet. Bitte Passwort eingeben ` +
              `oder eine andere Rolle wählen.`,
              `Als ${cur} weiter`);
          }
        }
      }
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
