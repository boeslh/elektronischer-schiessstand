# 11. Online-Anzeige im Internet

Die Online-Anzeige ist ein eigener Dienst auf einem Server im Internet.
Mehrere Vereine können dort ihre **Preisschießen** (mit Historie) und
**Rundenwettkämpfe** (live, als Liste oder mit Zielscheiben) zeigen. Die
Daten schickt der Schießstand-Server des Vereins selbst; der Online-Server
verbindet sich nie mit einem Schießstand.

## Ablauf in Kürze

1. Der Betreiber des Online-Servers legt den **Verein** und einen
   **Benutzer** für den Verein an.
2. Der Verein meldet sich in der Verwaltung des Online-Servers an
   (`https://<online-server>/admin/`) und erzeugt einen **API-Schlüssel**.
3. Auf dem Schießstand-Server unter *Einstellungen → Online-Veröffentlichung*
   Adresse und Schlüssel eintragen, **Verbindung testen**, Übertragung
   einschalten.
4. Dort die Preisschießen und Rundenwettkämpfe hinzufügen, die online
   erscheinen sollen.

Die öffentlichen Seiten eines Vereins liegen unter
`https://<online-server>/v/<kürzel>/`. Läuft die Online-Anzeige in einem
Unterverzeichnis einer vorhandenen Webseite (z. B.
`https://gs.schuetzengau-massenhausen.de/befreeshoot`), gehört das
Unterverzeichnis zu allen Adressen – auch zu der, die auf dem
Schießstand-Server eingetragen wird.

## Was wird übertragen?

Nur die Daten, die die Anzeigen brauchen – als fertige Momentaufnahme, nicht
als Kopie der Datenbank:

| Anzeige | Übertragen | Nicht übertragen |
|---|---|---|
| **Preisschießen** | Name, Datum, Kennzahlen (Anzahl Vereine, Teilnehmer, Scheiben, Schuss); je sichtbarer Wertungsliste Platz, Startnummer, Name, Verein, Werte, Summe, Preis; Vereins-Auswertungen | Teilnehmer ohne Ergebnis in einer sichtbaren Liste, Sportklasse, Guthaben, Käufe, einzelne Scheiben und Schüsse |
| **Rundenwettkampf** | Name, Liga, Ort, Datum, Disziplin, Mannschaften; je Starter Name, Rolle, Paarung, Ergebnis, Serien, Hochrechnung und die Treffer der laufenden Serie (bzw. des Stechschusses) | alle anderen Mitglieder, Starter anderer Wettkämpfe |

Geburtsdatum, Passnummer, Anschrift, E-Mail und Telefon werden nie
übertragen. Was genau gesendet würde, zeigt auf dem Schießstand-Server der
Knopf **Daten** in der Liste der Veröffentlichungen.

## Aktualität

| Anzeige | Takt der Übertragung | Verzögerung online |
|---|---|---|
| Rundenwettkampf | 3 oder 5 Sekunden (nur bei Änderungen) | wenige Sekunden (gemessen 2–3 s bei 3 s Takt) |
| Preisschießen | 1, 2 oder 5 Minuten | bis zu einem Takt nach der letzten Neuberechnung der Ergebnislisten |

Die Online-Seiten eines Rundenwettkampfs aktualisieren sich alle 3 Sekunden
selbst. Unveränderte Daten werden nicht erneut gesendet.

## Einstellungen auf dem Schießstand-Server

*Einstellungen → Online-Veröffentlichung (Internet)* (nur Admin):

- **Adresse des Online-Servers** (muss mit `https://` beginnen)
- **API-Schlüssel des Vereins** (`ssk_…`; wird gespeichert und danach nur
  noch gekürzt angezeigt)
- **Eigenes CA-Zertifikat** – nur nötig, wenn der Online-Server ein selbst
  signiertes Zertifikat verwendet
- **Übertragung eingeschaltet**
- **Veröffentlichungen:** Preisschießen bzw. Rundenwettkampf auswählen, Takt
  wählen, **Hinzufügen**. Je Eintrag: Takt ändern, **Anhalten/Fortsetzen**,
  **Jetzt** (sofort übertragen), **Daten** (Inhalt ansehen), **Entfernen**.
  Status und Zeitpunkt der letzten Übertragung stehen in der Liste;
  Fehlermeldungen des Online-Servers werden angezeigt.

**Entfernen** beendet nur die Übertragung. Die Daten bleiben auf dem
Online-Server stehen.

## Online-Seiten

- **Startseite:** alle Vereine mit Veröffentlichungen.
- **Vereinsseite:** Rundenwettkämpfe und Preisschießen des Vereins, neueste
  zuerst, mit Status (läuft, beendet). Laufen Rundenwettkämpfe, führt
  **▶ Live-Anzeige** zu einer Seite, die alle laufenden Rundenwettkämpfe im
  Wechsel alle 10 Sekunden zeigt (z. B. für einen Bildschirm im
  Nachbarverein).
- **Preisschießen:** Startseite mit Kennzahlen und Teilnehmersuche,
  Wertungslisten, Vereins-Auswertungen und je Teilnehmer seine Platzierungen.
- **Rundenwettkampf:** Scheibenansicht (je Mannschaft bzw. je Paarung, mit
  grünem/grauem Punkt für die Führung im Paarungswettkampf) oder Liste;
  Stand, Hochrechnung und Punkte in der Mitte.

Die öffentlichen Seiten dürfen in andere Webseiten (z. B. die Homepage des
Vereins) eingebettet werden.

## Historie und Löschen

Ein Preisschießen bleibt nach seinem Ende online, auch wenn ein
Schießstand-Server es nicht mehr kennt oder ein anderer Schießstand-Server
des Vereins sich verbindet. So entsteht eine Historie. Gelöscht wird nur in
der Verwaltung des Online-Servers: **Veröffentlichungen → Löschen** entfernt
die Anzeige mit allen Daten endgültig. Überträgt ein Schießstand-Server sie
danach weiter, wird sie neu angelegt – vorher dort die Veröffentlichung
entfernen.

## Verwaltung des Online-Servers

| Rolle | Darf |
|---|---|
| **Plattform-Administrator** | Vereine anlegen und löschen, alle Benutzer, alle API-Schlüssel, alle Veröffentlichungen löschen |
| **Vereinsbenutzer** | Benutzer, API-Schlüssel und Veröffentlichungen des eigenen Vereins |

- Jeder Benutzer kann sein Passwort ändern (mindestens 10 Zeichen).
- **API-Schlüssel** werden nur beim Erzeugen einmal vollständig angezeigt.
  Ein gesperrter Schlüssel kann nichts mehr übertragen; bereits
  veröffentlichte Daten bleiben. Für jeden Schlüssel sieht man, wann und von
  welcher Adresse er zuletzt benutzt wurde.
- Einrichtung des Servers: `online/README.md`.

## Sicherheit

- Die Verbindung geht immer vom Schießstand-Server zum Online-Server, nur
  per **HTTPS** mit Prüfung des Server-Zertifikats.
- Jeder Verein schreibt mit seinem eigenen **API-Schlüssel** nur seine
  eigenen Veröffentlichungen. Auf dem Online-Server ist nur eine Prüfsumme
  des Schlüssels gespeichert.
- Passwörter der Verwaltung sind mit bcrypt gespeichert. Nach 5
  Fehlversuchen ist die Anmeldung für diesen Benutzer von dieser Adresse
  5 Minuten gesperrt. Anmeldungen enden nach 2 Stunden ohne Nutzung.
- Formulare der Verwaltung sind gegen fremd ausgelöste Anfragen (CSRF)
  geschützt.
