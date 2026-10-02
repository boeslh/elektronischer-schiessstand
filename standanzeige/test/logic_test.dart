import 'dart:async';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:standanzeige/api.dart';
import 'package:standanzeige/models.dart';
import 'package:standanzeige/target_painter.dart';

void main() {
  test('Modelle lesen das JSON des Stand-PC', () {
    final st = LaneStatus.fromJson({
      'lane_no': 4,
      'mode': 'wertung',
      'discipline': {
        'name': 'LGA-30', 'target_no': 1, 'trial_shots': 100, 'scoring_shots': 30,
        'shots_per_series': 10, 'decimal_scoring': true, 'anzeige': 'teilverdeckt'
      },
      'shooter': 'Zimmer, Christine',
      'wertung_count': 3,
      'history_gen': 7,
    });
    expect(st.laneNo, 4);
    expect(st.discipline!.decimalScoring, isTrue);
    expect(st.discipline!.masked, isTrue);
    expect(st.discipline!.fullyHidden, isFalse);

    final t = TargetGeom.fromJson({
      'name': 'LG',
      'rings': [
        {'v': 10, 'd': 0.5, 'filled': true},
        {'v': 1, 'd': 45.5, 'filled': false},
      ]
    });
    expect(t.rings.first.v, 1); // aussen zuerst
    expect(t.outerR, closeTo(22.75, 1e-9));
  });

  test('Auto-Zoom waehlt die kleinste passende Stufe', () {
    final t = TargetGeom.fromJson({
      'rings': [{'v': 1, 'd': 45.5, 'filled': false}]
    });
    Shot s(double x, double y) => Shot.fromJson({'x_mm': x, 'y_mm': y, 'ring': 9});
    expect(computeViewR(t, 'auto', [], 10), closeTo(22.75, 1e-9));
    // 5,5 mm Abstand -> Stufe "Fein" (0,2418 * 22,75 = 5,50)
    expect(computeViewR(t, 'auto', [s(5.5, 0)], 10), closeTo(0.2418 * 22.75, 1e-9));
    expect(computeViewR(t, 'auto', [s(1, 0)], 10), closeTo(0.0626 * 22.75, 1e-9));
    expect(computeViewR(t, '2', [s(1, 0)], 10), closeTo(0.6 * 22.75, 1e-9));
  });

  test('Live-Stream: Events, Keepalive und Neuverbindung', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    var connects = 0;
    server.listen((req) async {
      connects++;
      req.response.headers.contentType = ContentType('text', 'event-stream');
      req.response.bufferOutput = false;
      req.response.write('event: status\ndata: {"lane_no":4}\n\n');
      req.response.write(': keepalive\n\n');
      req.response.write('event: shot\ndata: {"ring":9}\n\n');
      await req.response.flush();
      await req.response.close(); // Abbruch -> Client muss neu verbinden
    });
    final api = StandPcApi('http://127.0.0.1:${server.port}');
    final events = <String>[];
    var opens = 0;
    final done = Completer<void>();
    final sse = SseConnection(
      api: api,
      onEvent: (e) => events.add('${e.event}:${e.data}'),
      onOpen: () {
        opens++;
        if (opens == 2 && !done.isCompleted) done.complete();
      },
      onDisconnect: (_) {},
    );
    sse.start();
    await done.future.timeout(const Duration(seconds: 10));
    sse.stop();
    await server.close(force: true);
    expect(events.take(2), ['status:{"lane_no":4}', 'shot:{"ring":9}']);
    expect(connects, greaterThanOrEqualTo(2));
  });
}
