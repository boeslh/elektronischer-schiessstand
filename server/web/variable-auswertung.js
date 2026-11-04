// Variable Auswertung (Kachel Auswertung, Reiter "Variable Auswertung"):
// Schritt 1 Auswahl der Scheiben, Schritt 2 Wertung, Schritt 3 Ergebnis.
// Gerechnet wird im Server (variable_auswertung.go); jede Aenderung rechnet
// mit kurzer Verzoegerung neu. Gespeichert wird nur die Konfiguration.
(function () {
  const $ = id => document.getElementById(id);
  const esc = s => String(s ?? '').replace(/[&<>"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]));
  const fmt = v => Number(v).toLocaleString('de-DE', {maximumFractionDigits: 3});
  const fmtDate = d => d ? d.split('-').reverse().join('.') : '';
  // CSV: Dezimalkomma, aber ohne Tausenderpunkt (Tabellenkalkulation)
  const fmtCSV = v => v == null ? '' : Number(v).toLocaleString('de-DE', {maximumFractionDigits: 3, useGrouping: false});

  // Filter-Kriterien mit mehreren Werten: Feld in der Konfiguration, Liste
  // aus /optionen, Beschriftung
  const MULTI = [
    {key: 'event_ids',          list: 'wettkaempfe',    label: 'Wettkampf'},
    {key: 'preisschiessen_ids', list: 'preisschiessen', label: 'Preisschießen'},
    {key: 'scheibe_ids',        list: 'scheiben',       label: 'Scheibe'},
    {key: 'discipline_ids',     list: 'disziplinen',    label: 'Disziplin'},
    {key: 'club_ids',           list: 'vereine',        label: 'Verein'},
    {key: 'class_ids',          list: 'klassen',        label: 'Schützenklasse'},
  ];
  const WERTUNG_LABEL = {
    ringe: 'Ringe', zehntel: 'Zehntel', teiler: 'Teiler', adler: 'Ringe',
    vor_nach: 'Vor- + Nachkomma', nachkomma: 'Nachkomma',
  };
  const GRUPPE_LABEL = {mannschaft: 'Mannschaften', verein: 'Vereine', gau: 'Gaue', klasse: 'Sportklassen', altersklasse: 'Altersklassen'};
  // Zusatzspalten der Listen: im PDF waehlbar, in der CSV immer alle
  const SPALTEN = [
    {key: 'verein',     label: 'Verein',             wert: e => e.verein},
    {key: 'gau',        label: 'Gau',                wert: e => e.gau},
    {key: 'mannschaft', label: 'Mannschaft',         wert: e => e.mannschaft},
    {key: 'klasse',     label: 'Klasse',             wert: e => e.klasse},
    {key: 'anzahl',     label: 'Anzahl Ergebnisse',  wert: e => String(e.anzahl), num: true},
    {key: 'scheiben',   label: 'Gewertete Scheiben', wert: e => (e.scheiben || []).join(', ')},
  ];
  const WERTUNG_HILFE = {
    ringe: 'Ganze Ringe.',
    zehntel: 'Zehntelringe.',
    teiler: 'Bester Teiler der Scheibe; kleiner ist besser.',
    adler: 'Abwechselnd aus Ring- und Teilerliste, wie die Adler-Wertung im Preisschießen. Ringe je Disziplin: Zehntel, wenn sie in Zehnteln gewertet wird.',
    vor_nach: 'Spaßwertung: Vor- und Nachkommastelle eines Schusses addiert (9,7 → 9 + 7 = 16; 10,9 → 19).',
    nachkomma: 'Spaßwertung: nur die Nachkommastelle eines Schusses (9,7 → 7).',
  };

  let optionen = null;
  let cfg = neueConfig();
  let geladenerName = '';
  let timer = null;
  let laufend = 0;
  let letztes = null;

  function neueConfig() {
    return {
      filter: {date_from: '', date_to: '', nur_vollstaendig: true,
               event_ids: [], preisschiessen_ids: [], scheibe_ids: [],
               discipline_ids: [], club_ids: [], class_ids: []},
      wertung: 'ringe', basis: 'summe', ziel: null, mehrfach: 'summe_beste', anz_scheiben: 1,
      faktoren: {}, gruppe: '', gruppe_beste_n: 0, pdf_spalten: [], pdf_quer: false,
    };
  }

  // ── Mehrfachauswahl ──────────────────────────────────────────────────────
  function sichtbareOptionen(m) {
    let list = optionen[m.list] || [];
    // Scheiben auf die gewaehlten Preisschiessen eingrenzen
    if (m.key === 'scheibe_ids' && cfg.filter.preisschiessen_ids.length) {
      list = list.filter(o => cfg.filter.preisschiessen_ids.includes(o.gruppe));
    }
    return list;
  }

  function renderMulti() {
    $('va-multis').innerHTML = MULTI.map(m => `
      <div class="va-field">
        <label>${m.label}</label>
        <div class="va-ms" data-key="${m.key}">
          <button type="button" class="va-ms-btn"></button>
          <div class="va-ms-pop" hidden>
            <input type="text" class="va-ms-search" placeholder="Suchen …">
            <div class="va-ms-actions"><button type="button" class="va-ms-none">Auswahl löschen</button></div>
            <div class="va-ms-list"></div>
          </div>
        </div>
      </div>`).join('');
    MULTI.forEach(m => {
      const box = document.querySelector(`.va-ms[data-key="${m.key}"]`);
      box.querySelector('.va-ms-btn').addEventListener('click', () => {
        const pop = box.querySelector('.va-ms-pop');
        const war = pop.hidden;
        document.querySelectorAll('.va-ms-pop').forEach(p => { p.hidden = true; });
        pop.hidden = !war;
        if (war) { fillMulti(m); box.querySelector('.va-ms-search').focus(); }
      });
      box.querySelector('.va-ms-search').addEventListener('input', () => fillMulti(m));
      box.querySelector('.va-ms-none').addEventListener('click', () => {
        cfg.filter[m.key] = [];
        fillMulti(m); labelMulti(); geaendert();
      });
      box.querySelector('.va-ms-list').addEventListener('change', e => {
        const set = new Set(cfg.filter[m.key]);
        if (e.target.checked) set.add(e.target.value); else set.delete(e.target.value);
        cfg.filter[m.key] = [...set];
        if (m.key === 'preisschiessen_ids') scheibenBereinigen();
        labelMulti(); geaendert();
      });
    });
    labelMulti();
  }

  // Scheiben eines abgewaehlten Preisschiessens aus der Auswahl nehmen
  function scheibenBereinigen() {
    if (!cfg.filter.preisschiessen_ids.length) return;
    const erlaubt = new Set(sichtbareOptionen(MULTI[2]).map(o => o.id));
    cfg.filter.scheibe_ids = cfg.filter.scheibe_ids.filter(id => erlaubt.has(id));
  }

  function fillMulti(m) {
    const box = document.querySelector(`.va-ms[data-key="${m.key}"]`);
    const q = box.querySelector('.va-ms-search').value.trim().toLowerCase();
    const list = sichtbareOptionen(m).filter(o => !q || o.name.toLowerCase().includes(q));
    box.querySelector('.va-ms-list').innerHTML = list.length
      ? list.map(o => `<label class="va-ms-item"><input type="checkbox" value="${esc(o.id)}"
          ${cfg.filter[m.key].includes(o.id) ? 'checked' : ''}> ${esc(o.name)}</label>`).join('')
      : '<div class="va-dim">Keine Einträge</div>';
  }

  function labelMulti() {
    MULTI.forEach(m => {
      const btn = document.querySelector(`.va-ms[data-key="${m.key}"] .va-ms-btn`);
      const ids = cfg.filter[m.key];
      const namen = ids.map(id => (optionen[m.list] || []).find(o => o.id === id)?.name).filter(Boolean);
      btn.textContent = !ids.length ? 'Alle' : ids.length === 1 ? (namen[0] || '1 gewählt') : `${ids.length} gewählt`;
      btn.title = namen.join(', ');
      btn.classList.toggle('va-aktiv', ids.length > 0);
    });
  }

  // ── Konfiguration <-> Formular ───────────────────────────────────────────
  function formular() {
    $('va-von').value = cfg.filter.date_from || '';
    $('va-bis').value = cfg.filter.date_to || '';
    $('va-voll').checked = !!cfg.filter.nur_vollstaendig;
    $('va-wertung').value = cfg.wertung;
    $('va-basis').value = cfg.basis || 'summe';
    $('va-ziel').value = cfg.ziel ?? '';
    $('va-mehrfach').value = cfg.mehrfach || 'summe_beste';
    $('va-anz').value = cfg.anz_scheiben || 1;
    $('va-gruppe').value = cfg.gruppe || '';
    $('va-besten').value = cfg.gruppe_beste_n || '';
    labelMulti();
    sichtbarkeit();
    pdfOptionen();
  }

  function pdfOptionen() {
    const gewaehlt = cfg.pdf_spalten || [];
    document.querySelectorAll('#va-pdf-spalten input').forEach(i => { i.checked = gewaehlt.includes(i.value); });
    $('va-pdf-quer').checked = !!cfg.pdf_quer;
    const namen = ['Platz', 'Name', 'Ergebnis', ...pdfSpalten().map(s => s.label)];
    $('va-pdf-sum').textContent = `${namen.join(', ')} · ${cfg.pdf_quer ? 'Querformat' : 'Hochformat'}`;
  }

  function pdfSpalten() {
    return SPALTEN.filter(s => (cfg.pdf_spalten || []).includes(s.key));
  }

  function sichtbarkeit() {
    const w = cfg.wertung;
    $('va-basis-feld').hidden = w === 'teiler' || w === 'adler';
    $('va-anz-feld').hidden = cfg.mehrfach === 'mittelwert';
    $('va-ziel-feld').hidden = w === 'adler';
    $('va-gruppe-box').hidden = w === 'adler';
    $('va-besten-feld').hidden = !cfg.gruppe;
    $('va-wertung-hilfe').textContent = WERTUNG_HILFE[w];
  }

  function lesen() {
    cfg.filter.date_from = $('va-von').value;
    cfg.filter.date_to = $('va-bis').value;
    cfg.filter.nur_vollstaendig = $('va-voll').checked;
    const alt = cfg.wertung;
    cfg.wertung = $('va-wertung').value;
    // Spasswertungen zaehlen in der Regel einen Schuss, Ringe die Summe
    if (alt !== cfg.wertung) {
      const spass = w => w === 'vor_nach' || w === 'nachkomma';
      if (spass(cfg.wertung) && !spass(alt)) $('va-basis').value = 'bester_schuss';
      if (!spass(cfg.wertung) && spass(alt)) $('va-basis').value = 'summe';
    }
    cfg.basis = $('va-basis').value;
    const ziel = $('va-ziel').value.trim();
    cfg.ziel = ziel === '' ? null : parseFloat(ziel.replace(',', '.'));
    cfg.mehrfach = $('va-mehrfach').value;
    cfg.anz_scheiben = Math.max(1, parseInt($('va-anz').value) || 1);
    cfg.gruppe = $('va-gruppe').value;
    cfg.gruppe_beste_n = Math.max(0, parseInt($('va-besten').value) || 0);
  }

  function geaendert() {
    lesen();
    sichtbarkeit();
    clearTimeout(timer);
    timer = setTimeout(berechnen, 300);
  }

  // ── Berechnen und Anzeigen ───────────────────────────────────────────────
  async function berechnen() {
    const nr = ++laufend;
    $('va-status').textContent = 'Rechne …';
    try {
      const r = await fetch('/api/variable-auswertung/berechnen', {
        method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(cfg),
      });
      const d = await r.json();
      if (!r.ok) throw new Error(d.error || r.statusText);
      if (nr !== laufend) return; // inzwischen neu angestossen
      letztes = d;
      $('va-status').textContent = '';
      renderAuswahl(d);
      renderFaktoren(d);
      renderErgebnis(d);
    } catch (e) {
      if (nr === laufend) $('va-status').textContent = 'Fehler: ' + e.message;
    }
  }

  function renderAuswahl(d) {
    $('va-step-1').textContent = `1 Auswahl (${d.scheiben_gesamt} Scheiben)`;
    $('va-anz-scheiben').textContent = d.scheiben_gesamt;
    $('va-scheiben-mehr').textContent = d.scheiben_gesamt > d.scheiben.length
      ? `Angezeigt werden die ersten ${d.scheiben.length}.` : '';
    $('va-scheiben-body').innerHTML = d.scheiben.map(s => `<tr>
      <td>${fmtDate(s.datum)}</td><td>${esc(s.name)}</td><td>${esc(s.verein)}</td><td>${esc(s.klasse)}</td>
      <td>${esc(s.disziplin)}</td><td>${esc(s.scheibe)}</td><td>${esc(s.herkunft)}</td>
      <td class="va-num">${s.shot_count}</td></tr>`).join('')
      || '<tr><td colspan="8" class="va-dim">Keine Scheiben in dieser Auswahl.</td></tr>';
  }

  function renderFaktoren(d) {
    const gesetzt = Object.values(cfg.faktoren).filter(f => f.ring || f.teiler).length;
    $('va-faktoren-sum').textContent = gesetzt ? `${gesetzt} gesetzt` : 'keine';
    if (document.activeElement?.dataset?.fkey) return; // nicht unter dem Cursor neu aufbauen
    $('va-faktoren-body').innerHTML = d.faktor_zeilen.map(z => {
      const f = cfg.faktoren[z.key] || {};
      return `<tr><td>${esc(z.label)}</td><td class="va-dim">${z.typ === 'scheibe' ? 'Scheibe' : 'Disziplin'} · ${z.anzahl}×</td>
        <td><input type="number" step="any" min="0" placeholder="1" data-fkey="${esc(z.key)}" data-art="ring" value="${f.ring || ''}"></td>
        <td><input type="number" step="any" min="0" placeholder="1" data-fkey="${esc(z.key)}" data-art="teiler" value="${f.teiler || ''}"></td></tr>`;
    }).join('') || '<tr><td colspan="4" class="va-dim">Keine Scheiben in der Auswahl.</td></tr>';
  }

  function renderErgebnis(d) {
    $('va-hinweise').innerHTML = d.hinweise.map(h => `<div class="va-hinweis">${esc(h)}</div>`).join('');
    const ziel = cfg.ziel != null && cfg.wertung !== 'adler';
    const adler = cfg.wertung === 'adler';
    const wertKopf = WERTUNG_LABEL[cfg.wertung];

    const gruppen = d.gruppen || [];
    $('va-gruppen-box').hidden = !gruppen.length;
    $('va-gruppen-titel').textContent = GRUPPE_LABEL[cfg.gruppe] || 'Gruppen';
    $('va-export').hidden = $('va-pdf-opt').hidden = !d.einzel.length;
    $('va-csv-einzel').textContent = gruppen.length ? 'CSV Einzel' : 'CSV';
    $('va-csv-gruppen').hidden = !gruppen.length;
    $('va-einzel-box').hidden = gruppen.length > 0; // Einzelergebnisse stehen dann je Gruppe
    $('va-gruppen-kopf').innerHTML = `<th>Platz</th><th>Name</th><th>Verein</th><th>Klasse</th>
      <th>Gewertet</th><th class="va-num">${esc(wertKopf)}</th>${ziel ? '<th class="va-num">Abstand</th>' : ''}`;
    $('va-gruppen-body').innerHTML = gruppen.map(g => `<tr class="va-gruppe-zeile">
      <td class="va-platz">${g.platz}.</td><td colspan="2">${esc(g.name)}</td>
      <td colspan="2">${g.anzahl} von ${g.mitglieder.length} gewertet</td>
      <td class="va-num va-wert">${fmt(g.wert)}</td>${ziel ? `<td class="va-num">${fmt(g.abstand)}</td>` : ''}</tr>`
      + g.mitglieder.map(m => `<tr class="${m.gewertet ? '' : 'va-nicht'}">
        <td class="va-platz">${m.platz}.</td>
        <td>${esc(m.name)}${m.gewertet ? '' : '<span class="va-tag">nicht gewertet</span>'}</td>
        <td>${esc(m.verein)}</td><td>${esc(m.klasse)}</td>
        <td class="va-dim">${(m.scheiben || []).map(esc).join(', ')}</td>
        <td class="va-num va-wert">${fmt(m.wert)}</td>${ziel ? `<td class="va-num">${fmt(m.abstand)}</td>` : ''}</tr>`).join('')
    ).join('');

    $('va-einzel-kopf').innerHTML = `<th>Platz</th><th>Name</th><th>Verein</th><th>Klasse</th>
      <th>Gewertet</th><th class="va-num">${esc(wertKopf)}</th>
      ${adler ? '<th class="va-num">Teiler</th>' : ''}${ziel ? '<th class="va-num">Abstand</th>' : ''}`;
    $('va-einzel-body').innerHTML = d.einzel.map(e => `<tr>
      <td class="va-platz">${e.platz}.</td><td>${esc(e.name)}</td><td>${esc(e.verein)}</td><td>${esc(e.klasse)}</td>
      <td class="va-dim">${(e.scheiben || []).map(esc).join(', ')}</td>
      <td class="va-num va-wert">${fmt(e.wert)}</td>
      ${adler ? `<td class="va-num">${e.wert_teiler != null ? fmt(e.wert_teiler) : '–'}</td>` : ''}
      ${ziel ? `<td class="va-num">${fmt(e.abstand)}</td>` : ''}</tr>`).join('')
      || '<tr><td colspan="8" class="va-dim">Kein Ergebnis für diese Auswahl.</td></tr>';
  }

  // ── Ausgabe (CSV im Browser, PDF vom Server aus denselben Daten) ─────────
  function titel() {
    return $('va-name').value.trim() || 'Variable Auswertung';
  }

  // PDF: je Gruppe eine Kopfzeile, darunter alle Mitglieder
  function gruppenListe(d) {
    const ziel = cfg.ziel != null;
    const spalten = pdfSpalten();
    const columns = [{label: 'Platz'}, {label: 'Name'}, ...spalten.map(s => ({label: s.label, num: s.num})),
      {label: WERTUNG_LABEL[cfg.wertung], num: true}];
    if (ziel) columns.push({label: 'Abstand', num: true});
    const rows = [], row_styles = [];
    d.gruppen.forEach(g => {
      rows.push([`${g.platz}.`, `${g.name} – ${g.anzahl} von ${g.mitglieder.length} gewertet`, ...spalten.map(() => ''),
        fmt(g.wert), ...(ziel ? [fmt(g.abstand)] : [])]);
      row_styles.push('gruppe');
      g.mitglieder.forEach(m => {
        rows.push([`${m.platz}.`, m.name, ...spalten.map(s => s.wert(m)), fmt(m.wert),
          ...(ziel ? [fmt(m.abstand)] : [])]);
        row_styles.push(m.gewertet ? '' : 'nicht');
      });
    });
    return {columns, rows, row_styles};
  }

  function einzelTabelle(d, fmtWert, spalten) {
    const adler = cfg.wertung === 'adler';
    const ziel = cfg.ziel != null && !adler;
    const columns = [{label: 'Platz'}, {label: 'Name'}, ...spalten.map(s => ({label: s.label, num: s.num})),
      {label: WERTUNG_LABEL[cfg.wertung], num: true}];
    if (adler) columns.push({label: 'Teiler', num: true});
    if (ziel) columns.push({label: 'Abstand', num: true});
    const rows = d.einzel.map(e => [`${e.platz}.`, e.name, ...spalten.map(s => s.wert(e)), fmtWert(e.wert),
      ...(adler ? [e.wert_teiler != null ? fmtWert(e.wert_teiler) : '–'] : []),
      ...(ziel ? [fmtWert(e.abstand)] : [])]);
    return {columns, rows};
  }

  function csvLaden(name, columns, rows) {
    const q = v => /[;"\n]/.test(v) ? '"' + String(v).replace(/"/g, '""') + '"' : String(v);
    const lines = [columns.map(c => q(c.label)).join(';'), ...rows.map(r => r.map(q).join(';'))];
    const blob = new Blob(['\ufeff' + lines.join('\r\n')], {type: 'text/csv;charset=utf-8'});
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = name.replace(/[^\wäöüÄÖÜß.-]+/g, '_') + '.csv';
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(a.href), 1000);
  }

  // Vor jeder Ausgabe frisch rechnen: eine Eingabe kann noch in der Warteschleife sein
  async function aktuell() {
    clearTimeout(timer);
    lesen();
    await berechnen();
    return letztes;
  }

  async function csvEinzel() {
    const d = await aktuell();
    if (d.gruppen.length) { csvEinzelJeGruppe(d); return; }
    const {columns, rows} = einzelTabelle(d, fmtCSV, SPALTEN);
    csvLaden(titel(), columns, rows);
  }

  function csvEinzelJeGruppe(d) {
    const ziel = cfg.ziel != null;
    const columns = [{label: 'Gruppe'}, {label: 'Gruppenplatz'}, {label: 'Platz in Gruppe'}, {label: 'Name'},
      ...SPALTEN, {label: WERTUNG_LABEL[cfg.wertung]}, ...(ziel ? [{label: 'Abstand'}] : []), {label: 'In Gruppenwertung'}];
    const rows = d.gruppen.flatMap(g => g.mitglieder.map(m => [g.name, String(g.platz), String(m.platz), m.name,
      ...SPALTEN.map(s => s.wert(m)), fmtCSV(m.wert), ...(ziel ? [fmtCSV(m.abstand)] : []),
      m.gewertet ? 'ja' : 'nein']));
    csvLaden(titel() + ' Einzel', columns, rows);
  }

  async function csvGruppen() {
    const d = await aktuell();
    const ziel = cfg.ziel != null;
    const columns = [{label: 'Platz'}, {label: 'Gruppe'}, {label: 'Gewertet'}, {label: 'Mitglieder gesamt'},
      {label: WERTUNG_LABEL[cfg.wertung]}, ...(ziel ? [{label: 'Abstand'}] : []), {label: 'Gewertete Mitglieder'}];
    const rows = d.gruppen.map(g => [String(g.platz), g.name, String(g.anzahl), String(g.mitglieder.length),
      fmtCSV(g.wert), ...(ziel ? [fmtCSV(g.abstand)] : []),
      g.mitglieder.filter(m => m.gewertet).map(m => `${m.name} (${fmtCSV(m.wert)})`).join(' · ')]);
    csvLaden(titel() + ' Gruppen', columns, rows);
  }

  // Einstellungen der Auswertung fuer den PDF-Kopf
  function pdfKopf(d) {
    const kv = [];
    const add = (label, value) => { if (value) kv.push({label, value}); };
    const f = cfg.filter;
    if (f.date_from || f.date_to) add('Zeitraum', `${fmtDate(f.date_from) || '…'} – ${fmtDate(f.date_to) || '…'}`);
    MULTI.forEach(m => add(m.label, f[m.key].map(id => (optionen[m.list] || []).find(o => o.id === id)?.name).filter(Boolean).join(', ')));
    add('Scheiben', `${d.scheiben_gesamt}${f.nur_vollstaendig ? ' (nur vollständige)' : ''}`);
    add('Wertung', $('va-wertung').selectedOptions[0].textContent);
    if (!$('va-basis-feld').hidden) add('Je Scheibe zählt', $('va-basis').selectedOptions[0].textContent);
    if (cfg.wertung !== 'adler' && cfg.ziel != null) add('Zielergebnis', fmt(cfg.ziel));
    add('Mehrere Ergebnisse', cfg.mehrfach === 'mittelwert' ? 'Mittelwert aller Ergebnisse' : `Summe der besten ${cfg.anz_scheiben}`);
    if (d.gruppen.length) add('Gruppenwertung', GRUPPE_LABEL[cfg.gruppe] + (cfg.gruppe_beste_n ? `, max. ${cfg.gruppe_beste_n} Mitglieder in der Wertung` : ''));
    add('Faktoren', d.faktor_zeilen.filter(z => cfg.faktoren[z.key]).map(z => {
      const x = cfg.faktoren[z.key];
      return z.label + ':' + (x.ring ? ` Ringe × ${fmt(x.ring)}` : '') + (x.teiler ? ` Teiler × ${fmt(x.teiler)}` : '');
    }).join(' · '));
    return kv;
  }

  async function pdf() {
    const win = window.open('', '_blank'); // vor dem await oeffnen, sonst greift der Popup-Blocker
    try {
      const d = await aktuell();
      const sections = [d.gruppen.length
        ? {title: 'Gruppenwertung – ' + GRUPPE_LABEL[cfg.gruppe], ...gruppenListe(d)}
        : {title: 'Einzelwertung', ...einzelTabelle(d, fmt, pdfSpalten())}];
      const hinweise = [...d.hinweise];
      if (d.gruppen.some(g => g.mitglieder.some(m => !m.gewertet))) {
        hinweise.push('Grau und kursiv: Ergebnis gehört zur Gruppe, zählt aber nicht zum Gruppenergebnis.');
      }
      const resp = await fetch('/api/variable-auswertung/pdf', {
        method: 'POST', headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({title: titel(), summary: pdfKopf(d), sections, legend: hinweise.join(' '),
          orientation: cfg.pdf_quer ? 'L' : 'P'}),
      });
      if (!resp.ok) throw new Error(await resp.text());
      const url = URL.createObjectURL(await resp.blob());
      if (win) win.location = url; else location.href = url;
    } catch (e) {
      if (win) win.close();
      meldung('PDF-Erzeugung fehlgeschlagen: ' + e.message, true);
    }
  }

  // ── Gespeicherte Auswertungen ────────────────────────────────────────────
  async function ladeListe() {
    const list = await fetch('/api/variable-auswertungen').then(r => r.json()).catch(() => []);
    const wrap = $('va-saved-list');
    wrap.innerHTML = Array.isArray(list) && list.length ? '' : '<span class="va-dim">Noch keine gespeichert.</span>';
    (Array.isArray(list) ? list : []).forEach(s => {
      const chip = document.createElement('div');
      chip.className = 'saved-chip';
      chip.innerHTML = `<span style="font-size:12px">${esc(s.name)}</span>
        <button type="button" class="chip-load">Laden</button>
        <button type="button" class="chip-del" title="Löschen">×</button>`;
      chip.querySelector('.chip-load').addEventListener('click', () => laden(s));
      chip.querySelector('.chip-del').addEventListener('click', () => loeschen(s));
      wrap.appendChild(chip);
    });
  }

  function laden(s) {
    const c = neueConfig();
    const g = s.config || {};
    cfg = {...c, ...g, filter: {...c.filter, ...(g.filter || {})}, faktoren: g.faktoren || {}};
    for (const m of MULTI) cfg.filter[m.key] = cfg.filter[m.key] || [];
    geladenerName = s.name;
    $('va-name').value = s.name;
    formular();
    $('va-faktoren-body').innerHTML = '';
    meldung(`„${s.name}“ geladen.`);
    berechnen();
    schritt('3');
  }

  async function speichern() {
    const name = $('va-name').value.trim();
    if (!name) { meldung('Bitte einen Namen eingeben.', true); return; }
    lesen();
    const liste = await fetch('/api/variable-auswertungen').then(r => r.json()).catch(() => []);
    if (name !== geladenerName && Array.isArray(liste) && liste.some(s => s.name === name)
        && !confirm(`„${name}“ gibt es schon. Überschreiben?`)) return;
    try {
      const r = await fetch('/api/variable-auswertungen', {
        method: 'PUT', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({name, config: cfg}),
      });
      const d = await r.json();
      if (!r.ok) throw new Error(d.error || r.statusText);
      geladenerName = name;
      meldung(`„${name}“ gespeichert.`);
      ladeListe();
    } catch (e) { meldung('Fehler: ' + e.message, true); }
  }

  async function loeschen(s) {
    if (!confirm(`Auswertung „${s.name}“ löschen?`)) return;
    await fetch(`/api/variable-auswertungen/${s.id}`, {method: 'DELETE'}).catch(() => {});
    if (geladenerName === s.name) geladenerName = '';
    ladeListe();
  }

  function neu() {
    cfg = neueConfig();
    geladenerName = '';
    $('va-name').value = '';
    formular();
    $('va-faktoren-body').innerHTML = '';
    meldung('');
    schritt('1');
    berechnen();
  }

  function meldung(text, fehler) {
    const el = $('va-meldung');
    el.textContent = text;
    el.style.color = fehler ? 'var(--err)' : 'var(--acc)';
  }

  // ── Schritte, Start ──────────────────────────────────────────────────────
  function schritt(n) {
    document.querySelectorAll('.va-step').forEach(b => b.classList.toggle('active', b.dataset.step === n));
    document.querySelectorAll('.va-panel').forEach(p => { p.hidden = p.dataset.step !== n; });
  }

  async function start() {
    if (optionen) return;
    optionen = await fetch('/api/variable-auswertung/optionen').then(r => r.json()).catch(() => null);
    if (!optionen || optionen.error) {
      optionen = null;
      $('va-status').textContent = 'Auswahllisten konnten nicht geladen werden.';
      return;
    }
    renderMulti();
    $('va-pdf-spalten').innerHTML = SPALTEN.map(s =>
      `<label class="va-check"><input type="checkbox" value="${s.key}"> ${esc(s.label)}</label>`).join('');
    formular();
    ['va-von', 'va-bis', 'va-voll', 'va-wertung', 'va-basis', 'va-ziel', 'va-mehrfach', 'va-anz', 'va-gruppe', 'va-besten']
      .forEach(id => {
        $(id).addEventListener('input', geaendert);
        $(id).addEventListener('change', geaendert);
      });
    $('va-faktoren-body').addEventListener('input', e => {
      const key = e.target.dataset.fkey;
      if (!key) return;
      const f = cfg.faktoren[key] || {};
      f[e.target.dataset.art] = parseFloat(e.target.value) || 0;
      if (f.ring || f.teiler) cfg.faktoren[key] = f; else delete cfg.faktoren[key];
      geaendert();
    });
    document.querySelectorAll('.va-step').forEach(b => b.addEventListener('click', () => schritt(b.dataset.step)));
    $('va-speichern').addEventListener('click', speichern);
    $('va-neu').addEventListener('click', neu);
    $('va-pdf').addEventListener('click', pdf);
    $('va-pdf-opt').addEventListener('change', () => {
      cfg.pdf_spalten = [...document.querySelectorAll('#va-pdf-spalten input:checked')].map(i => i.value);
      cfg.pdf_quer = $('va-pdf-quer').checked;
      pdfOptionen();
    });
    $('va-csv-einzel').addEventListener('click', csvEinzel);
    $('va-csv-gruppen').addEventListener('click', csvGruppen);
    document.addEventListener('click', e => {
      if (!e.target.closest('.va-ms')) document.querySelectorAll('.va-ms-pop').forEach(p => { p.hidden = true; });
    });
    ladeListe();
    berechnen();
  }

  document.querySelector('.tab[data-pane="variabel"]').addEventListener('click', start);
})();
