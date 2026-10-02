// Scheibe + Treffer - 1:1 nach buildTarget()/renderShots()/computeViewR() in
// standpc/web/index.html (Masse in mm, y nach oben).

import 'dart:math';

import 'package:flutter/material.dart';

import 'models.dart';

const double kCaliber = 4.5; // Kaliberdurchmesser mm (4,5 mm Diabolo)
const double _shotStrokeW = kCaliber * 0.05;
const double _shotR = kCaliber / 2 - _shotStrokeW / 2;

class ZoomOption {
  final String key, label;
  final double? fraction; // Anteil am Aussenradius, null = Auto
  const ZoomOption(this.key, this.label, this.fraction);
}

const zoomOptions = [
  ZoomOption('auto', 'Auto (Standard)', null),
  ZoomOption('1', 'Komplett', 1.00),
  ZoomOption('2', 'Groß', 0.60),
  ZoomOption('3', 'Mittel', 0.4209),
  ZoomOption('4', 'Fein', 0.2418),
  ZoomOption('5', 'Maximal', 0.0626),
];

/// Sichtbarer Radius in mm (ohne Rand) - Auto: kleinste Stufe, in der der
/// Mittelpunkt jedes Schusses der letzten Serie noch liegt.
double computeViewR(TargetGeom? t, String zoomMode, List<Shot> shots, int perSeries) {
  final outerR = t?.outerR ?? 0;
  if (outerR <= 0) return 0;
  if (zoomMode != 'auto') {
    final z = zoomOptions.firstWhere((o) => o.key == zoomMode, orElse: () => zoomOptions[1]);
    return (z.fraction ?? 1) * outerR;
  }
  final series = shots.length > perSeries ? shots.sublist(shots.length - perSeries) : shots;
  if (series.isEmpty) return outerR;
  var maxR = 0.0;
  for (final s in series) {
    maxR = max(maxR, sqrt(s.xMm * s.xMm + s.yMm * s.yMm));
  }
  final fixed = zoomOptions.where((o) => o.fraction != null).toList()
    ..sort((a, b) => a.fraction!.compareTo(b.fraction!));
  for (final z in fixed) {
    final r = z.fraction! * outerR;
    if (r >= maxR) return r;
  }
  return outerR;
}

class TargetPainter extends CustomPainter {
  final TargetGeom? target;
  final List<Shot> shots;
  final int perSeries;
  final double viewR;
  final ShotDisplay display;
  final String targetColor;
  final bool hideShots;

  TargetPainter({
    required this.target,
    required this.shots,
    required this.perSeries,
    required this.viewR,
    required this.display,
    required this.targetColor,
    required this.hideShots,
  });

  @override
  void paint(Canvas canvas, Size size) {
    final t = target;
    if (t == null || t.rings.isEmpty || viewR <= 0) return;
    final pad = max(viewR * 0.07, kCaliber * 0.8);
    final half = viewR + pad;
    final scale = min(size.width, size.height) / (2 * half);

    canvas.save();
    canvas.clipRect(Offset.zero & size);
    canvas.translate(size.width / 2, size.height / 2);
    canvas.scale(scale);

    final rings = t.rings;
    final outerR = t.outerR;
    final strokeW = max(outerR * 0.004, 0.08);
    var minStep = double.infinity;
    for (var i = 0; i < rings.length - 1; i++) {
      minStep = min(minStep, (rings[i].d - rings[i + 1].d) / 2);
    }
    if (minStep == double.infinity) minStep = outerR;
    final fontSize = max(minStep * 0.58, outerR * 0.045);

    // 1. Scheibenpapier
    canvas.drawCircle(Offset.zero, outerR, Paint()..color = const Color(0xFFE8E4DC));

    // 2. Spiegel (Preisschiessen: Farbe je Scheibe)
    var darkR = 0.0;
    for (final r in rings) {
      if (r.filled) darkR = max(darkR, r.d / 2);
    }
    if (darkR > 0) {
      canvas.drawCircle(Offset.zero, darkR,
          Paint()..color = parseHexColor(targetColor, const Color(0xFF1C1C1C)));
    }

    // 3. Ringlinien
    for (final r in rings) {
      canvas.drawCircle(
          Offset.zero,
          r.d / 2,
          Paint()
            ..style = PaintingStyle.stroke
            ..strokeWidth = strokeW
            ..color = r.filled ? const Color(0xFF888888) : const Color(0xFF555555));
    }

    // 4. Innenzehner (gestrichelt bei LP/SP)
    if (t.inner10D > 0) {
      final p = Paint()
        ..style = PaintingStyle.stroke
        ..strokeWidth = strokeW
        ..color = const Color(0xFF888888);
      final r = t.inner10D / 2;
      if (t.inner10Dashed) {
        final dl = strokeW * 5;
        final n = max(8, (2 * pi * r / (2 * dl)).floor());
        final step = 2 * pi / n;
        for (var i = 0; i < n; i++) {
          canvas.drawArc(Rect.fromCircle(center: Offset.zero, radius: r), i * step, step / 2, false, p);
        }
      } else {
        canvas.drawCircle(Offset.zero, r, p);
      }
    }

    // 5. Ringbeschriftung 1-9 an 4 Positionen
    for (var i = 0; i < rings.length; i++) {
      final r = rings[i];
      if (r.v < 1 || r.v > 9) continue;
      final innerR = i + 1 < rings.length ? rings[i + 1].d / 2 : 0.0;
      final labelR = (r.d / 2 + innerR) / 2;
      final color = r.filled ? const Color(0xFFBBBBBB) : const Color(0xFF555555);
      for (final p in const [Offset(0, -1), Offset(1, 0), Offset(0, 1), Offset(-1, 0)]) {
        _text(canvas, '${r.v}', p * labelR, fontSize, color, FontWeight.w400);
      }
    }

    // 6. Schuesse der aktuellen Serie
    if (!hideShots && shots.isNotEmpty) {
      final serStart = ((shots.length - 1) ~/ perSeries) * perSeries;
      final series = shots.sublist(serStart);
      for (var i = 0; i < series.length; i++) {
        final s = series[i];
        final isLast = i == series.length - 1;
        final fill = s.overcount
            ? const Color(0xFFFFA500)
            : (isLast ? display.byRing(s.ring) : display.previous);
        final groupOpacity = (!s.overcount && !isLast) ? display.opacityPrevious : 1.0;
        final c = Offset(s.xMm, -s.yMm);
        canvas.drawCircle(c, _shotR, Paint()..color = fill.withValues(alpha: 0.82 * groupOpacity));
        canvas.drawCircle(
            c,
            _shotR,
            Paint()
              ..style = PaintingStyle.stroke
              ..strokeWidth = _shotStrokeW
              ..color = const Color(0xFF1A1A1A).withValues(alpha: groupOpacity));
        final lum = (0.299 * (fill.r * 255) + 0.587 * (fill.g * 255) + 0.114 * (fill.b * 255)) / 255;
        final txt = (lum > 0.5 ? Colors.black : Colors.white).withValues(alpha: groupOpacity);
        _text(canvas, '${serStart + i + 1}', c, kCaliber * 0.42, txt, FontWeight.bold);
      }
    }
    canvas.restore();
  }

  // Text in Scheibenkoordinaten (mm). Flutter rendert Schrift erst ab einer
  // gewissen Groesse sauber - daher in Pixelgroesse layouten und skaliert
  // zeichnen.
  void _text(Canvas canvas, String s, Offset at, double sizeMm, Color color, FontWeight w) {
    const k = 20.0; // Layout in 20-facher Groesse, dann herunterskalieren
    final tp = TextPainter(
      text: TextSpan(
          text: s,
          style: TextStyle(fontSize: sizeMm * k, color: color, fontWeight: w, height: 1.0)),
      textDirection: TextDirection.ltr,
    )..layout();
    canvas.save();
    canvas.translate(at.dx, at.dy);
    canvas.scale(1 / k);
    tp.paint(canvas, Offset(-tp.width / 2, -tp.height / 2));
    canvas.restore();
  }

  @override
  bool shouldRepaint(TargetPainter old) =>
      old.target != target ||
      old.shots != shots ||
      old.viewR != viewR ||
      old.display != display ||
      old.targetColor != targetColor ||
      old.hideShots != hideShots ||
      old.perSeries != perSeries;
}
