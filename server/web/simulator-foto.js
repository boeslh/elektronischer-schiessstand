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

function computeGeometry() {
  F.geo = null;
  if (F.screws.length < 4 || !F.rim || F.rim.length < 20) return;
  const h = homography(F.screws.map(p => [p.x, p.y]), [[0, 0], [1, 0], [1, 1], [0, 1]]);
  if (!h) return;
  const pts = F.rim.map(p => applyH(h, p.x, p.y));
  const D = refDiameter();
  const knownW = parseFloat($('foto-screwW').value) || 0;
  const knownH = parseFloat($('foto-screwH').value) || 0;

  // Robuste Anpassung: Randpunkte, die deutlich vom Kreis abweichen (z.B.
  // ein Einschuss direkt am Kreisrand, Lichtreflex), werden iterativ
  // verworfen.
  let keep = pts, fit = null, sx = 0, sy = 0, rms = 0, meanR = 0;
  for (let iter = 0; iter < 4; iter++) {
    fit = fitAxisEllipse(keep);
    if (!fit) return;
    sx = knownW || D / 2 / fit.au;
    sy = knownH || D / 2 / fit.av;
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
    measuredD: 2 * meanR, axisRatio: (fit.au * sx) / (fit.av * sy), knownSize: !!(knownW && knownH),
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
async function loadFile(file) {
  const url = URL.createObjectURL(file);
  const img = new Image();
  img.src = url;
  try { await img.decode(); } catch (e) { setStatus('Bild konnte nicht geladen werden.'); return; }
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
  $('foto-work').style.display = '';
  fitView();
  setMode('screws');
  renderPairs();
  renderResult();
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

// Automatische Lochsuche innerhalb des Schraubenrechtecks (ausserhalb des
// Innenkreises und abseits der Schrauben): dunkle, zusammenhaengende
// Flecken mit plausibler Groesse fuer ein Diabolo-Loch.
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
  let added = 0;
  for (const h of found) {
    const ppm = pxPerMM(h.x, h.y);
    if (!F.holes.some(o => Math.hypot(o.x - h.x, o.y - h.y) < 2 * ppm)) { F.holes.push(h); added++; }
  }
  F.pairs = []; F.result = null;
  renderPairs(); renderResult();
  setStatus(`${found.length} Loch/Löcher gefunden, ${added} neu hinzugefügt. Bitte prüfen: Klick auf ein Loch entfernt es, Klick daneben fügt eins hinzu.`);
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
  if (F.mode === 'circle') return 'In die schwarze Fläche des Innenkreises klicken - der Rand wird automatisch erkannt.';
  if (F.mode === 'holes') return 'Einschussloch anklicken (rastet auf die Lochmitte ein) - Klick auf ein markiertes Loch entfernt es.';
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
    `Schraubenrechteck ${g.sx.toFixed(2)} × ${g.sy.toFixed(2)} mm${g.knownSize ? ' (vorgegeben)' : ' (aus Kreisdurchmesser)'}`,
    `Kreisrand: Abweichung ${g.rimRMS.toFixed(3)} mm RMS (${quality}), ${g.rimUsed}/${g.rimTotal} Randpunkte`,
  ];
  if (g.knownSize) parts.push(`gemessener Kreisdurchmesser ${g.measuredD.toFixed(2)} mm (Soll ${refDiameter()} mm), Achsverhältnis ${g.axisRatio.toFixed(4)}`);
  // Mit vorgegebenem Schraubenabstand ist der Kreis eine unabhaengige
  // Kontrolle: passt er nicht (Durchmesser oder Rundheit > 0.5% daneben),
  // sind meist die Schrauben ungenau markiert.
  const bad = g.knownSize && (Math.abs(g.measuredD / refDiameter() - 1) > 0.005 || Math.abs(g.axisRatio - 1) > 0.005);
  el.style.color = bad ? '#ffb347' : '';
  if (bad) parts.unshift('⚠ Kreis passt nicht zum Schraubenrechteck (> 0,5 % Abweichung) – Schraubenmitten stark vergrößert genauer setzen, höher aufgelöstes Foto verwenden oder Ø/Schraubenabstand prüfen');
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
  return (x, y) => [a_re * (x - sx) - a_im * (y - sy) + tx, a_im * (x - sx) + a_re * (y - sy) + ty];
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
  let gate = maxD, corrected = raw, systematic = null;
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
    gate = Math.min(maxD, Math.max(2.5, 3 * rms));
    const [ox, oy] = tf(0, 0); // Versatz der Neuberechnung im Scheibenzentrum
    systematic = {dx: ox, dy: oy, rms, gate};
    matches = greedy(corrected, gate);
  }
  // mehrdeutig: ein anderes Loch liegt kaum weiter von der (korrigierten)
  // Neuberechnung entfernt als das zugeordnete
  const result = matches.map(m => {
    const [cx, cy] = corrected[m.si];
    const second = Math.min(...holesMM.map(([x, y], hi) => hi === m.hi ? Infinity : Math.hypot(x - cx, y - cy)));
    return {hi: m.hi, s: sims[m.si], resid: m.d, ambiguous: second < m.d + Math.max(1.5, 0.5 * gate)};
  });
  return {matches: result, systematic};
}

function shotRange() {
  const from = parseInt($('foto-shot-from').value), to = parseInt($('foto-shot-to').value);
  return s => (isNaN(from) || s.shot_no >= from) && (isNaN(to) || s.shot_no <= to);
}

async function matchShots() {
  if (!F.geo) { setStatus('Zuerst Schrauben und Innenkreis markieren.'); return; }
  if (!F.holes.length) { setStatus('Noch keine Einschusslöcher markiert.'); return; }
  if (!currentSessionId) return;
  setStatus('Berechne Schusspositionen mit den aktuellen Parametern…');
  let resp;
  try {
    resp = await api(`/api/sessions/${currentSessionId}/simulate`, {
      method: 'POST', headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({params: currentParams()}),
    });
  } catch (e) { setStatus('Fehler: ' + e.message); return; }
  const inRange = shotRange();
  const sims = (resp.shots || []).filter(s => s.has_raw && s.sim && s.sim.pos_valid && inRange(s))
    .map(s => ({shot_no: s.shot_no, x: s.sim.x_mm, y: s.sim.y_mm}));
  const maxD = parseFloat($('foto-maxdist').value) || 15;
  const holesMM = F.holes.map(h => imgToMM(h.x, h.y));
  const {matches, systematic} = assignHoles(holesMM, sims, maxD);
  F.pairs = matches.map(m => ({
    hole: m.hi, shot_no: m.s.shot_no, x_mm: holesMM[m.hi][0], y_mm: holesMM[m.hi][1],
    sim_x: m.s.x, sim_y: m.s.y, dist: Math.hypot(holesMM[m.hi][0] - m.s.x, holesMM[m.hi][1] - m.s.y),
    resid: m.resid, ambiguous: m.ambiguous, include: !m.ambiguous,
  }));
  F.pairs.sort((a, b) => a.shot_no - b.shot_no);
  const used = new Set(F.pairs.map(p => p.shot_no));
  F.unmatchedSim = sims.filter(s => !used.has(s.shot_no));
  F.result = null;
  const freeHoles = F.holes.length - F.pairs.length;
  const nAmb = F.pairs.filter(p => p.ambiguous).length;
  let txt = `${F.pairs.length} Zuordnung(en)${nAmb ? `, davon ${nAmb} mehrdeutig (abgewählt)` : ''}. `
    + `${freeHoles} Loch/Löcher ohne Schuss (rot, werden ignoriert – z.B. frühere Versuche), `
    + `${F.unmatchedSim.length} Schuss/Schüsse ohne Loch (blau „?“).`;
  if (systematic) txt += ` Systematischer Versatz vor Kalibrierung ca. ${Math.hypot(systematic.dx, systematic.dy).toFixed(1)} mm, `
    + `Zuordnung danach bis ${systematic.gate.toFixed(1)} mm.`;
  setStatus(txt);
  renderPairs(); renderResult(); draw();
}

function renderPairs() {
  const box = $('foto-pairs');
  if (!F.pairs.length) { box.innerHTML = ''; $('foto-optbox').style.display = 'none'; return; }
  $('foto-optbox').style.display = '';
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
      <span class="hint">übernimmt die Werte in Abschnitt 2 - danach wie gewohnt neu berechnen und als SET-Befehle exportieren.</span>
    </div>`;
  $('foto-apply').onclick = () => {
    applyParams(r.params);
    $('foto-optstatus').textContent = 'Übernommen.';
    if (typeof runSimulation === 'function') runSimulation();
  };
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
    renderPairs(); renderResult(); updateGeoInfo(); setMode('screws');
  };
  $('foto-match').onclick = matchShots;
  $('foto-optimize').onclick = optimize;
  for (const id of ['foto-diameter', 'foto-screwW', 'foto-screwH']) {
    // (Schussbereich wirkt erst beim naechsten "zuordnen")
    $(id).addEventListener('input', () => { computeGeometry(); updateGeoInfo(); F.pairs = []; F.result = null; renderPairs(); renderResult(); draw(); });
  }
}
// Beim Wechsel der Session: Zuordnung verwerfen (gehoert zu den Schuessen
// der alten Session), Foto und Markierungen bleiben.
window.fotoSessionChanged = () => { F.pairs = []; F.result = null; F.unmatchedSim = []; renderPairs(); renderResult(); draw(); };
// Fuer Tests/Diagnose (Browser-Konsole bzw. automatisierter Oberflaechentest)
window.fotoState = F;
window.fotoTest = {loadFile, onImageClick, detectHoles, matchShots, optimize, imgToMM, mmToImg, pxPerMM, setMode, computeGeometry, fitAxisEllipse, homography, applyH};

if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
else init();
})();
