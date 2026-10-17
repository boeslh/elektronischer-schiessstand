#!/usr/bin/env python3
"""Erzeugt das Kommunikationsdiagramm der Anlage (Beispiel: 3 Staende,
2 Anzeige-Bildschirme, alle anderen Komponenten je einmal).

Das Diagramm wird hier EINMAL beschrieben und daraus zweifach ausgegeben:

  docs/kommunikation.drawio          editierbar in draw.io / diagrams.net
  docs/bilder/kommunikation.png      Bild fuer die Dokumentation (SVG,
                                     per Playwright-Chromium gerendert)

Aufruf:  docs/.venv/bin/python docs/tools/kommunikation_diagramm.py
"""

import html
from pathlib import Path

DOCS = Path(__file__).resolve().parent.parent
W, H = 1420, 1070

# Farben je Verbindungsart (siehe Legende)
C_HTTP = "#1f6fb2"     # HTTP / SSE
C_SQL = "#d9822b"      # PostgreSQL
C_DEV = "#2e8b57"      # Messplatine / Geraet (seriell, TCP)
C_EXT = "#7a7a7a"      # extern / optisch

# Knoten: id -> (x, y, w, h, text, fill, stroke, shape)
NODES = {}
EDGES = []   # (id, src, dst, points, text, color, dashed, both, label_xy)
GROUPS = []  # (x, y, w, h, text)


def node(nid, x, y, w, h, text, fill="#ffffff", stroke="#4a5866", shape="rect"):
    NODES[nid] = (x, y, w, h, text, fill, stroke, shape)


def edge(src, dst, points, text="", color=C_HTTP, dashed=False, both=True, label_xy=None):
    EDGES.append((f"e{len(EDGES)}", src, dst, points, text, color, dashed, both, label_xy))


# ---- drei Staende ----------------------------------------------------------
for i in range(3):
    n = i + 1
    y0 = 40 + i * 340
    GROUPS.append((30, y0, 530, 310, f"Stand {n}"))
    node(f"esp{n}", 50, y0 + 60, 150, 80,
         "ESP32-Messplatine\nMikrofone + Piezo\nberechnet Trefferlage", "#eaf5ee", C_DEV)
    node(f"spc{n}", 340, y0 + 50, 200, 100,
         f"Stand-PC (Stand {n})\nschiessstand-standpc\nHTTP :900{n} · ESP :920{n}\nRing/Zehntel, Probe/Wertung",
         "#e8f0fa", C_HTTP)
    node(f"tab{n}", 340, y0 + 205, 200, 70, "Schützenanzeige\nBrowser oder\nStandanzeige-App", "#f4f6f8")
    via = "USB-Seriell\n115200 Bd" if n == 1 else "TCP (WLAN/LAN)\nHMAC-signiert"
    edge(f"esp{n}", f"spc{n}", [(200, y0 + 100), (340, y0 + 100)], via, C_DEV, label_xy=(270, y0 + 78))
    edge(f"spc{n}", f"tab{n}", [(440, y0 + 150), (440, y0 + 205)], "HTTP + SSE", C_HTTP,
         label_xy=(486, y0 + 178))
node("app", 50, 245, 150, 70, "App\n„My Shootings“\n(Smartphone)", "#f4f6f8")
edge("tab1", "app", [(340, 280), (200, 280)], "QR-Code", C_EXT, dashed=True, both=False, label_xy=(270, 266))

# ---- zentrale Dienste --------------------------------------------------------
node("wm", 760, 60, 280, 110, "Wertmaschine  :8092\nschiessstand-wertmaschine\nPapierscheiben-Ergebnisse", "#e8f0fa", C_HTTP)
node("srv", 760, 330, 280, 130,
     "Server  :8090\nschiessstand-server\nVerwaltung, REST-API, SSE /events\nPDF, E-Mail, Druck", "#e8f0fa", C_HTTP)
node("db", 760, 640, 280, 110, "PostgreSQL  :5432\nDatenbank „schiessstand“", "#fdf1e4", C_SQL, "cyl")
node("pa", 760, 900, 280, 110,
     "Preisanzeige  :8091\nschiessstand-preisanzeige\nAnzeigen /anzeige/{id}\nErgebnislisten /ps/…", "#e8f0fa", C_HTTP)

# Stand-PC -> Server (HTTP) und Stand-PC -> DB (nur Schuesse)
for i in range(3):
    n = i + 1
    y0 = 40 + i * 340
    sy, bx = 360 + i * 35, 600 + i * 30
    edge(f"spc{n}", "srv", [(540, y0 + 75), (bx, y0 + 75), (bx, sy), (760, sy)],
         "HTTP :8090" if n == 1 else "", C_HTTP, label_xy=(685, 348) if n == 1 else None)
    dy, dx = 665 + i * 30, 700 + i * 15
    edge(f"spc{n}", "db", [(540, y0 + 125), (dx, y0 + 125), (dx, dy), (760, dy)],
         "INSERT shots" if n == 1 else "", C_SQL, both=False, label_xy=(640, y0 + 113) if n == 1 else None)

edge("srv", "db", [(900, 460), (900, 640)], "SQL lesen/schreiben\nLISTEN shot_fired", C_SQL,
     label_xy=(970, 550))
edge("pa", "db", [(900, 900), (900, 750)], "SQL nur lesen", C_SQL, both=False, label_xy=(955, 825))
edge("wm", "srv", [(900, 170), (900, 330)], "HTTP :8090\nHMAC (PSK)", C_HTTP, both=True, label_xy=(950, 250))

# ---- Browser und externe Geraete --------------------------------------------
node("disag", 1160, 40, 230, 60, "Disag RM III\n(Papierscheiben)", "#eaf5ee", C_DEV)
node("bwm", 1160, 125, 230, 55, "Browser Wertmaschine", "#f4f6f8")
edge("wm", "disag", [(1040, 80), (1160, 70)], "RS-232", C_DEV, label_xy=(1100, 62))
edge("wm", "bwm", [(1040, 145), (1160, 152)], "HTTP + SSE", C_HTTP, label_xy=(1100, 136))

node("bv", 1160, 300, 230, 80, "Browser Verwaltung\nBüro, Kasse, Aufsicht,\nTechnik", "#f4f6f8")
edge("bv", "srv", [(1160, 345), (1040, 350)], "HTTP + SSE", C_HTTP, label_xy=(1100, 333))
node("smtp", 1160, 410, 230, 45, "Mailserver (SMTP)", "#f7f7f7", C_EXT)
node("lp", 1160, 470, 230, 45, "Drucker (CUPS)", "#f7f7f7", C_EXT)
edge("srv", "smtp", [(1040, 410), (1160, 432)], "SMTP", C_EXT, both=False, label_xy=(1100, 413))
edge("srv", "lp", [(1040, 440), (1160, 492)], "lp", C_EXT, both=False, label_xy=(1105, 474))
node("onl", 1160, 530, 230, 55, "Online-Anzeige (Internet)\noptional, HTTPS :443", "#f7f7f7", C_EXT)
edge("srv", "onl", [(1040, 455), (1160, 557)], "HTTPS", C_EXT, both=False, label_xy=(1092, 500))

node("b1", 1160, 880, 230, 60, "Anzeige-Bildschirm 1\nBrowser, z. B. RWK-Dynamisch", "#f4f6f8")
node("b2", 1160, 965, 230, 60, "Anzeige-Bildschirm 2\nBrowser, z. B. Preisschießen", "#f4f6f8")
edge("b1", "pa", [(1160, 910), (1040, 930)], "HTTP :8091", C_HTTP, both=False, label_xy=(1100, 905))
edge("b2", "pa", [(1160, 995), (1040, 975)], "HTTP :8091", C_HTTP, both=False, label_xy=(1100, 997))

LEGEND = [(C_HTTP, False, "HTTP / SSE"), (C_SQL, False, "PostgreSQL"),
          (C_DEV, False, "Gerät (seriell / TCP)"), (C_EXT, True, "extern / optisch")]
LEGEND_POS = (1160, 610)


# ---------------------------------------------------------------------------
# draw.io
# ---------------------------------------------------------------------------

def drawio_xml() -> str:
    def esc(t):
        return html.escape(t.replace("\n", "<br>"), quote=True)

    cells = ['<mxCell id="0"/>', '<mxCell id="1" parent="0"/>']
    for k, (x, y, w, h, t) in enumerate(GROUPS):
        cells.append(
            f'<mxCell id="g{k}" value="{esc(t)}" style="rounded=1;arcSize=4;dashed=1;fillColor=#fbfcfd;'
            f'strokeColor=#9aa7b4;verticalAlign=top;align=left;spacingLeft=10;fontStyle=1;fontSize=14;html=1;" '
            f'vertex="1" parent="1"><mxGeometry x="{x}" y="{y}" width="{w}" height="{h}" as="geometry"/></mxCell>')
    for nid, (x, y, w, h, t, fill, stroke, shape) in NODES.items():
        lines = t.split("\n")
        val = f"<b>{html.escape(lines[0])}</b>" + "".join("<br>" + html.escape(s) for s in lines[1:])
        st = (f"shape=cylinder3;boundedLbl=1;size=12;" if shape == "cyl" else "rounded=1;arcSize=10;") + \
             f"whiteSpace=wrap;html=1;fillColor={fill};strokeColor={stroke};fontSize=12;"
        cells.append(f'<mxCell id="{nid}" value="{html.escape(val, quote=True)}" style="{st}" vertex="1" parent="1">'
                     f'<mxGeometry x="{x}" y="{y}" width="{w}" height="{h}" as="geometry"/></mxCell>')
    for eid, src, dst, pts, t, color, dashed, both, _ in EDGES:
        st = (f"edgeStyle=none;html=1;rounded=0;strokeWidth=2;strokeColor={color};fontColor={color};fontSize=11;"
              f"labelBackgroundColor=#ffffff;endArrow=block;endFill=1;"
              f"startArrow={'block' if both else 'none'};startFill=1;" + ("dashed=1;" if dashed else ""))
        (sx, sy), (tx, ty) = pts[0], pts[-1]
        mid = "".join(f'<mxPoint x="{px}" y="{py}"/>' for px, py in pts[1:-1])
        cells.append(
            f'<mxCell id="{eid}" value="{esc(t)}" style="{st}" edge="1" parent="1" source="{src}" target="{dst}">'
            f'<mxGeometry relative="1" as="geometry"><mxPoint x="{sx}" y="{sy}" as="sourcePoint"/>'
            f'<mxPoint x="{tx}" y="{ty}" as="targetPoint"/>'
            + (f'<Array as="points">{mid}</Array>' if mid else "") + '</mxGeometry></mxCell>')
    lx, ly = LEGEND_POS
    cells.append(f'<mxCell id="leg" value="&lt;b&gt;Verbindungen&lt;/b&gt;" style="rounded=1;fillColor=#ffffff;'
                 f'strokeColor=#c3ccd5;verticalAlign=top;align=left;spacingLeft=10;html=1;" vertex="1" parent="1">'
                 f'<mxGeometry x="{lx}" y="{ly}" width="230" height="150" as="geometry"/></mxCell>')
    for k, (color, dashed, t) in enumerate(LEGEND):
        y = ly + 45 + k * 26
        cells.append(
            f'<mxCell id="lg{k}" value="{html.escape(t)}" style="endArrow=none;html=1;strokeWidth=2;strokeColor={color};'
            f'labelPosition=right;align=left;spacingLeft=8;fontSize=12;' + ("dashed=1;" if dashed else "") +
            f'" edge="1" parent="1"><mxGeometry relative="1" as="geometry"><mxPoint x="{lx + 15}" y="{y}" '
            f'as="sourcePoint"/><mxPoint x="{lx + 60}" y="{y}" as="targetPoint"/></mxGeometry></mxCell>')
    body = "".join(cells)
    return ('<mxfile host="schiessstand-doku"><diagram name="Kommunikation" id="kommunikation">'
            f'<mxGraphModel dx="{W}" dy="{H}" grid="1" gridSize="10" page="1" pageWidth="{W}" pageHeight="{H}">'
            f'<root>{body}</root></mxGraphModel></diagram></mxfile>\n')


# ---------------------------------------------------------------------------
# SVG (fuer das PNG)
# ---------------------------------------------------------------------------

def svg() -> str:
    out = [f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" viewBox="0 0 {W} {H}" '
           'font-family="DejaVu Sans, Arial, sans-serif">',
           '<rect width="100%" height="100%" fill="#ffffff"/>', "<defs>"]
    for c in {C_HTTP, C_SQL, C_DEV, C_EXT}:
        k = c[1:]
        out.append(f'<marker id="a{k}" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" '
                   f'orient="auto-start-reverse"><path d="M0,0 L10,5 L0,10 z" fill="{c}"/></marker>')
    out.append("</defs>")

    def text(x, y, lines, size=12, color="#1d2733", bold_first=False, anchor="middle"):
        lh = size * 1.3
        y0 = y - (len(lines) - 1) * lh / 2
        for k, s in enumerate(lines):
            fw = "bold" if bold_first and k == 0 else "normal"
            fs = size + 1 if bold_first and k == 0 else size
            out.append(f'<text x="{x}" y="{y0 + k * lh:.1f}" font-size="{fs}" font-weight="{fw}" fill="{color}" '
                       f'text-anchor="{anchor}" dominant-baseline="central">{html.escape(s)}</text>')

    for x, y, w, h, t in GROUPS:
        out.append(f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="10" fill="#fbfcfd" stroke="#9aa7b4" '
                   'stroke-dasharray="6 4"/>')
        out.append(f'<text x="{x + 12}" y="{y + 22}" font-size="15" font-weight="bold" fill="#4a5866">{t}</text>')

    # Kanten unter den Knoten
    labels = []
    for _, _, _, pts, t, color, dashed, both, lxy in EDGES:
        d = " ".join(f"{px},{py}" for px, py in pts)
        k = color[1:]
        out.append(f'<polyline points="{d}" fill="none" stroke="{color}" stroke-width="2" '
                   + ('stroke-dasharray="6 4" ' if dashed else "")
                   + f'marker-end="url(#a{k})" ' + (f'marker-start="url(#a{k})" ' if both else "") + "/>")
        if t and lxy:
            labels.append((lxy, t.split("\n"), color))

    for nid, (x, y, w, h, t, fill, stroke, shape) in NODES.items():
        if shape == "cyl":
            ry = 12
            out.append(f'<path d="M{x},{y + ry} A{w / 2},{ry} 0 0 1 {x + w},{y + ry} V{y + h - ry} '
                       f'A{w / 2},{ry} 0 0 1 {x},{y + h - ry} Z" fill="{fill}" stroke="{stroke}" stroke-width="1.5"/>')
            out.append(f'<path d="M{x},{y + ry} A{w / 2},{ry} 0 0 0 {x + w},{y + ry}" fill="none" '
                       f'stroke="{stroke}" stroke-width="1.5"/>')
            text(x + w / 2, y + h / 2 + 6, t.split("\n"), bold_first=True)
        else:
            out.append(f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="8" fill="{fill}" stroke="{stroke}" '
                       'stroke-width="1.5"/>')
            text(x + w / 2, y + h / 2, t.split("\n"), bold_first=True)

    for (lx, ly), lines, color in labels:
        wmax = max(len(s) for s in lines) * 6.4 + 8
        hh = len(lines) * 14 + 4
        out.append(f'<rect x="{lx - wmax / 2:.1f}" y="{ly - hh / 2:.1f}" width="{wmax:.1f}" height="{hh}" '
                   'rx="3" fill="#ffffff" fill-opacity="0.92"/>')
        text(lx, ly, lines, size=11, color=color)

    lx, ly = LEGEND_POS
    out.append(f'<rect x="{lx}" y="{ly}" width="230" height="150" rx="8" fill="#ffffff" stroke="#c3ccd5"/>')
    out.append(f'<text x="{lx + 12}" y="{ly + 22}" font-size="13" font-weight="bold" fill="#1d2733">Verbindungen</text>')
    for k, (color, dashed, t) in enumerate(LEGEND):
        y = ly + 45 + k * 26
        out.append(f'<line x1="{lx + 15}" y1="{y}" x2="{lx + 60}" y2="{y}" stroke="{color}" stroke-width="2.5" '
                   + ('stroke-dasharray="6 4"' if dashed else "") + "/>")
        out.append(f'<text x="{lx + 70}" y="{y}" font-size="12" fill="#1d2733" dominant-baseline="central">'
                   f'{html.escape(t)}</text>')
    out.append("</svg>")
    return "\n".join(out)


def main():
    (DOCS / "kommunikation.drawio").write_text(drawio_xml(), encoding="utf-8")
    print("  docs/kommunikation.drawio")
    from playwright.sync_api import sync_playwright
    with sync_playwright() as pw:
        b = pw.chromium.launch()
        p = b.new_page(viewport={"width": W, "height": H}, device_scale_factor=1.5)
        p.set_content(f'<html><body style="margin:0">{svg()}</body></html>')
        p.screenshot(path=str(DOCS / "bilder" / "kommunikation.png"), clip={"x": 0, "y": 0, "width": W, "height": H})
        b.close()
    print("  docs/bilder/kommunikation.png")


if __name__ == "__main__":
    main()
