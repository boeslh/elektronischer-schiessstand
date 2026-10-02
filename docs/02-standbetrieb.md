# 2. Standbetrieb

## Standsteuerung

Die Standsteuerung ist die Übersicht für die Aufsicht: Jeder Stand ist eine
Karte mit Schütze, Disziplin bzw. Scheibe, aktuellem Ergebnis und Status.
Der Punkt oben rechts zeigt, ob der Stand-PC erreichbar ist.

![Standsteuerung](bilder/standsteuerung.png)

Funktionen:

- **Stand belegen:** Schütze (aus den Mitgliedern) und Disziplin wählen; der
  Stand-PC übernimmt die Belegung automatisch.
- **Sitzung steuern:** Probe/Wertung, Pause, Beenden bzw. Freigeben.
- **Live-Übersicht:** Ergebnisse und Schusszahlen aktualisieren sich laufend.
- **Preisschießen-Stände** werden gekennzeichnet (z. B. „wartet auf
  Teilnehmerwahl am Stand“ bei Selbstbedienung).
- **Wettkampf-Schnellbelegung:** Starter eines Wettkampfs direkt auf die
  Stände verteilen.
- **Wartungs-Schütze** (nur Admin): Stand ohne echten Schützen belegen, z. B.
  für Probeschüsse zur Kalibrierung. Diese Ergebnisse erscheinen nicht in der
  normalen Ergebnisliste.

## Anzeige am Stand

Am Stand sieht der Schütze seine Scheibe mit den Treffern der laufenden
Serie, den letzten Schuss groß, das Gesamtergebnis, die Serien und eine
Liste aller Schüsse. Die Anzeige läuft im Browser des Stand-PC oder als
[Android-App](09-apps.md#standanzeige-app).

![Anzeige am Stand](bilder/standpc-anzeige.png)

*Kopfzeile: Name, Disziplin, Modus (Probe/Wertung), Knöpfe für die Menüs;
rechts der Verbindungsstatus und die Standnummer.*

| Knopf | Funktion |
|---|---|
| **→ Wertung / ← Probe** | Wechsel zwischen Probe- und Wertungsschüssen (zurück zu Probe nur vor dem ersten Wertungsschuss) |
| 🏆 | Preisschießen-Menü (nur im Preisschießen) |
| 🔓 | Stand freigeben (beendet die Sitzung, Schüsse bleiben gespeichert) |
| 📤 | Ergebnis mitnehmen: E-Mail, Ausdruck, QR-Code |
| 🎯 | Disziplin wählen (nur ohne zugewiesenen Schützen) |
| 🔍 | Zoom |

Treffer werden farbig dargestellt (Zehner rot, Neuner gelb, sonst blau; die
vorherigen Schüsse der Serie grau). Bei verdeckten Disziplinen zeigt die
Anzeige keine Werte bzw. kein Trefferbild.

### Zoom

Im Modus **Auto** wählt die Anzeige selbst die kleinste Vergrößerung, in der
alle Schüsse der laufenden Serie zu sehen sind. Alternativ gibt es fünf feste
Stufen.

![Zoom-Menü](bilder/standpc-zoom.png)

### Ergebnis mitnehmen

Über 📤 kann sich der Schütze das Ergebnis seiner letzten Scheibe auf diesem
Stand mitnehmen, auch nachdem der Stand schon freigegeben wurde (Ergebnisse
der letzten 12 Stunden).

![Ergebnis mitnehmen](bilder/standpc-ergebnis.png)

- **Per E-Mail senden:** schickt das Einzelergebnis als PDF an die in den
  Stammdaten hinterlegte Adresse. Die Adresse wird am Stand nur gekürzt
  angezeigt. Voraussetzung: Mailserver in den
  [Einstellungen](08-verwaltung.md#einstellungen) eingerichtet.
- **Ausdrucken:** druckt das PDF auf dem in den Einstellungen gewählten
  Drucker.
- **QR-Code für die App:** zeigt einen QR-Code mit bis zu 60 Schüssen
  inklusive Trefferlage. Er wird mit der App „My Shootings“ gescannt (siehe
  [Apps](09-apps.md#app-my-shootings)).

Zum Schutz vor Missbrauch sind E-Mail und Druck auf drei Vorgänge je Ergebnis
in zehn Minuten begrenzt.

![QR-Code für die App](bilder/standpc-ergebnis-qr.png)

### Preisschießen am Stand

Läuft am Stand ein Preisschießen, öffnet sich das Menü automatisch, sobald
eine Auswahl ansteht. Der Teilnehmer wählt seine nächste Scheibe, kann
weitere Scheiben oder Sets buchen und den Stand freigeben. Ist ein Stand für
**Selbstbedienung** freigegeben, sucht sich der Teilnehmer zuerst selbst
über Nummer oder Namen aus.

![Preisschießen-Menü am Stand](bilder/standpc-preisschiessen.png)

*Die Zahlen hinter den Scheiben bedeuten gekauft/beendet.*

## Standaktion

Hilfsfunktionen für Störungen im laufenden Betrieb:

- **Schütze transferieren:** Eine laufende Sitzung auf einen anderen, freien
  Stand verschieben, z. B. bei einem Geräteausfall. Erst den Quell-Stand,
  dann den Ziel-Stand anklicken.
- **Schüsse aus lokalem Log importieren:** Jeder Stand-PC protokolliert alle
  Schüsse zusätzlich lokal. Fehlen Schüsse in der Datenbank (z. B. nach einer
  Netzwerkstörung), lassen sie sich von dort abrufen und einer Sitzung
  zuordnen.
- **Disziplinen auf Stand-PC übertragen:** Disziplinliste für den Betrieb
  ohne Serververbindung auf einen Stand-PC schreiben.

![Standaktion](bilder/standaktion.png)
