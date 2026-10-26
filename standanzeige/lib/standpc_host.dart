// Eingebauter Stand-PC (Standalone-Betrieb ohne Server): Steuerung des
// Android-Dienstes StandPcService ueber den MethodChannel "standpc" und
// Erzeugung der Stand-PC-Konfiguration (standpc/main.go Config).
//
// Der Stand-PC laeuft auf diesem Geraet, nimmt die TCP-Verbindung des ESP32
// per WLAN an und liefert der Anzeige dieselben Endpunkte wie ein externer
// Stand-PC - die App verbindet sich dann mit 127.0.0.1. Jeder Durchgang wird
// mit Disziplin und Zeitpunkt lokal gespeichert; der Schiessstand-Server kann
// die Durchgaenge spaeter ueber die Adresse dieses Geraets importieren
// (Standaktion -> Lokale Durchgaenge importieren).

import 'dart:convert';

import 'package:flutter/services.dart';

const embeddedHttpPort = 9001;
const embeddedUrl = 'http://127.0.0.1:$embeddedHttpPort';

class StandPcHost {
  static const _ch = MethodChannel('de.schiessstand.standanzeige/standpc');

  static String config({required int laneNo, required int espPort, required String psk}) {
    return const JsonEncoder.withIndent('  ').convert({
      'lane_no': laneNo,
      'standalone': true,
      'transport': 'tcp',
      'tcp_listen': ':$espPort',
      // auf allen Schnittstellen: der Server holt die Durchgaenge ueber das
      // WLAN ab und kann Disziplinen uebertragen
      'http_listen': ':$embeddedHttpPort',
      'esp32_psk_hex': psk.trim(),
      'shot_log_dir': 'shotlog',
      'disciplines_file': 'disciplines.json',
      'font_sizes_file': 'font_sizes.json',
      'admin_password_file': 'admin_password_hash.json',
      // Die Trefferposition rechnet der ESP32; die Sensorpositionen dienen
      // nur der Ersatzrechnung des Stand-PC.
      'sensors': [
        {'x_mm': 0, 'y_mm': 0},
        {'x_mm': 250, 'y_mm': 0},
        {'x_mm': 250, 'y_mm': 250},
        {'x_mm': 0, 'y_mm': 250},
      ],
      'sound_speed_mps': 3000,
      'plate_angle_deg': 30,
      'caliber_mm': 4.5,
      'edge_scoring': true,
    });
  }

  static Future<void> start({required int laneNo, required int espPort, required String psk}) =>
      _ch.invokeMethod('start', {'config': config(laneNo: laneNo, espPort: espPort, psk: psk)});

  static Future<void> stop() => _ch.invokeMethod('stop');

  static Future<Map<String, dynamic>> status() async {
    final r = await _ch.invokeMethod('status');
    return Map<String, dynamic>.from(r as Map);
  }

  /// IPv4-Adressen dieses Geraets, z.B. "192.168.2.50 (wlan0)".
  static Future<List<String>> addresses() async {
    final r = await _ch.invokeMethod('addresses');
    return List<String>.from(r as List);
  }
}
