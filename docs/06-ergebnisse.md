# 6. Ergebnisse, Wettkämpfe und Anzeigen

## Ergebnisse

Alle geschossenen Ergebnisse mit Schütze, Datum, Veranstaltung, Disziplin,
Schusszahl, Ergebnis (mit Anzahl Zehner) und Status. Filter nach Datum,
Name und Veranstaltung; archivierte Veranstaltungen lassen sich einblenden.
Je Ergebnis gibt es die **grafische Ansicht** und das **PDF**.

![Ergebnisse](bilder/ergebnisse.png)

### Grafische Ansicht

Alle Treffer einer Scheibe auf der Zielscheibe, daneben jede Serie mit
Zehntelwert und Teiler je Schuss. Zoomstufen wie am Stand. Mit dem Recht
„Ergebnisse korrigieren“ lassen sich einzelne Schüsse nachträglich
korrigieren (✎); jede Korrektur wird protokolliert. Rollen mit der Kachel
„Simulator“ sehen oben rechts zusätzlich **Bandkontrolle**: Dort werden die
Löcher im Papierband mit dem Ergebnis verglichen (siehe
[Bandkontrolle](07-technik.md#bandkontrolle)).

![Grafische Ansicht eines Ergebnisses](bilder/ergebnis-ansicht.png)

### Einzelergebnis als PDF

Das PDF fasst ein Ergebnis auf einer Seite zusammen: Kopf mit Name, Verein,
Sportklasse, Disziplin, Datum und Stand; Gesamtergebnis mit Innenzehnern
und den fünf besten Teilern; Trefferbild gesamt und je Serie mit
Ringbeschriftung. Unter jedem Schuss steht in Klammern der Wert der jeweils
anderen Wertungsart: bei Zehntelwertung der ganze Ring, bei Ringwertung der
Zehntelwert. Dasselbe PDF wird auch per E-Mail verschickt und am Stand
gedruckt.

![Einzelergebnis-PDF (Beispiel mit erfundenen Daten)](bilder/pdf-einzelergebnis.png)

## Wettkämpfe

Planung von Einzel-, Runden- und Gruppenwettkämpfen: Grunddaten,
teilnehmende Mannschaften und Einheiten, Starter. Ergebnisse können auch
manuell erfasst werden. Beendete Wettkämpfe
werden archiviert (siehe [Archiv](08-verwaltung.md#archiv)).

![Wettkämpfe](bilder/wettkaempfe.png)

In den Grunddaten eines Wettkampfs werden neben Name, Typ, Datum, Disziplin
und Ort auch die Wertung und der Umgang mit der Schießzeit festgelegt:

![Grunddaten eines Rundenwettkampfs](bilder/wettkampf-bearbeiten.png)

### Rundenwettkampf

Ein Rundenwettkampf hat eine Heim- und eine Gastmannschaft. Nach dem
Festlegen der Mannschaften übernimmt **„Starter beider Mannschaften
übernehmen“** die Mitglieder beider Mannschaften als Starter. Je Starter
gibt es die Rolle **S** (Stammschütze), **E** (Ersatz, wird gewertet) oder
**AK** (außer Konkurrenz). Es gibt zwei Wertungsarten:

- **Mannschaftswettkampf:** Es entscheidet das Mannschaftsergebnis. Unter
  **Mannschaftswertung** wird festgelegt, wie viele Schützen je Mannschaft
  zählen (die besten 2 bis 10 oder alle). Diese Vorgabe gilt für die
  Auswertung, das PDF und die Anzeigen (Ergebnis und Hochrechnung).
- **Paarungswettkampf:** Je **Paarung** schießt ein Heim- gegen einen
  Gastschützen; das bessere Ergebnis (Zehntel bzw. Ringe) bringt der
  Mannschaft einen Punkt. Es gewinnt die Mannschaft mit den meisten Punkten.
  Bei vier Paarungen und Punktgleichstand zählt das Mannschaftsergebnis (Summe
  der gepaarten Schützen) als weiterer Punkt. Die Paarungen (1–5) werden je
  Starter zugeordnet, **„Automatisch zuordnen“** verteilt die Stammschützen
  der Reihe nach. Eine Übersicht zeigt, wer gegen wen schießt.

### Stechschüsse

Steht eine Paarung gleich, entscheiden einzelne Stechschüsse – je Paarung
ein eigener Durchgang an den beiden Ständen der Schützen. Gestartet wird
über die [Standsteuerung](02-standbetrieb.md#gemeinsame-standaktionen)
(beide Stände markieren → **⚔ Stechschuss** oder über den Kasten „Stechen
erforderlich“).

- Die ersten drei Stechschüsse zählen in der Wertung der Disziplin (Ringe
  oder Zehntel), ab dem vierten wird immer in Zehntel verglichen.
- Die Paarung ist entschieden, sobald in einem Durchgang ein Schütze besser
  ist; bei Gleichstand folgt der nächste Durchgang.
- Jeder Stechschuss beginnt am Stand mit leerer Scheibe; das Ergebnis der
  Wertung bleibt unverändert. Stechschüsse erscheinen nicht als eigenes
  Ergebnis in der Ergebnisliste.

### Schießzeit und Umschalten Probe → Wertung

Die Zeiten selbst stehen in der [Disziplin](03-stammdaten.md#disziplinen)
(Gesamtzeit oder getrennte Probe- und Wertungszeit). Im Wettkampf wird
festgelegt, wie sie wirken:

| Schießzeit | Wirkung |
|---|---|
| **Uhrzeit** | nur die Uhrzeit wird angezeigt (weiße Schrift) |
| **Soft** | Zeit zählt herunter, hat aber keine Wirkung (grüne Schrift); nach Ablauf wird sie negativ |
| **Hard** | Zeit zählt herunter; nach Ablauf wird kein Schuss mehr gewertet (rote Schrift) |

| Umschalten Probe → Wertung | Ablauf |
|---|---|
| **Individuell** | Jeder Schütze schaltet selbst um. Gilt immer, wenn die Disziplin nur eine Gesamtzeit hat. Die Wertungszeit beginnt mit dem Ende der Probezeit oder früher mit dem Umschalten. |
| **Zentral** | Zeitstart (**⏱ Zeit starten**) und Umschalten (**Wertung**) nur über die Standsteuerung; am Stand ist der Umschalter gesperrt. Vor dem Start und nach Ablauf der Probezeit bis zur Wertungsfreigabe wird kein Schuss gewertet. Die Wertungszeit beginnt mit der Freigabe. |
| **Automatisch** | Nach Ablauf der Probezeit schaltet der Stand selbst auf Wertung, falls der Schütze das nicht schon vorher getan hat. |

Die Zeit läuft ab **⏱ Zeit starten** in der Standsteuerung (einzeln oder für
alle markierten Stände), sonst ab dem Beginn der Probe. Die Vorbelegung für
neue Wettkämpfe steht in den [Einstellungen](08-verwaltung.md#einstellungen).

## Auswertung

Auswertungen von Wettkämpfen. Ergebnisse lassen sich als PDF ausgeben und
als „gespeicherte Auswertung“ ablegen:

| Reiter | Auswertung |
|---|---|
| **Rundenwettkampf** | Mannschaftswertung mit der im Wettkampf festgelegten Anzahl der besten Schützen (vorbelegt, für gespeicherte Auswertungen änderbar); beim Paarungswettkampf zusätzlich die Paarungswertung mit Punkten, Mannschaftspunkt, Stechschüssen und Endstand |
| **Gruppenwettkampf** | Gruppenwertung nach Ringen, Zehnteln oder Teiler, mit wählbarer Besten-Anzahl je Gruppe |
| **Meisterschaft** | Kreis-, Bezirks- und Landesmeisterschaften – *in Entwicklung* |
| **Mannschaften Ringe** | Direktvergleich der Mannschaftsringe über mehrere Veranstaltungen – *in Entwicklung* |
| **Variable Auswertung** | frei zusammengestellte Auswertung über alle Scheiben, siehe unten |

![Auswertung](bilder/auswertung.png)

### Variable Auswertung

Die Auswertung entsteht in drei Schritten; jede Änderung rechnet sofort neu.
Unter einem Namen gespeichert wird die Einstellung, nicht das Ergebnis: Eine
geladene Auswertung zeigt immer den aktuellen Stand.

**1 Auswahl** – welche Scheiben zählen: Zeitraum, Wettkampf, Preisschießen,
Scheibe, Disziplin, Verein und Schützenklasse. Je Kriterium lassen sich mehrere
Einträge wählen („oder“), verschiedene Kriterien gelten zusammen („und“).
Wettkampf, Preisschießen und Scheibe beschreiben die Herkunft und ergänzen
sich: Wettkampf A und Preisschießen B ergibt die Scheiben aus beiden. Die
Schützenklasse stammt aus dem Starter bzw. dem Preisschießen-Teilnehmer;
Scheiben ohne beides (freies Training) fallen bei einer Klassenauswahl heraus.

**2 Wertung**

| Einstellung | Bedeutung |
|---|---|
| **Wertung** | Ringe, Zehntel, Teiler, Adler (abwechselnd aus Ring- und Teilerliste) oder Spaßwertungen: Vor- und Nachkomma eines Schusses addiert (9,7 → 16) bzw. nur die Nachkommastelle (9,7 → 7) |
| **Je Scheibe zählt** | Summe aller Schüsse oder bester Schuss |
| **Zielergebnis** | Es gewinnt, wer am nächsten daran liegt (z. B. Teiler, Ziel 222) |
| **Mehrere Ergebnisse je Teilnehmer** | *Summe der besten*: die gewählte Anzahl bester Ergebnisse wird addiert; wer weniger hat, steht hinter denen mit voller Anzahl. *Mittelwert*: Durchschnitt aller Ergebnisse, die Anzahl der Ergebnisse spielt für den Platz keine Rolle |
| **Faktoren** | Multiplikator für Ringe und Teiler je Disziplin oder Scheibe; die Scheibe hat Vorrang |
| **Gruppenwertung** | Mannschaften (aus den Startern der Wettkämpfe), Vereine, Gaue, Sportklassen oder Altersklassen. Die maximale Anzahl Mitglieder in der Wertung ist wählbar, es zählen die besten Ergebnisse der Gruppe. Altersklassen fassen die männliche und die weibliche Klasse gleichen Alters zusammen (Klassennummer der weiblichen Klasse = männliche + 1, z. B. Schüler männlich 20 und Schüler weiblich 21) |

**3 Ergebnis** – Einzelwertung oder, mit Gruppenwertung, die Gruppen in der
Reihenfolge ihrer Platzierung mit allen Einzelergebnissen darunter. Ergebnisse,
die nicht zu den gewerteten Besten der Gruppe gehören, stehen abgesetzt als
„nicht gewertet“ dabei. Gleiche Werte ergeben denselben Platz. Scheiben ohne Wert für die gewählte Wertung
(z. B. Gesamteingabe ohne Einzelschüsse bei einer Spaßwertung) werden nicht
gewertet und als Hinweis gezählt.

Die Ergebnisliste lässt sich ausgeben: **PDF** mit den Einstellungen der
Auswertung und der Ergebnisliste, Gruppen samt aller Mitglieder in einer
Datei. **CSV** für die Tabellenkalkulation, bei einer Gruppenwertung als zwei
Dateien: **CSV Gruppen** mit den Platzierungen und **CSV Einzel** mit den
Einzelergebnissen je Gruppe, einschließlich der Angabe, ob sie gewertet wurden.

Das PDF zeigt standardmäßig nur Platz, Name und Ergebnis, mit Gruppenwertung
auch die Gruppe. Unter **PDF-Liste** lassen sich Verein, Gau, Mannschaft,
Klasse, Anzahl Ergebnisse und gewertete Scheiben zuschalten und das Querformat
wählen. Die Auswahl wird mit der Auswertung gespeichert. Die CSV-Dateien
enthalten immer alle Spalten.

## Anzeigen

Bildschirme im Vorraum werden fest auf eine Adresse der
[Preisanzeige](#preisanzeige) eingestellt: `http://<Server>:8091/anzeige/<id>`.
Was dort erscheint, wird hier im Server zentral festgelegt, ohne an den
Bildschirm gehen zu müssen:

- **Standanzeige (Raster):** die Scheiben mehrerer Stände nebeneinander, live
- **Rundenwettkampf:** Zwischenstand eines Wettkampfs, wahlweise als
  **Liste** (Schützen mit Ergebnis) oder als **Scheiben** (je Mannschaft bis
  zu fünf Trefferbilder, in der Mitte Stand und Hochrechnung)
- **RWK-Dynamisch:** links alle belegten Stände als Trefferbilder (bei vielen
  Ständen seitenweise wechselnd), rechts die heute laufenden
  Rundenwettkämpfe mit Mannschaftsergebnis, Hochrechnung (HR) und den
  Einzelergebnissen, im Wechsel alle 10 Sekunden (einstellbar). Die Farben
  der Mannschaften (bis zu 5 Paarungen / 10 Mannschaften) sind einstellbar.
- **Preisschießen (Kiosk):** Ergebnislisten eines Preisschießens (auch
  „automatisch das aktuell markierte“)

Einstellbar sind außerdem die angezeigten Stände, die Rastergröße und das
Wechselintervall.

![Anzeigen](bilder/anzeigen.png)

Die **Hochrechnung** rechnet den bisherigen Schnitt auf die volle
Schusszahl hoch; für die Mannschaft zählen die besten Schützen nach der im
Wettkampf festgelegten Wertung. Jedes Trefferbild zeigt außerdem die
Restzeit (Soft grün, Hard rot, siehe
[Schießzeit](#schießzeit-und-umschalten-probe--wertung)).

![Rundenwettkampf in der Scheibenansicht](bilder/anzeige-runde-scheiben.png)

Im **Paarungswettkampf** stehen die Schützen einer Paarung in der
Scheibenansicht übereinander. Wer nach Hochrechnung vorne liegt, bekommt
oben rechts einen grünen Punkt, der andere einen grauen; in der Mitte stehen
die Punkte. Während eines Stechschusses zeigt das Trefferbild nur den
Stechschuss, Gesamtergebnis und Hochrechnung bleiben die der Wertung.

![RWK-Dynamisch](bilder/anzeige-rwk-dynamisch.png)

## Preisanzeige

Die Preisanzeige ist ein eigener Dienst (Port 8091) für die Bildschirme:
Ergebnislisten von Preisschießen und Vereinsabenden sowie die oben
beschriebenen Anzeigen (`/anzeige/<id>`). Sie liest die Datenbank nur und
rechnet keine Wertungen selbst: Preisschießen-Wertungen kommen aus dem vom
Server vorberechneten Ergebnis-Speicher, die Live-Anzeigen lesen die
aktuellen Schüsse direkt. Der Server wird dadurch nicht belastet (siehe
[Kommunikation](10-kommunikation.md)).

![Preisanzeige – Auswahl](bilder/preisanzeige-start.png)

Je Preisschießen gibt es eine Startseite mit Kennzahlen und
Teilnehmersuche sowie die in der Anzeige-Konfiguration freigegebenen Listen.

![Preisanzeige – Startseite eines Preisschießens](bilder/preisanzeige.png)

![Preisanzeige – Ergebnisliste](bilder/preisanzeige-liste.png)
