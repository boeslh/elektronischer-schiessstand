# Elektronischer Schießstand – Funktionsübersicht

Diese Dokumentation gibt einen Überblick über alle Funktionen der Anlage: die
Verwaltungsoberfläche des Servers, die Anzeige am Stand, die Zusatzprogramme
und die Apps. Ausführliche Schritt-für-Schritt-Anleitungen folgen später als
eigene Dokumente.

| Kapitel | Inhalt |
|---|---|
| [1. Systemüberblick](01-ueberblick.md) | Aufbau der Anlage, Startseite, Rollen |
| [2. Standbetrieb](02-standbetrieb.md) | Standsteuerung, Anzeige am Stand, Ergebnis mitnehmen, Standaktion |
| [3. Stammdaten und Disziplinen](03-stammdaten.md) | Gaue, Vereine, Mitglieder, Mannschaften, Sportklassen, Disziplinen |
| [4. Preisschießen](04-preisschiessen.md) | Anmeldung, Kasse, Teilnehmer, Stände, Auswertung, Gewinne, Verwaltung |
| [5. Vereinsabend](05-vereinsabend.md) | Schießsaison mit Tages- und Saisonwertung, Anwesenheit |
| [6. Ergebnisse, Wettkämpfe und Anzeigen](06-ergebnisse.md) | Ergebnisliste, Trefferbild, PDF, Wettkämpfe, Auswertung, Anzeigebildschirme |
| [7. Technik und Kalibrierung](07-technik.md) | Simulator, Gerätekonfiguration, Wertmaschine |
| [8. Systemverwaltung](08-verwaltung.md) | Einstellungen, Benutzerverwaltung, Import/Export, Archiv, Entwicklung |
| [9. Apps](09-apps.md) | Standanzeige-App (Android), App „My Shootings“ |

## Hinweise

- **Personendaten:** Alle Namen, Geburtsdaten, Passnummern und
  Kontaktdaten in den Screenshots sind erfunden. Die Bilder wurden aus dem
  Entwicklungssystem erzeugt; personenbezogene Daten werden dabei
  automatisch durch Pseudonyme ersetzt.
- **Stand der Bilder:** Oktober 2026 (Server-Version mit Migration 077).
  Kleine Abweichungen zur aktuellen Oberfläche sind möglich.

## PDF erzeugen

```bash
docs/build-pdf.sh            # eine Gesamt-PDF:  docs/pdf/Schiessstand-Funktionsuebersicht.pdf
docs/build-pdf.sh --einzeln  # zusätzlich je Kapitel eine PDF
```

Beim ersten Aufruf richtet das Skript eine eigene Python-Umgebung mit einem
Browser zum Drucken ein (`docs/.venv`, ca. 300 MB, Internet nötig). Die
erzeugten PDFs liegen in `docs/pdf/` und werden nicht im Git-Repo
gespeichert.

## Screenshots erneuern

```bash
docs/tools/make-images.sh
```

Erzeugt alle Bilder in `docs/bilder/` neu. Voraussetzung: Server, Stand-PC
Stand 4 und Preisanzeige laufen auf diesem Rechner, Flutter ist installiert
(für die App-Bilder). Einzelne Bereiche lassen sich mit
`docs/.venv/bin/python docs/tools/screenshots.py standpc` usw. erneuern.
