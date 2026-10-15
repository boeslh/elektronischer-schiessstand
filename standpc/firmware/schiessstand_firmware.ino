/*
 * ============================================================================
 *  Elektronischer Schießstand – ESP32 Firmware
 *  Rev 4.17.0 – NEU Zonen-Kalibrierung (nur ALGO=CLASSIC, Default aus):
 *              Der Knall ist je nach getroffenen Papierlagen unterschiedlich
 *              laut - die Komparatoren loesen dadurch lageabhaengig spaeter
 *              aus. Mit EINEM Parametersatz bleiben je Bereich systematische
 *              Abweichungen von 2-4 mm (Foto-Studie 12.10.2026). Je Zone
 *              ein eigener Satz aus Schallgeschwindigkeit, OFS0-5 und
 *              OFFSETX/Y (Geometrie gemeinsam):
 *                A  im Loch der Vorsatzscheibe (nur Schussband)  r < ZHOLE
 *                B  neben dem Schussband (nur Vorsatzscheibe)     |x| > ZBAND
 *                C  Schussband + Vorsatzscheibe                   sonst
 *              Ablauf je Schuss: Lage mit den Basiswerten, daraus Zonen-
 *              Gewichte (lineare Ueberblendung +-ZBLEND um jede Grenze),
 *              mit den Saetzen der beteiligten Zonen neu rechnen, Lage
 *              gewichtet mitteln (hoechstens 3 Durchgaenge) - identisch zum
 *              Server-Simulator (sim_zones.go). Siehe zoneSolve().
 *              - NEU SET ZONES=0|1, SET ZHOLE=<um>, SET ZBAND=<um>,
 *                SET ZBLEND=<um>, SET ZCAL=<A|B|C>,<mps>,<ofs0>,..,<ofs5>,
 *                <offx>,<offy> bzw. SET ZCAL=<A|B|C>,OFF
 *              - NEU SHOWZONES -> Telegramm "zones" (alle Zonenwerte)
 *              - "config" traegt zones/z_hole_um, "shot" bei aktiven Zonen
 *                "zone" (Zone mit dem groessten Anteil)
 *              - CAL RESET verwirft auch die Zonen-Saetze (sie gehoeren zu
 *                den alten Offsets), Grenzen und ZONES bleiben.
 *              - Werks-Defaults (nur leeres NVS): SET RADIUS 4000 statt 200
 *                (beim Wechsel mm -> 0.001mm verstellt; mit 0,2 mm liegen
 *                fast alle Schnittpunkte ausserhalb, cluster_hits wird
 *                bedeutungslos) und SET BSHIFTPCT 30 statt 40 (bester Wert
 *                der Foto-Studie 12.10.2026).
 * ============================================================================
 *
 *  Rev 4.16.1 – Bugfix abgeschnittene Telegramme (SHOW und shot):
 *              - "config" (Antwort auf SHOW) war in einem 900-Byte-Puffer
 *                gebaut, ist mit allen Feldern aber schon mit typischen
 *                Werten ~1000 Zeichen lang (Worst-Fall ~1230) und wurde
 *                beim Stand-PC abgeschnitten (kaputtes JSON, ab paper_*
 *                fehlend). Jetzt static 1536 Byte; Korrelations-Hilfspuffer
 *                in emitLine() entsprechend 1600 Byte.
 *              - "shot" (CLASSIC und RIM) von 900 auf 1024 Byte (static,
 *                nicht mehr auf dem Loop-Stack).
 *              - Offline-Ringpuffer: Zeilenlaenge TXBUF_LINE um das
 *                Signatur-Praefix erweitert (1024 + 18), damit auch ein
 *                maximal langer, signierter shot vollstaendig gepuffert wird.
 *              Telegramm-Inhalte und Felder unveraendert.
 * ============================================================================
 *
 *  Rev 4.16.0 – Vollstaendige Rohdaten fuer den Server-Simulator:
 *              - NEU SET RAWLOG=0|1 (Default 0): zu jedem GUELTIGEN Schuss
 *                direkt nach "shot" ein "raw"-Telegramm (gleiche seq) mit
 *                allen steigenden UND fallenden Flanken aus den Ring-
 *                puffern in einem Fenster um den Schuss - unkorrigiert,
 *                ohne die Fenster-/Anzahl-Grenzen der Auswertung - plus
 *                aktive Parameter (sound_act_mps, walk_k, walk_max_us,
 *                ofs_ns, mic_en, capture, Fenster). Abgelehnte Schuesse
 *                bleiben unveraendert (kein raw). Siehe emitRawTelegram().
 *              - "shot"-Telegramme tragen jetzt sound_act_mps (aktive
 *                Schallgeschwindigkeit, bei SET DHT=1 veraenderlich) und
 *                raw_follows (1 = raw folgt als naechste Zeile auf demselben
 *                Weg, der Host kann mit der Anzeige darauf warten; 0 bei
 *                RAWLOG=0 oder wenn der shot mangels TCP-Verbindung im
 *                Offline-Puffer landet, siehe rawWillFollow()).
 *              - Bugfix Offline-Ringpuffer: 32 x 1024 statt 64 x 360 Byte.
 *                shot-Zeilen erreichten mit air_w_ns ~340 Zeichen (+17 Byte
 *                Signatur) und wurden bei fehlender TCP-Verbindung
 *                abgeschnitten (kaputtes JSON, ungueltige Signatur).
 *                emitf()/emitReject() behalten 360 Byte (EMIT_LINE).
 * ============================================================================
 *
 *  Rev 4.15.0 – NEU (optional, SET DHT=0|1, Default 0): Schallgeschwindigkeit
 *              aus Temperatur und Luftfeuchte eines DHT22 an GPIO15 (Anregung:
 *              freETarget speed_of_sound.c). 1 K Temperaturfehler = 0,18 %
 *              Massstabsfehler, der mit dem Abstand vom Zentrum waechst.
 *              - Formel: 331,3*sqrt(1+T/273,15)*(1+0,16*x_w), x_w = Wasser-
 *                dampf-Stoffmengenanteil (Magnus, 1013,25 hPa), siehe
 *                speedOfSoundMps().
 *              - Eigene Ausleseroutine OHNE Interrupt-Sperre (DHT-Biblio-
 *                theken sperren ~5 ms alle Interrupts -> Mic-Flanken
 *                gingen verloren), nur in Ruhephasen, alle 30 s, Median aus
 *                3, Plausibilitaets- und Sprungpruefung - siehe serviceDht().
 *              - SET DHT=0 oder keine gueltige Messung seit 5 min: fester
 *                Wert SET SOUNDSPEED - jetzt mit Nachkommastelle, neuer
 *                Default 342.5 m/s (greift nur bei leerem NVS bzw. nach
 *                CAL RESET; ein gespeicherter ganzzahliger Wert wird
 *                uebernommen, neuer NVS-Key "sound_f"). CAL IMPORT nimmt
 *                SOUNDSPEED ebenfalls mit Nachkommastelle an.
 *              - STATUS: sound_act_mps, sound_src ("dht"|"fixed"), bei DHT=1
 *                temp_c, rh_pct, dht_age_s, dht_err (eigener 600-Byte-
 *                Puffer statt emitf()). SHOW/cal-done: sound_mps bleibt
 *                ganzzahlig (Kompatibilitaet), neu sound_set_mps (genau),
 *                dht bzw. sound_act_mps.
 * ============================================================================
 *
 *  Rev 4.14.0 – NEU (optional, SET WALKK, Default 0 = aus): Korrektur der
 *              amplitudenabhaengigen Komparator-Verzoegerung OHNE Zusatz-
 *              hardware (schusserkennung-rev5.md Abschnitt 4, Punkt 4).
 *              Ein leises Signal (fernes Mic) ueberschreitet die LM339-
 *              Schwelle spaeter als ein lautes - positionsabhaengig, daher
 *              nicht per SET OFS kalibrierbar. freETarget misst dafuer mit
 *              einer zweiten Schwelle je Kanal; hier dient stattdessen die
 *              Pulsbreite (Zeit ueber der Schwelle) als Amplitudenmass:
 *              - Bei aktivem MCPWM-Capture (SET CAPTURE=MCPWM) erfassen die
 *                Capture-Kanaele jetzt auch die FALLENDE Flanke (eigener
 *                Ringpuffer fallCC/fallUs, Preller < 2us zusammengefasst).
 *              - Pulsbreite = letzte fallende Flanke vor der naechsten
 *                gespeicherten steigenden (max. SET WALKMAX), siehe
 *                edgeWidthNs().
 *              - Korrektur je Mic: t += WALKK * (Breite - Mittel der Breiten
 *                dieses Schusses), siehe walkDeltas()/rimBuildEdges(). Der
 *                Bezug auf das Schuss-Mittel haelt t0 (Piezo-Gate bei RIM)
 *                unveraendert. WALKK=0.5 entspricht der Pulsmitte (fuer
 *                eine symmetrische Halbwelle amplitudenunabhaengig).
 *              - SET WALKCAL=1: CAL START bestimmt WALKK zusammen mit den
 *                Mic-Offsets (zusaetzliche Koordinate im Abstiegsverfahren).
 *              - shot-Telegramme tragen bei aktivem MCPWM ein neues Feld
 *                air_w_ns (Pulsbreite der ersten Flanke je Mic) fuer die
 *                Offline-Analyse; "cal"/"done" meldet walk_k/walk_fitted.
 *              Ohne MCPWM, bei TESTSHOOT oder WALKK=0 bleibt alles exakt wie
 *              bisher. Die Ringpuffer-Kopie in processShotAnchored() liegt
 *              jetzt in einer static-Struktur (edgeSnap) statt auf dem
 *              Loop-Stack.
 * ============================================================================
 *
 *  Rev 4.13.0 – NEU (optional, SET CAPTURE=ISR|MCPWM, Default ISR =
 *              bisheriges Verhalten): Hardware-Zeitstempel der 6 Luft-Mics
 *              per MCPWM-Capture (schusserkennung-rev5.md Abschnitt 4,
 *              Punkt 3; Anregung u.a. durch freETarget, das grundsaetzlich
 *              keine ISR-Zeitstempel verwendet).
 *              Problem bisher: airISR() liest den Zykluszaehler erst beim
 *              Eintritt in die Interrupt-Routine - dazwischen liegt die
 *              Interrupt-Latenz samt Jitter (WLAN-Interrupts, gemeinsamer
 *              GPIO-Dispatcher, ~1-3us = 0,3-1mm Laufweg im Luftschall).
 *              MCPWM-Capture friert den Zaehlerstand in HARDWARE zum
 *              Flankenzeitpunkt ein (APB 80 MHz = 12,5ns Aufloesung). Der
 *              ESP32 hat 2 MCPWM-Gruppen x 3 Capture-Kanaele = genau 6 Mics
 *              (AIR0-2 -> Gruppe 0, AIR3-5 -> Gruppe 1, Pins unveraendert).
 *              - Beide Gruppen-Timer werden beim Boot EINMAL ueber einen
 *                gemeinsamen GPIO-Sync-Impuls auf MCPWM_SYNC_PIN (GPIO23,
 *                muss unbeschaltet bleiben!) gleichzeitig genullt.
 *              - Gemeinsame Zeitbasis bleibt der CPU-Zykluszaehler: der
 *                Capture-Callback rechnet den Hardware-Stempel exakt in
 *                Zyklen um (cc = Anker + (cap - capAnker) * 3, beide Takte
 *                aus derselben PLL, modulo 2^32 dauerhaft exakt). Dadurch
 *                laufen Ringpuffer, beide Auswertepfade (ALGO=CLASSIC/RIM),
 *                CAL START, TESTSHOOT und das Piezo (bleibt normaler
 *                GPIO-Interrupt, dient nur als Gate) UNVERAENDERT weiter.
 *              - Der Anker wird beim Boot per Soft-Catch gesetzt (Latenz
 *                ~us, wirkt nur als konstanter Versatz Mics<->Piezo, NICHT
 *                zwischen den Mics) und in loop() alle 10s gegengeprueft
 *                (STATUS: cap_check_ns/cap_reanchor), siehe
 *                serviceCaptureCheck().
 *              - Da zwei Gruppen = zwei Interrupts eine fruehere Flanke
 *                NACH einer spaeteren melden koennen, setzt der klassische
 *                Pfad firstAirCC jetzt ggf. auf die fruehere Flanke zurueck
 *                (im ISR-Betrieb tritt das nie auf - unveraendert).
 *              Schlaegt die MCPWM-Initialisierung fehl, faellt die Firmware
 *              automatisch auf ISR zurueck (STATUS "capture":"isr",
 *              serielle Meldung). Wirkt erst nach REBOOT. Nach dem
 *              Umschalten CAL RESET + neue CAL START-Serie, da die
 *              bisherigen SET OFS-Werte die ISR-Latenzen mit einkalibriert
 *              haben.
 * ============================================================================
 *
 *  Rev 4.12.1 – Robusterer Verbindungsaufbau zum Stand-PC (maintainNetwork()):
 *              - WLAN-Watchdog: ist das WLAN laenger als 30s getrennt, wird
 *                selbst neu verbunden (WiFi.disconnect()/begin()). Der Core-
 *                Autoreconnect gibt bei manchen Trennungsgruenden (AUTH_FAIL,
 *                NO_AP_FOUND-Varianten 210-212) nach dem ersten Fehlschlag
 *                auf - dann blieb der ESP32 dauerhaft offline, wenn der
 *                Access Point beim Boot noch nicht bereit war.
 *              - Beendet der Stand-PC die TCP-Verbindung sauber (FIN), wird
 *                das jetzt erkannt und sofort neu verbunden -
 *                NetworkClient::connected() meldet in diesem Fall weiter
 *                "verbunden".
 *              - TCP-Keepalive (5s/2s/3x): ein abgeschalteter Stand-PC wird
 *                nach ca. 11s erkannt.
 *              - Wartezeit zwischen TCP-Versuchen max. 8s (vorher 16s),
 *                Fehlversuche und WLAN-Zustandswechsel werden seriell
 *                gemeldet.
 * ============================================================================
 *
 *  Rev 4.12.0 – solveAirPosition(), Stufe 2 (Endposition): neues, im Server-
 *              Simulator erprobtes Verfahren (server/sim_posalgo.go), dort
 *              gegen Σ9-Schnittpunkt-Cluster UND Foto-Kalibrierung (reale
 *              Lochpositionen) mit den besten Ergebnissen:
 *              - SET CLUSTERCORR=0|1 (NVS, Default 1): ALLE Schnittpunkte
 *                werden kugeldurchmesser-korrigiert (SET BSHIFTPCT/BSHIFTCAP,
 *                je Schnittpunkt mit dessen eigener Referenz, gleiche Formel
 *                wie fuer die Stufe-1-Loesung) ausgewaehlt und gemittelt -
 *                bisher ging nur die Stufe-1-Loesung korrigiert, alle anderen
 *                roh in den Mittelwert ein.
 *              - SET CLUSTERITER=0-10 (NVS, Default 5): zusaetzliche
 *                Durchgaenge, in denen der Cluster (SET RADIUS) um den zuletzt
 *                berechneten Mittelpunkt neu ausgewaehlt wird (Mean-Shift),
 *                Abbruch sobald sich der Mittelpunkt nicht mehr bewegt.
 *              - SET LSQ=0|1 (NVS, Default 0) + SET LSQPELLETR=0.0-5.0 (mm,
 *                Default 2.25): Endposition zusaetzlich per Ausgleichsrechnung
 *                (Gauss-Newton ueber x, y, Startzeit) ueber ALLE ausgeloesten
 *                Mikrofone verfeinern, Modell: Schall vom naechstgelegenen
 *                Punkt des Lochrands. Braucht >= 4 Mics; bei >= 5 werden die
 *                zwei auffaelligsten Mics probeweise weggelassen (Echo-
 *                Schutz). Optional, im Simulator "oft gut".
 *              CLUSTERCORR=0 + CLUSTERITER=0 + LSQ=0 = exakt das Verhalten
 *              bis Rev 4.11.5. cluster_hits/precision_um beziehen sich auf
 *              die Endposition (bei CLUSTERCORR=1 auf die korrigierten
 *              Schnittpunkte). Rechenaufwand je Schuss mit Defaults ca.
 *              +70% gegenueber 4.11.5 (mit LSQ ca. +100-160%).
 *              Ausserdem (erneut, ging mit dem 4.11.5-Stand verloren, vgl.
 *              Rev 4.11.6 im Repo): emitLine() haengt die Korrelations-ID
 *              "corr" auch an lange Zeilen an (Hilfspuffer 1024 statt 160
 *              Byte) - die config-Antwort auf SHOW kam sonst ohne corr beim
 *              Stand-PC an ("Gerät nicht erreichbar: keine Antwort innerhalb
 *              von 2s" bei der Kalibrier-Uebernahme).
 * ============================================================================
 *
 *  Rev 4.11.5 – Neu: SET SENDREJECT=0|1 (NVS, Default 0). Bei 0 gehen
 *              "reject"-Telegramme (zu wenige Mics bzw. bei ALGO=RIM "fit
 *              failed") nur noch ueber Serial raus, NICHT mehr per TCP an
 *              den Stand-PC (auch nicht in den Offline-Ringpuffer), und
 *              zwar OHNE "seq"-Feld: shotCounter/sequenceNo werden nur noch
 *              bei gueltigen Schuessen erhoeht, damit der Stand-PC keine
 *              Luecken im seq-Zaehler sieht und keine verlorenen Schuesse
 *              vermutet. SET SENDREJECT=1 stellt das bisherige Verhalten
 *              (Rev 4.6.1: jede Ausloesung bekommt eine seq, Rejects gehen
 *              auch per TCP) wieder her. Umsetzung zentral in emitReject().
 *              Ausserdem neu: SET PIEZOREQ=0|1 (NVS, Default 1). Bei SET
 *              PIEZO=1 wird ein Schuss ohne gueltige Piezo-Bestaetigung
 *              (kein Piezo-Signal bzw. ausserhalb PIEZOMIN..PIEZOMAX) jetzt
 *              als "reject" (reason "no piezo"/"piezo out of window")
 *              behandelt statt wie bisher als "shot" mit piezo_ok=0/
 *              clean=0 - und faellt damit auch unter SET SENDREJECT. Ein
 *              solcher Reject setzt nur die kurze SET REJECTLOCK-Sperrzeit
 *              (typisch Muendungsknall-Fehlausloeser, der echte Einschlag
 *              kann noch folgen). SET PIEZOREQ=0 = bisheriges Verhalten.
 *              Nur ALGO=CLASSIC - bei ALGO=RIM ist das Piezo ohnehin der
 *              Trigger. docs/protokoll-referenz.md entsprechend aktualisiert.
 * ============================================================================
 *
 *  Rev 4.11.4 – calCostClassic()/solveAirPosition(): Die Kugeldurchmesser-
 *              Korrektur (SET BSHIFTPCT/BSHIFTCAP) fliesst jetzt in die
 *              Timing-Offset-Kalibrierung (CAL START) mit ein. Bisher
 *              optimierte calCostClassic() gegen den Rest-Fehler VOR der
 *              Korrektur - SET BSHIFTPCT/BSHIFTCAP war dadurch komplett
 *              wirkungslos fuer die Kalibrier-Suche, egal welcher Wert
 *              eingestellt war, kamen immer dieselben Mic-Offsets heraus.
 *              solveAirPosition() liefert dafuer einen neuen optionalen
 *              Ausgabewert outResidualCorrMm (derselbe Rest-Fehler wie
 *              outResidualMm, aber MIT angewandter Korrektur, an der bereits
 *              verschobenen Stufe-1-Position bestX/bestY neu berechnet) -
 *              outResidualMm/pos_res_um bleiben unveraendert der reine
 *              Vorher-Wert (fuer die Telegramm-Ausgabe/Diagnose unveraendert
 *              relevant). Bewusst NICHT stattdessen precision_um (Kandidaten-
 *              Clusterbreite) als Kostenmass verwendet, da das nur von den 2
 *              naechstgelegenen Kandidaten abhaengt und die Suche dadurch
 *              einen zufaellig eng geclusterten, aber falschen Punkt statt
 *              der echten Kalibrierung finden koennte. Default SET BSHIFTPCT
 *              (nur Fallback fuer ein nie konfiguriertes Geraet) von 50 auf
 *              40 geaendert - mit der jetzt korrektur-bewussten Kalibrierung
 *              hat sich 40% als besser erwiesen. Ursprung/Vorab-Test: Server-
 *              seitiger Simulator (separates Projekt, server/simulator.go),
 *              dort u.a. mit einem Regressionstest fuer die Wiederherstell-
 *              barkeit eines einzelnen absichtlich verstellten Mikrofon-
 *              Offsets nach der Umstellung geprueft. docs/protokoll-
 *              referenz.md (5.2/7.4) entsprechend aktualisiert.
 * ============================================================================
 *
 *  Rev 4.11.3 – Hardware-Umverkabelung: AIR_PINS[4]/[5] (Luft-Mic "links
 *              mitte"/"rechts mitte") von GPIO32/33 auf GPIO4/13 verlegt.
 *              Hintergrund (siehe Session-Historie zu Kanal 4 sowie
 *              schusserkennung-rev5.md Abschnitt 4): der ESP32-GPIO-
 *              Interrupt-Controller hat fuer die Pins 0-31 und 32-39 ZWEI
 *              getrennte Status-Register - der gemeinsame ISR-Dispatcher
 *              liest und verarbeitet IMMER zuerst vollstaendig alle
 *              anstehenden Flanken aus Bank 0 (0-31), bevor er ueberhaupt
 *              Bank 1 (32-39) abfragt. Lagen also z.B. AIR0-AIR3 (GPIO25/26/
 *              27/14, alle Bank 0) und AIR4/AIR5 (bisher GPIO32/33, Bank 1)
 *              bei einem Treffer nahe der Scheibenmitte (wo alle 6 Mics fast
 *              gleichzeitig ausloesen) im selben Interrupt-Aufruf, bekamen
 *              AIR4/AIR5 einen um die Bank-Umschaltung plus die komplette
 *              Bearbeitung der Bank-0-Flanken zu SPAETEN Zeitstempel -
 *              positionsabhaengig (am staerksten genau in der Scheibenmitte)
 *              und damit NICHT per SET OFS4/OFS5 wegkalibrierbar, da der
 *              Effekt nur auftritt, wenn UEBERHAUPT ein Bank-0-Mic im
 *              selben Interrupt mitfeuert. Nach der Umverkabelung liegen
 *              alle 6 Luft-Mics in Bank 0 - nur noch das Piezo (GPIO34)
 *              bleibt in Bank 1, was unkritisch ist (SET PIEZOMIN..
 *              PIEZOMAX, >=100us Verzoegerung, liegt weit ausserhalb dieses
 *              ns-Effekts). GPIO4/13 bewusst statt z.B. GPIO0/2/12/15
 *              gewaehlt: keine Strapping-Pins (GPIO12 waere z.B. die Flash-
 *              Spannungs-Auswahl VDD_SDIO - ein bei Reset dauerhaft high
 *              liegender Komparatorausgang koennte dort jeden Bootvorgang
 *              verhindern), verhalten sich wie GPIO25/26/27/14 (normaler
 *              interner Pull-Up, keine Sonderbehandlung wie bei GPIO34-39
 *              noetig - siehe setup()). Reine Code-Aenderung ist AIR_PINS[]
 *              (siehe dort); die Geometrie (MIC_X/MIC_Y, Index-basiert)
 *              bleibt unberuehrt. Empfehlung nach dem Umstecken: CAL RESET
 *              + neue CAL START-Serie, da die bisherigen OFS4/OFS5-Werte
 *              ggf. einen Teil der jetzt entfallenen Bank-Verzoegerung mit
 *              einkalibriert hatten. docs/protokoll-referenz.md (7.1)
 *              entsprechend aktualisiert.
 * ============================================================================
 *
 *  Rev 4.11.2 – Aufraeumen ungenutzter Funktionen (Fortsetzung von Rev
 *              4.11.1): Messmodus TARGET_STEEL komplett entfernt - Papier
 *              (Durchschlagmessung) ist jetzt die einzige, fest verdrahtete
 *              Geometrie. Entfernt: cfg.targetMode, TARGET_STEEL/TARGET_PAPER,
 *              SET TARGET, cfg.standoffSteelMm/SET STANDOFFSTEEL,
 *              MIC_HALF_Y_STEEL/MIC_STANDOFF_STEEL, das "target"/
 *              "standoff_steel_mm"-Feld im SHOW-Telegramm. applyTargetGeometry()
 *              setzt MIC_Y[]/micStandoffMm jetzt unbedingt auf die (bisherige)
 *              PAPER-Geometrie (SET STANDOFFPAPER bleibt unveraendert
 *              konfigurierbar). WICHTIG - die Piezo-Trigger-Logik im
 *              PAPER-Betrieb ist davon NICHT betroffen: alle Stellen, die
 *              bisher auf "cfg.targetMode==TARGET_STEEL -> PIEZOMIN
 *              ueberspringen bzw. Gate/Nullpunkt anders legen" verzweigten
 *              (processShot()::piezoOk, calCostRim()/processShotAnchored()::
 *              gate.t0MaxNs, handleTestShoot() fuer beide ALGO-Pfade), wurden
 *              gezielt auf den bisherigen PAPER-Zweig festgeschrieben - SET
 *              PIEZOMIN/PIEZOMAX/PIEZO verhalten sich fuer echte wie fuer
 *              TESTSHOOT-Schuesse exakt wie zuvor im PAPER-Modus. Das Piezo
 *              selbst sitzt weiterhin (unveraendert) als Koerperschall-
 *              Trigger auf einer Stahlplatte HINTER der Papierscheibe -
 *              das ist Trigger-Hardware, kein Messmodus, und bleibt
 *              bestehen. tools/replay_shot.py: Default fuer ein fehlendes
 *              "target"-Feld (altes Log ohne dieses Feld) von "steel" auf
 *              "paper" umgestellt; "--target steel" bleibt zum Nachrechnen
 *              aelterer Logs von vor dieser Revision nutzbar. docs/protokoll-
 *              referenz.md entsprechend bereinigt (Abschnitt 7.2 etc.).
 * ============================================================================
 *
 *  Rev 4.11.1 – Aufraeumen ungenutzter Funktionen (Beginn, siehe Wunsch nach
 *              Reduzierung auf tatsaechlich genutzte Befehle): PIN-Diagnose
 *              (Rev 4.8.5) komplett entfernt - Befehl PIN <n>/PIN LIST,
 *              "pin"-Telegramm, PIN_DIAG_ALLOWED[]/PinDiagMode/pinDiagMode[]/
 *              pinDiagAllowed()/pinDiagModeName()/pinDiagReport()/
 *              handlePinCommand() sowie der zugehoerige HELP-Text. War nur
 *              fuer die Hardware-/Verkabelungsfehlersuche gedacht (siehe
 *              Session-Historie), nicht fuer den Normalbetrieb. Wirkt sich
 *              NICHT auf die Pinbelegung selbst aus (AIR_PINS/PIEZO_PIN/
 *              PAPER_*_PIN etc. unveraendert) - nur der Diagnose-BEFEHL
 *              entfaellt. docs/protokoll-referenz.md entsprechend bereinigt
 *              (Abschnitte 4.9/5.5 entfernt, nachfolgende umnummeriert).
 * ============================================================================
 *
 *  Rev 4.11.0 – NEU (optional, siehe SET ALGO): zweiter, per SET ALGO=
 *              CLASSIC|RIM umschaltbarer Auswertepfad ("RIM"), der
 *              schusserkennung-rev5.md umsetzt - Default bleibt CLASSIC
 *              (bisheriges Verhalten 1:1 unveraendert). Kernproblem von
 *              CLASSIC: die ERSTE beliebige Mic-Flanke oeffnet das Sammel-
 *              fenster und setzt den Zeitnullpunkt - bei einem Muendungs-
 *              knall (kommt bei 10m ~38ms VOR dem Einschlag an) faellt der
 *              danach folgende echte Einschlag in die anschliessende
 *              SET DEBOUNCE-Sperrzeit und geht komplett verloren, Piezo
 *              inklusive.
 *              ALGO=RIM loest das ueber 2 unabhaengige Bausteine (beide
 *              nur aktiv/wirksam bei ALGO=RIM, CLASSIC unangetastet):
 *              (1) Ringpuffer-Erfassung (ringCC/ringUs/ringHead, airISR()):
 *              JEDE Luft-Mic-Flanke wird laufend aufgezeichnet, unabhaengig
 *              von Sperrzeit/Fenster-Zustand - nichts geht mehr verloren.
 *              (2) Piezo-Anker-Trigger (piezoPending/freezeActive/
 *              postWaitUs, piezoISR()/processShotAnchored()): NUR das
 *              Piezo loest noch aus, ausgewertet wird RUECKWIRKEND im
 *              Fenster [Piezo-PIEZOMAX .. Piezo-PIEZOMIN] - Knall, Echos
 *              und Stoerflanken fallen automatisch heraus, egal in welcher
 *              Reihenfolge sie eintreffen.
 *              Die Positionsloesung (shot_locator.h, portabel/identisch
 *              auf ESP32 und PC, siehe test/sim/host_sim.cpp) ersetzt fuer
 *              ALGO=RIM solveAirPosition() durch eine robuste Ausgleichs-
 *              rechnung (Gauss-Newton, Huber-Gewichte) ueber ALLE
 *              Inlier-Mics gleichzeitig samt Lochrand-Modell (SET PELLETR)
 *              und einer echten 1-Sigma-Unsicherheitsangabe (sigma_x_um/
 *              sigma_y_um im "shot"-Telegramm, "clean" nutzt dafuer
 *              SET MAXSIGMA statt precision_um/cluster_hits/RADIUS/
 *              MINCLUSTER - diese Parameter bleiben fuer ALGO=CLASSIC
 *              weiter in Kraft). CAL START/TESTSHOOT funktionieren in
 *              beiden Modi (kalibrieren/simulieren jeweils den gerade
 *              aktiven Pfad). "shot"/"reject"-Telegramme tragen neu ein
 *              "algo":"classic"|"rim"-Feld; air_ns ist bei ALGO=RIM relativ
 *              zum Piezo (bei CLASSIC unveraendert relativ zur ersten
 *              Flanke) - siehe docs/protokoll-referenz.md.
 *              Nebenbei (wirkt auf BEIDE Modi, siehe schusserkennung-
 *              rev5.md Abschnitt 4): der Zykluszaehler (esp_cpu_get_cycle_
 *              count()) wird in airISR()/piezoISR() jetzt als ALLERERSTE
 *              Anweisung gelesen (vorher nach esp_timer_get_time(), das
 *              selbst Jitter kostet) - reduziert Zeitstempel-Jitter
 *              geringfuegig, aendert sonst nichts.
 *              Empfehlung vor Wettkampfeinsatz: ALGO=RIM zunaechst per
 *              TESTSHOOT pruefen, danach mit echten Schuessen auf einer
 *              Trainingsbahn gegen ALGO=CLASSIC vergleichen (z.B. per
 *              tools/replay_shot.py), bevor es produktiv gesetzt wird.
 * ============================================================================
 *
 *  Rev 4.10.3 – Zwei Default-Werte angepasst (nur NVS-Erstwerte, bereits
 *              persistierte Anlagen NICHT betroffen - siehe SET CALSHOTS/
 *              SOUNDSPEED zum manuellen Nachziehen bzw. FACTORY+REBOOT):
 *              (1) SOUNDSPEED-Default von 355 auf 343 m/s (klassischer Wert
 *              bei 20°C) - der bisherige empirisch hoehere Default wird
 *              nicht mehr als sinnvoller Startwert angesehen.
 *              (2) CALSHOTS-Default von 5 auf 10 - mehr Kalibrier-Schuesse
 *              fuer einen stabileren Timing-Offset-Koordinatenabstieg
 *              (runCalibration()).
 * ============================================================================
 *
 *  Rev 4.10.2 – Papiervorschub-Schrittmotor lief nicht mehr: die in Rev
 *              4.8.7 (STEP=GPIO21, EN=GPIO19) verabredete Belegung war
 *              zwischenzeitlich ohne entsprechenden Hardware-Umbau auf
 *              STEP=GPIO19/EN=GPIO21 getauscht worden. Zurueckgetauscht auf
 *              die urspruengliche, zur tatsaechlichen Verkabelung passende
 *              Belegung: PAPER_STEP_PIN=21, PAPER_EN_PIN=19 (DIR=GPIO18
 *              unveraendert).
 * ============================================================================
 *
 *  Rev 4.10.1 – Zwei Korrekturen an der in 4.10.0 eingefuehrten TCP-
 *              Authentifizierung (bei der Einbindung von 4.10.0 wurde
 *              versehentlich von einer aelteren Basis ausgegangen, siehe
 *              Rev-4.9.1-Hinweis unten):
 *              (1) TESTSHOOT [<z_mm>] (Rev 4.9.1) war dabei komplett
 *              verlorengegangen (Befehl, handleTestShoot(), synthetic-
 *              ShotPending, das "synthetic"-Feld in shot/reject, HELP-Text)
 *              - wiederhergestellt, unveraendert zu Rev 4.9.1.
 *              (2) emitLine() signierte TCP-Zeilen bisher ueber einen auf
 *              TXBUF_LINE (340 Byte) begrenzten Zwischenpuffer - dadurch
 *              blieben laengere Telegramme (v.a. "config", eigener 900-Byte-
 *              Puffer, typischer Inhalt bereits >500 Byte) bei konfiguriertem
 *              SET PSK FAST IMMER unsigniert, obwohl der Datei-Kopf "jede
 *              Zeile" verspricht. Fix: Tag und Nutzlast werden bei
 *              bestehender TCP-Verbindung jetzt als zwei getrennte
 *              tcp.write()-Aufrufe gesendet (siehe emitLine()) - dadurch
 *              entfaellt die Laengenbegrenzung fuers Signieren komplett,
 *              unabhaengig von TXBUF_LINE. TXBUF_LINE selbst (jetzt 360)
 *              bleibt nur noch als Kapazitaet des Offline-Ringpuffers
 *              (txBufPush(), ausschliesslich fuer shot/reject bei fehlender
 *              TCP-Verbindung) und des emitf()-Formatpuffers relevant.
 * ============================================================================
 *
 *  Rev 4.10.0 – TCP-Authentifizierung: die bisher voellig ungeschuetzte
 *              TCP-Verbindung zum Stand-PC (jedes Geraet im selben Netz
 *              konnte Befehle wie SET/REBOOT/FACTORY senden oder gefaelschte
 *              shot-Telegramme einspeisen) traegt jetzt optional ein
 *              HMAC-SHA256-Tag (mbedtls_md_hmac, Hardware-beschleunigt -
 *              vernachlaessigbare Rechenlast, KEIN TLS-Handshake) je Zeile:
 *              "<16-Hex-Zeichen-Tag> <Zeile>\n", Tag = erste 8 Byte von
 *              HMAC-SHA256(psk, Zeile). Schluessel ist ein einziger,
 *              ANLAGENWEITER Pre-Shared Key (AUTH_PSK_DEFAULT_HEX, siehe
 *              oben nahe NVS_NS - einmal pro Anlage erzeugen und in ALLE
 *              geflashten Geraete kompilieren, damit ein Ersatzgeraet ohne
 *              Pairing sofort einsatzbereit ist), ueber SET PSK=<64
 *              Hex-Zeichen> auch nachtraeglich setz-/rotierbar (siehe
 *              handleSet(), applyPskHex()). Leerer Schluessel = Feature
 *              komplett aus (Rueckwaertskompatibel fuer einen risikofreien
 *              Rollout). Verifikation beim Empfang in verifyIncomingIfTcp()
 *              (aufgerufen aus handleCommand(), noch vor dem Korrelations-
 *              ID-Parsing), Signieren beim Versand im tcp.write()-Zweig von
 *              emitLine(). BEWUSST NUR der TCP-Kanal - Serial bleibt
 *              vollstaendig unveraendert/unsigniert, da physischer
 *              Kabelzugriff dort schon die Absicherung ist und der
 *              dokumentierte manuelle Diagnose-Workflow (HELP/PIN/Terminal)
 *              sonst kaputt ginge (siehe pollCommands()/handleCommand()
 *              Parameter viaTcp). Keine Verschluesselung (Confidentiality)
 *              - nur Authentizitaet/Integritaet, bewusster Kompromiss fuer
 *              minimale Rechenlast; Mitlesen auf dem lokalen Netz (z.B. des
 *              per SET PASS=... uebertragenen WLAN-Passworts) bleibt
 *              moeglich. Nebenbei behoben: der Kommandopuffer (AUTH_CMD_BUF_LEN,
 *              vorher fest 96 Zeichen) war schon fuer die laengste
 *              dokumentierte Zeile (CAL IMPORT OFS0=...,...,SOUNDSPEED=...)
 *              knapp/zu klein und reicht jetzt mit Signatur-Praefix erst
 *              recht nicht mehr - auf 220 Zeichen erhoeht (TXBUF_LINE
 *              analog von 320 auf 340).
 * ============================================================================
 *
 *  Rev 4.9.0 – Erste Stufe der erweiterten Stand-PC-Interaktion (siehe
 *              docs/remote-interaktion-konzept.md): (1) Jedes status/config/
 *              confignet/cal-Telegramm traegt jetzt ein "mac"-Feld (Basis-
 *              MAC per esp_read_mac(), unabhaengig vom WLAN-Status) als
 *              stabile Geraete-ID fuer die Stand-PC-Datenhaltung. (2) Jeder
 *              Befehl kann mit einem Suffix " #<id>" versehen werden, dessen
 *              Wert die Antwort als zusaetzliches "corr"-Feld zuordenbar
 *              macht (siehe pendingCorrId/emitLine()). (3) Neuer ACTION-
 *              Namensraum: ACTION LIGHT=ON|OFF|AUTO uebersteuert die bisher
 *              rein automatische Standbeleuchtung (serviceLight()), ACTION
 *              TARGETCHANGE loest - wie TESTSHOOTPAPER - einen Papiervorschub
 *              aus, aber unter einem fuer den produktiven Bedienfall
 *              sprechenden Namen (identischer Codepfad). (4) CAL IMPORT
 *              schreibt Mikrofon-Offsets/Schallgeschwindigkeit atomar aus
 *              einer vom Stand-PC gespeicherten Kalibrierung zurueck (erst
 *              alle Werte validieren, dann erst schreiben - kein
 *              Teilzustand bei Uebertragungsabbruch). (5) Sicherheitsnetz
 *              fuer Netzwerk-Fernkonfiguration: die erste SET SSID/PASS/
 *              HOST/PORT/STATIC/IP/GW/SUBNET/DNS-Aenderung je Bootzyklus
 *              sichert die bis dahin gueltigen Werte als Ruecksprungpunkt
 *              (NVS "*_prv"-Keys + "net_pnd"-Flag), siehe
 *              armNetWatchdogIfNeeded(). Bestaetigt niemand die neue
 *              Konfiguration per NET CONFIRM innerhalb von 3 Minuten nach
 *              dem naechsten Boot (serviceNetWatchdog()), schreibt der ESP32
 *              die gesicherten Werte automatisch zurueck und rebootet erneut
 *              - verhindert, dass eine fehlerhafte Fernkonfiguration das
 *              Geraet dauerhaft unerreichbar macht. NET STATUS zeigt den
 *              Zustand, confignet zusaetzlich per net_pending/
 *              net_confirm_deadline_s.
 * ============================================================================
 *
 *  Rev 4.8.10 – Neu: Kommando TESTSHOOTPAPER - loest servicePaperStepper()/
 *              startPaperFeed() aus, so als waere gerade ein Schuss
 *              registriert worden, zum Testen von Mechanik/Treiber ohne
 *              echten Schuss auf die Scheibe. Bewusst unabhaengig von
 *              PAPERAUTO/PAPERTRIGGER (die filtern nur ECHTE Schuesse) und
 *              OHNE shotCounter/sequenceNo zu erhoehen oder ein "shot"-
 *              Telegramm zu senden, damit der Stand-PC keinen Phantom-
 *              Treffer sieht. Ignoriert (Fehlermeldung), wenn gerade schon
 *              ein Vorschub/Einfaedeln laeuft.
 * ============================================================================
 *
 *  Rev 4.8.9 – Default fuer SET PAPERDIR (cfg.paperDirInvert) von 0 auf 1
 *              gedreht: mit der unveraenderten DIR-Polaritaet lief der
 *              Papiervorschub am TMC2209 rueckwaerts statt vorwaerts (per
 *              TESTMODE=1 anhand von "PAPER DIR(GPIO18) = LOW (rueckwaerts)"
 *              festgestellt). SET PAPERDIR=0|1 bleibt weiterhin zur Laufzeit
 *              umschaltbar, invertiert jetzt aber ab Werk.
 * ============================================================================
 *
 *  Rev 4.8.8 – Treiber ist tatsaechlich ein TMC2209 (nicht DRV8825/A4988) und
 *              laeuft mit MS1/MS2 im Werkszustand (unbeschaltet/UART-Pins
 *              offen) auf 1/8-Microstepping, also 8 * 200 = 1600 Schritte/
 *              Umdrehung statt der bisher angenommenen 200 Vollschritte.
 *              PAPER_STEPS_PER_REV entsprechend angepasst - ohne das war der
 *              tatsaechliche Vorschub nur 1/8 der per SET PAPERFEED/PAPERSPEED
 *              eingestellten mm. STEP/DIR/EN-Ansteuerung selbst unveraendert,
 *              TMC2209 verhaelt sich dabei wie DRV8825/A4988 (STEP-Flanke,
 *              DIR-Pegel, EN aktiv-LOW).
 * ============================================================================
 *
 *  Rev 4.8.7 – STEP-Signal (Papiervorschub) von GPIO5 auf GPIO21 verlegt -
 *              GPIO5 ist ein Strapping-Pin (Boot-Modus) und dafuer offenbar
 *              nicht zuverlaessig genug. DIR (GPIO18) und EN (GPIO19)
 *              unveraendert, GPIO21 liegt auf dem Devkit weiterhin in
 *              unmittelbarer Naehe der beiden.
 * ============================================================================
 *
 *  Rev 4.8.6 – STEP-Impuls (GPIO5) hatte eine feste, fuer den Treiber locker
 *              ausreichende Mindest-High-Zeit von 4us (PAPER_STEP_PULSE_US,
 *              per delayMicroseconds() blockierend erzeugt) - an einer zur
 *              Fehlersuche angeklemmten LED war davon nichts zu sehen. Die
 *              High-Zeit ist jetzt dynamisch die HALBE aktuelle Schritt-
 *              periode (50% Tastgrad, PAPER_STEP_PULSE_MIN_US nur noch
 *              Untergrenze) - bei SET PAPERJOGSPEED/PAPERSPEED im niedrigen
 *              Bereich damit als LED-Blinken sichtbar. Dafuer komplett auf
 *              nicht-blockierend umgestellt: paperStepRise() zieht STEP nur
 *              noch HIGH und merkt sich in paperStepFallUs, wann die
 *              Ruecklanke faellig ist - erledigt wird die dann am Anfang von
 *              servicePaperStepper() bei jedem loop()-Durchlauf (ein
 *              delayMicroseconds() ueber ggf. zig/hundert Millisekunden
 *              haette sonst Schusserfassung/Netzwerk/Kommandos blockiert).
 * ============================================================================
 *
 *  Rev 4.8.5 – Neu: PIN-Diagnose (Verdacht auf einen defekten GPIO) - PIN <n>
 *              IN|PULLUP|PULLDOWN setzt einen GPIO aus einer Positivliste
 *              (aktuell 0,2,4,5,15,16,17,18,19) als Eingang und liest ihn
 *              sofort, PIN <n> OUT=<0|1> setzt ihn als Ausgang auf LOW/HIGH,
 *              PIN <n> READ liest den zuletzt gesetzten Modus erneut, PIN
 *              LIST zeigt Modus+Pegel aller erlaubten Pins. NICHT NVS-
 *              persistent (wie SET TESTMODE) - nach Reboot wieder im
 *              normalen Betriebszustand. Ueberschreibt bei den Pins, die
 *              gleichzeitig vom Papiervorschub genutzt werden (5/16-19),
 *              dessen Modus/Pegel bis zum naechsten Schritt-Impuls bzw. zur
 *              naechsten Schalter-Abfrage - fuer gezielte Pin-Fehlersuche
 *              gewollt. Siehe handlePinCommand()/PIN_DIAG_ALLOWED[].
 *              [Entfernt in Rev 4.11.1 - siehe dortigen Eintrag.]
 * ============================================================================
 *
 *  Rev 4.8.4 – Geschwindigkeit des manuellen Dauerbetriebs (Einfaedeln ueber
 *              die Kippschalter-Positionen) war fest auf 15.0 mm/s einkompi-
 *              liert und damit fuers Einfaedeln zu langsam - jetzt per SET
 *              PAPERJOGSPEED=<1.0-100.0> (mm/s) laufzeitkonfigurierbar,
 *              Default 75.0 (= 5x der bisherigen festen Geschwindigkeit).
 *              cfg.paperJogSpeedMmS ersetzt das bisherige PAPER_JOG_SPEED_
 *              MMPS-Define, in SHOW als paper_jog_speed_mmps enthalten.
 * ============================================================================
 *
 *  Rev 4.8.3 – SET TESTMODE=1 gibt jetzt auch fuer den Papiervorschub Klartext-
 *              Zeilen aus (Fehlersuche an den Einfaedel-Schaltern): bei jeder
 *              Pegelaenderung an GPIO16/17 ("erkannt"/"losgelassen"), bei
 *              jeder Aenderung von DIR/EN (siehe paperSetDir()/paperEnable())
 *              sowie alle 100 STEP-Impulse eine Zaehler-Zeile (jeden
 *              einzelnen Impuls zu loggen wuerde die Ausgabe fluten). Siehe
 *              paperTestLog()/servicePaperStepper().
 * ============================================================================
 *
 *  Rev 4.8.2 – Pinbelegung Papiervorschub getauscht: STEP/DIR/EN liegen jetzt
 *              auf GPIO5/18/19 (auf dem Devkit nebeneinander, samt GND-Pin
 *              in der Naehe - praktisch fuer einen gemeinsamen Treiber-
 *              Stecker), die beiden Einfaedel-Schalter auf GPIO16/17 (vorher
 *              STEP/DIR/EN=5/16/17, Schalter=18/19). Reine Pin-Umbelegung,
 *              Verhalten/Logik unveraendert.
 * ============================================================================
 *
 *  Rev 4.8.1 – Diagnose-Test 3 (GPIO32->GPIO35, siehe Rev-4.4.8-Hinweis
 *              weiter unten) physisch zurueckgebaut - AIR_PINS[4] wieder auf
 *              GPIO32, TEST_SENSOR_NAMES[4] wieder ohne "(GPIO35!)"-Zusatz.
 *              Damit sind jetzt alle 3 Hardware-Diagnosetests an Kanal 4
 *              ("links mitte") zurueckgebaut, die Ursache blieb das LM339
 *              dieses Kanals selbst (siehe Testverlauf weiter unten).
 * ============================================================================
 *
 *  Rev 4.8.0 – Neu: Papiervorschub. Ein NEMA17-Schrittmotor (1,8 Grad/Schritt,
 *              50,2mm Vorschub/Umdrehung) ueber ein DRV8825/A4988-Treiber-
 *              board zieht die Papierrolle hinter dem Scheibenzentrum nach
 *              jedem Schuss ein Stueck weiter (STEP=GPIO5, DIR=GPIO18,
 *              EN=GPIO19, aktiv-LOW). SET PAPERFEED=<10.0-100.0> (mm,
 *              Default 50.0) und SET PAPERSPEED=<0.5-30.0> (mm/s, Default
 *              5.0) sind zur Laufzeit konfigurierbar; die zweite Haelfte der
 *              Strecke bremst linear auf ca. 30% der eingestellten
 *              Geschwindigkeit ab (PAPER_DECEL_END_FRACTION), damit die Rolle
 *              durch ihre Massentraegheit am Ende nicht nachlaeuft. SET
 *              PAPERTRIGGER=ANY|PIEZO|CLEAN legt fest, bei welcher Art
 *              Ausloesung vorgeschoben wird (Default PIEZO: nur wenn das
 *              Piezo den Einschlag bestaetigt hat, unabhaengig vom Luft-Mic-
 *              Ergebnis), SET PAPERAUTO=0|1 schaltet den automatischen
 *              Vorschub komplett ab (z.B. fuer Testschuesse/Kalibrierung
 *              ohne Papierverbrauch, Default 1/an). SET PAPERDIR=0|1
 *              invertiert die Drehrichtung softwareseitig (vertauschte
 *              Motoranschluesse). Zusaetzlich 2 Positionen eines Kipp-
 *              schalters (GPIO16/17, kein Mittel-Aus) fuer manuellen
 *              Dauerbetrieb beim Einfaedeln der Rolle - haben Vorrang vor
 *              einem laufenden automatischen Vorschub; liegt an keinem oder
 *              (Fehlerfall) an beiden Pins ein Signal an, wird gestoppt.
 *              Komplett nicht-blockierend ueber servicePaperStepper() in
 *              loop() umgesetzt (kein delay() waehrend des Vorschubs), damit
 *              Schusserfassung/Netzwerk/Kommandos nicht stillstehen.
 * ============================================================================
 *
 *  Rev 4.7.4 – SHOW ("type":"config") wurde trotz Auslagerung der Netzwerk-
 *              Felder (Rev 4.7.1) mit den seither dazugekommenen Feldern
 *              (mic_half_x_mm, bullet_shift_pct/cap_mm) wieder zu lang fuer
 *              den 500-Byte-Puffer und schnitt am Ende ab (offset_x_um/
 *              offset_y_um/sound_mps fehlten) - Puffer auf 700 Byte erhoeht
 *              (deckt den rechnerischen Worst-Fall aller Felder bei
 *              Maximalwerten, ~592 Byte, mit Marge ab). Ausserdem: wifi/tcp
 *              (Live-Verbindungsstatus) aus STATUS nach SHOWNET verschoben
 *              (dort als wifi_ip/tcp_connected) - gehoeren inhaltlich zur
 *              Netzwerkseite, nicht zum Auswertungs-/Betriebsstatus.
 * ============================================================================
 *
 *  Rev 4.7.3 – Horizontaler Mic-Abstand zur Mittellinie (bisher fest
 *              einkompiliertes MIC_HALF_X=115.0mm) ist jetzt zur Laufzeit
 *              konfigurierbar: SET MICHALFX (5.0-300.0mm, Default 115.0,
 *              cfg.micHalfXMm) - deckt z.B. ab, dass die Mikrofon-Membran
 *              1-2mm hinter der bisher vermessenen aeusseren Huelle liegt.
 *              Neue Funktion applyMicHalfX() setzt MIC_X[] (jetzt nicht mehr
 *              const), analog zu applyTargetGeometry() fuer MIC_Y/Standoff -
 *              aber bewusst getrennt, da MIC_X nicht vom Zielmodus abhaengt.
 *              Wirkt sofort, kein Reboot noetig, in SHOW mit ausgegeben.
 * ============================================================================
 *
 *  Rev 4.7.2 – runCalibration(): 2 zusaetzliche, noch feinere Runden (jetzt
 *              11 statt 9) im Koordinatenabstieg. Beobachtung (Nachbau der
 *              echten Kalibrier-Schuesse in Python): bei nur 5 Kalibrier-
 *              Schuessen kann ein Mic-Offset ueber mehrere Runden hinweg
 *              exakt auf einem groben Zwischenschritt "einfrieren" (Kosten-
 *              funktion dort zu flach fuer die jeweilige Schrittweite), statt
 *              weiter zu konvergieren - erreichbarer Bereich bleibt praktisch
 *              gleich (~19990 statt ~19961ns von +-MIC_OFS_MAX_NS=20000),
 *              nur die Aufloesung am Ende wird feiner (~9.8ns letzter Schritt
 *              statt ~39ns).
 * ============================================================================
 *
 *  Rev 4.7.1 – SHOW-Telegramm wurde mit wachsender Parameterzahl wieder zu
 *              lang und drohte am Puffer (line[600]) abgeschnitten zu
 *              werden. Netzwerkbezogene Felder (ssid/pass/host/port/
 *              static_ip/ip/gateway/subnet/dns) sind deshalb in einen neuen
 *              Befehl SHOWNET (eigenes "type":"confignet"-Telegramm)
 *              ausgelagert - SHOW enthaelt jetzt nur noch die Auswertungs-/
 *              Kalibrier-Parameter (lane bleibt dort, da Bahnnummer keine
 *              Netzwerkeinstellung ist).
 * ============================================================================
 *
 *  Rev 4.7.0 – Neu: Kugeldurchmesser-Korrektur der Stufe-1-Loesung in
 *              solveAirPosition() (SET BSHIFTPCT/SET BSHIFTCAP). Idee: Das
 *              Projektil (ca. 4,5mm Durchmesser) strahlt den Einschlags-
 *              schall nicht exakt aus dem Lochmittelpunkt ab, sondern eher
 *              von der dem jeweiligen Mikrofon zugewandten Kugeloberflaeche.
 *              Fuer jedes an der Stufe-1-Loesung NICHT beteiligte
 *              (verbleibende) Mikrofon wird deshalb aus dem vorzeichen-
 *              behafteten Rest-Fehler eine Verschiebung Richtung (bzw. weg
 *              von) diesem Mikrofon abgeleitet, gewichtet mit SET BSHIFTPCT
 *              (Default 50%) und je Mikrofon gedeckelt auf SET BSHIFTCAP
 *              (Default 3.0mm). Wirkt nur auf die Stufe-1-Loesung, die
 *              dadurch automatisch auch als korrigierte Referenz in den
 *              Verifizierungsschritt (Stufe 2) einfliesst. SET BSHIFTPCT=0
 *              schaltet die Korrektur ab. Beide Werte in SHOW enthalten.
 * ============================================================================
 *
 *  Rev 4.6.1 – Protokoll fuer die Stand-PC-Anzeige entkoppelt von SET DEBUG:
 *              "shot"- und "reject"-Telegramme gehen jetzt IMMER raus (auch
 *              bei Ausreissern/zu wenigen Mics), jede Ausloesung bekommt
 *              dafuer immer eine fortlaufende "seq"-Nummer (auch Rejects -
 *              vorher nur bei genug Mics vergeben). Neues Feld "clean" im
 *              "shot"-Telegramm uebernimmt die bisherige DEBUG=0-Filterlogik,
 *              damit die Anzeige "saubere" Treffer erkennen kann, ohne die
 *              Schwellenlogik selbst nachzubilden. SET DEBUG steuert nur noch
 *              die zusaetzlichen Diagnosefelder (type=cand, x_um_pre/...).
 * ============================================================================
 *
 *  Rev 4.6.0 – Mic-Standoff (rechtwinklig zur Platte/Scheibe) ist jetzt zur
 *              Laufzeit konfigurierbar statt fest einkompiliert: SET
 *              STANDOFFSTEEL/SET STANDOFFPAPER (je 5.0-100.0mm, Default
 *              30.0/28.0 - vorher MIC_STANDOFF_STEEL/MIC_STANDOFF_PAPER
 *              Compile-Konstanten). Wirkt sofort (applyTargetGeometry()),
 *              kein Reboot noetig, wird in SHOW mit ausgegeben.
 *              [SET STANDOFFSTEEL in Rev 4.11.1 entfernt (mit TARGET_STEEL),
 *              SET STANDOFFPAPER bleibt unveraendert.]
 * ============================================================================
 *
 *  Rev 4.5.1 – Zwei Aenderungen an CAL START:
 *              1. airHits (Reject-/Kalibrier-Schwelle) wurde bisher VOR der
 *              SET MICEN-Maskierung aus den rohen ISR-Zaehlern berechnet -
 *              deaktivierte Mikrofone konnten so faelschlich zu "genug Hits"
 *              beitragen, obwohl sie an Loesung/Kalibrierung gar nicht
 *              teilnehmen. Jetzt wird airHits aus dem bereits maskierten
 *              airSeen[] gezaehlt.
 *              2. Die automatische Schallgeschwindigkeits-Mitkalibrierung
 *              (seit Rev 4.4) ist entfernt - lieferte wiederholt unplausible
 *              Werte (zuletzt 363 m/s bei nur 5 Schuessen). SET SOUNDSPEED
 *              ist wieder ein rein manueller Parameter, calCost()/
 *              runCalibration() wieder reiner Rest-Fehler-Koordinatenabstieg
 *              nur fuer die Timing-Offsets. MIC_OFS_MAX_NS gleichzeitig von
 *              5000 auf 20000ns angehoben (~1,8mm -> ~7,1mm bei 355m/s) -
 *              Schrittweite in runCalibration() entsprechend angepasst
 *              (stepNs=10000/9 Runden statt 2500/9).
 * ============================================================================
 *
 *  Rev 4.5.0 – Neu: SET MICEN0..MICEN5=<0|1> schliesst ein Mikrofon gezielt
 *              aus der Positionsloesung UND aus CAL START aus (Default 1/an
 *              fuer alle). Deaktivierte Mikrofone werden in processShot() so
 *              behandelt, als haetten sie nicht ausgeloest (airSeen=false),
 *              unabhaengig von einer tatsaechlich erfassten Flanke - wirkt
 *              dadurch automatisch auch auf calSeenBuf (CAL START). Gedacht,
 *              um hardwareseitig auffaellige Kanaele (siehe Session-Historie,
 *              IC2/AIR4+AIR5) gezielt abzuschalten, ohne die Hardware selbst
 *              zu aendern. SET MINMICS ggf. anpassen (mit 2 deaktivierten
 *              Mikrofonen bleiben nur noch 4 nutzbare).
 * ============================================================================
 *
 *  Rev 4.4.9 – Diagnose-Test 2 (GPIO25<->GPIO33-Tausch am LM339-Ausgang)
 *              physisch zurueckgebaut - MIC_X/MIC_Y[0]/[5] und die
 *              TEST_SENSOR_NAMES dafuer wieder auf Normalstand. Test 3
 *              (GPIO32->GPIO35, siehe AIR_PINS[]) bleibt unveraendert aktiv.
 * ============================================================================
 *
 *  Rev 4.4.8 – NUR FUER DEN HARDWARE-DIAGNOSETEST (3. Test, Nachfolgetest zu
 *              4.4.6/4.4.7): GPIO32 (Index 4, "links mitte") wurde per
 *              Umverkabelung auf GPIO35 verlegt - dasselbe LM339-Ausgangs-
 *              signal/dieselbe Mikrofon-Kette, nur ueber einen anderen ESP32-
 *              Pin. AIR_PINS[4]=35 statt 32; MIC_X[4]/MIC_Y[4] UNVERAENDERT
 *              (gleiches Signal). GPIO35 hat wie GPIO34 keinen internen
 *              Pull-Up - setup() waehlt jetzt je Pin automatisch INPUT (fuer
 *              GPIO34-39) oder INPUT_PULLUP. Der GPIO25<->GPIO33-Tausch aus
 *              4.4.7 bleibt zusaetzlich aktiv (physisch nicht zurueckgebaut).
 *              Ziel: zeigt GPIO35 (dasselbe Signal wie vorher GPIO32)
 *              weiterhin die Auffaelligkeit -> Mikrofon-/LM339-Kette ist die
 *              Ursache, nicht der Pin. Verhaelt sich GPIO35 unauffaellig ->
 *              GPIO32 als ESP32-Pin selbst war die Ursache. CAL RESET vor
 *              neuen Testschuessen empfohlen.
 * ============================================================================
 *
 *  Rev 4.4.7 – NUR FUER DEN HARDWARE-DIAGNOSETEST (Nachfolgetest zu 4.4.6):
 *              Der Mikrofon-Tausch an GPIO27/32 wurde zurueckgebaut (MIC_Y[2]/
 *              MIC_Y[4] wieder auf Original). Stattdessen wurde der LM339-
 *              AUSGANG (nicht das Mikrofon) zwischen GPIO25 (links unten,
 *              Haupt-IC) und GPIO33 (rechts mitte, dasselbe separate IC wie
 *              das in 4.4.6 auffaellige GPIO32) getauscht - MIC_X[0]/MIC_X[5]
 *              und MIC_Y[0]/MIC_Y[5] entsprechend vertauscht, AIR_PINS[]/
 *              Kalibrier-Offsets bleiben kanalbezogen unveraendert. Ziel:
 *              zeigt der jetzt ueber GPIO25 laufende (urspruenglich GPIO33-)
 *              Kanal ebenfalls eine Auffaelligkeit, ist das separate IC als
 *              Ganzes verdaechtig, nicht nur der eine Kanal an GPIO32.
 *              CAL RESET vor neuen Testschuessen empfohlen.
 * ============================================================================
 *
 *  Rev 4.4.6 – NUR FUER DEN HARDWARE-DIAGNOSETEST: Verkabelung zwischen LM339
 *              und Mikrofon-Element wurde zwischen GPIO27 (links oben) und
 *              GPIO32 (links mitte) physisch getauscht. Firmware kompensiert
 *              das in der GEOMETRIE (MIC_Y[2]/MIC_Y[4] in applyTargetGeometry()
 *              vertauscht, TEST_SENSOR_NAMES[2]/[4] entsprechend markiert) -
 *              AIR_PINS[]/Kalibrier-Offsets (SET OFS<i>) bleiben bewusst
 *              kanalbezogen (GPIO-Verkabelung zum ESP32 hat sich nicht
 *              geaendert) unveraendert. Ziel: zeigt eine zuvor auffaellige
 *              Zeitabweichung weiterhin am Kanal (GPIO32/AIR4-Slot) -> IC/
 *              Kanal ist die Ursache; wandert sie zur neuen Position von
 *              "links oben" -> das Mikrofon-Element/dessen Einbau ist die
 *              Ursache. CAL RESET vor neuen Testschuessen empfohlen (alte
 *              Offsets galten fuer die alte Verkabelung).
 * ============================================================================
 *
 *  Rev 4.4.5 – runCalibration() lief mit stepNs=800/7 Runden als geometrische
 *              Reihe nur bis ~1587,5ns (erlaubter Bereich ist aber +-5000ns,
 *              MIC_OFS_MAX_NS) - Kalibrierung lief dadurch scheinbar "an eine
 *              Grenze", die es so gar nicht gab (derselbe Bug wurde bereits
 *              einmal in dieser Session gefunden/behoben, kam durch den
 *              Rueckbau auf den alten Codestand aber wieder zurueck). Jetzt
 *              stepNs=2500/9 Runden (erreicht die vollen ~4990ns).
 * ============================================================================
 *
 *  Rev 4.4.4 – CAL START sammelt Schuesse ohne Piezo-Bestaetigung (bzw. mit
 *              unplausibler Piezo-Verzoegerung, siehe piezoOk) nicht mehr
 *              fuer die Kalibrierung ein (nur bei aktivem SET PIEZO) - bisher
 *              wurde dort nur auf >=3 erfasste Mics geprueft, wodurch auch
 *              durch den Muendungsknall verfrueht geoeffnete Sammelfenster
 *              in die Kalibrierung einflossen und sie verfaelschen konnten.
 * ============================================================================
 *
 *  Rev 4.4.3 – calCost() nutzt fuer die Schallgeschwindigkeits-Kalibrierung
 *              (CAL START) jetzt den verifizierten Stufe-2-precision_um-Wert
 *              statt des rohen Stufe-1-Werts (clusterRadiusMm war dort bisher
 *              fest 0.0f, der Verifizierungsschritt damit wirkungslos) - die
 *              Kugeldurchmesser-Erkenntnis aus Rev 4.4.2 fliesst dadurch auch
 *              in CAL START mit ein, nicht nur in die Telegramm-Ausgabe.
 * ============================================================================
 *
 *  Rev 4.4.2 – Zweistufige Positionsauswertung: der Kugeldurchmesser (ca.
 *              4,5mm) bedeutet, dass der Schall je nach beteiligter Mic-
 *              Kombination von leicht unterschiedlichen Punkten am Rand des
 *              Schusslochs ausgehen kann - SET RADIUS wird deshalb groess-
 *              zuegiger als reines Messrauschen bemessen (deckt den
 *              Lochdurchmesser mit ab). Statt nur die einzelne beste Dreier-
 *              Kombination (Stufe 1) als Endergebnis zu verwenden, wird
 *              jetzt zusaetzlich (Stufe 2, "Verifizierungsschritt") der
 *              Mittelpunkt ALLER Kandidaten innerhalb von SET RADIUS um die
 *              Stufe-1-Loesung gebildet und als finale Referenz verwendet -
 *              cluster_hits/precision_um werden relativ dazu neu berechnet
 *              (pos_res_um bleibt der Stufe-1-Wert, siehe solveAirPosition()).
 *              Bei SET DEBUG=3 werden zusaetzlich die Stufe-1-Werte
 *              ("x_um_pre"/"y_um_pre"/"precision_um_pre"/"cluster_hits_pre")
 *              mit ausgegeben, sonst nur das Endergebnis nach Stufe 2.
 * ============================================================================
 *
 *  Rev 4.4 – Schallgeschwindigkeit (SET SOUNDSPEED, Default 355 m/s) laufzeit-
 *            konfigurierbar und wird von CAL START automatisch mitkalibriert
 * ============================================================================
 *
 *  Neu in 4.4: SOUND_MM_PER_NS ist keine feste Konstante mehr, sondern
 *  cfg.soundSpeedMps (SET SOUNDSPEED=<300-400>, Default 355 m/s - empirisch
 *  per Testschuessen ermittelt, hoeher als die klassischen 343 m/s/20C).
 *  Ein radial mit dem Abstand vom Zentrum WACHSENDER Fehler (Treffer am Rand
 *  werden zu nah am Zentrum berechnet) ist ein typisches Anzeichen fuer eine
 *  zu NIEDRIG angenommene Schallgeschwindigkeit.
 *
 *  CAL START (siehe unten) kalibriert seit dieser Revision zusaetzlich zu
 *  den Mic-Offsets automatisch auch die Schallgeschwindigkeit mit, optimiert
 *  auf precision_um (nicht auf den sonst genutzten pos_res_um-Rest-Fehler -
 *  siehe Kommentar bei calCost()/runCalibration() fuer die Begruendung).
 * ============================================================================
 *
 *  Rev 4.3 – Piezo-Trigger-Bestaetigung (SET PIEZO) zur Erkennung von
 *            verfrueht durch den Muendungsknall ausgeloesten Sammelfenstern;
 *            reiner Sensor-Testmodus (SET TESTMODE) zur Kommissionierung
 * ============================================================================
 *
 *  SET TESTMODE=1 (nicht persistent, nach Reboot immer aus): schaltet die
 *  komplette Schuss-/TDOA-Logik ab und gibt bei jeder Sensor-Ausloesung
 *  (Luft-Mic ODER Piezo) sofort eine Klartext-Zeile mit Sensor-Bezeichnung
 *  UND Zeitstempel aus, z.B. "AIR2 links-oben        +0.585 ms" - je
 *  Sensor max. 1 Meldung alle 3s (Prellen/Nachschwinger beim Testen per
 *  Hand/Klopfen). Der erste Sensor nach einer Trennlinie (bzw. nach dem
 *  Aktivieren) beginnt eine neue Serie bei "+0.000 ms", alle folgenden
 *  zeigen die Verzoegerung dazu - damit laesst sich bei einem ECHTEN Schuss
 *  direkt ablesen, in welchem zeitlichen Abstand die Sensoren (inkl. Piezo)
 *  wirklich ausloesen, um z.B. SET TDOA/PIEZOMIN/PIEZOMAX passend zu
 *  justieren. Kommt 5s lang von keinem Sensor ein Signal, wird EINMALIG
 *  eine Trennlinie aus 10 "-" ausgegeben (startet die naechste Serie).
 *  Siehe testMode/testModeHit() sowie loop() weiter unten.
 * ============================================================================
 *
 *  Neu in 4.3: Optionales Piezo-Kontaktmikrofon (Koerperschall) auf der
 *  Stahlplatte hinter der Papierscheibe, an PIEZO_PIN (GPIO34).
 *
 *  Hintergrund: Das Projektil ist mit ~150 m/s LANGSAMER als der Schall
 *  (343 m/s) - der Muendungsknall der Waffe kann die Luft-Mikrofone am Ziel
 *  daher VOR dem eigentlichen Einschlag erreichen und das Sammelfenster
 *  verfrueht (mit falscher Richtung/Geometrie) oeffnen. Das fuehrt zu
 *  scheinbar "sauberen", aber physikalisch falschen Positionen.
 *
 *  Das Piezo sitzt in der Stahlplatte und spricht nur auf echten
 *  Koerperschall (Projektil-Einschlag) an, nicht auf Luftschall - damit ist
 *  es unempfindlich gegen den Muendungsknall. Es loest rechnerisch
 *  SET PIEZOMIN..SET PIEZOMAX (Default 100..1400 us) NACH dem ersten
 *  Luft-Ereignis aus (Flugzeit des Projektils vom Einschlagpunkt zur
 *  8-18cm dahinterliegenden Stahlplatte bei ~150 m/s). Faellt die
 *  gemessene Piezo-Verzoegerung ausserhalb dieses Fensters (oder das Piezo
 *  loest gar nicht aus), hat vermutlich der Muendungsknall statt des
 *  echten Einschlags das Sammelfenster geoeffnet - der Schuss gilt dann
 *  NICHT mehr automatisch als sauber (siehe isClean in processShot()),
 *  wird bei SET DEBUG>=1 aber weiterhin mit "piezo_ns"/"piezo_ok" zur
 *  Diagnose ausgegeben. SET PIEZO=0 deaktiviert die Pruefung wieder
 *  komplett (Default 1/an).
 *
 *  Das Sammelfenster (SET WINDOW) wird bei aktivem Piezo automatisch auf
 *  mindestens SET PIEZOMAX + Sicherheitsmarge verlaengert, damit das
 *  Piezo-Ereignis nicht verpasst wird (siehe loop()).
 *
 *  [Bis Rev 4.11.0 gab es hier zusaetzlich einen per SET TARGET waehlbaren
 *  STEEL-Modus (direkter Beschuss einer Stahlplatte ohne Papier, Piezo dort
 *  quasi latenzfrei nahe t=0, PIEZOMIN nicht geprueft) - seit Rev 4.11.1
 *  entfernt, da nicht mehr genutzt. Die obige Beschreibung (PIEZOMIN..
 *  PIEZOMAX NACH dem ersten Luft-Ereignis) gilt jetzt immer.]
 * ============================================================================
 *
 *  Die Messung über das Stahlblech (Körperschall-Sensoren + MCPWM-Hardware-
 *  Capture) hat sich in der Praxis nicht als zuverlässig genug erwiesen und
 *  wurde komplett entfernt. Die Positionsbestimmung erfolgt ausschließlich
 *  über Mikrofone (Piezo/Elektret) in der Seitenwand, die den Luftschall des
 *  Einschlags per TDOA (Time-Difference-of-Arrival) auswerten.
 *
 *  Ablauf: Das erste erfasste Mikrofon öffnet das Sammelfenster (SET WINDOW,
 *  Default 1 ms – knapp über SET TDOA). Innerhalb dieses Fensters
 *  werden pro Mikrofon bis zu 6 Flanken erfasst (Multi-Edge-Capture, blendet
 *  Nachschwinger/Echos aus).
 *  Gültig ab SET MINMICS Mikrofonen von 6 (Default 5, siehe cfg.minMics).
 *
 *  Geometrie-Plausibilitaetsfilter: Aufgrund des Mikrofonabstands kann die
 *  Laufzeitdifferenz zwischen zwei Mikrofonen fuer denselben Einschlag
 *  SET TDOA (Default 750 us, siehe cfg.airMaxTdoaUs) nicht überschreiten.
 *  Flanken, die spaeter als das schnellste Mikrofon dieses Schusses
 *  eintreffen, werden daher schon in der ISR verworfen (airISR) - reduziert
 *  sowohl die Flanken-Kombinationen in solveAirPosition() als auch die
 *  Telegrammgroesse.
 *
 *  Pinbelegung Luft-Mikrofone (LM339-Frontend), je 3 pro Seitenwand:
 *    GPIO25 = links unten     GPIO26 = rechts unten
 *    GPIO27 = links oben      GPIO14 = rechts oben
 *    GPIO32 = links mitte     GPIO33 = rechts mitte
 *  Diese Tabelle beschreibt die GPIO-Verkabelung im Normalzustand.
 *
 *  Pinbelegung Papiervorschub (siehe PAPER_*-Defines/servicePaperStepper()):
 *    GPIO21 = STEP (Treiber)     GPIO18 = DIR (Treiber)
 *    GPIO19 = EN (Treiber, aktiv-LOW)
 *    (GPIO21/18/19 liegen auf dem Devkit nebeneinander, samt GND-Pin daneben;
 *     STEP/EN waren zwischenzeitlich versehentlich vertauscht (STEP=19,
 *     EN=21) - siehe Rev 4.10.2 - und wieder auf die urspruengliche,
 *     tatsaechliche Verkabelung zurueckgetauscht.)
 *    GPIO16 = Kippschalter Pos. "vorwaerts" (Einfaedeln)
 *    GPIO17 = Kippschalter Pos. "rueckwaerts" (Einfaedeln)
 *
 *  Hardware-Diagnose (Verfolgung einer auffaelligen Zeitabweichung an
 *  Kanal 4/"links mitte"), Verlauf ueber 3 Tests:
 *   1. Mikrofon-Element GPIO27<->GPIO32 getauscht: Abweichung blieb am
 *      Kanal (GPIO32), nicht am Mikrofon-Element. (zurueckgebaut)
 *   2. LM339-Ausgang GPIO25<->GPIO33 getauscht (dasselbe separate IC wie
 *      GPIO32): alle drei Kanaele auffaellig, GPIO32 aber konsistent am
 *      staerksten. (zurueckgebaut)
 *   3. GPIO32-Signal auf GPIO35 verlegt (dasselbe Signal, anderer ESP32-Pin):
 *      Abweichung praktisch unveraendert -> weder Mikrofon-Element noch
 *      ESP32-Pin sind die Ursache, sondern das LM339 (bzw. dessen
 *      Beschaltung) dieses einen Kanals selbst. (zurueckgebaut)
 *
 *  Telegramm:
 *    {"type":"shot","seq":8,"air_ns":[[0,...],[...],[...],[...],[...],[...]],
 *     "x_um":-22300,"y_um":-300,"pos_res_um":43910,"precision_um":120,
 *     "cluster_hits":3,"pos_valid":1,"clean":1,"hits":4,"ts":123456789}
 *    air_ns[i] = Liste ALLER erfassten Flanken von Mikrofon i, in ns
 *    relativ zum ersten erfassten Mikrofon. Leere Liste = keine Flanke.
 *
 *  Trefferposition (x_um/y_um, 1 Einheit = 0.001 mm) wird per Hyperbel-
 *  Trilateration aus den ersten Flanken der Mikrofone berechnet. Ursprung
 *  = Zentrum der Zielflaeche (Papierscheibe), x positiv nach RECHTS, y
 *  positiv nach OBEN (aus Schützensicht). Geometrie (siehe MIC_X/MIC_Y/
 *  applyTargetGeometry() unten): x = ±115 mm vom Zentrum je Seitenwand (SET
 *  MICHALFX), 3 Mikrofone auf Höhe Mitte/+/-Y = ±85 mm, 28 mm Standoff (SET
 *  STANDOFFPAPER).
 *
 *  pos_valid=0, falls < 3 Mics ausgewertet werden konnten oder die
 *  Geometrie entartet war (x_um/y_um/pos_res_um/precision_um/cluster_hits
 *  dann 0).
 *
 *  pos_res_um = Rest-Fehler der gewählten Lösung in 0.001mm. Bei mehr als
 *  3 Treffern probiert solveAirPosition() alle Dreier-Kombinationen der
 *  erfassten Mics für die direkte Lösung durch und bewertet jede anhand
 *  des mittleren Rest-Fehlers der übrigen (nicht an der Lösung beteiligten)
 *  Mics; die Kombination mit dem kleinsten Rest-Fehler gewinnt. Bei genau
 *  3 Treffern gibt es keine übrigen Mics zum Prüfen -> Rest-Fehler immer 0.
 *  Mit 6 statt 4 Mikrofonen steigt die Redundanz (bis zu 3 statt 1
 *  Kontroll-Mics) und damit die Robustheit gegenüber einzelnen Ausreißer-
 *  Flanken deutlich. Ein großer pos_res_um (>> wenige mm) bedeutet trotzdem:
 *  Vorsicht, die Flankenzeiten waren nicht gut konsistent (z.B. Echo statt
 *  Direktschall an einem Mikrofon) - die Position ist dann mit Unsicherheit
 *  zu behandeln, nicht blind zu werten.
 *
 *  precision_um / cluster_hits: Jede der oben genannten Dreier-Kombinationen
 *  liefert unabhängig eine eigene Kandidatenposition (x,y). Seit Rev 4.4.2
 *  laeuft die Auswertung zweistufig (siehe solveAirPosition()): Stufe 1
 *  bestimmt wie bisher die einzelne beste Dreier-Kombination (kleinster
 *  Rest-Fehler pos_res_um). Stufe 2 ("Verifizierungsschritt") bildet den
 *  Mittelpunkt ALLER Kandidaten innerhalb von SET RADIUS um die Stufe-1-
 *  Loesung (Grund: der ca. 4,5mm Kugeldurchmesser bedeutet, dass der Schall
 *  je nach Mic-Kombination von leicht unterschiedlichen Punkten am Rand des
 *  Schusslochs ausgehen kann - der Mittelpunkt der ohnehin uebereinstimmenden
 *  Kombinationen ist ein robusterer Schaetzer als eine einzelne Kombination)
 *  und verwendet diesen Mittelpunkt als finale Referenz fuer x_um/y_um.
 *  precision_um/cluster_hits werden relativ zu DIESER Referenz berechnet:
 *  precision_um ist die quadratisch gemittelte Abweichung (RMS, 0.001mm) der
 *  bis zu 2 NÄCHSTGELEGENEN Kandidaten von der finalen Referenz - bewusst nur
 *  die besten 2, damit einzelne weit abweichende Ausreisser-Kombinationen
 *  (z.B. durch Echos) den Wert nicht dominieren. cluster_hits zählt ALLE
 *  Kandidatenpositionen (nicht nur die besten 2) innerhalb von SET RADIUS
 *  (Default 4000 = 4.0mm, bis Rev 4.16 200 = 0.2mm) um die finale Referenz. Bei genau 3 Treffern gibt
 *  es nur eine Kombination -> precision_um immer 0, cluster_hits immer 1,
 *  Stufe 2 aendert dann nichts. pos_res_um bleibt IMMER der Stufe-1-Wert
 *  (Konsistenz der urspruenglichen Loesung, nicht des gemittelten Punkts).
 *  SET DEBUG=3 gibt zusaetzlich die Stufe-1-Werte aus ("x_um_pre"/"y_um_pre"/
 *  "precision_um_pre"/"cluster_hits_pre"), sonst nur das Endergebnis.
 *
 *  Protokoll (Rev 4.6.1): JEDE Ausloesung (>= SET MINMICS Mics oder nicht)
 *  erzeugt IMMER ein Telegramm ("type":"shot" bzw. "type":"reject" bei zu
 *  wenigen Mics) mit fortlaufender "seq"-Nummer - unabhaengig von SET DEBUG
 *  und unabhaengig davon, ob der Schuss "sauber" war. Damit kommt bei der
 *  Anzeige zu jeder Sequenz-Nummer etwas an, auch bei Ausreissern oder zu
 *  duennen Schuessen. Ein "shot"-Telegramm traegt zusaetzlich das Feld
 *  "clean" (1/0): fasst zusammen, ob der Schuss allen Qualitaetsschwellen
 *  genuegt (Position bestimmbar, pos_res_um < SET OUTLIER, cluster_hits >=
 *  SET MINCLUSTER, precision_um <= SET MAXPRECISION, und - falls SET
 *  PIEZO=1 - vom Piezo im erwarteten Zeitfenster bestaetigt) - die Anzeige
 *  muss diese Schwellenlogik dadurch nicht selbst nachbilden.
 *
 *  SET DEBUG=0-3 (persistent im NVS) steuert NUR NOCH zusaetzliche
 *  Diagnosefelder, nicht mehr OB ueberhaupt etwas gesendet wird:
 *    0 (Default): keine zusaetzlichen Diagnosefelder
 *    3: zusaetzlich je Schuss eine Zeile PRO ausgewerteter Mic-Kombination
 *      ({"type":"cand",...}), mit deren Ergebnis (x_mm/y_mm, 2 Nachkomma-
 *      stellen) und den dafuer verwendeten Laufzeiten (t_ref_ns/t_a_ns/
 *      t_b_ns der beteiligten Mics ref/a/b) - siehe solveAirPosition() -
 *      sowie die Stufe-1-Werte vor dem Verifizierungsschritt (x_um_pre/
 *      y_um_pre/precision_um_pre/cluster_hits_pre) im "shot"-Telegramm.
 *
 *  SET OUTLIER=<0.001mm> (persistent im NVS, Default 5000 = 5.0mm) legt
 *  die Schwelle fest, ab der ein Schuss als "Mikrofon-Ausreißer" gilt.
 *
 *  SET MINCLUSTER=<0-20> (persistent im NVS, Default 2) legt fest, wie
 *  viele cluster_hits (siehe precision_um/cluster_hits oben) mindestens
 *  vorliegen muessen, damit ein Schuss als sauber gilt - bei genau 3
 *  Treffern ist cluster_hits immer 1 (keine Redundanz), solche Schuesse
 *  fallen mit dem Default also automatisch unter DEBUG=1.
 *
 *  SET MAXPRECISION=<0.001mm> (persistent im NVS, Default 2000 = 2.0mm)
 *  legt die Schwelle fest, ab der precision_um einen Schuss als nicht mehr
 *  sauber gilt.
 *
 *  Befehl HELP oder ? gibt eine Liste aller SET-Parameter mit gültigem
 *  Wertebereich aus. Reiner Klartext nur über Serial (nicht per emitLine/
 *  TCP), damit der JSON-Zeilenstrom zum Stand-PC sauber bleibt.
 *
 *  Zeitstempel: Die Flankenzeiten stammen vom CPU-Zykluszähler
 *  (esp_cpu_get_cycle_count, ~4,2 ns Auflösung bei 240 MHz), nicht vom
 *  µs-Timer. Grobzeitlogik (Sperrzeit, Sammelfenster) nutzt weiter
 *  esp_timer. WICHTIG: Alle Sensor-ISRs werden aus setup() registriert und
 *  laufen damit auf demselben Core -> Zykluszähler-Werte sind vergleichbar.
 *
 *  Konfiguration / Befehle: SET ... (NVS-persistent), SHOW, STATUS, PING,
 *  RESET, REBOOT, FACTORY, HELP/?
 *
 *  Kalibrierung (Timing-Offset je Mikrofon, SET OFS0..OFS5 in ns, Bereich
 *  +-MIC_OFS_MAX_NS=20000): CAL START sammelt die naechsten SET CALSHOTS
 *  (Default 10) gueltigen Schuesse an BELIEBIGEN, vorher nicht festgelegten
 *  Stellen der Scheibe und berechnet daraus per Koordinatenabstieg
 *  automatisch einen Timing-Offset je Mikrofon (keine Benutzerinteraktion
 *  noetig), der direkt persistiert und ab dem naechsten Schuss angewendet
 *  wird. Kompensiert werden damit systematische Laufzeitunterschiede der
 *  Kanaele (Komparator-Schwelle, Kabellaenge) - keine 3D-Neuvermessung der
 *  Mic-Positionen (siehe runCalibration() fuer die Begruendung). SET
 *  SOUNDSPEED wird bewusst NICHT mitkalibriert (siehe Rev-4.5.1-Hinweis ganz
 *  oben) - blieb in der Praxis wiederholt bei unplausiblen Werten haengen und
 *  bleibt daher ein rein manueller Parameter. CAL ABORT bricht ab, CAL STATUS
 *  zeigt den Fortschritt, CAL RESET setzt alle Offsets auf 0 und die
 *  Schallgeschwindigkeit auf den Default (343 m/s) zurueck.
 *
 *  Build: Arduino IDE / PlatformIO, Board "ESP32 Dev Module"
 *         (Arduino-Core >= 2.x wegen esp_cpu_get_cycle_count)
 * ============================================================================
 */

#include <Arduino.h>
#include <WiFi.h>
#include <lwip/sockets.h>          // recv()/TCP-Keepalive, siehe maintainNetwork()
#include <Preferences.h>
#include <math.h>                 // sqrtf/fabsf/lroundf (Positionsberechnung)
#include "esp_cpu.h"
#include "esp_mac.h"               // esp_read_mac() - Basis-MAC als Geraete-ID,
                                    // unabhaengig vom WLAN-Zustand (siehe deviceMac)
#include <mbedtls/md.h>            // HMAC-SHA256 fuer die TCP-Authentifizierung
                                    // (siehe AUTH_PSK_DEFAULT_HEX/computeAuthTag) -
                                    // im ESP32-Arduino-Core bereits enthalten,
                                    // Hardware-SHA-Beschleunigung -> vernachlaessigbare
                                    // Rechenlast, KEIN TLS-Handshake pro Verbindung
#include "shot_locator.h"          // SET ALGO=RIM (siehe Rev-4.11.0-Hinweis oben) -
                                    // portabler Loeser, identisch auf ESP32 und PC
                                    // (test/sim/host_sim.cpp), kein Arduino-Include
#include "driver/mcpwm_cap.h"      // SET CAPTURE=MCPWM (Rev 4.13.0) - Hardware-
#include "driver/mcpwm_sync.h"     // Zeitstempel der Luft-Mics, siehe
#include "driver/gpio.h"           // airCaptureInit()

// ---------------------------------------------------------------------------
// Konstanten & Werks-Defaults (greifen nur bei leerem NVS)
// ---------------------------------------------------------------------------

#define FW_VERSION   "4.17.0"
#define SERIAL_BAUD  115200
#define NVS_NS       "schiessstd"     // NVS-Namespace

// Anlagenweiter Pre-Shared Key fuer die HMAC-Authentifizierung der
// TCP-Verbindung zum Stand-PC (Serial bleibt bewusst unauthentifiziert -
// physischer Kabelzugriff ist dort schon die Absicherung, siehe
// verifyIncomingIfTcp()/emitLine()). Leer = Feature aus (Rueckwaertskompatibel
// fuer einen risikofreien Rollout). Fuer den Produktivbetrieb EINMAL PRO
// ANLAGE einen Schluessel erzeugen (z.B. "openssl rand -hex 32"), hier
// eintragen und alle ESP32 dieser Anlage mit demselben Firmware-Image
// flashen - ein Ersatzgeraet ist dann ohne weiteres Pairing sofort
// einsatzbereit. Kann spaeter ueber "SET PSK=<64 Hex-Zeichen>" (authentifiziert
// mit dem noch gueltigen Schluessel, oder unauthentifiziert ueber Serial)
// rotiert werden - der neue Wert wird in NVS persistiert und hat dann
// Vorrang vor diesem Default.
#define AUTH_PSK_DEFAULT_HEX  "df07c9827321073c62f4edd830aa8b6adbac934a126605b25b33006c03fb2c9f"

// Zeitfenster nach einem Boot mit ausstehender Netzwerk-Bestaetigung (siehe
// armNetWatchdogIfNeeded()/serviceNetWatchdog()), bevor die zuletzt
// bestaetigte Netzwerkkonfiguration automatisch wiederhergestellt wird.
#define NET_CONFIRM_TIMEOUT_MS  180000UL   // 3 Minuten

// Luft-Mikrofone: LM339-Frontend, 3 pro Seitenwand (links/rechts)
#define NUM_AIR        6
#define AIR_MAX_EDGES  6
// Reihenfolge: 0=links unten 1=rechts unten 2=links oben 3=rechts oben
//              4=links mitte 5=rechts mitte  (siehe MIC_X/MIC_Y weiter unten)
//
// Diagnose-Test 3 (GPIO32->GPIO35, siehe Rev-4.4.8-Hinweis im Header-
// Kommentar ganz oben) ist zurueckgebaut - Index 4 war danach wieder auf
// GPIO32 (Tests 1/2 waren bereits vorher zurueckgebaut, siehe Rev 4.4.7/
// 4.4.9).
//
// Rev 4.11.3 – Index 4/5 (GPIO32/33) auf GPIO4/13 umverkabelt: GPIO32/33
// liegen im ESP32-GPIO-Interrupt-Controller in einer ZWEITEN Bank
// (GPIO 32-39), die der gemeinsame GPIO-ISR-Dispatcher immer ERST NACH
// vollstaendiger Abarbeitung der ersten Bank (GPIO 0-31) ueberhaupt liest -
// bei fast gleichzeitigen Flanken (Treffer nahe der Scheibenmitte, wo alle
// Mics fast gleich weit entfernt sind) bekamen Index 4/5 dadurch einen um
// die Bank-Umschaltung UND die Bearbeitung aller anstehenden Bank-0-Flanken
// zu spaeten Zeitstempel - positionsabhaengig, nicht per SET OFS4/OFS5
// wegkalibrierbar (siehe Session-Historie zu Kanal 4 oben). GPIO4/13 liegen
// in derselben Bank wie alle anderen Mics (0-31) - nur noch das Piezo
// (GPIO34) bleibt in der zweiten Bank, dessen Verzoegerung zu den Mics
// (SET PIEZOMIN..PIEZOMAX, >=100us) liegt weit ausserhalb dieses ns-Effekts.
// GPIO4/13 sind bewusst gewaehlt: keine Strapping-Pins (anders als z.B.
// GPIO0/2/12/15), verhalten sich wie GPIO25/26/27/14 (normaler interner
// Pull-Up, keine Sonderbehandlung wie bei GPIO34-39 noetig). Nach dieser
// Umverkabelung CAL RESET + neue CAL START-Serie empfohlen, da die alten
// OFS4/OFS5-Werte ggf. einen Teil der jetzt entfallenen Bank-Verzoegerung
// mit einkalibriert hatten.
static const uint8_t AIR_PINS[NUM_AIR] = {25, 26, 27, 14, 4, 13};

// Optionales Piezo-Kontaktmikrofon (Koerperschall) auf der Stahlplatte,
// dient als Trigger-Bestaetigung gegen verfrueh durch den Muendungsknall
// geoeffnete Sammelfenster (siehe SET PIEZO, Rev-4.3-Hinweis oben).
// GPIO34 hat KEINEN internen Pull-Up/Down (ESP32 GPIO 34-39) - das
// Piezo-Modul muss push-pull treiben (uebliche LM393-Komparator-Module tun das).
#define PIEZO_PIN  34

// SET CAPTURE=MCPWM (Rev 4.13.0, siehe airCaptureInit()): Hilfspin, ueber den
// beim Boot EIN Sync-Impuls beide MCPWM-Capture-Timer (Gruppe 0: AIR0-2,
// Gruppe 1: AIR3-5) gleichzeitig nullt. Wird als Ausgang getrieben und per
// GPIO-Matrix intern in beide Gruppen zurueckgelesen - MUSS UNBESCHALTET
// bleiben. GPIO23: kein Strapping-Pin, sonst in dieser Firmware ungenutzt.
#define MCPWM_SYNC_PIN  23

// Papiervorschub (NEMA17-Schrittmotor, 1,8 Grad/Schritt = 200 Vollschritte/
// Umdrehung, ueber TMC2209-Treiberboard, per MS1/MS2 im Werkszustand auf
// 1/8-Microstepping = 1600 Schritte/Umdrehung, siehe PAPER_STEPS_PER_REV
// unten) - zieht die Papierrolle hinter dem Zentrum der Scheibe nach jedem
// Schuss ein Stueck weiter, siehe servicePaperStepper()/startPaperFeed()
// weiter unten. STEP/DIR sind die ueblichen Treiber-Logiksignale, EN ist
// beim TMC2209 (wie bei DRV8825/A4988) aktiv-LOW
// (LOW = Endstufe aktiv/Haltemoment, HIGH = hochohmig/aus).
// GPIO21/18/19 liegen auf dem ESP32-Devkit nebeneinander (samt GND-Pin in der
// Naehe) - praktisch fuer die 3 Treiber-Leitungen an einem Stecker.
// (GPIO5 war urspruenglich STEP, ist aber als Strapping-Pin fuer das
// STEP-Signal ungeeignet - siehe Rev-Historie oben - daher auf GPIO21 verlegt.
// STEP/EN waren zwischenzeitlich versehentlich auf GPIO19/21 getauscht (siehe
// Rev 4.10.2) - zurueckgetauscht auf die urspruengliche Belegung: STEP=21,
// EN=19.)
#define PAPER_STEP_PIN     21
#define PAPER_DIR_PIN      18
#define PAPER_EN_PIN       19
// 2 Positionen eines Kippschalters (kein Mittel-Aus) fuer den manuellen
// Dauerbetrieb beim Einfaedeln der Rolle - je Pin gegen GND schaltend,
// daher INPUT_PULLUP (aktiv = LOW). Liegt an KEINEM oder (sollte normalerweise
// nicht vorkommen) an BEIDEN Pins ein Signal an, wird sicherheitshalber
// gestoppt statt eine Richtung zu vermuten, siehe servicePaperStepper().
#define PAPER_SW_FWD_PIN   16
#define PAPER_SW_REV_PIN   17
// Entprellzeit fuer die beiden Kippschalter-Eingaenge: ein einzelner kurzer
// Stoerimpuls (z.B. Schaltrauschen vom Schrittmotortreiber, das ueber
// gemeinsame Masse/Kabelfuehrung einkoppelt) wuerde sonst als "losgelassen"
// gelesen und der Treiber sicherheitshalber sofort abgeschaltet
// (paperEnable(false), siehe servicePaperStepper()) - obwohl der Schalter
// mechanisch durchgehend gehalten wird. Ein neuer Pegel muss deshalb erst
// PAPER_SW_DEBOUNCE_US lang stabil anliegen, bevor er uebernommen wird.
#define PAPER_SW_DEBOUNCE_US 5000

// Standbeleuchtung (LED): geht beim Booten sofort an (siehe setup()) und
// zeigt danach den TCP-Verbindungsstatus zum Host an - bleibt waehrend der
// ersten LIGHT_GRACE_MS an, um dem Verbindungsaufbau Zeit zu geben, geht
// danach aus falls bis dahin keine TCP-Session steht, und folgt anschliessend
// live tcp.connected() (siehe serviceLight() weiter unten).
// Zur Fehlereingrenzung (WLAN ok, aber Host/TCP nicht erreichbar?) blinkt die
// LED LIGHT_BLINK_DELAY_MS nach dem Abschalten einmal fuer LIGHT_BLINK_
// DURATION_MS kurz auf, sofern zu dem Zeitpunkt eine WLAN-Verbindung steht -
// danach bleibt sie endgueltig aus, bis tcp.connected() wieder true wird.
#define LIGHT_PIN               22
#define LIGHT_GRACE_MS          10000
#define LIGHT_BLINK_DELAY_MS    1000
#define LIGHT_BLINK_DURATION_MS 1000

// Mechanik: 200 Vollschritte/Umdrehung (1,8 Grad/Schritt), TMC2209 laeuft
// mit MS1/MS2 im Werkszustand (unbeschaltet) auf 1/8-Microstepping, also
// 200 * 8 = 1600 Schritte/Umdrehung. Falls MS1/MS2 spaeter fest verdrahtet
// oder per UART auf eine andere Aufloesung gestellt werden, muss diese
// Konstante entsprechend angepasst werden (siehe Rev-4.8.8-Hinweis oben) -
// sonst stimmt der ueber SET PAPERFEED/PAPERSPEED eingestellte mm-Wert nicht
// mehr mit dem tatsaechlichen Vorschub ueberein.
// 50,2mm Papiervorschub je Umdrehung.
#define PAPER_STEPS_PER_REV   1600
#define PAPER_MM_PER_REV      50.2f
#define PAPER_STEPS_PER_MM    (PAPER_STEPS_PER_REV / PAPER_MM_PER_REV)
// STEP-Impuls: High-Zeit ist die HALBE aktuelle Schrittperiode (50% Tastgrad,
// siehe paperStepRise()) statt eines festen, fuer den Treiber selbst voellig
// ausreichenden Kurzimpulses - damit ist z.B. eine an GPIO21 angeklemmte LED
// bei niedrigen Geschwindigkeiten (SET PAPERJOGSPEED/PAPERSPEED) tatsaechlich
// als Blinken sichtbar. PAPER_STEP_PULSE_MIN_US ist nur die untere Grenze
// dafuer (TMC2209 wie DRV8825/A4988 brauchen laut Datenblatt << 1us).
#define PAPER_STEP_PULSE_MIN_US   4

// Geschwindigkeitsprofil des automatischen Vorschubs (SET PAPERSPEED, siehe
// servicePaperStepper()): erste Haelfte der Strecke konstant, zweite Haelfte
// linear abfallend, damit die Papierrolle durch ihre eigene Massentraegheit
// am Ende nicht nachlaeuft/uebersteuert. PAPER_MIN_SPEED_MMPS verhindert bei
// sehr niedrig eingestellter SET PAPERSPEED ein unpraktikabel langsames Ende.
#define PAPER_DECEL_END_FRACTION  0.3f
#define PAPER_MIN_SPEED_MMPS      0.5f
// Geschwindigkeit fuer den manuellen Dauerbetrieb (Einfaedeln, SET
// PAPERJOGSPEED) - eigener Parameter statt cfg.paperSpeedMmS, da beim
// Einfaedeln Zuegigkeit statt der fuer den Schuss-Vorschub gewaehlten
// Praezisions-Geschwindigkeit gewuenscht ist. Kein Beschleunigungsprofil wie
// beim automatischen Vorschub - der Motor startet direkt mit voller
// Geschwindigkeit, bei sehr hoch eingestelltem PAPERJOGSPEED ist daher ein
// Anlaufverlust (verlorene Schritte) moeglich.
#define PAPER_JOG_SPEED_MIN_MMPS  1.0f
#define PAPER_JOG_SPEED_MAX_MMPS  100.0f

#define PAPER_TRIG_ANY    0    // Vorschub bei JEDER Ausloesung (auch reject)
#define PAPER_TRIG_PIEZO  1    // nur wenn das Piezo ausgeloest hat (Default)
#define PAPER_TRIG_CLEAN  2    // nur bei "clean"-Schuessen (alle Qualitaetsschwellen)

// Auswertepfad (SET ALGO, siehe Rev-4.11.0-Hinweis im Header-Kommentar ganz
// oben sowie processShot()/processShotAnchored()): CLASSIC ist Default und
// bisheriges Verhalten 1:1 unveraendert, RIM aktiviert Ringpuffer-Erfassung +
// Piezo-Anker-Trigger + shot_locator.h-Loeser.
#define ALGO_CLASSIC  0
#define ALGO_RIM      1

// Geometriebedingt kann die Laufzeitdifferenz zwischen zwei Mikrofonen fuer
// denselben Einschlag eine gewisse Schwelle nicht ueberschreiten. Flanken,
// die spaeter als das schnellste Mikrofon dieses Schusses eintreffen, sind
// daher garantiert kein Direktschall (Echo/Nachschwinger) und werden schon
// in der ISR verworfen - reduziert sowohl die Zahl der Flanken-Kombinationen
// in solveAirPosition() als auch die Telegrammgroesse. Laufzeitkonfigurierbar
// per SET TDOA (Default 750 us, siehe cfg.airMaxTdoaUs).

// Zonen-Kalibrierung (Rev 4.17.0, siehe zoneSolve()): Satz einer Zone.
// valid=0 -> die Zone nutzt die Basiswerte (SET SOUNDSPEED/OFS<i>/OFFSETX/Y).
#define ZONE_COUNT 3   // 0=A Loch Vorsatzscheibe, 1=B neben Band, 2=C Band+Vorsatz
struct ZoneCal {
    uint8_t valid;
    float   soundMps;               // wirkt als Differenz zu SET SOUNDSPEED
                                    // (eine DHT-Messung bleibt so erhalten)
    int32_t micOffsetNs[NUM_AIR];
    int32_t offsetXUm, offsetYUm;
};

struct DeviceConfig {
    String   ssid;          // "" = WLAN deaktiviert
    String   pass;
    String   host;          // Stand-PC
    uint16_t port;
    uint16_t lane;
    uint32_t debounceMs;
    uint32_t rejectLockoutMs; // Sperrzeit NACH einem Reject (zu wenige Mics,
                            // z.B. Muendungsknall-Fehlausloeser) statt der
                            // vollen debounceMs (SET REJECTLOCK, Default 5) -
                            // siehe Kommentar in processShot()
    bool     sendRejects;   // "reject"-Telegramme auch per TCP senden und
                            // dafuer seq vergeben (SET SENDREJECT, Default 0 =
                            // nur Serial, ohne seq) - siehe emitReject()
    uint32_t windowMs;
    uint8_t  debug;        // Ausgabe-Filter: 0=nur saubere Schuesse (Default),
                            // 1=+Schuesse mit Mikrofon-Ausreisser, 2=+Reject
                            // wegen zu weniger Hits
    uint32_t airOutlierUm; // Schwelle (0.001mm) ab der ein Schuss als
                            // Mikrofon-Ausreisser gilt (SET OUTLIER)
    uint32_t clusterRadiusUm; // Umkreis (0.001mm) fuer cluster_hits (SET RADIUS)
    uint8_t  minClusterHits; // Mindestzahl cluster_hits, ab der ein Schuss
                            // als sauber gilt (SET MINCLUSTER, Default 2)
    uint32_t maxPrecisionUm; // Schwelle (0.001mm) fuer precision_um, ab der
                            // ein Schuss NICHT mehr als sauber gilt
                            // (SET MAXPRECISION, Default 2000 = 2.0mm)
    uint8_t  minMics;       // Mindestzahl Mics fuer eine gueltige Auswertung
                            // (SET MINMICS, 3-NUM_AIR, Default 5)
    uint32_t airMaxTdoaUs;  // Geometrie-Plausibilitaetsfenster in us
                            // (SET TDOA, Default 750)
    int32_t  micOffsetNs[NUM_AIR]; // Timing-Offset je Mikrofon in ns, per
                            // Kalibrierung (CAL START) ermittelt oder
                            // manuell per SET OFS0..OFS<NUM_AIR-1>
    bool     micEnabled[NUM_AIR]; // Mikrofon fuer Auswertung UND Kalibrierung
                            // beruecksichtigen? (SET MICEN0..MICEN<NUM_AIR-1>,
                            // Default 1/an) - deaktivierte Mikrofone werden
                            // in processShot() so behandelt, als haetten sie
                            // nicht ausgeloest (airSeen=false), unabhaengig
                            // davon ob tatsaechlich eine Flanke erfasst wurde.
                            // Dient z.B. dazu, hardwareseitig auffaellige
                            // Kanaele (siehe Session-Historie IC2) gezielt
                            // aus Positionsloesung UND CAL START auszuschliessen
    uint8_t  calShotCount;  // Anzahl Kalibrier-Schuesse (SET CALSHOTS,
                            // 3-MAX_CAL_SHOTS, Default 10)
    float    standoffPaperMm; // Mic-Standoff (rechtwinklig zur Scheibe) in mm
                            // (SET STANDOFFPAPER, Default 28.0)
    float    micHalfXMm;   // Horizontaler Abstand Mic-Spalte<->Mittellinie in
                            // mm (SET MICHALFX, Default 115.0) - deckt z.B. ab,
                            // dass die Membran 1-2mm hinter der aeusseren
                            // Mic-Huelle liegt
    uint8_t  bulletShiftPct; // Kugeldurchmesser-Korrektur (siehe
                            // solveAirPosition()): Gewichtung 0-100% des
                            // signierten Rest-Fehlers der Stufe-1-Loesung
                            // gegen die daran nicht beteiligten Mikrofone,
                            // als Verschiebung Richtung/weg vom jeweiligen
                            // Mikrofon (SET BSHIFTPCT, Default 30, 0=aus) -
                            // fliesst seit Rev 4.11.4 auch in CAL START mit
                            // ein, siehe calCostClassic()
    float    bulletShiftCapMm; // Kappung der Kugeldurchmesser-Korrektur je
                            // Mikrofon in mm (SET BSHIFTCAP, Default 3.0)
    bool     clusterCorr;  // Stufe 2: alle Schnittpunkte kugeldurchmesser-
                            // korrigiert mitteln (SET CLUSTERCORR, Default 1,
                            // Rev 4.12.0) - siehe solveAirPosition()
    uint8_t  clusterIter;  // Stufe 2: zusaetzliche Nachzentrier-Durchgaenge
                            // (SET CLUSTERITER 0-10, Default 5, Rev 4.12.0)
    bool     lsq;          // Endposition per Ausgleichsrechnung ueber alle
                            // Mics verfeinern (SET LSQ, Default 0, Rev 4.12.0)
    float    lsqPelletRMm; // Lochrand-Radius fuer SET LSQ in mm (SET
                            // LSQPELLETR, Default 2.25, 0 = Punktquelle)
    uint8_t  algoMode;      // ALGO_CLASSIC (Default) oder ALGO_RIM (SET ALGO,
                            // siehe Rev-4.11.0-Hinweis im Header-Kommentar) -
                            // waehlt Trigger UND Positionsloesung, wirkt
                            // sofort, kein Reboot noetig
    float    pelletRadiusMm; // Wirksamer akustischer Lochrand-Radius fuer
                            // ALGO=RIM (SET PELLETR, Default 2.25 = 4,5mm-
                            // Diabolo, 0 = Punktquelle), siehe shot_locator.h
    float    maxSigmaMm;   // "clean"-Schwelle fuer ALGO=RIM (SET MAXSIGMA,
                            // Default 2.0mm): max(sigma_x,sigma_y) aus der
                            // Kovarianzmatrix darf diesen Wert nicht
                            // ueberschreiten - Gegenstueck zu MAXPRECISION/
                            // RADIUS/MINCLUSTER bei ALGO=CLASSIC
    bool     airMcpwm;      // Luft-Mic-Zeitstempel per MCPWM-Hardware-Capture
                            // statt GPIO-ISR (SET CAPTURE=MCPWM|ISR, Default
                            // ISR, wirkt nach Reboot) - siehe airCaptureInit()
    float    walkK;         // Amplituden-Korrektur ueber die Pulsbreite:
                            // t += walkK * (Breite - Schuss-Mittel) (SET WALKK
                            // 0.0-1.0, Default 0 = aus, 0.5 = Pulsmitte), nur
                            // mit aktivem MCPWM-Capture - siehe walkDeltas()
    bool     walkCal;       // CAL START bestimmt walkK mit (SET WALKCAL, Default 0)
    uint32_t walkMaxUs;     // Groesste plausible Pulsbreite in us (SET WALKMAX,
                            // Default 150) - siehe edgeWidthNs()
    bool     usePiezo;      // Piezo (Stahlplatte, PIEZO_PIN) als Trigger-
                            // Bestaetigung nutzen? (SET PIEZO, Default 1/an)
    bool     piezoRequired; // Bei SET PIEZO=1: Schuss ohne gueltige Piezo-
                            // Bestaetigung (piezo_ok=0) als "reject" statt als
                            // "shot" mit clean=0 behandeln? (SET PIEZOREQ,
                            // Default 1) - nur ALGO=CLASSIC, siehe processShot()
    uint32_t piezoMinUs;    // Erwartete min. Verzoegerung Piezo nach erstem
                            // Luftschall-Ereignis in us (SET PIEZOMIN, Default 100),
                            // siehe processShot()
    uint32_t piezoMaxUs;    // Erwartete max. Verzoegerung Piezo nach erstem
                            // Luftschall-Ereignis in us (SET PIEZOMAX, Default 1400)
                            // - Ausreisser-Obergrenze
    uint32_t testCooldownMs; // Min. Abstand zwischen 2 Meldungen desselben
                            // Sensors im Testmodus, in ms (SET TESTCOOLDOWN,
                            // Default 3000) - siehe testModeHit()
    int32_t  offsetXUm;    // Konstanter Korrektur-Offset auf x_um/y_um in
    int32_t  offsetYUm;    // 0.001mm, z.B. zum Ausgleich einer Mess-
                            // gitter-Verschiebung (SET OFFSETX/OFFSETY,
                            // Default 0) - wird erst NACH der Trilateration
                            // addiert, siehe processShot()
    bool     zonesOn;       // Zonen-Kalibrierung aktiv (SET ZONES, Default 0,
                            // Rev 4.17.0) - siehe zoneSolve()
    uint32_t zHoleUm;       // Radius Loch der Vorsatzscheibe in 0.001mm
                            // (SET ZHOLE, Default 0 = Zonen wirkungslos;
                            // LG 15250, LP 29750)
    uint32_t zBandUm;       // halbe Breite des Schussbands (SET ZBAND,
                            // Default 45000)
    uint32_t zBlendUm;      // Ueberblendung +- um jede Zonengrenze (SET
                            // ZBLEND, Default 2250 = Diabolo-Radius)
    ZoneCal  zone[ZONE_COUNT]; // Saetze A/B/C (SET ZCAL)
    bool     rawLog;        // Zu jedem gueltigen Schuss ein "raw"-Telegramm mit
                            // allen unkorrigierten Flanken (SET RAWLOG, Default
                            // 0, Rev 4.16.0) - siehe emitRawTelegram()
    bool     useDht;        // Schallgeschwindigkeit aus DHT22-Messung (SET DHT,
                            // Default 0, Rev 4.15.0) - siehe serviceDht()
    float    soundSpeedMps; // Schallgeschwindigkeit in m/s bei SET DHT=0 bzw.
                            // ohne gueltige Messung (SET SOUNDSPEED, Default
                            // 342.5, seit Rev 4.15.0 mit Nachkommastelle, rein manueller
                            // Parameter, NICHT von CAL START mitkalibriert)
                            // - siehe applySoundSpeed()
    float    paperFeedMm;  // Papiervorschub je Schuss in mm (SET PAPERFEED,
                            // 0.0-150.0, Default 50.0)
    float    paperSpeedMmS; // Vorschub-Geschwindigkeit in mm/s, gilt fuer
                            // die erste Haelfte der Strecke (SET PAPERSPEED,
                            // 0.5-30.0, Default 5.0) - siehe
                            // servicePaperStepper() fuer das Abbrems-Profil
                            // in der zweiten Haelfte
    bool     paperAuto;    // Automatischen Vorschub nach einer Ausloesung
                            // ueberhaupt durchfuehren? (SET PAPERAUTO,
                            // Default 1/an) - 0 z.B. fuer Testschuesse/
                            // Kalibrierung ohne Papierverbrauch
    uint8_t  paperTrigger; // Wann der automatische Vorschub ausloest
                            // (SET PAPERTRIGGER=ANY|PIEZO|CLEAN, Default
                            // PIEZO) - siehe PAPER_TRIG_*-Defines oben
    bool     paperDirInvert; // Vorschub-Drehrichtung invertieren (SET
                            // PAPERDIR=0|1, Default 1) - gleicht eine beim
                            // Anschluss vertauschte Motorwicklung/Kabel-
                            // Reihenfolge aus, ohne die Hardware zu aendern.
                            // Default 1 (invertiert), da die unveraenderte
                            // DIR-Polaritaet (0) beim TMC2209 in Testmode-
                            // Messungen rueckwaerts statt vorwaerts lief.
    float    paperJogSpeedMmS; // Geschwindigkeit fuer den manuellen Dauer-
                            // betrieb (Einfaedeln) in mm/s (SET
                            // PAPERJOGSPEED, PAPER_JOG_SPEED_MIN_MMPS-
                            // PAPER_JOG_SPEED_MAX_MMPS, Default 75.0 =
                            // 5x der urspruenglichen fest einkompilierten
                            // 15.0 mm/s - war fuers Einfaedeln zu langsam)
    // Netzwerk: statische IP (staticIP=false → DHCP)
    bool     staticIP;
    String   ip;
    String   gateway;
    String   subnet;
    String   dns;           // "" → Gateway als DNS verwenden
    String   pskHex;        // TCP-Authentifizierung, siehe AUTH_PSK_DEFAULT_HEX
                            // (SET PSK) - "" = Signieren/Pruefen aus
};

static DeviceConfig cfg;
static Preferences  prefs;

// true, wenn beim letzten Boot ein "net_pnd"-Flag im NVS vorgefunden wurde
// (siehe armNetWatchdogIfNeeded()/serviceNetWatchdog()) - eine Netzwerk-
// Aenderung wartet dann noch auf NET CONFIRM bzw. auf den automatischen
// Ruecksprung nach NET_CONFIRM_TIMEOUT_MS.
static bool netPendingLoaded = false;

static void loadConfig()
{
    prefs.begin(NVS_NS, /*readOnly=*/true);
    netPendingLoaded = prefs.getBool("net_pnd", false);
    cfg.ssid       = prefs.getString("ssid", "");
    cfg.pass       = prefs.getString("pass", "");
    cfg.host       = prefs.getString("host", "192.168.1.10");
    cfg.port       = prefs.getUShort("port", 9000);
    cfg.lane       = prefs.getUShort("lane", 1);
    cfg.debounceMs = prefs.getUInt("debounce", 100);
    cfg.rejectLockoutMs = prefs.getUInt("reject_lock", 5);
    cfg.sendRejects = prefs.getBool("send_reject", false);
    cfg.windowMs   = prefs.getUInt("window", 1);
    cfg.debug      = prefs.getUChar("debug", 0);
    cfg.airOutlierUm = prefs.getUInt("outlier", 5000);   // Default 5.0 mm
    cfg.clusterRadiusUm = prefs.getUInt("cluster_r", 4000); // Default 4.0 mm (seit Rev 4.17.0, vorher 0.2 mm)
    cfg.minClusterHits = prefs.getUChar("min_clust", 2);
    cfg.maxPrecisionUm = prefs.getUInt("max_prec", 2000);  // Default 2.0 mm
    cfg.minMics    = prefs.getUChar("min_mics", 5);
    cfg.airMaxTdoaUs = prefs.getUInt("tdoa_us", 750);
    for (int i = 0; i < NUM_AIR; i++) {
        char key[8];
        snprintf(key, sizeof(key), "ofs%d", i);
        cfg.micOffsetNs[i] = prefs.getInt(key, 0);
        snprintf(key, sizeof(key), "mic_en%d", i);
        cfg.micEnabled[i] = prefs.getBool(key, true);
    }
    cfg.calShotCount = prefs.getUChar("cal_n", 10);
    cfg.standoffPaperMm = prefs.getFloat("standoff_pa", 28.0f);
    // Literal statt MIC_HALF_X-Makro: das Makro ist erst weiter unten im
    // File definiert (siehe dortigen Kommentar zu STANDOFFPAPER, wo
    // derselbe Reihenfolge-Fallstrick schon einmal aufgetreten ist).
    cfg.micHalfXMm = prefs.getFloat("mic_half_x", 115.0f);
    // Default 30 (Rev 4.17.0, vorher 40, urspruenglich 50): bester Wert der
    // Foto-Studie vom 12.10.2026 (server/calib_study_test.go
    // TestStudyBulletShift). Gilt nur als Fallback fuer ein frisch
    // geflashtes/nie konfiguriertes Geraet - ein bereits per SET BSHIFTPCT
    // konfiguriertes Geraet behaelt seinen eingestellten Wert.
    cfg.bulletShiftPct = prefs.getUChar("bshift_pct", 30);
    cfg.bulletShiftCapMm = prefs.getFloat("bshift_cap", 3.0f);
    cfg.clusterCorr  = prefs.getBool("clu_corr", true);
    cfg.clusterIter  = prefs.getUChar("clu_iter", 5);
    cfg.lsq          = prefs.getBool("lsq", false);
    cfg.lsqPelletRMm = prefs.getFloat("lsq_pr", 2.25f);
    cfg.algoMode   = prefs.getUChar("algo", ALGO_CLASSIC);
    cfg.pelletRadiusMm = prefs.getFloat("pellet_r", 2.25f);
    cfg.maxSigmaMm = prefs.getFloat("max_sigma", 2.0f);
    cfg.airMcpwm   = prefs.getBool("air_mcpwm", false);
    cfg.walkK      = prefs.getFloat("walk_k", 0.0f);
    cfg.walkCal    = prefs.getBool("walk_cal", false);
    cfg.walkMaxUs  = prefs.getUInt("walk_max", 150);
    cfg.usePiezo   = prefs.getBool("use_piezo", true);
    cfg.piezoRequired = prefs.getBool("piezo_req", true);
    cfg.piezoMinUs = prefs.getUInt("piezo_min", 100);
    cfg.piezoMaxUs = prefs.getUInt("piezo_max", 1400);
    cfg.testCooldownMs = prefs.getUInt("test_cd_ms", 3000);
    cfg.offsetXUm = prefs.getInt("ofs_x_um", 0);
    cfg.offsetYUm = prefs.getInt("ofs_y_um", 0);
    // Rev 4.15.0: Wert jetzt float unter neuem Key "sound_f". Ein bereits
    // gespeicherter ganzzahliger Wert (alter Key "sound_mps") wird
    // uebernommen - der neue Default 342.5 greift nur bei leerem NVS.
    if (prefs.isKey("sound_f"))        cfg.soundSpeedMps = prefs.getFloat("sound_f", 342.5f);
    else if (prefs.isKey("sound_mps")) cfg.soundSpeedMps = (float)prefs.getUShort("sound_mps", 343);
    else                               cfg.soundSpeedMps = 342.5f;
    cfg.useDht = prefs.getBool("dht_en", false);
    cfg.rawLog = prefs.getBool("raw_log", false);
    cfg.zonesOn  = prefs.getBool("z_on", false);
    cfg.zHoleUm  = prefs.getUInt("z_hole", 0);
    cfg.zBandUm  = prefs.getUInt("z_band", 45000);
    cfg.zBlendUm = prefs.getUInt("z_blend", 2250);
    for (int z = 0; z < ZONE_COUNT; z++) {
        char key[8];
        snprintf(key, sizeof(key), "z_set%d", z);
        memset(&cfg.zone[z], 0, sizeof(ZoneCal));
        if (prefs.getBytesLength(key) == sizeof(ZoneCal)) {
            prefs.getBytes(key, &cfg.zone[z], sizeof(ZoneCal));
        }
    }
    cfg.paperFeedMm   = prefs.getFloat("paper_mm", 50.0f);
    cfg.paperSpeedMmS = prefs.getFloat("paper_mmps", 5.0f);
    cfg.paperAuto     = prefs.getBool("paper_auto", true);
    cfg.paperTrigger  = prefs.getUChar("paper_trig", PAPER_TRIG_PIEZO);
    cfg.paperDirInvert = prefs.getBool("paper_dir", true);
    cfg.paperJogSpeedMmS = prefs.getFloat("paper_jog", 75.0f);
    cfg.staticIP   = prefs.getBool("static_ip", false);
    cfg.ip         = prefs.getString("ip", "");
    cfg.gateway    = prefs.getString("gateway", "");
    cfg.subnet     = prefs.getString("subnet", "255.255.255.0");
    cfg.dns        = prefs.getString("dns", "");
    cfg.pskHex     = prefs.getString("psk", AUTH_PSK_DEFAULT_HEX);
    prefs.end();
    applyPskHex(cfg.pskHex);
}

// Einzelnen Wert persistieren (oeffnet kurz schreibend)
template <typename T>
static void saveVal(const char *key, T value);

template <> void saveVal<String>(const char *key, String v)
{ prefs.begin(NVS_NS, false); prefs.putString(key, v); prefs.end(); }
template <> void saveVal<uint16_t>(const char *key, uint16_t v)
{ prefs.begin(NVS_NS, false); prefs.putUShort(key, v); prefs.end(); }
template <> void saveVal<uint32_t>(const char *key, uint32_t v)
{ prefs.begin(NVS_NS, false); prefs.putUInt(key, v); prefs.end(); }
template <> void saveVal<bool>(const char *key, bool v)
{ prefs.begin(NVS_NS, false); prefs.putBool(key, v); prefs.end(); }
template <> void saveVal<uint8_t>(const char *key, uint8_t v)
{ prefs.begin(NVS_NS, false); prefs.putUChar(key, v); prefs.end(); }
template <> void saveVal<int32_t>(const char *key, int32_t v)
{ prefs.begin(NVS_NS, false); prefs.putInt(key, v); prefs.end(); }
template <> void saveVal<float>(const char *key, float v)
{ prefs.begin(NVS_NS, false); prefs.putFloat(key, v); prefs.end(); }

// Zonen-Satz z (Rev 4.17.0) als Block persistieren
static void saveZoneSet(int z)
{
    char key[8];
    snprintf(key, sizeof(key), "z_set%d", z);
    prefs.begin(NVS_NS, false);
    prefs.putBytes(key, &cfg.zone[z], sizeof(ZoneCal));
    prefs.end();
}

// ---------------------------------------------------------------------------
// TCP-Authentifizierung (HMAC-SHA256, siehe AUTH_PSK_DEFAULT_HEX oben) -
// Wire-Format: "<16-Hex-Zeichen-Tag> <Zeile>\n". Der Tag sind die ersten
// 8 Byte (64 Bit) von HMAC-SHA256(psk, Zeile) - fuer dieses Bedrohungsmodell
// (LAN-Angreifer, kein staatlicher Akteur) ausreichend und haelt den
// Overhead klein (die eingehende Kommandozeile ist auf AUTH_CMD_BUF_LEN
// begrenzt, siehe pollCommands()).
// ---------------------------------------------------------------------------
#define AUTH_TAG_HEX_LEN  16   // 8 Byte Tag, hex-kodiert
static uint8_t authPskBytes[32];
static size_t  authPskLen = 0;   // 0 = kein Schluessel konfiguriert -> Feature aus

static int hexNibble(char c)
{
    if (c >= '0' && c <= '9') return c - '0';
    if (c >= 'a' && c <= 'f') return c - 'a' + 10;
    if (c >= 'A' && c <= 'F') return c - 'A' + 10;
    return -1;
}

// applyPskHex dekodiert einen Hex-String in authPskBytes/authPskLen - bei
// ungueltiger Laenge/ungueltigen Zeichen wird der Schluessel deaktiviert
// (authPskLen=0), damit ein Tippfehler nicht zu einem stillen Sicherheits-
// versagen (z.B. falscher, aber "gueltiger" Schluessel) fuehrt, sondern das
// Geraet erkennbar unauthentifiziert bleibt (SHOWNET/STATUS zeigen das nicht
// direkt an, aber jede TCP-Zeile wird dann unsigniert - fuer den Rollout,
// siehe Dateikopf, ist das der bewusst gewaehlte sichere Fallback).
static void applyPskHex(const String &hex)
{
    authPskLen = 0;
    if (hex.length() != sizeof(authPskBytes) * 2) return;
    for (size_t i = 0; i < sizeof(authPskBytes); i++) {
        int hi = hexNibble(hex[2 * i]);
        int lo = hexNibble(hex[2 * i + 1]);
        if (hi < 0 || lo < 0) return;
        authPskBytes[i] = (uint8_t)((hi << 4) | lo);
    }
    authPskLen = sizeof(authPskBytes);
}

// computeAuthTag berechnet den Tag ueber [data,len) und schreibt ihn als
// AUTH_TAG_HEX_LEN+1 Hex-Zeichen (inkl. Nullterminator) nach out.
static void computeAuthTag(const uint8_t *data, size_t len, char *out)
{
    uint8_t full[32];   // SHA256-Ausgabe, wir nutzen nur die ersten 8 Byte
    mbedtls_md_context_t ctx;
    mbedtls_md_init(&ctx);
    mbedtls_md_setup(&ctx, mbedtls_md_info_from_type(MBEDTLS_MD_SHA256), 1 /*hmac*/);
    mbedtls_md_hmac_starts(&ctx, authPskBytes, authPskLen);
    mbedtls_md_hmac_update(&ctx, data, len);
    mbedtls_md_hmac_finish(&ctx, full);
    mbedtls_md_free(&ctx);
    static const char hexDigits[] = "0123456789abcdef";
    for (int i = 0; i < AUTH_TAG_HEX_LEN / 2; i++) {
        out[2 * i]     = hexDigits[full[i] >> 4];
        out[2 * i + 1] = hexDigits[full[i] & 0x0F];
    }
    out[AUTH_TAG_HEX_LEN] = '\0';
}

// verifyIncomingIfTcp prueft (nur wenn viaTcp und ein Schluessel konfiguriert
// ist) das Tag-Praefix einer eingehenden Zeile und liefert bei Erfolg true
// mit auf die Nutzlast gekuerztem line zurueck. Ohne konfigurierten
// Schluessel oder ueber Serial: immer true, line unveraendert (siehe
// Dateikopf zu AUTH_PSK_DEFAULT_HEX).
static bool verifyIncomingIfTcp(String &line, bool viaTcp)
{
    if (!viaTcp || authPskLen == 0) return true;
    if (line.length() <= AUTH_TAG_HEX_LEN || line[AUTH_TAG_HEX_LEN] != ' ') return false;
    String tag     = line.substring(0, AUTH_TAG_HEX_LEN);
    String payload = line.substring(AUTH_TAG_HEX_LEN + 1);
    char want[AUTH_TAG_HEX_LEN + 1];
    computeAuthTag((const uint8_t *)payload.c_str(), payload.length(), want);
    // Konstant-Zeit-Vergleich (Timing-Seitenkanal) statt tag.equals(want).
    uint8_t diff = 0;
    for (int i = 0; i < AUTH_TAG_HEX_LEN; i++) diff |= (uint8_t)(tag[i] ^ want[i]);
    if (diff != 0) return false;
    line = payload;
    return true;
}

// ---------------------------------------------------------------------------
// Geraete-ID (Basis-MAC) - siehe Rev-4.9.0-Hinweis oben
// ---------------------------------------------------------------------------
// Per esp_read_mac() direkt aus dem Werks-Efuse gelesen statt ueber
// WiFi.macAddress() - dadurch unabhaengig davon, ob/wann der WLAN-Treiber
// initialisiert wird (bei leerer SET SSID bleibt WiFi.mode() unaufgerufen).
// In status/config/confignet/cal-Telegrammen als "mac" enthalten, damit der
// Stand-PC Konfiguration/Kalibrierung einem physischen Geraet zuordnen kann,
// unabhaengig von der (frei vergebbaren) SET LANE-Bahnnummer.
static char deviceMac[18] = "00:00:00:00:00:00";

static void initDeviceMac()
{
    uint8_t mac[6];
    esp_read_mac(mac, ESP_MAC_WIFI_STA);
    snprintf(deviceMac, sizeof(deviceMac), "%02X:%02X:%02X:%02X:%02X:%02X",
             mac[0], mac[1], mac[2], mac[3], mac[4], mac[5]);
}

// ---------------------------------------------------------------------------
// Sicherheitsnetz fuer Netzwerk-Fernkonfiguration (siehe Rev-4.9.0-Hinweis
// oben, Konzept docs/remote-interaktion-konzept.md Abschnitt 5.2)
// ---------------------------------------------------------------------------
// netSnapshotArmedThisBoot verhindert, dass mehrere SET SSID/HOST/...-Aufrufe
// innerhalb desselben (noch unbestaetigten) Boot-Zyklus die "*_prv"-Werte
// wiederholt ueberschreiben - sie sollen immer die zuletzt BESTAETIGTE
// Konfiguration festhalten, nicht eine zwischenzeitliche, selbst noch nicht
// bestaetigte Aenderung. Wird in setup() anhand von netPendingLoaded
// vorbelegt (siehe dort) und bei NET CONFIRM wieder freigegeben.
static bool     netSnapshotArmedThisBoot = false;
static bool     netWatchdogArmed         = false;
static uint32_t netWatchdogDeadlineMs    = 0;

// Sichert die AKTUELL GUELTIGEN Netzwerkwerte als Ruecksprungpunkt, bevor der
// erste Netzwerk-SET-Befehl dieses Boot-Zyklus sie ueberschreibt. Muss vor
// dem Schreiben des neuen Werts aufgerufen werden (siehe Aufrufstellen in
// handleSet()).
static void armNetWatchdogIfNeeded()
{
    if (netSnapshotArmedThisBoot) return;
    prefs.begin(NVS_NS, false);
    prefs.putString("ssid_prv", cfg.ssid);
    prefs.putString("pass_prv", cfg.pass);
    prefs.putString("host_prv", cfg.host);
    prefs.putUShort("port_prv", cfg.port);
    prefs.putBool("static_prv", cfg.staticIP);
    prefs.putString("ip_prv", cfg.ip);
    prefs.putString("gw_prv", cfg.gateway);
    prefs.putString("subnet_prv", cfg.subnet);
    prefs.putString("dns_prv", cfg.dns);
    prefs.putBool("net_pnd", true);
    prefs.end();
    netSnapshotArmedThisBoot = true;
    netPendingLoaded         = true;   // fuer sendShowNetConfig() diesen Boot bereits sichtbar
}

// ---------------------------------------------------------------------------
// Schusserfassung – Luftschall-Mikrofone (Multi-Edge-Capture)
// ---------------------------------------------------------------------------

// Grobzeit (esp_timer) fuer Fensterlogik und Sperrzeit
static volatile uint64_t firstHitTimeUs = 0;
static volatile bool     shotInProgress = false;
static volatile uint64_t lockoutUntil = 0;

static uint32_t shotCounter = 0;
static uint32_t sequenceNo  = 0;

// TESTSHOOT (siehe handleTestShoot()/processShot()): markiert den NAECHSTEN
// von processShot() ausgewerteten Treffer als synthetisch erzeugt - fuehrt
// zu einem zusaetzlichen "synthetic":1-Feld im shot/reject-Telegramm, damit
// der Stand-PC einen Testschuss (z.B. um ihn NICHT in eine echte Wertung zu
// uebernehmen) von einem echten Treffer unterscheiden kann. Wird von
// processShot() sofort nach dem Lesen wieder auf false gesetzt.
static bool syntheticShotPending = false;

static uint32_t cpuMHz      = 240;   // wird in setup() ermittelt

// DHT22-Messung und aktive Schallgeschwindigkeit (Rev 4.15.0, siehe
// serviceDht()/applySoundSpeed()).
static bool     dhtValid     = false;   // gefilterter Messwert verfuegbar
static float    dhtTempC     = 0.0f;    // Median der letzten 3 gueltigen Messungen
static float    dhtRhPct     = 0.0f;
static uint32_t dhtLastOkMs  = 0;
static uint32_t dhtErrors    = 0;       // Pruefsumme/Timeout/unplausibel seit Boot
static float    dhtHistT[3], dhtHistH[3];
static uint8_t  dhtHistN = 0, dhtHistPos = 0;
static float    soundMpsActive = 342.5f; // tatsaechlich verwendeter Wert in m/s
static bool     soundFromDht   = false;  // true: aus DHT22, false: SET SOUNDSPEED

// Timing-Offset-Kalibrierung (siehe CAL START / runCalibration() weiter
// unten): sammelt die naechsten cfg.calShotCount gueltigen Schuesse mit
// ihren ROHEN (unkorrigierten) Erst-Flankenzeiten, danach wird daraus per
// Koordinatenabstieg ein Timing-Offset je Mikrofon bestimmt.
#define MAX_CAL_SHOTS 20
static bool    calActive    = false;
static uint8_t calCollected = 0;
static int64_t calRawNs[MAX_CAL_SHOTS][NUM_AIR];
static bool    calSeenBuf[MAX_CAL_SHOTS][NUM_AIR];

// RIM-Kalibrierungsdaten (SET ALGO=RIM, siehe calCostRim()/processShot
// Anchored()): analog zu calRawNs/calSeenBuf oben, aber je Mikrofon MEHRERE
// ROHE (nicht offset-korrigierte) Kandidatenflanken relativ zum Piezo -
// runCalibration() probiert beim Koordinatenabstieg verschiedene Offsets
// aus, calCostRim() zieht sie hier erst ab und ruft dann shotloc::locate().
static uint8_t calEdgeCountRim[MAX_CAL_SHOTS][NUM_AIR];
static float   calEdgeNsRim[MAX_CAL_SHOTS][NUM_AIR][shotloc::MAX_EDGES];
static bool    calEnabledRim[MAX_CAL_SHOTS][NUM_AIR];

// Pulsbreiten (ns, -1 = keine) zu den Kalibrier-Flanken (Rev 4.14.0), damit
// runCalibration() SET WALKK mitbestimmen kann (SET WALKCAL=1): CLASSIC je
// Mic die erste Flanke, RIM je Kandidatenflanke.
static int32_t calWNs[MAX_CAL_SHOTS][NUM_AIR];
static int32_t calEdgeWRim[MAX_CAL_SHOTS][NUM_AIR][shotloc::MAX_EDGES];

// Multi-Edge-Capture der Luftkanaele
static volatile uint32_t airCC[NUM_AIR][AIR_MAX_EDGES];
static volatile uint8_t  airCount[NUM_AIR] = {0};
static volatile uint32_t airLastCC[NUM_AIR] = {0};
static volatile uint32_t firstAirCC = 0;   // CPU-Zyklen, Fenster-Nullpunkt
                                            // (erste Mikrofon-Flanke)

// ---------------------------------------------------------------------------
// Ringpuffer-Erfassung fuer SET ALGO=RIM (siehe processShotAnchored() weiter
// unten sowie schusserkennung-rev5.md). Im Gegensatz zum klassischen Pfad
// oben (fest, ab der ERSTEN Flanke bis cfg.airMaxTdoaUs) zeichnet airISR()
// hier JEDE Flanke laufend und OHNE Fenster-/Sperrzeit-Bezug auf - dadurch
// geht bei ALGO=RIM auch eine durch einen Muendungsknall verzoegerte echte
// Einschlagflanke nicht mehr verloren. Der Ringpuffer laeuft UNABHAENGIG vom
// gewaehlten SET ALGO immer mit (vernachlaessigbarer ISR-Mehraufwand, siehe
// airISR()) - bei ALGO=CLASSIC bleibt er schlicht ungenutzt, dadurch ist ein
// SET ALGO=RIM jederzeit ohne Reboot wirksam. Ausgewertet (und dabei erst
// vor Ueberschreiben geschuetzt, siehe freezeActive/freezeCC) wird er nur,
// wenn das Piezo bei ALGO=RIM tatsaechlich einen Anker setzt (piezoISR()).
#define EDGE_RING 32
static volatile uint32_t ringCC[NUM_AIR][EDGE_RING];
static volatile uint32_t ringUs[NUM_AIR][EDGE_RING];
static volatile uint8_t  ringHead[NUM_AIR] = {0};
static volatile bool     freezeActive = false;   // ab dem Piezo-Anker bis
                                            // processShotAnchored() gelesen
                                            // hat: nichts mehr ueberschreiben
static volatile uint32_t freezeCC     = 0;

// Fallende Flanken je Mic (Rev 4.14.0, nur bei aktivem MCPWM-Capture
// befuellt, siehe airFall()): Gegenstueck zu ringCC/ringUs fuer die Puls-
// breite = Zeit, die das Signal ueber der Komparatorschwelle bleibt. Sie
// waechst mit der Amplitude und dient als Mass fuer die amplitudenabhaengige
// Ausloeseverzoegerung (leises Signal ueberschreitet die Schwelle spaeter),
// siehe edgeWidthNs()/walkDeltas(). fallUs = 0: Slot nie beschrieben.
#define FALL_RING 32
static volatile uint32_t fallCC[NUM_AIR][FALL_RING];
static volatile uint32_t fallUs[NUM_AIR][FALL_RING];
static volatile uint8_t  fallHead[NUM_AIR] = {0};

// Piezo-Anker (SET ALGO=RIM, siehe piezoISR()): bewusst eigene Variablen statt
// piezoCC/piezoSeen (klassischer Pfad, unveraendert) - beide Pfade duerfen
// sich nicht gegenseitig beeinflussen, da nur SET ALGO entscheidet, welcher
// gerade aktiv ist.
static volatile bool     piezoPending  = false;
static volatile uint32_t piezoAnchorCC = 0;   // Zykluszaehler-Zeitstempel des Piezo
static volatile uint64_t piezoAnchorUs = 0;   // esp_timer_get_time() des Piezo -
                                            // steuert die postWaitUs-Wartezeit in loop()
static uint32_t          postWaitUs    = 1000; // siehe updatePostWaitUs()
static float              rimMaxDistMm  = 320.0f; // siehe updateRimMaxDist()

// ---------------------------------------------------------------------------
// Reiner Sensor-Testmodus (SET TESTMODE) - Kommissionierung/Hardware-Test:
// jede Sensor-Flanke (Luft-Mic ODER Piezo) erzeugt sofort eine Klartext-
// Zeile mit der Sensor-Bezeichnung, unabhaengig von Schuss-/TDOA-Logik.
// Bewusst NICHT in NVS persistiert (kein saveVal in SET TESTMODE) - nach
// jedem Reboot ist der Testmodus immer wieder aus.
// ---------------------------------------------------------------------------
// Cooldown ist per SET TESTCOOLDOWN einstellbar (cfg.testCooldownMs, Default
// 3000ms) - fuer reines Antippen von Hand reichen 3s, um Prellen/
// Nachschwinger auszublenden. Bei einem ECHTEN Schuss koennen an einem
// Mikrofon aber mehrere echte, zeitlich getrennte Ereignisse ankommen
// (Muendungsknall, Einschlag Papier, Einschlag Stahl) - mit 3s Cooldown
// wuerde nur das jeweils erste je Sensor sichtbar, alle spaeteren werden
// unterdrueckt. Fuer die Analyse echter Schuesse SET TESTCOOLDOWN deutlich
// kleiner stellen (z.B. 50ms), um alle Ereignisse je Sensor zu sehen.
#define TEST_IDLE_US             5000000ULL   // Trennlinie nach 5s Stille
static bool testMode = false;
static volatile bool     testFired[NUM_AIR + 1]   = {false};  // Index NUM_AIR = Piezo
static volatile uint64_t testLastFireUs[NUM_AIR + 1] = {0};
static uint64_t testLastActivityUs = 0;   // fuer 5s-Trennlinie, nur in loop()
static bool     testSeparatorShown = false; // nur 1x Trennlinie je Stille-Periode
static uint64_t testSeriesStartUs  = 0;   // Zeitpunkt des 1. Sensors nach der
                                           // letzten Trennlinie (fuer +ms-Anzeige)
static bool     testSeriesActive   = false;
// Alle frueheren Hardware-Diagnose-Verkabelungstausche (Rev 4.4.6-4.4.9,
// 4.8.1, siehe Header-Kommentar ganz oben) sind zurueckgebaut - Namen
// entsprechen wieder 1:1 der Normalverkabelung (AIR_PINS[]/MIC_X/MIC_Y).
static const char *TEST_SENSOR_NAMES[NUM_AIR + 1] = {
    "AIR0 links-unten", "AIR1 rechts-unten", "AIR2 links-oben",
    "AIR3 rechts-oben",  "AIR4 links-mitte",  "AIR5 rechts-mitte",
    "PIEZO stahlplatte",
};

// Gemeinsame Testmodus-Behandlung fuer Luft-Mics und Piezo: Cooldown pruefen/
// merken und Meldeflag setzen, komplett unabhaengig von Schuss-/TDOA-Zustand.
static inline void IRAM_ATTR testModeHit(uint8_t sensorIdx, uint64_t nowUs)
{
    const uint64_t cooldownUs = (uint64_t)cfg.testCooldownMs * 1000ULL;
    if (testLastFireUs[sensorIdx] != 0
        && (nowUs - testLastFireUs[sensorIdx]) < cooldownUs) {
        return;
    }
    testLastFireUs[sensorIdx] = nowUs;
    testFired[sensorIdx]      = true;
}

// Zeitpunkt (esp_timer, us, auf 32 Bit gekuerzt) der letzten Luft-Mic-Flanke -
// serviceCaptureCheck() prueft die MCPWM-Zeitbasis nur in ruhigen Momenten.
static volatile uint32_t lastAirEdgeUs = 0;

// Gemeinsame Verarbeitung EINER Luft-Mic-Flanke (Rev 4.13.0 aus airISR()
// herausgeloest): cc ist der Flankenzeitpunkt in CPU-Zyklen - bei SET
// CAPTURE=ISR der beim ISR-Eintritt gelesene Zykluszaehler, bei SET
// CAPTURE=MCPWM der in Zyklen umgerechnete Hardware-Capture-Stempel (siehe
// airCapCallback()). Ab hier ist beides identisch.
static void IRAM_ATTR airEdge(uint32_t idx, uint32_t cc, uint64_t nowUs)
{
    lastAirEdgeUs = (uint32_t)nowUs;
    if (testMode) { testModeHit((uint8_t)idx, nowUs); return; }

    // --- Ringpuffer (SET ALGO=RIM, siehe Deklaration oben) ----------------
    // Laeuft IMMER mit, unabhaengig von SET ALGO und WAEHREND der Sperrzeit/
    // Fenstergrenzen des klassischen Pfads unten - genau das ist der Zweck:
    // eine durch einen Muendungsknall-Fehlausloeser verzoegerte echte
    // Einschlagflanke geht hier nicht verloren. freezeActive/freezeCC werden
    // ausschliesslich von piezoISR() (nur bei ALGO=RIM) gesetzt, bei
    // ALGO=CLASSIC bleibt freezeActive dauerhaft false.
    if (!freezeActive || (int32_t)(cc - freezeCC) <= 0) {
        const uint8_t rh    = ringHead[idx];
        const uint8_t rPrev = (rh + EDGE_RING - 1) % EDGE_RING;
        // Totzeit 20us, analog zur klassischen Erfassung unten.
        if ((uint32_t)(cc - ringCC[idx][rPrev]) >= 20U * cpuMHz) {
            ringCC[idx][rh] = cc;
            ringUs[idx][rh] = (uint32_t)nowUs;
            ringHead[idx]   = (rh + 1) % EDGE_RING;
        }
    }

    // --- Klassischer Pfad (SET ALGO=CLASSIC, Default) - unveraendert ------
    // Die erste Mikrofon-Flanke oeffnet das Sammelfenster selbst.
    if (nowUs < lockoutUntil) return;
    if (!shotInProgress) {
        shotInProgress = true;
        firstAirCC     = cc;
        firstHitTimeUs = nowUs;
    } else if ((int32_t)(cc - firstAirCC) < 0) {
        // Nur bei SET CAPTURE=MCPWM moeglich: AIR0-2 und AIR3-5 melden sich
        // ueber ZWEI verschiedene Interrupts (je MCPWM-Gruppe einen), eine
        // fruehere Flanke kann daher NACH einer spaeteren hier ankommen. Der
        // Nullpunkt muss die frueheste Flanke sein - processShot() rechnet
        // (airCC - firstAirCC) vorzeichenlos. Im ISR-Betrieb ist cc immer
        // monoton steigend, dieser Zweig greift dort nie.
        firstAirCC = cc;
    }

    // Geometrie-Plausibilitaet: > cfg.airMaxTdoaUs nach dem schnellsten Mic
    // kann physikalisch kein Direktschall desselben Einschlags mehr sein.
    uint32_t dSinceFirst = cc - firstAirCC;          // wrap-sicher
    if (dSinceFirst > cfg.airMaxTdoaUs * cpuMHz) return;

    uint8_t n = airCount[idx];
    if (n >= AIR_MAX_EDGES) return;

    // Totzeit 20 µs zwischen Flanken desselben Kanals: blendet das
    // Eigenschwingen der Piezo-Resonanz (~4,4 kHz) nach jeder Flanke aus
    if (n > 0) {
        uint32_t dCC = cc - airLastCC[idx];          // wrap-sicher
        if (dCC < 20U * cpuMHz) return;
    }
    airCC[idx][n]   = cc;
    airLastCC[idx]  = cc;
    airCount[idx]   = n + 1;
}

// Fallende Flanke eines Luft-Mics (nur MCPWM, siehe airCapCallback()). Prellt
// der Komparator an der Schwelle (mehrere Flanken binnen 2us), wird nur die
// jeweils letzte behalten, damit der Ring nicht mit Prellern volllaeuft.
static void IRAM_ATTR airFall(uint32_t idx, uint32_t cc, uint32_t nowUs32)
{
    if (testMode) return;
    if (freezeActive && (int32_t)(cc - freezeCC) > 0) return;   // siehe airEdge()
    const uint8_t h    = fallHead[idx];
    const uint8_t prev = (h + FALL_RING - 1) % FALL_RING;
    if (nowUs32 == 0) nowUs32 = 1;                    // 0 = "nie beschrieben"
    if (fallUs[idx][prev] != 0 && (uint32_t)(cc - fallCC[idx][prev]) < 2U * cpuMHz) {
        fallCC[idx][prev] = cc;
        fallUs[idx][prev] = nowUs32;
        return;
    }
    fallCC[idx][h] = cc;
    fallUs[idx][h] = nowUs32;
    fallHead[idx]  = (h + 1) % FALL_RING;
}

// SET CAPTURE=ISR (Default): GPIO-Interrupt je Luft-Mic.
void IRAM_ATTR airISR(void *arg)
{
    // Rev 4.11.0: Zykluszaehler jetzt als ALLERERSTE Anweisung (vorher nach
    // esp_timer_get_time(), das selbst Jitter kostet) - siehe Header-Hinweis
    // Abschnitt 4 in schusserkennung-rev5.md. Wirkt auf BEIDE Auswertepfade.
    const uint32_t cc = esp_cpu_get_cycle_count();
    airEdge((uint32_t)arg, cc, (uint64_t)esp_timer_get_time());
}

// ---------------------------------------------------------------------------
// SET CAPTURE=MCPWM (Rev 4.13.0): Hardware-Capture der Luft-Mics
// ---------------------------------------------------------------------------
// Je Mic ein MCPWM-Capture-Kanal (AIR0-2 an Gruppe 0, AIR3-5 an Gruppe 1).
// Die Hardware friert bei der steigenden Flanke den Stand des Capture-Timers
// (APB-Takt, 80 MHz) ein - unabhaengig davon, wann der Callback dann laeuft.
//
// Umrechnung in die gemeinsame Zeitbasis (CPU-Zykluszaehler): CPU- und
// APB-Takt kommen aus derselben PLL, das Verhaeltnis ist ganzzahlig (240/80
// = 3). Damit gilt fuer die (unendlich weiterlaufend gedachten) Zaehler
// CC = 3*CAP + C. Modulo 2^32 bleibt das exakt, auch ueber beliebig viele
// Ueberlaeufe beider Zaehler hinweg (Multiplikation mit ganzer Zahl ist mit
// der Restklassenbildung vertraeglich) - EIN Anker (capAnchorCap/
// capAnchorCC) beim Boot reicht dauerhaft. Fehler im Anker (Latenz des
// Soft-Catch-Callbacks, ~us) verschieben ALLE Mics gleich und damit nur
// das Verhaeltnis Mics<->Piezo (Gate mit >=100us Spielraum), nicht die
// fuer die Position allein relevanten Mic-Differenzen.
//
// Alle Callbacks laufen auf dem Core, auf dem setup() die Kanaele anlegt -
// derselbe Core wie piezoISR()/loop(), also derselbe Zykluszaehler.
static bool                       capActive   = false;  // MCPWM liefert Mic-Flanken
static const char                *capErr      = "";     // Grund fuer ISR-Fallback
static mcpwm_cap_timer_handle_t   capTimer[2] = {nullptr, nullptr};
static mcpwm_sync_handle_t        capSync[2]  = {nullptr, nullptr};
static mcpwm_cap_channel_handle_t capChan[NUM_AIR] = {nullptr};
static uint32_t                   capRatio    = 3;      // CPU-Zyklen je Capture-Tick

static volatile uint32_t capAnchorCap = 0;   // Capture-Stand ...
static volatile uint32_t capAnchorCC  = 0;   // ... und zugehoeriger Zykluszaehler

// Soft-Catch (Anker setzen/pruefen, siehe capSoftCatch()): der naechste
// Callback auf Kanal capSoftIdx ist KEINE Mic-Flanke.
static volatile bool     capSoftPending = false;
static volatile bool     capSoftDone    = false;
static volatile uint8_t  capSoftIdx     = 0;
static volatile uint32_t capSoftCap     = 0;
static volatile uint32_t capSoftCC      = 0;

// Diagnose (STATUS): Gruppen-Sync-Pruefung beim Boot und letzte
// Gegenpruefung der Zeitbasis in ns, Anzahl der Neu-Verankerungen.
static int32_t  capSyncCheckNs = 0;
static int32_t  capCheckNs     = 0;
static uint32_t capReanchors   = 0;

static bool IRAM_ATTR airCapCallback(mcpwm_cap_channel_handle_t chan,
                                     const mcpwm_capture_event_data_t *edata,
                                     void *user_ctx)
{
    (void)chan;
    const uint32_t ccNow = esp_cpu_get_cycle_count();
    const uint32_t idx   = (uint32_t)user_ctx;
    const uint32_t cap   = edata->cap_value;

    if (capSoftPending && idx == capSoftIdx) {
        capSoftCap     = cap;
        capSoftCC      = ccNow;
        capSoftPending = false;
        capSoftDone    = true;
        return false;
    }
    if (!capActive) return false;   // Init laeuft noch bzw. ISR-Fallback

    const uint32_t cc = capAnchorCC + (cap - capAnchorCap) * capRatio;   // mod 2^32 exakt
    if (edata->cap_edge == MCPWM_CAP_EDGE_NEG) {      // Pulsende (Rev 4.14.0)
        airFall(idx, cc, (uint32_t)esp_timer_get_time());
        return false;
    }
    airEdge(idx, cc, (uint64_t)esp_timer_get_time());
    return false;
}

// Loest auf Kanal idx einen Software-Capture aus und liefert den dabei
// eingefrorenen Capture-Stand samt Zykluszaehler im Callback. Kurzes
// Busy-Wait (typ. wenige us, max. 2ms).
static bool capSoftCatch(uint8_t idx, uint32_t *outCap, uint32_t *outCC)
{
    capSoftIdx     = idx;
    capSoftDone    = false;
    capSoftPending = true;
    if (mcpwm_capture_channel_trigger_soft_catch(capChan[idx]) != ESP_OK) {
        capSoftPending = false;
        return false;
    }
    const uint32_t t0 = micros();
    while (!capSoftDone) {
        if (micros() - t0 > 2000) { capSoftPending = false; return false; }
    }
    *outCap = capSoftCap;
    *outCC  = capSoftCC;
    return true;
}

// Erwarteter Zykluszaehler zu einem Capture-Stand laut aktuellem Anker
static inline uint32_t capToCC(uint32_t cap)
{
    return capAnchorCC + (cap - capAnchorCap) * capRatio;
}

// Richtet die 6 Capture-Kanaele ein. Bei false steht in capErr der Grund,
// setup() faellt dann auf die GPIO-ISRs zurueck.
static bool airCaptureInit()
{
#if !SOC_MCPWM_SUPPORTED
    capErr = "MCPWM nicht vorhanden";
    return false;
#else
    const int chPerTimer = SOC_MCPWM_CAPTURE_CHANNELS_PER_TIMER;   // ESP32: 3
    if ((NUM_AIR + chPerTimer - 1) / chPerTimer > 2) {
        capErr = "zu viele Mics fuer 2 MCPWM-Gruppen";
        return false;
    }

    // 1) Je Gruppe ein Capture-Timer, beide auf denselben Sync-GPIO gelegt
    for (int g = 0; g < 2; g++) {
        mcpwm_capture_timer_config_t tcfg = {};
        tcfg.group_id = g;
        tcfg.clk_src  = MCPWM_CAPTURE_CLK_SRC_DEFAULT;   // APB
        if (mcpwm_new_capture_timer(&tcfg, &capTimer[g]) != ESP_OK) {
            capErr = "mcpwm_new_capture_timer"; return false;
        }
        mcpwm_gpio_sync_src_config_t scfg = {};
        scfg.group_id = g;
        scfg.gpio_num = MCPWM_SYNC_PIN;
        if (mcpwm_new_gpio_sync_src(&scfg, &capSync[g]) != ESP_OK) {
            capErr = "mcpwm_new_gpio_sync_src"; return false;
        }
        mcpwm_capture_timer_sync_phase_config_t pcfg = {};
        pcfg.sync_src    = capSync[g];
        pcfg.count_value = 0;
        pcfg.direction   = MCPWM_TIMER_DIRECTION_UP;
        if (mcpwm_capture_timer_set_phase_on_sync(capTimer[g], &pcfg) != ESP_OK) {
            capErr = "mcpwm_capture_timer_set_phase_on_sync"; return false;
        }
    }

    // 2) Ein Capture-Kanal je Mic. Steigende Flanke = Ausloesung (wie bisher
    //    RISING), seit Rev 4.14.0 zusaetzlich die fallende fuer die
    //    Pulsbreite (siehe airFall()/edgeWidthNs()).
    for (int i = 0; i < NUM_AIR; i++) {
        const bool noPullup = AIR_PINS[i] >= 34 && AIR_PINS[i] <= 39;
        mcpwm_capture_channel_config_t ccfg = {};
        ccfg.gpio_num       = AIR_PINS[i];
        ccfg.prescale       = 1;
        ccfg.flags.pos_edge = true;
        ccfg.flags.neg_edge = true;
        ccfg.flags.pull_up  = noPullup ? 0 : 1;
        if (mcpwm_new_capture_channel(capTimer[i / chPerTimer], &ccfg,
                                      &capChan[i]) != ESP_OK) {
            capErr = "mcpwm_new_capture_channel"; return false;
        }
        mcpwm_capture_event_callbacks_t cbs = {};
        cbs.on_cap = airCapCallback;
        if (mcpwm_capture_channel_register_event_callbacks(capChan[i], &cbs,
                                                           (void *)i) != ESP_OK) {
            capErr = "mcpwm_capture_channel_register_event_callbacks"; return false;
        }
        if (mcpwm_capture_channel_enable(capChan[i]) != ESP_OK) {
            capErr = "mcpwm_capture_channel_enable"; return false;
        }
    }

    // Gruppe 1 startet absichtlich 200us spaeter: ohne wirksamen Sync (Schritt
    // 4) bliebe dieser Versatz stehen und die Pruefung in Schritt 5 schlaegt
    // sicher an - ein Start direkt hintereinander ergaebe nur einen kleinen,
    // unentdeckbaren Versatz, der die Mic-Differenzen zwischen den Gruppen
    // je nach Boot unterschiedlich verfaelschen wuerde.
    for (int g = 0; g < 2; g++) {
        if (g > 0) delayMicroseconds(200);
        if (mcpwm_capture_timer_enable(capTimer[g]) != ESP_OK
            || mcpwm_capture_timer_start(capTimer[g]) != ESP_OK) {
            capErr = "mcpwm_capture_timer_start"; return false;
        }
    }

    // 3) Taktverhaeltnis CPU/Capture muss ganzzahlig sein (siehe oben)
    uint32_t capHz = 0;
    if (mcpwm_capture_timer_get_resolution(capTimer[0], &capHz) != ESP_OK
        || capHz == 0) {
        capErr = "Capture-Aufloesung unbekannt"; return false;
    }
    const uint64_t cpuHz = (uint64_t)cpuMHz * 1000000ULL;
    if (cpuHz % capHz != 0) {
        capErr = "CPU-/Capture-Takt nicht ganzzahlig"; return false;
    }
    capRatio = (uint32_t)(cpuHz / capHz);

    // 4) Beide Gruppen-Timer gleichzeitig nullen: der Pin wird erst JETZT
    //    als Ausgang geschaltet (die Sync-Eingaenge beider Gruppen haengen
    //    bereits ueber die GPIO-Matrix daran) und einmal gepulst.
    gpio_set_level((gpio_num_t)MCPWM_SYNC_PIN, 0);
    gpio_set_direction((gpio_num_t)MCPWM_SYNC_PIN, GPIO_MODE_INPUT_OUTPUT);
    delayMicroseconds(5);
    gpio_set_level((gpio_num_t)MCPWM_SYNC_PIN, 1);
    delayMicroseconds(5);
    gpio_set_level((gpio_num_t)MCPWM_SYNC_PIN, 0);

    // 5) Anker setzen (Gruppe 0) und Gruppe 1 gegenpruefen: ohne wirksamen
    //    Sync laufen die Timer um die absichtlichen 200us auseinander (siehe
    //    Start oben). Schwelle 20us, da die Soft-Catch-Latenz selbst um
    //    einige us streuen kann.
    uint32_t cap0, cc0, cap1, cc1;
    if (!capSoftCatch(0, &cap0, &cc0)) { capErr = "Soft-Catch Gruppe 0"; return false; }
    capAnchorCap = cap0;
    capAnchorCC  = cc0;
    if (!capSoftCatch((uint8_t)chPerTimer, &cap1, &cc1)) {
        capErr = "Soft-Catch Gruppe 1"; return false;
    }
    const int32_t devCC = (int32_t)(cc1 - capToCC(cap1));
    capSyncCheckNs = (int32_t)((int64_t)devCC * 1000 / (int64_t)cpuMHz);
    if (capSyncCheckNs > 20000 || capSyncCheckNs < -20000) {
        capErr = "Gruppen-Sync fehlgeschlagen (Pin MCPWM_SYNC_PIN beschaltet?)";
        return false;
    }

    capActive = true;
    return true;
#endif
}

// Gibt die Capture-Kanaele wieder frei (ISR-Fallback): die Mic-Pins sollen
// danach ausschliesslich ueber attachInterruptArg() laufen.
static void airCaptureRelease()
{
    capActive = false;
    for (int i = 0; i < NUM_AIR; i++) {
        if (!capChan[i]) continue;
        mcpwm_capture_channel_disable(capChan[i]);
        mcpwm_del_capture_channel(capChan[i]);
        capChan[i] = nullptr;
    }
    for (int g = 0; g < 2; g++) {
        if (capTimer[g]) {
            mcpwm_capture_timer_stop(capTimer[g]);
            mcpwm_capture_timer_disable(capTimer[g]);
            mcpwm_del_capture_timer(capTimer[g]);
            capTimer[g] = nullptr;
        }
        if (capSync[g]) { mcpwm_del_sync_src(capSync[g]); capSync[g] = nullptr; }
    }
}

// Gegenpruefung der Zeitbasis (aus loop(), alle CAP_CHECK_MS): Soft-Catch
// auf AIR0 und Vergleich mit der Vorhersage des Ankers. Da der Anker selbst
// per Soft-Catch entstand, ist der Wert die DIFFERENZ zweier Callback-
// Latenzen - erwartet wird etwas nahe 0 (|x| typ. < 2us). Groessere Abweichung
// bedeutet, dass ein Zaehler gestanden oder gesprungen ist (z.B. geaenderter
// CPU-Takt) - dann wird neu verankert und capReanchors erhoeht. Nur in
// ruhigen Momenten (kein Schuss in Arbeit, letzte Mic-Flanke >= 200ms her),
// damit nie die Flanken EINES Schusses mit zwei verschiedenen Ankern
// umgerechnet werden. Restrisiko: eine echte AIR0-Flanke exakt in den
// wenigen us des Soft-Catch wuerde als Pruefwert verbraucht.
#define CAP_CHECK_MS       10000UL
#define CAP_REANCHOR_NS    50000L
static void serviceCaptureCheck()
{
    static uint32_t lastCheckMs = 0;
    if (!capActive) return;
    if (millis() - lastCheckMs < CAP_CHECK_MS) return;
    if (shotInProgress || piezoPending) return;
    if ((uint32_t)esp_timer_get_time() - lastAirEdgeUs < 200000UL) return;
    lastCheckMs = millis();

    uint32_t cap, cc;
    if (!capSoftCatch(0, &cap, &cc)) return;
    const int32_t devCC = (int32_t)(cc - capToCC(cap));
    capCheckNs = (int32_t)((int64_t)devCC * 1000 / (int64_t)cpuMHz);
    if (capCheckNs > CAP_REANCHOR_NS || capCheckNs < -CAP_REANCHOR_NS) {
        noInterrupts();
        capAnchorCap = cap;
        capAnchorCC  = cc;
        interrupts();
        capReanchors++;
        Serial.printf("# Capture: Zeitbasis neu verankert (Abweichung %ld ns)\n",
                      (long)capCheckNs);
    }
}

// Piezo (Koerperschall auf der Stahlplatte, siehe PIEZO_PIN) - nur EINE
// Flanke pro Schuss noetig (im Gegensatz zu den Luft-Mics kein Echo-Problem,
// da direkter Kontaktschall). Bewusst KEINE cfg.airMaxTdoaUs-Pruefung wie
// bei airISR: das Piezo loest planmaessig deutlich spaeter aus (SET
// PIEZOMIN..PIEZOMAX, Default 100..1400 us) als das Geometriefenster fuer
// die Luft-Mics erlaubt.
static volatile uint32_t piezoCC   = 0;
static volatile bool     piezoSeen = false;

void IRAM_ATTR piezoISR()
{
    const uint32_t cc = esp_cpu_get_cycle_count();   // ZUERST, siehe airISR()
    const uint64_t nowUs = (uint64_t)esp_timer_get_time();
    if (testMode) { testModeHit(NUM_AIR, nowUs); return; }

    if (cfg.algoMode == ALGO_RIM) {
        // Ringpuffer-Pfad: das Piezo ist hier der ALLEINIGE Trigger (siehe
        // schusserkennung-rev5.md Abschnitt 1) - keine shotInProgress/
        // firstAirCC-Kopplung mehr, Luft-Mics loesen keine Sperrzeit mehr
        // aus (siehe Ringpuffer-Block in airISR() oben). piezoPending
        // schuetzt zusaetzlich gegen ein erneutes Ausloesen waehrend der
        // laufenden postWaitUs-Wartezeit (z.B. Nachschwingen der Platte).
        if (piezoPending || nowUs < lockoutUntil) return;
        piezoAnchorCC = cc;
        piezoAnchorUs = nowUs;
        freezeCC      = cc + postWaitUs * cpuMHz;
        freezeActive  = true;
        piezoPending  = true;
        return;
    }

    // --- Klassischer Pfad (SET ALGO=CLASSIC, Default) - unveraendert ------
    if (nowUs < lockoutUntil) return;
    if (!shotInProgress) {
        shotInProgress = true;
        firstAirCC     = cc;
        firstHitTimeUs = nowUs;
    }
    if (piezoSeen) return;
    piezoCC   = cc;
    piezoSeen = true;
}

static void resetShotState()
{
    noInterrupts();
    firstAirCC     = 0;
    firstHitTimeUs = 0;
    shotInProgress = false;
    for (int i = 0; i < NUM_AIR; i++) airCount[i] = 0;
    piezoCC   = 0;
    piezoSeen = false;
    // Ringpuffer-Pfad (SET ALGO=RIM): nur die laufende Piezo-Anker-Wartezeit
    // verwerfen - die zirkulierenden Flanken selbst (ringCC/ringUs) bleiben
    // unangetastet, die naechste Piezo-Flanke setzt ohnehin einen neuen Anker.
    piezoPending = false;
    freezeActive = false;
    interrupts();
}

// ---------------------------------------------------------------------------
// Netzwerk + Sendepuffer (identisch zu Rev 3.1)
// ---------------------------------------------------------------------------

static WiFiClient tcp;
static bool       wifiEnabled = false;
static uint32_t   nextConnectAttemptMs = 0;
static uint32_t   connectBackoffMs = 1000;

// WLAN-Watchdog (Rev 4.12.1, siehe maintainNetwork()): der Core-Autoreconnect
// gibt bei manchen Trennungsgruenden (z.B. AUTH_FAIL, NO_AP_FOUND_*-Varianten
// 210-212) nach dem ersten Fehlschlag endgueltig auf - typisch, wenn der
// Access Point beim ESP32-Boot noch nicht bereit ist. Ist das WLAN laenger
// als WIFI_RETRY_MS getrennt, wird deshalb selbst neu verbunden.
#define WIFI_RETRY_MS      30000UL
static bool       wifiDown = true;
static uint32_t   wifiDownSinceMs = 0;

// TCP-Keepalive (Rev 4.12.1): erkennt einen abgeschalteten/abgestuerzten
// Stand-PC (kein FIN/RST) nach IDLE + INTVL*CNT = ca. 11s.
#define TCP_KA_IDLE_S      5
#define TCP_KA_INTVL_S     2
#define TCP_KA_CNT         3

// Zustandsautomat der Standbeleuchtung, siehe serviceLight() weiter unten:
// LP_WAIT_CONN = an, wartet auf TCP-Verbindung (Boot oder nach Verbindungs-
//                verlust erneut durchlaufen);
// LP_OFF_WAIT  = aus, wartet LIGHT_BLINK_DELAY_MS ab, ob geblinkt wird;
// LP_BLINK     = kurz an (Diagnose-Blink bei bestehendem WLAN);
// LP_OFF_DONE  = endgueltig aus, bis wieder tcp.connected().
enum LightPhase : uint8_t { LP_WAIT_CONN, LP_OFF_WAIT, LP_BLINK, LP_OFF_DONE };
static LightPhase lightPhase = LP_WAIT_CONN;
static uint32_t   lightPhaseMs = 0;
static bool       lightWasConnected = false;

// ACTION LIGHT=ON|OFF|AUTO (siehe handleActionCommand()): uebersteuert die
// automatische Verbindungsanzeige oben. NICHT NVS-persistent (wie SET
// TESTMODE) - nach einem Reboot ist die Anzeige immer wieder LIGHT_AUTO,
// damit ein vergessenes ACTION LIGHT=OFF sie nicht dauerhaft lahmlegt.
enum LightOverride : uint8_t { LIGHT_AUTO, LIGHT_FORCE_ON, LIGHT_FORCE_OFF };
static LightOverride lightOverride = LIGHT_AUTO;

// Offline-Ringpuffer fuer shot/reject (txBufPush). Rev 4.16.0: 32 x 1024 statt
// 64 x 360 Byte - shot-Zeilen erreichten mit air_w_ns/sound_act_mps bereits
// ~340-380 Zeichen (+17 Byte Signatur-Praefix) und wurden beim Puffern
// abgeschnitten (kaputtes JSON, ungueltige Signatur). 32 KB statt 23 KB RAM.
#define TXBUF_SLOTS 32
// Rev 4.16.1: + Signatur-Praefix ("<16 hex> "), damit ein shot mit voller
// Laenge (line[1024], siehe processShot()) auch signiert vollstaendig passt.
#define TXBUF_LINE  (1024 + AUTH_TAG_HEX_LEN + 2)
// Formatpuffer von emitf()/emitReject() (auf dem Loop-Stack, daher bewusst
// klein gehalten) - laengere Telegramme bauen ihren eigenen Puffer.
#define EMIT_LINE   360
static char    txBuf[TXBUF_SLOTS][TXBUF_LINE];
static uint8_t txHead = 0, txTail = 0;

static inline bool txBufEmpty() { return txHead == txTail; }
static inline bool txBufFull()  { return (uint8_t)(txHead + 1) % TXBUF_SLOTS == txTail; }

static void txBufPush(const char *line)
{
    if (txBufFull()) txTail = (txTail + 1) % TXBUF_SLOTS;
    strncpy(txBuf[txHead], line, TXBUF_LINE - 1);
    txBuf[txHead][TXBUF_LINE - 1] = '\0';
    txHead = (txHead + 1) % TXBUF_SLOTS;
}

// Waehrend handleCommand() einen Befehl mit " #<id>"-Suffix verarbeitet (siehe
// dortiges Parsing), haengt emitLine() den Wert als zusaetzliches "corr"-Feld
// an JEDE waehrend dieses einen Aufrufs gesendete JSON-Zeile an - das deckt
// sowohl den Normalfall (genau eine ok/error-Antwort) als auch z.B. CAL START
// ab. pollCommands() setzt den Wert direkt nach dem Aufruf wieder auf -1
// zurueck, damit spaeter (asynchron aus loop()) gesendete Telegramme
// (shot/status/...) NICHT versehentlich eine laengst abgearbeitete
// Korrelations-ID tragen. -1 = kein Befehl mit Suffix in Bearbeitung.
static long pendingCorrId = -1;

static void emitLine(const char *line)
{
    // Trailer des Aufrufers (falls vorhanden) abschneiden und einheitlich
    // neu anhaengen: auf Serial IMMER "\r\n" - reines "\n" laesst manches
    // Terminal (z.B. PuTTY im Raw-Modus) zwar eine Zeile nach unten
    // springen, aber nicht an den Zeilenanfang zurueckkehren (fehlendes
    // CR). Der TCP-Stream zum Stand-PC bleibt bewusst reines "\n" (JSON-
    // Zeilenstrom, kein Terminal).
    size_t len = strlen(line);
    while (len > 0 && (line[len - 1] == '\n' || line[len - 1] == '\r')) len--;

    // Korrelations-ID einfuegen (siehe pendingCorrId oben): nur moeglich,
    // wenn die Zeile mit "}" endet (jede JSON-Telegrammzeile dieser Firmware
    // tut das) und in den Hilfspuffer passt. Bis Rev 4.11.5 war der Puffer nur
    // 160 Byte gross (Annahme: waehrend der Befehlsverarbeitung nur kurze
    // ok/error/cal-Zeilen) - die Antwort auf SHOW (config, bis ~900 Byte,
    // siehe sendShowConfig()) kam dadurch OHNE corr beim Stand-PC an und
    // lief dort in den Timeout. Jetzt static (nicht auf dem Loop-Stack) und
    // gross genug fuer die laengsten Telegramme (line[1536], siehe sendShowConfig()) plus corr-Feld;
    // emitLine() wird nur aus loop() aufgerufen, ist also nicht reentrant.
    // Rev 4.16.1: 1600 Byte - "config" ist bis ~1230 Zeichen lang (siehe
    // sendShowConfig(), line[1536]).
    static char corrBuf[1600];
    if (pendingCorrId >= 0 && len > 0 && line[len - 1] == '}'
        && len < sizeof(corrBuf) - 24) {
        int n = snprintf(corrBuf, sizeof(corrBuf), "%.*s,\"corr\":%ld}",
                          (int)(len - 1), line, pendingCorrId);
        line = corrBuf;
        len  = (size_t)n;
    }

    Serial.write((const uint8_t *)line, len);
    Serial.print("\r\n");

    if (!wifiEnabled) return;

    // TCP-Ausgang signieren, falls ein Schluessel konfiguriert ist (Serial
    // oben bleibt bewusst UNSIGNIERT, siehe Dateikopf zu AUTH_PSK_DEFAULT_HEX).
    // Bis Rev 4.10.0 wurden Tag+Zeile erst in einen gemeinsamen, auf
    // TXBUF_LINE begrenzten Kopierpuffer geschrieben - dadurch blieben lange
    // Telegramme (v.a. "config", das in einem eigenen 900-Byte-Puffer gebaut
    // und per emitLine() direkt verschickt wird, siehe sendShowConfig())
    // FAST IMMER unsigniert, obwohl ein Schluessel konfiguriert war (Bug).
    // Fix: bei bestehender TCP-Verbindung Tag und Zeile als ZWEI getrennte
    // tcp.write()-Aufrufe senden - TCP ist ein Bytestrom, fuer den Empfaenger
    // nicht von einem einzelnen Aufruf zu unterscheiden, dafuer entfaellt
    // jede Laengenbegrenzung durch einen Zwischenpuffer. Nur fuer den
    // Offline-Ringpuffer (txBufPush(), ausschliesslich fuer shot/reject bei
    // fehlender TCP-Verbindung) wird weiterhin EIN zusammenhaengender String
    // gebraucht, dort gilt unveraendert die (bereits grosszuegig bemessene)
    // TXBUF_LINE-Kapazitaet als Obergrenze - siehe dortigen Kommentar.
    char authTag[AUTH_TAG_HEX_LEN + 1];
    const bool signOutgoing = (authPskLen > 0);
    if (signOutgoing) computeAuthTag((const uint8_t *)line, len, authTag);

    if (tcp.connected()) {
        while (!txBufEmpty() && tcp.connected()) {
            tcp.print(txBuf[txTail]);
            txTail = (txTail + 1) % TXBUF_SLOTS;
        }
        if (tcp.connected()) {
            if (signOutgoing) {
                tcp.write((const uint8_t *)authTag, AUTH_TAG_HEX_LEN);
                tcp.write((const uint8_t *)" ", 1);
            }
            tcp.write((const uint8_t *)line, len);
            tcp.print('\n');
            return;
        }
    }
    if (strstr(line, "\"type\":\"shot\"") || strstr(line, "\"type\":\"reject\"")) {
        if (signOutgoing) {
            static char signedBuf[TXBUF_LINE];   // static: 1 KB nicht auf den Loop-Stack
            snprintf(signedBuf, sizeof(signedBuf), "%s %s", authTag, line);
            txBufPush(signedBuf);
        } else {
            txBufPush(line);
        }
    }
}

static void emitf(const char *fmt, ...)
{
    char line[EMIT_LINE];
    va_list args;
    va_start(args, fmt);
    vsnprintf(line, sizeof(line), fmt, args);
    va_end(args);
    emitLine(line);
}

// "reject"-Telegramm ausgeben. fmt/Argumente liefern alles NACH dem "seq"-
// Feld (beginnend mit "\"algo\":...", inkl. schliessender "}").
//  SET SENDREJECT=1: seq/shots werden erhoeht, Ausgabe per emitLine() (Serial
//                    + TCP, bei fehlender Verbindung in den Ringpuffer).
//  SET SENDREJECT=0 (Default): Ausgabe NUR ueber Serial, OHNE "seq"-Feld und
//                    ohne seq/shots zu erhoehen - der Stand-PC bekommt den
//                    Reject gar nicht zu sehen und darf deshalb auch keine
//                    Luecke im seq-Zaehler finden.
static void emitReject(const char *fmt, ...)
{
    char body[EMIT_LINE - 40];
    va_list args;
    va_start(args, fmt);
    vsnprintf(body, sizeof(body), fmt, args);
    va_end(args);

    char line[EMIT_LINE];
    if (cfg.sendRejects) {
        shotCounter++;
        sequenceNo++;
        snprintf(line, sizeof(line), "{\"type\":\"reject\",\"seq\":%u,%s",
                 sequenceNo, body);
        emitLine(line);
    } else {
        // Zeilenende wie emitLine() auf Serial: immer "\r\n"
        int n = snprintf(line, sizeof(line), "{\"type\":\"reject\",%s", body);
        if (n >= (int)sizeof(line)) n = (int)sizeof(line) - 1;
        while (n > 0 && (line[n - 1] == '\n' || line[n - 1] == '\r')) line[--n] = '\0';
        Serial.print(line);
        Serial.print("\r\n");
    }
}

// ---------------------------------------------------------------------------
// Telegramme
// ---------------------------------------------------------------------------

static void sendStatus()
{
    // wifi/tcp-Verbindungsstatus sind seit Rev 4.7.4 Teil von SHOWNET
    // (sendShowNetConfig(), Felder wifi_ip/tcp_connected) statt hier -
    // gehoeren inhaltlich zur Netzwerkseite, nicht zum Auswertungs-Status.
    // capture (Rev 4.13.0): TATSAECHLICH aktive Erfassung der Luft-Mics -
    // kann bei SET CAPTURE=MCPWM nach einem Init-Fehler "isr" sein. Die
    // cap_*-Diagnosefelder nur bei aktivem MCPWM (siehe serviceCaptureCheck()).
    char capBuf[96];
    if (capActive) {
        snprintf(capBuf, sizeof(capBuf),
                 ",\"capture\":\"mcpwm\",\"cap_sync_ns\":%ld,\"cap_check_ns\":%ld,"
                 "\"cap_reanchor\":%lu",
                 (long)capSyncCheckNs, (long)capCheckNs, (unsigned long)capReanchors);
    } else {
        snprintf(capBuf, sizeof(capBuf), ",\"capture\":\"isr\"");
    }
    // Schallgeschwindigkeit (Rev 4.15.0): aktiver Wert und Quelle, bei SET
    // DHT=1 zusaetzlich die gefilterten Messwerte (null = noch keine gueltige
    // Messung), Alter der letzten gueltigen Messung und Fehlerzaehler.
    char sndBuf[160];
    int  sn = snprintf(sndBuf, sizeof(sndBuf), ",\"sound_act_mps\":%.2f,\"sound_src\":\"%s\"",
                       soundMpsActive, soundFromDht ? "dht" : "fixed");
    if (cfg.useDht) {
        if (dhtValid) {
            snprintf(sndBuf + sn, sizeof(sndBuf) - sn,
                     ",\"temp_c\":%.1f,\"rh_pct\":%.1f,\"dht_age_s\":%lu,\"dht_err\":%lu",
                     dhtTempC, dhtRhPct, (unsigned long)((millis() - dhtLastOkMs) / 1000UL),
                     (unsigned long)dhtErrors);
        } else {
            snprintf(sndBuf + sn, sizeof(sndBuf) - sn,
                     ",\"temp_c\":null,\"rh_pct\":null,\"dht_err\":%lu",
                     (unsigned long)dhtErrors);
        }
    }
    // Eigener Puffer statt emitf() (EMIT_LINE=360): mit capture- und
    // Schallgeschwindigkeits-Feldern wird die Zeile laenger.
    char line[600];
    snprintf(line, sizeof(line),
             "{\"type\":\"status\",\"version\":\"%s\",\"mac\":\"%s\",\"lane\":%u,"
             "\"uptime_s\":%llu,\"shots\":%u,\"window_ms\":%u,"
             "\"debounce_ms\":%u,\"mics\":%d,"
             "\"buffered\":%u,\"test_mode\":%d%s%s}\n",
             FW_VERSION, deviceMac, cfg.lane,
             (unsigned long long)(esp_timer_get_time() / 1000000ULL),
             shotCounter, cfg.windowMs, cfg.debounceMs, NUM_AIR,
             (unsigned)((txHead - txTail + TXBUF_SLOTS) % TXBUF_SLOTS),
             testMode ? 1 : 0, capBuf, sndBuf);
    emitLine(line);
}

static void sendShowConfig()
{
    char ofsBuf[80], enBuf[24];
    int  on = 0, oe = 0;
    for (int i = 0; i < NUM_AIR; i++) {
        on += snprintf(ofsBuf + on, sizeof(ofsBuf) - on, "%s%ld",
                        i > 0 ? "," : "", (long)cfg.micOffsetNs[i]);
        oe += snprintf(enBuf + oe, sizeof(enBuf) - oe, "%s%d",
                        i > 0 ? "," : "", cfg.micEnabled[i] ? 1 : 0);
    }
    // Eigener, grosszuegig bemessener Puffer statt emitf() (dessen interner
    // Puffer nur TXBUF_LINE=320 Byte fasst) - die SHOW-Zeile ist mit allen
    // Feldern laenger und wuerde sonst stillschweigend abgeschnitten. Die
    // Netzwerk-Felder (SSID/Pass/Host/IP/...) sind seit Rev 4.7.1 in einer
    // eigenen SHOWNET-Zeile ausgelagert (siehe sendShowNetConfig()). Trotzdem
    // reichte 500 Byte (Rev 4.7.1-4.7.3) nicht mehr aus (Rev 4.7.3 kam mit
    // mic_half_x_mm/bullet_shift_* wieder an die Grenze) - 700 Byte deckte den
    // Worst-Fall aller Felder bei Maximalwerten (~592 Byte) mit Marge ab.
    // Rev (Papiervorschub) kam mit den paper_*-Feldern wieder naeher an die
    // Grenze (~660 Byte Worst-Fall) - auf 900 Byte angehoben. Seit den
    // Feldern aus Rev 4.13-4.16 (capture, walk_*, sound_set_mps, dht,
    // raw_log, cluster_corr/iter, lsq*) typisch ~1000, im Worst-Fall ~1230
    // Zeichen - 900 Byte schnitten die Zeile ab (Rev 4.16.1: 1536 Byte,
    // static statt auf dem Loop-Stack; nur aus loop() aufgerufen).
    static char line[1536];
    int  n = snprintf(line, sizeof(line),
          "{\"type\":\"config\",\"mac\":\"%s\",\"lane\":%u,"
          "\"debounce_ms\":%u,\"reject_lockout_ms\":%u,\"send_reject\":%d,"
          "\"window_ms\":%u,\"debug\":%d,"
          "\"outlier_um\":%u,\"cluster_radius_um\":%u,\"min_cluster_hits\":%d,"
          "\"max_precision_um\":%u,\"min_mics\":%d,"
          "\"tdoa_us\":%u,\"mic_offset_ns\":[%s],\"mic_enabled\":[%s],\"cal_shots\":%d,"
          "\"standoff_paper_mm\":%.2f,"
          "\"mic_half_x_mm\":%.2f,"
          "\"bullet_shift_pct\":%d,\"bullet_shift_cap_mm\":%.2f,"
          "\"cluster_corr\":%d,\"cluster_iter\":%u,\"lsq\":%d,\"lsq_pellet_r_mm\":%.2f,"
          "\"algo\":\"%s\",\"pellet_r_mm\":%.2f,\"max_sigma_um\":%ld,"
          "\"capture\":\"%s\",\"walk_k\":%.3f,\"walk_cal\":%d,\"walk_max_us\":%u,"
          "\"use_piezo\":%d,\"piezo_req\":%d,\"piezo_min_us\":%u,\"piezo_max_us\":%u,"
          "\"test_cooldown_ms\":%u,\"offset_x_um\":%ld,\"offset_y_um\":%ld,"
          "\"sound_mps\":%u,\"sound_set_mps\":%.1f,\"dht\":%d,\"raw_log\":%d,"
          "\"zones\":%d,\"z_hole_um\":%lu,"
          "\"paper_feed_mm\":%.2f,\"paper_speed_mmps\":%.2f,\"paper_auto\":%d,"
          "\"paper_trigger\":\"%s\",\"paper_dir_invert\":%d,"
          "\"paper_jog_speed_mmps\":%.2f}\n",
          deviceMac, cfg.lane,
          cfg.debounceMs, cfg.rejectLockoutMs, cfg.sendRejects ? 1 : 0,
          cfg.windowMs, cfg.debug,
          cfg.airOutlierUm, cfg.clusterRadiusUm, cfg.minClusterHits,
          cfg.maxPrecisionUm, cfg.minMics,
          cfg.airMaxTdoaUs, ofsBuf, enBuf, cfg.calShotCount,
          cfg.standoffPaperMm,
          cfg.micHalfXMm,
          cfg.bulletShiftPct, cfg.bulletShiftCapMm,
          cfg.clusterCorr ? 1 : 0, cfg.clusterIter, cfg.lsq ? 1 : 0, cfg.lsqPelletRMm,
          cfg.algoMode == ALGO_RIM ? "rim" : "classic",
          cfg.pelletRadiusMm, lroundf(cfg.maxSigmaMm * 1000.0f),
          cfg.airMcpwm ? "mcpwm" : "isr",
          cfg.walkK, cfg.walkCal ? 1 : 0, (unsigned)cfg.walkMaxUs,
          cfg.usePiezo ? 1 : 0, cfg.piezoRequired ? 1 : 0,
          cfg.piezoMinUs, cfg.piezoMaxUs,
          cfg.testCooldownMs, (long)cfg.offsetXUm, (long)cfg.offsetYUm,
          // sound_mps: ganzzahlig gerundet wie bis Rev 4.14 (Kompatibilitaet
          // mit Stand-PCs, die eine Ganzzahl erwarten) - genauer Wert in
          // sound_set_mps, aktiver Wert in STATUS sound_act_mps.
          (unsigned)lroundf(cfg.soundSpeedMps), cfg.soundSpeedMps, cfg.useDht ? 1 : 0,
          cfg.rawLog ? 1 : 0,
          cfg.zonesOn ? 1 : 0, (unsigned long)cfg.zHoleUm,
          cfg.paperFeedMm, cfg.paperSpeedMmS, cfg.paperAuto ? 1 : 0,
          cfg.paperTrigger == PAPER_TRIG_ANY ? "any"
              : (cfg.paperTrigger == PAPER_TRIG_CLEAN ? "clean" : "piezo"),
          cfg.paperDirInvert ? 1 : 0,
          cfg.paperJogSpeedMmS);
    (void)n;
    emitLine(line);
}

// SHOWNET: Netzwerkbezogene Konfiguration (WLAN, Ziel-Host, eigene IP-
// Konfiguration) - seit Rev 4.7.1 aus SHOW ausgelagert, da die SHOW-Zeile
// mit allen Auswertungs-Parametern sonst den Puffer sprengt. wifi_ip/
// tcp_connected (Rev 4.7.4, vorher Teil von STATUS als wifi/tcp) sind
// LIVE-Verbindungsstatus, keine Konfiguration - gehoeren aber inhaltlich
// zur Netzwerkseite und stehen deshalb ebenfalls hier statt in STATUS.
static void sendShowNetConfig()
{
    char wifiIp[20] = "none";
    if (wifiEnabled && WiFi.status() == WL_CONNECTED) {
        snprintf(wifiIp, sizeof(wifiIp), "%s", WiFi.localIP().toString().c_str());
    }
    // net_pending/net_confirm_deadline_s (Rev 4.9.0, siehe armNetWatchdog
    // IfNeeded()/serviceNetWatchdog()): net_pending=true, solange eine
    // Netzwerkaenderung noch nicht per NET CONFIRM bestaetigt wurde -
    // net_confirm_deadline_s nur vorhanden, wenn der Ruecksprung-Watchdog
    // bereits laeuft (also NACH dem Reboot mit den neuen Werten; vorher, im
    // selben Boot-Zyklus wie die SET-Aenderung, gibt es noch keine
    // Deadline).
    char netPendBuf[40] = "";
    if (netPendingLoaded) {
        if (netWatchdogArmed) {
            uint32_t remainMs = netWatchdogDeadlineMs - millis();
            snprintf(netPendBuf, sizeof(netPendBuf),
                     ",\"net_pending\":true,\"net_confirm_deadline_s\":%u",
                     (unsigned)(remainMs / 1000));
        } else {
            snprintf(netPendBuf, sizeof(netPendBuf), ",\"net_pending\":true");
        }
    } else {
        snprintf(netPendBuf, sizeof(netPendBuf), ",\"net_pending\":false");
    }
    char line[500];
    int  n = snprintf(line, sizeof(line),
          "{\"type\":\"confignet\",\"mac\":\"%s\",\"ssid\":\"%s\",\"pass\":\"%s\","
          "\"host\":\"%s\",\"port\":%u,"
          "\"static_ip\":%d,\"ip\":\"%s\",\"gateway\":\"%s\","
          "\"subnet\":\"%s\",\"dns\":\"%s\","
          "\"wifi_ip\":\"%s\",\"tcp_connected\":%s%s}\n",
          deviceMac,
          cfg.ssid.c_str(),
          cfg.pass.length() ? "****" : "",
          cfg.host.c_str(), cfg.port,
          cfg.staticIP ? 1 : 0, cfg.ip.c_str(), cfg.gateway.c_str(),
          cfg.subnet.c_str(), cfg.dns.c_str(),
          wifiIp, tcp.connected() ? "true" : "false", netPendBuf);
    (void)n;
    emitLine(line);
}

// ---------------------------------------------------------------------------
// Papiervorschub (NEMA17 + DRV8825/A4988) - siehe PAPER_*-Defines oben
// ---------------------------------------------------------------------------
// Nicht-blockierende Schrittmotor-Ansteuerung: servicePaperStepper() wird aus
// loop() bei jedem Durchlauf aufgerufen und generiert bei Faelligkeit (siehe
// paperNextStepUs, esp_timer-Zeitbasis wie der Rest der Firmware) jeweils
// GENAU EINEN STEP-Impuls - ein delay()/eine Warteschleife wuerde sonst
// waehrend des gesamten Vorschubs (bis zu mehrere Sekunden) Schusserfassung,
// Netzwerk und Kommandoverarbeitung blockieren.
//
// PAPER_IDLE:                kein Vorschub aktiv, Treiber stromlos (EN=HIGH)
// PAPER_FEEDING:              automatischer Vorschub nach einem Schuss
//                             (SET PAPERFEED-Strecke, Geschwindigkeitsprofil
//                             siehe unten), Treiber aktiv
// PAPER_JOG_FWD/PAPER_JOG_REV: manueller Dauerbetrieb ueber die beiden
//                             Kippschalter-Positionen (Einfaedeln), feste
//                             SET PAPERJOGSPEED (Default 75.0mm/s), Treiber aktiv
enum PaperMode : uint8_t { PAPER_IDLE, PAPER_FEEDING, PAPER_JOG_FWD, PAPER_JOG_REV };
static PaperMode paperMode       = PAPER_IDLE;
static uint32_t  paperStepIndex  = 0;   // bereits ausgefuehrte Schritte (PAPER_FEEDING)
static uint32_t  paperStepsTotal = 0;   // Gesamtschritte fuer den laufenden Vorschub
static uint32_t  paperHalfSteps  = 0;   // Schrittindex, ab dem abgebremst wird
static uint64_t  paperNextStepUs = 0;   // esp_timer_get_time()-Zeitpunkt des naechsten Schritts
static bool      paperStepPinHigh = false; // STEP aktuell HIGH, wartet auf Ruecklanke
static uint64_t  paperStepFallUs  = 0;     // Zeitpunkt, zu dem STEP wieder LOW gezogen wird

// Entprellung der beiden Kippschalter-Eingaenge (PAPER_SW_DEBOUNCE_US) -
// je Pin der zuletzt gelesene Rohwert, dessen Zeitpunkt und der aktuell
// als gueltig uebernommene (entprellte) Zustand, siehe debouncePaperSwitch().
static bool      swFwdRawLast = false, swRevRawLast = false;
static bool      swFwdStable  = false, swRevStable  = false;
static uint64_t  swFwdChangeUs = 0,    swRevChangeUs = 0;

static inline bool debouncePaperSwitch(bool raw, bool &rawLast, bool &stable,
                                        uint64_t &changeUs, uint64_t now)
{
    if (raw != rawLast) {
        rawLast  = raw;
        changeUs = now;
    } else if (raw != stable && (now - changeUs) >= PAPER_SW_DEBOUNCE_US) {
        stable = raw;
    }
    return stable;
}

static inline uint32_t paperStepPeriodUsForSpeed(float mmPerS)
{
    if (mmPerS < 0.05f) mmPerS = 0.05f;   // Division durch 0 / absurd lange Perioden vermeiden
    return (uint32_t)(1.0e6f / (mmPerS * PAPER_STEPS_PER_MM));
}

// SET TESTMODE=1 (siehe testMode weiter oben): gibt bei jeder Aenderung von
// EN/DIR eine Klartext-Zeile aus (Verkabelungs-/Elektronik-Fehlersuche fuer
// den Papiervorschub) - analog zu den bestehenden Sensor-Testmodus-Zeilen,
// aber unabhaengig davon ueber emitLine() ausgegeben, da EN/DIR nur bei
// einem Moduswechsel (nicht bei jedem Schritt) gesetzt werden.
static void paperTestLog(const char *msg)
{
    if (!testMode) return;
    emitLine(msg);
}

static inline void paperEnable(bool en)
{
    bool level = en ? LOW : HIGH;
    if (testMode) {
        char buf[64];
        snprintf(buf, sizeof(buf), "PAPER EN(GPIO%d) = %s", PAPER_EN_PIN,
                 en ? "LOW (aktiv)" : "HIGH (aus)");
        paperTestLog(buf);
    }
    digitalWrite(PAPER_EN_PIN, level);   // aktiv-LOW (DRV8825/A4988-Standard)
}

static inline void paperSetDir(bool forward)
{
    bool level = forward != cfg.paperDirInvert;    // SET PAPERDIR invertiert bei Bedarf
    if (testMode) {
        char buf[80];
        snprintf(buf, sizeof(buf), "PAPER DIR(GPIO%d) = %s (%s%s)", PAPER_DIR_PIN,
                 level ? "HIGH" : "LOW", forward ? "vorwaerts" : "rueckwaerts",
                 cfg.paperDirInvert ? ", PAPERDIR=1 invertiert" : "");
        paperTestLog(buf);
    }
    digitalWrite(PAPER_DIR_PIN, level ? HIGH : LOW);
}

// Zaehler fuer die STEP-Impulse im Testmodus - bei jedem einzelnen Schritt
// zu loggen wuerde die Ausgabe fluten (bis zu ~400 Schritte je Vorschub),
// daher nur alle 100 Impulse eine Meldung.
static uint32_t paperTestStepCount = 0;

// Zieht STEP JETZT auf HIGH und merkt sich (paperStepFallUs), wann
// servicePaperStepper() es wieder LOW ziehen soll - die HALBE uebergebene
// Periode (periodUs), siehe Kommentar bei PAPER_STEP_PULSE_MIN_US. Bewusst
// KEIN delayMicroseconds() fuer die Ruecklanke mehr: bei niedrigen
// Geschwindigkeiten waere die halbe Periode viele zehn/hundert Millisekunden
// lang - das wuerde Schusserfassung/Netzwerk/Kommandoverarbeitung so lange
// blockieren. Die eigentliche Ruecklanke erledigt der Aufruf am Anfang von
// servicePaperStepper() bei jedem loop()-Durchlauf.
static inline void paperStepRise(uint32_t periodUs)
{
    digitalWrite(PAPER_STEP_PIN, HIGH);
    uint32_t highUs = periodUs / 2;
    if (highUs < PAPER_STEP_PULSE_MIN_US) highUs = PAPER_STEP_PULSE_MIN_US;
    paperStepFallUs  = (uint64_t)esp_timer_get_time() + highUs;
    paperStepPinHigh = true;
    if (testMode) {
        paperTestStepCount++;
        if (paperTestStepCount % 100 == 0) {
            char buf[48];
            snprintf(buf, sizeof(buf), "PAPER STEP(GPIO%d) Impuls #%u",
                     PAPER_STEP_PIN, (unsigned)paperTestStepCount);
            paperTestLog(buf);
        }
    }
}

// Startet den automatischen Vorschub (SET PAPERFEED mm, siehe processShot())
// - wird ignoriert, wenn bereits ein Vorschub laeuft oder gerade manuell per
// Schalter eingefaedelt wird (kein Ueberlagern zweier Bewegungen).
static void startPaperFeed()
{
    if (paperMode != PAPER_IDLE) return;
    uint32_t steps = (uint32_t)lroundf(cfg.paperFeedMm * PAPER_STEPS_PER_MM);
    if (steps == 0) return;
    paperStepsTotal = steps;
    paperHalfSteps  = steps / 2;
    paperStepIndex  = 0;
    paperSetDir(true);
    paperEnable(true);
    paperMode       = PAPER_FEEDING;
    paperNextStepUs = (uint64_t)esp_timer_get_time();
}

// Aus loop() bei jedem Durchlauf aufzurufen. Prueft zuerst die beiden
// Einfaedel-Schalter (haben Vorrang, unterbrechen dafuer noetigenfalls auch
// einen laufenden automatischen Vorschub) und bedient danach - falls faellig
// - den naechsten Schritt des aktuellen Modus.
static void servicePaperStepper()
{
    const uint64_t now = (uint64_t)esp_timer_get_time();

    // Ruecklanke eines evtl. noch offenen STEP-Impulses zuerst bedienen -
    // unabhaengig von paperMode/Schaltern, damit ein Impuls auch dann
    // korrekt zu Ende gebracht wird, wenn sich der Modus zwischenzeitlich
    // aendert (z.B. Vorschub fertig, Schalter losgelassen).
    if (paperStepPinHigh && now >= paperStepFallUs) {
        digitalWrite(PAPER_STEP_PIN, LOW);
        paperStepPinHigh = false;
    }

    // Rohwerte entprellt uebernehmen (PAPER_SW_DEBOUNCE_US) - siehe Kommentar
    // bei debouncePaperSwitch()/PAPER_SW_DEBOUNCE_US: verhindert, dass ein
    // kurzer Stoerimpuls den Treiber faelschlich abschaltet.
    bool swFwdRaw = (digitalRead(PAPER_SW_FWD_PIN) == LOW);
    bool swRevRaw = (digitalRead(PAPER_SW_REV_PIN) == LOW);
    bool swFwd = debouncePaperSwitch(swFwdRaw, swFwdRawLast, swFwdStable, swFwdChangeUs, now);
    bool swRev = debouncePaperSwitch(swRevRaw, swRevRawLast, swRevStable, swRevChangeUs, now);

    // SET TESTMODE=1: bei jeder Pegelaenderung (nicht bei jedem loop()-
    // Durchlauf) eine Klartext-Zeile - zeigt direkt, ob die Verkabelung der
    // Schalter (aktiv=LOW gegen GND, siehe INPUT_PULLUP in setup()) korrekt
    // ankommt, unabhaengig davon, ob daraus ein Modus-/Bewegungswechsel folgt.
    if (testMode) {
        static bool lastSwFwd = false, lastSwRev = false;
        char buf[48];
        if (swFwd != lastSwFwd) {
            snprintf(buf, sizeof(buf), "PAPER GPIO%d (vorwaerts) %s",
                     PAPER_SW_FWD_PIN, swFwd ? "erkannt" : "losgelassen");
            paperTestLog(buf);
            lastSwFwd = swFwd;
        }
        if (swRev != lastSwRev) {
            snprintf(buf, sizeof(buf), "PAPER GPIO%d (rueckwaerts) %s",
                     PAPER_SW_REV_PIN, swRev ? "erkannt" : "losgelassen");
            paperTestLog(buf);
            lastSwRev = swRev;
        }
    }

    if (swFwd && !swRev) {
        if (paperMode != PAPER_JOG_FWD) {
            paperMode       = PAPER_JOG_FWD;
            paperSetDir(true);
            paperEnable(true);
            paperNextStepUs = now;
        }
    } else if (swRev && !swFwd) {
        if (paperMode != PAPER_JOG_REV) {
            paperMode       = PAPER_JOG_REV;
            paperSetDir(false);
            paperEnable(true);
            paperNextStepUs = now;
        }
    } else if (paperMode == PAPER_JOG_FWD || paperMode == PAPER_JOG_REV) {
        // Weder noch (Normalfall bei losgelassenem Schalter) ODER beide
        // Pins gleichzeitig (sollte am 2-Positionen-Kippschalter nicht
        // vorkommen) - in beiden Faellen sicherheitshalber stoppen statt
        // eine Richtung zu vermuten.
        paperMode = PAPER_IDLE;
        paperEnable(false);
    }

    if (paperMode == PAPER_IDLE) return;

    if (now < paperNextStepUs) return;

    if (paperMode == PAPER_JOG_FWD || paperMode == PAPER_JOG_REV) {
        uint32_t period = paperStepPeriodUsForSpeed(cfg.paperJogSpeedMmS);
        paperStepRise(period);
        paperNextStepUs = now + period;
        return;
    }

    // PAPER_FEEDING: erste Haelfte der Strecke mit konstanter SET PAPERSPEED,
    // zweite Haelfte linear abfallend bis PAPER_DECEL_END_FRACTION davon
    // (mind. PAPER_MIN_SPEED_MMPS) - bremst die Papierrolle rechtzeitig ab,
    // damit sie durch ihre eigene Massentraegheit am Ende nicht nachlaeuft.
    float speed = cfg.paperSpeedMmS;
    if (paperStepIndex >= paperHalfSteps && paperStepsTotal > paperHalfSteps) {
        float frac = (float)(paperStepIndex - paperHalfSteps)
                   / (float)(paperStepsTotal - paperHalfSteps);
        float endSpeed = cfg.paperSpeedMmS * PAPER_DECEL_END_FRACTION;
        if (endSpeed < PAPER_MIN_SPEED_MMPS) endSpeed = PAPER_MIN_SPEED_MMPS;
        speed = cfg.paperSpeedMmS - frac * (cfg.paperSpeedMmS - endSpeed);
    }
    uint32_t period = paperStepPeriodUsForSpeed(speed);
    paperStepRise(period);
    paperStepIndex++;
    if (paperStepIndex >= paperStepsTotal) {
        paperMode = PAPER_IDLE;
        paperEnable(false);
        return;
    }
    paperNextStepUs = now + period;
}

// ---------------------------------------------------------------------------
// Positionsberechnung fuer die Luftschall-Messung
// ---------------------------------------------------------------------------
// Lokales Koordinatensystem der Zielflaeche: Ursprung = Scheibenzentrum,
// x nach rechts, y nach oben (aus Schuetzensicht), z senkrecht von der
// Scheibe weg Richtung Schuetze. Mic-Reihenfolge identisch zu AIR_PINS[]:
//   0 = GPIO25 = links unten     1 = GPIO26 = rechts unten
//   2 = GPIO27 = links oben      3 = GPIO14 = rechts oben
//   4 = GPIO4  = links mitte     5 = GPIO13 = rechts mitte
// Alle frueheren Hardware-Diagnosetests (Rev 4.4.6-4.4.9/4.8.1) sind
// zurueckgebaut. Index 4/5 lagen bis Rev 4.11.2 auf GPIO32/33, seit Rev
// 4.11.3 auf GPIO4/13 (siehe AIR_PINS[]-Kommentar oben - Grund: GPIO-
// Interrupt-Dispatch-Bank-Bias, nicht Hardware-Diagnose).

// x-Abstand Mic-Spalte<->vertikale Mittellinie. Seit Rev 4.7.3 nur noch der
// Default-Wert - der tatsaechlich genutzte Abstand ist per SET MICHALFX
// laufzeitkonfigurierbar (cfg.micHalfXMm), siehe applyMicHalfX() weiter unten
// (deckt z.B. ab, dass die Mikrofon-Membran 1-2mm hinter der aeusseren,
// bisher vermessenen Huelle liegt).
#define MIC_HALF_X          115.0f  // mm, horizontaler Abstand Mic-Spalte<->Zentrum (Default)
// Papierscheibe (Durchschlag-Messung): Mics 85mm ueber/unter Mitte, 28mm
// rechtwinklig vor der Scheibe (SET STANDOFFPAPER fuer den Standoff).
#define MIC_HALF_Y_PAPER     85.0f  // mm
#define MIC_STANDOFF_PAPER   28.0f  // mm

// Schallgeschwindigkeit: bei SET DHT=1 aus Temperatur/Luftfeuchte des DHT22
// (Rev 4.15.0, siehe serviceDht()), sonst bzw. ohne gueltige Messung der
// feste Wert SET SOUNDSPEED (cfg.soundSpeedMps, Default 342.5 m/s) - siehe
// applySoundSpeed(). Ein radial mit dem
// Abstand vom Zentrum WACHSENDER Fehler (Treffer am Rand werden zu nah am
// Zentrum berechnet) ist ein typisches Anzeichen fuer eine zu NIEDRIG
// angenommene Schallgeschwindigkeit und kann darueber ausgeglichen werden.
// Wird zusaetzlich automatisch durch CAL START mitkalibriert, siehe
// runCalibration() weiter unten.
#define SOUND_SPEED_MIN_MPS  300
#define SOUND_SPEED_MAX_MPS  400
static float soundMmPerNs = 0.0003425f;

// ---------------------------------------------------------------------------
// Temperatur/Luftfeuchte per DHT22 (Rev 4.15.0, SET DHT=1)
// ---------------------------------------------------------------------------
// Die Schallgeschwindigkeit aendert sich um ca. 0,6 m/s (0,18 %) je Kelvin.
// Der Fehler wirkt wie ein Massstabsfehler, der mit dem Abstand vom
// Scheibenzentrum waechst - z.B. 10 K Unterschied zur Kalibrier-Temperatur
// ergeben bei 30 mm Ablage grob 0,5 mm. Deshalb wird sie bei SET DHT=1 aus
// der gemessenen Temperatur und Luftfeuchte berechnet (Anregung: freETarget,
// speed_of_sound.c).
//
// Auslesen OHNE Interrupt-Sperre (eigene Routine statt DHT-Bibliothek): die
// ueblichen Bibliotheken sperren fuer die ~5 ms des Protokolls alle
// Interrupts - Mic-Flanken in dieser Zeit wuerden verschluckt bzw. bekaemen
// (ISR-Betrieb) falsche Zeitstempel. Hier misst loop() die Pulse per
// Zykluszaehler; verlaengert ein Interrupt einen Puls, faellt das ueber die
// Pruefsumme auf und die Messung wird einige Sekunden spaeter wiederholt.
// Gemessen wird zudem nur in Ruhephasen (kein Schuss in Arbeit, letzte
// Mic-Flanke >= 200 ms her). GPIO15 ist ein Strapping-Pin (Bootlog-Ausgabe),
// unkritisch: der DHT22 haelt die Leitung im Ruhezustand ueber seinen
// Pull-up auf HIGH, dem Normalzustand beim Booten.
#define DHT_PIN          15
#define DHT_INTERVAL_MS  30000UL    // Messabstand (DHT22: min. 2 s)
#define DHT_RETRY_MS     5000UL     // nach fehlgeschlagener Messung
#define DHT_FIRST_MS     2500UL     // nach Boot/Einschalten (Sensor-Anlaufzeit)
#define DHT_MAX_AGE_MS   300000UL   // aelter -> Ersatzwert SET SOUNDSPEED
// Zustandsvariablen (dhtValid/dhtTempC/... , soundMpsActive) stehen weiter
// oben bei cpuMHz, da sendStatus() sie schon vorher braucht.

// Schallgeschwindigkeit in feuchter Luft (m/s). Trockene Luft:
// 331,3 * sqrt(T_K / 273,15). Wasserdampf (leichter als Luft) erhoeht sie um
// ca. 16 % seines Stoffmengenanteils x_w; Saettigungsdampfdruck nach Magnus,
// Luftdruck fest 1013,25 hPa (950 hPa statt 1013 aendern das Ergebnis nur um
// ~0,05 m/s). Kontrolle: 20 C/50 % -> 343,8 m/s, 20 C trocken -> 343,2 m/s.
static float speedOfSoundMps(float tempC, float rhPct)
{
    const float pSatPa = 611.2f * expf(17.62f * tempC / (243.12f + tempC));
    const float xw     = (rhPct / 100.0f) * pSatPa / 101325.0f;
    return 331.3f * sqrtf(1.0f + tempC / 273.15f) * (1.0f + 0.16f * xw);
}

// Setzt soundMmPerNs auf den aktuell gueltigen Wert (DHT-Messung oder SET
// SOUNDSPEED). Wird beim Booten, bei SET SOUNDSPEED/DHT und nach jeder neuen
// DHT-Messung aufgerufen - nur aus loop(), also nie mitten in einer
// Schussauswertung.
static void applySoundSpeed()
{
    soundFromDht   = cfg.useDht && dhtValid;
    soundMpsActive = soundFromDht ? speedOfSoundMps(dhtTempC, dhtRhPct)
                                  : cfg.soundSpeedMps;
    soundMmPerNs = soundMpsActive * 1.0e-6f;   // (m/s) -> mm/ns
    updatePostWaitUs();   // haengt von der Schallgeschwindigkeit ab (SET ALGO=RIM)
}

// Dauer in us, die DHT_PIN auf 'level' bleibt; -1 bei Timeout.
static int32_t dhtPulseUs(int level, uint32_t timeoutUs)
{
    const uint32_t start = esp_cpu_get_cycle_count();
    const uint32_t maxCC = timeoutUs * cpuMHz;
    while (gpio_get_level((gpio_num_t)DHT_PIN) == level) {
        if (esp_cpu_get_cycle_count() - start > maxCC) return -1;
    }
    return (int32_t)((esp_cpu_get_cycle_count() - start) / cpuMHz);
}

// Eine Messung nach DHT22/AM2302-Protokoll, blockiert loop() ca. 5 ms.
static bool dhtRead(float *tempC, float *rhPct)
{
    uint8_t b[5] = {0, 0, 0, 0, 0};
    pinMode(DHT_PIN, OUTPUT);
    digitalWrite(DHT_PIN, LOW);
    delayMicroseconds(1200);           // Startsignal (>= 1 ms)
    pinMode(DHT_PIN, INPUT_PULLUP);    // freigeben
    // Antwort: Pull-up HIGH (20-40 us), Sensor 80 us LOW, 80 us HIGH
    if (dhtPulseUs(1, 200) < 0 || dhtPulseUs(0, 200) < 0 || dhtPulseUs(1, 200) < 0) {
        return false;
    }
    // 40 Bit: je 50 us LOW, dann 26-28 us HIGH ('0') oder 70 us HIGH ('1')
    for (int i = 0; i < 40; i++) {
        const int32_t lo = dhtPulseUs(0, 200);
        const int32_t hi = dhtPulseUs(1, 200);
        if (lo < 0 || hi < 0) return false;
        b[i / 8] = (uint8_t)(b[i / 8] << 1);
        if (hi > lo) b[i / 8] |= 1;
    }
    if ((uint8_t)(b[0] + b[1] + b[2] + b[3]) != b[4]) return false;
    if ((b[0] | b[1] | b[2] | b[3]) == 0) return false;   // Leitung tot
    *rhPct = (float)(((uint16_t)b[0] << 8) | b[1]) * 0.1f;
    float t = (float)((((uint16_t)b[2] & 0x7F) << 8) | b[3]) * 0.1f;
    *tempC = (b[2] & 0x80) ? -t : t;
    return true;
}

static float median3(const float v[3], uint8_t n)
{
    if (n == 1) return v[0];
    if (n == 2) return 0.5f * (v[0] + v[1]);
    const float a = v[0], b = v[1], c = v[2];
    return fmaxf(fminf(a, b), fminf(fmaxf(a, b), c));
}

// Aus loop(): misst alle DHT_INTERVAL_MS in Ruhephasen, filtert (Median aus
// 3, Plausibilitaet, Sprungsperre) und uebernimmt den Wert per
// applySoundSpeed(). Faellt die Messung laenger als DHT_MAX_AGE_MS aus oder
// wird SET DHT=0 gesetzt, gilt wieder SET SOUNDSPEED.
static void serviceDht()
{
    static bool     wasOn  = false;
    static uint32_t nextMs = 0;
    const uint32_t  now    = millis();

    if (!cfg.useDht) {
        if (wasOn) {
            wasOn = false; dhtValid = false; dhtHistN = 0; dhtHistPos = 0;
            applySoundSpeed();
        }
        return;
    }
    if (!wasOn) {
        wasOn  = true;
        pinMode(DHT_PIN, INPUT_PULLUP);
        nextMs = now + DHT_FIRST_MS;
    }
    if (dhtValid && now - dhtLastOkMs > DHT_MAX_AGE_MS) {
        dhtValid = false; dhtHistN = 0; dhtHistPos = 0;
        applySoundSpeed();
        Serial.println("# DHT22: keine gueltige Messung seit 5 min - Ersatzwert SET SOUNDSPEED");
    }
    if ((int32_t)(now - nextMs) < 0) return;
    if (shotInProgress || piezoPending) return;
    if ((uint32_t)esp_timer_get_time() - lastAirEdgeUs < 200000UL) return;

    float t, h;
    if (!dhtRead(&t, &h) || t < -20.0f || t > 60.0f || h < 0.0f || h > 100.0f) {
        dhtErrors++;
        nextMs = now + DHT_RETRY_MS;
        return;
    }
    nextMs = now + DHT_INTERVAL_MS;
    // Sprungsperre: mehr als 5 K gegenueber dem gefilterten Wert binnen
    // weniger Minuten ist physikalisch unplausibel (Lesefehler, der die
    // Pruefsumme zufaellig bestanden hat).
    if (dhtValid && fabsf(t - dhtTempC) > 5.0f) {
        dhtErrors++;
        return;
    }
    dhtHistT[dhtHistPos] = t;
    dhtHistH[dhtHistPos] = h;
    dhtHistPos = (dhtHistPos + 1) % 3;
    if (dhtHistN < 3) dhtHistN++;
    dhtTempC    = median3(dhtHistT, dhtHistN);
    dhtRhPct    = median3(dhtHistH, dhtHistN);
    dhtValid    = true;
    dhtLastOkMs = now;
    applySoundSpeed();
}

// Schwelle fuer "Mikrofon-Ausreisser" ist zur Laufzeit konfigurierbar:
// SET OUTLIER=<0.001mm>, siehe cfg.airOutlierUm (Default 5000 = 5.0mm).
// Zulaessiger Bereich fuer den per Kalibrierung (CAL START) ermittelten
// bzw. per SET OFS<i> manuell gesetzten Timing-Offset je Mikrofon. War
// 5000ns (~1,8mm bei 355m/s) - auf Wunsch erweitert, da das fuer reale
// Kabellaengen-/Bauteil-Unterschiede ggf. zu eng bemessen war.
#define MIC_OFS_MAX_NS  20000

// MIC_X/MIC_Y/micStandoffMm sind laufzeitveraenderlich (SET MICHALFX bzw.
// SET STANDOFFPAPER, siehe applyMicHalfX()/applyTargetGeometry() unten).
//
// Alle 3 Hardware-Diagnosetests (Mikrofon-Tausch GPIO27/32, LM339-Ausgang-
// Tausch GPIO25/33, GPIO32->GPIO35) sind zurueckgebaut - MIC_X wieder auf
// Normalstand. Test 3 betraf ohnehin nur AIR_PINS[4] (siehe dortigen
// Kommentar), nie die Geometrie hier (dasselbe Signal, nur anderer Pin).
static float MIC_X[NUM_AIR] = { -MIC_HALF_X, +MIC_HALF_X, -MIC_HALF_X, +MIC_HALF_X, -MIC_HALF_X, +MIC_HALF_X };
static float MIC_Y[NUM_AIR] = { -MIC_HALF_Y_PAPER, -MIC_HALF_Y_PAPER, +MIC_HALF_Y_PAPER, +MIC_HALF_Y_PAPER, 0.0f, 0.0f };
static float micStandoffMm = MIC_STANDOFF_PAPER;

// Setzt MIC_X[] passend zum per SET MICHALFX gewaehlten horizontalen
// Mic-Abstand zur Mittellinie. Wird beim Booten (nach loadConfig()) und bei
// jeder Aenderung von SET MICHALFX aufgerufen - wirkt sofort, kein Reboot
// noetig. Getrennt von applyTargetGeometry(), da der X-Abstand unabhaengig
// vom Standoff ist.
static void applyMicHalfX()
{
    MIC_X[0] = -cfg.micHalfXMm; MIC_X[1] = +cfg.micHalfXMm;
    MIC_X[2] = -cfg.micHalfXMm; MIC_X[3] = +cfg.micHalfXMm;
    MIC_X[4] = -cfg.micHalfXMm; MIC_X[5] = +cfg.micHalfXMm;
    updateRimMaxDist();   // SET ALGO=RIM, siehe dort
}

// Setzt MIC_Y[]/micStandoffMm (Papierscheiben-Geometrie, siehe
// MIC_HALF_Y_PAPER). Wird beim Booten (nach loadConfig()) und bei jeder
// Aenderung von SET STANDOFFPAPER aufgerufen - wirkt sofort, kein Reboot
// noetig. Bis Rev 4.11.0 gab es hier zusaetzlich einen per SET TARGET
// waehlbaren STEEL-Modus (direkter Beschuss einer Stahlplatte ohne Papier,
// andere Geometrie/Standoff) - seit Rev 4.11.1 entfernt, da nicht mehr
// genutzt. Das Piezo (Koerperschall-Trigger, siehe PIEZO_PIN) sitzt
// weiterhin auf einer Stahlplatte HINTER der Papierscheibe - das ist reine
// Trigger-Hardware, unabhaengig von dieser (jetzt fest auf Papier stehenden)
// Geometrie.
//
// Alle 3 Diagnose-Tests (AIR2<->AIR4-Mikrofontausch, GPIO25<->GPIO33-Tausch
// am LM339-Ausgang, GPIO32->GPIO35) sind zurueckgebaut - MIC_Y wieder auf
// Normalstand.
static void applyTargetGeometry()
{
    micStandoffMm = cfg.standoffPaperMm;
    MIC_Y[0] = -MIC_HALF_Y_PAPER; MIC_Y[1] = -MIC_HALF_Y_PAPER;
    MIC_Y[2] = +MIC_HALF_Y_PAPER; MIC_Y[3] = +MIC_HALF_Y_PAPER;
    MIC_Y[4] = 0.0f;              MIC_Y[5] = 0.0f;
    updateRimMaxDist();   // SET ALGO=RIM, siehe dort
}

// ---------------------------------------------------------------------------
// SET ALGO=RIM - Geometrie-/Zeitfenster-Hilfsfunktionen (shot_locator.h,
// processShotAnchored(), calCostRim()). Getrennt von den obigen apply*()-
// Funktionen, weil sie von mehreren davon abhaengen (MIC_X, MIC_Y/
// micStandoffMm UND cfg.soundSpeedMps/piezoMinUs).
// ---------------------------------------------------------------------------

// Groesste denkbare Distanz Einschlag<->Mikrofon bei der AKTUELLEN Geometrie
// (SET MICHALFX/STANDOFFPAPER) plus Sicherheitsmarge - begrenzt
// das Zeitfenster, in dem eine Flanke ueberhaupt noch als Direktschall in
// Frage kommt (shotloc::Geometry::maxDistMm). Wird bei jeder Aenderung der
// Geometrie neu berechnet (siehe applyMicHalfX()/applyTargetGeometry()) statt
// wie in schusserkennung-rev5.md fest auf 320mm zu setzen, da SET MICHALFX
// bis 300mm zulaesst (rechnerischer Extremfall > 320mm moeglich).
static void updateRimMaxDist()
{
    float maxX = 0.0f, maxY = 0.0f;
    for (int i = 0; i < NUM_AIR; i++) {
        if (fabsf(MIC_X[i]) > maxX) maxX = fabsf(MIC_X[i]);
        if (fabsf(MIC_Y[i]) > maxY) maxY = fabsf(MIC_Y[i]);
    }
    rimMaxDistMm = sqrtf(maxX * maxX + maxY * maxY
                        + micStandoffMm * micStandoffMm) + 20.0f;   // Marge
    updatePostWaitUs();   // haengt von rimMaxDistMm ab
}

// Wartezeit NACH dem Piezo-Anker, bis processShotAnchored() ausgewertet wird
// (siehe loop()): muss mindestens so lang sein, dass auch das am weitesten
// entfernte Mikrofon seine Flanke noch melden konnte, abzueglich der
// planmaessigen Mindestverzoegerung PIEZOMIN (das Piezo kommt ja selbst erst
// nach dieser Verzoegerung), plus Sicherheitsmarge. Wird bei jeder Aenderung
// von SET SOUNDSPEED/PIEZOMIN bzw. der Geometrie neu berechnet.
static void updatePostWaitUs()
{
    int32_t us = (int32_t)(rimMaxDistMm / (soundMpsActive * 1.0e-3f))
               - (int32_t)cfg.piezoMinUs + 150;
    postWaitUs = (us < 200) ? 200 : (uint32_t)us;
}

// Loest (x,y) aus 2 "Loese"-Mics relativ zu einer Referenz (Zeitnullpunkt)
// per Hyperbel-Trilateration (TDOA). Die Distanz Referenz<->Treffer wird
// als 3. Unbekannte mitgeloest (Linearisierung + quadratische Gleichung).
static bool solveAirPair(int ref, int a, int b, const int64_t tNs[NUM_AIR],
                          float *outX, float *outY, float *outD)
{
    const float Xr = MIC_X[ref], Yr = MIC_Y[ref];
    const float ra = (float)(tNs[a] - tNs[ref]) * soundMmPerNs;
    const float rb = (float)(tNs[b] - tNs[ref]) * soundMmPerNs;

    // Gleichung je Mic i: 2(Xi-Xr)x + 2(Yi-Yr)y + 2*ri*d = Ki-Kr-ri^2
    // (Ki = Xi^2+Yi^2; der Standoff Zi^2 kuerzt sich weg, da fuer alle
    // Mics identisch)
    const float A1 = 2.0f * (MIC_X[a] - Xr), B1 = 2.0f * (MIC_Y[a] - Yr), C1 = 2.0f * ra;
    const float D1 = (MIC_X[a]*MIC_X[a] + MIC_Y[a]*MIC_Y[a])
                    - (Xr*Xr + Yr*Yr) - ra*ra;
    const float A2 = 2.0f * (MIC_X[b] - Xr), B2 = 2.0f * (MIC_Y[b] - Yr), C2 = 2.0f * rb;
    const float D2 = (MIC_X[b]*MIC_X[b] + MIC_Y[b]*MIC_Y[b])
                    - (Xr*Xr + Yr*Yr) - rb*rb;

    // x = x0 + x1*d, y = y0 + y1*d (Cramer'sche Regel)
    const float det = A1*B2 - A2*B1;
    if (fabsf(det) < 1e-6f) return false;   // a, b kollinear mit ref

    const float x0 = (D1*B2 - D2*B1) / det;
    const float x1 = (C2*B1 - C1*B2) / det;
    const float y0 = (A1*D2 - A2*D1) / det;
    const float y1 = (A2*C1 - A1*C2) / det;

    // Einsetzen in d^2 = (x-Xr)^2 + (y-Yr)^2 + STANDOFF^2 -> quadratisch in d
    const float px = x0 - Xr, py = y0 - Yr;
    const float qa = 1.0f - x1*x1 - y1*y1;
    const float qb = -2.0f * (px*x1 + py*y1);
    const float qc = -(px*px + py*py + micStandoffMm*micStandoffMm);

    float d;
    if (fabsf(qa) < 1e-6f) {
        if (fabsf(qb) < 1e-6f) return false;
        d = -qc / qb;
    } else {
        const float disc = qb*qb - 4.0f*qa*qc;
        if (disc < 0.0f) return false;
        const float sq = sqrtf(disc);
        const float d1 = (-qb + sq) / (2.0f*qa);
        const float d2 = (-qb - sq) / (2.0f*qa);
        if (d1 > 0.0f && (d2 <= 0.0f || d1 < d2))  d = d1;   // kleinste
        else if (d2 > 0.0f)                         d = d2;   // positive Loesung
        else                                         return false;
    }
    if (d <= 0.0f) return false;

    *outX = x0 + x1*d;
    *outY = y0 + y1*d;
    *outD = d;
    return true;
}

// Bestimmt die Trefferposition aus den ersten Flanken der erfassten
// Luftmikrofone (3 bis NUM_AIR).
//
// Jede moegliche Dreier-Kombination der erfassten Mics wird direkt geloest
// (kleinste Zeit darunter = Referenz); die dabei NICHT beteiligten, aber
// ebenfalls erfassten Mics dienen als Kontrolle (mittlerer Rest-Fehler
// gegen die geloeste Position). Die Kombination mit dem kleinsten
// mittleren Rest-Fehler gewinnt. Bei genau 3 erfassten Mics gibt es keine
// Kontroll-Mics -> Rest-Fehler immer 0 (keine Redundanz). Je mehr Mics
// erfasst wurden, desto mehr Kontroll-Mics stehen zur Verfuegung und desto
// robuster ist die Ausreisser-Erkennung. Ein grosser Rest-Fehler (>> wenige
// mm) zeigt an, dass die Messung fuer diesen Schuss nicht in sich
// konsistent war (z.B. eine Flanke war ein Echo statt Direktschall) - das
// muss dann der Empfaenger (Stand-PC/Bediener) beurteilen.
// Maximale Zahl an Dreier-Kombinationen bei NUM_AIR=6 Mics: C(6,3) = 20
#define AIR_MAX_COMBOS  20

// Kugeldurchmesser-Korrektur (SET BSHIFTPCT/BSHIFTCAP) fuer eine geloeste
// Dreier-Kombination (x, y, d = Abstand zum Referenzmikrofon ref, beteiligte
// Mics ref/a/b): Verschiebung je NICHT beteiligtem Mikrofon in dessen
// Richtung um den gewichteten, gekappten signierten Rest-Fehler - dieselbe
// Formel fuer die Stufe-1-Loesung wie (seit Rev 4.12.0, SET CLUSTERCORR)
// fuer jeden einzelnen Schnittpunkt.
static void bulletShiftFor(float x, float y, float d, int ref, int a, int b,
                           const int all[NUM_AIR], int nAll, const int64_t tNs[NUM_AIR],
                           float *outShiftX, float *outShiftY)
{
    float shiftX = 0.0f, shiftY = 0.0f;
    for (int k = 0; k < nAll; k++) {
        const int m = all[k];
        if (m == ref || m == a || m == b) continue;
        const float mdx = MIC_X[m] - x, mdy = MIC_Y[m] - y;
        const float distM = sqrtf(mdx*mdx + mdy*mdy);
        if (distM < 1.0e-3f) continue;
        const float dc = sqrtf(mdx*mdx + mdy*mdy + micStandoffMm*micStandoffMm);
        const float rc = (float)(tNs[m] - tNs[ref]) * soundMmPerNs;
        const float signedResidual = dc - (d + rc);
        float shift = signedResidual * ((float)cfg.bulletShiftPct / 100.0f);
        shift = constrain(shift, -cfg.bulletShiftCapMm, cfg.bulletShiftCapMm);
        shiftX += shift * (mdx / distM);
        shiftY += shift * (mdy / distM);
    }
    *outShiftX = shiftX;
    *outShiftY = shiftY;
}

// ── SET LSQ (Rev 4.12.0): Ausgleichsrechnung ueber alle Mikrofone ─────────
// Laufweg vom naechstgelegenen Punkt des Lochrands (Radius SET LSQPELLETR um
// x/y) zu Mikrofon i, samt Ableitungen nach x/y.
static float lsqPathMm(int i, float x, float y, float *gx, float *gy)
{
    const float dx = x - MIC_X[i], dy = y - MIC_Y[i];
    float rho = sqrtf(dx*dx + dy*dy);
    if (rho < 1.0e-3f) rho = 1.0e-3f;
    float rr = rho - cfg.lsqPelletRMm;
    if (rr < 0.0f) rr = 0.0f;
    const float D = sqrtf(rr*rr + micStandoffMm*micStandoffMm);
    const float k = rr / (D * rho);
    *gx = k * dx;
    *gy = k * dy;
    return D;
}

// 3x3-System A*x = b per Cramer.
static bool lsqSolve3(const float A[3][3], const float b[3], float x[3])
{
    #define LSQ_DET(m) ((m)[0][0]*((m)[1][1]*(m)[2][2]-(m)[1][2]*(m)[2][1]) \
                      - (m)[0][1]*((m)[1][0]*(m)[2][2]-(m)[1][2]*(m)[2][0]) \
                      + (m)[0][2]*((m)[1][0]*(m)[2][1]-(m)[1][1]*(m)[2][0]))
    const float det = LSQ_DET(A);
    if (fabsf(det) < 1.0e-12f) return false;
    for (int k = 0; k < 3; k++) {
        float m[3][3];
        for (int r = 0; r < 3; r++)
            for (int c = 0; c < 3; c++) m[r][c] = (c == k) ? b[r] : A[r][c];
        x[k] = LSQ_DET(m) / det;
    }
    #undef LSQ_DET
    return true;
}

// Gauss-Newton ueber (x, y, s0), Residuum je Mikrofon r = D(x,y) - (s0 +
// c*t), Start in x0/y0. false bei singulaerem System oder wenn die Loesung
// mehr als 10mm vom Start wegwandert (Divergenz).
static bool lsqFit(const int64_t tNs[NUM_AIR], const bool use[NUM_AIR], float x0, float y0,
                   float *outX, float *outY, float res[NUM_AIR])
{
    float x = x0, y = y0, s0 = 0.0f, gx, gy;
    int n = 0;
    for (int i = 0; i < NUM_AIR; i++) {
        if (!use[i]) continue;
        s0 += lsqPathMm(i, x, y, &gx, &gy) - soundMmPerNs * (float)tNs[i];
        n++;
    }
    if (n < 4) return false;
    s0 /= (float)n;
    for (int it = 0; it < 6; it++) {
        float A[3][3] = {{0}}, b[3] = {0}, dlt[3];
        for (int i = 0; i < NUM_AIR; i++) {
            if (!use[i]) continue;
            const float D = lsqPathMm(i, x, y, &gx, &gy);
            const float r = D - (s0 + soundMmPerNs * (float)tNs[i]);
            const float J[3] = { gx, gy, -1.0f };
            for (int p = 0; p < 3; p++) {
                for (int q = 0; q < 3; q++) A[p][q] += J[p] * J[q];
                b[p] -= J[p] * r;
            }
        }
        if (!lsqSolve3(A, b, dlt)) return false;
        x += dlt[0]; y += dlt[1]; s0 += dlt[2];
        if (sqrtf(dlt[0]*dlt[0] + dlt[1]*dlt[1]) < 1.0e-5f) break;
    }
    if (sqrtf((x - x0)*(x - x0) + (y - y0)*(y - y0)) > 10.0f) return false;
    for (int i = 0; i < NUM_AIR; i++) {
        res[i] = use[i] ? lsqPathMm(i, x, y, &gx, &gy) - (s0 + soundMmPerNs * (float)tNs[i]) : 0.0f;
    }
    *outX = x;
    *outY = y;
    return true;
}

static float lsqRms(const float res[NUM_AIR], const bool use[NUM_AIR])
{
    float sum = 0.0f;
    int n = 0;
    for (int i = 0; i < NUM_AIR; i++) if (use[i]) { sum += res[i]*res[i]; n++; }
    return (n > 0) ? sqrtf(sum / (float)n) : 0.0f;
}

// SET LSQ: Endposition *x/*y (Start = Cluster-Mittelpunkt) per Ausgleichs-
// rechnung verfeinern. Echo-Schutz bei >= 5 Mics: die (bis zu) zwei Mics mit
// dem groessten Residuum probeweise weglassen - ein grob falsches Mikrofon
// verteilt seinen Fehler im Fit auf alle, eine reine Schwelle greift daher
// nicht zuverlaessig. Uebernommen wird eine Loesung ohne dieses Mikrofon,
// wenn ihr RMS-Residuum weniger als halb so gross ist. Bei Fehlschlag
// bleibt *x/*y unveraendert. Entspricht server/sim_posalgo.go lsqPosition().
static void lsqRefine(const int64_t tNs[NUM_AIR], const bool seen[NUM_AIR], float *x, float *y)
{
    bool use[NUM_AIR];
    int n = 0;
    for (int i = 0; i < NUM_AIR; i++) { use[i] = seen[i]; if (use[i]) n++; }
    float fx, fy, res[NUM_AIR];
    if (!lsqFit(tNs, use, *x, *y, &fx, &fy, res)) return;
    float bestRms = lsqRms(res, use);
    if (n >= 5 && bestRms > 0.1f) {
        // Mics nach absteigendem |Residuum|
        int order[NUM_AIR], nOrd = 0;
        for (int i = 0; i < NUM_AIR; i++) if (use[i]) order[nOrd++] = i;
        for (int i = 1; i < nOrd; i++)
            for (int j = i; j > 0 && fabsf(res[order[j]]) > fabsf(res[order[j-1]]); j--) {
                const int t = order[j]; order[j] = order[j-1]; order[j-1] = t;
            }
        for (int k = 0; k < 2 && k < nOrd; k++) {
            use[order[k]] = false;
            float x2, y2, r2[NUM_AIR];
            if (lsqFit(tNs, use, *x, *y, &x2, &y2, r2) && lsqRms(r2, use) < 0.5f * bestRms) {
                fx = x2; fy = y2;
                break;
            }
            use[order[k]] = true;
        }
    }
    *x = fx;
    *y = fy;
}

// outXmmPre/outYmmPre/outPrecisionMmPre/outClusterHitsPre (optional, NULL
// erlaubt): der ROHE Stufe-1-Wert VOR dem Verifizierungsschritt (siehe
// dortigen Kommentar) - nur fuer SET DEBUG=3 gedacht, damit sich beide
// Stufen miteinander vergleichen lassen.
//
// outResidualCorrMm (optional, NULL erlaubt): derselbe Rest-Fehler wie
// outResidualMm (Mittel des Abstands zu den NICHT an der Stufe-1-Loesung
// beteiligten Mics), aber MIT bereits angewandter Kugeldurchmesser-Korrektur
// (SET BSHIFTPCT/BSHIFTCAP, siehe dortigen Block) - outResidualMm selbst
// bleibt bewusst der reine Vorher-Wert (unveraendert fuer pos_res_um/das
// SET-DEBUG=3-Telegramm). Fuer die Kalibrierung (calCostClassic()) ist
// outResidualCorrMm das richtige Kostenmass: nur so wirkt sich SET
// BSHIFTPCT/BSHIFTCAP ueberhaupt auf die gefundenen Mic-Offsets aus - vorher
// wurde die Kalibrierung immer gegen den unkorrigierten Rest-Fehler
// optimiert und ignorierte die Korrektur dadurch komplett, unabhaengig vom
// eingestellten SET BSHIFTPCT-Wert.
static bool solveAirPosition(const int64_t tNs[NUM_AIR], const bool seen[NUM_AIR],
                              float clusterRadiusMm, bool emitCandidateDebug,
                              float *outXmm, float *outYmm, float *outResidualMm,
                              float *outPrecisionMm, int *outClusterHits,
                              float *outXmmPre, float *outYmmPre,
                              float *outPrecisionMmPre, int *outClusterHitsPre,
                              float *outResidualCorrMm)
{
    int all[NUM_AIR], nAll = 0;
    for (int i = 0; i < NUM_AIR; i++) if (seen[i]) all[nAll++] = i;
    if (nAll < 3) return false;

    // Kandidatenpositionen aller loesbaren Dreier-Kombinationen, fuer die
    // spaetere Praezisions-/Cluster-Auswertung (siehe unten).
    float candX[AIR_MAX_COMBOS], candY[AIR_MAX_COMBOS];
    float candD[AIR_MAX_COMBOS];                    // fuer SET CLUSTERCORR
    int8_t candRef[AIR_MAX_COMBOS], candA[AIR_MAX_COMBOS], candB[AIR_MAX_COMBOS];
    int   nCand = 0;

    bool  found = false;
    int   bestCandIdx = -1;
    float bestResidual = -1.0f, bestX = 0.0f, bestY = 0.0f, bestD = 0.0f;
    int   bestRef = -1, bestA = -1, bestB = -1;
    for (int i0 = 0; i0 < nAll; i0++) {
    for (int i1 = i0 + 1; i1 < nAll; i1++) {
    for (int i2 = i1 + 1; i2 < nAll; i2++) {
        const int trio[3] = { all[i0], all[i1], all[i2] };
        int ref = trio[0];
        for (int k = 1; k < 3; k++) if (tNs[trio[k]] < tNs[ref]) ref = trio[k];
        int a = -1, b = -1;
        for (int k = 0; k < 3; k++) {
            if (trio[k] == ref) continue;
            if (a < 0) a = trio[k]; else b = trio[k];
        }

        float x, y, d;
        if (!solveAirPair(ref, a, b, tNs, &x, &y, &d)) continue;

        int candIdx = -1;
        if (nCand < AIR_MAX_COMBOS) {
            candIdx = nCand;
            candX[nCand] = x; candY[nCand] = y; candD[nCand] = d;
            candRef[nCand] = (int8_t)ref; candA[nCand] = (int8_t)a; candB[nCand] = (int8_t)b;
            nCand++;
        }

        // SET DEBUG=3: jedes einzelne Kombinations-Ergebnis mit den dafuer
        // verwendeten Laufzeiten ausgeben (nur fuer echte Schuesse, nicht
        // waehrend der internen Kalibrier-Kostenauswertung - siehe calCost()).
        if (emitCandidateDebug) {
            emitf("{\"type\":\"cand\",\"seq\":%u,\"x_mm\":%.2f,\"y_mm\":%.2f,"
                  "\"ref\":%d,\"a\":%d,\"b\":%d,"
                  "\"t_ref_ns\":%lld,\"t_a_ns\":%lld,\"t_b_ns\":%lld}\n",
                  sequenceNo, x, y, ref, a, b,
                  (long long)tNs[ref], (long long)tNs[a], (long long)tNs[b]);
        }

        // Rest-Fehler gegen alle erfassten, an dieser Loesung nicht
        // beteiligten Mics (Mittelwert statt nur eines einzelnen Checks).
        float residualSum = 0.0f;
        int   nCheck = 0;
        for (int k = 0; k < nAll; k++) {
            const int m = all[k];
            if (m == ref || m == a || m == b) continue;
            const float dc = sqrtf((x - MIC_X[m])*(x - MIC_X[m])
                                  + (y - MIC_Y[m])*(y - MIC_Y[m])
                                  + micStandoffMm*micStandoffMm);
            const float rc = (float)(tNs[m] - tNs[ref]) * soundMmPerNs;
            residualSum += fabsf(dc - (d + rc));
            nCheck++;
        }
        const float residual = (nCheck > 0) ? (residualSum / nCheck) : 0.0f;

        if (!found || residual < bestResidual) {
            found = true;
            bestResidual = residual;
            bestX = x;
            bestY = y;
            bestD = d;
            bestRef = ref;
            bestA = a;
            bestB = b;
            bestCandIdx = candIdx;
        }
    }}}
    if (!found) return false;

    // Kugeldurchmesser-Korrektur (SET BSHIFTPCT/BSHIFTCAP): das Projektil
    // (ca. 4,5mm Durchmesser) strahlt den Einschlagsschall nicht exakt aus
    // dem Lochmittelpunkt ab, sondern eher von der dem jeweiligen Mikrofon
    // zugewandten Kugeloberflaeche. Fuer die an der Stufe-1-Loesung NICHT
    // beteiligten (verbleibenden) Mikrofone zeigt genau das der signierte
    // Rest-Fehler an: dc-(bestD+rc) > 0 heisst, das Mikrofon hat den Schall
    // FRUEHER empfangen als es die Punktquelle bestX/bestY vorhersagt -> die
    // tatsaechliche Quelle lag naeher an diesem Mikrofon. bestX/bestY wird
    // deshalb je verbleibendem Mikrofon in dessen Richtung verschoben,
    // gewichtet mit SET BSHIFTPCT (Default 30%) und je Mikrofon gedeckelt
    // auf SET BSHIFTCAP (Default 3.0mm) - bewusst NUR auf die Stufe-1-
    // Loesung angewendet (wirkt dadurch automatisch auch als neue Referenz
    // fuer den Verifizierungsschritt/Stufe 2 unten), 0% schaltet ab.
    if (cfg.bulletShiftPct > 0) {
        float shiftX, shiftY;
        bulletShiftFor(bestX, bestY, bestD, bestRef, bestA, bestB, all, nAll, tNs, &shiftX, &shiftY);
        bestX += shiftX;
        bestY += shiftY;
    }

    // outResidualCorrMm: derselbe Rest-Fehler wie bestResidual, aber MIT der
    // oben bereits angewandten Kugeldurchmesser-Korrektur (bestX/bestY) -
    // bestD ist der Abstand vom Referenzmikrofon zur ROHEN Position (aus
    // solveAirPair) und nach der Verschiebung nicht mehr gueltig, daher wird
    // der Abstand vom Referenzmikrofon zur KORRIGIERTEN Position hier neu
    // bestimmt (dRefCorr).
    if (outResidualCorrMm) {
        const float refDx = bestX - MIC_X[bestRef], refDy = bestY - MIC_Y[bestRef];
        const float dRefCorr = sqrtf(refDx*refDx + refDy*refDy + micStandoffMm*micStandoffMm);
        float corrResidualSum = 0.0f;
        int   corrNCheck = 0;
        for (int k = 0; k < nAll; k++) {
            const int m = all[k];
            if (m == bestRef || m == bestA || m == bestB) continue;
            const float dc = sqrtf((bestX - MIC_X[m])*(bestX - MIC_X[m])
                                  + (bestY - MIC_Y[m])*(bestY - MIC_Y[m])
                                  + micStandoffMm*micStandoffMm);
            const float rc = (float)(tNs[m] - tNs[bestRef]) * soundMmPerNs;
            corrResidualSum += fabsf(dc - (dRefCorr + rc));
            corrNCheck++;
        }
        *outResidualCorrMm = (corrNCheck > 0) ? (corrResidualSum / corrNCheck) : 0.0f;
    }

    // Praezision: quadratisch gemittelte Abweichung (RMS) der bis zu 2
    // NAECHSTGELEGENEN zusaetzlichen Kandidatenpositionen (aus den uebrigen
    // Mic-Kombinationen) von der gewaehlten Loesung - bewusst nur die besten
    // 2, damit einzelne weit abweichende Ausreisser-Kombinationen (z.B.
    // durch Echos) den Wert nicht dominieren. Bei genau 3 erfassten Mics
    // gibt es keine zusaetzliche Kombination -> precision immer 0.
    // cluster_hits zaehlt weiterhin ALLE Kandidaten innerhalb von
    // clusterRadiusMm um die Loesung (SET RADIUS).
    // Stufe 1 (Rohwert): Referenz = die einzelne beste Dreier-Kombination.
    float d1 = -1.0f, d2 = -1.0f;   // zwei kleinste Abstaende (mm)
    int   inRadius = 0;
    for (int i = 0; i < nCand; i++) {
        const float dx = candX[i] - bestX, dy = candY[i] - bestY;
        const float dist = sqrtf(dx*dx + dy*dy);
        if (dist <= clusterRadiusMm) inRadius++;
        if (i == bestCandIdx) continue;
        if (d1 < 0.0f || dist < d1)      { d2 = d1; d1 = dist; }
        else if (d2 < 0.0f || dist < d2) { d2 = dist; }
    }
    int   nNear = (d1 >= 0.0f ? 1 : 0) + (d2 >= 0.0f ? 1 : 0);
    float sumSq = (d1 >= 0.0f ? d1*d1 : 0.0f) + (d2 >= 0.0f ? d2*d2 : 0.0f);
    const float precisionPre    = (nNear > 0) ? sqrtf(sumSq / (float)nNear) : 0.0f;
    const int   clusterHitsPre  = inRadius;

    // Verifizierungsschritt (Stufe 2): Das Projektil hinterlaesst ein Loch
    // mit ca. 4,5mm Durchmesser - je nach beteiligter Mic-Kombination kann
    // der Schall von einer leicht anderen Stelle des Lochrands ausgegangen
    // sein. SET RADIUS ist deshalb bewusst groesszuegiger als reines
    // Mess-Rauschen bemessen (deckt den Lochdurchmesser mit ab). Statt die
    // Stufe-1-Loesung (nur EINE Dreier-Kombination) als endgueltiges
    // Ergebnis zu verwenden, wird hier der Mittelpunkt ALLER Kandidaten
    // innerhalb von clusterRadiusMm um die Stufe-1-Loesung gebildet und als
    // neue Referenz gesetzt - das ist ein robusterer Schaetzer, da er alle
    // ohnehin uebereinstimmenden Kombinationen mittelt statt sich auf eine
    // einzelne zu verlassen. cluster_hits/precision_um werden relativ zu
    // dieser neuen Referenz neu berechnet (pos_res_um bleibt unveraendert
    // von Stufe 1, da es die Konsistenz der urspruenglichen Loesung
    // beschreibt, nicht die des gemittelten Punkts).
    //
    // Rev 4.12.0: Punktmenge und Ablauf per SET CLUSTERCORR/CLUSTERITER/LSQ
    // (siehe Header). ptX/ptY = die fuer die Clusterbildung verwendeten
    // Schnittpunkte: roh, nur die Stufe-1-Loesung korrigiert (bisher,
    // CLUSTERCORR=0) oder alle korrigiert (CLUSTERCORR=1).
    float ptX[AIR_MAX_COMBOS], ptY[AIR_MAX_COMBOS];
    const bool corrAll = cfg.clusterCorr && cfg.bulletShiftPct > 0;
    for (int i = 0; i < nCand; i++) {
        if (i == bestCandIdx) { ptX[i] = bestX; ptY[i] = bestY; continue; }
        ptX[i] = candX[i];
        ptY[i] = candY[i];
        if (corrAll) {
            float sx, sy;
            bulletShiftFor(candX[i], candY[i], candD[i], candRef[i], candA[i], candB[i],
                           all, nAll, tNs, &sx, &sy);
            ptX[i] += sx;
            ptY[i] += sy;
        }
    }
    // Mittelwert aller Punkte im Radius um die Stufe-1-Loesung, danach
    // (CLUSTERITER) erneut um den jeweils neuen Mittelpunkt (Mean-Shift).
    // Die Stufe-1-Loesung liegt im ersten Durchgang im Abstand 0 und zaehlt
    // damit wie bisher selbst mit.
    float verX = bestX, verY = bestY;
    for (int pass = 0; pass <= (int)cfg.clusterIter; pass++) {
        float sumX = 0.0f, sumY = 0.0f;
        int   nSum = 0;
        // Stufe-1-Loesung zuerst summieren (k=-1), dann die uebrigen in
        // Index-Reihenfolge - gleiche Additionsreihenfolge wie bis Rev 4.11.5,
        // damit CLUSTERCORR=0/CLUSTERITER=0 bitgenau dasselbe Ergebnis liefert.
        for (int k = -1; k < nCand; k++) {
            const int i = (k < 0) ? bestCandIdx : k;
            if (i < 0 || (k >= 0 && i == bestCandIdx)) continue;
            const float dx = ptX[i] - verX, dy = ptY[i] - verY;
            if (sqrtf(dx*dx + dy*dy) <= clusterRadiusMm) {
                sumX += ptX[i];
                sumY += ptY[i];
                nSum++;
            }
        }
        if (nSum == 0) break;
        const float nx = sumX / (float)nSum, ny = sumY / (float)nSum;
        const float moved = sqrtf((nx - verX)*(nx - verX) + (ny - verY)*(ny - verY));
        verX = nx;
        verY = ny;
        if (moved < 1.0e-4f) break;
    }
    if (cfg.lsq) lsqRefine(tNs, seen, &verX, &verY);

    // cluster_hits/precision relativ zur Endposition - bei CLUSTERCORR=1
    // gegen die korrigierten, sonst (wie bisher) gegen die rohen Schnittpunkte.
    float vd1 = -1.0f, vd2 = -1.0f;
    int   verInRadius = 0;
    for (int i = 0; i < nCand; i++) {
        const float px = corrAll ? ptX[i] : candX[i];
        const float py = corrAll ? ptY[i] : candY[i];
        const float dx = px - verX, dy = py - verY;
        const float dist = sqrtf(dx*dx + dy*dy);
        if (dist <= clusterRadiusMm) verInRadius++;
        if (vd1 < 0.0f || dist < vd1)      { vd2 = vd1; vd1 = dist; }
        else if (vd2 < 0.0f || dist < vd2) { vd2 = dist; }
    }
    const int   verNNear = (vd1 >= 0.0f ? 1 : 0) + (vd2 >= 0.0f ? 1 : 0);
    const float verSumSq = (vd1 >= 0.0f ? vd1*vd1 : 0.0f) + (vd2 >= 0.0f ? vd2*vd2 : 0.0f);

    *outXmm         = verX;
    *outYmm         = verY;
    *outResidualMm  = bestResidual;
    *outPrecisionMm = (verNNear > 0) ? sqrtf(verSumSq / (float)verNNear) : 0.0f;
    *outClusterHits = verInRadius;
    if (outXmmPre)         *outXmmPre = bestX;
    if (outYmmPre)         *outYmmPre = bestY;
    if (outPrecisionMmPre) *outPrecisionMmPre = precisionPre;
    if (outClusterHitsPre) *outClusterHitsPre = clusterHitsPre;
    return true;
}

// ---------------------------------------------------------------------------
// Timing-Offset-Kalibrierung (CAL START)
// ---------------------------------------------------------------------------
// Idee: Elektronik-/Kabellaufzeit-Unterschiede zwischen den Mikrofon-Kanaelen
// (LM339-Komparator-Schwelle, Kabellaenge) aeussern sich als naeherungsweise
// KONSTANTER Timing-Fehler je Mikrofon (0.343 mm/ns - schon wenige ns
// entsprechen mm-Abweichungen) und dominieren typischerweise die mechanische
// Einbautoleranz der Mikrofone. calCost()/runCalibration() bestimmen daher
// GEZIELT nur einen skalaren ns-Offset je Mikrofon, keine 3D-Neuvermessung
// der Mic-Positionen - letzteres waere ein deutlich schwierigeres
// Selbstlokalisierungsproblem (unbekannte Mic- UND Schusspositionen
// gleichzeitig) und mit wenigen Schuessen an unbekannter Position numerisch
// riskant.
//
// Eichfreiheitsgrad: Eine gemeinsame Konstante zu ALLEN Offsets addiert
// aendert an keiner TDOA-Differenz etwas (sie kuerzt sich in jeder
// Zeitdifferenz zwischen zwei Mics weg) - Offset[0] wird deshalb fix auf 0
// gehalten, alle anderen Offsets sind relativ dazu.
//
// Optimierung: Pattern-Search / Koordinatenabstieg (kein Matrix-Solver
// noetig), abwechselnd fuer jedes Mic-Offset UND (seit Rev 4.4) fuer die
// Schallgeschwindigkeit (SET SOUNDSPEED): ein kleiner Schritt in beide
// Richtungen wird probiert und die Aenderung uebernommen, die die jeweiligen
// Kosten am staerksten senkt; die Schrittweite wird pro Runde halbiert
// (grob -> fein).
//
// Die Mic-Offsets nutzen als Kosten den solveAirPosition()-Rest-Fehler
// (Konsistenz der Loesung gegen die NICHT an ihr beteiligten Mics) - und
// zwar den Rest-Fehler NACH Anwendung der Kugeldurchmesser-Korrektur (SET
// BSHIFTPCT/BSHIFTCAP), nicht den reinen Vorher-Wert. Vorher (bis inkl. Rev
// 4.11.3) wurde hier der unkorrigierte Rest-Fehler verwendet, wodurch SET
// BSHIFTPCT/BSHIFTCAP komplett wirkungslos fuer die Kalibrier-Suche war -
// egal welcher Wert eingestellt war, kamen immer dieselben Mic-Offsets
// heraus. Bewusst NICHT stattdessen die Kandidaten-Clusterbreite
// (precisionMm) als Kostenmass verwendet: die haengt nur von den zwei
// naechstgelegenen Kandidaten ab und ist dadurch genauso "gamebar" wie das
// analoge minMics=3-Problem bei calCostRim() - die Suche kann dann einen
// zufaellig eng geclusterten, aber falschen Punkt finden statt der echten
// Kalibrierung (im Simulator-Regressionstest nachgewiesen).
// SET SOUNDSPEED wird bewusst NICHT mitkalibriert (siehe Rev-4.5.1-Hinweis
// ganz oben) - blieb in der Praxis wiederholt bei unplausiblen Werten haengen
// (zuletzt 363 m/s bei nur 5 Kalibrier-Schuessen) und bleibt daher ein rein
// manueller Parameter.

// ---------------------------------------------------------------------------
// Amplituden-Korrektur ueber die Pulsbreite (Rev 4.14.0, SET WALKK)
// ---------------------------------------------------------------------------
// Der LM339 schaltet, sobald das Mic-Signal die Schwelle ueberschreitet. Ein
// leises Signal (fernes Mic, schraeger Einfall) braucht dafuer laenger als
// ein lautes - die Ausloesung kommt amplitudenabhaengig SPAETER, positions-
// abhaengig und deshalb nicht per SET OFS wegkalibrierbar
// (schusserkennung-rev5.md Abschnitt 4, Punkt 4). freETarget loest das mit
// einer zweiten Schwelle je Kanal; ohne Zusatzhardware dient hier die
// Pulsbreite w (Zeit ueber der Schwelle) als Amplitudenmass:
//   lautes Signal -> frueh ueber, spaet unter die Schwelle -> breiter Puls
// Fuer eine symmetrische Halbwelle liegt die Pulsmitte (t + w/2) unabhaengig
// von der Amplitude immer auf dem Scheitel - daher walkK = 0.5 als
// physikalisch begruendeter Startwert. Reale Signale sind nicht exakt
// symmetrisch, deshalb ist walkK einstellbar bzw. per CAL START (SET
// WALKCAL=1) bestimmbar.
//
// Korrektur je Mic: t += walkK * (w - w_mittel), w_mittel = Mittel aller
// gueltigen Breiten DIESES Schusses. Ein fuer alle Mics gleicher Anteil
// faellt bei TDOA ohnehin heraus; durch den Bezug auf den Mittelwert
// verschiebt sich aber auch t0 nicht (wichtig fuer das Piezo-Gate bei
// ALGO=RIM). Mics ohne gueltige Breite (ISR-Betrieb, TESTSHOOT, fehlende/
// unplausible fallende Flanke) bleiben unkorrigiert - bei walkK=0 oder ohne
// MCPWM verhaelt sich alles exakt wie bisher.

#define WALK_MIN_NS  300   // kuerzere "Pulse" sind Komparator-Preller

// Momentaufnahme der Flanken-Ringe fuer die Auswertung. static statt auf dem
// Loop-Stack (3 KB), da nur aus loop() benutzt (processShot()/
// processShotAnchored(), nicht reentrant).
struct EdgeSnapshot {
    uint32_t riseCC[NUM_AIR][EDGE_RING];
    uint32_t riseUs[NUM_AIR][EDGE_RING];
    uint32_t fallCC[NUM_AIR][FALL_RING];
    uint32_t fallUs[NUM_AIR][FALL_RING];
    uint32_t nowUs;
};
static EdgeSnapshot edgeSnap;

// Muss unter noInterrupts() aufgerufen werden.
static void takeEdgeSnapshot()
{
    memcpy((void *)edgeSnap.riseCC, (const void *)ringCC, sizeof(edgeSnap.riseCC));
    memcpy((void *)edgeSnap.riseUs, (const void *)ringUs, sizeof(edgeSnap.riseUs));
    memcpy((void *)edgeSnap.fallCC, (const void *)fallCC, sizeof(edgeSnap.fallCC));
    memcpy((void *)edgeSnap.fallUs, (const void *)fallUs, sizeof(edgeSnap.fallUs));
    edgeSnap.nowUs = (uint32_t)esp_timer_get_time();
}

// Pulsbreite in ns zur steigenden Flanke rise (CPU-Zyklen) auf Mic i, -1 wenn
// keine plausible fallende Flanke vorliegt. Gesucht wird die LETZTE fallende
// Flanke vor der naechsten gespeicherten steigenden Flanke desselben Mics
// (hoechstens SET WALKMAX spaeter) - Preller an der Schwelle (steigende
// Flanken innerhalb der 20us-Totzeit landen nicht im Ring) verlaengern den
// Puls dadurch nicht kuenstlich und verkuerzen ihn auch nicht.
// Altersgrenze 100ms gegen Mehrdeutigkeit durch den Zyklus-Ueberlauf (17,9s).
static int32_t edgeWidthNs(int i, uint32_t rise)
{
    uint32_t limit = cfg.walkMaxUs * cpuMHz;
    for (int k = 0; k < EDGE_RING; k++) {
        if (edgeSnap.riseUs[i][k] == 0) continue;
        if (edgeSnap.nowUs - edgeSnap.riseUs[i][k] > 100000UL) continue;
        const int32_t d = (int32_t)(edgeSnap.riseCC[i][k] - rise);
        if (d > 0 && (uint32_t)d < limit) limit = (uint32_t)d;
    }
    int32_t best = -1;
    for (int k = 0; k < FALL_RING; k++) {
        if (edgeSnap.fallUs[i][k] == 0) continue;
        if (edgeSnap.nowUs - edgeSnap.fallUs[i][k] > 100000UL) continue;
        const int32_t d = (int32_t)(edgeSnap.fallCC[i][k] - rise);
        if (d > 0 && (uint32_t)d < limit && d > best) best = d;
    }
    if (best < (int32_t)((uint32_t)WALK_MIN_NS * cpuMHz / 1000U)) return -1;
    return (int32_t)((int64_t)best * 1000 / (int64_t)cpuMHz);
}

// Korrektur je Mic in ns (siehe Blockkommentar oben). use[i]=false bzw.
// wNs[i]<0: keine Korrektur fuer dieses Mic, geht auch nicht ins Mittel ein.
static void walkDeltas(const int32_t wNs[NUM_AIR], const bool use[NUM_AIR],
                       float k, float outNs[NUM_AIR])
{
    float sum = 0.0f;
    int   n   = 0;
    for (int i = 0; i < NUM_AIR; i++) {
        if (use[i] && wNs[i] >= 0) { sum += (float)wNs[i]; n++; }
    }
    const float mean = n > 0 ? sum / (float)n : 0.0f;
    for (int i = 0; i < NUM_AIR; i++) {
        outNs[i] = (k != 0.0f && use[i] && wNs[i] >= 0)
                 ? k * ((float)wNs[i] - mean) : 0.0f;
    }
}

// Kandidatenflanken eines Mics aufsteigend sortieren, Breiten mitfuehren
// (n klein, <= EDGE_RING) - shot_locator.h nimmt die ersten 2 je Mic.
static void sortEdgesWithWidth(float t[], int32_t w[], int n)
{
    for (int a = 1; a < n; a++) {
        const float vt = t[a]; const int32_t vw = w[a]; int b = a - 1;
        while (b >= 0 && t[b] > vt) { t[b + 1] = t[b]; w[b + 1] = w[b]; b--; }
        t[b + 1] = vt; w[b + 1] = vw;
    }
}

// ALGO=RIM: aus rohen Kandidatenflanken (ns relativ zum Piezo, OHNE Offset)
// die Eingabe fuer shotloc::locate() bauen - gemeinsam fuer
// processShotAnchored() und calCostRim(). Offsets abziehen, Pulsbreiten-
// Korrektur anwenden (Bezug: Mittel der Breiten der jeweils FRUEHESTEN
// Flanke je Mic - Stellvertreter des Direktschalls, Echos/Nachschwinger
// sollen das Mittel nicht verziehen), aufsteigend sortieren und auf
// MAX_EDGES kuerzen. t/w sind Arbeitskopien des Aufrufers und werden
// ueberschrieben; danach gehoert w[i][k] zu ed->t[i][k].
static void rimBuildEdges(float t[NUM_AIR][EDGE_RING], int32_t w[NUM_AIR][EDGE_RING],
                          const int n[NUM_AIR], const bool enabled[NUM_AIR],
                          const float ofsNs[NUM_AIR], float k, shotloc::Edges *ed)
{
    float sum = 0.0f;
    int   cnt = 0;
    for (int i = 0; i < NUM_AIR; i++) {
        int first = -1;
        for (int j = 0; j < n[i]; j++) {
            t[i][j] -= ofsNs[i];
            if (first < 0 || t[i][j] < t[i][first]) first = j;
        }
        if (enabled[i] && first >= 0 && w[i][first] >= 0) {
            sum += (float)w[i][first];
            cnt++;
        }
    }
    const float mean = cnt > 0 ? sum / (float)cnt : 0.0f;
    for (int i = 0; i < NUM_AIR; i++) {
        ed->enabled[i] = enabled[i];
        ed->n[i] = 0;
        if (!enabled[i]) continue;
        if (k != 0.0f && cnt > 0) {
            for (int j = 0; j < n[i]; j++) {
                if (w[i][j] >= 0) t[i][j] += k * ((float)w[i][j] - mean);
            }
        }
        sortEdgesWithWidth(t[i], w[i], n[i]);
        for (int j = 0; j < n[i] && ed->n[i] < shotloc::MAX_EDGES; j++) {
            ed->t[i][ed->n[i]++] = t[i][j];
        }
    }
}

// Haengt ,"air_w_ns":[...] (Pulsbreite der ersten bzw. bei ALGO=RIM der
// fruehesten Kandidatenflanke je Mic, null = keine) an ein shot-Telegramm an
// - nur bei aktivem MCPWM-Capture, im ISR-Betrieb bleibt das Telegramm
// unveraendert. Liefert die Zahl geschriebener Zeichen, nie mehr als Platz da ist.
static int appendAirWidths(char *buf, size_t size, const int32_t wNs[NUM_AIR])
{
    if (!capActive || size < 64) return 0;
    int n = snprintf(buf, size, ",\"air_w_ns\":[");
    for (int i = 0; i < NUM_AIR && n < (int)size - 16; i++) {
        if (wNs[i] >= 0) n += snprintf(buf + n, size - n, "%s%ld", i > 0 ? "," : "", (long)wNs[i]);
        else             n += snprintf(buf + n, size - n, "%snull", i > 0 ? "," : "");
    }
    if (n < (int)size - 2) n += snprintf(buf + n, size - n, "]");
    return n < (int)size ? n : (int)size - 1;
}

// ---------------------------------------------------------------------------
// Rohdaten-Telegramm fuer den Server-Simulator (Rev 4.16.0, SET RAWLOG=1)
// ---------------------------------------------------------------------------
// Zu jedem GUELTIGEN Schuss (direkt nach dem "shot"-Telegramm, gleiche seq)
// eine Zeile mit allen steigenden und fallenden Flanken aus den Ringpuffern
// in einem Fenster um den Schuss - UNKORRIGIERT (kein Offset, keine Puls-
// breiten-Korrektur) und ohne die Fenster-/Anzahl-Grenzen der Auswertung,
// dazu die zum Schusszeitpunkt aktiven Parameter. Damit kann der Simulator
// beide Auswertepfade mit beliebigen Parametern nachrechnen. Fallende
// Flanken statt fertiger Pulsbreiten, damit auch eine andere Breiten-Regel
// bzw. ein anderes WALKMAX exakt nachgerechnet werden kann (Regel der
// Firmware siehe edgeWidthNs()).
// Abgelehnte Schuesse bekommen bewusst KEIN raw-Telegramm (Reject-Verhalten
// unveraendert). Das Telegramm wird nicht im Offline-Ringpuffer gehalten
// (der nimmt nur shot/reject, siehe emitLine()).
#define RAW_EXTRA_NS     500000.0f  // Flanken bis 500us nach dem Fenster (Pulsende/
                                    // naechste Flanke fuer die Breiten-Regel)
#define RAW_MAX_PER_MIC  16         // je Mic und Flankenart, Rest -> "truncated":1

// Flanken eines Ringpuffers im Fenster [ref-preNs, ref+postNs] in ns relativ
// zu refCC, aufsteigend. out muss RAW_MAX_PER_MIC Werte fassen (Zeiger statt
// Array-Groesse in der Signatur: die automatisch erzeugten Arduino-Prototypen
// stehen vor dem #define). Liefert die Anzahl; *cut = true, wenn gekappt.
static int rawCollect(const uint32_t *cc, const uint32_t *us, int ringLen,
                      uint32_t refCC, float preNs, float postNs,
                      int32_t *out, bool *cut)
{
    float   t[EDGE_RING > FALL_RING ? EDGE_RING : FALL_RING];
    int32_t dummy[EDGE_RING > FALL_RING ? EDGE_RING : FALL_RING];
    int n = 0;
    for (int k = 0; k < ringLen; k++) {
        if (us[k] == 0) continue;                                  // nie beschrieben
        if (edgeSnap.nowUs - us[k] > 100000UL) continue;           // veraltet (Zyklus-Wrap)
        const float tNs = (float)(int32_t)(cc[k] - refCC) * 1000.0f / (float)cpuMHz;
        if (tNs < -preNs || tNs > postNs) continue;
        t[n] = tNs; dummy[n] = 0; n++;
    }
    sortEdgesWithWidth(t, dummy, n);
    if (n > RAW_MAX_PER_MIC) { n = RAW_MAX_PER_MIC; *cut = true; }
    for (int k = 0; k < n; k++) out[k] = (int32_t)lroundf(t[k]);
    return n;
}

// Wert fuer das Feld "raw_follows" im shot-Telegramm: 1, wenn direkt als
// naechste Zeile ein raw-Telegramm auf DEMSELBEN Weg folgt - der Host kann
// dann mit der Anzeige darauf warten. Bei aktivem WLAN ohne TCP-Verbindung
// wird der shot im Offline-Puffer gehalten und spaeter nachgeliefert, das
// raw-Telegramm aber nicht (nur Serial) - dann 0, sonst wartete der Host
// beim Nachliefern vergeblich. Ohne WLAN gehen beide nur ueber Serial: 1.
static bool rawWillFollow()
{
    return cfg.rawLog && (!wifiEnabled || tcp.connected());
}

// refCC: Zeitnullpunkt (CLASSIC: erste Flanke wie air_ns, RIM: Piezo).
// winPreNs/winPostNs: Auswertefenster relativ zu refCC (Flanken werden bis
// winPostNs + RAW_EXTRA_NS mitgeschickt). Liest edgeSnap - muss nach
// takeEdgeSnapshot() des laufenden Schusses aufgerufen werden.
static void emitRawTelegram(const char *algo, uint32_t refCC, float winPreNs, float winPostNs,
                            bool piezoSeen, int64_t piezoNs, uint64_t tsUs,
                            const char *synSuffix)
{
    static char line[2600];   // static: nur aus loop(), nicht reentrant
    const size_t sz = sizeof(line);
    bool cut = false;
    int  n = snprintf(line, sz,
                      "{\"type\":\"raw\",\"seq\":%u,\"algo\":\"%s\",\"ref\":\"%s\","
                      "\"capture\":\"%s\",\"sound_act_mps\":%.2f,\"walk_k\":%.3f,"
                      "\"walk_max_us\":%u,\"win_pre_ns\":%ld,\"win_post_ns\":%ld,",
                      sequenceNo, algo, strcmp(algo, "rim") == 0 ? "piezo" : "first",
                      capActive ? "mcpwm" : "isr", soundMpsActive, cfg.walkK,
                      (unsigned)cfg.walkMaxUs, lroundf(winPreNs), lroundf(winPostNs));
    n += snprintf(line + n, sz - n, "\"ofs_ns\":[");
    for (int i = 0; i < NUM_AIR; i++)
        n += snprintf(line + n, sz - n, "%s%ld", i ? "," : "", (long)cfg.micOffsetNs[i]);
    n += snprintf(line + n, sz - n, "],\"mic_en\":[");
    for (int i = 0; i < NUM_AIR; i++)
        n += snprintf(line + n, sz - n, "%s%d", i ? "," : "", cfg.micEnabled[i] ? 1 : 0);
    if (piezoSeen) n += snprintf(line + n, sz - n, "],\"piezo_ns\":%lld", (long long)piezoNs);
    else           n += snprintf(line + n, sz - n, "],\"piezo_ns\":null");

    // Groesse: 6 Mics x 2 Flankenarten x max. 16 Werte x max. ~9 Zeichen
    // = ~1730 Zeichen + Kopf ~300 - passt sicher in line[2600].
    const float postNs = winPostNs + RAW_EXTRA_NS;
    for (int kind = 0; kind < 2; kind++) {
        n += snprintf(line + n, sz - n, kind == 0 ? ",\"rise_ns\":[" : "],\"fall_ns\":[");
        for (int i = 0; i < NUM_AIR; i++) {
            int32_t v[RAW_MAX_PER_MIC];
            const int cnt = (kind == 0)
                ? rawCollect(edgeSnap.riseCC[i], edgeSnap.riseUs[i], EDGE_RING,
                             refCC, winPreNs, postNs, v, &cut)
                : rawCollect(edgeSnap.fallCC[i], edgeSnap.fallUs[i], FALL_RING,
                             refCC, winPreNs, postNs, v, &cut);
            n += snprintf(line + n, sz - n, "%s[", i ? "," : "");
            for (int k = 0; k < cnt; k++)
                n += snprintf(line + n, sz - n, "%s%ld", k ? "," : "", (long)v[k]);
            n += snprintf(line + n, sz - n, "]");
        }
    }
    snprintf(line + n, sz - n, "],\"truncated\":%d,\"ts\":%llu%s}\n",
             cut ? 1 : 0, (unsigned long long)(tsUs / 1000ULL), synSuffix);
    emitLine(line);
}

// Kosten der aktuell angenommenen Offsets ueber alle gesammelten Kalibrier-
// Schuesse (Summe der Rest-Fehler NACH Kugeldurchmesser-Korrektur, siehe
// solveAirPosition()/outResidualCorrMm - unveraendert durch den
// Verifizierungsschritt). Schuesse, die damit keine Loesung mehr ergeben
// (geometrisch entartet), werden mit einem Strafwert belegt statt ignoriert
// zu werden.
static float calCostClassic(const float offsets[NUM_AIR], float walkK)
{
    float total = 0.0f;
    for (int k = 0; k < calCollected; k++) {
        float walk[NUM_AIR];
        walkDeltas(calWNs[k], calSeenBuf[k], walkK, walk);
        int64_t corrected[NUM_AIR];
        for (int i = 0; i < NUM_AIR; i++) {
            corrected[i] = calSeenBuf[k][i]
                         ? calRawNs[k][i] - (int64_t)lroundf(offsets[i] - walk[i])
                         : 0;
        }
        float x, y, res, prec, resCorr;
        int   hitsN;
        if (solveAirPosition(corrected, calSeenBuf[k],
                              (float)cfg.clusterRadiusUm / 1000.0f, false,
                              &x, &y, &res, &prec, &hitsN,
                              nullptr, nullptr, nullptr, nullptr,
                              &resCorr)) {
            total += resCorr;
        } else {
            total += 1000.0f;   // Strafe: macht Schuss unloesbar
        }
    }
    return total;
}

// SET ALGO=RIM-Gegenstueck zu calCostClassic(): Kosten = Summe der
// quadrierten RMS-Residuen (ns^2, siehe shot_locator.h::Result::rmsNs) ueber
// alle gesammelten Kalibrier-Schuesse, mit den zu testenden Offsets VOR dem
// Fit abgezogen (siehe schusserkennung-rev5.md Abschnitt 3.4). minMics=3
// (nicht cfg.minMics) - reine Kosten-Bewertung waehrend der Suche, analog zu
// calCostClassic()/solveAirPosition() (die ebenfalls nur 3 Mics verlangen).
static float calCostRim(const float offsets[NUM_AIR], float walkK)
{
    // Arbeitskopien fuer rimBuildEdges() - static (Loop-Stack), eigene Puffer,
    // da runCalibration() aus processShotAnchored() heraus laeuft, das seine
    // eigenen Puffer danach noch braucht.
    static float   wt[NUM_AIR][EDGE_RING];
    static int32_t ww[NUM_AIR][EDGE_RING];
    shotloc::Geometry g;
    for (int i = 0; i < NUM_AIR; i++) { g.micX[i] = MIC_X[i]; g.micY[i] = MIC_Y[i]; }
    g.standoffMm     = micStandoffMm;
    g.soundMmPerNs   = soundMmPerNs;
    g.pelletRadiusMm = cfg.pelletRadiusMm;
    g.maxDistMm      = rimMaxDistMm;

    shotloc::Gate gate;
    gate.active  = true;
    gate.t0MinNs = -(float)cfg.piezoMaxUs * 1000.0f;
    gate.t0MaxNs = -(float)cfg.piezoMinUs * 1000.0f;
    shotloc::Params params;
    params.minMics = 3;

    float total = 0.0f;
    for (int k = 0; k < calCollected; k++) {
        shotloc::Edges e{};
        int cnt[NUM_AIR];
        for (int i = 0; i < NUM_AIR; i++) {
            cnt[i] = calEdgeCountRim[k][i];
            for (int j = 0; j < cnt[i]; j++) {
                wt[i][j] = calEdgeNsRim[k][i][j];
                ww[i][j] = calEdgeWRim[k][i][j];
            }
        }
        rimBuildEdges(wt, ww, cnt, calEnabledRim[k], offsets, walkK, &e);
        shotloc::Result r;
        if (shotloc::locate(g, e, gate, params, &r) && r.valid) {
            total += r.rmsNs * r.rmsNs;
        } else {
            total += 5000.0f * 5000.0f;   // Strafe (ns^2): macht Schuss unloesbar
        }
    }
    return total;
}

// Dispatcher: runCalibration() (unten) ruft ausschliesslich calCost() auf
// und bleibt dadurch fuer beide Auswertepfade unveraendert - kalibriert wird
// jeweils der Pfad, der gerade per SET ALGO aktiv ist.
static float calCost(const float offsets[NUM_AIR], float walkK)
{
    return (cfg.algoMode == ALGO_RIM) ? calCostRim(offsets, walkK)
                                      : calCostClassic(offsets, walkK);
}

static void runCalibration()
{
    float offsets[NUM_AIR];
    for (int i = 0; i < NUM_AIR; i++) offsets[i] = 0.0f;   // Neukalibrierung

    // SET WALKCAL=1 (Rev 4.14.0): walkK als zusaetzliche Koordinate im
    // selben Abstiegsverfahren, Start beim aktuellen SET WALKK, Bereich 0-1.
    // Trennbar von den Offsets, weil diese je Mic konstant sind, die Puls-
    // breiten-Korrektur aber von Schuss zu Schuss variiert. Ohne MCPWM gibt
    // es keine Pulsbreiten - dann bleibt walkK unveraendert.
    const bool fitWalk = cfg.walkCal && capActive;
    float      walkK   = cfg.walkK;
    float      stepK   = 0.25f;

    const int   refMic = 0;      // Eichfreiheitsgrad: fix auf Offset 0
    // Startschrittweite/Rundenzahl bewusst so gewaehlt, dass die Summe aller
    // Schritte (geometrische Reihe, Faktor 0.5) den vollen erlaubten Bereich
    // (+-MIC_OFS_MAX_NS=20000, siehe Kommentar dort) tatsaechlich erreichen
    // kann: 10000*(2-2^-10) ~ 19990ns, letzter Schritt ~9.8ns. 2 zusaetzliche
    // Runden (Rev 4.7.2, vorher 9) gegen das Nachbau-Ergebnis von Session
    // 2026-08-25: bei nur 5 Kalibrier-Schuessen kann ein Mic-Offset ueber
    // mehrere Runden hinweg exakt auf einem groben Zwischenschritt "einfrieren"
    // (Kostenfunktion an der Stelle zu flach fuer die dortige Schrittweite),
    // bevor er sich doch noch loest - die 2 zusaetzlichen, noch feineren
    // Runden geben einem solchen Wert eine bessere Chance, sich zu loesen.
    float       stepNs  = 10000.0f;
    for (int pass = 0; pass < 11; pass++) {
        for (int i = 0; i < NUM_AIR; i++) {
            if (i == refMic) continue;
            const float base     = offsets[i];
            const float baseCost = calCost(offsets, walkK);

            offsets[i] = constrain(base + stepNs, (float)-MIC_OFS_MAX_NS, (float)MIC_OFS_MAX_NS);
            const float costPlus = calCost(offsets, walkK);

            offsets[i] = constrain(base - stepNs, (float)-MIC_OFS_MAX_NS, (float)MIC_OFS_MAX_NS);
            const float costMinus = calCost(offsets, walkK);

            if (baseCost <= costPlus && baseCost <= costMinus) {
                offsets[i] = base;
            } else if (costPlus < costMinus) {
                offsets[i] = constrain(base + stepNs, (float)-MIC_OFS_MAX_NS, (float)MIC_OFS_MAX_NS);
            } else {
                offsets[i] = constrain(base - stepNs, (float)-MIC_OFS_MAX_NS, (float)MIC_OFS_MAX_NS);
            }
        }
        if (fitWalk) {
            const float baseCost  = calCost(offsets, walkK);
            const float kPlus     = constrain(walkK + stepK, 0.0f, 1.0f);
            const float kMinus    = constrain(walkK - stepK, 0.0f, 1.0f);
            const float costPlus  = calCost(offsets, kPlus);
            const float costMinus = calCost(offsets, kMinus);
            if (costPlus < baseCost && costPlus <= costMinus)  walkK = kPlus;
            else if (costMinus < baseCost)                     walkK = kMinus;
            stepK *= 0.5f;
        }
        stepNs *= 0.5f;
    }
    if (fitWalk) {
        cfg.walkK = walkK;
        saveVal<float>("walk_k", cfg.walkK);
    }

    char line[300];
    int  n = snprintf(line, sizeof(line),
                      "{\"type\":\"cal\",\"state\":\"done\",\"mac\":\"%s\",\"offsets_ns\":[",
                      deviceMac);
    for (int i = 0; i < NUM_AIR; i++) {
        cfg.micOffsetNs[i] = (int32_t)lroundf(offsets[i]);
        char key[8];
        snprintf(key, sizeof(key), "ofs%d", i);
        saveVal<int32_t>(key, cfg.micOffsetNs[i]);
        n += snprintf(line + n, sizeof(line) - n, "%s%ld",
                      i > 0 ? "," : "", (long)cfg.micOffsetNs[i]);
    }
    // sound_mps ganzzahlig wie bisher (siehe sendShowConfig()), dazu der
    // genaue Ersatzwert und die waehrend der Kalibrierung aktive
    // Schallgeschwindigkeit (bei SET DHT=1 aus der Messung).
    n += snprintf(line + n, sizeof(line) - n, "],\"sound_mps\":%u,\"sound_set_mps\":%.1f,"
                  "\"sound_act_mps\":%.2f,\"walk_k\":%.3f,\"walk_fitted\":%d,\"shots\":%d}\n",
                  (unsigned)lroundf(cfg.soundSpeedMps), cfg.soundSpeedMps, soundMpsActive,
                  cfg.walkK, fitWalk ? 1 : 0, calCollected);
    emitLine(line);
    sendShowConfig();   // komplette Konfiguration direkt im Anschluss zeigen
}

// ---------------------------------------------------------------------------
// Zonen-Kalibrierung (Rev 4.17.0) - identisch zum Server-Simulator
// (server/sim_zones.go), damit Simulation und Geraet dasselbe rechnen.
// ---------------------------------------------------------------------------
static const char ZONE_NAMES[ZONE_COUNT] = { 'A', 'B', 'C' };

static inline bool zonesActive()
{
    return cfg.zonesOn && cfg.zHoleUm > 0 && cfg.algoMode == ALGO_CLASSIC;
}

// 1 fuer v <= edge-w, 0 fuer v >= edge+w, linear dazwischen
static float zoneRamp(float v, float edge, float w)
{
    if (w <= 0.0f) return v < edge ? 1.0f : 0.0f;
    float t = (edge + w - v) / (2.0f * w);
    return t < 0.0f ? 0.0f : (t > 1.0f ? 1.0f : t);
}

// Anteile der Zonen A/B/C an der Lage x/y (Scheibenkoordinaten in mm, inkl.
// Nachkorrektur), Summe 1.
static void zoneWeights(float x, float y, float w[ZONE_COUNT])
{
    const float blend = (cfg.zBlendUm > 0 && cfg.zBlendUm <= 10000) ? cfg.zBlendUm / 1000.0f : 2.25f;
    const float band  = cfg.zBandUm > 0 ? cfg.zBandUm / 1000.0f : 45.0f;
    const float wA = zoneRamp(sqrtf(x * x + y * y), cfg.zHoleUm / 1000.0f, blend);
    const float wB = 1.0f - zoneRamp(fabsf(x), band, blend);
    w[0] = wA;
    w[1] = (1.0f - wA) * wB;
    w[2] = (1.0f - wA) * (1.0f - wB);
}

// Zweite Stufe der Positionsbestimmung mit Zonen-Saetzen. Eingang: rohe
// Erstflanken (airT0NsRaw), Pulsbreiten-Korrektur (walk) und die Lage der
// Basis-Rechnung in Scheibenkoordinaten (*xMm/*yMm inkl. OFFSETX/Y).
// Ausgang: gewichtet gemittelte Lage (inkl. der Nachkorrektur der Zonen) und
// Rest-Fehler/Praezision/cluster_hits der Zone mit dem groessten Anteil.
// Liefert den Index dieser Zone, -1 = keine Zonen-Rechnung moeglich (die
// Basiswerte bleiben dann unveraendert).
static int zoneSolve(const int64_t airT0NsRaw[NUM_AIR], const bool seen[NUM_AIR],
                     const float walk[NUM_AIR], float *xMm, float *yMm,
                     float *resMm, float *precMm, int *clusterN)
{
    const float baseSoundMmPerNs = soundMmPerNs;
    float x = *xMm, y = *yMm;
    float w[ZONE_COUNT], lastW[ZONE_COUNT] = { -1.0f, -1.0f, -1.0f };
    int   dom = -1;
    for (int iter = 0; iter < 3; iter++) {
        zoneWeights(x, y, w);
        if (iter > 0 && w[0] == lastW[0] && w[1] == lastW[1] && w[2] == lastW[2]) break;
        for (int k = 0; k < ZONE_COUNT; k++) lastW[k] = w[k];
        float sx = 0.0f, sy = 0.0f, sw = 0.0f, best = -1.0f;
        int   iterDom = -1;
        float bRes = 0.0f, bPrec = 0.0f;
        int   bClu = 0;
        for (int k = 0; k < ZONE_COUNT; k++) {
            if (w[k] <= 0.0f) continue;
            const ZoneCal &z = cfg.zone[k];
            const int32_t *ofs = z.valid ? z.micOffsetNs : cfg.micOffsetNs;
            int64_t tNs[NUM_AIR];
            for (int i = 0; i < NUM_AIR; i++) {
                tNs[i] = seen[i] ? airT0NsRaw[i] - ofs[i] + (int64_t)lroundf(walk[i]) : 0;
            }
            // Schallgeschwindigkeit als Differenz zur Basis (SET SOUNDSPEED),
            // damit eine DHT-Messung erhalten bleibt
            soundMmPerNs = z.valid
                ? (soundMpsActive + (z.soundMps - cfg.soundSpeedMps)) * 1.0e-6f
                : baseSoundMmPerNs;
            float px, py, pr, pp, pxPre, pyPre, ppPre;
            int   pc, pcPre;
            const bool ok = solveAirPosition(tNs, seen, (float)cfg.clusterRadiusUm / 1000.0f, false,
                                             &px, &py, &pr, &pp, &pc,
                                             &pxPre, &pyPre, &ppPre, &pcPre, nullptr);
            soundMmPerNs = baseSoundMmPerNs;
            if (!ok) continue;
            const int32_t offX = z.valid ? z.offsetXUm : cfg.offsetXUm;
            const int32_t offY = z.valid ? z.offsetYUm : cfg.offsetYUm;
            sx += w[k] * (px + offX / 1000.0f);
            sy += w[k] * (py + offY / 1000.0f);
            sw += w[k];
            if (w[k] > best) {
                best = w[k]; iterDom = k;
                bRes = pr; bPrec = pp; bClu = pc;
            }
        }
        // keine Zone loesbar: Stand des letzten Durchgangs uebernehmen (nicht
        // return - sonst gingen Lage und Rest-Fehler auseinander)
        if (sw <= 0.0f) break;
        x = sx / sw;
        y = sy / sw;
        dom = iterDom;
        *resMm = bRes; *precMm = bPrec; *clusterN = bClu;
    }
    *xMm = x;
    *yMm = y;
    return dom;
}

// SHOWZONES (Rev 4.17.0): alle Zonenwerte in einer eigenen Zeile (haelt die
// ohnehin lange "config"-Zeile kurz).
static void sendShowZones()
{
    static char line[768];   // static: nur aus loop()
    int n = snprintf(line, sizeof(line),
                     "{\"type\":\"zones\",\"mac\":\"%s\",\"zones\":%d,\"active\":%d,"
                     "\"z_hole_um\":%lu,\"z_band_um\":%lu,\"z_blend_um\":%lu",
                     deviceMac, cfg.zonesOn ? 1 : 0, zonesActive() ? 1 : 0,
                     (unsigned long)cfg.zHoleUm, (unsigned long)cfg.zBandUm,
                     (unsigned long)cfg.zBlendUm);
    for (int k = 0; k < ZONE_COUNT && n < (int)sizeof(line) - 200; k++) {
        const ZoneCal &z = cfg.zone[k];
        if (!z.valid) {
            n += snprintf(line + n, sizeof(line) - n, ",\"%c\":null", ZONE_NAMES[k] + 32);
            continue;
        }
        n += snprintf(line + n, sizeof(line) - n,
                      ",\"%c\":{\"sound_mps\":%.1f,\"mic_offset_ns\":[%ld,%ld,%ld,%ld,%ld,%ld],"
                      "\"offset_x_um\":%ld,\"offset_y_um\":%ld}",
                      ZONE_NAMES[k] + 32, z.soundMps,
                      (long)z.micOffsetNs[0], (long)z.micOffsetNs[1], (long)z.micOffsetNs[2],
                      (long)z.micOffsetNs[3], (long)z.micOffsetNs[4], (long)z.micOffsetNs[5],
                      (long)z.offsetXUm, (long)z.offsetYUm);
    }
    if (n < (int)sizeof(line) - 3) snprintf(line + n, sizeof(line) - n, "}\n");
    emitLine(line);
}

static void processShot()
{
    // TESTSHOOT (siehe handleTestShoot()): gilt NUR fuer GENAU diese eine
    // Auswertung, deshalb sofort lesen und zuruecksetzen - ein spaeterer
    // echter Treffer darf nicht faelschlich als "synthetic" markiert werden.
    const bool wasSynthetic = syntheticShotPending;
    syntheticShotPending = false;
    const char *synSuffix = wasSynthetic ? ",\"synthetic\":1" : "";

    uint32_t localAirCC[NUM_AIR][AIR_MAX_EDGES];
    uint8_t  localAirN[NUM_AIR];
    uint32_t localFirstAirCC;
    uint64_t localFirstUs;
    uint32_t localPiezoCC;
    bool     localPiezoSeen;

    noInterrupts();
    localFirstAirCC = firstAirCC;
    for (int i = 0; i < NUM_AIR; i++) {
        localAirN[i] = airCount[i];
        for (int e = 0; e < localAirN[i]; e++) localAirCC[i][e] = airCC[i][e];
    }
    localFirstUs   = firstHitTimeUs;
    localPiezoCC   = piezoCC;
    localPiezoSeen = piezoSeen;
    takeEdgeSnapshot();   // Pulsbreiten (SET WALKK), siehe edgeWidthNs()

    // Sperrzeit richtet sich danach, ob dieser Trigger plausibel zu einem
    // gueltigen Schuss gehoert: bei zu wenigen Mic-Treffern (typisch ein
    // einzelnes, durch den Muendungsknall verfruehtes Mikrofon, siehe
    // Session-Historie AIR1) nur die kurze SET REJECTLOCK-Sperrzeit setzen
    // statt der vollen SET DEBOUNCE-Zeit. Grund: der ECHTE Einschlag trifft
    // je nach Muendungsknall-Vorlauf u.U. erst zig ms spaeter ein (bei
    // 300m Schussdistanz z.B. ~27ms) - mit der vollen Sperrzeit (Default
    // 100ms) waere das Geraet in diesem Moment noch gesperrt und wuerde den
    // echten Einschlag komplett verschlucken, statt ein neues, sauberes
    // Sammelfenster dafuer zu oeffnen. Nach einem plausiblen Schuss bleibt
    // die volle Sperrzeit bestehen (blendet Nachschwinger/Echos desselben
    // Einschlags aus). Nur eine grobe Vorab-Schaetzung (ohne SET MICEN-
    // Maskierung) - die eigentliche Reject-Entscheidung weiter unten bleibt
    // davon unberuehrt.
    int quickAirHits = 0;
    for (int i = 0; i < NUM_AIR; i++) if (localAirN[i] > 0) quickAirHits++;
    // SET PIEZOREQ=1 (bei SET PIEZO=1): genug Mics allein reichen nicht mehr,
    // ohne Piezo wird der Schuss unten ohnehin abgelehnt - dann ebenfalls nur
    // die kurze Sperrzeit, damit ein spaeter folgender echter Einschlag
    // (typisch: Muendungsknall hat das Fenster verfrueht geoeffnet) noch
    // erfasst wird. Mit Piezo-Signal bleibt es bei der vollen Sperrzeit.
    const bool piezoGate = cfg.usePiezo && cfg.piezoRequired;
    bool quickPlausible = (quickAirHits >= (int)cfg.minMics && !piezoGate)
                        || (cfg.usePiezo && localPiezoSeen);
    uint32_t lockMs = quickPlausible ? cfg.debounceMs : cfg.rejectLockoutMs;
    lockoutUntil = (uint64_t)esp_timer_get_time()
                 + (uint64_t)lockMs * 1000ULL;
    interrupts();

    resetShotState();

    // ROHE (unkorrigierte) Erst-Flankenzeiten - werden unabhaengig vom
    // Reject-Filter berechnet, da sie auch fuer die Kalibrierung (unten)
    // gebraucht werden.
    int64_t airT0NsRaw[NUM_AIR];
    bool    airSeen[NUM_AIR];
    for (int i = 0; i < NUM_AIR; i++) {
        // SET MICEN<i>=0: Mikrofon wird behandelt, als haette es nicht
        // ausgeloest - unabhaengig von einer tatsaechlich erfassten Flanke.
        // Wirkt dadurch sowohl auf die Positionsloesung (solveAirPosition()
        // sieht diesen Mic-Index gar nicht erst) als auch auf CAL START
        // (calSeenBuf uebernimmt airSeen 1:1, siehe weiter unten).
        airSeen[i] = cfg.micEnabled[i] && localAirN[i] > 0;
        if (airSeen[i]) {
            uint32_t dCC = localAirCC[i][0] - localFirstAirCC;   // wrap-sicher
            airT0NsRaw[i] = (int64_t)((uint64_t)dCC * 1000ULL / (uint64_t)cpuMHz);
        } else {
            airT0NsRaw[i] = 0;
        }
    }

    // airHits erst NACH der SET MICEN-Maskierung zaehlen - sonst koennten
    // deaktivierte Mikrofone (die gar nicht in die Loesung/Kalibrierung
    // eingehen) trotzdem zum Erreichen von "genug Hits" beitragen und einen
    // eigentlich zu duennen Schuss faelschlich durchwinken.
    int airHits = 0;
    for (int i = 0; i < NUM_AIR; i++) if (airSeen[i]) airHits++;

    // Pulsbreite der ersten Flanke je Mic (Rev 4.14.0, -1 = keine, z.B. im
    // ISR-Betrieb) - fuer SET WALKK, CAL START und das air_w_ns-Feld.
    int32_t airWNs[NUM_AIR];
    for (int i = 0; i < NUM_AIR; i++) {
        airWNs[i] = airSeen[i] ? edgeWidthNs(i, localAirCC[i][0]) : -1;
    }

    // Piezo-Verzoegerung relativ zum ersten Luft-Ereignis (siehe Rev-4.3-
    // Hinweis oben) - unabhaengig von Reject-Filter/Kalibrierung, da rein
    // diagnostisch bzw. fuer die Sauber-Bewertung (isClean) unten gebraucht.
    int64_t piezoT0NsRaw = 0;
    if (localPiezoSeen) {
        uint32_t dCC = localPiezoCC - localFirstAirCC;   // wrap-sicher
        piezoT0NsRaw = (int64_t)((uint64_t)dCC * 1000ULL / (uint64_t)cpuMHz);
    }
    // Geschoss muss die Papier->Stahl-Luecke (8-18cm) erst noch durchqueren,
    // bevor das Piezo ausloest -> planmaessig SPAETER als der erste
    // Luftschall-Treffer (SET PIEZOMIN..PIEZOMAX danach), siehe Rev-4.3-
    // Hinweis oben. [Bis Rev 4.11.0 gab es hier zusaetzlich einen STEEL-
    // Modus, in dem die Platte selbst die Trefferflaeche war und PIEZOMIN
    // deshalb nicht geprueft wurde - seit Rev 4.11.1 entfernt, PIEZOMIN gilt
    // jetzt immer.]
    bool piezoOk = localPiezoSeen
                 && piezoT0NsRaw <= (int64_t)cfg.piezoMaxUs * 1000LL
                 && piezoT0NsRaw >= (int64_t)cfg.piezoMinUs * 1000LL;

    // Kalibrierung (CAL START) laeuft unabhaengig vom normalen Reject-Filter
    // und vom SET DEBUG-Level mit - der Bediener braucht sofort Feedback.
    // Trilateration braucht grundsaetzlich mindestens 3 Mics, unabhaengig
    // von der (ggf. hoeher gesetzten) SET MINMICS-Schwelle.
    if (calActive) {
        if (airHits < 3) {
            emitf("{\"type\":\"cal\",\"state\":\"skipped\",\"reason\":\"only %d mic(s)\","
                  "\"progress\":%d,\"need\":%d}\n",
                  airHits, calCollected, cfg.calShotCount);
        } else if (cfg.usePiezo && !piezoOk) {
            // NEU: ohne Piezo-Bestaetigung (bzw. mit unplausibler Piezo-
            // Verzoegerung) wird der Schuss NICHT fuer die Kalibrierung
            // verwendet - typischerweise ein durch den Muendungsknall
            // verfrueht geoeffnetes Sammelfenster (siehe Rev-4.3-Hinweis
            // oben), dessen Flankenzeiten die Kalibrierung sonst verfaelschen
            // wuerden. Gilt nur bei aktivem Piezo (SET PIEZO=1) - ohne Piezo
            // gibt es keine Bestaetigung, die geprueft werden koennte.
            emitf("{\"type\":\"cal\",\"state\":\"skipped\",\"reason\":\"no piezo confirmation\","
                  "\"progress\":%d,\"need\":%d}\n", calCollected, cfg.calShotCount);
        } else if (calCollected < MAX_CAL_SHOTS) {
            for (int i = 0; i < NUM_AIR; i++) {
                calSeenBuf[calCollected][i] = airSeen[i];
                calRawNs[calCollected][i]   = airT0NsRaw[i];
                calWNs[calCollected][i]     = airWNs[i];
            }
            calCollected++;
            if (calCollected >= cfg.calShotCount) {
                runCalibration();
                calActive    = false;
                calCollected = 0;
            } else {
                emitf("{\"type\":\"cal\",\"state\":\"waiting\",\"progress\":%d,"
                      "\"need\":%d}\n", calCollected, cfg.calShotCount);
            }
        }
    }

    // Automatischer Papiervorschub (SET PAPERAUTO/PAPERTRIGGER, siehe
    // servicePaperStepper() weiter oben): ANY/PIEZO werden hier bewusst VOR
    // dem airHits-Reject-Check ausgeloest, da beide Modi unabhaengig davon
    // gelten sollen, ob genug Mikrofone fuer eine Positionsloesung reichten
    // (das Piezo erkennt den Einschlag unabhaengig von den Luft-Mics). Der
    // CLEAN-Modus wird erst ganz unten nach der isClean-Berechnung ausgeloest.
    if (cfg.paperAuto
        && (cfg.paperTrigger == PAPER_TRIG_ANY
            || (cfg.paperTrigger == PAPER_TRIG_PIEZO && localPiezoSeen))) {
        startPaperFeed();
    }

    if (airHits < cfg.minMics) {
        // Rev 4.6.1: nicht mehr an SET DEBUG gebunden. Rev 4.11.5: per TCP
        // (mit seq) nur noch bei SET SENDREJECT=1, sonst nur Serial ohne
        // seq - siehe emitReject(). SET DEBUG steuert nur die zusaetzlichen
        // "cand"/"_pre"-Diagnosefelder (SET DEBUG=3).
        if (cfg.usePiezo && localPiezoSeen) {
            emitReject("\"algo\":\"classic\",\"reason\":\"only %d mic(s)\","
                       "\"hits\":%d,\"piezo_ns\":%lld%s}\n",
                       airHits, airHits, (long long)piezoT0NsRaw, synSuffix);
        } else if (cfg.usePiezo) {
            emitReject("\"algo\":\"classic\",\"reason\":\"only %d mic(s)\","
                       "\"hits\":%d,\"piezo_ns\":null%s}\n", airHits, airHits, synSuffix);
        } else {
            emitReject("\"algo\":\"classic\",\"reason\":\"only %d mic(s)\","
                       "\"hits\":%d%s}\n", airHits, airHits, synSuffix);
        }
        return;
    }

    // SET PIEZOREQ=1 (Default, nur wirksam bei SET PIEZO=1): ohne gueltige
    // Piezo-Bestaetigung ist der Schuss ungueltig -> "reject" statt "shot"
    // mit clean=0 (Ausgabe/seq-Verhalten siehe emitReject()/SET SENDREJECT).
    if (piezoGate && !piezoOk) {
        if (localPiezoSeen) {
            emitReject("\"algo\":\"classic\",\"reason\":\"piezo out of window\","
                       "\"hits\":%d,\"piezo_ns\":%lld%s}\n",
                       airHits, (long long)piezoT0NsRaw, synSuffix);
        } else {
            emitReject("\"algo\":\"classic\",\"reason\":\"no piezo\","
                       "\"hits\":%d,\"piezo_ns\":null%s}\n", airHits, synSuffix);
        }
        return;
    }

    // Gueltiger Schuss: seq/shots erst hier erhoehen (Rejects zaehlen nur bei
    // SET SENDREJECT=1 mit, siehe emitReject()). Muss vor solveAirPosition()
    // stehen, da die "cand"-Diagnosezeilen (SET DEBUG=3) sequenceNo tragen.
    shotCounter++;
    sequenceNo++;

    // Kalibrierten Timing-Offset je Mikrofon abziehen (SET OFS<i> bzw.
    // CAL START, Default 0) - siehe runCalibration() - und die Pulsbreiten-
    // Korrektur (SET WALKK, Rev 4.14.0, bei 0 bzw. ohne Breiten = 0) addieren.
    float walk[NUM_AIR];
    walkDeltas(airWNs, airSeen, cfg.walkK, walk);
    int64_t airT0Ns[NUM_AIR];
    for (int i = 0; i < NUM_AIR; i++) {
        airT0Ns[i] = airSeen[i]
                   ? airT0NsRaw[i] - cfg.micOffsetNs[i] + (int64_t)lroundf(walk[i])
                   : 0;
    }
    float posX = 0.0f, posY = 0.0f, posRes = 0.0f, posPrecision = 0.0f;
    int   clusterHits = 0;
    float posXPre = 0.0f, posYPre = 0.0f, posPrecisionPre = 0.0f;
    int   clusterHitsPre = 0;
    bool  posOk = solveAirPosition(airT0Ns, airSeen,
                                    (float)cfg.clusterRadiusUm / 1000.0f,
                                    cfg.debug >= 3,
                                    &posX, &posY, &posRes,
                                    &posPrecision, &clusterHits,
                                    &posXPre, &posYPre, &posPrecisionPre, &clusterHitsPre,
                                    nullptr); // Korrigierter Rest-Fehler wird nur fuer die Kalibrierung gebraucht (calCostClassic())
    // Zonen-Kalibrierung (Rev 4.17.0, SET ZONES=1): zweite Stufe mit den
    // Saetzen der Zone(n), in der die Basis-Lage liegt - siehe zoneSolve().
    // Die Lage kommt dort inkl. Nachkorrektur der Zonen zurueck; die
    // Basis-Nachkorrektur unten wird deshalb vorher wieder abgezogen.
    int zoneIdx = -1;
    if (posOk && zonesActive()) {
        float zx = posX + cfg.offsetXUm / 1000.0f, zy = posY + cfg.offsetYUm / 1000.0f;
        zoneIdx = zoneSolve(airT0NsRaw, airSeen, walk, &zx, &zy, &posRes, &posPrecision, &clusterHits);
        if (zoneIdx >= 0) {
            posX = zx - cfg.offsetXUm / 1000.0f;
            posY = zy - cfg.offsetYUm / 1000.0f;
        }
    }
    long  xUm    = posOk ? lroundf(posX         * 1000.0f) : 0;
    long  yUm    = posOk ? lroundf(posY         * 1000.0f) : 0;
    long  resUm  = posOk ? lroundf(posRes       * 1000.0f) : 0;
    long  precUm = posOk ? lroundf(posPrecision * 1000.0f) : 0;
    int   clusterN = posOk ? clusterHits : 0;
    // Stufe-1-Werte (vor dem Verifizierungsschritt) fuer SET DEBUG=3 - siehe
    // Kommentar bei solveAirPosition(). xUmPre/yUmPre bewusst OHNE
    // OFFSETX/OFFSETY-Nachkorrektur (die gilt nur fuer das finale Ergebnis).
    long  xUmPre    = posOk ? lroundf(posXPre         * 1000.0f) : 0;
    long  yUmPre    = posOk ? lroundf(posYPre         * 1000.0f) : 0;
    long  precUmPre = posOk ? lroundf(posPrecisionPre * 1000.0f) : 0;
    int   clusterNPre = posOk ? clusterHitsPre : 0;
    // Konstante Nachkorrektur (SET OFFSETX/OFFSETY, Default 0) - erst NACH
    // der Trilateration angewandt, z.B. zum Ausgleich einer Messgitter-
    // Verschiebung. Wirkt sich NICHT auf pos_res_um/precision_um aus, da
    // die reine Geometrieguete der Loesung unveraendert bleibt.
    if (posOk) {
        xUm += cfg.offsetXUm;
        yUm += cfg.offsetYUm;
    }

    // 6 Mics x 6 Flanken je bis zu ~8-stellig -> Puffer grosszuegig bemessen
    // (Rev 4.16.1: 1024 statt 900 Byte, static statt auf dem Loop-Stack -
    // processShot() laeuft nur aus loop(), ist also nicht reentrant)
    static char line[1024];
    int  n = snprintf(line, sizeof(line),
                      "{\"type\":\"shot\",\"seq\":%u,\"algo\":\"classic\",\"air_ns\":[", sequenceNo);
    for (int i = 0; i < NUM_AIR && n < (int)sizeof(line) - 32; i++) {
        if (i > 0) line[n++] = ',';
        line[n++] = '[';
        for (int e = 0; e < localAirN[i]
                     && n < (int)sizeof(line) - 24; e++) {
            if (e > 0) line[n++] = ',';
            // Luftkanaele referenzieren den ersten erfassten Mikrofon-Hit
            uint32_t dCC = localAirCC[i][e] - localFirstAirCC;   // wrap-sicher
            int64_t ns = (int64_t)((uint64_t)dCC * 1000ULL
                                   / (uint64_t)cpuMHz);
            n += snprintf(line + n, sizeof(line) - n, "%lld",
                          (long long)ns);
        }
        line[n++] = ']';
    }
    n += snprintf(line + n, sizeof(line) - n, "]");
    n += appendAirWidths(line + n, sizeof(line) - n, airWNs);
    // SET DEBUG=3: zusaetzlich die Stufe-1-Werte (vor dem Verifizierungs-
    // schritt, siehe solveAirPosition()) ausgeben, damit sich beide Stufen
    // vergleichen lassen - sonst nur das Endergebnis nach der Verifizierung.
    if (cfg.debug >= 3) {
        n += snprintf(line + n, sizeof(line) - n,
                      ",\"x_um_pre\":%ld,\"y_um_pre\":%ld,"
                      "\"precision_um_pre\":%ld,\"cluster_hits_pre\":%d",
                      xUmPre, yUmPre, precUmPre, clusterNPre);
    }
    n += snprintf(line + n, sizeof(line) - n,
                  ",\"x_um\":%ld,\"y_um\":%ld,\"pos_res_um\":%ld,"
                  "\"precision_um\":%ld,\"cluster_hits\":%d,\"pos_valid\":%d,"
                  "\"sound_act_mps\":%.2f,\"raw_follows\":%d",
                  xUm, yUm, resUm, precUm, clusterN, posOk ? 1 : 0, soundMpsActive,
                  rawWillFollow() ? 1 : 0);
    // Rev 4.17.0: Zone mit dem groessten Anteil (nur bei aktiven Zonen)
    if (zoneIdx >= 0) {
        n += snprintf(line + n, sizeof(line) - n, ",\"zone\":\"%c\"", ZONE_NAMES[zoneIdx]);
    }
    if (cfg.usePiezo) {
        if (localPiezoSeen) {
            n += snprintf(line + n, sizeof(line) - n,
                          ",\"piezo_ns\":%lld,\"piezo_ok\":%d",
                          (long long)piezoT0NsRaw, piezoOk ? 1 : 0);
        } else {
            n += snprintf(line + n, sizeof(line) - n,
                          ",\"piezo_ns\":null,\"piezo_ok\":0");
        }
    }
    // "clean" fasst zusammen, ob der Schuss allen Qualitaetsschwellen
    // genuegt (kein Ausreisser, Position bestimmbar, genug uebereinstimmende
    // Mic-Kombinationen, precision_um innerhalb der Schwelle, und - falls
    // SET PIEZO=1 - vom Piezo im erwarteten Zeitfenster bestaetigt) - damit
    // muss die Anzeige diese Schwellenlogik nicht selbst nachbilden, um
    // "gute" von "fragwuerdigen" Schuessen zu unterscheiden.
    bool isClean = posOk
                 && (unsigned long)resUm < cfg.airOutlierUm
                 && clusterN >= (int)cfg.minClusterHits
                 && (unsigned long)precUm <= cfg.maxPrecisionUm
                 && (!cfg.usePiezo || piezoOk);
    // SET PAPERTRIGGER=CLEAN: erst jetzt ausloesen, da isClean erst hier
    // feststeht (ANY/PIEZO werden weiter oben vor dem Reject-Check ausgeloest)
    if (cfg.paperAuto && cfg.paperTrigger == PAPER_TRIG_CLEAN && isClean) {
        startPaperFeed();
    }
    n += snprintf(line + n, sizeof(line) - n, ",\"clean\":%d", isClean ? 1 : 0);
    snprintf(line + n, sizeof(line) - n, ",\"hits\":%d,\"ts\":%llu%s}\n",
             airHits, (unsigned long long)(localFirstUs / 1000ULL), synSuffix);

    // Rev 4.6.1: nicht mehr an isClean/SET DEBUG gebunden - geht jetzt IMMER
    // raus (auch unsaubere/Ausreisser-Schuesse), damit bei der Anzeige zu
    // jeder Sequenz-Nummer etwas ankommt. Die Anzeige kann "clean" (siehe
    // oben) nutzen, um trotzdem nur saubere Treffer hervorzuheben/zu werten.
    emitLine(line);

    // SET RAWLOG=1 (Rev 4.16.0): Rohdaten fuer den Server-Simulator. Fenster
    // wie in loop() (Sammelfenster, bei Piezo bis PIEZOMAX+200us) plus ein
    // Vorlauf wie bei ALGO=RIM, damit auch Flanken VOR dem Fenster-Start
    // (Muendungsknall, Sperrzeit) sichtbar sind.
    if (cfg.rawLog) {
        float postNs = (float)cfg.windowMs * 1.0e6f;
        if (cfg.usePiezo) postNs = fmaxf(postNs, ((float)cfg.piezoMaxUs + 200.0f) * 1000.0f);
        emitRawTelegram("classic", localFirstAirCC,
                        (float)cfg.piezoMaxUs * 1000.0f + 50000.0f, postNs,
                        localPiezoSeen, piezoT0NsRaw, localFirstUs, synSuffix);
    }
}

// ---------------------------------------------------------------------------
// SET ALGO=RIM - Gegenstueck zu processShot() (siehe Rev-4.11.0-Hinweis im
// Header-Kommentar ganz oben sowie schusserkennung-rev5.md): wird aus loop()
// gerufen, sobald nach einem Piezo-Anker (piezoISR(), NUR bei ALGO=RIM
// gesetzt) postWaitUs verstrichen ist. Liest die Ringpuffer RUECKWIRKEND aus,
// baut daraus je Mikrofon die zum Gate passenden Kandidatenflanken (siehe
// shot_locator.h) und ruft shotloc::locate() auf - ersetzt dadurch sowohl die
// klassische Fenster-/Sperrzeit-Logik als auch solveAirPosition() fuer diesen
// Pfad. air_ns im Telegramm ist hier relativ zum PIEZO (nicht wie bei
// ALGO=CLASSIC relativ zur ersten Flanke) - siehe docs/protokoll-referenz.md.
// ---------------------------------------------------------------------------
static void processShotAnchored()
{
    // TESTSHOOT (siehe handleTestShoot()): gilt NUR fuer GENAU diese eine
    // Auswertung - analog zu processShot().
    const bool wasSynthetic = syntheticShotPending;
    syntheticShotPending = false;
    const char *synSuffix = wasSynthetic ? ",\"synthetic\":1" : "";

    // Ringpuffer-Kopie liegt seit Rev 4.14.0 in edgeSnap (static, samt den
    // fallenden Flanken fuer die Pulsbreiten) statt auf dem Loop-Stack.
    const uint32_t (&localRingCC)[NUM_AIR][EDGE_RING] = edgeSnap.riseCC;
    const uint32_t (&localRingUs)[NUM_AIR][EDGE_RING] = edgeSnap.riseUs;
    uint32_t localPiezoCC;
    uint64_t localPiezoUs;

    noInterrupts();
    takeEdgeSnapshot();
    localPiezoCC = piezoAnchorCC;
    localPiezoUs = piezoAnchorUs;
    piezoPending = false;
    freezeActive = false;
    // Konservative Vorab-Sperrzeit (volle SET DEBOUNCE) - wird unten auf die
    // kurze SET REJECTLOCK-Zeit verkuerzt, falls dieser Schuss abgelehnt wird
    // (analog zur quickPlausible-Vorab-Schaetzung in processShot()). Muss
    // HIER (noch unter noInterrupts) gesetzt werden, da piezoPending bereits
    // false ist und piezoISR() ab sofort wieder auf ein neues Ereignis
    // reagieren wuerde.
    lockoutUntil = (uint64_t)esp_timer_get_time() + (uint64_t)cfg.debounceMs * 1000ULL;
    interrupts();

    // Kandidatenflanken je Mic: alles innerhalb [-preNs, +postNs] relativ
    // zum Piezo, offset-korrigiert, aufsteigend sortiert (siehe
    // schusserkennung-rev5.md Abschnitt 3.3).
    const float preNs  = (float)cfg.piezoMaxUs * 1000.0f + 50000.0f;
    const float postNs = (float)postWaitUs * 1000.0f;

    // Rohe Kandidaten (ns relativ zum Piezo, OHNE Offset) samt Pulsbreite
    // (Rev 4.14.0) sammeln und roh sortieren - so gehen sie in CAL START.
    // static: Loop-Stack schonen, nicht reentrant.
    static float   rawT[NUM_AIR][EDGE_RING];
    static int32_t rawW[NUM_AIR][EDGE_RING];
    static float   workT[NUM_AIR][EDGE_RING];
    static int32_t workW[NUM_AIR][EDGE_RING];
    int  rawN[NUM_AIR];
    bool micOn[NUM_AIR];
    float ofsNs[NUM_AIR];
    for (int i = 0; i < NUM_AIR; i++) {
        micOn[i] = cfg.micEnabled[i];
        ofsNs[i] = (float)cfg.micOffsetNs[i];
        rawN[i]  = 0;
        if (!micOn[i]) continue;
        for (int k = 0; k < EDGE_RING; k++) {
            if (localRingUs[i][k] == 0) continue;   // nie beschriebener Slot
            const float tNs = (float)(int32_t)(localRingCC[i][k] - localPiezoCC)
                             * 1000.0f / (float)cpuMHz;
            // Fenster wie bisher auf die offset-korrigierte Zeit bezogen
            if (tNs - ofsNs[i] < -preNs || tNs - ofsNs[i] > postNs) continue;
            rawT[i][rawN[i]] = tNs;
            rawW[i][rawN[i]] = edgeWidthNs(i, localRingCC[i][k]);
            rawN[i]++;
        }
        sortEdgesWithWidth(rawT[i], rawW[i], rawN[i]);
        if (rawN[i] > shotloc::MAX_EDGES) rawN[i] = shotloc::MAX_EDGES;
        for (int k = 0; k < rawN[i]; k++) { workT[i][k] = rawT[i][k]; workW[i][k] = rawW[i][k]; }
    }
    // Offset abziehen, Pulsbreiten-Korrektur (SET WALKK), sortieren - danach
    // gehoert workW[i][k] zu ed.t[i][k].
    shotloc::Edges ed{};
    rimBuildEdges(workT, workW, rawN, micOn, ofsNs, cfg.walkK, &ed);
    int32_t firstWNs[NUM_AIR];
    for (int i = 0; i < NUM_AIR; i++) firstWNs[i] = ed.n[i] > 0 ? workW[i][0] : -1;

    int nAvail = 0;
    for (int i = 0; i < NUM_AIR; i++) if (ed.n[i] > 0) nAvail++;

    // Kalibrierung (CAL START) laeuft unabhaengig vom normalen Reject-Filter,
    // analog zu processShot(). Gespeichert werden die ROHEN (weder offset-
    // noch pulsbreiten-korrigierten) Kandidatenflanken samt Breite - calCostRim()
    // wendet beides beim Suchen ueber rimBuildEdges() selbst an.
    if (calActive) {
        if (nAvail < 3) {
            emitf("{\"type\":\"cal\",\"state\":\"skipped\",\"reason\":\"only %d mic(s)\","
                  "\"progress\":%d,\"need\":%d}\n", nAvail, calCollected, cfg.calShotCount);
        } else if (calCollected < MAX_CAL_SHOTS) {
            for (int i = 0; i < NUM_AIR; i++) {
                calEnabledRim[calCollected][i]   = micOn[i];
                calEdgeCountRim[calCollected][i] = (uint8_t)rawN[i];
                for (int k = 0; k < rawN[i]; k++) {
                    calEdgeNsRim[calCollected][i][k] = rawT[i][k];
                    calEdgeWRim[calCollected][i][k]  = rawW[i][k];
                }
            }
            calCollected++;
            if (calCollected >= cfg.calShotCount) {
                runCalibration();
                calActive    = false;
                calCollected = 0;
            } else {
                emitf("{\"type\":\"cal\",\"state\":\"waiting\",\"progress\":%d,"
                      "\"need\":%d}\n", calCollected, cfg.calShotCount);
            }
        }
    }

    // Automatischer Papiervorschub: bei ALGO=RIM ist das Piezo immer der
    // (alleinige) Ausloeser - ANY/PIEZO verhalten sich deshalb hier gleich.
    // CLEAN wird erst unten nach der isClean-Berechnung ausgeloest.
    if (cfg.paperAuto
        && (cfg.paperTrigger == PAPER_TRIG_ANY || cfg.paperTrigger == PAPER_TRIG_PIEZO)) {
        startPaperFeed();
    }

    bool           ok = false;
    shotloc::Result r{};
    if (nAvail >= cfg.minMics) {
        shotloc::Geometry g;
        for (int i = 0; i < NUM_AIR; i++) { g.micX[i] = MIC_X[i]; g.micY[i] = MIC_Y[i]; }
        g.standoffMm     = micStandoffMm;
        g.soundMmPerNs   = soundMmPerNs;
        g.pelletRadiusMm = cfg.pelletRadiusMm;
        g.maxDistMm      = rimMaxDistMm;

        shotloc::Gate gate;
        gate.active  = true;
        gate.t0MinNs = -(float)cfg.piezoMaxUs * 1000.0f;
        gate.t0MaxNs = -(float)cfg.piezoMinUs * 1000.0f;

        shotloc::Params params;
        params.minMics = cfg.minMics;

        ok = shotloc::locate(g, ed, gate, params, &r) && r.valid;
    }

    // Erfolgreicher Schuss: volle Sperrzeit (oben bereits vorsorglich
    // gesetzt) bleibt bestehen. Reject: auf die kurze SET REJECTLOCK-Zeit
    // verkuerzen, damit ein rasch folgender ECHTER Schuss nicht unnoetig
    // lange blockiert wird (analog zu processShot()).
    if (!ok) {
        lockoutUntil = (uint64_t)esp_timer_get_time() + (uint64_t)cfg.rejectLockoutMs * 1000ULL;
        if (nAvail < cfg.minMics) {
            emitReject("\"algo\":\"rim\",\"reason\":\"only %d mic(s)\","
                       "\"hits\":%d%s}\n", nAvail, nAvail, synSuffix);
        } else {
            emitReject("\"algo\":\"rim\",\"reason\":\"fit failed\","
                       "\"hits\":%d%s}\n", nAvail, synSuffix);
        }
        return;
    }

    // Gueltiger Schuss: seq/shots erst hier erhoehen (siehe processShot()).
    shotCounter++;
    sequenceNo++;

    long xUm    = lroundf(r.xMm * 1000.0f) + cfg.offsetXUm;
    long yUm    = lroundf(r.yMm * 1000.0f) + cfg.offsetYUm;
    long sigXUm = lroundf(r.sigmaXMm * 1000.0f);
    long sigYUm = lroundf(r.sigmaYMm * 1000.0f);

    // 6 Mics x bis zu shotloc::MAX_EDGES Flanken -> Puffer grosszuegig bemessen
    // (Rev 4.16.1: 1024 statt 900 Byte, static wie bei ALGO=CLASSIC)
    static char line[1024];
    int  n = snprintf(line, sizeof(line),
                      "{\"type\":\"shot\",\"seq\":%u,\"algo\":\"rim\",\"air_ns\":[", sequenceNo);
    for (int i = 0; i < NUM_AIR && n < (int)sizeof(line) - 32; i++) {
        if (i > 0) line[n++] = ',';
        line[n++] = '[';
        for (int k = 0; k < ed.n[i] && n < (int)sizeof(line) - 24; k++) {
            if (k > 0) line[n++] = ',';
            n += snprintf(line + n, sizeof(line) - n, "%ld", lroundf(ed.t[i][k]));
        }
        line[n++] = ']';
    }
    n += snprintf(line + n, sizeof(line) - n, "]");
    n += appendAirWidths(line + n, sizeof(line) - n, firstWNs);
    n += snprintf(line + n, sizeof(line) - n,
                  ",\"x_um\":%ld,\"y_um\":%ld,\"sigma_x_um\":%ld,\"sigma_y_um\":%ld,"
                  "\"rms_ns\":%.0f,\"t0_ns\":%.0f,\"used_mask\":%u,\"pos_valid\":1,"
                  "\"sound_act_mps\":%.2f,\"raw_follows\":%d",
                  xUm, yUm, sigXUm, sigYUm, r.rmsNs, r.t0Ns, r.usedMask, soundMpsActive,
                  rawWillFollow() ? 1 : 0);
    // "clean" (SET MAXSIGMA): Gegenstueck zu isClean bei ALGO=CLASSIC -
    // dort precision_um/cluster_hits/airOutlierUm, hier die statistische
    // 1-Sigma-Unsicherheit aus der Kovarianzmatrix.
    const bool isClean = fmaxf(r.sigmaXMm, r.sigmaYMm) <= cfg.maxSigmaMm;
    if (cfg.paperAuto && cfg.paperTrigger == PAPER_TRIG_CLEAN && isClean) {
        startPaperFeed();
    }
    n += snprintf(line + n, sizeof(line) - n, ",\"clean\":%d", isClean ? 1 : 0);
    snprintf(line + n, sizeof(line) - n, ",\"hits\":%d,\"ts\":%llu%s}\n",
             (int)r.nUsed, (unsigned long long)(localPiezoUs / 1000ULL), synSuffix);
    emitLine(line);

    // SET RAWLOG=1 (Rev 4.16.0): Rohdaten fuer den Server-Simulator, Fenster
    // = Kandidatenfenster oben, Nullpunkt = Piezo.
    if (cfg.rawLog) {
        emitRawTelegram("rim", localPiezoCC, preNs, postNs, true, 0, localPiezoUs, synSuffix);
    }
}

// ---------------------------------------------------------------------------
// Befehle – inkl. SET-Kommandos mit NVS-Persistenz
// ---------------------------------------------------------------------------

// Reiner Mensch-lesbarer Hilfetext (HELP oder ?), nur ueber Serial - nicht
// per emitLine/TCP, damit der JSON-Zeilenstrom fuer den Stand-PC nicht mit
// Kommentarzeilen vermischt wird (analog zu den "# ..."-Logzeilen in setup()).
static void sendHelp()
{
    static const char *lines[] = {
        "# Verfuegbare SET-Parameter:",
        "#   SET SSID=<text>          WLAN-Name (leer = WLAN aus, Reboot noetig)",
        "#   SET PASS=<text>          WLAN-Passwort (Reboot noetig)",
        "#   SET HOST=<ip/host>       Ziel-Host des Stand-PC (Reboot noetig)",
        "#   SET PORT=<1-65535>       TCP-Port des Stand-PC (Reboot noetig)",
        "#   SET LANE=<1-999>         Bahnnummer",
        "#   SET DEBOUNCE=<10-5000>   Sperrzeit NACH einem gueltigen Schuss in ms",
        "#   SET REJECTLOCK=<0-2000> Sperrzeit NACH einem Reject (zu wenige Mics,",
        "#                            z.B. Muendungsknall-Fehlausloeser) in ms,",
        "#                            Default 5 - bewusst kurz, damit ein spaeter",
        "#                            eintreffender ECHTER Einschlag nicht durch die",
        "#                            (viel laengere) SET DEBOUNCE-Zeit verschluckt wird",
        "#   SET SENDREJECT=<0|1>     0 (Default): reject-Telegramme nur ueber Serial, ohne",
        "#                            seq (Stand-PC sieht keine seq-Luecken); 1: auch per",
        "#                            TCP, mit fortlaufender seq wie shot",
        "#   SET WINDOW=<1-50>        Sammelfenster in ms",
        "#   SET DEBUG=<0-3>          shot/reject-Telegramme gehen seit Rev 4.6.1 IMMER",
        "#                            raus (jede Sequenz-Nummer kommt bei der Anzeige an,",
        "#                            das Feld \"clean\" markiert saubere Treffer). SET DEBUG",
        "#                            steuert nur noch zusaetzliche Diagnosefelder:",
        "#                            3=+Kandidaten-Zeilen je Mic-Kombination (type=cand)",
        "#                            +Stufe-1-Werte vor dem Verifizierungsschritt",
        "#                            (x_um_pre/y_um_pre/precision_um_pre/cluster_hits_pre)",
        "#   SET OUTLIER=<0-500000>   Ausreisser-Schwelle in 0.001mm (Default 5000)",
        "#   SET RADIUS=<0-500000>    Umkreis fuer cluster_hits UND fuer den Verifizierungs-",
        "#                            Mittelpunkt (Stufe 2, siehe solveAirPosition()) in",
        "#                            0.001mm (Default 4000) - sollte den Kugeldurchmesser",
        "#                            (ca. 4500) mit abdecken, nicht nur reines Messrauschen",
        "#   SET MINCLUSTER=<0-20>    Mindest-cluster_hits fuer sauberen Schuss (Default 2)",
        "#   SET MAXPRECISION=<0-500000>   Max. precision_um fuer sauberen Schuss",
        "#                            in 0.001mm (Default 2000)",
        "#   SET MINMICS=<3-6>        Mindestzahl Mics fuer gueltigen Schuss (Default 5)",
        "#   SET TDOA=<100-5000>      Geometrie-Plausibilitaetsfenster in us (Default 750)",
        "#   SET STANDOFFPAPER=<5.0-100.0>  Mic-Standoff (rechtwinklig zur Scheibe)",
        "#                            in mm (Default 28.0)",
        "#   SET MICHALFX=<5.0-300.0> Horizontaler Mic-Abstand zur Mittellinie in mm",
        "#                            (Default 115.0)",
        "#   SET BSHIFTPCT=<0-100>    Kugeldurchmesser-Korrektur (siehe solveAirPosition()):",
        "#                            Gewichtung in % des Rest-Fehlers der Stufe-1-Loesung",
        "#                            gegen die daran unbeteiligten Mikrofone, als Ver-",
        "#                            schiebung Richtung/weg vom jeweiligen Mikrofon,",
        "#                            fliesst auch in CAL START mit ein (Default 30,",
        "#                            0=Korrektur aus)",
        "#   SET BSHIFTCAP=<0.0-20.0> Kappung der Kugeldurchmesser-Korrektur je Mikrofon",
        "#                            in mm (Default 3.0). Gilt NUR fuer ALGO=CLASSIC",
        "#   SET CLUSTERCORR=<0|1>    Endposition aus ALLEN kugeldurchmesser-korrigierten",
        "#                            Schnittpunkten mitteln (Default 1; 0 = nur die",
        "#                            Stufe-1-Loesung korrigiert, wie bis Rev 4.11.5)",
        "#   SET CLUSTERITER=<0-10>   Nachzentrier-Durchgaenge des Clusters (Default 5,",
        "#                            0 = einmalig um die Stufe-1-Loesung)",
        "#   SET LSQ=<0|1>            Endposition per Ausgleichsrechnung ueber alle Mics",
        "#                            verfeinern (Default 0, braucht >= 4 Mics)",
        "#   SET LSQPELLETR=<0.0-5.0> Lochrand-Radius in mm fuer SET LSQ (Default 2.25)",
        "#   SET ALGO=<CLASSIC|RIM>   Auswertepfad (Default CLASSIC = bisheriges",
        "#                            Verhalten unveraendert). RIM: Ringpuffer-Erfassung",
        "#                            (keine Flanke geht mehr durch Muendungsknall/Sperr-",
        "#                            zeit verloren) + Piezo-Anker-Trigger (Piezo ist der",
        "#                            EINZIGE Trigger, SET PIEZO=0 wird ignoriert) +",
        "#                            robuste Ausgleichsrechnung mit Lochrand-Modell statt",
        "#                            solveAirPosition() - siehe schusserkennung-rev5.md.",
        "#                            \"shot\"/\"reject\" tragen ein \"algo\"-Feld, air_ns ist",
        "#                            bei RIM relativ zum Piezo (nicht zur ersten Flanke)",
        "#   SET PELLETR=<0.0-10.0>   Wirksamer akustischer Lochrand-Radius in mm fuer",
        "#                            ALGO=RIM (Default 2.25 = 4,5mm-Diabolo, 0=Punktquelle)",
        "#   SET MAXSIGMA=<0.0-50.0>  \"clean\"-Schwelle fuer ALGO=RIM in mm: max(sigma_x,",
        "#                            sigma_y) aus der Kovarianzmatrix (Default 2.0).",
        "#                            Gegenstueck zu MAXPRECISION/RADIUS/MINCLUSTER (CLASSIC)",
        "#   SET CAPTURE=<ISR|MCPWM>  Zeitstempel der Luft-Mics: ISR (Default, bisher) oder",
        "#                            MCPWM-Hardware-Capture (12,5ns, ohne Interrupt-",
        "#                            Jitter, GPIO23 muss frei bleiben). Reboot noetig,",
        "#                            danach CAL RESET + CAL START. STATUS \"capture\"",
        "#                            zeigt den aktiven Modus (Fallback auf isr)",
        "#   SET WALKK=<0.0-1.0>      Amplituden-Korrektur ueber die Pulsbreite (nur",
        "#                            MCPWM): t += K*(Breite - Mittel). 0 = aus (Default),",
        "#                            0.5 = Pulsmitte (symmetrischer Puls)",
        "#   SET WALKCAL=<0|1>        CAL START bestimmt WALKK mit (Default 0)",
        "#   SET WALKMAX=<5-500>      Groesste plausible Pulsbreite in us (Default 150)",
        "#   SET PIEZO=<0|1>          Piezo (Stahlplatte, GPIO34) als Trigger-",
        "#                            Bestaetigung nutzen (Default 1/an)",
        "#   SET PIEZOREQ=<0|1>       1 (Default): bei SET PIEZO=1 wird ein Schuss",
        "#                            ohne Piezo bzw. mit Piezo ausserhalb PIEZOMIN..",
        "#                            PIEZOMAX als reject behandelt (siehe SENDREJECT);",
        "#                            0: shot mit piezo_ok=0/clean=0 (nur ALGO=CLASSIC)",
        "#   SET PIEZOMIN=<0-5000>    Min. erwartete Piezo-Verzoegerung in us",
        "#                            nach erstem Luft-Ereignis (Default 100)",
        "#   SET PIEZOMAX=<0-5000>    Max. erwartete Piezo-Verzoegerung in us",
        "#                            nach erstem Luft-Ereignis (Default 1400) -",
        "#                            Ausreisser-Obergrenze",
        "#   SET TESTMODE=<0|1>       Reiner Sensor-Testmodus (Klartext-Zeile je",
        "#                            Sensor-Ausloesung mit +ms seit dem 1. Sensor",
        "#                            der Serie, 10 '-' nach 5s Stille) - NICHT",
        "#                            persistent, nach Reboot immer aus (Default 0)",
        "#   SET TESTCOOLDOWN=<0-10000>    Min. Abstand zw. 2 Meldungen desselben",
        "#                            Sensors im Testmodus in ms (Default 3000).",
        "#                            Fuer die Analyse echter Schuesse (mehrere",
        "#                            echte Ereignisse je Sensor moeglich: Knall/",
        "#                            Papier/Stahl) deutlich kleiner stellen (z.B. 50)",
        "#   SET OFFSETX=<-50000..50000>   Konstanter Korrektur-Offset auf x_um",
        "#                            in 0.001mm, NACH der Trilateration addiert",
        "#                            (Default 0, z.B. gg. Messgitter-Versatz)",
        "#   SET OFFSETY=<-50000..50000>   Wie OFFSETX, fuer y_um (Default 0)",
        "#   SET SOUNDSPEED=<300-400> Schallgeschwindigkeit in m/s bei SET DHT=0 bzw.",
        "#                            ohne gueltige Messung (Default 342.5, Nach-",
        "#                            kommastelle erlaubt). Radial mit dem Abstand",
        "#                            wachsender Fehler (Rand zu nah am Zentrum)",
        "#                            -> Wert erhoehen. Rein manueller Wert, wird",
        "#                            NICHT von CAL START mitkalibriert.",
        "#   SET DHT=<0|1>            Schallgeschwindigkeit aus Temperatur/Luftfeuchte",
        "#                            des DHT22 an GPIO15 (Default 0). STATUS zeigt",
        "#                            sound_act_mps/sound_src/temp_c/rh_pct",
        "#   SET ZONES=<0|1>          Zonen-Kalibrierung (nur ALGO=CLASSIC, Default 0):",
        "#                            je Bereich eigener Satz, siehe SHOWZONES",
        "#   SET ZHOLE=<0-60000>      Radius Loch der Vorsatzscheibe in 0.001mm",
        "#                            (LG 15250, LP 29750; 0 = Zonen wirkungslos)",
        "#   SET ZBAND=<0-100000>     halbe Breite des Schussbands (Default 45000)",
        "#   SET ZBLEND=<0-10000>     Ueberblendung an den Grenzen (Default 2250)",
        "#   SET ZCAL=<A|B|C>,<mps>,<ofs0>,..,<ofs5>,<offx>,<offy>  Satz einer Zone",
        "#                            (A=Loch, B=neben Band, C=Band+Vorsatz),",
        "#                            SET ZCAL=<A|B|C>,OFF = Zone nutzt die Basiswerte",
        "#   SET RAWLOG=<0|1>         Zu jedem gueltigen Schuss ein \"raw\"-Telegramm mit",
        "#                            allen unkorrigierten Flanken und Parametern fuer",
        "#                            den Server-Simulator (Default 0)",
        "#   SET PAPERFEED=<0.0-150.0>      Papiervorschub je Schuss in mm (Default 50.0)",
        "#   SET PAPERSPEED=<0.5-30.0> Vorschub-Geschwindigkeit in mm/s fuer die erste",
        "#                            Haelfte der Strecke, danach Abbremsrampe (Default 5.0)",
        "#   SET PAPERAUTO=<0|1>      Automatischen Vorschub durchfuehren (Default 1/an)",
        "#   SET PAPERTRIGGER=<ANY|PIEZO|CLEAN>  Wann automatisch vorgeschoben wird:",
        "#                            ANY=jede Ausloesung, PIEZO=nur wenn das Piezo",
        "#                            ausgeloest hat (Default), CLEAN=nur bei \"clean\"-Schuss",
        "#   SET PAPERDIR=<0|1>       Vorschub-Drehrichtung invertieren (Default 1),",
        "#                            gleicht vertauschte Motoranschluesse softwareseitig aus",
        "#   SET PAPERJOGSPEED=<1.0-100.0>  Geschwindigkeit fuer den manuellen Dauer-",
        "#                            betrieb (Einfaedeln, Kippschalter) in mm/s (Default 75.0)",
        "#   TESTSHOOTPAPER            Loest den Papiervorschub aus, so als waere ein",
        "#                            Schuss registriert worden (kein echtes Schuss-Telegramm,",
        "#                            zaehlt nicht in shots/seq) - zum Testen von Mechanik/",
        "#                            Treiber ohne Schuss auf die Scheibe",
        "#   TESTSHOOT [<z_mm>]        Synthetischer Testschuss zum Pruefen der Kommunikation:",
        "#                            waehlt x/y zufaellig in [-z_mm, +z_mm] (Default 25.0 =",
        "#                            gesamte LG-Scheibe), erzeugt dazu passende Rohlaufzeiten",
        "#                            nach AKTUELLER Kalibrierung und laesst ein normales",
        "#                            shot/reject-Telegramm entstehen (inkl. Papiervorschub,",
        "#                            \"synthetic\":1 markiert es als Testschuss). Fehler bei",
        "#                            laufendem Schuss/Sperrzeit/aktiver CAL START-Sammlung.",
        "#   SET CALSHOTS=<3-20>      Anzahl Kalibrier-Schuesse fuer CAL START (Default 10)",
        "#   SET OFS0..OFS5=<-20000..20000>  Timing-Offset je Mikrofon in ns (Default 0,",
        "#                            wird durch CAL START automatisch gesetzt)",
        "#   SET MICEN0..MICEN5=<0|1> Mikrofon fuer Positionsloesung UND CAL START",
        "#                            beruecksichtigen (Default 1/an) - 0 schliesst",
        "#                            den Kanal komplett aus (z.B. hardwareseitig",
        "#                            auffaellige Kanaele gezielt abschalten)",
        "#   SET STATIC=<0|1>         Statische IP an/aus (Reboot noetig)",
        "#   SET IP=<ip>              Statische IP-Adresse (Reboot noetig)",
        "#   SET GW=<ip>              Gateway, auch: GATEWAY (Reboot noetig)",
        "#   SET SUBNET=<mask>        Subnetzmaske (Reboot noetig)",
        "#   SET DNS=<ip>             DNS-Server, leer = Gateway (Reboot noetig)",
        "# Kalibrierung (Timing-Offset je Mikrofon):",
        "#   CAL START                startet Sammlung von SET CALSHOTS Schuessen,",
        "#                            berechnet und speichert die Offsets danach automatisch.",
        "#                            Bei aktivem SET PIEZO zaehlen nur Schuesse mit",
        "#                            Piezo-Bestaetigung (piezo_ok) fuer die Kalibrierung",
        "#   CAL ABORT                bricht laufende Kalibrierung ab (Offsets unveraendert)",
        "#   CAL STATUS               zeigt Kalibrier-Fortschritt",
        "#   CAL RESET                setzt alle Mikrofon-Offsets auf 0 und die",
        "#                            Schallgeschwindigkeit auf 343 m/s zurueck",
        "#   CAL IMPORT OFS0=<ns>,...,OFS5=<ns>,SOUNDSPEED=<mps>  atomarer Bulk-",
        "#                            Restore (z.B. vom Stand-PC gespeicherte Kalibrierung) -",
        "#                            erst alle Werte validieren, dann erst schreiben",
        "# Weitere Befehle: SHOW  SHOWNET  SHOWZONES  STATUS  PING  RESET  REBOOT  FACTORY  HELP/?",
        "#   SHOWNET zeigt die Netzwerkkonfiguration (SSID/Pass/Host/Port/eigene",
        "#   IP-Konfiguration) UND den Live-Verbindungsstatus (wifi_ip/tcp_connected)",
        "#   separat von SHOW (Auswertungs-Parameter) und STATUS (Betriebsstatus)",
        "# Korrelations-ID: jeder Befehl kann mit \" #<id>\" enden (z.B. SET LANE=2 #7)",
        "# - die Antwort(en) auf GENAU diesen Befehl tragen dann zusaetzlich \"corr\":<id>",
        "# ACTION-Namensraum (kurzlebige Bedienbefehle, NICHT persistent):",
        "#   ACTION LIGHT=ON|OFF       uebersteuert die automatische Standbeleuchtung",
        "#   ACTION LIGHT=AUTO         gibt die Beleuchtung zurueck an serviceLight()",
        "#   ACTION TARGETCHANGE       loest einen Papiervorschub aus (wie TESTSHOOTPAPER,",
        "#                            gleicher Codepfad, sprechenderer Name fuer den",
        "#                            produktiven Scheibenwechsel-Bedienfall)",
        "# NET-Befehle (Sicherheitsnetz fuer Netzwerk-Fernkonfiguration):",
        "#   NET STATUS               zeigt, ob eine Netzwerkaenderung auf Bestaetigung",
        "#                            wartet (und ggf. verbleibende Zeit bis zum",
        "#                            automatischen Ruecksprung)",
        "#   NET CONFIRM              bestaetigt eine gerade aktive Netzwerkkonfiguration",
        "#                            endgueltig - erst NACH dem Reboot moeglich, der sie",
        "#                            uebernommen hat (siehe SET SSID/PASS/HOST/PORT/",
        "#                            STATIC/IP/GW/SUBNET/DNS: die erste solche Aenderung",
        "#                            je Bootzyklus sichert automatisch einen Ruecksprung-",
        "#                            punkt; wird binnen 3 Minuten nach dem naechsten Boot",
        "#                            kein NET CONFIRM gesendet, stellt der ESP32 die",
        "#                            zuletzt bestaetigte Konfiguration automatisch wieder",
        "#                            her und startet neu)",
    };
    for (size_t i = 0; i < sizeof(lines) / sizeof(lines[0]); i++) {
        Serial.println(lines[i]);
    }
}

// SET-Werte sind case-SENSITIV (SSID/Passwort!), deshalb wird hier nur das
// Schluesselwort normalisiert, nicht der Wert.
static bool handleSet(const String &raw)
{
    int eq = raw.indexOf('=');
    if (eq < 5) return false;                  // "SET x=" Minimum
    String key = raw.substring(4, eq);
    String val = raw.substring(eq + 1);
    key.trim(); key.toUpperCase();
    val.trim();

    if (key == "SSID") {
        armNetWatchdogIfNeeded();
        cfg.ssid = val; saveVal<String>("ssid", val);
        emitf("{\"type\":\"ok\",\"set\":\"ssid\",\"reboot_required\":true}\n");
    } else if (key == "PASS") {
        armNetWatchdogIfNeeded();
        cfg.pass = val; saveVal<String>("pass", val);
        emitf("{\"type\":\"ok\",\"set\":\"pass\",\"reboot_required\":true}\n");
    } else if (key == "HOST") {
        armNetWatchdogIfNeeded();
        cfg.host = val; saveVal<String>("host", val);
        emitf("{\"type\":\"ok\",\"set\":\"host\",\"reboot_required\":true}\n");
    } else if (key == "PORT") {
        long p = val.toInt();
        if (p < 1 || p > 65535) { emitLine("{\"type\":\"error\",\"msg\":\"port 1-65535\"}\n"); return true; }
        armNetWatchdogIfNeeded();
        cfg.port = (uint16_t)p; saveVal<uint16_t>("port", cfg.port);
        emitf("{\"type\":\"ok\",\"set\":\"port\",\"reboot_required\":true}\n");
    } else if (key == "LANE") {
        long l = val.toInt();
        if (l < 1 || l > 999) { emitLine("{\"type\":\"error\",\"msg\":\"lane 1-999\"}\n"); return true; }
        cfg.lane = (uint16_t)l; saveVal<uint16_t>("lane", cfg.lane);
        emitf("{\"type\":\"ok\",\"set\":\"lane\",\"value\":%u}\n", cfg.lane);
    } else if (key == "DEBOUNCE") {
        long v = val.toInt();
        if (v < 10 || v > 5000) { emitLine("{\"type\":\"error\",\"msg\":\"debounce 10-5000\"}\n"); return true; }
        cfg.debounceMs = (uint32_t)v; saveVal<uint32_t>("debounce", cfg.debounceMs);
        emitf("{\"type\":\"ok\",\"set\":\"debounce\",\"value\":%u}\n", cfg.debounceMs);
    } else if (key == "REJECTLOCK") {
        long v = val.toInt();
        if (v < 0 || v > 2000) { emitLine("{\"type\":\"error\",\"msg\":\"rejectlock 0-2000\"}\n"); return true; }
        cfg.rejectLockoutMs = (uint32_t)v; saveVal<uint32_t>("reject_lock", cfg.rejectLockoutMs);
        emitf("{\"type\":\"ok\",\"set\":\"rejectlock\",\"value\":%u}\n", cfg.rejectLockoutMs);
    } else if (key == "SENDREJECT") {
        long v = val.toInt();
        if (v != 0 && v != 1) { emitLine("{\"type\":\"error\",\"msg\":\"sendreject 0|1\"}\n"); return true; }
        cfg.sendRejects = (v == 1);
        saveVal<bool>("send_reject", cfg.sendRejects);
        emitf("{\"type\":\"ok\",\"set\":\"sendreject\",\"value\":%d}\n", cfg.sendRejects ? 1 : 0);
    } else if (key == "DEBUG") {
        long v = val.toInt();
        if (v < 0 || v > 3) { emitLine("{\"type\":\"error\",\"msg\":\"debug 0-3\"}\n"); return true; }
        cfg.debug = (uint8_t)v;
        saveVal<uint8_t>("debug", cfg.debug);
        emitf("{\"type\":\"ok\",\"set\":\"debug\",\"value\":%d}\n", cfg.debug);
    } else if (key == "OUTLIER") {
        long v = val.toInt();
        if (v < 0 || v > 500000) { emitLine("{\"type\":\"error\",\"msg\":\"outlier 0-500000\"}\n"); return true; }
        cfg.airOutlierUm = (uint32_t)v;
        saveVal<uint32_t>("outlier", cfg.airOutlierUm);
        emitf("{\"type\":\"ok\",\"set\":\"outlier\",\"value\":%u}\n", cfg.airOutlierUm);
    } else if (key == "RADIUS") {
        long v = val.toInt();
        if (v < 0 || v > 500000) { emitLine("{\"type\":\"error\",\"msg\":\"radius 0-500000\"}\n"); return true; }
        cfg.clusterRadiusUm = (uint32_t)v;
        saveVal<uint32_t>("cluster_r", cfg.clusterRadiusUm);
        emitf("{\"type\":\"ok\",\"set\":\"radius\",\"value\":%u}\n", cfg.clusterRadiusUm);
    } else if (key == "MINCLUSTER") {
        long v = val.toInt();
        if (v < 0 || v > AIR_MAX_COMBOS) { emitf("{\"type\":\"error\",\"msg\":\"mincluster 0-%d\"}\n", AIR_MAX_COMBOS); return true; }
        cfg.minClusterHits = (uint8_t)v;
        saveVal<uint8_t>("min_clust", cfg.minClusterHits);
        emitf("{\"type\":\"ok\",\"set\":\"mincluster\",\"value\":%d}\n", cfg.minClusterHits);
    } else if (key == "MAXPRECISION") {
        long v = val.toInt();
        if (v < 0 || v > 500000) { emitLine("{\"type\":\"error\",\"msg\":\"maxprecision 0-500000\"}\n"); return true; }
        cfg.maxPrecisionUm = (uint32_t)v;
        saveVal<uint32_t>("max_prec", cfg.maxPrecisionUm);
        emitf("{\"type\":\"ok\",\"set\":\"maxprecision\",\"value\":%u}\n", cfg.maxPrecisionUm);
    } else if (key == "MINMICS") {
        long v = val.toInt();
        if (v < 3 || v > NUM_AIR) { emitf("{\"type\":\"error\",\"msg\":\"minmics 3-%d\"}\n", NUM_AIR); return true; }
        cfg.minMics = (uint8_t)v;
        saveVal<uint8_t>("min_mics", cfg.minMics);
        emitf("{\"type\":\"ok\",\"set\":\"minmics\",\"value\":%d}\n", cfg.minMics);
    } else if (key == "TDOA") {
        long v = val.toInt();
        if (v < 100 || v > 5000) { emitLine("{\"type\":\"error\",\"msg\":\"tdoa 100-5000\"}\n"); return true; }
        cfg.airMaxTdoaUs = (uint32_t)v;
        saveVal<uint32_t>("tdoa_us", cfg.airMaxTdoaUs);
        emitf("{\"type\":\"ok\",\"set\":\"tdoa\",\"value\":%u}\n", cfg.airMaxTdoaUs);
    } else if (key == "STANDOFFPAPER") {
        float v = val.toFloat();
        if (v < 5.0f || v > 100.0f) { emitLine("{\"type\":\"error\",\"msg\":\"standoffpaper 5.0-100.0\"}\n"); return true; }
        cfg.standoffPaperMm = v;
        saveVal<float>("standoff_pa", cfg.standoffPaperMm);
        applyTargetGeometry();
        emitf("{\"type\":\"ok\",\"set\":\"standoffpaper\",\"value\":%.2f}\n", cfg.standoffPaperMm);
    } else if (key == "MICHALFX") {
        float v = val.toFloat();
        if (v < 5.0f || v > 300.0f) { emitLine("{\"type\":\"error\",\"msg\":\"michalfx 5.0-300.0\"}\n"); return true; }
        cfg.micHalfXMm = v;
        saveVal<float>("mic_half_x", cfg.micHalfXMm);
        applyMicHalfX();
        emitf("{\"type\":\"ok\",\"set\":\"michalfx\",\"value\":%.2f}\n", cfg.micHalfXMm);
    } else if (key == "BSHIFTPCT") {
        long v = val.toInt();
        if (v < 0 || v > 100) { emitLine("{\"type\":\"error\",\"msg\":\"bshiftpct 0-100\"}\n"); return true; }
        cfg.bulletShiftPct = (uint8_t)v;
        saveVal<uint8_t>("bshift_pct", cfg.bulletShiftPct);
        emitf("{\"type\":\"ok\",\"set\":\"bshiftpct\",\"value\":%d}\n", cfg.bulletShiftPct);
    } else if (key == "BSHIFTCAP") {
        float v = val.toFloat();
        if (v < 0.0f || v > 20.0f) { emitLine("{\"type\":\"error\",\"msg\":\"bshiftcap 0.0-20.0\"}\n"); return true; }
        cfg.bulletShiftCapMm = v;
        saveVal<float>("bshift_cap", cfg.bulletShiftCapMm);
        emitf("{\"type\":\"ok\",\"set\":\"bshiftcap\",\"value\":%.2f}\n", cfg.bulletShiftCapMm);
    } else if (key == "CLUSTERCORR") {
        long v = val.toInt();
        if (v != 0 && v != 1) { emitLine("{\"type\":\"error\",\"msg\":\"clustercorr 0|1\"}\n"); return true; }
        cfg.clusterCorr = (v == 1);
        saveVal<bool>("clu_corr", cfg.clusterCorr);
        emitf("{\"type\":\"ok\",\"set\":\"clustercorr\",\"value\":%d}\n", cfg.clusterCorr ? 1 : 0);
    } else if (key == "CLUSTERITER") {
        long v = val.toInt();
        if (v < 0 || v > 10) { emitLine("{\"type\":\"error\",\"msg\":\"clusteriter 0-10\"}\n"); return true; }
        cfg.clusterIter = (uint8_t)v;
        saveVal<uint8_t>("clu_iter", cfg.clusterIter);
        emitf("{\"type\":\"ok\",\"set\":\"clusteriter\",\"value\":%u}\n", cfg.clusterIter);
    } else if (key == "LSQ") {
        long v = val.toInt();
        if (v != 0 && v != 1) { emitLine("{\"type\":\"error\",\"msg\":\"lsq 0|1\"}\n"); return true; }
        cfg.lsq = (v == 1);
        saveVal<bool>("lsq", cfg.lsq);
        emitf("{\"type\":\"ok\",\"set\":\"lsq\",\"value\":%d}\n", cfg.lsq ? 1 : 0);
    } else if (key == "LSQPELLETR") {
        float v = val.toFloat();
        if (v < 0.0f || v > 5.0f) { emitLine("{\"type\":\"error\",\"msg\":\"lsqpelletr 0.0-5.0\"}\n"); return true; }
        cfg.lsqPelletRMm = v;
        saveVal<float>("lsq_pr", cfg.lsqPelletRMm);
        emitf("{\"type\":\"ok\",\"set\":\"lsqpelletr\",\"value\":%.2f}\n", cfg.lsqPelletRMm);
    } else if (key == "PIEZO") {
        long v = val.toInt();
        if (v != 0 && v != 1) { emitLine("{\"type\":\"error\",\"msg\":\"piezo 0|1\"}\n"); return true; }
        cfg.usePiezo = (v == 1);
        saveVal<bool>("use_piezo", cfg.usePiezo);
        emitf("{\"type\":\"ok\",\"set\":\"piezo\",\"value\":%d}\n", cfg.usePiezo ? 1 : 0);
    } else if (key == "CAPTURE") {
        // Rev 4.13.0: Zeitstempel der Luft-Mics per GPIO-ISR (Default) oder
        // MCPWM-Hardware-Capture. Wirkt erst nach REBOOT (Peripherie wird in
        // setup() eingerichtet), STATUS "capture" zeigt den aktiven Modus.
        String v = val; v.toUpperCase();
        if (v == "MCPWM")    cfg.airMcpwm = true;
        else if (v == "ISR") cfg.airMcpwm = false;
        else { emitLine("{\"type\":\"error\",\"msg\":\"capture isr|mcpwm\"}\n"); return true; }
        saveVal<bool>("air_mcpwm", cfg.airMcpwm);
        emitf("{\"type\":\"ok\",\"set\":\"capture\",\"value\":\"%s\","
              "\"reboot_required\":true}\n", cfg.airMcpwm ? "mcpwm" : "isr");
    } else if (key == "WALKK") {
        // Rev 4.14.0: Pulsbreiten-Korrektur (siehe walkDeltas()), wirkt sofort.
        float v = val.toFloat();
        if (v < 0.0f || v > 1.0f) { emitLine("{\"type\":\"error\",\"msg\":\"walkk 0.0-1.0\"}\n"); return true; }
        cfg.walkK = v;
        saveVal<float>("walk_k", cfg.walkK);
        if (v > 0.0f && !capActive) {
            emitf("{\"type\":\"ok\",\"set\":\"walkk\",\"value\":%.3f,"
                  "\"warn\":\"wirkt nur mit aktivem SET CAPTURE=MCPWM\"}\n", cfg.walkK);
        } else {
            emitf("{\"type\":\"ok\",\"set\":\"walkk\",\"value\":%.3f}\n", cfg.walkK);
        }
    } else if (key == "WALKCAL") {
        long v = val.toInt();
        if (v != 0 && v != 1) { emitLine("{\"type\":\"error\",\"msg\":\"walkcal 0|1\"}\n"); return true; }
        cfg.walkCal = (v == 1);
        saveVal<bool>("walk_cal", cfg.walkCal);
        emitf("{\"type\":\"ok\",\"set\":\"walkcal\",\"value\":%d}\n", cfg.walkCal ? 1 : 0);
    } else if (key == "WALKMAX") {
        long v = val.toInt();
        if (v < 5 || v > 500) { emitLine("{\"type\":\"error\",\"msg\":\"walkmax 5-500\"}\n"); return true; }
        cfg.walkMaxUs = (uint32_t)v;
        saveVal<uint32_t>("walk_max", cfg.walkMaxUs);
        emitf("{\"type\":\"ok\",\"set\":\"walkmax\",\"value\":%u}\n", (unsigned)cfg.walkMaxUs);
    } else if (key == "PIEZOREQ") {
        long v = val.toInt();
        if (v != 0 && v != 1) { emitLine("{\"type\":\"error\",\"msg\":\"piezoreq 0|1\"}\n"); return true; }
        cfg.piezoRequired = (v == 1);
        saveVal<bool>("piezo_req", cfg.piezoRequired);
        emitf("{\"type\":\"ok\",\"set\":\"piezoreq\",\"value\":%d}\n", cfg.piezoRequired ? 1 : 0);
    } else if (key == "ALGO") {
        String v = val; v.toUpperCase();
        uint8_t newAlgo;
        if (v == "CLASSIC")     newAlgo = ALGO_CLASSIC;
        else if (v == "RIM")    newAlgo = ALGO_RIM;
        else { emitLine("{\"type\":\"error\",\"msg\":\"algo classic|rim\"}\n"); return true; }
        cfg.algoMode = newAlgo;
        saveVal<uint8_t>("algo", cfg.algoMode);
        // Ringpuffer-/Piezo-Anker-Zustand des jeweils anderen Pfads verwerfen,
        // damit ein Umschalten mitten im Betrieb keinen halbfertigen Zustand
        // hinterlaesst (siehe resetShotState()).
        resetShotState();
        if (cfg.algoMode == ALGO_RIM && !cfg.usePiezo) {
            // ALGO=RIM braucht das Piezo zwingend als alleinigen Trigger
            // (siehe piezoISR()) - SET PIEZO=0 wird dabei NICHT ausgewertet,
            // ohne echtes Piezo-Signal loest dieser Pfad also gar nicht aus.
            emitf("{\"type\":\"ok\",\"set\":\"algo\",\"value\":\"%s\","
                  "\"warn\":\"ALGO=RIM ignoriert PIEZO=0 - Trigger ist immer das Piezo\"}\n", v.c_str());
        } else {
            emitf("{\"type\":\"ok\",\"set\":\"algo\",\"value\":\"%s\"}\n", v.c_str());
        }
    } else if (key == "PELLETR") {
        float v = val.toFloat();
        if (v < 0.0f || v > 10.0f) { emitLine("{\"type\":\"error\",\"msg\":\"pelletr 0.0-10.0\"}\n"); return true; }
        cfg.pelletRadiusMm = v;
        saveVal<float>("pellet_r", cfg.pelletRadiusMm);
        emitf("{\"type\":\"ok\",\"set\":\"pelletr\",\"value\":%.2f}\n", cfg.pelletRadiusMm);
    } else if (key == "MAXSIGMA") {
        float v = val.toFloat();
        if (v <= 0.0f || v > 50.0f) { emitLine("{\"type\":\"error\",\"msg\":\"maxsigma 0.0-50.0\"}\n"); return true; }
        cfg.maxSigmaMm = v;
        saveVal<float>("max_sigma", cfg.maxSigmaMm);
        emitf("{\"type\":\"ok\",\"set\":\"maxsigma\",\"value\":%.2f}\n", cfg.maxSigmaMm);
    } else if (key == "PIEZOMIN") {
        long v = val.toInt();
        if (v < 0 || v > 5000) { emitLine("{\"type\":\"error\",\"msg\":\"piezomin 0-5000\"}\n"); return true; }
        cfg.piezoMinUs = (uint32_t)v;
        saveVal<uint32_t>("piezo_min", cfg.piezoMinUs);
        updatePostWaitUs();   // SET ALGO=RIM, siehe dort
        emitf("{\"type\":\"ok\",\"set\":\"piezomin\",\"value\":%u}\n", cfg.piezoMinUs);
    } else if (key == "PIEZOMAX") {
        long v = val.toInt();
        if (v < 0 || v > 5000) { emitLine("{\"type\":\"error\",\"msg\":\"piezomax 0-5000\"}\n"); return true; }
        cfg.piezoMaxUs = (uint32_t)v;
        saveVal<uint32_t>("piezo_max", cfg.piezoMaxUs);
        emitf("{\"type\":\"ok\",\"set\":\"piezomax\",\"value\":%u}\n", cfg.piezoMaxUs);
    } else if (key == "TESTMODE") {
        long v = val.toInt();
        if (v != 0 && v != 1) { emitLine("{\"type\":\"error\",\"msg\":\"testmode 0|1\"}\n"); return true; }
        // Bewusst NICHT persistiert (kein saveVal) - siehe Testmodus-
        // Hinweis bei testMode oben: nach Reboot immer wieder aus.
        testMode = (v == 1);
        resetShotState();
        noInterrupts();
        for (int i = 0; i <= NUM_AIR; i++) { testFired[i] = false; testLastFireUs[i] = 0; }
        interrupts();
        testLastActivityUs = (uint64_t)esp_timer_get_time();
        testSeparatorShown = false;
        testSeriesActive   = false;
        emitf("{\"type\":\"ok\",\"set\":\"testmode\",\"value\":%d}\n", testMode ? 1 : 0);
    } else if (key == "TESTCOOLDOWN") {
        long v = val.toInt();
        if (v < 0 || v > 10000) { emitLine("{\"type\":\"error\",\"msg\":\"testcooldown 0-10000\"}\n"); return true; }
        cfg.testCooldownMs = (uint32_t)v;
        saveVal<uint32_t>("test_cd_ms", cfg.testCooldownMs);
        emitf("{\"type\":\"ok\",\"set\":\"testcooldown\",\"value\":%u}\n", cfg.testCooldownMs);
    } else if (key == "OFFSETX") {
        long v = val.toInt();
        if (v < -50000 || v > 50000) { emitLine("{\"type\":\"error\",\"msg\":\"offsetx -50000..50000\"}\n"); return true; }
        cfg.offsetXUm = (int32_t)v;
        saveVal<int32_t>("ofs_x_um", cfg.offsetXUm);
        emitf("{\"type\":\"ok\",\"set\":\"offsetx\",\"value\":%ld}\n", (long)cfg.offsetXUm);
    } else if (key == "OFFSETY") {
        long v = val.toInt();
        if (v < -50000 || v > 50000) { emitLine("{\"type\":\"error\",\"msg\":\"offsety -50000..50000\"}\n"); return true; }
        cfg.offsetYUm = (int32_t)v;
        saveVal<int32_t>("ofs_y_um", cfg.offsetYUm);
        emitf("{\"type\":\"ok\",\"set\":\"offsety\",\"value\":%ld}\n", (long)cfg.offsetYUm);
    } else if (key == "SOUNDSPEED") {
        // Seit Rev 4.15.0 mit Nachkommastelle (z.B. 342.5); gilt bei SET DHT=0
        // bzw. solange keine gueltige DHT-Messung vorliegt.
        float v = val.toFloat();
        if (v < SOUND_SPEED_MIN_MPS || v > SOUND_SPEED_MAX_MPS) {
            emitf("{\"type\":\"error\",\"msg\":\"soundspeed %d-%d\"}\n",
                  SOUND_SPEED_MIN_MPS, SOUND_SPEED_MAX_MPS);
            return true;
        }
        cfg.soundSpeedMps = v;
        saveVal<float>("sound_f", cfg.soundSpeedMps);
        applySoundSpeed();
        emitf("{\"type\":\"ok\",\"set\":\"soundspeed\",\"value\":%.1f,\"active_mps\":%.2f}\n",
              cfg.soundSpeedMps, soundMpsActive);
    } else if (key == "ZONES") {
        // Rev 4.17.0: Zonen-Kalibrierung an/aus (wirkt nur mit ZHOLE > 0 und
        // ALGO=CLASSIC), siehe zoneSolve()
        long v = val.toInt();
        if (v != 0 && v != 1) { emitLine("{\"type\":\"error\",\"msg\":\"zones 0|1\"}\n"); return true; }
        cfg.zonesOn = (v == 1);
        saveVal<bool>("z_on", cfg.zonesOn);
        emitf("{\"type\":\"ok\",\"set\":\"zones\",\"value\":%d,\"active\":%d}\n",
              cfg.zonesOn ? 1 : 0, zonesActive() ? 1 : 0);
    } else if (key == "ZHOLE" || key == "ZBAND" || key == "ZBLEND") {
        // Rev 4.17.0: Zonengrenzen in 0.001mm - ZHOLE = Radius des Lochs der
        // Vorsatzscheibe (LG 15250, LP 29750; vom Stand-PC je Scheibentyp
        // gesetzt), ZBAND = halbe Bandbreite, ZBLEND = Ueberblendung
        long v = val.toInt();
        const long maxV = key == "ZHOLE" ? 60000 : (key == "ZBAND" ? 100000 : 10000);
        if (v < 0 || v > maxV) {
            emitf("{\"type\":\"error\",\"msg\":\"%s 0-%ld\"}\n", key == "ZHOLE" ? "zhole" : (key == "ZBAND" ? "zband" : "zblend"), maxV);
            return true;
        }
        if (key == "ZHOLE")      { cfg.zHoleUm  = (uint32_t)v; saveVal<uint32_t>("z_hole",  cfg.zHoleUm); }
        else if (key == "ZBAND") { cfg.zBandUm  = (uint32_t)v; saveVal<uint32_t>("z_band",  cfg.zBandUm); }
        else                     { cfg.zBlendUm = (uint32_t)v; saveVal<uint32_t>("z_blend", cfg.zBlendUm); }
        String lk = key; lk.toLowerCase();
        emitf("{\"type\":\"ok\",\"set\":\"%s\",\"value\":%ld}\n", lk.c_str(), v);
    } else if (key == "ZCAL") {
        // Rev 4.17.0: Satz einer Zone, atomar (erst alles pruefen, dann
        // schreiben): ZCAL=<A|B|C>,<mps>,<ofs0>,...,<ofs5>,<offx>,<offy>
        // bzw. ZCAL=<A|B|C>,OFF (Zone nutzt die Basiswerte)
        String v = val; v.toUpperCase();
        int zi = -1;
        if (v.length() >= 1) {
            for (int k = 0; k < ZONE_COUNT; k++) if (v[0] == ZONE_NAMES[k]) zi = k;
        }
        if (zi < 0 || v.length() < 3 || v[1] != ',') {
            emitLine("{\"type\":\"error\",\"msg\":\"zcal <A|B|C>,<mps>,<ofs0..5>,<offx>,<offy> | <A|B|C>,OFF\"}\n");
            return true;
        }
        String rest = v.substring(2); rest.trim();
        if (rest == "OFF") {
            memset(&cfg.zone[zi], 0, sizeof(ZoneCal));
            saveZoneSet(zi);
            emitf("{\"type\":\"ok\",\"set\":\"zcal\",\"zone\":\"%c\",\"valid\":0}\n", ZONE_NAMES[zi]);
            return true;
        }
        float  nums[9];
        int    cnt = 0, start = 0;
        bool   ok = true;
        while (ok && cnt < 9) {
            int comma = rest.indexOf(',', start);
            String tok = comma < 0 ? rest.substring(start) : rest.substring(start, comma);
            tok.trim();
            if (tok.length() == 0) { ok = false; break; }
            nums[cnt++] = tok.toFloat();
            if (comma < 0) break;
            start = comma + 1;
        }
        if (!ok || cnt != 9 || nums[0] < SOUND_SPEED_MIN_MPS || nums[0] > SOUND_SPEED_MAX_MPS) ok = false;
        for (int i = 1; ok && i <= NUM_AIR; i++) {
            if (nums[i] < -MIC_OFS_MAX_NS || nums[i] > MIC_OFS_MAX_NS) ok = false;
        }
        if (ok && (fabsf(nums[7]) > 50000.0f || fabsf(nums[8]) > 50000.0f)) ok = false;
        if (!ok) {
            emitLine("{\"type\":\"error\",\"msg\":\"zcal: 9 Werte (mps 300-400, ofs -20000..20000, offx/offy -50000..50000)\"}\n");
            return true;
        }
        ZoneCal z;
        memset(&z, 0, sizeof(z));
        z.valid    = 1;
        z.soundMps = roundf(nums[0] * 10.0f) / 10.0f;
        for (int i = 0; i < NUM_AIR; i++) z.micOffsetNs[i] = (int32_t)lroundf(nums[1 + i]);
        z.offsetXUm = (int32_t)lroundf(nums[7]);
        z.offsetYUm = (int32_t)lroundf(nums[8]);
        cfg.zone[zi] = z;
        saveZoneSet(zi);
        emitf("{\"type\":\"ok\",\"set\":\"zcal\",\"zone\":\"%c\",\"valid\":1}\n", ZONE_NAMES[zi]);
    } else if (key == "RAWLOG") {
        // Rev 4.16.0: "raw"-Telegramm zu jedem gueltigen Schuss (siehe
        // emitRawTelegram()), wirkt sofort.
        long v = val.toInt();
        if (v != 0 && v != 1) { emitLine("{\"type\":\"error\",\"msg\":\"rawlog 0|1\"}\n"); return true; }
        cfg.rawLog = (v == 1);
        saveVal<bool>("raw_log", cfg.rawLog);
        emitf("{\"type\":\"ok\",\"set\":\"rawlog\",\"value\":%d}\n", cfg.rawLog ? 1 : 0);
    } else if (key == "DHT") {
        // Rev 4.15.0: Schallgeschwindigkeit aus DHT22 (GPIO15) an/aus, wirkt
        // sofort. Erste Messung ca. 2,5 s nach dem Einschalten (serviceDht()),
        // bis dahin und bei SET DHT=0 gilt SET SOUNDSPEED.
        long v = val.toInt();
        if (v != 0 && v != 1) { emitLine("{\"type\":\"error\",\"msg\":\"dht 0|1\"}\n"); return true; }
        cfg.useDht = (v == 1);
        saveVal<bool>("dht_en", cfg.useDht);
        if (!cfg.useDht) {
            dhtValid = false;
            applySoundSpeed();
        }
        emitf("{\"type\":\"ok\",\"set\":\"dht\",\"value\":%d}\n", cfg.useDht ? 1 : 0);
    } else if (key == "PAPERFEED") {
        float v = val.toFloat();
        if (v < 0.0f || v > 150.0f) { emitLine("{\"type\":\"error\",\"msg\":\"paperfeed 0.0-150.0\"}\n"); return true; }
        cfg.paperFeedMm = v;
        saveVal<float>("paper_mm", cfg.paperFeedMm);
        emitf("{\"type\":\"ok\",\"set\":\"paperfeed\",\"value\":%.2f}\n", cfg.paperFeedMm);
    } else if (key == "PAPERSPEED") {
        float v = val.toFloat();
        if (v < 0.5f || v > 30.0f) { emitLine("{\"type\":\"error\",\"msg\":\"paperspeed 0.5-30.0\"}\n"); return true; }
        cfg.paperSpeedMmS = v;
        saveVal<float>("paper_mmps", cfg.paperSpeedMmS);
        emitf("{\"type\":\"ok\",\"set\":\"paperspeed\",\"value\":%.2f}\n", cfg.paperSpeedMmS);
    } else if (key == "PAPERAUTO") {
        long v = val.toInt();
        if (v != 0 && v != 1) { emitLine("{\"type\":\"error\",\"msg\":\"paperauto 0|1\"}\n"); return true; }
        cfg.paperAuto = (v == 1);
        saveVal<bool>("paper_auto", cfg.paperAuto);
        emitf("{\"type\":\"ok\",\"set\":\"paperauto\",\"value\":%d}\n", cfg.paperAuto ? 1 : 0);
    } else if (key == "PAPERTRIGGER") {
        String v = val; v.toUpperCase();
        uint8_t newTrig;
        if (v == "ANY")        newTrig = PAPER_TRIG_ANY;
        else if (v == "PIEZO") newTrig = PAPER_TRIG_PIEZO;
        else if (v == "CLEAN") newTrig = PAPER_TRIG_CLEAN;
        else { emitLine("{\"type\":\"error\",\"msg\":\"papertrigger any|piezo|clean\"}\n"); return true; }
        cfg.paperTrigger = newTrig;
        saveVal<uint8_t>("paper_trig", cfg.paperTrigger);
        emitf("{\"type\":\"ok\",\"set\":\"papertrigger\",\"value\":\"%s\"}\n", v.c_str());
    } else if (key == "PAPERDIR") {
        long v = val.toInt();
        if (v != 0 && v != 1) { emitLine("{\"type\":\"error\",\"msg\":\"paperdir 0|1\"}\n"); return true; }
        cfg.paperDirInvert = (v == 1);
        saveVal<bool>("paper_dir", cfg.paperDirInvert);
        emitf("{\"type\":\"ok\",\"set\":\"paperdir\",\"value\":%d}\n", cfg.paperDirInvert ? 1 : 0);
    } else if (key == "PAPERJOGSPEED") {
        float v = val.toFloat();
        if (v < PAPER_JOG_SPEED_MIN_MMPS || v > PAPER_JOG_SPEED_MAX_MMPS) {
            emitf("{\"type\":\"error\",\"msg\":\"paperjogspeed %.1f-%.1f\"}\n",
                  PAPER_JOG_SPEED_MIN_MMPS, PAPER_JOG_SPEED_MAX_MMPS);
            return true;
        }
        cfg.paperJogSpeedMmS = v;
        saveVal<float>("paper_jog", cfg.paperJogSpeedMmS);
        emitf("{\"type\":\"ok\",\"set\":\"paperjogspeed\",\"value\":%.2f}\n", cfg.paperJogSpeedMmS);
    } else if (key == "CALSHOTS") {
        long v = val.toInt();
        if (v < 3 || v > MAX_CAL_SHOTS) { emitf("{\"type\":\"error\",\"msg\":\"calshots 3-%d\"}\n", MAX_CAL_SHOTS); return true; }
        cfg.calShotCount = (uint8_t)v;
        saveVal<uint8_t>("cal_n", cfg.calShotCount);
        emitf("{\"type\":\"ok\",\"set\":\"calshots\",\"value\":%d}\n", cfg.calShotCount);
    } else if (key.startsWith("OFS") && key.length() == 4 && isDigit(key[3])) {
        int idx = key[3] - '0';
        if (idx >= NUM_AIR) { emitLine("{\"type\":\"error\",\"msg\":\"unknown key\"}\n"); return true; }
        long v = val.toInt();
        if (v < -MIC_OFS_MAX_NS || v > MIC_OFS_MAX_NS) {
            emitf("{\"type\":\"error\",\"msg\":\"ofs%d %d..%d\"}\n", idx, -MIC_OFS_MAX_NS, MIC_OFS_MAX_NS);
            return true;
        }
        cfg.micOffsetNs[idx] = (int32_t)v;
        char key2[8];
        snprintf(key2, sizeof(key2), "ofs%d", idx);
        saveVal<int32_t>(key2, cfg.micOffsetNs[idx]);
        emitf("{\"type\":\"ok\",\"set\":\"ofs%d\",\"value\":%ld}\n", idx, (long)cfg.micOffsetNs[idx]);
    } else if (key.startsWith("MICEN") && key.length() == 6 && isDigit(key[5])) {
        int idx = key[5] - '0';
        if (idx >= NUM_AIR) { emitLine("{\"type\":\"error\",\"msg\":\"unknown key\"}\n"); return true; }
        long v = val.toInt();
        if (v != 0 && v != 1) { emitf("{\"type\":\"error\",\"msg\":\"micen%d 0|1\"}\n", idx); return true; }
        cfg.micEnabled[idx] = (v == 1);
        char key2[8];
        snprintf(key2, sizeof(key2), "mic_en%d", idx);
        saveVal<bool>(key2, cfg.micEnabled[idx]);
        emitf("{\"type\":\"ok\",\"set\":\"micen%d\",\"value\":%d}\n", idx, cfg.micEnabled[idx] ? 1 : 0);
    } else if (key == "WINDOW") {
        long v = val.toInt();
        if (v < 1 || v > 50) { emitLine("{\"type\":\"error\",\"msg\":\"window 1-50\"}\n"); return true; }
        cfg.windowMs = (uint32_t)v; saveVal<uint32_t>("window", cfg.windowMs);
        emitf("{\"type\":\"ok\",\"set\":\"window\",\"value\":%u}\n", cfg.windowMs);
    } else if (key == "STATIC") {
        long v = val.toInt();
        if (v != 0 && v != 1) { emitLine("{\"type\":\"error\",\"msg\":\"static 0|1\"}\n"); return true; }
        armNetWatchdogIfNeeded();
        cfg.staticIP = (v == 1);
        saveVal<bool>("static_ip", cfg.staticIP);
        emitf("{\"type\":\"ok\",\"set\":\"static_ip\",\"value\":%d,\"reboot_required\":true}\n",
              cfg.staticIP ? 1 : 0);
    } else if (key == "IP") {
        IPAddress tmp;
        if (!tmp.fromString(val)) { emitLine("{\"type\":\"error\",\"msg\":\"invalid IP address\"}\n"); return true; }
        armNetWatchdogIfNeeded();
        cfg.ip = val; saveVal<String>("ip", val);
        emitf("{\"type\":\"ok\",\"set\":\"ip\",\"value\":\"%s\",\"reboot_required\":true}\n", val.c_str());
    } else if (key == "GW" || key == "GATEWAY") {
        IPAddress tmp;
        if (!tmp.fromString(val)) { emitLine("{\"type\":\"error\",\"msg\":\"invalid gateway address\"}\n"); return true; }
        armNetWatchdogIfNeeded();
        cfg.gateway = val; saveVal<String>("gateway", val);
        emitf("{\"type\":\"ok\",\"set\":\"gateway\",\"value\":\"%s\",\"reboot_required\":true}\n", val.c_str());
    } else if (key == "SUBNET") {
        IPAddress tmp;
        if (!tmp.fromString(val)) { emitLine("{\"type\":\"error\",\"msg\":\"invalid subnet mask\"}\n"); return true; }
        armNetWatchdogIfNeeded();
        cfg.subnet = val; saveVal<String>("subnet", val);
        emitf("{\"type\":\"ok\",\"set\":\"subnet\",\"value\":\"%s\",\"reboot_required\":true}\n", val.c_str());
    } else if (key == "DNS") {
        if (val.length() > 0) {
            IPAddress tmp;
            if (!tmp.fromString(val)) { emitLine("{\"type\":\"error\",\"msg\":\"invalid DNS address\"}\n"); return true; }
        }
        armNetWatchdogIfNeeded();
        cfg.dns = val; saveVal<String>("dns", val);
        emitf("{\"type\":\"ok\",\"set\":\"dns\",\"value\":\"%s\",\"reboot_required\":true}\n", val.c_str());
    } else if (key == "PSK") {
        // TCP-Authentifizierung (siehe AUTH_PSK_DEFAULT_HEX oben) - bewusst
        // KEIN armNetWatchdogIfNeeded()/reboot_required: wirkt wie die
        // uebrigen Betriebsparameter sofort, ist kein Netzwerk-Feld im Sinne
        // des Sicherheitsnetzes (Abschnitt 7.6 der Protokoll-Referenz).
        if (val.length() != 64) {
            emitLine("{\"type\":\"error\",\"msg\":\"psk muss 64 Hex-Zeichen (32 Byte) sein\"}\n");
            return true;
        }
        for (size_t i = 0; i < val.length(); i++) {
            if (hexNibble(val[i]) < 0) {
                emitLine("{\"type\":\"error\",\"msg\":\"psk: ungueltiges Hex-Zeichen\"}\n");
                return true;
            }
        }
        cfg.pskHex = val; saveVal<String>("psk", val);
        applyPskHex(val);
        emitLine("{\"type\":\"ok\",\"set\":\"psk\"}\n"); // Wert nie zurueckspiegeln (wie PASS)
    } else {
        emitLine("{\"type\":\"error\",\"msg\":\"unknown key\"}\n");
    }
    return true;
}

// ---------------------------------------------------------------------------
// ACTION-Namensraum (Rev 4.9.0) - kurzlebige Bedienbefehle, bewusst getrennt
// von SET (das persistente Konfiguration ist): siehe docs/remote-
// interaktion-konzept.md Abschnitt 6. upperRest = alles nach "ACTION "
// (bereits getrimmt/GROSS, siehe handleCommand()).
// ---------------------------------------------------------------------------
static void handleActionCommand(const String &upperRest)
{
    if (upperRest.startsWith("LIGHT=")) {
        String v = upperRest.substring(6);
        if (v == "ON")        { lightOverride = LIGHT_FORCE_ON;  digitalWrite(LIGHT_PIN, HIGH); }
        else if (v == "OFF")  { lightOverride = LIGHT_FORCE_OFF; digitalWrite(LIGHT_PIN, LOW); }
        else if (v == "AUTO") { lightOverride = LIGHT_AUTO; }
        else {
            emitLine("{\"type\":\"error\",\"msg\":\"action light on|off|auto\"}\n");
            return;
        }
        emitf("{\"type\":\"ok\",\"action\":\"light\",\"state\":\"%s\"}\n",
              lightOverride == LIGHT_FORCE_ON ? "on"
                  : (lightOverride == LIGHT_FORCE_OFF ? "off" : "auto"));
        return;
    }
    if (upperRest == "TARGETCHANGE") {
        // Identischer Codepfad wie TESTSHOOTPAPER (siehe dortigen Kommentar)
        // - eigener, fuer den produktiven Scheibenwechsel sprechender Name.
        if (paperMode != PAPER_IDLE) {
            emitLine("{\"type\":\"error\",\"msg\":\"paper feed busy\"}\n");
            return;
        }
        startPaperFeed();
        emitLine("{\"type\":\"ok\",\"action\":\"targetchange\"}\n");
        return;
    }
    emitLine("{\"type\":\"error\",\"msg\":\"unknown action\"}\n");
}

// ---------------------------------------------------------------------------
// TESTSHOOT [<z_mm>] - synthetischer Testschuss zum Pruefen der Kommunikations-
// strecke zum Stand-PC, OHNE echte Sensorik/Munition. Waehlt x/y zufaellig
// gleichverteilt in [-z_mm, +z_mm] mm (Default z_mm=25.0, "gesamte LG-
// Scheibe"), berechnet daraus fuer jeden aktiven Mikrofonkanal (SET MICEN0..
// MICEN5) die nach AKTUELLER Kalibrierung (SET OFS0..OFS5, Zielgeometrie
// SET STANDOFFPAPER/MICHALFX, SET SOUNDSPEED) passende
// Rohlaufzeit und speist sie GENAU in dieselben Puffer ein, die sonst
// airISR()/piezoISR() fuellen (airCC/airCount/firstAirCC/firstHitTimeUs/
// piezoCC/piezoSeen/shotInProgress). Der weitere Ablauf (Fensterlogik in
// loop(), processShot(), automatischer Papiervorschub, ggf. CAL-Sammlung)
// laeuft DANACH unveraendert wie bei einem echten Schuss - dadurch entsteht
// ein regulaeres "shot"- (oder ggf. "reject"-)Telegramm inkl. air_ns = den
// soeben erzeugten Rohwerten, ohne jede Sonderbehandlung im Auswertungscode.
// Einziger Unterschied nach aussen: das zusaetzliche "synthetic":1-Feld
// (siehe syntheticShotPending/processShot()), damit der Stand-PC einen
// Testschuss klar von einem echten Treffer unterscheiden kann.
// ---------------------------------------------------------------------------
static void handleTestShoot(float zMm)
{
    // SET ALGO=RIM: "Schuss in Bearbeitung" heisst hier piezoPending statt
    // shotInProgress (das bleibt bei ALGO=RIM staendig false, siehe airISR()/
    // piezoISR()).
    const bool busy = (cfg.algoMode == ALGO_RIM) ? piezoPending : shotInProgress;
    if (busy) {
        emitLine("{\"type\":\"error\",\"msg\":\"shot in progress\"}\n");
        return;
    }
    const uint64_t nowUs = (uint64_t)esp_timer_get_time();
    if (nowUs < lockoutUntil) {
        emitLine("{\"type\":\"error\",\"msg\":\"debounce active\"}\n");
        return;
    }
    if (calActive) {
        // Wuerde sonst als (voellig unrealistisch "perfekter") Kalibrier-
        // Schuss in CAL START einfliessen und eine echte Kalibrierung
        // verfaelschen - siehe runCalibration()/calCost().
        emitLine("{\"type\":\"error\",\"msg\":\"testshoot not allowed during active calibration\"}\n");
        return;
    }

    const long xUm = random(-(long)(zMm * 1000.0f), (long)(zMm * 1000.0f) + 1);
    const long yUm = random(-(long)(zMm * 1000.0f), (long)(zMm * 1000.0f) + 1);
    const float xMm = (float)xUm / 1000.0f;
    const float yMm = (float)yUm / 1000.0f;

    // Rohlaufzeiten je Mikrofon (Schallausbreitung vom synthetischen
    // Trefferpunkt) - dieselbe Geometrie (MIC_X/MIC_Y/micStandoffMm) und
    // Schallgeschwindigkeit (soundMmPerNs) wie in solveAirPosition().
    float rawToFNs[NUM_AIR];
    bool  seen[NUM_AIR];
    float minToFNs = 0.0f;
    bool  haveMin  = false;
    for (int i = 0; i < NUM_AIR; i++) {
        seen[i] = cfg.micEnabled[i];
        if (!seen[i]) continue;
        float dx   = xMm - MIC_X[i], dy = yMm - MIC_Y[i];
        float dist = sqrtf(dx * dx + dy * dy + micStandoffMm * micStandoffMm);
        rawToFNs[i] = dist / soundMmPerNs;
        if (!haveMin || rawToFNs[i] < minToFNs) { minToFNs = rawToFNs[i]; haveMin = true; }
    }
    if (!haveMin) {
        emitLine("{\"type\":\"error\",\"msg\":\"testshoot: no mic enabled (SET MICEN0..MICEN5)\"}\n");
        return;
    }

    // Piezo-Verzoegerung relativ zum ersten Luft-Ereignis, konsistent mit
    // processShot()/piezoOk (siehe Rev-4.3-Hinweis im Header-Kommentar):
    // Laufzeit durch die Papier->Stahl-Luecke, mittig im erlaubten Fenster
    // SET PIEZOMIN..PIEZOMAX gewaehlt.
    const float piezoTargetNs = (float)(cfg.piezoMinUs + cfg.piezoMaxUs) * 1000.0f / 2.0f;

    if (cfg.algoMode == ALGO_RIM) {
        // Ringpuffer-Pfad: Anker = Piezo (t=0), jede Mic-Flanke relativ dazu
        // ist t0 + Laufzeit mit t0 = -piezoTargetNs - siehe
        // processShotAnchored()/schusserkennung-rev5.md. SET PIEZO=0 wird
        // hier bewusst IGNORIERT (siehe SET ALGO-Warnhinweis in handleSet())
        // - ohne Piezo-Anker kann ALGO=RIM prinzipbedingt nicht ausloesen,
        // TESTSHOOT soll den vorgesehenen Ablauf trotzdem zeigen koennen.
        noInterrupts();
        const uint32_t anchorCC = esp_cpu_get_cycle_count();
        for (int i = 0; i < NUM_AIR; i++) {
            if (!seen[i]) continue;
            const float   edgeNs = rawToFNs[i] - piezoTargetNs + (float)cfg.micOffsetNs[i];
            const int32_t dCC    = (int32_t)lroundf(edgeNs * (float)cpuMHz / 1000.0f);
            const uint8_t rh     = ringHead[i];
            ringCC[i][rh] = anchorCC + (uint32_t)dCC;
            ringUs[i][rh] = (uint32_t)nowUs;
            ringHead[i]   = (rh + 1) % EDGE_RING;
        }
        piezoAnchorCC        = anchorCC;
        piezoAnchorUs        = nowUs;
        freezeCC             = anchorCC + postWaitUs * cpuMHz;
        freezeActive         = true;
        piezoPending         = true;
        syntheticShotPending = true;
        interrupts();
    } else {
        // --- Klassischer Pfad (unveraendert) -------------------------------
        const bool usePiezoNow = cfg.usePiezo;
        noInterrupts();
        // Anker fuer alle synthetischen Zeiten dieses Fensters - reiner
        // Momentaufnahme-Wert, kein echtes ISR-Ereignis. airCC[i][0]/piezoCC
        // werden unten als (firstAirCC + gewuenschtes Delta in CPU-Zyklen)
        // gesetzt - dieselbe wrap-sichere uint32_t-Arithmetik wie beim
        // Auslesen in processShot() ("dCC = localAirCC[i][e] - localFirstAirCC").
        firstAirCC = esp_cpu_get_cycle_count();
        for (int i = 0; i < NUM_AIR; i++) {
            if (!seen[i]) { airCount[i] = 0; continue; }
            // Nullpunkt = fruehestes Mikrofon (minToFNs).
            float   targetNs = rawToFNs[i] - minToFNs;
            float   rawNsWithOffset = targetNs + (float)cfg.micOffsetNs[i];
            int32_t dCC = (int32_t)lroundf(rawNsWithOffset * (float)cpuMHz / 1000.0f);
            airCC[i][0] = firstAirCC + (uint32_t)dCC;
            airCount[i] = 1;
        }
        if (usePiezoNow) {
            int32_t dCCPiezo = (int32_t)lroundf(piezoTargetNs * (float)cpuMHz / 1000.0f);
            piezoCC   = firstAirCC + (uint32_t)dCCPiezo;
            piezoSeen = true;
        } else {
            piezoCC   = 0;
            piezoSeen = false;
        }
        firstHitTimeUs       = nowUs;
        syntheticShotPending = true;
        shotInProgress       = true;
        interrupts();
    }

    emitf("{\"type\":\"ok\",\"cmd\":\"testshoot\",\"z_mm\":%.2f,"
          "\"target_x_um\":%ld,\"target_y_um\":%ld}\n",
          zMm, xUm, yUm);
}

static void handleCommand(const String &rawCmd, bool viaTcp)
{
    String raw = rawCmd;
    raw.trim();
    if (raw.length() == 0) return;

    // Authentifizierung (nur TCP, nur wenn ein Schluessel konfiguriert ist -
    // siehe AUTH_PSK_DEFAULT_HEX/verifyIncomingIfTcp oben): VOR dem
    // Korrelations-ID-Parsing pruefen, damit ein nicht authentifizierter
    // Aufrufer nicht einmal eine mit "corr" versehene Fehlerantwort
    // provozieren kann.
    if (!verifyIncomingIfTcp(raw, viaTcp)) {
        emitLine("{\"type\":\"error\",\"msg\":\"auth\"}\n");
        return;
    }

    // Korrelations-ID (" #<id>", siehe pendingCorrId): abtrennen, BEVOR der
    // eigentliche Befehl geparst wird - der Rest der Funktion sieht davon
    // nichts mehr. pollCommands() setzt pendingCorrId nach dem Aufruf dieser
    // Funktion wieder zurueck. Bewusst NUR ein rein numerischer Suffix nach
    // " #" wird erkannt, alles andere (z.B. ein woertliches "#" in SET
    // PASS=...) bleibt unangetastet Teil des Befehls.
    pendingCorrId = -1;
    int hashIdx = raw.lastIndexOf(" #");
    if (hashIdx >= 0) {
        String idStr = raw.substring(hashIdx + 2);
        bool allDigits = idStr.length() > 0;
        for (size_t i = 0; i < idStr.length() && allDigits; i++) {
            if (!isDigit(idStr[i])) allDigits = false;
        }
        if (allDigits) {
            pendingCorrId = idStr.toInt();
            raw = raw.substring(0, hashIdx);
            raw.trim();
        }
    }

    // Nur fuer Schluesselwort-Vergleich eine Gross-Kopie anlegen
    String upper = raw;
    upper.toUpperCase();

    if (upper.startsWith("SET ")) {
        handleSet(raw);                       // Original wg. case-sensitiver Werte
        return;
    }
    if (upper.startsWith("ACTION ")) {
        String rest = upper.substring(7);
        rest.trim();
        handleActionCommand(rest);
        return;
    }
    if (upper.startsWith("NET")) {
        String sub = upper.substring(3);
        sub.trim();
        if (sub == "CONFIRM") {
            // Darf bewusst erst NACH dem Reboot in die neuen Werte bestaetigt
            // werden (netWatchdogArmed==true) - eine Bestaetigung VOR dem
            // Reboot wuerde das Sicherheitsnetz aufheben, ohne dass die neuen
            // Werte je erreichbar waren (siehe armNetWatchdogIfNeeded()/
            // serviceNetWatchdog()).
            if (!netWatchdogArmed) {
                emitLine(netPendingLoaded
                    ? "{\"type\":\"error\",\"msg\":\"reboot required before confirming\"}\n"
                    : "{\"type\":\"error\",\"msg\":\"no pending network change\"}\n");
                return;
            }
            prefs.begin(NVS_NS, false);
            prefs.putBool("net_pnd", false);
            prefs.end();
            netWatchdogArmed         = false;
            netSnapshotArmedThisBoot = false;
            netPendingLoaded         = false;
            emitLine("{\"type\":\"ok\",\"cmd\":\"net_confirm\"}\n");
        } else if (sub == "STATUS") {
            if (netWatchdogArmed) {
                uint32_t remainMs = netWatchdogDeadlineMs - millis();
                emitf("{\"type\":\"net\",\"pending\":true,\"confirm_deadline_s\":%u}\n",
                      (unsigned)(remainMs / 1000));
            } else if (netPendingLoaded) {
                emitLine("{\"type\":\"net\",\"pending\":true,\"reboot_required\":true}\n");
            } else {
                emitLine("{\"type\":\"net\",\"pending\":false}\n");
            }
        } else {
            emitLine("{\"type\":\"error\",\"msg\":\"unknown net command\"}\n");
        }
        return;
    }
    if (upper == "SHOW")    { sendShowConfig(); return; }
    if (upper == "SHOWNET") { sendShowNetConfig(); return; }
    if (upper == "SHOWZONES") { sendShowZones(); return; }
    if (upper == "HELP" || upper == "?") { sendHelp(); return; }
    if (upper == "PING")    { emitLine("{\"type\":\"pong\"}\n"); return; }
    if (upper == "STATUS")  { sendStatus(); return; }
    if (upper == "REBOOT")  {
        emitLine("{\"type\":\"ok\",\"cmd\":\"reboot\"}\n");
        delay(100);
        ESP.restart();
    }
    if (upper == "FACTORY") {
        prefs.begin(NVS_NS, false);
        prefs.clear();
        prefs.end();
        emitLine("{\"type\":\"ok\",\"cmd\":\"factory\",\"reboot_required\":true}\n");
        return;
    }
    if (upper == "RESET") {
        shotCounter = 0;
        sequenceNo  = 0;
        resetShotState();
        emitLine("{\"type\":\"ok\",\"cmd\":\"reset\"}\n");
        return;
    }
    if (upper == "TESTSHOOTPAPER") {
        // Loest NUR den Papiervorschub aus, so als waere gerade ein Schuss
        // registriert worden - zum Testen von Mechanik/Treiber ohne echten
        // Schuss auf die Scheibe. Bewusst UNABHAENGIG von PAPERAUTO/
        // PAPERTRIGGER (das sind Filter fuer ECHTE Schuesse) und OHNE
        // shotCounter/sequenceNo zu erhoehen oder ein "shot"-Telegramm zu
        // senden - der Stand-PC soll dadurch keinen echten Treffer sehen.
        if (paperMode != PAPER_IDLE) {
            emitLine("{\"type\":\"error\",\"msg\":\"paper feed busy\"}\n");
            return;
        }
        startPaperFeed();
        emitLine("{\"type\":\"ok\",\"cmd\":\"testshootpaper\"}\n");
        return;
    }
    if (upper == "TESTSHOOT" || upper.startsWith("TESTSHOOT ")) {
        // Absichtlich per Leerzeichen von TESTSHOOTPAPER (oben) abgegrenzt -
        // "TESTSHOOTPAPER" selbst matcht keinen der beiden Faelle hier.
        float zMm = 25.0f;   // Default: gesamte LG-Scheibe
        if (upper.length() > 9) {
            String zStr = upper.substring(9);
            zStr.trim();
            if (zStr.length() > 0) zMm = zStr.toFloat();
        }
        if (zMm < 1.0f || zMm > 500.0f) {
            emitLine("{\"type\":\"error\",\"msg\":\"testshoot z 1.0-500.0\"}\n");
            return;
        }
        handleTestShoot(zMm);
        return;
    }
    if (upper.startsWith("CAL")) {
        String sub = upper.substring(3);
        sub.trim();
        if (sub == "START" || sub.length() == 0) {
            calActive    = true;
            calCollected = 0;
            emitf("{\"type\":\"cal\",\"state\":\"start\",\"need\":%d}\n", cfg.calShotCount);
        } else if (sub == "ABORT") {
            calActive    = false;
            calCollected = 0;
            emitLine("{\"type\":\"cal\",\"state\":\"aborted\"}\n");
        } else if (sub == "STATUS") {
            emitf("{\"type\":\"cal\",\"state\":\"%s\",\"progress\":%d,\"need\":%d}\n",
                  calActive ? "waiting" : "idle", calCollected, cfg.calShotCount);
        } else if (sub == "RESET") {
            for (int i = 0; i < NUM_AIR; i++) {
                cfg.micOffsetNs[i] = 0;
                char key2[8];
                snprintf(key2, sizeof(key2), "ofs%d", i);
                saveVal<int32_t>(key2, 0);
            }
            cfg.soundSpeedMps = 342.5f;   // Werks-Default seit Rev 4.15.0
            saveVal<float>("sound_f", cfg.soundSpeedMps);
            applySoundSpeed();
            // Rev 4.17.0: Zonen-Saetze gehoeren zu den alten Offsets - verwerfen
            // (Grenzen und SET ZONES bleiben)
            for (int z = 0; z < ZONE_COUNT; z++) {
                memset(&cfg.zone[z], 0, sizeof(ZoneCal));
                saveZoneSet(z);
            }
            emitLine("{\"type\":\"ok\",\"cmd\":\"cal_reset\"}\n");
        } else if (sub.startsWith("IMPORT")) {
            // Atomarer Bulk-Restore einer vom Stand-PC gespeicherten
            // Kalibrierung (Rev 4.9.0, siehe docs/remote-interaktion-
            // konzept.md Abschnitt 7.2): "CAL IMPORT OFS0=<ns>,...,
            // OFS5=<ns>,SOUNDSPEED=<mps>" - alle Werte zuerst validieren,
            // ERST DANACH schreiben, damit ein Abbruch mitten in der
            // Uebertragung (z.B. TCP-Verbindungsverlust) keinen
            // inkonsistenten Zwischenzustand hinterlaesst (anders als bei
            // einer Folge einzelner SET OFS<i>-Befehle).
            String rest = sub.substring(6);
            rest.trim();
            int32_t newOfs[NUM_AIR];
            bool    setOfs[NUM_AIR];
            for (int i = 0; i < NUM_AIR; i++) { newOfs[i] = cfg.micOffsetNs[i]; setOfs[i] = false; }
            float    newSound = cfg.soundSpeedMps;
            bool     setSound = false;
            bool     ok = (rest.length() > 0);
            int      start = 0;
            while (ok && start <= (int)rest.length()) {
                int comma = rest.indexOf(',', start);
                String tok = (comma < 0) ? rest.substring(start) : rest.substring(start, comma);
                tok.trim();
                if (tok.length() > 0) {
                    int eq = tok.indexOf('=');
                    if (eq < 1) { ok = false; break; }
                    String k = tok.substring(0, eq); k.trim();
                    long   v = tok.substring(eq + 1).toInt();
                    if (k.startsWith("OFS") && k.length() == 4 && isDigit(k[3])) {
                        int idx = k[3] - '0';
                        if (idx >= NUM_AIR || v < -MIC_OFS_MAX_NS || v > MIC_OFS_MAX_NS) { ok = false; break; }
                        newOfs[idx] = (int32_t)v;
                        setOfs[idx] = true;
                    } else if (k == "SOUNDSPEED") {
                        // Seit Rev 4.15.0 auch mit Nachkommastelle
                        const float fv = tok.substring(eq + 1).toFloat();
                        if (fv < SOUND_SPEED_MIN_MPS || fv > SOUND_SPEED_MAX_MPS) { ok = false; break; }
                        newSound  = fv;
                        setSound  = true;
                    } else {
                        ok = false; break;
                    }
                }
                if (comma < 0) break;
                start = comma + 1;
            }
            if (!ok) {
                emitLine("{\"type\":\"error\",\"msg\":\"cal import: bad key=value "
                         "(OFS0..OFS5=-20000..20000, SOUNDSPEED=300..400)\"}\n");
                return;
            }
            for (int i = 0; i < NUM_AIR; i++) {
                if (!setOfs[i]) continue;
                cfg.micOffsetNs[i] = newOfs[i];
                char key2[8];
                snprintf(key2, sizeof(key2), "ofs%d", i);
                saveVal<int32_t>(key2, newOfs[i]);
            }
            if (setSound) {
                cfg.soundSpeedMps = newSound;
                saveVal<float>("sound_f", newSound);
                applySoundSpeed();
            }
            emitLine("{\"type\":\"ok\",\"cmd\":\"cal_import\"}\n");
        } else {
            emitLine("{\"type\":\"error\",\"msg\":\"unknown cal command\"}\n");
        }
        return;
    }
    // Kurzformen aus Rev 3.0/3.1 – jetzt ebenfalls persistent:
    if (upper.startsWith("DEBOUNCE=")) { handleSet("SET " + raw); return; }
    if (upper.startsWith("WINDOW="))   { handleSet("SET " + raw); return; }

    emitLine("{\"type\":\"error\",\"msg\":\"unknown command\"}\n");
}

// echo: Zeichen sofort auf s zurückschreiben (fuer Terminals ohne lokales
// Echo, z.B. MobaXterm seriell). Beim TCP-Kanal AUS, da die Gegenstelle
// dort reines JSON erwartet.
// AUTH_CMD_BUF_LEN: max. Laenge einer eingehenden Kommandozeile. War vorher
// fest 96 - reichte fuer die laengste dokumentierte Zeile (CAL IMPORT
// OFS0=...,...,SOUNDSPEED=... #<id>, siehe protokoll-referenz.md Abschnitt
// 5.3) schon vorher kaum, und braucht jetzt zusaetzlich Platz fuer das
// AUTH_TAG_HEX_LEN+1 Zeichen lange Signatur-Praefix auf dem TCP-Kanal.
#define AUTH_CMD_BUF_LEN  220

static void pollCommands(Stream &s, String &buf, bool echo, bool viaTcp)
{
    while (s.available() > 0) {
        char c = (char)s.read();
        if (echo) s.write(c);
        if (c == '\n' || c == '\r') {
            if (buf.length() > 0) {
                handleCommand(buf, viaTcp);
                pendingCorrId = -1;   // siehe Kommentar bei pendingCorrId/emitLine()
                buf = "";
            }
        } else if (buf.length() < AUTH_CMD_BUF_LEN) {
            buf += c;
        }
    }
}

// ---------------------------------------------------------------------------
// WLAN / TCP Verwaltung
// ---------------------------------------------------------------------------

// Prueft direkt am Socket, ob die Gegenstelle die Verbindung beendet hat.
// Noetig, weil NetworkClient::connected() (Core 3.x) ein sauberes Schliessen
// durch den Stand-PC (FIN, recv()==0) NICHT erkennt und weiter true liefert -
// ohne diese Pruefung wuerde nie neu verbunden, solange nichts gesendet wird.
// Nur aufrufen, wenn tcp.available()==0, damit noch gepufferte Befehle
// vorher abgearbeitet werden.
static bool tcpPeerGone()
{
    const int fd = tcp.fd();
    if (fd < 0) return true;
    uint8_t dummy;
    const int res = recv(fd, &dummy, 1, MSG_DONTWAIT | MSG_PEEK);
    if (res > 0)  return false;          // Daten liegen an
    if (res == 0) return true;           // FIN: Gegenstelle hat geschlossen
    switch (errno) {
        case EWOULDBLOCK:
#if EAGAIN != EWOULDBLOCK
        case EAGAIN:
#endif
        case EINTR:
        case ENOENT:                     // vfs, siehe NetworkClient::connected()
            return false;
        default:                         // ECONNRESET, ECONNABORTED (Keepalive), ...
            return true;
    }
}

static void maintainNetwork()
{
    if (!wifiEnabled) return;

    const uint32_t now = millis();

    if (WiFi.status() != WL_CONNECTED) {
        if (tcp.connected()) tcp.stop();
        if (!wifiDown) {
            wifiDown        = true;
            wifiDownSinceMs = now;
            Serial.println("# WLAN getrennt");
        } else if (now - wifiDownSinceMs >= WIFI_RETRY_MS) {
            // WiFi.disconnect()/begin() blockieren nicht (Verbindungsaufbau
            // laeuft im WLAN-Task) - daher auch waehrend des Papiervorschubs ok.
            Serial.printf("# WLAN seit %lus nicht verbunden (Status %d) - neuer Verbindungsversuch\n",
                          (unsigned long)((now - wifiDownSinceMs) / 1000UL),
                          (int)WiFi.status());
            WiFi.disconnect();
            WiFi.begin(cfg.ssid.c_str(), cfg.pass.c_str());
            wifiDownSinceMs = now;
        }
        return;
    }
    if (wifiDown) {
        wifiDown = false;
        Serial.printf("# WLAN verbunden, IP %s\n", WiFi.localIP().toString().c_str());
        // Nach (Wieder-)Verbindung sofort den Stand-PC versuchen
        nextConnectAttemptMs = now;
        connectBackoffMs     = 1000;
    }

    if (tcp.connected() && tcp.available() == 0 && tcpPeerGone()) {
        Serial.println("# TCP-Verbindung vom Stand-PC beendet - baue neu auf");
        tcp.stop();
        nextConnectAttemptMs = now;
        connectBackoffMs     = 1000;
    }

    if (!tcp.connected()) {
        if ((int32_t)(now - nextConnectAttemptMs) < 0) return;

        // tcp.connect() blockiert bis zu 2s (siehe Timeout unten) - waehrend
        // eines laufenden Papiervorschubs/Einfaedelns wuerde das den naechsten
        // STEP-Impuls (servicePaperStepper() laeuft nur zwischen den loop()-
        // Aufrufen) um bis zu 2s verzoegern und den Motor dabei sichtbar
        // anhalten lassen, obwohl der Kippschalter durchgehend gehalten wird.
        // Deshalb hier zurueckstellen, bis der Motor wieder steht - der
        // naechste loop()-Durchlauf danach versucht es sofort erneut.
        if (paperMode != PAPER_IDLE) return;

        Serial.printf("# TCP connect %s:%u ...\n", cfg.host.c_str(), cfg.port);
        if (tcp.connect(cfg.host.c_str(), cfg.port, 2000)) {
            tcp.setNoDelay(true);
            const int ka = 1, kaIdle = TCP_KA_IDLE_S, kaIntvl = TCP_KA_INTVL_S,
                      kaCnt = TCP_KA_CNT;
            tcp.setSocketOption(SOL_SOCKET,  SO_KEEPALIVE,  &ka,      sizeof(ka));
            tcp.setSocketOption(IPPROTO_TCP, TCP_KEEPIDLE,  &kaIdle,  sizeof(kaIdle));
            tcp.setSocketOption(IPPROTO_TCP, TCP_KEEPINTVL, &kaIntvl, sizeof(kaIntvl));
            tcp.setSocketOption(IPPROTO_TCP, TCP_KEEPCNT,   &kaCnt,   sizeof(kaCnt));
            connectBackoffMs = 1000;
            Serial.println("# TCP verbunden");
            sendStatus();
        } else {
            // millis() neu lesen: der fehlgeschlagene connect() kann bis zu
            // 2s blockiert haben
            nextConnectAttemptMs = millis() + connectBackoffMs;
            Serial.printf("# TCP connect fehlgeschlagen - naechster Versuch in %lus\n",
                          (unsigned long)(connectBackoffMs / 1000UL));
            if (connectBackoffMs < 8000) connectBackoffMs *= 2;
        }
    }
}

// Sicherheitsnetz fuer Netzwerk-Fernkonfiguration (Rev 4.9.0, siehe
// armNetWatchdogIfNeeded() und den Rev-4.9.0-Hinweis im Header-Kommentar):
// laeuft NUR nach einem Boot mit noch unbestaetigtem "net_pnd"-Flag
// (netWatchdogArmed, siehe setup()). Bestaetigt niemand die neue Konfig-
// uration per NET CONFIRM innerhalb von NET_CONFIRM_TIMEOUT_MS, werden die
// zuletzt bestaetigten Werte ("*_prv"-NVS-Keys) zurueckgeschrieben und das
// Geraet neu gestartet - rein zeitbasiert (kein tcp.connected()-Check
// noetig), deckt damit auch den Fall ab, dass WLAN mit den neuen Werten gar
// nicht erst verbindet (z.B. falsches SSID/PASS).
static void serviceNetWatchdog()
{
    if (!netWatchdogArmed) return;
    if ((int32_t)(millis() - netWatchdogDeadlineMs) < 0) return;

    Serial.println("# Netzwerk-Aenderung nicht bestaetigt (NET CONFIRM) - "
                    "stelle vorherige Konfiguration wieder her und starte neu");
    prefs.begin(NVS_NS, false);
    String pssid    = prefs.getString("ssid_prv", "");
    String ppass    = prefs.getString("pass_prv", "");
    String phost    = prefs.getString("host_prv", "192.168.1.10");
    uint16_t pport  = prefs.getUShort("port_prv", 9000);
    bool     pstatc = prefs.getBool("static_prv", false);
    String pip      = prefs.getString("ip_prv", "");
    String pgw      = prefs.getString("gw_prv", "");
    String psubnet  = prefs.getString("subnet_prv", "255.255.255.0");
    String pdns     = prefs.getString("dns_prv", "");
    prefs.putString("ssid", pssid);
    prefs.putString("pass", ppass);
    prefs.putString("host", phost);
    prefs.putUShort("port", pport);
    prefs.putBool("static_ip", pstatc);
    prefs.putString("ip", pip);
    prefs.putString("gateway", pgw);
    prefs.putString("subnet", psubnet);
    prefs.putString("dns", pdns);
    prefs.putBool("net_pnd", false);
    prefs.end();
    delay(100);
    ESP.restart();
}

// Standbeleuchtung: an setup() bereits eingeschaltet (siehe dort). Solange
// tcp.connected(), bleibt sie an. Faellt die TCP-Verbindung weg (oder steht
// sie seit dem Boot noch nie), durchlaeuft sie den LightPhase-Automaten:
// nach LIGHT_GRACE_MS aus, nach weiteren LIGHT_BLINK_DELAY_MS bei
// bestehendem WLAN ein kurzer Diagnose-Blink (LIGHT_BLINK_DURATION_MS),
// danach endgueltig aus - bis die TCP-Verbindung wieder steht und der
// gesamte Zyklus bei einem erneuten Abbruch von vorn beginnt.
static void serviceLight()
{
    // ACTION LIGHT=ON|OFF: uebernimmt den Pin komplett, die automatische
    // Verbindungsanzeige (inkl. LightPhase-Automat) pausiert dabei - siehe
    // handleActionCommand(). ACTION LIGHT=AUTO gibt die Kontrolle zurueck,
    // ab dann greift der Automat wieder ab dem naechsten tcp.connected()-
    // Wechsel wie gewohnt.
    if (lightOverride != LIGHT_AUTO) {
        digitalWrite(LIGHT_PIN, lightOverride == LIGHT_FORCE_ON ? HIGH : LOW);
        return;
    }

    const bool     connected = tcp.connected();
    const uint32_t now       = millis();

    if (connected) {
        digitalWrite(LIGHT_PIN, HIGH);
        lightWasConnected = true;
        lightPhase   = LP_WAIT_CONN;
        lightPhaseMs = now;
        return;
    }

    if (lightWasConnected) {
        // Gerade erst die Verbindung verloren - Zyklus (LED an, Gnadenfrist)
        // von vorn beginnen.
        lightWasConnected = false;
        lightPhase   = LP_WAIT_CONN;
        lightPhaseMs = now;
    }

    switch (lightPhase) {
    case LP_WAIT_CONN:
        if (now - lightPhaseMs >= LIGHT_GRACE_MS) {
            digitalWrite(LIGHT_PIN, LOW);
            lightPhase   = LP_OFF_WAIT;
            lightPhaseMs = now;
        }
        break;
    case LP_OFF_WAIT:
        if (now - lightPhaseMs >= LIGHT_BLINK_DELAY_MS) {
            if (WiFi.status() == WL_CONNECTED) {
                digitalWrite(LIGHT_PIN, HIGH);
                lightPhase = LP_BLINK;
            } else {
                lightPhase = LP_OFF_DONE;
            }
            lightPhaseMs = now;
        }
        break;
    case LP_BLINK:
        if (now - lightPhaseMs >= LIGHT_BLINK_DURATION_MS) {
            digitalWrite(LIGHT_PIN, LOW);
            lightPhase   = LP_OFF_DONE;
            lightPhaseMs = now;
        }
        break;
    case LP_OFF_DONE:
        break;
    }
}

// ---------------------------------------------------------------------------
// Setup / Loop
// ---------------------------------------------------------------------------

void setup()
{
    // Beleuchtung sofort an, noch vor allem anderen (siehe serviceLight()
    // fuer das Abschalten falls binnen LIGHT_GRACE_MS keine TCP-Session
    // zum Host zustande kommt).
    pinMode(LIGHT_PIN, OUTPUT);
    digitalWrite(LIGHT_PIN, HIGH);
    lightPhaseMs = millis();

    Serial.begin(SERIAL_BAUD);
    initDeviceMac();
    loadConfig();
    applyTargetGeometry();
    applyMicHalfX();
    applySoundSpeed();

    // Sicherheitsnetz Netzwerk-Fernkonfiguration (Rev 4.9.0): war beim
    // letzten Boot ein "net_pnd"-Flag gesetzt (loadConfig() hat es bereits
    // nach netPendingLoaded gelesen), wurde die soeben geladene Konfiguration
    // noch NICHT per NET CONFIRM bestaetigt - Watchdog scharf schalten.
    // netSnapshotArmedThisBoot wird ebenfalls vorbelegt, damit ein weiterer
    // Netzwerk-SET-Befehl vor der Bestaetigung die "*_prv"-Werte (= die
    // zuletzt BESTAETIGTE Konfiguration) nicht ueberschreibt.
    netSnapshotArmedThisBoot = netPendingLoaded;
    if (netPendingLoaded) {
        netWatchdogArmed      = true;
        netWatchdogDeadlineMs = millis() + NET_CONFIRM_TIMEOUT_MS;
    }

    // CPU-Takt fuer die Zyklen->ns-Umrechnung ermitteln
    cpuMHz = getCpuFrequencyMhz();
    if (cpuMHz < 80) cpuMHz = 240;   // Fallback

    // Luft-Mics: Hardware-Capture (SET CAPTURE=MCPWM, Rev 4.13.0) oder wie
    // bisher GPIO-ISR. Faellt MCPWM aus, automatisch ISR (capErr nennt den
    // Grund, STATUS zeigt "capture":"isr").
    if (cfg.airMcpwm) {
        if (airCaptureInit()) {
            Serial.printf("# Luft-Mics: MCPWM-Hardware-Capture (%lu CPU-Zyklen/Tick,"
                          " Gruppen-Sync %ld ns)\n",
                          (unsigned long)capRatio, (long)capSyncCheckNs);
        } else {
            Serial.printf("# MCPWM-Capture nicht verfuegbar (%s) - Fallback auf ISR\n",
                          capErr);
            airCaptureRelease();
        }
    }
    // Alle ISRs auf gleichem Core (setup laeuft auf einem Core), damit
    // alle Mikrofone denselben Zykluszaehler benutzen.
    for (uint32_t i = 0; i < NUM_AIR; i++) {
        // GPIO34-39 haben KEINEN internen Pull-Up - normaler INPUT-Modus,
        // das LM339 muss (wie bei PIEZO_PIN bereits der Fall) aktiv treiben.
        // Aktuell betrifft das keinen der AIR_PINS (nur den Sonderfall aus
        // dem inzwischen zurueckgebauten Diagnose-Test 3, siehe Header).
        // Bei aktivem MCPWM-Capture hat airCaptureInit() die Pins samt
        // Pull-Up bereits konfiguriert, eine zusaetzliche ISR entfaellt.
        if (capActive) continue;
        bool noPullup = AIR_PINS[i] >= 34 && AIR_PINS[i] <= 39;
        pinMode(AIR_PINS[i], noPullup ? INPUT : INPUT_PULLUP);
        attachInterruptArg(digitalPinToInterrupt(AIR_PINS[i]),
                           airISR, (void *)i, RISING);
    }
    // GPIO34 hat keinen internen Pull-Up (siehe PIEZO_PIN oben) - normaler
    // INPUT-Modus, das Piezo-Modul muss aktiv treiben. Wird unabhaengig von
    // SET PIEZO immer registriert (kostet nichts), nur processShot()
    // wertet die Flanken je nach cfg.usePiezo aus.
    pinMode(PIEZO_PIN, INPUT);
    attachInterrupt(digitalPinToInterrupt(PIEZO_PIN), piezoISR, RISING);
    resetShotState();

    // Papiervorschub (siehe servicePaperStepper() oben): Treiber zunaechst
    // deaktiviert (EN=HIGH), Einfaedel-Schalter mit internem Pull-Up (aktiv
    // = LOW gegen GND).
    pinMode(PAPER_STEP_PIN, OUTPUT);
    pinMode(PAPER_DIR_PIN, OUTPUT);
    pinMode(PAPER_EN_PIN, OUTPUT);
    digitalWrite(PAPER_STEP_PIN, LOW);
    paperEnable(false);
    pinMode(PAPER_SW_FWD_PIN, INPUT_PULLUP);
    pinMode(PAPER_SW_REV_PIN, INPUT_PULLUP);

    wifiEnabled = cfg.ssid.length() > 0;
    if (wifiEnabled) {
        WiFi.mode(WIFI_STA);
        WiFi.setAutoReconnect(true);
        WiFi.setSleep(false);

        if (cfg.staticIP && cfg.ip.length() > 0 && cfg.gateway.length() > 0) {
            IPAddress ipAddr, gwAddr, snAddr, dnsAddr;
            bool ok = ipAddr.fromString(cfg.ip)
                   && gwAddr.fromString(cfg.gateway)
                   && snAddr.fromString(cfg.subnet.length() > 0
                                        ? cfg.subnet : "255.255.255.0");
            if (ok) {
                if (cfg.dns.length() > 0) dnsAddr.fromString(cfg.dns);
                else                      dnsAddr = gwAddr;
                WiFi.config(ipAddr, gwAddr, snAddr, dnsAddr);
                Serial.printf("# Statische IP: %s  GW: %s  SM: %s  DNS: %s\n",
                              cfg.ip.c_str(), cfg.gateway.c_str(),
                              cfg.subnet.c_str(),
                              cfg.dns.length() > 0 ? cfg.dns.c_str()
                                                   : cfg.gateway.c_str());
            } else {
                Serial.println("# Warnung: Ung. statische IP-Konfig – DHCP wird verwendet");
            }
        } else if (cfg.staticIP) {
            Serial.println("# Warnung: STATIC=1 aber IP/GW fehlen – DHCP wird verwendet");
        }

        WiFi.begin(cfg.ssid.c_str(), cfg.pass.c_str());
        Serial.printf("# WLAN verbinde mit '%s' ...\n", cfg.ssid.c_str());
    } else {
        Serial.println("# WLAN aus (SSID leer) – SET SSID=... zum Aktivieren");
    }

    Serial.println();
    sendStatus();
    emitLine("{\"type\":\"ready\"}\n");
}

void loop()
{
    // Reiner Sensor-Testmodus (SET TESTMODE=1): laeuft komplett unabhaengig
    // von der Schuss-/TDOA-Logik (die ISRs setzen in diesem Modus weder
    // shotInProgress noch airCC/piezoCC, siehe airISR()/piezoISR() oben).
    if (testMode) {
        // Erst ALLE seit dem letzten loop()-Durchlauf ausgeloesten Sensoren
        // einsammeln (mit ISR-Zeitstempel, nicht Poll-Zeit) und das fruehste
        // Ereignis dieses Schwungs bestimmen, BEVOR irgendetwas ausgegeben
        // wird - sonst wuerde bei mehreren Sensoren im selben loop()-
        // Durchlauf (typisch bei einem echten Schuss) der Array-Index statt
        // der tatsaechlichen zeitlichen Reihenfolge ueber die 0ms-Referenz
        // entscheiden.
        bool     fired[NUM_AIR + 1];
        uint64_t fireUs[NUM_AIR + 1];
        bool     anyFired     = false;
        bool     haveBatchMin = false;
        uint64_t batchMinUs   = 0;
        for (int i = 0; i <= NUM_AIR; i++) {
            fired[i] = testFired[i];
            if (!fired[i]) continue;
            noInterrupts();
            testFired[i] = false;
            fireUs[i] = testLastFireUs[i];
            interrupts();
            anyFired = true;
            if (!haveBatchMin || fireUs[i] < batchMinUs) {
                batchMinUs   = fireUs[i];
                haveBatchMin = true;
            }
        }

        if (anyFired) {
            // Der erste Sensor nach einer Trennlinie startet die Serie bei
            // 0ms, alle weiteren zeigen die Verzoegerung dazu - damit laesst
            // sich bei einem echten Schuss direkt ablesen, welches
            // Zeitfenster (z.B. SET TDOA/PIEZOMAX) zuschlagen koennte.
            if (!testSeriesActive) {
                testSeriesStartUs = batchMinUs;
                testSeriesActive  = true;
            }
            for (int i = 0; i <= NUM_AIR; i++) {
                if (!fired[i]) continue;
                char buf[48];
                int64_t deltaUs = (int64_t)fireUs[i] - (int64_t)testSeriesStartUs;
                snprintf(buf, sizeof(buf), "%-20s +%.3f ms", TEST_SENSOR_NAMES[i],
                          (double)deltaUs / 1000.0);
                emitLine(buf);
            }
        }

        const uint64_t nowUs = (uint64_t)esp_timer_get_time();
        if (anyFired) {
            testLastActivityUs = nowUs;
            testSeparatorShown = false;
        } else if (!testSeparatorShown && nowUs - testLastActivityUs >= TEST_IDLE_US) {
            emitLine("----------");
            testSeparatorShown = true;
            testSeriesActive   = false;
        }
    }

    if (cfg.algoMode == ALGO_RIM) {
        // SET ALGO=RIM (siehe airISR()/piezoISR()/processShotAnchored()):
        // ausgewertet wird postWaitUs NACH dem Piezo-Anker, unabhaengig von
        // SET WINDOW (das gilt nur fuer ALGO=CLASSIC).
        if (piezoPending
            && (uint64_t)esp_timer_get_time() - piezoAnchorUs >= postWaitUs) {
            processShotAnchored();
        }
    } else if (shotInProgress) {
        const uint64_t now = (uint64_t)esp_timer_get_time();
        // Bei aktivem Piezo (SET PIEZO=1, Default) muss das Sammelfenster
        // mindestens bis SET PIEZOMAX + Sicherheitsmarge offen bleiben,
        // sonst wuerde das planmaessig erst 0,1-1,4ms nach dem ersten
        // Luft-Ereignis eintreffende Piezo-Signal verpasst (siehe
        // Rev-4.3-Hinweis oben). SET WINDOW bleibt die untere Grenze.
        uint64_t effWindowUs = (uint64_t)cfg.windowMs * 1000ULL;
        if (cfg.usePiezo) {
            const uint64_t piezoWaitUs = (uint64_t)cfg.piezoMaxUs + 200ULL;
            if (piezoWaitUs > effWindowUs) effWindowUs = piezoWaitUs;
        }
        if (now - firstHitTimeUs >= effWindowUs) {
            processShot();
        }
    }

    servicePaperStepper();
    serviceCaptureCheck();
    serviceDht();

    maintainNetwork();
    serviceNetWatchdog();
    serviceLight();

    static String serialBuf, tcpBuf;
    pollCommands(Serial, serialBuf, true, false);   // Serial: kein Auth (physischer Zugriff)
    if (tcp.connected()) {
        pollCommands(tcp, tcpBuf, false, true);     // TCP: Auth, falls PSK konfiguriert
    }
}
