// Screenshots der App fuer die Dokumentation (docs/bilder/app-*.png) - mit
// echten Schriften statt der Testschrift. Laeuft nur mit DOKU=1:
//   DOKU=1 flutter test test/doku_screenshots_test.dart --update-goldens
import 'dart:io';
import 'dart:math';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:standanzeige/api.dart';
import 'package:standanzeige/display_screen.dart';
import 'package:standanzeige/display_state.dart';
import 'package:standanzeige/models.dart';
import 'package:standanzeige/settings.dart';
import 'package:standanzeige/theme.dart';

final _skip = Platform.environment['DOKU'] == null;

Future<void> _loadFont(String family, List<String> files) async {
  final loader = FontLoader(family);
  for (final f in files) {
    loader.addFont(Future.value(ByteData.sublistView(File(f).readAsBytesSync())));
  }
  await loader.load();
}

Future<void> _fonts() async {
  final home = Platform.environment['HOME'];
  final mf = '$home/development/flutter/bin/cache/artifacts/material_fonts';
  await _loadFont('Roboto', ['$mf/Roboto-Regular.ttf', '$mf/Roboto-Medium.ttf', '$mf/Roboto-Bold.ttf']);
  await _loadFont('Emoji', ['/usr/share/fonts/truetype/noto/NotoColorEmoji.ttf']);
  await _loadFont('DejaVu', ['/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf']);
}

DisplayState _state() {
  final st = DisplayState(StandPcApi('http://127.0.0.1:1'));
  st.targets = {
    '1': TargetGeom.fromJson({
      'name': 'LG',
      'rings': [
        for (var v = 10; v >= 1; v--) {'v': v, 'd': 0.5 + (10 - v) * 5.0, 'filled': v >= 4}
      ]
    })
  };
  st.status = LaneStatus.fromJson({
    'lane_no': 4, 'mode': 'wertung', 'shooter': 'Huber, Anna', 'wertung_count': 23, 'history_gen': 1,
    'discipline': {'name': 'LGA-30', 'target_no': 1, 'trial_shots': 100, 'scoring_shots': 30,
      'shots_per_series': 10, 'decimal_scoring': true}
  });
  st.conn = ConnState.live;
  final rnd = Random(3);
  st.shots = [
    for (var i = 0; i < 23; i++)
      () {
        final x = (rnd.nextDouble() - 0.5) * 17, y = (rnd.nextDouble() - 0.5) * 17;
        final d = sqrt(x * x + y * y);
        final dec = max(0.0, ((10.9 - max(0, d - 2.25) / 2.5) * 10).floor() / 10);
        return Shot.fromJson({'x_mm': x, 'y_mm': y, 'decimal': dec, 'ring': dec.floor(),
          'center_distance': d * 100, 'mode': 'wertung'});
      }()
  ];
  return st;
}

Future<void> _pump(WidgetTester tester, Size size, Widget home) async {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1.0;
  final base = appTheme();
  final theme = base.copyWith(
    textTheme: base.textTheme.apply(fontFamily: 'Roboto', fontFamilyFallback: ['DejaVu', 'Emoji']),
  );
  await tester.pumpWidget(MaterialApp(debugShowCheckedModeBanner: false, theme: theme,
      home: DefaultTextStyle(style: const TextStyle(fontFamily: 'Roboto', fontFamilyFallback: ['DejaVu', 'Emoji']), child: home)));
  await tester.pump();
}

void main() {
  setUpAll(_fonts);

  testWidgets('Tablet quer', (tester) async {
    await _pump(tester, const Size(1280, 800),
        DisplayScreen(state: _state(), settings: AppSettings(url: 'x', scale: 1.0), onOpenSettings: () {}));
    await expectLater(find.byType(DisplayScreen), matchesGoldenFile('../../docs/bilder/app-tablet.png'));
    addTearDown(tester.view.reset);
  }, skip: _skip);

  testWidgets('Handy hoch', (tester) async {
    await _pump(tester, const Size(412, 892),
        DisplayScreen(state: _state(), settings: AppSettings(url: 'x', scale: 0.9), onOpenSettings: () {}));
    await expectLater(find.byType(DisplayScreen), matchesGoldenFile('../../docs/bilder/app-handy.png'));
    addTearDown(tester.view.reset);
  }, skip: _skip);

  testWidgets('Einstellungen', (tester) async {
    await _pump(tester, const Size(800, 640),
        SettingsScreen(settings: AppSettings(url: 'http://192.168.2.174:9004', scale: 1.2), onSaved: (_) {}));
    await expectLater(find.byType(SettingsScreen), matchesGoldenFile('../../docs/bilder/app-einstellungen.png'));
    addTearDown(tester.view.reset);
  }, skip: _skip);
}
