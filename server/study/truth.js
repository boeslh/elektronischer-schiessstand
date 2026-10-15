// Rekonstruiert die wahren Trefferlagen (mm) aus den gespeicherten Foto-
// Markierungen - mit dem Original-Code aus web/simulator-foto.js - und ordnet
// sie den Schuessen zu (robuste Zuordnung wie im Simulator).
const fs = require('fs');
const src = fs.readFileSync(__dirname + '/../web/simulator-foto.js', 'utf8');
const grab = name => { const i = src.indexOf('function ' + name + '('); let d = 0, j = src.indexOf('{', i);
  for (let k = j; k < src.length; k++) { if (src[k] === '{') d++; if (src[k] === '}' && --d === 0) return src.slice(i, k + 1); } };
const F = {};
const vals = {};
const $ = id => ({ value: vals[id] });
eval(['solveLinear', 'homography', 'applyH', 'invert3', 'fitAxisEllipse', 'computeGeometry', 'imgToMM', 'mmToImg',
      'fitSimilarity', 'robustMatch', 'robustMatchRange', 'assignHoles', 'refDiameter'].map(grab).join('\n'));
for (const sid of process.argv.slice(2)) {
  const m = JSON.parse(fs.readFileSync(`${__dirname}/data/markers_${sid}.json`, 'utf8'));
  const dev = JSON.parse(fs.readFileSync(`${__dirname}/data/device_${sid}.json`, 'utf8'));
  vals['foto-diameter'] = m.diameter; vals['foto-screwW'] = m.screw_w; vals['foto-screwH'] = m.screw_h;
  F.screws = m.screws; F.rim = m.rim; F.holes = m.holes;
  computeGeometry();
  const holesMM = F.holes.map(h => imgToMM(h.x, h.y));
  const sims = dev.filter(s => s.status === 'valid').map(s => ({ shot_no: s.shot_no, x: s.x_mm, y: s.y_mm }));
  const r = assignHoles(holesMM, sims, +m.max_dist || 15);
  const pairs = r.matches.filter(x => !(m.excluded || []).includes(x.s.shot_no))
    .map(x => ({ shot_no: x.s.shot_no, x_mm: holesMM[x.hi][0], y_mm: holesMM[x.hi][1], dev_x: x.s.x, dev_y: x.s.y, ambiguous: x.ambiguous }));
  const g = F.geo;
  const out = { session_id: sid, geo: { rimRMS: g.rimRMS, measuredD: g.measuredD, rectRatio: g.rectRatio, ctrlW: g.ctrlW, sx: g.sx, sy: g.sy },
    systematic: r.systematic, warning: r.warning || null, holes_mm: holesMM, pairs };
  fs.writeFileSync(`${__dirname}/data/truth_${sid}.json`, JSON.stringify(out, null, 1));
  const errs = pairs.map(p => Math.hypot(p.dev_x - p.x_mm, p.dev_y - p.y_mm)).sort((a, b) => a - b);
  console.log(sid.slice(0, 8), `Löcher ${holesMM.length}, Paare ${pairs.length}, Ø Innenkreis gemessen ${g.measuredD.toFixed(2)} (Rand-RMS ${g.rimRMS.toFixed(3)}), Schraubenrechteck aus Kreis ${g.sx.toFixed(2)} x ${g.sy.toFixed(2)}, Seitenverh. ${g.rectRatio.toFixed(4)}`,
    '| Gerät→Foto: Median', errs[errs.length >> 1].toFixed(2), 'Max', errs[errs.length - 1].toFixed(2), '| systematisch', r.systematic && Object.fromEntries(Object.entries(r.systematic).map(([k, v]) => [k, +v.toFixed(3)])), r.warning || '');
}
