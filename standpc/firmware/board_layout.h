// ============================================================================
//  Platinenlayout - Pinbelegung des ESP32 (die EINZIGE Stelle dafuer)
// ============================================================================
//
//  Auswahl des Layouts: BOARD_LAYOUT unten setzen ODER per Build-Flag in
//  platformio.ini ueberschreiben, z.B.
//      build_flags = -DBOARD_LAYOUT=BOARD_LAYOUT_0_9
//
//  Neues Layout anlegen: einen weiteren #elif-Block mit allen BOARD_*-Werten
//  kopieren, eigene Konstante BOARD_LAYOUT_x_y vergeben, BOARD_NAME setzen.
//
//  Zuordnung der Luft-Mikrofone (Index = AIR0..AIR5, fest mit der Geometrie
//  MIC_X/MIC_Y und den Kalibrier-Offsets SET OFS0..OFS5 verknuepft - beim
//  Layoutwechsel daher NUR die Pins aendern, nie die Reihenfolge):
//      AIR0 links-unten   AIR1 rechts-unten   AIR2 links-oben
//      AIR3 rechts-oben   AIR4 links-mitte    AIR5 rechts-mitte
//
//  Hinweise zur Pinwahl (ESP32-WROOM):
//  - Mikrofone moeglichst alle auf GPIO 0-31: GPIO 32-39 liegen in der
//    zweiten Interrupt-Bank und bekommen im ISR-Betrieb positionsabhaengig
//    spaetere Zeitstempel (siehe Rev 4.11.3 in der Firmware).
//  - GPIO 34-39: reine Eingaenge OHNE internen Pull-up - der Komparator muss
//    aktiv treiben (Piezo/Frontplatte). Die Firmware schaltet den Pull-up
//    fuer diese Pins automatisch ab.
//  - Strapping-Pins 0, 2, 5, 12, 15 meiden (GPIO12 HIGH beim Boot = falsche
//    Flash-Spannung, ESP32 startet nicht). GPIO 6-11 = Flash, tabu.
//  - MCPWM_SYNC_PIN wird intern als Ausgang genutzt und MUSS unbeschaltet
//    bleiben.
// ============================================================================
#pragma once

#define BOARD_LAYOUT_0_9   90    // bisheriges Layout
#define BOARD_LAYOUT_1_0  100    // Layout 1.0 / 1.0.1 (mit Frontplatte)

#ifndef BOARD_LAYOUT
#define BOARD_LAYOUT  BOARD_LAYOUT_1_0
#endif

#if BOARD_LAYOUT == BOARD_LAYOUT_0_9
  #define BOARD_NAME          "0.9"
  //                          AIR0 AIR1 AIR2 AIR3 AIR4 AIR5
  #define BOARD_AIR_PINS      { 25,  26,  27,  14,   4,  13 }
  #define PIEZO_PIN           34    // Stahlplatte (Koerperschall)
  #define FRONT_PIN           -1    // Frontplatte: nicht vorhanden
  #define MCPWM_SYNC_PIN      23    // unbeschaltet lassen!
  #define PAPER_STEP_PIN      21
  #define PAPER_DIR_PIN       18
  #define PAPER_EN_PIN        19
  #define PAPER_SW_FWD_PIN    16
  #define PAPER_SW_REV_PIN    17
  #define LIGHT_PIN           22
  #define DHT_PIN             15

#elif BOARD_LAYOUT == BOARD_LAYOUT_1_0
  #define BOARD_NAME          "1.0"
  //                          AIR0 AIR1 AIR2 AIR3 AIR4 AIR5
  #define BOARD_AIR_PINS      { 14,  25,  26,   4,  27,  13 }
  #define PIEZO_PIN           34    // Stahlplatte (Koerperschall)
  #define FRONT_PIN           35    // Frontplatte (Schuss neben die Scheibe)
  // Folgende Pins unveraendert aus Layout 0.9 uebernommen - beim neuen
  // Layout bitte gegenpruefen:
  #define MCPWM_SYNC_PIN      23    // unbeschaltet lassen!
  #define PAPER_STEP_PIN      21
  #define PAPER_DIR_PIN       18
  #define PAPER_EN_PIN        19
  #define PAPER_SW_FWD_PIN    16
  #define PAPER_SW_REV_PIN    17
  #define LIGHT_PIN           22
  #define DHT_PIN             15

#else
  #error "Unbekanntes BOARD_LAYOUT - siehe include/board_layout.h"
#endif

// ============================================================================
//  Mikrofon-Anordnung (Rev 4.20.0) - Soll-Positionen der Mikrofone
// ============================================================================
//  Unabhaengig vom Platinenlayout waehlbar: MIC_LAYOUT unten setzen oder per
//  -DMIC_LAYOUT=... in platformio.ini. Koordinaten in mm, Ursprung =
//  Scheibenmittelpunkt, x nach rechts, y nach oben (Schuetzensicht), z =
//  Abstand vor der Papierscheibe. Reihenfolge AIR0..AIR5 (siehe oben).
//
//  Die Kalibrierung (SET DIST<i> bzw. CAL START) verschiebt jedes Mikrofon
//  entlang seiner Verbindungslinie zum Scheibenmittelpunkt; die Richtung
//  (Winkel) bleibt fest.
#define MIC_LAYOUT_FLAT     1    // bis Rev 4.19: flach in der Seitenwand
#define MIC_LAYOUT_ANGLED   2    // auf den Scheibenmittelpunkt ausgerichtet

#ifndef MIC_LAYOUT
#define MIC_LAYOUT  MIC_LAYOUT_ANGLED
#endif

#if MIC_LAYOUT == MIC_LAYOUT_FLAT
  #define MIC_LAYOUT_NAME     "flat"
  // Werks-Defaults der frueheren SET MICHALFX=115 / STANDOFFPAPER=28
  //                          AIR0    AIR1    AIR2    AIR3    AIR4    AIR5
  #define MIC_NOM_X_MM      { -115.0f, 115.0f, -115.0f, 115.0f, -115.0f, 115.0f }
  #define MIC_NOM_Y_MM      {  -85.0f, -85.0f,   85.0f,  85.0f,    0.0f,   0.0f }
  #define MIC_NOM_Z_MM      {   28.0f,  28.0f,   28.0f,  28.0f,   28.0f,  28.0f }

#elif MIC_LAYOUT == MIC_LAYOUT_ANGLED
  #define MIC_LAYOUT_NAME     "angled"
  // Entfernung zum Scheibenmittelpunkt: oben/unten 134,47 mm, mitte 115,73 mm
  //                          AIR0    AIR1    AIR2    AIR3    AIR4    AIR5
  #define MIC_NOM_X_MM      { -109.0f, 109.0f, -109.0f, 109.0f, -113.0f, 113.0f }
  #define MIC_NOM_Y_MM      {  -75.0f, -75.0f,   75.0f,  75.0f,    0.0f,   0.0f }
  #define MIC_NOM_Z_MM      {   24.0f,  24.0f,   24.0f,  24.0f,   25.0f,  25.0f }

#else
  #error "Unbekanntes MIC_LAYOUT - siehe include/board_layout.h"
#endif

// Frontplatten-Treffer (nur mit FRONT_PIN >= 0): wird als gueltiger Schuss
// an dieser festen Position gemeldet ("shot" mit "front":1), Scheiben-
// koordinaten in 0.001 mm (x nach rechts, y nach oben, 0/0 = Scheibenmitte).
#define FRONT_HIT_X_UM   110000L   // 110 mm rechts
#define FRONT_HIT_Y_UM  -110000L   // 110 mm unten
