// ============================================================================
// simulator-foto.js – optionale Foto-Kalibrierung im Simulator (Abschnitt 3b).
//
// Ablauf: Foto der beschossenen Scheibe laden -> 4 Befestigungsschrauben
// markieren -> in den schwarzen Innenkreis klicken (Rand wird automatisch
// erkannt) -> Einschussloecher markieren/automatisch suchen -> Loecher den
// gemessenen Schuessen zuordnen -> Parameter auf dem Server optimieren
// (POST /api/sessions/{id}/photo-calibrate, siehe sim_photo_cal.go).
//
// Geometrie: Die 4 Schrauben werden als Ecken eines (unbekannt grossen)
// achsparallelen Rechtecks angenommen. Eine Homographie Bild -> Einheits-
// quadrat entzerrt damit die Perspektive (schraeg aufgenommenes Foto); im
// entzerrten Raum ist der Innenkreis eine achsparallele Ellipse, deren
// Halbachsen zusammen mit dem bekannten Durchmesser den Massstab in x und y
// liefern und deren Mitte der Scheibenmittelpunkt ist. Optional koennen die
// tatsaechlichen Schraubenabstaende angegeben werden - der Kreis liefert
// dann nur noch die Mitte, sein gemessener Durchmesser dient als Kontrolle.
//
// Nutzt globale Funktionen/Variablen von simulator.html: api(),
// currentParams(), applyParams(), currentSessionId, runSimulation().
// ============================================================================
(function () {
'use strict';

const $ = id => document.getElementById(id);
const PELLET_R_MM = 2.25;

const F = {
  img: null, W: 0, H: 0, lum: null, sat: null,
  screws: [],          // Bildpixel, Reihenfolge TL, TR, BR, BL
  rim: null,           // Randpunkte des Innenkreises (Bildpixel, subpixelgenau)
  holes: [],           // Einschussloecher (Bildpixel)
  geo: null,           // Ergebnis computeGeometry()
  mode: 'screws',
  verify: null,        // Antwort von /photo-verify
  verifyShot: null,    // ausgewaehlter Schuss (Messpunkte im Foto)
  view: {s: 1, ox: 0, oy: 0},
  pairs: [],           // Zuordnung Loch <-> Schuss
  result: null,        // Antwort der Optimierung
  unmatchedSim: [],    // simulierte Schuesse ohne Loch (fuer die Anzeige)
};

// ── Lineare Algebra ─────────────────────────────────────────────────────
function solveLinear(A, b) {
  const n = b.length;
  const M = A.map((row, i) => [...row, b[i]]);
  for (let c = 0; c < n; c++) {
    let p = c;
    for (let r = c + 1; r < n; r++) if (Math.abs(M[r][c]) > Math.abs(M[p][c])) p = r;
    [M[c], M[p]] = [M[p], M[c]];
    if (Math.abs(M[c][c]) < 1e-12) return null;
    for (let r = 0; r < n; r++) {
      if (r === c) continue;
      const f = M[r][c] / M[c][c];
      for (let k = c; k <= n; k++) M[r][k] -= f * M[c][k];
    }
  }
  return M.map((row, i) => row[n] / row[i]);
}

// Homographie (3x3, zeilenweise) aus 4 Punktpaaren src[i] -> dst[i]
function homography(src, dst) {
  const A = [], b = [];
  for (let i = 0; i < 4; i++) {
    const [x, y] = src[i], [u, v] = dst[i];
    A.push([x, y, 1, 0, 0, 0, -u * x, -u * y]); b.push(u);
    A.push([0, 0, 0, x, y, 1, -v * x, -v * y]); b.push(v);
  }
  const h = solveLinear(A, b);
  return h ? [...h, 1] : null;
}
function applyH(h, x, y) {
  const w = h[6] * x + h[7] * y + h[8];
  return [(h[0] * x + h[1] * y + h[2]) / w, (h[3] * x + h[4] * y + h[5]) / w];
}
function invert3(m) {
  const [a, b, c, d, e, f, g, h, i] = m;
  const A = e * i - f * h, B = -(d * i - f * g), C = d * h - e * g;
  const det = a * A + b * B + c * C;
  return [A / det, -(b * i - c * h) / det, (b * f - c * e) / det,
          B / det, (a * i - c * g) / det, -(a * f - c * d) / det,
          C / det, -(a * h - b * g) / det, (a * e - b * d) / det];
}

// Achsparallele Ellipse A u^2 + C v^2 + D u + E v = 1 (kleinste Quadrate)
function fitAxisEllipse(pts) {
  const M = [[0,0,0,0],[0,0,0,0],[0,0,0,0],[0,0,0,0]], r = [0,0,0,0];
  for (const [u, v] of pts) {
    const row = [u * u, v * v, u, v];
    for (let a = 0; a < 4; a++) {
      for (let b = 0; b < 4; b++) M[a][b] += row[a] * row[b];
      r[a] += row[a];
    }
  }
  const p = solveLinear(M, r);
  if (!p) return null;
  // Wegen der Normierung "= 1" sind A und C negativ, wenn der Ursprung
  // ausserhalb der Ellipse liegt (der Normalfall) - gueltig ist jede
  // Loesung mit k/A > 0 und k/C > 0.
  const [A, C, D, E] = p;
  const uc = -D / (2 * A), vc = -E / (2 * C);
  const k = 1 + A * uc * uc + C * vc * vc;
  if (!(k / A > 0 && k / C > 0)) return null;
  return {uc, vc, au: Math.sqrt(k / A), av: Math.sqrt(k / C)};
}

function otsu(values) {
  const hist = new Array(256).fill(0);
  for (const v of values) hist[Math.max(0, Math.min(255, v | 0))]++;
  const total = values.length;
  let sum = 0;
  for (let i = 0; i < 256; i++) sum += i * hist[i];
  let sumB = 0, wB = 0, best = 0, thr = 128;
  for (let t = 0; t < 256; t++) {
    wB += hist[t];
    if (!wB) continue;
    const wF = total - wB;
    if (!wF) break;
    sumB += t * hist[t];
    const mB = sumB / wB, mF = (sum - sumB) / wF;
    const between = wB * wF * (mB - mF) * (mB - mF);
    if (between > best) { best = between; thr = t + 0.5; }
  }
  return thr;
}

function median(arr) {
  if (!arr.length) return 0;
  const s = Float32Array.from(arr).sort();
  return s[s.length >> 1];
}

// ── Geometrie ───────────────────────────────────────────────────────────
function refDiameter() { return parseFloat($('foto-diameter').value) || 59.5; }

// Geometrie: Die 4 Schrauben entzerren die Aufnahme (Perspektive) und legen
// die Achsen fest; der MASSSTAB kommt aus dem Innenkreis (Loch der
// Vorsatzscheibe, LG Ø 30,5 / LP Ø 59,5 mm) - der ist genauer bekannt als
// der Schraubenabstand (an zwei Fotos lag der eingetragene Abstand von
// 141,5 mm um 1,3 % daneben). Der eingetragene Schraubenabstand dient nur
// noch als Kontrolle.
function computeGeometry() {
  F.geo = null;
  if (F.screws.length < 4 || !F.rim || F.rim.length < 20) return;
  const h = homography(F.screws.map(p => [p.x, p.y]), [[0, 0], [1, 0], [1, 1], [0, 1]]);
  if (!h) return;
  const pts = F.rim.map(p => applyH(h, p.x, p.y));
  const D = refDiameter();
  const ctrlW = parseFloat($('foto-screwW').value) || 0;
  const ctrlH = parseFloat($('foto-screwH').value) || 0;

  // Robuste Anpassung: Randpunkte, die deutlich vom Kreis abweichen (z.B.
  // ein Einschuss direkt am Kreisrand, Lichtreflex), werden iterativ
  // verworfen. Massstab je Achse aus den Ellipsen-Halbachsen.
  let keep = pts, fit = null, sx = 0, sy = 0, rms = 0, meanR = 0;
  for (let iter = 0; iter < 4; iter++) {
    fit = fitAxisEllipse(keep);
    if (!fit) return;
    sx = D / 2 / fit.au;
    sy = D / 2 / fit.av;
    const rad = keep.map(([u, v]) => Math.hypot((u - fit.uc) * sx, (v - fit.vc) * sy));
    meanR = rad.reduce((a, b) => a + b, 0) / rad.length;
    rms = Math.sqrt(rad.reduce((a, r) => a + (r - meanR) ** 2, 0) / rad.length);
    const lim = Math.max(2.5 * rms, 0.05);
    const next = keep.filter((_, i) => Math.abs(rad[i] - meanR) <= lim);
    if (next.length === keep.length || next.length < 20) break;
    keep = next;
  }
  F.geo = {
    h, hinv: invert3(h), uc: fit.uc, vc: fit.vc, sx, sy,
    rimRMS: rms, rimUsed: keep.length, rimTotal: pts.length,
    measuredD: 2 * meanR, ctrlW, ctrlH,
    // Seitenverhaeltnis des aus dem Kreis bestimmten Schraubenrechtecks
    // gegenueber dem eingetragenen (1 = passt)
    rectRatio: ctrlW && ctrlH ? (sx / sy) / (ctrlW / ctrlH) : 1,
  };
}

// Schrauben unabhaengig von der Klickreihenfolge in die von computeGeometry
// erwartete Ordnung TL, TR, BR, BL bringen: Sortierung nach Winkel um den
// Schwerpunkt (Bildkoordinaten, y nach unten -> aufsteigend = im
// Uhrzeigersinn), dann so rotieren, dass die Schraube mit kleinstem x+y
// (oben links) vorne steht. Ohne das ergab z.B. die Z-Reihenfolge (TL, TR,
// BL, BR) ein verdrehtes Viereck, dessen Homographie keine Ellipse mehr
// liefert ("Innenkreis noch nicht erkannt" trotz erkanntem Rand).
function orderScrews() {
  if (F.screws.length !== 4) return;
  const cx = F.screws.reduce((a, p) => a + p.x, 0) / 4;
  const cy = F.screws.reduce((a, p) => a + p.y, 0) / 4;
  const s = [...F.screws].sort((a, b) => Math.atan2(a.y - cy, a.x - cx) - Math.atan2(b.y - cy, b.x - cx));
  let k = 0;
  s.forEach((p, i) => { if (p.x + p.y < s[k].x + s[k].y) k = i; });
  F.screws = [...s.slice(k), ...s.slice(0, k)];
}

// Bildpixel -> Scheibenkoordinaten in mm (x rechts, y oben, Ursprung Kreismitte)
function imgToMM(x, y) {
  const g = F.geo;
  const [u, v] = applyH(g.h, x, y);
  return [(u - g.uc) * g.sx, -(v - g.vc) * g.sy];
}
function mmToImg(X, Y) {
  const g = F.geo;
  return applyH(g.hinv, X / g.sx + g.uc, -Y / g.sy + g.vc);
}
// lokaler Massstab (Bildpixel je mm) an einer Bildposition
function pxPerMM(x, y) {
  if (!F.geo) return Math.max(F.W, F.H) / 250;
  const [a, b] = imgToMM(x, y), [c, d] = imgToMM(x + 1, y), [e, f] = imgToMM(x, y + 1);
  const area = Math.abs((c - a) * (f - b) - (d - b) * (e - a)); // mm^2 je Pixel
  return 1 / Math.sqrt(area);
}

// ── Bild laden ──────────────────────────────────────────────────────────
// loadImage: Bild (Datei oder vom Server geladener Blob) dekodieren und als
// Arbeitsbild setzen - Markierungen werden zurueckgesetzt. false = Fehler.
async function loadImage(blob) {
  const url = URL.createObjectURL(blob);
  const img = new Image();
  img.src = url;
  try { await img.decode(); } catch (e) { setStatus('Bild konnte nicht geladen werden.'); return false; }
  F.img = img;
  F.W = img.naturalWidth;
  F.H = img.naturalHeight;
  const c = document.createElement('canvas');
  c.width = F.W; c.height = F.H;
  const ctx = c.getContext('2d', {willReadFrequently: true});
  ctx.drawImage(img, 0, 0);
  const d = ctx.getImageData(0, 0, F.W, F.H).data;
  F.lum = new Float32Array(F.W * F.H);
  F.sat = new Uint8Array(F.W * F.H);
  for (let i = 0, j = 0; i < F.lum.length; i++, j += 4) {
    const r = d[j], g = d[j + 1], b = d[j + 2];
    F.lum[i] = 0.299 * r + 0.587 * g + 0.114 * b;
    F.sat[i] = Math.max(r, g, b) - Math.min(r, g, b);
  }
  F.screws = []; F.rim = null; F.holes = []; F.geo = null; F.pairs = []; F.result = null; F.unmatchedSim = [];
  F.verify = null; F.verifyShot = null; F.zoneResult = null;
  $('foto-work').style.display = '';
  fitView();
  renderZoneResult();
  $('foto-oneclick-status').textContent = '';
  setMode('screws');
  renderPairs();
  renderResult();
  renderVerify();
  return true;
}

// loadFile: vom Nutzer gewaehltes Foto laden und zur Scheibe (Session) auf
// dem Server speichern - ein vorhandenes Foto wird dabei ersetzt.
async function loadFile(file) {
  F.stored = false; // Markierungen erst nach erfolgreichem Upload wieder speichern
  if (!(await loadImage(file))) return;
  if (!currentSessionId) return;
  const sid = currentSessionId;
  setPhotoInfo('Speichere Foto auf dem Server…');
  try {
    const r = await fetch(`/api/sessions/${sid}/photo`, {
      method: 'PUT', body: file,
      headers: {'Content-Type': file.type || 'image/jpeg', 'X-Filename': encodeURIComponent(file.name || '')},
    });
    if (!r.ok) throw new Error((await r.json().catch(() => ({}))).error || r.statusText);
    const meta = await r.json();
    if (sid !== currentSessionId) return;
    F.stored = true;
    F.savedMarkers = JSON.stringify(markerState());
    setPhotoInfo(photoInfoText(meta));
  } catch (e) {
    F.stored = false;
    setPhotoInfo('Foto konnte nicht gespeichert werden: ' + e.message, true);
  }
}

// ── Speicherung der Markierungen ────────────────────────────────────────
// Alles, was zum Wiederholen der Auswertung noetig ist (Bildkoordinaten
// bzw. Formularwerte). Format-Version v, falls es sich spaeter aendert.
function markerState() {
  const val = id => $(id) ? $(id).value : '';
  return {
    v: 1,
    screws: F.screws.map(p => ({x: p.x, y: p.y})),
    rim: F.rim ? F.rim.map(p => ({x: +p.x.toFixed(2), y: +p.y.toFixed(2)})) : null,
    holes: F.holes.map(p => ({x: p.x, y: p.y})),
    diameter: val('foto-diameter'), screw_w: val('foto-screwW'), screw_h: val('foto-screwH'),
    shot_from: val('foto-shot-from'), shot_to: val('foto-shot-to'), max_dist: val('foto-maxdist'),
    excluded: F.pairs.filter(p => !p.include).map(p => p.shot_no),
  };
}

let markerTimer = null;
// scheduleMarkerSave: nach jeder Aenderung (aufgerufen aus draw()) die
// Markierungen verzoegert speichern - nur wenn sich etwas geaendert hat.
function scheduleMarkerSave() {
  if (!F.stored || !currentSessionId || F.restoring) return;
  const json = JSON.stringify(markerState());
  if (json === F.savedMarkers) return;
  clearTimeout(markerTimer);
  const sid = currentSessionId;
  markerTimer = setTimeout(async () => {
    if (sid !== currentSessionId || !F.stored) return;
    const now = JSON.stringify(markerState());
    try {
      const r = await fetch(`/api/sessions/${sid}/photo/markers`, {method: 'PUT', headers: {'Content-Type': 'application/json'}, body: now});
      if (r.ok) F.savedMarkers = now;
    } catch (e) { /* naechste Aenderung versucht es erneut */ }
  }, 1200);
}

function photoInfoText(meta) {
  const t = new Date(meta.uploaded_at).toLocaleString('de-DE', {dateStyle: 'short', timeStyle: 'short'});
  return `Foto zu dieser Scheibe gespeichert (${meta.filename ? meta.filename + ', ' : ''}${(meta.size_bytes / 1048576).toFixed(1)} MB, ${t}). `
    + 'Markierungen werden automatisch gespeichert; ein neues Foto ersetzt dieses.';
}

function setPhotoInfo(t, err) {
  const el = $('foto-stored');
  if (!el) return;
  el.textContent = t || '';
  el.style.color = err ? '#ff6b6b' : '';
}

// restorePhoto: beim Laden einer Scheibe das gespeicherte Foto samt
// Markierungen holen. Ohne Foto wird die Foto-Kalibrierung geleert (ein
// Foto gehoert immer zu genau einer Scheibe).
async function restorePhoto(sid) {
  F.stored = false;
  F.zoneResult = null;
  renderZoneResult();
  if ($('foto-oneclick-status')) $('foto-oneclick-status').textContent = '';
  let meta = null;
  try {
    const r = await fetch(`/api/sessions/${sid}/photo/meta`);
    if (r.ok) meta = await r.json();
  } catch (e) { /* kein Foto */ }
  if (sid !== currentSessionId) return;
  if (!meta) {
    F.img = null; F.screws = []; F.rim = null; F.holes = []; F.geo = null;
    $('foto-work').style.display = 'none';
    if ($('foto-file')) $('foto-file').value = '';
    setPhotoInfo('Zu dieser Scheibe ist noch kein Foto gespeichert.');
    return;
  }
  setPhotoInfo('Lade gespeichertes Foto…');
  let blob;
  try {
    const r = await fetch(`/api/sessions/${sid}/photo`);
    if (!r.ok) throw new Error(r.statusText);
    blob = await r.blob();
  } catch (e) { setPhotoInfo('Gespeichertes Foto konnte nicht geladen werden: ' + e.message, true); return; }
  if (sid !== currentSessionId) return;
  F.restoring = true;
  try {
    if (!(await loadImage(blob))) return;
    const m = meta.markers;
    if (m) {
      const set = (id, v) => { if ($(id) && v != null && v !== '') $(id).value = v; };
      set('foto-diameter', m.diameter); set('foto-screwW', m.screw_w); set('foto-screwH', m.screw_h);
      if ($('foto-shot-from')) $('foto-shot-from').value = m.shot_from || '';
      if ($('foto-shot-to')) $('foto-shot-to').value = m.shot_to || '';
      set('foto-maxdist', m.max_dist);
      F.screws = (m.screws || []).map(p => ({x: p.x, y: p.y}));
      F.rim = m.rim ? m.rim.map(p => ({x: p.x, y: p.y})) : null;
      F.holes = (m.holes || []).map(p => ({x: p.x, y: p.y}));
      computeGeometry();
      updateGeoInfo();
      setMode(F.screws.length < 4 ? 'screws' : (!F.geo ? 'circle' : 'holes'));
    }
    F.stored = true;
    setPhotoInfo(photoInfoText(meta));
    // Zuordnung wiederherstellen (inkl. abgewaehlter Schuesse)
    if (m && F.geo && F.holes.length) {
      await matchShots();
      const ex = new Set(m.excluded || []);
      F.pairs.forEach(p => { if (ex.has(p.shot_no)) { p.include = false; p.userSet = true; } });
      renderPairs(); draw();
    }
  } finally {
    F.restoring = false;
    F.savedMarkers = JSON.stringify(markerState());
  }
}

// ── Erkennung ───────────────────────────────────────────────────────────
// Schraubenmitte: Schwerpunkt der farblosen (Metall), nicht dunklen Pixel im
// Umkreis - Papier (beige) und Rahmen sind deutlich farbiger. Klappt das
// nicht eindeutig, bleibt der Klickpunkt. Standardmaessig AUS: am Testfoto
// zog der graue Schattenwurf unter der Unterlegscheibe den Schwerpunkt um
// ~1.2mm weg, und ein solcher Fehler an EINER Schraube verschiebt die
// Lochpositionen bereits um bis zu ~0.35mm - ein sorgfaeltiger Klick bei
// starker Vergroesserung ist genauer.
function snapScrew(x, y) {
  if (!$('foto-snap-screws').checked) return {x, y};
  const r = Math.round(Math.max(F.W, F.H) * 0.025);
  const sats = [];
  for (let yy = Math.max(0, y - r); yy <= Math.min(F.H - 1, y + r); yy += 2)
    for (let xx = Math.max(0, x - r); xx <= Math.min(F.W - 1, x + r); xx += 2) sats.push(F.sat[yy * F.W + xx]);
  const satThr = Math.min(30, median(sats) * 0.5);
  // Fenster iterativ auf den Schwerpunkt nachfuehren (Mean-Shift) - sonst
  // zieht ein ungenauer Klick den Schwerpunkt mit, weil das Fenster die
  // Schraube nur teilweise erfasst.
  let cx = x, cy = y;
  for (let iter = 0; iter < 8; iter++) {
    let sx = 0, sy = 0, n = 0, total = 0;
    for (let yy = Math.max(0, Math.round(cy - r)); yy <= Math.min(F.H - 1, cy + r); yy++) {
      for (let xx = Math.max(0, Math.round(cx - r)); xx <= Math.min(F.W - 1, cx + r); xx++) {
        if ((xx - cx) ** 2 + (yy - cy) ** 2 > r * r) continue;
        total++;
        const i = yy * F.W + xx;
        if (F.sat[i] < satThr && F.lum[i] > 50) { sx += xx; sy += yy; n++; }
      }
    }
    if (n < 15 || n > total * 0.6) return {x, y};
    const nx = sx / n, ny = sy / n;
    const moved = Math.hypot(nx - cx, ny - cy);
    cx = nx; cy = ny;
    if (moved < 0.05) break;
  }
  // mehr als ein halbes Fenster weggelaufen: eher etwas anderes erwischt
  if (Math.hypot(cx - x, cy - y) > r) return {x, y};
  return {x: cx, y: cy};
}

// Innenkreis: Flood-Fill der dunklen Flaeche ab dem Klickpunkt (Schwelle per
// Otsu zwischen Loch und Papier), aeusserer Rand mit Subpixel-Interpolation.
function detectRim(x0, y0) {
  const W = F.W, H = F.H, L = F.lum;
  const half = Math.round(Math.min(W, H) * 0.25);
  const sample = [];
  for (let y = Math.max(0, y0 - half); y < Math.min(H, y0 + half); y += 3)
    for (let x = Math.max(0, x0 - half); x < Math.min(W, x0 + half); x += 3) sample.push(L[y * W + x]);
  const thr = otsu(sample);
  const seed = Math.round(y0) * W + Math.round(x0);
  if (L[seed] >= thr) return 'Bitte in die schwarze Fläche des Innenkreises klicken.';

  const inside = new Uint8Array(W * H);
  const stack = [seed];
  inside[seed] = 1;
  let n = 0, minX = W, maxX = 0, minY = H, maxY = 0;
  const limit = W * H * 0.35;
  while (stack.length) {
    const i = stack.pop();
    const x = i % W, y = (i - x) / W;
    if (x === 0 || y === 0 || x === W - 1 || y === H - 1) return 'Die dunkle Fläche reicht bis zum Bildrand - bitte in den Innenkreis klicken (nicht in einen dunklen Schlitz daneben).';
    if (++n > limit) return 'Dunkle Fläche zu groß - Kontrast zwischen Kreis und Papier zu gering?';
    if (x < minX) minX = x; if (x > maxX) maxX = x; if (y < minY) minY = y; if (y > maxY) maxY = y;
    for (const j of [i + 1, i - 1, i + W, i - W]) {
      if (!inside[j] && L[j] < thr) { inside[j] = 1; stack.push(j); }
    }
  }
  // Aussenbereich innerhalb der Bounding-Box (+1) fluten, damit nur der
  // AEUSSERE Rand zaehlt (hellere Stellen im Loch, z.B. Reflexe auf dem
  // Band, erzeugen sonst innere Randpunkte).
  const bx0 = minX - 1, by0 = minY - 1, bw = maxX - minX + 3, bh = maxY - minY + 3;
  const outside = new Uint8Array(bw * bh);
  const st2 = [0];
  outside[0] = 1;
  while (st2.length) {
    const k = st2.pop();
    const lx = k % bw, ly = (k - lx) / bw;
    for (const [dx, dy] of [[1, 0], [-1, 0], [0, 1], [0, -1]]) {
      const nx = lx + dx, ny = ly + dy;
      if (nx < 0 || ny < 0 || nx >= bw || ny >= bh) continue;
      const kk = ny * bw + nx;
      if (!outside[kk] && !inside[(by0 + ny) * W + bx0 + nx]) { outside[kk] = 1; st2.push(kk); }
    }
  }
  const rim = [];
  for (let ly = 1; ly < bh - 1; ly++) {
    for (let lx = 1; lx < bw - 1; lx++) {
      const x = bx0 + lx, y = by0 + ly, i = y * W + x;
      if (!inside[i]) continue;
      for (const [dx, dy] of [[1, 0], [-1, 0], [0, 1], [0, -1]]) {
        if (!outside[(ly + dy) * bw + lx + dx]) continue;
        const j = i + dy * W + dx;
        const t = Math.max(0, Math.min(1, (thr - L[i]) / ((L[j] - L[i]) || 1)));
        rim.push({x: x + t * dx, y: y + t * dy});
      }
    }
  }
  if (rim.length < 20) return 'Kreisrand nicht erkannt.';
  F.rim = rim;
  return null;
}

// Einschussloch am Klickpunkt einrasten: Schwerpunkt der zusammen-
// haengenden dunklen Flaeche (deutlich dunkler als das umgebende Papier),
// die dem Klick am naechsten liegt.
function snapHole(x, y) {
  if (!$('foto-snap-holes').checked) return {x, y};
  if (insideDark(x, y)) return snapBrightHole(x, y);
  const W = F.W, L = F.lum;
  const r = Math.round(pxPerMM(x, y) * 3.5);
  const x0 = Math.max(0, Math.round(x - r)), x1 = Math.min(F.W - 1, Math.round(x + r));
  const y0 = Math.max(0, Math.round(y - r)), y1 = Math.min(F.H - 1, Math.round(y + r));
  const vals = [];
  for (let yy = y0; yy <= y1; yy++) for (let xx = x0; xx <= x1; xx++) vals.push(L[yy * W + xx]);
  const thr = median(vals) * 0.6;
  let start = -1, bestD = Infinity;
  for (let yy = y0; yy <= y1; yy++) for (let xx = x0; xx <= x1; xx++) {
    const d = (xx - x) ** 2 + (yy - y) ** 2;
    if (L[yy * W + xx] < thr && d < bestD) { bestD = d; start = yy * W + xx; }
  }
  if (start < 0) return {x, y};
  const seen = new Set([start]);
  const stack = [start];
  let sx = 0, sy = 0, n = 0;
  while (stack.length) {
    const i = stack.pop();
    const xx = i % W, yy = (i - xx) / W;
    sx += xx; sy += yy; n++;
    for (const j of [i + 1, i - 1, i + W, i - W]) {
      const jx = j % W, jy = (j - jx) / W;
      if (jx < x0 || jx > x1 || jy < y0 || jy > y1 || seen.has(j) || L[j] >= thr) continue;
      seen.add(j); stack.push(j);
    }
  }
  return {x: sx / n, y: sy / n};
}

// ── Löcher im Schwarzen (Innenkreis) ────────────────────────────────────
// Im schwarzen Spiegel erscheinen Einschusslöcher bei hellem Hintergrund
// HELLER als die Umgebung. Die weißen Ringlinien im Schwarzen (LP: Ringe
// 7-10) sind ebenfalls hell, aber dünn: eine Erosion um ~0,4 mm entfernt
// sie, ein Loch (Ø ~4,5 mm) bleibt als kompakter Fleck erhalten.

// insideDark: liegt der Bildpunkt innerhalb des markierten Innenkreises?
function insideDark(x, y) {
  if (!F.geo) return false;
  const [X, Y] = imgToMM(x, y);
  return Math.hypot(X, Y) < F.geo.measuredD / 2;
}

// Helligkeit von Schwarz (innen) und Papier (außen) aus Ringzonen um den
// Innenkreis; je Geometrie zwischengespeichert.
function darkPaperLevels() {
  const g = F.geo;
  if (g.levels) return g.levels;
  const R = g.measuredD / 2, W = F.W, L = F.lum;
  const [cx, cy] = mmToImg(0, 0);
  const ext = (R + 15) * pxPerMM(cx, cy);
  const dark = [], paper = [];
  const x0 = Math.max(0, Math.floor(cx - ext)), x1 = Math.min(W - 1, Math.ceil(cx + ext));
  const y0 = Math.max(0, Math.floor(cy - ext)), y1 = Math.min(F.H - 1, Math.ceil(cy + ext));
  const step = Math.max(1, Math.round((x1 - x0) / 400));
  for (let yy = y0; yy <= y1; yy += step) {
    for (let xx = x0; xx <= x1; xx += step) {
      const [X, Y] = imgToMM(xx, yy);
      const r = Math.hypot(X, Y);
      if (r < R - 1) dark.push(L[yy * W + xx]);
      else if (r > R + 3 && r < R + 15) paper.push(L[yy * W + xx]);
    }
  }
  g.levels = {dark: median(dark), paper: median(paper)};
  return g.levels;
}

// brightBlobs: helle, nach Erosion kompakte Flecken im Rechteck x0..x1,
// y0..y1, nur Pixel mit inMask(x, y). Liefert Schwerpunkt (Bildpixel),
// Ersatzdurchmesser (mm, Erosion zurückgerechnet) und ob der Fleck die
// Maskengrenze berührt (dann ist er angeschnitten).
function brightBlobs(x0, y0, x1, y1, thr, inMask) {
  const W = F.W, L = F.lum;
  const bw = x1 - x0 + 1, bh = y1 - y0 + 1;
  const ppm = pxPerMM((x0 + x1) / 2, (y0 + y1) / 2);
  const e = Math.max(1, Math.round(0.4 * ppm));
  const mask = new Uint8Array(bw * bh), bright = new Uint8Array(bw * bh);
  for (let ly = 0; ly < bh; ly++) {
    for (let lx = 0; lx < bw; lx++) {
      if (!inMask(x0 + lx, y0 + ly)) continue;
      mask[ly * bw + lx] = 1;
      if (L[(y0 + ly) * W + x0 + lx] > thr) bright[ly * bw + lx] = 1;
    }
  }
  // Erosion (quadratisches Element 2e+1, separierbar): horizontal, dann vertikal
  const runMin = (src, dst, len, n, stride, step) => {
    for (let k = 0; k < n; k++) {
      const base = k * stride;
      let run = 0;
      const tmp = new Uint8Array(len);
      for (let i = 0; i < len; i++) { run = src[base + i * step] ? run + 1 : 0; tmp[i] = run > 2 * e ? 1 : 0; }
      for (let i = 0; i < len; i++) dst[base + i * step] = (i + e < len && tmp[i + e]) ? 1 : 0;
    }
  };
  const h = new Uint8Array(bw * bh), er = new Uint8Array(bw * bh);
  runMin(bright, h, bw, bh, bw, 1);
  runMin(h, er, bh, bw, 1, bw);
  const label = new Uint8Array(bw * bh);
  const out = [];
  for (let k = 0; k < bw * bh; k++) {
    if (!er[k] || label[k]) continue;
    const stack = [k];
    label[k] = 1;
    let sx = 0, sy = 0, n = 0, edge = false;
    const pts = [];
    // ausserhalb der Maske (oder des Rechtecks) im Abstand e+1: der
    // urspruengliche (nicht erodierte) Fleck reicht bis an die Grenze
    const outside = (ax, ay) => ax < 0 || ay < 0 || ax >= bw || ay >= bh || !mask[ay * bw + ax];
    const reach = e + 1;
    while (stack.length) {
      const q = stack.pop();
      const lx = q % bw, ly = (q - lx) / bw;
      sx += lx; sy += ly; n++;
      pts.push(lx, ly);
      if (!edge && (outside(lx + reach, ly) || outside(lx - reach, ly) || outside(lx, ly + reach) || outside(lx, ly - reach))) edge = true;
      for (const [dx, dy] of [[1, 0], [-1, 0], [0, 1], [0, -1]]) {
        const nx = lx + dx, ny = ly + dy;
        if (nx < 0 || ny < 0 || nx >= bw || ny >= bh) continue;
        const qq = ny * bw + nx;
        if (label[qq] || !er[qq]) continue;
        label[qq] = 1; stack.push(qq);
      }
    }
    const mx = sx / n, my = sy / n;
    let maxR = 0, cxx = 0, cyy = 0, cxy = 0;
    for (let i = 0; i < pts.length; i += 2) {
      const dx = pts[i] - mx, dy = pts[i + 1] - my;
      maxR = Math.max(maxR, Math.hypot(dx, dy));
      cxx += dx * dx; cyy += dy * dy; cxy += dx * dy;
    }
    const compact = n / (Math.PI * Math.max(1, maxR) ** 2);
    // Achsverhaeltnis aus den zweiten Momenten (1 = rund; zwei
    // ueberlappende Loecher ergeben einen laenglichen Fleck)
    const tr = (cxx + cyy) / n, dt = Math.sqrt(((cxx - cyy) / n) ** 2 + 4 * (cxy / n) ** 2);
    const elong = Math.sqrt((tr + dt) / Math.max(1e-9, tr - dt));
    const dEq = (2 * Math.sqrt(n / Math.PI) + 2 * e) / ppm;
    out.push({x: x0 + mx, y: y0 + my, dEq, compact, elong, edge});
  }
  return out;
}

// Loch im Schwarzen am Klickpunkt einrasten: nächster heller Fleck.
function snapBrightHole(x, y) {
  const lv = darkPaperLevels();
  const thr = lv.dark + 0.5 * (lv.paper - lv.dark);
  const r = Math.round(pxPerMM(x, y) * 4);
  const x0 = Math.max(0, Math.round(x - r)), x1 = Math.min(F.W - 1, Math.round(x + r));
  const y0 = Math.max(0, Math.round(y - r)), y1 = Math.min(F.H - 1, Math.round(y + r));
  const R = F.geo.measuredD / 2;
  const blobs = brightBlobs(x0, y0, x1, y1, thr, (xx, yy) => { const [X, Y] = imgToMM(xx, yy); return Math.hypot(X, Y) < R - 0.2; });
  let best = null, bd = Infinity;
  for (const b of blobs) {
    const d = Math.hypot(b.x - x, b.y - y);
    if (b.dEq >= 1.2 && d < bd) { bd = d; best = b; }
  }
  // angeschnittene oder ueberlappende Flecken: Schwerpunkt waere falsch,
  // dann gilt der Klick (stark vergroessert setzen)
  if (!best || bd >= r || best.edge || best.dEq > 6 || best.elong > 1.35) return {x, y};
  return {x: best.x, y: best.y};
}

// Automatische Suche heller Löcher im Innenkreis. Liefert die gefundenen
// Löcher und die Zahl der am Kreisrand angeschnittenen (nicht übernommen).
function detectDarkAreaHoles() {
  const g = F.geo, R = g.measuredD / 2;
  const lv = darkPaperLevels();
  if (lv.paper - lv.dark < 30) return {found: [], cut: 0, big: 0, lowContrast: true};
  const thr = lv.dark + 0.5 * (lv.paper - lv.dark);
  let x0 = F.W, x1 = 0, y0 = F.H, y1 = 0;
  for (let k = 0; k < 64; k++) {
    const [px, py] = mmToImg(R * Math.cos(k * Math.PI / 32), R * Math.sin(k * Math.PI / 32));
    x0 = Math.min(x0, px); x1 = Math.max(x1, px); y0 = Math.min(y0, py); y1 = Math.max(y1, py);
  }
  x0 = Math.max(0, Math.floor(x0) - 2); y0 = Math.max(0, Math.floor(y0) - 2);
  x1 = Math.min(F.W - 1, Math.ceil(x1) + 2); y1 = Math.min(F.H - 1, Math.ceil(y1) + 2);
  const blobs = brightBlobs(x0, y0, x1, y1, thr, (xx, yy) => { const [X, Y] = imgToMM(xx, yy); return Math.hypot(X, Y) < R - 0.2; });
  const found = [];
  let cut = 0, big = 0;
  for (const b of blobs) {
    if (b.dEq < 1.5 || b.compact < 0.35) continue;
    if (b.edge) { cut++; continue; }
    // deutlich groesser als ein Diabolo (4,5 mm): wahrscheinlich mehrere
    // ueberlappende Loecher - deren Schwerpunkt waere keines davon
    if (b.dEq > 6 || b.elong > 1.35) { big++; continue; }
    found.push({x: b.x, y: b.y});
  }
  return {found, cut, big, lowContrast: false};
}

// Automatische Lochsuche innerhalb des Schraubenrechtecks (abseits der
// Schrauben): ausserhalb des Innenkreises dunkle, zusammenhaengende Flecken
// mit plausibler Groesse fuer ein Diabolo-Loch, innerhalb helle Flecken
// (detectDarkAreaHoles).
function detectHoles() {
  if (!F.geo) { setStatus('Zuerst Schrauben und Innenkreis markieren.'); return; }
  const W = F.W, L = F.lum, g = F.geo;
  const R = g.measuredD / 2;
  const xs = F.screws.map(p => p.x), ys = F.screws.map(p => p.y);
  const x0 = Math.max(0, Math.floor(Math.min(...xs))), x1 = Math.min(W - 1, Math.ceil(Math.max(...xs)));
  const y0 = Math.max(0, Math.floor(Math.min(...ys))), y1 = Math.min(F.H - 1, Math.ceil(Math.max(...ys)));
  const bw = x1 - x0 + 1, bh = y1 - y0 + 1;
  const screwMM = F.screws.map(p => imgToMM(p.x, p.y));
  const ok = new Uint8Array(bw * bh);
  const paper = [];
  for (let yy = y0; yy <= y1; yy++) {
    for (let xx = x0; xx <= x1; xx++) {
      const [u, v] = applyH(g.h, xx, yy);
      if (u < 0.02 || u > 0.98 || v < 0.02 || v > 0.98) continue;
      const [X, Y] = imgToMM(xx, yy);
      if (Math.hypot(X, Y) < R + 1.5) continue;
      if (screwMM.some(([sx, sy]) => Math.hypot(X - sx, Y - sy) < 8)) continue;
      ok[(yy - y0) * bw + xx - x0] = 1;
      if (((xx + yy) & 7) === 0) paper.push(L[yy * W + xx]);
    }
  }
  const thr = median(paper) * 0.6;
  const label = new Uint8Array(bw * bh);
  const found = [];
  for (let k = 0; k < bw * bh; k++) {
    if (!ok[k] || label[k] || L[(y0 + ((k / bw) | 0)) * W + x0 + (k % bw)] >= thr) continue;
    const stack = [k];
    label[k] = 1;
    let sx = 0, sy = 0, n = 0;
    while (stack.length) {
      const q = stack.pop();
      const lx = q % bw, ly = (q - lx) / bw;
      sx += lx; sy += ly; n++;
      for (const [dx, dy] of [[1, 0], [-1, 0], [0, 1], [0, -1]]) {
        const nx = lx + dx, ny = ly + dy;
        if (nx < 0 || ny < 0 || nx >= bw || ny >= bh) continue;
        const qq = ny * bw + nx;
        if (label[qq] || !ok[qq] || L[(y0 + ny) * W + x0 + nx] >= thr) continue;
        label[qq] = 1; stack.push(qq);
      }
    }
    const cx = x0 + sx / n, cy = y0 + sy / n;
    const ppm = pxPerMM(cx, cy);
    const dEq = 2 * Math.sqrt(n / Math.PI) / ppm;
    if (dEq >= 1.5 && dEq <= 7) found.push({x: cx, y: cy});
  }
  const inner = detectDarkAreaHoles();
  found.push(...inner.found);
  let added = 0;
  for (const h of found) {
    const ppm = pxPerMM(h.x, h.y);
    if (!F.holes.some(o => Math.hypot(o.x - h.x, o.y - h.y) < 2 * ppm)) { F.holes.push(h); added++; }
  }
  F.pairs = []; F.result = null;
  renderPairs(); renderResult();
  let note = ` (davon ${inner.found.length} im Innenkreis)`;
  if (inner.lowContrast) note = ' (Innenkreis: zu wenig Kontrast zwischen Schwarz und Papier, dort nicht gesucht)';
  if (inner.cut) note += `; ${inner.cut} am Kreisrand angeschnitten - bitte von Hand setzen`;
  if (inner.big) note += `; ${inner.big} Fleck(en) im Innenkreis zu groß oder länglich (überlappende Löcher?) - bitte von Hand setzen`;
  setStatus(`${found.length} Loch/Löcher gefunden${note}, ${added} neu hinzugefügt. Bitte prüfen: Klick auf ein Loch entfernt es, Klick daneben fügt eins hinzu.`);
  draw();
}

// ── Interaktion ─────────────────────────────────────────────────────────
function setStatus(t) { $('foto-status').textContent = t; }

function modeHint() {
  if (F.mode === 'screws') {
    return F.screws.length < 4
      ? `Schraube ${F.screws.length + 1} von 4 (Reihenfolge beliebig) stark vergrößert (Mausrad) exakt in der Mitte anklicken – 1 mm Fehler an einer Schraube verschiebt Löcher um bis zu ~0,3 mm. Ziehen = verschieben.`
      : 'Alle 4 Schrauben gesetzt - ein weiterer Klick versetzt die nächstgelegene Schraube.';
  }
  if (F.mode === 'circle') return 'In die schwarze Fläche des Innenkreises klicken - der Rand wird automatisch erkannt. Bei Ringlinien im Schwarzen (LP-Spiegel) in den äußersten schwarzen Ring klicken.';
  if (F.mode === 'holes') return 'Einschussloch anklicken (rastet auf die Lochmitte ein, im Schwarzen auf den hellen Fleck) - Klick auf ein markiertes Loch entfernt es.';
  return 'Mausrad = Zoom, Ziehen = verschieben.';
}

function setMode(m) {
  F.mode = m;
  document.querySelectorAll('#foto-modes button').forEach(b => b.classList.toggle('foto-active', b.dataset.mode === m));
  setStatus(modeHint());
  draw();
}

function onImageClick(x, y) {
  if (F.mode === 'screws') {
    const p = snapScrew(x, y);
    if (F.screws.length < 4) F.screws.push(p);
    else {
      let k = 0;
      F.screws.forEach((s, i) => { if (Math.hypot(s.x - x, s.y - y) < Math.hypot(F.screws[k].x - x, F.screws[k].y - y)) k = i; });
      F.screws[k] = p;
    }
    orderScrews();
    if (F.screws.length === 4 && !F.rim) { computeGeometry(); setMode('circle'); return; }
  } else if (F.mode === 'circle') {
    const err = detectRim(x, y);
    if (err) { setStatus(err); return; }
    computeGeometry();
    if (F.screws.length === 4) { setMode('holes'); updateGeoInfo(); return; }
  } else if (F.mode === 'holes') {
    const ppm = pxPerMM(x, y);
    const k = F.holes.findIndex(h => Math.hypot(h.x - x, h.y - y) < Math.max(2.5 * ppm, 8 / F.view.s));
    if (k >= 0) F.holes.splice(k, 1);
    else F.holes.push(snapHole(x, y));
    F.pairs = []; F.result = null;
    renderPairs(); renderResult();
  }
  computeGeometry();
  updateGeoInfo();
  setStatus(modeHint());
  draw();
}

function updateGeoInfo() {
  const el = $('foto-geo');
  const g = F.geo;
  if (!g) {
    el.textContent = F.screws.length < 4 ? `${F.screws.length}/4 Schrauben markiert.`
      : !F.rim ? 'Innenkreis noch nicht erkannt.'
      : 'Innenkreis erkannt, Geometrie aber nicht berechenbar – bitte die 4 Schraubenpositionen prüfen.';
    return;
  }
  const quality = g.rimRMS < 0.15 ? 'sehr gut' : g.rimRMS < 0.3 ? 'gut' : 'mäßig - Schrauben/Kreis prüfen';
  const parts = [
    `Maßstab aus Innenkreis Ø ${refDiameter()} mm → Schraubenrechteck ${g.sx.toFixed(2)} × ${g.sy.toFixed(2)} mm`
      + (g.ctrlW && g.ctrlH ? ` (eingetragen ${g.ctrlW} × ${g.ctrlH} mm, nur Kontrolle)` : ''),
    `Kreisrand: Abweichung ${g.rimRMS.toFixed(3)} mm RMS (${quality}), ${g.rimUsed}/${g.rimTotal} Randpunkte`,
  ];
  // Kontrollen: Seitenverhaeltnis des Schraubenrechtecks (> 0,5 % daneben =
  // Schrauben ungenau markiert) und Groesse (> 3 % daneben = vermutlich
  // falscher Innenkreis-Durchmesser, z.B. LG/LP verwechselt).
  const warn = [];
  if (Math.abs(g.rectRatio - 1) > 0.005)
    warn.push(`⚠ Schraubenrechteck passt nicht zum runden Kreis (Seitenverhältnis ${((g.rectRatio - 1) * 100).toFixed(1)} % daneben) – Schraubenmitten stark vergrößert genauer setzen`);
  if (g.ctrlW && Math.abs(g.sx / g.ctrlW - 1) > 0.03)
    warn.push(`⚠ Schraubenabstand aus dem Kreis weicht ${((g.sx / g.ctrlW - 1) * 100).toFixed(1)} % vom eingetragenen ab – Ø Innenkreis prüfen (LG 30,5 / LP 59,5 mm)`);
  el.style.color = warn.length ? '#ffb347' : '';
  parts.unshift(...warn);
  const cx = (0.5 - g.uc) * g.sx, cy = -(0.5 - g.vc) * g.sy;
  parts.push(`Mitte Schraubenrechteck liegt bei x=${cx.toFixed(2)} / y=${cy.toFixed(2)} mm`);
  parts.push(`${F.holes.length} Loch/Löcher markiert`);
  el.textContent = parts.join(' · ');
}

// ── Ansicht ─────────────────────────────────────────────────────────────
function canvasSize() {
  const c = $('foto-canvas');
  const w = c.parentElement.clientWidth;
  const h = Math.min(Math.round(window.innerHeight * 0.72), Math.round(w * (F.H / F.W || 0.75)));
  return [w, Math.max(200, h)];
}
function fitView() {
  if (!F.img) return;
  const [w, h] = canvasSize();
  const s = Math.min(w / F.W, h / F.H);
  F.view = {s, ox: -(w / s - F.W) / 2, oy: -(h / s - F.H) / 2};
  draw();
}
const toS = (x, y) => [(x - F.view.ox) * F.view.s, (y - F.view.oy) * F.view.s];

function draw() {
  scheduleMarkerSave();
  const c = $('foto-canvas');
  if (!F.img || !c) return;
  const [w, h] = canvasSize();
  const dpr = window.devicePixelRatio || 1;
  if (c.width !== Math.round(w * dpr) || c.height !== Math.round(h * dpr)) {
    c.width = Math.round(w * dpr); c.height = Math.round(h * dpr);
    c.style.width = w + 'px'; c.style.height = h + 'px';
  }
  const ctx = c.getContext('2d');
  const {s, ox, oy} = F.view;
  ctx.setTransform(1, 0, 0, 1, 0, 0);
  ctx.fillStyle = '#000';
  ctx.fillRect(0, 0, c.width, c.height);
  ctx.setTransform(dpr * s, 0, 0, dpr * s, -ox * s * dpr, -oy * s * dpr);
  ctx.imageSmoothingEnabled = s < 2;
  ctx.drawImage(F.img, 0, 0);
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);

  const line = (pts, color, width = 1.5, close = false, dash = null) => {
    ctx.beginPath();
    pts.forEach(([x, y], i) => { const [a, b] = toS(x, y); i ? ctx.lineTo(a, b) : ctx.moveTo(a, b); });
    if (close) ctx.closePath();
    ctx.strokeStyle = color; ctx.lineWidth = width; ctx.setLineDash(dash || []); ctx.stroke(); ctx.setLineDash([]);
  };
  const ring = (x, y, rPx, color, width = 2) => {
    const [a, b] = toS(x, y);
    ctx.beginPath(); ctx.arc(a, b, Math.max(4, rPx * s), 0, 2 * Math.PI);
    ctx.strokeStyle = color; ctx.lineWidth = width; ctx.stroke();
  };
  const label = (x, y, text, color, dx = 8, dy = -8) => {
    const [a, b] = toS(x, y);
    ctx.font = 'bold 12px system-ui';
    ctx.lineWidth = 3; ctx.strokeStyle = 'rgba(0,0,0,.8)'; ctx.strokeText(text, a + dx, b + dy);
    ctx.fillStyle = color; ctx.fillText(text, a + dx, b + dy);
  };
  const cross = (x, y, color, size = 6) => {
    const [a, b] = toS(x, y);
    ctx.beginPath(); ctx.moveTo(a - size, b); ctx.lineTo(a + size, b); ctx.moveTo(a, b - size); ctx.lineTo(a, b + size);
    ctx.strokeStyle = color; ctx.lineWidth = 2; ctx.stroke();
  };

  // Schrauben + Rechteck
  if (F.screws.length > 1) line(F.screws.map(p => [p.x, p.y]), 'rgba(255,215,0,.6)', 1, F.screws.length === 4, [5, 4]);
  F.screws.forEach((p, i) => { cross(p.x, p.y, '#ffd700', 9); label(p.x, p.y, String(i + 1), '#ffd700'); });

  if (F.geo) {
    const g = F.geo, R = g.measuredD / 2;
    const circ = [];
    for (let k = 0; k <= 96; k++) { const a = k / 96 * 2 * Math.PI; circ.push(mmToImg(R * Math.cos(a), R * Math.sin(a))); }
    line(circ, '#3dcc6e', 2);
    // Achsen und 10-mm-Ringe als Kontrolle der Entzerrung
    const ext = R * 1.6;
    line([mmToImg(-ext, 0), mmToImg(ext, 0)], 'rgba(61,204,110,.7)', 1);
    line([mmToImg(0, -ext), mmToImg(0, ext)], 'rgba(61,204,110,.7)', 1);
    for (let rr = Math.ceil((R + 5) / 10) * 10; rr <= 70; rr += 10) {
      const pts = [];
      for (let k = 0; k <= 96; k++) { const a = k / 96 * 2 * Math.PI; pts.push(mmToImg(rr * Math.cos(a), rr * Math.sin(a))); }
      line(pts, 'rgba(61,204,110,.25)', 1, false, [3, 5]);
    }
  } else if (F.rim) {
    ctx.fillStyle = '#3dcc6e';
    for (let k = 0; k < F.rim.length; k += 4) { const [a, b] = toS(F.rim[k].x, F.rim[k].y); ctx.fillRect(a - 1, b - 1, 2, 2); }
  }

  // Loecher, Zuordnung, Neuberechnung
  const pairByHole = new Map(F.pairs.map(p => [p.hole, p]));
  F.holes.forEach((hl, i) => {
    const rPx = PELLET_R_MM * pxPerMM(hl.x, hl.y);
    const p = pairByHole.get(i);
    const color = p ? (p.include ? '#3dcc6e' : '#888') : '#ff5050';
    ring(hl.x, hl.y, rPx, color);
    if (p && F.geo) {
      const res = F.result?.shots.find(r => r.shot_no === p.shot_no);
      const sim = mmToImg(p.sim_x, p.sim_y);
      line([[hl.x, hl.y], sim], 'rgba(123,167,255,.9)', 1.5);
      cross(sim[0], sim[1], '#7ba7ff');
      if (res && res.after_x_mm != null) {
        const aft = mmToImg(res.after_x_mm, res.after_y_mm);
        cross(aft[0], aft[1], '#e070ff', 5);
      }
      label(hl.x, hl.y, '#' + p.shot_no, color);
    }
  });
  // Pruefung: Geraeteposition (orange x) je Schuss, Messpunkte (Schnittpunkte)
  // des ausgewaehlten bzw. aller Schuesse
  if (F.geo && F.verify) {
    const all = $('foto-allpoints') && $('foto-allpoints').checked;
    const xmark = (x, y, color, size = 5) => {
      const [a, b] = toS(x, y);
      ctx.beginPath(); ctx.moveTo(a - size, b - size); ctx.lineTo(a + size, b + size);
      ctx.moveTo(a - size, b + size); ctx.lineTo(a + size, b - size);
      ctx.strokeStyle = color; ctx.lineWidth = 2; ctx.stroke();
    };
    const dot = (x, y, color, r) => {
      const [a, b] = toS(x, y);
      ctx.beginPath(); ctx.arc(a, b, r, 0, 2 * Math.PI); ctx.fillStyle = color; ctx.fill();
    };
    for (const v of F.verify.shots) {
      const dev = mmToImg(v.device_x_mm, v.device_y_mm);
      xmark(dev[0], dev[1], '#ff9f40');
      if (all || v.shot_no === F.verifyShot) {
        for (const c of v.candidates) {
          const [cx, cy] = mmToImg(c.corr_x_mm, c.corr_y_mm);
          dot(cx, cy, c.in_cluster ? 'rgba(61,214,255,.95)' : 'rgba(61,214,255,.45)', c.best ? 4 : 2.5);
          if (v.shot_no === F.verifyShot) {
            const [rx, ry] = mmToImg(c.x_mm, c.y_mm);
            dot(rx, ry, 'rgba(255,215,0,.6)', 2);
          }
        }
      }
      if (v.shot_no === F.verifyShot) {
        const t = mmToImg(v.truth_x_mm, v.truth_y_mm);
        line([t, dev], 'rgba(255,159,64,.9)', 1.5, false, [4, 3]);
      }
    }
  }

  if (F.geo) F.unmatchedSim.forEach(u => {
    const [x, y] = mmToImg(u.x, u.y);
    cross(x, y, 'rgba(123,167,255,.5)');
    label(x, y, '#' + u.shot_no + '?', 'rgba(123,167,255,.8)');
  });
}

function initCanvas() {
  const c = $('foto-canvas');
  let drag = null;
  c.addEventListener('pointerdown', e => {
    drag = {x: e.offsetX, y: e.offsetY, ox: F.view.ox, oy: F.view.oy, moved: false};
    c.setPointerCapture(e.pointerId);
  });
  c.addEventListener('pointermove', e => {
    if (!drag) return;
    const dx = e.offsetX - drag.x, dy = e.offsetY - drag.y;
    if (!drag.moved && Math.hypot(dx, dy) < 5) return;
    drag.moved = true;
    F.view.ox = drag.ox - dx / F.view.s;
    F.view.oy = drag.oy - dy / F.view.s;
    draw();
  });
  c.addEventListener('pointerup', e => {
    if (drag && !drag.moved && F.img && F.mode !== 'pan') {
      onImageClick(F.view.ox + e.offsetX / F.view.s, F.view.oy + e.offsetY / F.view.s);
    }
    drag = null;
  });
  c.addEventListener('wheel', e => {
    if (!F.img) return;
    e.preventDefault();
    const f = e.deltaY < 0 ? 1.25 : 0.8;
    const ix = F.view.ox + e.offsetX / F.view.s, iy = F.view.oy + e.offsetY / F.view.s;
    F.view.s = Math.max(0.05, Math.min(20, F.view.s * f));
    F.view.ox = ix - e.offsetX / F.view.s;
    F.view.oy = iy - e.offsetY / F.view.s;
    draw();
  }, {passive: false});
  window.addEventListener('resize', () => draw());
}

// ── Zuordnung Loch <-> Schuss ───────────────────────────────────────────
// Aehnlichkeitstransformation (Verschiebung, Drehung, Massstab) sim -> Foto
// per kleinster Quadrate (komplexe Zahlen: z' = a*z + b).
function fitSimilarity(pairs) {
  const n = pairs.length;
  let sx = 0, sy = 0, tx = 0, ty = 0;
  for (const p of pairs) { sx += p.sx; sy += p.sy; tx += p.tx; ty += p.ty; }
  sx /= n; sy /= n; tx /= n; ty /= n;
  let num_re = 0, num_im = 0, den = 0;
  for (const p of pairs) {
    const ax = p.sx - sx, ay = p.sy - sy, bx = p.tx - tx, by = p.ty - ty;
    num_re += ax * bx + ay * by;
    num_im += ax * by - ay * bx;
    den += ax * ax + ay * ay;
  }
  const a_re = den ? num_re / den : 1, a_im = den ? num_im / den : 0;
  const tf = (x, y) => [a_re * (x - sx) - a_im * (y - sy) + tx, a_im * (x - sx) + a_re * (y - sy) + ty];
  tf.scale = Math.hypot(a_re, a_im);
  tf.rotDeg = Math.atan2(a_im, a_re) * 180 / Math.PI;
  return tf;
}

// robustMatch: Lochmuster im Foto suchen, das am besten zum Schussmuster
// passt - unabhaengig von der Ausgangslage (Verschiebung, Drehung, Massstab,
// auch gespiegelt). Je zwei Schuesse und zwei Loecher bestimmen eine
// Aehnlichkeitstransformation; gewonnen hat die mit den meisten Schuessen,
// die danach innerhalb tolMM an einem Loch liegen. Liefert die Paare
// (Index Schuss/Loch) und die Transformation - oder null.
function robustMatch(holesMM, sims, tolMM) {
  // Normalfall zuerst: Foto und Schuesse beide in mm, Massstab also nahe 1 -
  // die weite Suche (falscher Referenzdurchmesser o.ae.) nur, wenn damit zu
  // wenig zusammenpasst.
  // Im Normalfall liegt das zugehoerige Loch hoechstens ~20 mm von der
  // Neuberechnung entfernt - das begrenzt die Kandidaten stark.
  const near = robustMatchRange(holesMM, sims, tolMM, 0.85, 1.15, 20, 8);
  if (near && near.count >= Math.max(4, 0.3 * Math.min(sims.length, holesMM.length))) return near;
  const wide = robustMatchRange(holesMM, sims, tolMM, 0.3, 3.5, Infinity, 5);
  return !near || (wide && wide.count > near.count) ? wide : near;
}

// robustMatchRange: Suche mit Massstabsbereich scMin..scMax, Bezugsloecher
// hoechstens maxOff mm von ihrem Schuss entfernt, nRef Bezugsschuesse.
function robustMatchRange(holesMM, sims, tolMM, scMin, scMax, maxOff, nRef) {
  const nS = sims.length, nH = holesMM.length;
  if (nS < 3 || nH < 3) return null;
  // Aufwand begrenzen (14.10.2026: 48 Schuesse x 52 Loecher hingen sonst
  // stundenlang): als Paar-Bezugspunkte nur bis zu 8 moeglichst weit
  // verteilte Schuesse (Farthest-Point-Auswahl), naechstes Loch ueber ein
  // Raster statt Suche ueber alle Loecher.
  const ref = [0];
  const dmin = sims.map(sv => Math.hypot(sv.x - sims[0].x, sv.y - sims[0].y));
  while (ref.length < Math.min(nRef, nS)) {
    let k = -1;
    dmin.forEach((d, i) => { if (!ref.includes(i) && (k < 0 || d > dmin[k])) k = i; });
    ref.push(k);
    sims.forEach((sv, i) => { dmin[i] = Math.min(dmin[i], Math.hypot(sv.x - sims[k].x, sv.y - sims[k].y)); });
  }
  let best = null;
  for (const mirror of [false, true]) {
    const H = holesMM.map(([x, y]) => [mirror ? -x : x, y]);
    const cell = Math.max(tolMM, 1);
    const grid = new Map();
    const gkey = (gx, gy) => gx * 100003 + gy;
    H.forEach(([x, y], h) => {
      const key = gkey(Math.floor(x / cell), Math.floor(y / cell));
      if (!grid.has(key)) grid.set(key, []);
      grid.get(key).push(h);
    });
    const nearest = (x, y) => {
      const cx = Math.floor(x / cell), cy = Math.floor(y / cell);
      let bd = Infinity, bh = -1;
      for (let gx = cx - 1; gx <= cx + 1; gx++) for (let gy = cy - 1; gy <= cy + 1; gy++) {
        const list = grid.get(gkey(gx, gy));
        if (list) for (const h of list) {
          const d = Math.hypot(H[h][0] - x, H[h][1] - y);
          if (d < bd) { bd = d; bh = h; }
        }
      }
      return [bd, bh];
    };
    for (let ii = 0; ii < ref.length; ii++) for (let jj = ii + 1; jj < ref.length; jj++) {
      const i = ref[ii], j = ref[jj];
      const dsx = sims[j].x - sims[i].x, dsy = sims[j].y - sims[i].y;
      const ds2 = dsx * dsx + dsy * dsy;
      if (ds2 < 9) continue; // zu nah beieinander - Richtung unbestimmt
      const candA = [], candB = [];
      for (let h = 0; h < nH; h++) {
        if (mirror || Math.hypot(H[h][0] - sims[i].x, H[h][1] - sims[i].y) <= maxOff) candA.push(h);
        if (mirror || Math.hypot(H[h][0] - sims[j].x, H[h][1] - sims[j].y) <= maxOff) candB.push(h);
      }
      for (const a of candA) for (const b of candB) {
        if (a === b) continue;
        const dhx = H[b][0] - H[a][0], dhy = H[b][1] - H[a][1];
        // z = (Hb-Ha)/(sj-si) als komplexe Zahl
        const zr = (dhx * dsx + dhy * dsy) / ds2, zi = (dhy * dsx - dhx * dsy) / ds2;
        const sc = Math.hypot(zr, zi);
        if (sc < scMin || sc > scMax) continue;
        let count = 0, sum = 0;
        const pairs = [];
        for (let k = 0; k < nS; k++) {
          // kann den bisher besten Treffer nicht mehr einholen -> abbrechen
          if (best && count + (nS - k) < best.count) break;
          const px = sims[k].x - sims[i].x, py = sims[k].y - sims[i].y;
          const [bd, bh] = nearest(H[a][0] + zr * px - zi * py, H[a][1] + zi * px + zr * py);
          if (bd <= tolMM) { count++; sum += bd; pairs.push({si: k, hi: bh}); }
        }
        if (!best || count > best.count || (count === best.count && sum < best.sum)) {
          best = {count, sum, pairs, mirror, scale: sc, rotDeg: Math.atan2(zi, zr) * 180 / Math.PI};
        }
      }
    }
  }
  return best;
}

// Zuordnung Loch <-> Schuss in zwei Stufen, damit Loecher frueherer
// Versuche (bzw. fremde Loecher) nicht faelschlich einem Schuss zugeordnet
// werden:
//  1. grob: gierig nach kleinstem Abstand innerhalb maxD,
//  2. aus diesen Paaren den systematischen Fehler (Verschiebung, Drehung,
//     Massstab - genau das, was die Kalibrierung spaeter behebt) robust
//     schaetzen, die Neuberechnung damit korrigieren und ENG zuordnen;
//     liegt dabei ein zweites Loch fast gleich nah, gilt der Schuss als
//     mehrdeutig und wird nicht verwendet.
function assignHoles(holesMM, sims, maxD) {
  const greedy = (pos, gate) => {
    const cand = [];
    holesMM.forEach(([x, y], hi) => sims.forEach((s, si) => {
      const d = Math.hypot(x - pos[si][0], y - pos[si][1]);
      if (d <= gate) cand.push({hi, si, d});
    }));
    cand.sort((a, b) => a.d - b.d);
    const usedH = new Set(), usedS = new Set(), out = [];
    for (const c of cand) {
      if (usedH.has(c.hi) || usedS.has(c.si)) continue;
      usedH.add(c.hi); usedS.add(c.si); out.push(c);
    }
    return out;
  };
  const raw = sims.map(s => [s.x, s.y]);
  let matches = greedy(raw, maxD);
  let gate = maxD, corrected = raw, systematic = null, warning = null;
  // Zu wenige direkte Treffer (Foto gedreht/gespiegelt/falscher Massstab
  // oder grosser Versatz): Muster unabhaengig von der Lage suchen.
  // Die lageunabhaengige Suche laeuft immer: bei weit verteilten Schuessen
  // findet der grobe Direktvergleich (maxD) auch bei gedrehtem/gespiegeltem
  // Foto zufaellig "passende" Loecher. Massgeblich ist daher, wie viele
  // Schuesse ENG (3 mm) an einem Loch liegen - direkt bzw. nach der besten
  // Transformation.
  {
    const directTight = greedy(raw, 3).length;
    const rb = robustMatch(holesMM, sims, 3);
    if (rb && rb.count >= 4 && rb.count > directTight) {
      if (rb.mirror) {
        return {matches: [], systematic: null, warning: `Die Löcher passen nur spiegelverkehrt zu den Schüssen (${rb.count} von ${sims.length}). `
          + 'Bitte die Reihenfolge der Schrauben prüfen: oben links → oben rechts → unten rechts → unten links (aus Sicht des Schützen).'};
      }
      matches = rb.pairs.map(p => ({si: p.si, hi: p.hi, d: Math.hypot(holesMM[p.hi][0] - raw[p.si][0], holesMM[p.hi][1] - raw[p.si][1])}));
    }
  }
  if (matches.length >= 4) {
    let inl = matches;
    let tf = null;
    for (let iter = 0; iter < 4; iter++) {
      tf = fitSimilarity(inl.map(m => ({sx: raw[m.si][0], sy: raw[m.si][1], tx: holesMM[m.hi][0], ty: holesMM[m.hi][1]})));
      const res = matches.map(m => { const [x, y] = tf(raw[m.si][0], raw[m.si][1]); return Math.hypot(x - holesMM[m.hi][0], y - holesMM[m.hi][1]); });
      const med = [...res].sort((a, b) => a - b)[res.length >> 1];
      const next = matches.filter((m, i) => res[i] <= Math.max(2, 3 * med));
      if (next.length < 4 || next.length === inl.length) { inl = next.length >= 4 ? next : inl; break; }
      inl = next;
    }
    corrected = raw.map(([x, y]) => tf(x, y));
    const res = inl.map(m => Math.hypot(corrected[m.si][0] - holesMM[m.hi][0], corrected[m.si][1] - holesMM[m.hi][1]));
    const rms = Math.sqrt(res.reduce((a, r) => a + r * r, 0) / res.length);
    gate = Math.min(Math.max(maxD, 3), Math.max(2.5, 3 * rms));
    const [ox, oy] = tf(0, 0); // Versatz der Neuberechnung im Scheibenzentrum
    systematic = {dx: ox, dy: oy, rms, gate, scale: tf.scale, rotDeg: tf.rotDeg};
    // Auffaellige Lage: Hinweis, woran es liegen duerfte - Drehung/Massstab
    // kann "Parameter optimieren" nicht beheben, sie stammen aus dem Foto.
    const w = [];
    let rot = ((tf.rotDeg % 360) + 540) % 360 - 180;
    if (Math.abs(rot) > 5) w.push(`Foto um ca. ${Math.round(rot)}° gegen die Schüsse verdreht – Reihenfolge der Schrauben prüfen (oben links → oben rechts → unten rechts → unten links).`);
    if (Math.abs(tf.scale - 1) > 0.08) w.push(`Maßstab Foto/Schüsse ${tf.scale.toFixed(2)} – Referenzdurchmesser des Innenkreises prüfen (LG-Spiegel 30,5 mm, LP-Spiegel 59,5 mm)`
      + (Math.abs(refDiameter() / tf.scale - 30.5) < 3 && refDiameter() > 40 ? ' – vermutlich LG-Scheibe, bitte 30,5 mm eintragen.'
        : Math.abs(refDiameter() / tf.scale - 59.5) < 5 && refDiameter() < 40 ? ' – vermutlich LP-Scheibe, bitte 59,5 mm eintragen.' : '.'));
    if (Math.hypot(ox, oy) > 10) w.push(`Großer Versatz ${Math.hypot(ox, oy).toFixed(0)} mm zwischen Foto und Schüssen – richtigen Innenkreis markiert?`);
    if (w.length) warning = w.join(' ');
    matches = greedy(corrected, gate);
  }
  // mehrdeutig: ein anderes Loch liegt kaum weiter von der (korrigierten)
  // Neuberechnung entfernt als das zugeordnete
  const result = matches.map(m => {
    const [cx, cy] = corrected[m.si];
    const second = Math.min(...holesMM.map(([x, y], hi) => hi === m.hi ? Infinity : Math.hypot(x - cx, y - cy)));
    return {hi: m.hi, s: sims[m.si], resid: m.d, ambiguous: second < m.d + Math.max(1.5, 0.5 * gate)};
  });
  return {matches: result, systematic, warning};
}

function shotRange() {
  const from = parseInt($('foto-shot-from').value), to = parseInt($('foto-shot-to').value);
  return s => (isNaN(from) || s.shot_no >= from) && (isNaN(to) || s.shot_no <= to);
}

async function matchShots(simResp) {
  if (!F.geo) { setStatus('Zuerst Schrauben und Innenkreis markieren.'); return; }
  if (!F.holes.length) { setStatus('Noch keine Einschusslöcher markiert.'); return; }
  if (!currentSessionId) return;
  // bewusst abgewaehlte Schuesse bleiben abgewaehlt
  const userExcluded = new Set(F.pairs.filter(p => p.userSet && !p.include).map(p => p.shot_no));
  let resp = simResp;
  if (!resp) {
    setStatus('Berechne Schusspositionen mit den aktuellen Parametern…');
    try {
      resp = await api(`/api/sessions/${currentSessionId}/simulate`, {
        method: 'POST', headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({params: currentParams()}),
      });
    } catch (e) { setStatus('Fehler: ' + e.message); return; }
  }
  const inRange = shotRange();
  const sims = (resp.shots || []).filter(s => s.has_raw && s.sim && s.sim.pos_valid && inRange(s))
    .map(s => ({shot_no: s.shot_no, x: s.sim.x_mm, y: s.sim.y_mm}));
  const maxD = parseFloat($('foto-maxdist').value) || 15;
  const holesMM = F.holes.map(h => imgToMM(h.x, h.y));
  const {matches, systematic, warning} = assignHoles(holesMM, sims, maxD);
  F.pairs = matches.map(m => ({
    hole: m.hi, shot_no: m.s.shot_no, x_mm: holesMM[m.hi][0], y_mm: holesMM[m.hi][1],
    sim_x: m.s.x, sim_y: m.s.y, dist: Math.hypot(holesMM[m.hi][0] - m.s.x, holesMM[m.hi][1] - m.s.y),
    resid: m.resid, ambiguous: m.ambiguous, include: !m.ambiguous,
  }));
  F.pairs.sort((a, b) => a.shot_no - b.shot_no);
  F.pairs.forEach(p => { if (userExcluded.has(p.shot_no)) { p.include = false; p.userSet = true; } });
  const used = new Set(F.pairs.map(p => p.shot_no));
  F.unmatchedSim = sims.filter(s => !used.has(s.shot_no));
  F.result = null;
  F.verify = null; F.verifyShot = null; renderVerify();
  const freeHoles = F.holes.length - F.pairs.length;
  const nAmb = F.pairs.filter(p => p.ambiguous).length;
  let txt = `${F.pairs.length} Zuordnung(en)${nAmb ? `, davon ${nAmb} mehrdeutig (abgewählt)` : ''}. `
    + `${freeHoles} Loch/Löcher ohne Schuss (rot, werden ignoriert – z.B. frühere Versuche), `
    + `${F.unmatchedSim.length} Schuss/Schüsse ohne Loch (blau „?“).`;
  if (systematic) txt += ` Systematischer Versatz vor Kalibrierung ca. ${Math.hypot(systematic.dx, systematic.dy).toFixed(1)} mm, `
    + `Zuordnung danach bis ${systematic.gate.toFixed(1)} mm.`;
  if (!F.pairs.length && !warning) txt += ' Keine Zuordnung möglich: Das Lochmuster passt auch gedreht, gespiegelt oder anders skaliert nicht zu den Schüssen – '
    + 'stimmen Session/Schussbereich, und sind die Löcher dieser Serie markiert?';
  if (warning) txt += ' ⚠ ' + warning;
  setStatus(txt);
  renderPairs(); renderResult(); draw();
}

// Fotopositionen (zugeordnete, verwendete Loecher) fuer die Ergebnis-
// anzeige in Abschnitt 4 - shot_no -> {x, y} in mm.
function fotoPositions() {
  const m = new Map();
  if (!F.geo) return m;
  for (const p of F.pairs) if (p.include) m.set(p.shot_no, {x: p.x_mm, y: p.y_mm});
  return m;
}

function renderPairs() {
  if (typeof onFotoPositionsChanged === 'function') onFotoPositionsChanged();
  const box = $('foto-pairs');
  if (!F.pairs.length) { box.innerHTML = ''; $('foto-optbox').style.display = 'none'; $('foto-verifybox').style.display = 'none'; return; }
  $('foto-optbox').style.display = '';
  $('foto-verifybox').style.display = '';
  renderVariantChoice();
  const res = new Map((F.result?.shots || []).map(r => [r.shot_no, r]));
  const f = v => v == null ? '–' : v.toFixed(2);
  box.innerHTML = `<table><thead><tr><th></th><th>Schuss</th><th>Foto x/y (mm)</th><th>Simuliert x/y (mm)</th>
      <th>Abweichung vorher</th><th title="Abstand nach Ausgleich von Verschiebung/Drehung/Maßstab - Maß für die Eindeutigkeit der Zuordnung">Rest nach Lageausgleich</th>
      <th>nachher</th></tr></thead><tbody>${F.pairs.map((p, i) => {
    const r = res.get(p.shot_no);
    return `<tr${p.ambiguous ? ' style="color:#ffb347"' : ''}><td><input type="checkbox" data-i="${i}" ${p.include ? 'checked' : ''}></td>
      <td>#${p.shot_no}${p.ambiguous ? ' <span title="Ein zweites Loch liegt fast gleich nah - Zuordnung unsicher">mehrdeutig</span>' : ''}</td>
      <td>${f(p.x_mm)} / ${f(p.y_mm)}</td><td>${f(p.sim_x)} / ${f(p.sim_y)}</td>
      <td>${f(p.dist)} mm</td><td>${f(p.resid)} mm</td>
      <td>${r ? (r.after_err_mm == null ? 'ungültig' : f(r.after_err_mm) + ' mm') : '–'}</td></tr>`;
  }).join('')}</tbody></table>`;
  box.querySelectorAll('input[type=checkbox]').forEach(cb => cb.onchange = () => {
    F.pairs[+cb.dataset.i].include = cb.checked;
    F.pairs[+cb.dataset.i].userSet = true; // bewusst gesetzt (One-Click respektiert das)
    draw();
  });
}

// ── Optimierung ─────────────────────────────────────────────────────────
async function optimize() {
  const pairs = F.pairs.filter(p => p.include).map(p => ({shot_no: p.shot_no, x_mm: p.x_mm, y_mm: p.y_mm}));
  const optimizeOpts = {
    mic_offsets: $('foto-opt-ofs').checked, offset: $('foto-opt-offset').checked,
    sound_speed: $('foto-opt-sound').checked, standoff: $('foto-opt-standoff').checked,
    mic_half_x: $('foto-opt-michalfx').checked,
  };
  $('foto-optstatus').textContent = 'Optimiere…';
  try {
    F.paramsBefore = currentParams();
    F.result = await api(`/api/sessions/${currentSessionId}/photo-calibrate`, {
      method: 'POST', headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({params: F.paramsBefore, optimize: optimizeOpts, pairs}),
    });
    $('foto-optstatus').textContent = '';
  } catch (e) {
    $('foto-optstatus').textContent = 'Fehler: ' + e.message;
    F.result = null;
  }
  renderPairs(); renderResult(); draw();
}

function renderResult() {
  const box = $('foto-result');
  const r = F.result;
  if (!r) { box.style.display = 'none'; return; }
  box.style.display = '';
  const a = F.paramsBefore || {}, b = r.params;
  const rows = [];
  const cmp = (name, va, vb, unit) => { if (va !== vb) rows.push(`<tr><td>${name}</td><td>${va}</td><td>${vb}</td><td>${unit}</td></tr>`); };
  for (let i = 1; i < 6; i++) cmp(`OFS${i}`, a.mic_offset_ns?.[i], b.mic_offset_ns[i], 'ns');
  cmp('OFFSETX', a.offset_x_um, b.offset_x_um, 'µm');
  cmp('OFFSETY', a.offset_y_um, b.offset_y_um, 'µm');
  cmp('SOUNDSPEED', a.sound_mps, b.sound_mps, 'm/s');
  cmp('STANDOFFPAPER', a.standoff_paper_mm, +b.standoff_paper_mm.toFixed(2), 'mm');
  cmp('MICHALFX', a.mic_half_x_mm, +b.mic_half_x_mm.toFixed(2), 'mm');
  const warn = [];
  if (Math.abs(r.rotation_deg) > 0.5) warn.push(`Restverdrehung ${r.rotation_deg.toFixed(2)}° - sind die Schrauben achsparallel zur Mikrofonanordnung markiert (Reihenfolge oben links → oben rechts → unten rechts → unten links)?`);
  if (r.scale && Math.abs(r.scale - 1) > 0.01) warn.push(`Restmaßstab ${r.scale.toFixed(4)} - Referenzdurchmesser prüfen oder SOUNDSPEED/STANDOFF mit optimieren.`);
  if (r.invalid_after) warn.push(`${r.invalid_after} Schuss/Schüsse sind mit den neuen Parametern ungültig.`);
  const outliers = r.shots.filter(s => s.after_err_mm != null && s.after_err_mm > Math.max(1, 3 * r.rms_after_mm));
  if (outliers.length) warn.push(`Auffällig große Abweichung bei ${outliers.map(s => '#' + s.shot_no).join(', ')} - evtl. falsch zugeordnetes Loch: abwählen und erneut optimieren.`);
  box.innerHTML = `
    <div><strong>Abweichung Foto ↔ Neuberechnung: ${r.rms_before_mm.toFixed(2)} mm → ${r.rms_after_mm.toFixed(2)} mm RMS</strong>
      (max. ${r.max_after_mm.toFixed(2)} mm, ${r.shots.length} Schüsse, ${r.free_params} freie Parameter)</div>
    ${rows.length ? `<table style="margin-top:8px;width:auto"><thead><tr><th>Parameter</th><th>bisher</th><th>neu</th><th></th></tr></thead><tbody>${rows.join('')}</tbody></table>` : '<div class="hint">Keine Parameteränderung.</div>'}
    ${warn.map(w => `<div style="color:#ffb347;margin-top:6px">⚠ ${w}</div>`).join('')}
    <div class="row-actions" style="margin-top:10px">
      <button class="btn" id="foto-apply">Parameter übernehmen</button>
      <span class="hint">übernimmt die Werte in die Grundwerte (Abschnitt 2) – danach neu berechnen, speichern und freigeben in Abschnitt 5.</span>
    </div>`;
  $('foto-apply').onclick = () => {
    applyParams(r.params);
    $('foto-optstatus').textContent = 'Übernommen.';
    if (typeof runSimulation === 'function') runSimulation();
  };
}

// ── One-Click-Kalibrierung mit Zonen (sim_zone_cal.go) ───────────────────
// Zuordnen (falls noetig) -> Basissatz + Zonen-Saetze kalibrieren ->
// Abschnitt 2 fuellen -> neu berechnen -> als Kalibrierung speichern. Die
// Freigabe an den Stand bleibt ein bewusster eigener Schritt (Abschnitt
// "Uebertragung an den Stand").
async function oneClickCalibrate() {
  const st = $('foto-oneclick-status');
  const say = (t, err) => { st.textContent = t; st.style.color = err ? 'var(--red)' : ''; };
  if (!F.geo) { say('Zuerst Schrauben, Innenkreis und Einschusslöcher markieren.', true); return; }
  if (!F.holes.length) { say('Noch keine Einschusslöcher markiert.', true); return; }
  // Startpaare aus der Zuordnung im Browser (darf unvollstaendig sein - die
  // endgueltige Zuordnung macht der Server)
  if (!F.pairs.length) {
    say('Ordne Löcher den Schüssen zu…');
    await matchShots();
  }
  const pairs = F.pairs.filter(p => p.include).map(p => ({shot_no: p.shot_no, x_mm: p.x_mm, y_mm: p.y_mm}));
  let hole = parseFloat(document.getElementById('z-hole').value);
  if (!(hole > 0)) { hole = holeRadiusForTarget(); document.getElementById('z-hole').value = hole; }
  const zones = {
    hole_radius_mm: hole,
    band_half_mm: parseFloat(document.getElementById('z-band').value) || 45,
    blend_mm: parseFloat(document.getElementById('z-blend').value) || 2.25,
  };
  const optimizeOpts = {
    mic_offsets: $('foto-opt-ofs').checked, offset: $('foto-opt-offset').checked,
    sound_speed: $('foto-opt-sound').checked, standoff: $('foto-opt-standoff').checked,
    mic_half_x: $('foto-opt-michalfx').checked,
  };
  const before = currentParams();
  // Der Server ordnet selbst zu (sim_zone_assign.go): alle Loecher, die
  // Schuesse im eingestellten Bereich, vom Nutzer abgewaehlte Schuesse
  // bleiben draussen; die Paare aus dem Browser dienen nur als Start.
  // abgewaehlt: vom Nutzer (auch gespeichert) oder automatisch, weil die
  // Zuordnung im Browser mehrdeutig war - letztere ordnet der Server neu zu
  const userExcluded = F.pairs.filter(p => !p.include && (p.userSet || !p.ambiguous));
  const inRange = shotRange();
  const shotNos = (typeof lastSessionShots !== 'undefined' ? lastSessionShots : [])
    .filter(s => s.has_raw && inRange(s)).map(s => s.shot_no);
  say(`Kalibriere: ${F.holes.length} Löcher, ${shotNos.length} Schüsse (Zuordnung + Grundwerte + Zonen)…`);
  let r;
  try {
    r = await api(`/api/sessions/${currentSessionId}/photo-calibrate-zones`, {
      method: 'POST', headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({params: before, optimize: optimizeOpts, zones, pairs,
        holes_mm: F.holes.map(h => imgToMM(h.x, h.y)), shot_nos: shotNos,
        excluded: userExcluded.map(p => p.shot_no)}),
    });
  } catch (e) { say('Fehler: ' + e.message, true); return; }
  applyParams(r.params);
  if (r.pairs && r.pairs.length) {
    F.pairs = r.pairs.map(p => ({
      hole: p.hole, shot_no: p.shot_no, x_mm: p.x_mm, y_mm: p.y_mm, sim_x: p.sim_x, sim_y: p.sim_y,
      dist: Math.hypot(p.x_mm - p.sim_x, p.y_mm - p.sim_y), resid: Math.hypot(p.x_mm - p.sim_x, p.y_mm - p.sim_y),
      ambiguous: false, include: true,
    })).concat(userExcluded).sort((a, b) => a.shot_no - b.shot_no);
    const used = new Set(F.pairs.map(p => p.shot_no));
    F.unmatchedSim = (F.unmatchedSim || []).filter(s => !used.has(s.shot_no));
    renderPairs(); draw();
    scheduleMarkerSave();
  }
  F.zoneResult = r;
  renderZoneResult();
  F.keepPairs = !!(r.pairs && r.pairs.length);
  if (typeof runSimulation === 'function') runSimulation();
  const now = new Date();
  const stamp = now.toLocaleString('de-DE', {day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit'});
  const nUsed = F.pairs.filter(p => p.include).length;
  const name = `Foto-Zonen ${stamp}${currentLoadedLaneNo != null ? ' – Stand ' + currentLoadedLaneNo : ''} (${nUsed} Schuss)`;
  try {
    await api('/api/simulator-configs', {method: 'POST', headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({name, lane_no: currentLoadedLaneNo, params: currentParams()})});
    await loadConfigOptions();
    say(`Übernommen (Grundwerte und Zonen) und als Kalibrierung „${name}“ gespeichert. `
      + 'Prüfen mit „Messwerte gegen das Foto prüfen“, dann in Abschnitt 5 an den Stand freigeben.');
  } catch (e) { say('Übernommen, aber Speichern fehlgeschlagen: ' + e.message, true); }
}

function renderZoneResult() {
  const box = $('foto-zoneresult');
  const r = F.zoneResult;
  if (!r) { box.style.display = 'none'; return; }
  box.style.display = '';
  const pct = v => Math.round(v * 100) + ' %';
  const st = s => s && s.n ? `${pct(s.below1)} &lt; 1 mm · P50 ${s.p50_mm.toFixed(2)} · P90 ${s.p90_mm.toFixed(2)} mm` : '–';
  const names = {A: 'Loch Vorsatzscheibe', B: 'neben Band', C: 'Band + Vorsatz'};
  const rows = r.zones.map(z => `<tr><td><strong>${z.zone}</strong> ${names[z.zone]}</td><td>${z.shots}</td><td>${esc(z.fitted)}</td>
    <td>${st(z.before)}</td><td>${st(z.after)}</td>
    <td>${z.check_mm != null ? `Ø ${z.check_mm.toFixed(2)} mm` : '–'}</td>
    <td>${z.set ? `${z.set.sound_mps.toFixed(1)} m/s` : '–'}</td></tr>`).join('');
  const weak = r.zones.filter(z => z.shots < 6).map(z => z.zone);
  const bad = (r.shots || []).filter(x => x.after_err_mm != null && x.after_err_mm > 3);
  box.innerHTML = `
    <div><strong>One-Click-Kalibrierung:</strong> vorher ${st(r.before)} → nur Grundwerte ${st(r.base_only)} →
      <strong>mit Zonen ${st(r.after)}</strong></div>
    <table style="margin-top:6px;width:auto"><thead><tr><th>Zone</th><th>Schüsse</th><th>angepasst</th><th>vorher</th><th>nachher</th>
      <th title="Jeder Schuss der Zone einmal weggelassen, mit den übrigen kalibriert und an ihm gemessen (Fehler bei 5 mm gekappt) – Maß für die Übertragbarkeit auf neue Schüsse">Kreuzprüfung</th><th>Schall</th></tr></thead>
      <tbody>${rows}</tbody></table>
    ${bad.length ? `<div style="color:#ffb347;margin-top:6px">⚠ Mehr als 3 mm daneben: ${bad.map(x => `#${x.shot_no} (${x.after_err_mm.toFixed(1)} mm)`).join(', ')} – Loch und Zuordnung prüfen, ggf. abwählen und erneut kalibrieren.</div>` : ''}
    <div class="hint" style="margin-top:4px">Werte an denselben Schüssen, an denen kalibriert wurde – auf neue Schüsse übertragen ist das Ergebnis etwas schlechter
      (Prüfung an der Serie vom 12.10., jeder Schuss einmal weggelassen und mit den übrigen kalibriert: 72 % &lt; 1 mm, P90 1,8 mm).
      ${weak.length ? `Für Zone ${weak.join(', ')} lagen weniger als 6 Schüsse vor – für einen vollständigen Satz je Zone mindestens 6, besser 10 Schüsse.` : ''}</div>`;
}

// ── Pruefung gegen das Foto ─────────────────────────────────────────────
// Varianten = Abweichungen von den aktuellen Parametern (Abschnitt 2); jede
// neue Simulator-Option laesst sich hier als weitere Zeile ergaenzen.
const VARIANTS = [
  {id: 'old', name: 'ohne Nachzentrieren und Korrektur aller Schnittpunkte', on: true,
   patch: p => ({pos_algo: {...p.pos_algo, corr_cluster: false, iterations: 0, weighted: false, lsq: false}})},
  {id: 'fw412', name: 'Standardverfahren (CLUSTERCORR, 5 Durchgänge)', on: true,
   patch: p => ({pos_algo: {...p.pos_algo, corr_cluster: true, iterations: 5, weighted: false, lsq: false}})},
  {id: 'lsq', name: '+ Ausgleichsrechnung (LSQ)', on: true,
   patch: p => ({pos_algo: {...p.pos_algo, corr_cluster: true, iterations: 5, lsq: true}})},
  {id: 'weighted', name: 'gewichtete Mittelung (nur Simulator)',
   patch: p => ({pos_algo: {...p.pos_algo, weighted: true}})},
  {id: 'nobshift', name: 'ohne Kugeldurchmesser-Korrektur', patch: p => ({bullet_shift_pct: 0})},
  {id: 'nowalk', name: 'ohne Pulsbreiten-Korrektur (WALKK 0)', patch: p => ({walk_k: 0})},
  {id: 'walk05', name: 'WALKK 0,5', patch: p => ({walk_k: 0.5})},
  {id: 'walkfirst', name: 'Pulsbreite bis erste fallende Flanke', patch: p => ({walk_width_rule: 'first'})},
  {id: 'sound', name: 'Schallgeschwindigkeit je Schuss umschalten', patch: p => ({sound_from_shot: !p.sound_from_shot})},
];

function renderVariantChoice() {
  const box = $('foto-variants');
  if (!box || box.childElementCount) return;
  box.innerHTML = '<span class="hint">Varianten:</span>' + VARIANTS.map(v =>
    `<label><input type="checkbox" data-variant="${v.id}"${v.on ? ' checked' : ''}> ${v.name}</label>`).join('');
}

async function verify() {
  const pairs = F.pairs.filter(p => p.include).map(p => ({shot_no: p.shot_no, x_mm: p.x_mm, y_mm: p.y_mm}));
  if (!pairs.length) { $('foto-verifystatus').textContent = 'Keine zugeordneten Löcher ausgewählt.'; return; }
  const params = currentParams();
  const chosen = VARIANTS.filter(v => document.querySelector(`[data-variant="${v.id}"]`)?.checked);
  const variants = chosen.map(v => ({name: v.name, params: {...params, ...v.patch(params)}}));
  $('foto-verifystatus').textContent = 'Rechne…';
  try {
    F.verify = await api(`/api/sessions/${currentSessionId}/photo-verify`, {
      method: 'POST', headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({params, pairs, variants}),
    });
    F.verifyParams = params;
    $('foto-verifystatus').textContent = '';
  } catch (e) {
    $('foto-verifystatus').textContent = 'Fehler: ' + e.message;
    F.verify = null;
  }
  renderVerify(); draw();
}

function renderVerify() {
  const box = $('foto-verify-result');
  const v = F.verify;
  if (!v) { box.innerHTML = ''; return; }
  const f2 = x => x == null ? '–' : x.toFixed(2);
  const statRow = (name, st, best) => `<tr${best ? ' style="color:#3dcc6e;font-weight:600"' : ''}><td>${name}</td><td>${st.n}${st.invalid ? ` (+${st.invalid} ungültig)` : ''}</td>
    <td>${f2(st.mean_dx_mm)} / ${f2(st.mean_dy_mm)}</td><td>${f2(st.rms_mm)}</td><td>${f2(st.rms_centered_mm)}</td>
    <td>${f2(st.median_mm)}</td><td>${f2(st.max_mm)}</td></tr>`;
  const sims = [{name: 'Neuberechnung (aktuelle Parameter)', st: v.sim_stats}, ...v.variants.map(x => ({name: 'Variante: ' + x.name, st: x.stats}))];
  const bestRms = Math.min(...sims.filter(x => x.st.n).map(x => x.st.rms_mm));
  const stats = `<table style="width:auto"><thead><tr><th>Position</th><th>Schüsse</th><th title="Messung minus Foto">mittl. Versatz x / y (mm)</th>
      <th>RMS (mm)</th><th title="Streuung nach Abzug des mittleren Versatzes - unabhängig von OFFSETX/Y">RMS ohne Versatz</th><th>Median</th><th>Max</th></tr></thead><tbody>
      ${statRow('Gerät (beim Schuss gemeldet)', v.device_stats, false)}
      ${sims.map(x => statRow(x.name, x.st, x.st.n && x.st.rms_mm === bestRms)).join('')}</tbody></table>`;

  const ms = v.mic_stats;
  const mics = `<table style="width:auto;margin-top:10px"><thead><tr><th>Mic</th><th>Schüsse</th>
      <th title="gemessene minus aus der Fotoposition erwartete Laufzeit (Schall vom Lochrand), Mittel der beteiligten Mics je Schuss abgezogen">mittl. Rest Lochrand (ns)</th>
      <th>Streuung (ns)</th><th title="dasselbe mit Schall von der Lochmitte">mittl. Rest Lochmitte (ns)</th>
      <th title="OFS, bei dem der mittlere Rest relativ zu Mic 0 verschwände">OFS aktuell → Vorschlag</th></tr></thead><tbody>
      ${ms.map(m => `<tr><td>${m.mic}</td><td>${m.n}</td><td>${m.n ? m.mean_rim_ns.toFixed(0) : '–'}</td>
        <td>${m.n > 1 ? m.std_rim_ns.toFixed(0) : '–'}</td><td>${m.n ? m.mean_center_ns.toFixed(0) : '–'}</td>
        <td>${(F.verifyParams.mic_offset_ns || [])[m.mic]} → ${m.suggest_ofs_ns}</td></tr>`).join('')}</tbody></table>
    <div class="row-actions" style="margin-top:6px">
      <button class="btn-sec" id="foto-ofs-apply">OFS-Vorschlag übernehmen</button>
      <span class="hint">Ein gleichbleibender Rest eines Mikrofons deutet auf einen falschen Offset; große Streuung auf Störungen (Echo, Pulsbreite) oder Geometrie.
        Den Vorschlag danach mit „Prüfen“ kontrollieren – die eigentliche Optimierung macht „Parameter optimieren“.</span>
    </div>`;

  const vnames = v.variants.map(x => x.name);
  const shots = `<div style="max-height:320px;overflow:auto;margin-top:10px"><table><thead><tr><th>Schuss</th><th>Foto x/y</th>
      <th>Gerät</th><th>Neuberechnung</th>${vnames.map(n => `<th title="${n}">${n.length > 18 ? n.slice(0, 17) + '…' : n}</th>`).join('')}
      <th title="Laufzeit-Rest je Mic (Lochrand-Modell), ns">Rest je Mic 0–5 (ns)</th><th>Messpunkte</th></tr></thead><tbody>
      ${v.shots.map(s => `<tr data-shot="${s.shot_no}" style="cursor:pointer${s.shot_no === F.verifyShot ? ';background:rgba(61,214,255,.12)' : ''}">
        <td>#${s.shot_no}</td><td>${f2(s.truth_x_mm)} / ${f2(s.truth_y_mm)}</td>
        <td>${f2(s.device_err_mm)}</td><td>${s.sim_valid ? f2(s.sim_err_mm) : 'ungültig'}</td>
        ${s.variant_err_mm.map(e => `<td>${e == null ? 'ungültig' : f2(e)}</td>`).join('')}
        <td style="font-family:monospace;font-size:11px">${s.mics.map(m => m.rim_ns == null ? '·' : Math.round(m.rim_ns)).join(' ')}</td>
        <td>${s.candidates.length} (${s.candidates.filter(c => c.in_cluster).length} im Cluster)</td></tr>`).join('')}
      </tbody></table></div>
    <div class="hint" style="margin-top:4px">Abweichungen in mm. Zeile anklicken: Messpunkte dieses Schusses im Foto
      (türkis = Schnittpunkte nach Kugeldurchmesser-Korrektur, hell = in der Mittelung, groß = beste Kombination; gelb = Rohwerte;
      orange × = Gerät, blau + = Neuberechnung, Ring = Loch).</div>`;

  box.innerHTML = stats + mics + shots;
  $('foto-ofs-apply').onclick = () => {
    applyParams({mic_offset_ns: v.mic_stats.map(m => m.suggest_ofs_ns)});
    $('foto-verifystatus').textContent = 'OFS übernommen – erneut „Prüfen“ zum Kontrollieren.';
  };
  box.querySelectorAll('tr[data-shot]').forEach(tr => tr.onclick = () => {
    const no = +tr.dataset.shot;
    F.verifyShot = F.verifyShot === no ? null : no;
    const s = v.shots.find(x => x.shot_no === no);
    if (F.verifyShot && s) focusOn(s.truth_x_mm, s.truth_y_mm, 14);
    renderVerify(); draw();
  });
}

// focusOn: Ansicht auf einen Punkt (mm) zentrieren, spanMM sichtbar.
function focusOn(xMM, yMM, spanMM) {
  if (!F.geo) return;
  const [w, h] = canvasSize();
  const [cx, cy] = mmToImg(xMM, yMM);
  const s = Math.min(w, h) / (spanMM * pxPerMM(cx, cy));
  F.view = {s, ox: cx - w / 2 / s, oy: cy - h / 2 / s};
}

// ── Init ────────────────────────────────────────────────────────────────
function init() {
  if (!$('foto-canvas')) return;
  initCanvas();
  $('foto-file').addEventListener('change', e => { if (e.target.files[0]) loadFile(e.target.files[0]); });
  document.querySelectorAll('#foto-modes button').forEach(b => b.onclick = () => setMode(b.dataset.mode));
  $('foto-autoholes').onclick = detectHoles;
  $('foto-fit').onclick = fitView;
  $('foto-reset').onclick = () => {
    F.screws = []; F.rim = null; F.holes = []; F.geo = null; F.pairs = []; F.result = null; F.unmatchedSim = [];
    F.verify = null; F.verifyShot = null; renderVerify();
    renderPairs(); renderResult(); updateGeoInfo(); setMode('screws');
  };
  $('foto-match').onclick = () => matchShots();
  $('foto-optimize').onclick = optimize;
  $('foto-oneclick').onclick = oneClickCalibrate;
  $('foto-verify').onclick = verify;
  $('foto-allpoints').onchange = draw;
  for (const id of ['foto-diameter', 'foto-screwW', 'foto-screwH']) {
    // (Schussbereich wirkt erst beim naechsten "zuordnen")
    $(id).addEventListener('input', () => { computeGeometry(); updateGeoInfo(); F.pairs = []; F.result = null; renderPairs(); renderResult(); draw(); });
  }
}
// Beim Wechsel der Session: Zuordnung verwerfen (gehoert zu den Schuessen
// der alten Session), Foto und Markierungen bleiben.
window.fotoSessionChanged = () => {
  F.pairs = []; F.result = null; F.unmatchedSim = []; F.verify = null; F.verifyShot = null;
  renderPairs(); renderResult(); renderVerify();
  if (currentSessionId) restorePhoto(currentSessionId);
};
// Fuer Tests/Diagnose (Browser-Konsole bzw. automatisierter Oberflaechentest)
window.fotoState = F;
window.fotoPositions = fotoPositions;
// Nach "Neu berechnen" (Abschnitt 4): Loecher mit den aktuellen Parametern
// neu zuordnen, damit die Foto-Lagen in der Ergebnisanzeige zu den
// gerade gerechneten Schuessen passen (die Zuordnung beim Laden des Fotos
// stammt von den damaligen Parametern). Nutzt die Simulationsantwort mit.
window.fotoRematch = async (simResp) => {
  if (!F.geo || !F.holes.length || F.restoring) return;
  // direkt nach der One-Click-Kalibrierung: deren (bessere) Zuordnung vom
  // Server behalten
  if (F.keepPairs) { F.keepPairs = false; return; }
  await matchShots(simResp);
};
window.fotoTest = {loadFile, loadImage, restorePhoto, markerState, onImageClick, detectHoles, matchShots, optimize, verify, renderVerify, assignHoles, robustMatch, imgToMM, mmToImg, pxPerMM, setMode, computeGeometry, fitAxisEllipse, homography, applyH};

if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
else init();
})();
