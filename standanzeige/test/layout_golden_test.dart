import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:standanzeige/api.dart';
import 'package:standanzeige/display_screen.dart';
import 'package:standanzeige/display_state.dart';
import 'package:standanzeige/models.dart';
import 'package:standanzeige/settings.dart';
import 'package:standanzeige/theme.dart';

const targetsJson =
    '{"1":{"name":"LG","inner10_d":0,"inner10_dashed":false,"rings":[{"v":10,"d":0.5,"filled":true},{"v":9,"d":5.5,"filled":true},{"v":8,"d":10.5,"filled":true},{"v":7,"d":15.5,"filled":true},{"v":6,"d":20.5,"filled":true},{"v":5,"d":25.5,"filled":true},{"v":4,"d":30.5,"filled":true},{"v":3,"d":35.5,"filled":false},{"v":2,"d":40.5,"filled":false},{"v":1,"d":45.5,"filled":false}]}}';

DisplayState fakeState() {
  final st = DisplayState(StandPcApi('http://127.0.0.1:1'));
  st.targets = (jsonDecode(targetsJson) as Map<String, dynamic>)
      .map((k, v) => MapEntry(k, TargetGeom.fromJson(v)));
  st.status = LaneStatus.fromJson({
    'lane_no': 4, 'mode': 'wertung', 'shooter': 'Zimmer, Christine', 'wertung_count': 13, 'history_gen': 1,
    'discipline': {'name': 'LGA-30', 'target_no': 1, 'trial_shots': 100, 'scoring_shots': 30,
      'shots_per_series': 10, 'decimal_scoring': true}
  });
  st.conn = ConnState.live;
  final pts = [[1.9,11.7,6.2,6],[9.5,-7.9,6.0,6],[4.0,-2.7,9.0,9],[1.3,-5.7,8.6,8],[6.0,-10.6,6.1,6],
    [-3.7,3.2,9.0,9],[0.0,-4.4,9.2,9],[-2.9,4.8,8.7,8],[0.4,0.0,10.8,10],[11.8,3.7,6.0,6],
    [1.0,-8.3,7.6,7],[0.3,-1.6,10.3,10],[-1.2,0.8,10.4,10]];
  st.shots = [for (final p in pts) Shot.fromJson({'x_mm': p[0], 'y_mm': p[1], 'decimal': p[2], 'ring': p[3], 'center_distance': 123.4, 'mode': 'wertung'})];
  return st;
}

Future<void> pumpAt(WidgetTester tester, Size size) async {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1.0;
  final st = fakeState();
  await tester.pumpWidget(MaterialApp(
    theme: appTheme(),
    home: DisplayScreen(state: st, settings: AppSettings(url: 'x', scale: 1.0), onOpenSettings: () {}),
  ));
  await tester.pump();
}

void main() {
  testWidgets('Tablet quer', (tester) async {
    await pumpAt(tester, const Size(1280, 800));
    await expectLater(find.byType(DisplayScreen), matchesGoldenFile('goldens/tablet_quer.png'));
    addTearDown(tester.view.reset);
  });
  testWidgets('Handy hoch', (tester) async {
    await pumpAt(tester, const Size(412, 900));
    await expectLater(find.byType(DisplayScreen), matchesGoldenFile('goldens/handy_hoch.png'));
    addTearDown(tester.view.reset);
  });
}
