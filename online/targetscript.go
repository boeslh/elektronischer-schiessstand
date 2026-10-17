// target_js.go – Zeichnen einer Scheibe mit Treffern (SVG), uebernommen aus
// preisanzeige/standanzeige.go standTargetJS (gleiches Aussehen wie die
// Anzeigen am Schiessstand).
package main

const standTargetJS = `// Muss VOR den Kachel-Markup stehen (siehe renderStandTile: jede Kachel ruft
// drawStandTarget() direkt in einem eigenen <script>-Tag auf, sobald der
// Parser dort ankommt) - stand die Funktion frueher in standanzeigeFoot
// (also NACH allen Kacheln), war sie beim ersten Aufruf noch undefiniert
// (ReferenceError, kein einziges Trefferbild wurde gezeichnet). Bugfix:
// "anzeige/1 zeigt keine Grafiken".
function contrastColor(hex) {
  const r = parseInt(hex.slice(1,3),16), g = parseInt(hex.slice(3,5),16), b = parseInt(hex.slice(5,7),16);
  return (0.299*r + 0.587*g + 0.114*b) / 255 > 0.5 ? '#000000' : '#FFFFFF';
}
function shotColorByRing(s) {
  if (s.ring == null) return '#666666';
  if (s.ring >= 10) return '#e04040';
  if (s.ring === 9)  return '#f3fb06';
  return '#4ab8ff';
}
const STAND_ZOOM_FRACTIONS = [0.0626, 0.2418, 0.4209, 0.60, 1.00];
// Kaliber in mm - reale Groesse des Trefferpunkts (wie schussbild.go CALIBER),
// skaliert also automatisch mit der Scheibengeometrie statt einem fixen
// Bruchteil des Aussenrings (der bei kleinem Zoom viel zu klein wirkte).
const STAND_CALIBER = 4.5;
// Schussrand: halbe Staerke, in der Farbe des Schusses und deckend
const STAND_SHOT_STROKE_W = STAND_CALIBER * 0.025;
const STAND_SHOT_R = STAND_CALIBER / 2 - STAND_SHOT_STROKE_W / 2;

function drawStandTarget(svgID, geo, shots) {
  const svg = document.getElementById(svgID);
  if (!svg || !geo || !geo.rings || !geo.rings.length) return;
  shots = shots || [];
  const NS = 'http://www.w3.org/2000/svg';
  const rings = [...geo.rings].sort((a,b) => b.d - a.d);
  const outerR = rings[0].d / 2;

  // Auto-Zoom analog der Auswertungsansicht (schussbild.go computeViewR):
  // kleinste feste Zoomstufe waehlen, die alle gezeigten Schuesse noch zeigt.
  // Ohne Schuss (noch nichts gefallen) volle Scheibe zeigen statt auf die
  // kleinste Stufe (0) zu "zoomen".
  let viewR = outerR;
  let maxDist = 0;
  let hasShot = false;
  for (const s of shots) {
    if (s.x_mm == null || s.y_mm == null) continue;
    hasShot = true;
    maxDist = Math.max(maxDist, Math.sqrt(s.x_mm*s.x_mm + s.y_mm*s.y_mm));
  }
  if (hasShot) {
    for (const f of STAND_ZOOM_FRACTIONS) {
      const r = f * outerR;
      if (r >= maxDist) { viewR = r; break; }
    }
  }
  const pad = Math.max(viewR * 0.08, outerR * 0.02);
  svg.setAttribute('viewBox', (-(viewR+pad))+' '+(-(viewR+pad))+' '+(2*(viewR+pad))+' '+(2*(viewR+pad)));

  const bg = document.createElementNS(NS,'circle');
  bg.setAttribute('r', outerR); bg.setAttribute('fill', '#e8e4dc');
  svg.appendChild(bg);

  let darkR = 0;
  for (const r of rings) if (r.filled) darkR = Math.max(darkR, r.d/2);
  if (darkR > 0) {
    const sp = document.createElementNS(NS,'circle');
    sp.setAttribute('r', darkR); sp.setAttribute('fill', '#1c1c1c');
    svg.appendChild(sp);
  }
  const strokeW = Math.max(outerR*0.006, 0.15);
  for (const r of rings) {
    const c = document.createElementNS(NS,'circle');
    c.setAttribute('r', r.d/2); c.setAttribute('fill','none');
    c.setAttribute('stroke', r.filled ? '#888' : '#555'); c.setAttribute('stroke-width', strokeW);
    svg.appendChild(c);
  }
  if (geo.inner10_d > 0) {
    const inn = document.createElementNS(NS,'circle');
    inn.setAttribute('r', geo.inner10_d/2); inn.setAttribute('fill','none');
    inn.setAttribute('stroke','#888'); inn.setAttribute('stroke-width', strokeW);
    if (geo.inner10_dashed) { const dl = strokeW*5; inn.setAttribute('stroke-dasharray', dl+' '+dl); }
    svg.appendChild(inn);
  }

  // Ringzahlen (analog schussbild.go buildTarget): je Ring 1-8 an vier
  // Positionen (N/O/S/W) auf der Mitte zwischen diesem und dem naechst
  // inneren Ring beschriftet.
  let minStep = Infinity;
  for (let i = 0; i < rings.length - 1; i++) minStep = Math.min(minStep, (rings[i].d - rings[i+1].d) / 2);
  const labelFontSize = Math.max(minStep * 0.58, outerR * 0.045);
  const labelPositions = [{nx:0,ny:-1},{nx:1,ny:0},{nx:0,ny:1},{nx:-1,ny:0}];
  for (let i = 0; i < rings.length; i++) {
    const r = rings[i];
    if (r.v < 1 || r.v > 8) continue; // die 9 steht auf Original-Scheiben nicht (zu gedraengt)
    const outerRadius = r.d / 2;
    const innerRadius = (i + 1 < rings.length) ? rings[i + 1].d / 2 : 0;
    const labelR = (outerRadius + innerRadius) / 2;
    const fillColor = r.filled ? '#bbb' : '#555';
    for (const p of labelPositions) {
      const t = document.createElementNS(NS,'text');
      t.setAttribute('x', p.nx * labelR); t.setAttribute('y', p.ny * labelR);
      t.setAttribute('text-anchor', 'middle');
      t.setAttribute('dominant-baseline', 'middle');
      t.setAttribute('font-size', labelFontSize);
      t.setAttribute('font-family', 'sans-serif');
      t.setAttribute('fill', fillColor);
      t.setAttribute('pointer-events', 'none');
      t.textContent = r.v;
      svg.appendChild(t);
    }
  }

  for (const s of shots) {
    if (s.x_mm == null || s.y_mm == null) continue;
    const fillColor = shotColorByRing(s);
    const g = document.createElementNS(NS,'g');

    const c = document.createElementNS(NS,'circle');
    c.setAttribute('cx', s.x_mm); c.setAttribute('cy', -s.y_mm);
    c.setAttribute('r', STAND_SHOT_R);
    c.setAttribute('fill', fillColor); c.setAttribute('fill-opacity','0.85');
    c.setAttribute('stroke', fillColor); c.setAttribute('stroke-opacity', '1'); c.setAttribute('stroke-width', STAND_SHOT_STROKE_W);
    g.appendChild(c);

    const t = document.createElementNS(NS,'text');
    t.setAttribute('x', s.x_mm); t.setAttribute('y', -s.y_mm);
    t.setAttribute('text-anchor', 'middle');
    t.setAttribute('dominant-baseline', 'central');
    t.setAttribute('font-size', STAND_CALIBER*0.42);
    t.setAttribute('font-family', 'sans-serif');
    t.setAttribute('font-weight', 'bold');
    t.setAttribute('fill', contrastColor(fillColor));
    t.textContent = String(s.no);
    g.appendChild(t);

    svg.appendChild(g);
  }
}
`
