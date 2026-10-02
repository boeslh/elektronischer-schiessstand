# 1. Systemüberblick

## Aufbau der Anlage

Die Anlage erfasst Treffer auf Luftgewehr- und Luftpistolenscheiben (10 m)
elektronisch und verwaltet alles rund um den Schießbetrieb: Mitglieder,
Disziplinen, Preisschießen, Vereinsabende, Wettkämpfe und Ergebnisse.

```
 Je Stand                         Zentral
┌──────────────────────────┐     ┌───────────────────────────────────┐
│ Kugelfang mit Sensoren   │     │ Server (Verwaltung, :8090)        │
│   │                      │     │  – Weboberfläche für Büro/Kasse   │
│   ▼                      │     │  – Datenbank (PostgreSQL)         │
│ ESP32-Messplatine        │     │  – PDF, E-Mail, Druck             │
│   │                      │     ├───────────────────────────────────┤
│   ▼                      │◄───►│ Preisanzeige (:8091)              │
│ Stand-PC                 │     │  – Ergebnislisten für Bildschirme │
│  – Trefferberechnung     │     ├───────────────────────────────────┤
│  – Anzeige für Schützen  │     │ Wertmaschine (Disag RM III)       │
│    (Browser oder App)    │     │  – Papierscheiben auswerten       │
└──────────────────────────┘     └───────────────────────────────────┘
```

| Baustein | Aufgabe |
|---|---|
| **Messplatine (ESP32)** | misst die Laufzeiten des Schalls an mehreren Sensoren |
| **Stand-PC** | berechnet daraus Trefferlage, Ring und Zehntel; zeigt dem Schützen Scheibe und Ergebnisse |
| **Server** | zentrale Verwaltung im Browser: Stände belegen, Stammdaten, Preisschießen, Vereinsabend, Ergebnisse, Einstellungen |
| **Preisanzeige** | eigener Dienst für Ergebnislisten auf Bildschirmen im Vorraum |
| **Wertmaschine** | bindet eine Disag-Auswertemaschine für Papierscheiben an |
| **Apps** | Standanzeige als Android-App, Ergebnisübernahme in die App „My Shootings“ |

## Startseite

Nach dem Aufruf des Servers im Browser erscheint die Startseite mit allen
Modulen als Kacheln. Welche Kacheln sichtbar und nutzbar sind, hängt von der
gewählten **Rolle** ab (siehe unten).

![Startseite mit allen Modulen](bilder/start.png)

*Startseite in der Rolle „Admin“. Im unteren Bereich „System“ liegen die
Verwaltungsfunktionen.*

## Rollen und Rechte

Die Anlage arbeitet mit **Rollen** statt persönlichen Benutzerkonten. Unten
rechts auf jeder Seite steht die aktuelle Rolle; ein Klick darauf wechselt
sie. Eine Rolle kann durch ein gemeinsames Passwort geschützt sein. Das
Passwort gilt für die Browsersitzung und muss nach dem Schließen des Browsers
erneut eingegeben werden.

Fest eingebaut sind:

| Rolle | gedacht für |
|---|---|
| **Admin** | Vereinsverantwortliche: alle Kacheln, alle Rechte |
| **Entwickler** | Technik: Kalibrierung, Simulator, Diagnose |
| **Anwender** | Schießleitung/Aufsicht im täglichen Betrieb |
| **Revisor** | Prüfung, inkl. Ergebniskorrekturen |

Weitere Rollen lassen sich in der [Benutzerverwaltung](08-verwaltung.md#benutzerverwaltung)
anlegen. Dort wird je Rolle festgelegt, welche Kacheln sie sieht und ob sie
Ergebnisse korrigieren oder Preisschießen bearbeiten darf.
