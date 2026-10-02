#!/usr/bin/env python3
"""Erzeugt die Screenshots der Dokumentation (docs/bilder/*.png).

Voraussetzung: laufender Server (:8090), Stand-PC (:9004) und Preisanzeige
(:8091) auf diesem Rechner, Python-Umgebung mit Playwright (siehe
docs/build-pdf.sh, das docs/.venv anlegt).

Fuer den Zugriff auf alle Kacheln legt das Skript eine kurzlebige Admin-
Sitzung in ui_role_sessions an und loescht sie am Ende wieder (Datenbank-
Zugang aus /etc/schiessstand/server.env, daher mit sudo-Rechten zum Lesen).

Aufruf:   docs/.venv/bin/python docs/tools/screenshots.py [name ...]
          (ohne Namen: alle Screenshots)
"""

import json
import re
import subprocess
import sys
from pathlib import Path

from playwright.sync_api import sync_playwright

sys.path.insert(0, str(Path(__file__).resolve().parent))
from anon import Anonymizer  # noqa: E402

SERVER = "http://localhost:8090"
STANDPC = "http://localhost:9004"
PREISANZEIGE = "http://localhost:8091"
WERTMASCHINE = "http://localhost:8092"
OUT = Path(__file__).resolve().parent.parent / "bilder"
TOKEN = "doku-screenshots-tmp-token"
VIEW = {"width": 1440, "height": 900}


def dsn() -> str:
    env = subprocess.run(["sudo", "cat", "/etc/schiessstand/server.env"],
                         capture_output=True, text=True, check=True).stdout
    return re.search(r"SCHIESSSTAND_DSN=(.*)", env).group(1).strip().strip('"')


def sql(query: str) -> str:
    return subprocess.run(["psql", dsn(), "-At", "-c", query],
                          capture_output=True, text=True, check=True).stdout.strip()


# Unterkante des sichtbaren Inhalts (ohne fest positionierte Elemente) -
# Bilder werden darauf zugeschnitten, damit keine grossen Leerflaechen bleiben.
CONTENT_BOTTOM_JS = """() => {
  // Blatt-Elemente (Text, Eingabefelder ...) und umrandete Kaesten, die nicht
  // die ganze Seite umspannen - Seitencontainer mit min-height:100vh zaehlen nicht.
  let b = 0;
  const vh = window.innerHeight;
  for (const el of document.querySelectorAll('body *')) {
    const cs = getComputedStyle(el);
    if (cs.position === 'fixed' || cs.display === 'none' || cs.visibility === 'hidden') continue;
    const r = el.getBoundingClientRect();
    if (r.width === 0 || r.height === 0) continue;
    const leaf = el.children.length === 0;
    const boxed = parseFloat(cs.borderBottomWidth) > 0 && r.height < vh * 0.9;
    if (!leaf && !boxed) continue;
    b = Math.max(b, r.bottom + window.scrollY);
  }
  return Math.ceil(b);
}"""


def shot(page, name, full=False, clip_h=None, crop=True):
    path = OUT / f"{name}.png"
    if clip_h:
        page.screenshot(path=str(path), clip={"x": 0, "y": 0, "width": VIEW["width"], "height": clip_h},
                        full_page=True)
    elif full:
        page.screenshot(path=str(path), full_page=True)
    elif crop:
        h = min(VIEW["height"], page.evaluate(CONTENT_BOTTOM_JS) + 20)
        h = max(h, 200)
        page.screenshot(path=str(path), clip={"x": 0, "y": 0, "width": VIEW["width"], "height": h})
    else:
        page.screenshot(path=str(path))
    print("  ", path.name)


def goto(page, url, wait=800):
    page.goto(url, wait_until="networkidle")
    page.wait_for_timeout(wait)


def click_text(page, text, wait=900):
    page.get_by_text(text, exact=True).first.click()
    page.wait_for_timeout(wait)


# ---------------------------------------------------------------------------
# Pseudonymisierung aller Antworten (siehe anon.py)
# ---------------------------------------------------------------------------

ANON = None


def install_anon(ctx):
    def handle(route):
        url = route.request.url
        path = url.split("?")[0]
        # Live-Streams nicht anfassen (wuerden beim Puffern haengen)
        if path.endswith("/events"):
            route.continue_()
            return
        if re.search(r"\.(js|css|png|jpg|svg|ico|woff2?)$", path) and "/qr.svg" not in path:
            route.continue_()
            return
        try:
            resp = route.fetch()
        except Exception:
            route.abort()
            return
        ct = resp.headers.get("content-type", "")
        if "json" in ct:
            route.fulfill(response=resp, body=ANON.json_text(resp.text()))
        elif "html" in ct and (url.startswith(PREISANZEIGE) or url.startswith(WERTMASCHINE)):
            route.fulfill(response=resp, body=ANON.html_text(resp.text()))
        else:
            route.fulfill(response=resp)
    ctx.route("**/*", handle)


# ---------------------------------------------------------------------------
# Szenen
# ---------------------------------------------------------------------------

def server_pages(ctx, ids):
    p = ctx.new_page()
    goto(p, SERVER + "/")
    shot(p, "start", full=True)

    goto(p, SERVER + "/lanes", 1500)
    shot(p, "standsteuerung")

    goto(p, SERVER + "/stammdaten")
    click_text(p, "Mitglieder")
    shot(p, "stammdaten-mitglieder")
    click_text(p, "Vereine")
    shot(p, "stammdaten-vereine")

    goto(p, SERVER + "/disciplines")
    shot(p, "disziplinen")

    goto(p, SERVER + "/wettkampf")
    shot(p, "wettkaempfe")

    goto(p, SERVER + "/standaktion")
    shot(p, "standaktion")

    goto(p, SERVER + "/ergebnisse", 1500)
    shot(p, "ergebnisse")

    goto(p, SERVER + "/simulator", 1500)
    p.fill("#f-date", ids["raw_date"])
    p.get_by_text("Suchen", exact=True).first.click()
    p.wait_for_timeout(1500)
    p.locator("tr").nth(1).click()
    p.wait_for_timeout(3000)
    shot(p, "simulator", clip_h=1600)

    goto(p, SERVER + "/geraetekonfiguration", 1500)
    shot(p, "geraetekonfiguration")

    goto(p, SERVER + "/auswertung")
    shot(p, "auswertung")

    goto(p, SERVER + "/anzeigen")
    shot(p, "anzeigen")

    goto(p, SERVER + "/settings")
    shot(p, "einstellungen", clip_h=1500)
    p.locator("h2", has_text="Ergebnis-Ausgabe am Stand").evaluate(
        "e => { e.closest('.section').scrollIntoView({block: 'start'}); window.scrollBy(0, -16); }")
    p.wait_for_timeout(300)
    shot(p, "einstellungen-ergebnis-ausgabe")

    goto(p, SERVER + "/benutzerverwaltung")
    shot(p, "benutzerverwaltung")

    goto(p, SERVER + "/import-export")
    shot(p, "import-export")

    goto(p, SERVER + "/archiv")
    shot(p, "archiv")

    goto(p, SERVER + "/entwicklung")
    shot(p, "entwicklung")
    p.close()


def preisschiessen_pages(ctx, ids):
    p = ctx.new_page()
    p.add_init_script(f"localStorage.setItem('ps_current_id', '{ids['ps']}')")
    goto(p, SERVER + "/preisschiessen-liste")
    shot(p, "preisschiessen-liste")
    goto(p, SERVER + "/preisschiessen", 1500)
    shot(p, "ps-anmeldung")
    for tab, name in [("Kasse", "ps-kasse"), ("Teilnehmer", "ps-teilnehmer"), ("Stand", "ps-stand"),
                      ("Gewinne", "ps-gewinne"), ("Verwaltung", "ps-verwaltung")]:
        p.locator(".tab-btn", has_text=re.compile(f"^{tab}$")).first.click()
        p.wait_for_timeout(1200)
        shot(p, name)
    p.locator(".tab-btn", has_text=re.compile("^Scheiben$")).first.click()
    p.wait_for_timeout(1200)
    shot(p, "ps-verwaltung-scheiben")
    p.locator(".tab-btn", has_text=re.compile("^Auswertung$")).first.click()
    p.wait_for_timeout(1200)
    shot(p, "ps-auswertung-wertungen")
    for tab, name in [("Ergebnisvorschau", "ps-auswertung-vorschau"), ("Vereins-Wertung", "ps-auswertung-verein"),
                      ("Anzeige-Konfiguration", "ps-auswertung-anzeige")]:
        p.locator(".tab-btn", has_text=re.compile(f"^{tab}$")).first.click()
        p.wait_for_timeout(1500)
        shot(p, name)
    p.close()

    # Kasse mit geoeffnetem Teilnehmerkonto
    p = ctx.new_page()
    p.add_init_script(f"localStorage.setItem('ps_current_id', '{ids['ps']}');"
                      f"localStorage.setItem('ps_kasse_open_teilnehmer_id', '{ids['teilnehmer']}')")
    goto(p, SERVER + "/preisschiessen", 2500)
    shot(p, "ps-kasse-konto", clip_h=1300)
    p.close()


def vereinsabend_pages(ctx, ids):
    p = ctx.new_page()
    goto(p, SERVER + "/vereinsabende-liste")
    shot(p, "vereinsabend-liste")
    goto(p, SERVER + f"/vereinsabende?id={ids['saison']}", 1500)
    shot(p, "vereinsabend-tagesergebnisse")
    for tab, name in [("Tageswertung", "vereinsabend-tageswertung"), ("Saisonwertung", "vereinsabend-saisonwertung"),
                      ("Anwesenheit", "vereinsabend-anwesenheit"), ("Verwaltung", "vereinsabend-verwaltung")]:
        p.locator(".tab-btn", has_text=re.compile(f"^{tab}$")).first.click()
        p.wait_for_timeout(1500)
        shot(p, name)
    p.close()


def ergebnis_ansicht(ctx, ids):
    p = ctx.new_page()
    sid = ids["session"]
    goto(p, SERVER + f"/ergebnis-ansicht?session_id={sid}&shots_per_series=10&decimal=1"
         f"&shooter=Huber%2C%20Anna&discipline=LGA-30", 2000)
    shot(p, "ergebnis-ansicht")
    p.close()


def preisanzeige(ctx, ids):
    p = ctx.new_page()
    goto(p, PREISANZEIGE + "/")
    shot(p, "preisanzeige-start")
    goto(p, PREISANZEIGE + f"/ps/{ids['saison']}/", 2500)
    shot(p, "preisanzeige")
    p.get_by_text("Punkte1", exact=True).first.click()
    p.wait_for_timeout(2500)
    shot(p, "preisanzeige-liste")
    p.close()


# ---- Stand-PC-Anzeige: Status/Schuesse/Preisschiessen werden simuliert ----

def _stand_status(shooter="Huber, Anna", mode="wertung"):
    return {"lane_no": 4, "mode": mode, "shooter": shooter, "wertung_count": 23, "history_gen": 1,
            "dev_mode": False, "discipline_name": "LGA-30",
            "discipline": {"name": "LGA-30", "target_no": 1, "trial_shots": 100, "scoring_shots": 30,
                           "shots_per_series": 10, "decimal_scoring": True}}


def _stand_shots(ids, n=23):
    rows = json.loads(subprocess.run(
        ["curl", "-s", "-b", f"ss_role={TOKEN}", f"{SERVER}/api/sessions/{ids['session']}/shots"],
        capture_output=True, text=True, check=True).stdout)
    rows = [r for r in rows if r["kind"] == "match" and r["status"] == "valid"][:n]
    return [{"x_mm": r["x_mm"], "y_mm": r["y_mm"], "ring": r["ring"], "decimal": r["decimal"],
             "inner_ten": r["inner_ten"], "center_distance": r["center_distance"], "mode": "wertung"}
            for r in rows]


PS_INFO = {
    "preisschiessen_name": "Traditionsschießen 2027", "teilnehmer_id": "t1", "teilnehmer_nr": 17,
    "shooter_name": "Huber, Anna", "guthaben": 4.5, "pending": True,
    "scheiben_typen": [{"scheibe_id": "s1", "name": "Ehrenscheibe", "gekauft": 1, "beendet": 0},
                       {"scheibe_id": "s2", "name": "Meisterscheibe", "gekauft": 2, "beendet": 1}],
    "angebot": {"sets": [{"id": "x1", "name": "Set Ehren + Meister", "price": 12.0}],
                "scheiben": [{"id": "y1", "name": "Ehrenscheibe", "price": 5.0},
                             {"id": "y2", "name": "Meisterscheibe", "price": 8.0},
                             {"id": "y3", "name": "Glücksscheibe", "price": 2.0}]},
}


def _qr_svg(ids, shots):
    import segno
    parts = []
    for s in shots:
        parts.append(f"{round(s['x_mm'] * 10)},{round(s['y_mm'] * 10)},{round(s['decimal'] * 10)}")
    text = "|".join(["MS1", "123456789", "202610021930", "Huber, Anna", "", "LGA-30", "lg", "z", "10",
                     "2", "38", ";".join(parts)])
    import io
    buf = io.BytesIO()
    segno.make(text, error="l", micro=False).save(buf, kind="svg", scale=8, border=4, xmldecl=False)
    return buf.getvalue().decode()


def stand_pc(ctx, ids):
    shots = _stand_shots(ids)
    erg = {"session_id": "doku", "shooter_name": "Anna Huber", "discipline": "LGA-30",
           "started_at": "2026-10-02T19:30:00+02:00", "shot_count": 30, "mail_configured": True,
           "has_email": True, "email_masked": "an…@example.org", "print_available": True, "qr_max_shots": 60}
    state = {"ps": None, "status": _stand_status()}

    def handle(route):
        path = route.request.url.split("?")[0][len(STANDPC):]
        body = None
        if path == "/status":
            body = state["status"]
        elif path == "/shots":
            body = shots
        elif path == "/preisschiessen":
            body = state["ps"]
        elif path == "/ergebnis":
            body = erg
        elif path == "/ergebnis/qr.svg":
            route.fulfill(status=200, content_type="image/svg+xml", body=_qr_svg(ids, _stand_shots(ids, 30)))
            return
        elif path == "/events":
            return  # bleibt offen (keine Live-Daten noetig)
        if body is not None or path == "/preisschiessen":
            route.fulfill(status=200, content_type="application/json", body=json.dumps(body))
        else:
            route.continue_()

    p = ctx.new_page()
    p.route(STANDPC + "/**", handle)

    def live():
        p.evaluate("() => { const c = document.getElementById('connStatus'); c.textContent = '● live'; c.className = 'live'; }")

    goto(p, STANDPC + "/", 1500)
    live()
    shot(p, "standpc-anzeige")

    p.click("#zoomBtn")
    p.wait_for_timeout(400)
    shot(p, "standpc-zoom")
    p.keyboard.press("Escape")
    p.evaluate("() => closeMenu('zoomOverlay')")

    p.click("#ergBtn")
    p.wait_for_timeout(800)
    shot(p, "standpc-ergebnis")
    p.click("#ergQRBtn")
    p.wait_for_timeout(800)
    shot(p, "standpc-ergebnis-qr")
    p.unroute_all(behavior="ignoreErrors")
    p.close()

    # Preisschiessen: Scheibenwahl am Stand
    state["ps"] = PS_INFO
    state["status"] = _stand_status(shooter="")
    p = ctx.new_page()
    p.route(STANDPC + "/**", handle)
    goto(p, STANDPC + "/", 1500)
    live()
    shot(p, "standpc-preisschiessen")
    p.unroute_all(behavior="ignoreErrors")
    p.close()


def wertmaschine(ctx, ids):
    p = ctx.new_page()
    goto(p, WERTMASCHINE + "/", 1500)
    shot(p, "wertmaschine")
    p.close()


SCENES = {
    "server": server_pages,
    "preisschiessen": preisschiessen_pages,
    "vereinsabend": vereinsabend_pages,
    "ergebnisansicht": ergebnis_ansicht,
    "preisanzeige": preisanzeige,
    "standpc": stand_pc,
    "wertmaschine": wertmaschine,
}


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    wanted = sys.argv[1:] or list(SCENES)
    sql(f"INSERT INTO ui_role_sessions(token, role_key, browser_session) VALUES ('{TOKEN}','admin',true) "
        "ON CONFLICT (token) DO UPDATE SET last_seen_at=now()")
    global ANON
    ANON = Anonymizer(sql("SELECT string_agg(first_name, chr(10)) FROM shooters").splitlines(),
                      sql("SELECT string_agg(last_name, chr(10)) FROM shooters").splitlines())
    ids = {
        "saison": sql("SELECT id FROM preisschiessen WHERE kind='vereinsabend_saison' ORDER BY created_at DESC LIMIT 1"),
        # Preisschiessen mit den meisten Teilnehmern (fuer gefuellte Tabellen)
        "ps": sql("SELECT p.id FROM preisschiessen p ORDER BY (SELECT count(*) FROM ps_teilnehmer t "
                  "WHERE t.preisschiessen_id=p.id) DESC LIMIT 1"),
        # beendete Zehntel-Session mit 30 Wertungsschuessen (Beispiel Trefferbild)
        "session": sql("SELECT se.id FROM sessions se JOIN disciplines d ON d.id=se.discipline_id "
                       "WHERE d.decimal_scoring AND (SELECT count(*) FROM shots s WHERE s.session_id=se.id "
                       "AND s.kind='match')=30 ORDER BY se.started_at DESC LIMIT 1"),
    }
    ids["teilnehmer"] = sql("SELECT t.id FROM ps_teilnehmer t WHERE t.preisschiessen_id='%s' "
                            "ORDER BY t.teilnehmer_nr LIMIT 1" % ids["ps"])
    ids["raw_date"] = sql("SELECT se.started_at::date FROM sessions se WHERE EXISTS (SELECT 1 FROM shots s "
                          "WHERE s.session_id=se.id AND s.air_ns IS NOT NULL) ORDER BY 1 DESC LIMIT 1")
    try:
        with sync_playwright() as pw:
            browser = pw.chromium.launch()
            ctx = browser.new_context(viewport=VIEW, locale="de-DE", timezone_id="Europe/Berlin")
            ctx.add_cookies([{"name": "ss_role", "value": TOKEN, "url": SERVER}])
            install_anon(ctx)
            ctx.add_init_script("""document.addEventListener('DOMContentLoaded', () => {
              const st = document.createElement('style');
              st.textContent = '#ssrole-badge{display:none!important}';
              document.head.appendChild(st);
            });""")
            for name in wanted:
                print(name)
                SCENES[name](ctx, ids)
            ctx.unroute_all(behavior="ignoreErrors")
            browser.close()
    finally:
        sql(f"DELETE FROM ui_role_sessions WHERE token='{TOKEN}'")


if __name__ == "__main__":
    main()
