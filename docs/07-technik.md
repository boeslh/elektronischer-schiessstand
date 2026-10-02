# 7. Technik und Kalibrierung

Diese Funktionen sind für die technische Betreuung der Anlage gedacht und
normalerweise nur in den Rollen Admin und Entwickler sichtbar.

## Simulator

Der Simulator berechnet gespeicherte Ergebnisse mit veränderten
Kalibrierparametern neu. Jeder Schuss wird mit seinen Rohdaten (Laufzeiten
an den Sensoren) gespeichert, deshalb lässt sich jede Einstellung im
Nachhinein an echten Schüssen ausprobieren, ohne erneut zu schießen.

![Simulator](bilder/simulator.png)

Ablauf in vier Schritten:

1. **Ergebnis auswählen** – nach Datum und Schütze suchen; Ergebnisse des
   Wartungs-Schützen lassen sich gezielt filtern.
2. **Kalibrierparameter** – Abstand Papier–Sensoren (Standoff),
   Mikrofonabstand, Offsets, Schallgeschwindigkeit, Kugeldurchmesser-
   Korrektur, Verfahren zur Positionsbestimmung, Piezo-Bestätigung,
   Mikrofon-Offsets und genutzte Mikrofone. Kalibrierungen lassen sich unter
   einem Namen speichern und wieder laden (gefiltert nach Stand).
3. **Kalibrierung** – Mikrofon-Offsets aus ausgewählten Schüssen berechnen;
   optional **Foto-Kalibrierung** über ein Foto der beschossenen
   Papierscheibe.
4. **Ergebnis** – Original und Neuberechnung im direkten Vergleich, inklusive
   Qualitätsmaß (Streuung der Schnittpunkte).

Passt eine Einstellung, wird sie mit **Zur Übernahme freigeben** an einen
Stand geschickt. Der Stand-PC übernimmt sie auf die Messplatine, sobald dort
jemand die Übernahme bestätigt.

## Gerätekonfiguration

Die Stand-PCs sichern die komplette Konfiguration der Messplatinen regelmäßig
und nach jeder Änderung. Die Übersicht zeigt je Stand die aktuelle Platine,
die letzte Änderung und Kalibrierung sowie Platinentausche. Ein Klick auf
einen Stand öffnet den Verlauf; zwei Versionen lassen sich vergleichen (auf
Wunsch nur die Unterschiede).

![Gerätekonfiguration](bilder/geraetekonfiguration.png)

## Admin-Oberfläche am Stand-PC

Jeder Stand-PC hat unter `/admin` eine eigene, passwortgeschützte
Oberfläche für Kalibrierung und Konfiguration direkt am Stand (mit
Live-Anzeige der Messwerte). Das Passwort wird zentral in den
[Einstellungen](08-verwaltung.md#einstellungen) gesetzt und an alle Stände
verteilt.

## Wertmaschine

Die Wertmaschine bindet eine Disag-Auswertemaschine (RM III) für
Papierscheiben an. Sie läuft auf dem Rechner, an dem die Maschine
angeschlossen ist, und überträgt die Ergebnisse an den Server, z. B. als
Preisschießen-Scheibe mit Seriennummer. Für die Fehlersuche zeigt sie die
serielle Kommunikation an.

![Wertmaschine](bilder/wertmaschine.png)
