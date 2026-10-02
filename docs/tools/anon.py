"""Pseudonymisierung fuer Doku-Screenshots.

Die Datenbank enthaelt echte Mitgliederdaten. Damit diese nicht in den
Screenshots (und damit im Git-Repo) landen, werden alle Antworten an den
Browser umgeschrieben, bevor sie angezeigt werden:

- jeder Vor- und Nachname aus der Tabelle shooters wird (in allen JSON-
  Zeichenketten) durch einen erfundenen, aber immer gleichen Namen ersetzt,
- Geburtsdatum, Passnummer, E-Mail, Telefon und Anschrift werden durch
  erfundene Werte ersetzt.

Die Datenbank selbst wird nicht veraendert.
"""

import hashlib
import json
import re

FAKE_LAST = [
    "Albrecht", "Bauer", "Berger", "Brandl", "Danner", "Eder", "Egger", "Feldmann", "Fink", "Forster",
    "Gruber", "Haas", "Hahn", "Hartl", "Heller", "Huber", "Jäger", "Kainz", "Keller", "Koch",
    "Kraus", "Lang", "Lechner", "Lindner", "Maier", "Mayr", "Moser", "Neumann", "Obermaier", "Pichler",
    "Pfeiffer", "Rauch", "Reiter", "Riedl", "Roth", "Schäfer", "Schmid", "Schneider", "Schuster", "Seidl",
    "Sommer", "Stadler", "Steiner", "Strobl", "Thaler", "Vogel", "Wagner", "Walter", "Weber", "Wimmer",
    "Winkler", "Wolf", "Zeller", "Ziegler", "Zimmermann", "Auer", "Bichler", "Dorfner", "Ertl", "Fuchs",
]
FAKE_FIRST = [
    "Andreas", "Anna", "Bernd", "Birgit", "Christian", "Claudia", "Daniel", "Doris", "Elias", "Emma",
    "Florian", "Franziska", "Georg", "Gabriele", "Hans", "Helga", "Jakob", "Julia", "Karl", "Katharina",
    "Lukas", "Lena", "Markus", "Maria", "Michael", "Monika", "Niklas", "Nina", "Peter", "Petra",
    "Quirin", "Rosa", "Sebastian", "Sabine", "Stefan", "Sophie", "Thomas", "Theresa", "Tobias", "Ursula",
    "Valentin", "Vera", "Werner", "Waltraud", "Xaver", "Yvonne", "Zacharias", "Zita", "Benedikt", "Lisa",
]

_WORD = "A-Za-zÄÖÜäöüßÉéèÁáÀàÓóÍíÚúŠšŽžČčĆćÇç"


def _h(s: str) -> int:
    return int(hashlib.sha1(s.encode()).hexdigest(), 16)


class Anonymizer:
    def __init__(self, first_names, last_names):
        # Ein Namensbestandteil kann Vor- UND Nachname sein (z.B. "Lorenz") -
        # er wird der Rolle zugeordnet, in der er haeufiger vorkommt.
        cnt_f, cnt_l = {}, {}
        for n in first_names:
            for tok in self._tokens(n):
                cnt_f[tok] = cnt_f.get(tok, 0) + 1
        for n in last_names:
            for tok in self._tokens(n):
                cnt_l[tok] = cnt_l.get(tok, 0) + 1
        mapping = {}
        for tok in set(cnt_f) | set(cnt_l):
            if cnt_f.get(tok, 0) > cnt_l.get(tok, 0):
                mapping[tok] = FAKE_FIRST[_h("F" + tok) % len(FAKE_FIRST)]
            else:
                mapping[tok] = FAKE_LAST[_h("L" + tok) % len(FAKE_LAST)]
        self.map = mapping
        if mapping:
            alt = "|".join(re.escape(t) for t in sorted(mapping, key=len, reverse=True))
            self.rx = re.compile(f"(?<![{_WORD}])({alt})(?![{_WORD}])")
        else:
            self.rx = None

    @staticmethod
    def _tokens(name):
        # Namensbestandteile (auch Doppelnamen einzeln), nur ab 3 Zeichen und
        # mit Grossbuchstaben am Anfang - "von", "de" usw. bleiben stehen
        for t in re.split(r"[\s\-]+", name or ""):
            t = t.strip(".,")
            if len(t) >= 3 and t[0].isupper():
                yield t

    def text(self, s: str) -> str:
        if not self.rx or not s:
            return s
        return self.rx.sub(lambda m: self.map[m.group(1)], s)

    # Felder mit personenbezogenen Daten, die nicht nur aus Namen bestehen
    def _field(self, key, v):
        k = key.lower()
        if not isinstance(v, str) or not v:
            return None
        h = _h(v)
        if k in ("birth_date", "geburtsdatum", "birthdate"):
            return f"{1950 + h % 55:04d}-{1 + h % 12:02d}-{1 + h % 28:02d}" + v[10:]
        if k in ("pass_no", "passnr", "pass_number"):
            return f"4180{h % 10000:04d}"
        if k == "email":
            return f"mitglied{h % 1000}@example.org"
        if k == "email_masked":
            return "mi…@example.org"
        if k in ("phone", "mobile", "telefon"):
            return "0000 000000"
        if k in ("street", "strasse"):
            return f"Musterstraße {1 + h % 50}"
        if k in ("zip", "plz"):
            return "85000"
        if k in ("city", "ort"):
            return "Musterstadt"
        if k == "notes":
            return ""
        # Zugangsdaten des Mailservers (Einstellungen)
        if k == "smtp_host":
            return "smtp.example.org"
        if k in ("smtp_user", "smtp_from"):
            return "ergebnisse@example.org"
        if k == "smtp_from_name":
            return "Musterverein e.V."
        return None

    def walk(self, obj, key=""):
        if isinstance(obj, dict):
            return {k: self.walk(v, k) for k, v in obj.items()}
        if isinstance(obj, list):
            return [self.walk(v, key) for v in obj]
        if isinstance(obj, str):
            f = self._field(key, obj)
            return f if f is not None else self.text(obj)
        return obj

    def json_text(self, body: str) -> str:
        try:
            return json.dumps(self.walk(json.loads(body)), ensure_ascii=False)
        except ValueError:
            return self.text(body)

    def html_text(self, body: str) -> str:
        # nur sichtbarer Text zwischen Tags
        return re.sub(r">([^<]+)<", lambda m: ">" + self.text(m.group(1)) + "<", body)
