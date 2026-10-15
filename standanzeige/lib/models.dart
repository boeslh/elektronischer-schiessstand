// Datenmodelle - Spiegel der JSON-Antworten des Stand-PC (standpc/web.go,
// session.go, main.go). Feldnamen siehe dort.

import 'dart:ui' show Color;

double _d(dynamic v) => v is num ? v.toDouble() : 0.0;
int _i(dynamic v) => v is num ? v.toInt() : 0;
String _s(dynamic v) => v is String ? v : '';

class Discipline {
  final String name;
  final int targetNo;
  final int trialShots;
  final int scoringShots;
  final int shotsPerSeries;
  final bool decimalScoring;

  /// "" / "voll" | "teilverdeckt" | "verdeckt" (siehe standpc maskShot)
  final String anzeige;

  Discipline.fromJson(Map<String, dynamic> j)
      : name = _s(j['name']),
        targetNo = _i(j['target_no']),
        trialShots = _i(j['trial_shots']),
        scoringShots = _i(j['scoring_shots']),
        shotsPerSeries = _i(j['shots_per_series']) > 0 ? _i(j['shots_per_series']) : 10,
        decimalScoring = j['decimal_scoring'] == true,
        anzeige = _s(j['anzeige']);

  bool get masked => anzeige.isNotEmpty && anzeige != 'voll';
  bool get fullyHidden => anzeige == 'verdeckt';
}

/// Schiesszeit (standpc/timing.go TimerInfo) - Zeitpunkte in Stand-PC-Zeit.
class TimerInfo {
  final String mode; // uhrzeit | soft | hard
  final String switchMode; // individuell | zentral | automatisch
  final bool separate;
  final bool started;
  final int probeDeadlineMs;
  final int deadlineMs;
  final int probeS;
  final int totalS;

  TimerInfo.fromJson(Map<String, dynamic> j)
      : mode = _s(j['mode']).isEmpty ? 'uhrzeit' : _s(j['mode']),
        switchMode = _s(j['switch']).isEmpty ? 'individuell' : _s(j['switch']),
        separate = j['separate'] == true,
        started = j['started'] == true,
        probeDeadlineMs = _i(j['probe_deadline_ms']),
        deadlineMs = _i(j['deadline_ms']),
        probeS = _i(j['probe_s']),
        totalS = _i(j['total_s']);

  const TimerInfo.none()
      : mode = 'uhrzeit',
        switchMode = 'individuell',
        separate = false,
        started = false,
        probeDeadlineMs = 0,
        deadlineMs = 0,
        probeS = 0,
        totalS = 0;
}

class LaneStatus {
  final int laneNo;
  final String mode; // probe | wertung
  final Discipline? discipline;
  final String shooter;
  final String eventName;
  final String eventType;
  final int wertungCount;
  final String notification;
  final int historyGen;
  // Stechschuss (Server rwk_stechen.go): Durchgang (0 = keiner) und das
  // Gesamtergebnis der Wertung
  final int stechenNo;
  final String baseTotal;
  final TimerInfo timer;
  /// Bedienung gesperrt (Standsteuerung, standpc/uilock.go)
  final bool uiLocked;
  /// Stand-PC-Uhr beim Senden minus Geraeteuhr (Abgleich der Countdown-Zeit)
  final int clockSkewMs;

  LaneStatus.fromJson(Map<String, dynamic> j)
      : laneNo = _i(j['lane_no']),
        mode = j['mode'] == 'wertung' ? 'wertung' : 'probe',
        discipline = j['discipline'] is Map<String, dynamic>
            ? Discipline.fromJson(j['discipline'])
            : null,
        shooter = _s(j['shooter']),
        eventName = _s(j['event_name']),
        eventType = _s(j['event_type']),
        wertungCount = _i(j['wertung_count']),
        notification = _s(j['notification']),
        historyGen = _i(j['history_gen']),
        stechenNo = _i(j['stechen_no']),
        baseTotal = _s(j['base_total']),
        uiLocked = j['ui_locked'] == true,
        timer = j['timer'] is Map<String, dynamic> ? TimerInfo.fromJson(j['timer']) : const TimerInfo.none(),
        clockSkewMs = _i(j['now_ms']) > 0 ? _i(j['now_ms']) - DateTime.now().millisecondsSinceEpoch : 0;
}

class Shot {
  final double xMm, yMm;
  final int ring;
  final double decimal;
  final bool innerTen;
  final double centerDistance;
  final String mode;
  final bool rejected;
  final bool overcount;

  Shot.fromJson(Map<String, dynamic> j)
      : xMm = _d(j['x_mm']),
        yMm = _d(j['y_mm']),
        ring = _i(j['ring']),
        decimal = _d(j['decimal']),
        innerTen = j['inner_ten'] == true,
        centerDistance = _d(j['center_distance']),
        mode = _s(j['mode']),
        rejected = j['rejected'] == true,
        overcount = j['overcount'] == true;
}

class RingGeom {
  final int v;
  final double d;
  final bool filled;
  RingGeom.fromJson(Map<String, dynamic> j)
      : v = _i(j['v']),
        d = _d(j['d']),
        filled = j['filled'] == true;
}

class TargetGeom {
  final String name;
  final double inner10D;
  final bool inner10Dashed;
  final List<RingGeom> rings; // absteigend nach Durchmesser (aussen -> innen)

  TargetGeom.fromJson(Map<String, dynamic> j)
      : name = _s(j['name']),
        inner10D = _d(j['inner10_d']),
        inner10Dashed = j['inner10_dashed'] == true,
        rings = ((j['rings'] as List?) ?? [])
            .whereType<Map<String, dynamic>>()
            .map(RingGeom.fromJson)
            .toList()
          ..sort((a, b) => b.d.compareTo(a.d));

  double get outerR => rings.isEmpty ? 0 : rings.first.d / 2;
}

Color parseHexColor(String hex, Color fallback) {
  var h = hex.trim();
  if (h.startsWith('#')) h = h.substring(1);
  if (h.length != 6) return fallback;
  final v = int.tryParse(h, radix: 16);
  return v == null ? fallback : Color(0xFF000000 | v);
}

class ShotDisplay {
  final Color ring10, ring9, other, previous;
  final double opacityPrevious;

  const ShotDisplay({
    this.ring10 = const Color(0xFFFF0000),
    this.ring9 = const Color(0xFFF3FB06),
    this.other = const Color(0xFF0209F9),
    this.previous = const Color(0xFFAAAAAA),
    this.opacityPrevious = 0.85,
  });

  factory ShotDisplay.fromJson(Map<String, dynamic> j) {
    const d = ShotDisplay();
    return ShotDisplay(
      ring10: parseHexColor(_s(j['color_ring10']), d.ring10),
      ring9: parseHexColor(_s(j['color_ring9']), d.ring9),
      other: parseHexColor(_s(j['color_other']), d.other),
      previous: parseHexColor(_s(j['color_previous']), d.previous),
      opacityPrevious: j['opacity_previous'] is num ? _d(j['opacity_previous']) : d.opacityPrevious,
    );
  }

  Color byRing(int ring) => ring >= 10 ? ring10 : (ring == 9 ? ring9 : other);
}

/// Schriftgroessen (Einstellungen am Server -> Push an den Stand-PC), in px
/// wie im Browser.
class FontSizes {
  final double name, scheibe, status, menu, menuPs, ergebnisse;
  const FontSizes({
    this.name = 16,
    this.scheibe = 13,
    this.status = 11,
    this.menu = 14,
    this.menuPs = 14,
    this.ergebnisse = 13,
  });
  factory FontSizes.fromJson(Map<String, dynamic> j) {
    const d = FontSizes();
    double f(String k, double def) => j[k] is num && _d(j[k]) > 0 ? _d(j[k]) : def;
    return FontSizes(
      name: f('name', d.name),
      scheibe: f('scheibe', d.scheibe),
      status: f('status', d.status),
      menu: f('menu', d.menu),
      menuPs: f('menu_ps', d.menuPs),
      ergebnisse: f('ergebnisse', d.ergebnisse),
    );
  }
}

class PSScheibeTyp {
  final String scheibeId, name;
  final int gekauft, beendet;
  PSScheibeTyp.fromJson(Map<String, dynamic> j)
      : scheibeId = _s(j['scheibe_id']),
        name = _s(j['name']),
        gekauft = _i(j['gekauft']),
        beendet = _i(j['beendet']);
}

class PSAngebotItem {
  final String id, name;
  final double price;
  PSAngebotItem.fromJson(Map<String, dynamic> j)
      : id = _s(j['id']),
        name = _s(j['name']),
        price = _d(j['price']);
}

/// Preisschiessen-Zustand des Stands (null = kein Preisschiessen).
class PSInfo {
  final String preisschiessenName, teilnehmerId, shooterName;
  final int teilnehmerNr;
  final double guthaben;
  final bool pending, needsTeilnehmer;
  final String currentScheibeName, currentTargetColor;
  final List<PSScheibeTyp> scheibenTypen;
  final List<PSAngebotItem> angebotScheiben, angebotSets;

  PSInfo.fromJson(Map<String, dynamic> j)
      : preisschiessenName = _s(j['preisschiessen_name']),
        teilnehmerId = _s(j['teilnehmer_id']),
        teilnehmerNr = _i(j['teilnehmer_nr']),
        shooterName = _s(j['shooter_name']),
        guthaben = _d(j['guthaben']),
        pending = j['pending'] == true,
        needsTeilnehmer = j['needs_teilnehmer'] == true,
        currentScheibeName = _s(j['current_scheibe_name']),
        currentTargetColor = _s(j['current_target_color']),
        scheibenTypen = _list(j['scheiben_typen'], PSScheibeTyp.fromJson),
        angebotScheiben = _list((j['angebot'] as Map?)?['scheiben'], PSAngebotItem.fromJson),
        angebotSets = _list((j['angebot'] as Map?)?['sets'], PSAngebotItem.fromJson);

  static List<T> _list<T>(dynamic v, T Function(Map<String, dynamic>) f) =>
      ((v as List?) ?? []).whereType<Map<String, dynamic>>().map(f).toList();
}

String formatEuro(double v) => '${v.toStringAsFixed(2).replaceAll('.', ',')} €';
