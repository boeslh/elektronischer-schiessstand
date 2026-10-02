#!/usr/bin/env python3
"""Baut aus den Markdown-Kapiteln (docs/NN-*.md) PDF-Dateien.

  build_pdf.py            Gesamt-PDF  docs/pdf/Schiessstand-Funktionsuebersicht.pdf
  build_pdf.py --einzeln  zusaetzlich je Kapitel eine PDF

Markdown -> HTML (Python-Markdown), Druck mit Chromium (Playwright). Aufruf
ueber docs/build-pdf.sh, das die Python-Umgebung bei Bedarf anlegt.
"""

import datetime
import html
import re
import sys
from pathlib import Path

import markdown
from markdown.extensions.toc import TocExtension
from playwright.sync_api import sync_playwright

DOCS = Path(__file__).resolve().parent.parent
OUT = DOCS / "pdf"
TITLE = "Elektronischer Schießstand – Funktionsübersicht"

CSS = """
@page { size: A4; margin: 18mm 16mm 20mm 16mm; }
html { font-family: "DejaVu Sans", "Noto Sans", Arial, sans-serif; font-size: 10pt; color: #1d2329; }
body { margin: 0; line-height: 1.45; }
h1 { font-size: 20pt; color: #0f5e50; border-bottom: 2px solid #4ab8a0; padding-bottom: 4px;
     margin: 0 0 12px; break-before: page; }
h1.first, .cover + h1 { break-before: auto; }
h2 { font-size: 14pt; color: #0f5e50; margin: 18px 0 6px; break-after: avoid; }
h3 { font-size: 11.5pt; margin: 14px 0 4px; break-after: avoid; }
p, li { orphans: 3; widows: 3; }
img { display: block; max-width: 100%; max-height: 205mm; margin: 8px auto 4px;
      border: 1px solid #c9d2d9; border-radius: 4px; break-inside: avoid; }
p:has(> img) { break-inside: avoid; margin: 6px 0; }
p > em:only-child { display: block; text-align: center; color: #5a6670; font-size: 9pt; margin-top: -2px; }
table { border-collapse: collapse; width: 100%; margin: 8px 0; font-size: 9pt; break-inside: avoid; }
th, td { border: 1px solid #c9d2d9; padding: 4px 6px; text-align: left; vertical-align: top; }
th { background: #e9f4f1; }
code { font-family: "DejaVu Sans Mono", monospace; font-size: 8.5pt; background: #eef1f3; padding: 0 3px; border-radius: 3px; }
pre { background: #f4f6f7; border: 1px solid #dde3e7; padding: 8px; font-size: 7.5pt; line-height: 1.25;
      white-space: pre; overflow: hidden; break-inside: avoid; }
pre code { background: none; padding: 0; }
a { color: #0f5e50; text-decoration: none; }
.cover { height: 240mm; display: flex; flex-direction: column; justify-content: center; }
.cover .t { font-size: 26pt; font-weight: bold; color: #0f5e50; }
.cover .s { font-size: 13pt; color: #5a6670; margin-top: 8px; }
.cover .d { font-size: 10pt; color: #5a6670; margin-top: 40px; }
.toc { break-before: page; }
.toc h1 { break-before: auto; }
.toc ol { font-size: 11pt; line-height: 1.9; }
"""


def slugify(value, sep):
    # wie GitHub: Kleinschreibung, Satzzeichen weg, Leerzeichen -> "-"
    value = re.sub(r"[^\w\- ]", "", value.strip().lower())
    return value.replace(" ", sep)


def chapters():
    return sorted(p for p in DOCS.glob("[0-9][0-9]-*.md"))


def to_html(md_text):
    # Verweise zwischen Kapiteln in Sprungmarken innerhalb der PDF umwandeln
    md_text = re.sub(r"\]\((\d\d-[\w-]+)\.md#([^)]+)\)", r"](#\2)", md_text)
    md_text = re.sub(r"\]\((\d\d-[\w-]+)\.md\)", r"](#kap-\1)", md_text)
    return markdown.markdown(md_text, extensions=[
        "tables", "fenced_code", "attr_list",
        TocExtension(slugify=slugify, permalink=False),
    ])


def render(pages, pdf_path, title):
    doc = f"""<!doctype html><html lang="de"><head><meta charset="utf-8">
<title>{html.escape(title)}</title><style>{CSS}</style></head><body>{pages}</body></html>"""
    tmp = DOCS / ".pdf-tmp.html"  # im docs-Ordner, damit bilder/... gefunden werden
    tmp.write_text(doc, encoding="utf-8")
    try:
        with sync_playwright() as pw:
            b = pw.chromium.launch()
            pg = b.new_page()
            pg.goto(tmp.as_uri(), wait_until="networkidle")
            footer = ('<div style="font-size:7pt;color:#7a858e;width:100%;padding:0 16mm;'
                      'display:flex;justify-content:space-between;">'
                      f'<span>{html.escape(title)}</span>'
                      '<span>Seite <span class="pageNumber"></span> / <span class="totalPages"></span></span></div>')
            pg.pdf(path=str(pdf_path), format="A4", print_background=True, display_header_footer=True,
                   header_template="<span></span>", footer_template=footer,
                   margin={"top": "16mm", "bottom": "18mm", "left": "16mm", "right": "16mm"})
            b.close()
    finally:
        tmp.unlink(missing_ok=True)
    print("  ", pdf_path.relative_to(DOCS.parent))


def main():
    OUT.mkdir(exist_ok=True)
    chs = chapters()
    today = datetime.date.today().strftime("%d.%m.%Y")

    parts, toc = [], []
    for p in chs:
        text = p.read_text(encoding="utf-8")
        h1 = re.search(r"^# (.+)$", text, re.M).group(1)
        name = html.escape(re.sub(r"^\d+\. ", "", h1))
        toc.append(f'<li><a href="#kap-{p.stem}">{name}</a></li>')
        parts.append(f'<a id="kap-{p.stem}"></a>' + to_html(text))

    cover = (f'<div class="cover"><div class="t">Elektronischer Schießstand</div>'
             f'<div class="s">Funktionsübersicht</div><div class="d">Stand: {today}</div></div>')
    toc_html = f'<div class="toc"><h1>Inhalt</h1><ol>{"".join(toc)}</ol></div>'
    render(cover + toc_html + "".join(parts), OUT / "Schiessstand-Funktionsuebersicht.pdf", TITLE)

    if "--einzeln" in sys.argv:
        for p in chs:
            body = to_html(p.read_text(encoding="utf-8")).replace("<h1", '<h1 class="first"', 1)
            h1 = re.search(r"^# (.+)$", p.read_text(encoding="utf-8"), re.M).group(1)
            render(body, OUT / f"{p.stem}.pdf", f"Schießstand – {h1}")


if __name__ == "__main__":
    main()
