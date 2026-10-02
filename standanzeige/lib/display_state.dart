// Zustand der Standanzeige - entspricht der Logik in standpc/web/index.html
// (applyStatus, addShot, renderPSMenu ...), aber mit vollstaendigem
// Neuabgleich statt Seiten-Reload: Bei jedem (Re-)Connect, bei jedem
// history_gen-Wechsel und zusaetzlich alle 30 s wird der komplette Zustand
// (Status, Schuesse, Preisschiessen) frisch vom Stand-PC geholt, damit die
// Anzeige nie unbemerkt auf altem Stand haengen bleibt.

import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';

import 'api.dart';
import 'models.dart';

enum ConnState { connecting, live, offline }

class DisplayState extends ChangeNotifier {
  final StandPcApi api;
  late final SseConnection _sse;
  Timer? _periodic;
  Timer? _notifTimer;
  bool _disposed = false;

  DisplayState(this.api) {
    _sse = SseConnection(
      api: api,
      onEvent: _onEvent,
      onOpen: () {
        conn = ConnState.live;
        connDetail = '';
        _notify();
        resync();
      },
      onDisconnect: (reason) {
        conn = ConnState.offline;
        connDetail = reason;
        _notify();
      },
    );
  }

  // ---- Zustand ----
  ConnState conn = ConnState.connecting;
  String connDetail = '';
  LaneStatus? status;
  List<Shot> shots = [];
  Map<String, TargetGeom> targets = {};
  ShotDisplay shotDisplay = const ShotDisplay();
  FontSizes fontSizes = const FontSizes();
  PSInfo? ps;
  List<Discipline> disciplines = [];
  String notification = '';
  String zoomMode = 'auto';

  /// Steigende Flanke "Preisschiessen-Auswahl steht an" -> Menue oeffnen.
  final StreamController<void> psOpenRequests = StreamController.broadcast();
  bool _psWasPending = false, _psWasNeedsTeilnehmer = false;

  /// teilnehmer_id, fuer die "Buchen" schon einmal bestaetigt wurde
  String? buchenConfirmedFor;

  int? _historyGen;
  bool _resyncing = false, _resyncAgain = false;

  // ---- abgeleitete Werte ----
  Discipline? get discipline => status?.discipline;
  String get mode => status?.mode ?? 'probe';
  int get shotsPerSeries => discipline?.shotsPerSeries ?? 10;
  bool get decimal => discipline?.decimalScoring ?? false;
  bool get masked => discipline?.masked ?? false;
  bool get fullyHidden => discipline?.fullyHidden ?? false;
  String get shooter => status?.shooter ?? '';

  TargetGeom? get target {
    final key = '${discipline?.targetNo ?? 1}';
    return targets[key] ?? targets['1'];
  }

  String get targetColor =>
      (ps != null && !ps!.pending && !ps!.needsTeilnehmer) ? ps!.currentTargetColor : '';

  bool get showDisciplineButton => shooter.isEmpty && ps == null;
  bool get showPsButton => ps != null;
  bool get showReleaseButton => shooter.isNotEmpty && ps == null;

  String get shooterHeader {
    if (shooter.isNotEmpty) return shooter;
    if (ps != null && ps!.needsTeilnehmer) return 'Teilnehmer wählen…';
    if (ps != null) return '#${ps!.teilnehmerNr} ${ps!.shooterName}';
    return 'Schützenanzeige';
  }

  String get disciplineHeader {
    if (ps != null && !ps!.pending && ps!.currentScheibeName.isNotEmpty) return ps!.currentScheibeName;
    final d = discipline;
    if (d == null) return '';
    return '${d.name} · ${d.trialShots} Probe / ${d.scoringShots} Wertung';
  }

  String get eventHeader {
    final st = status;
    if (st == null || st.eventName.isEmpty) return '';
    const types = {'einzel': 'Einzelwettkampf', 'runde': 'Rundenwettkampf', 'gruppe': 'Gruppenwettkampf'};
    final t = types[st.eventType];
    return t != null ? '${st.eventName} · $t' : st.eventName;
  }

  bool get disciplineComplete {
    final d = discipline;
    return d != null && d.scoringShots > 0 && (status?.wertungCount ?? 0) >= d.scoringShots;
  }

  String get releaseConfirmText => disciplineComplete
      ? 'Stand freigeben?'
      : 'Stand freigeben? Die Disziplin ist noch nicht abgeschlossen – bereits geschossene Schüsse bleiben gespeichert.';

  double scoreOf(Iterable<Shot> list) =>
      list.fold(0.0, (a, s) => a + (decimal ? s.decimal : s.ring.toDouble()));

  String fmtScore(double v) => masked ? '–' : (decimal ? v.toStringAsFixed(1) : v.toStringAsFixed(0));

  // ---- Lebenszyklus ----

  Future<void> start() async {
    await _loadStatic();
    _sse.start();
    _periodic = Timer.periodic(const Duration(seconds: 30), (_) => resync());
  }

  /// Nach Rueckkehr in den Vordergrund / Netzwechsel.
  void reconnect() {
    _sse.reconnect();
    _loadStatic();
  }

  Future<void> _loadStatic() async {
    try {
      final t = await api.getJson('/targets');
      if (t is Map<String, dynamic>) {
        targets = t.map((k, v) => MapEntry(k, TargetGeom.fromJson(v as Map<String, dynamic>)));
      }
      final d = await api.getJson('/display');
      if (d is Map<String, dynamic>) shotDisplay = ShotDisplay.fromJson(d);
      final f = await api.getJson('/font-sizes');
      if (f is Map<String, dynamic>) fontSizes = FontSizes.fromJson(f);
      final dl = await api.getJson('/disciplines');
      if (dl is List) {
        disciplines = dl.whereType<Map<String, dynamic>>().map(Discipline.fromJson).toList();
      }
      _notify();
    } catch (_) {
      // Stand-PC (noch) nicht erreichbar - der Live-Stream versucht es weiter,
      // beim naechsten Connect wird erneut geladen.
    }
  }

  /// Kompletter Neuabgleich: Status + Schuesse + Preisschiessen.
  Future<void> resync() async {
    if (_resyncing) {
      _resyncAgain = true;
      return;
    }
    _resyncing = true;
    try {
      if (targets.isEmpty) await _loadStatic();
      final st = await api.getJson('/status');
      final list = await api.getJson('/shots');
      final psj = await api.getJson('/preisschiessen');
      _applyStatusJson(st, rebuildShots: false);
      shots = [];
      for (final s in (list as List? ?? []).whereType<Map<String, dynamic>>()) {
        _addShot(Shot.fromJson(s));
      }
      _applyPs(psj);
      _notify();
    } catch (_) {
      // naechster Versuch beim naechsten Tick / Connect
    } finally {
      _resyncing = false;
      if (_resyncAgain) {
        _resyncAgain = false;
        resync();
      }
    }
  }

  void _onEvent(SseEvent ev) {
    dynamic data;
    try {
      data = jsonDecode(ev.data);
    } catch (_) {
      return;
    }
    switch (ev.event) {
      case 'shot':
        if (data is Map<String, dynamic>) {
          _addShot(Shot.fromJson(data));
          _notify();
        }
      case 'status':
        _applyStatusJson(data);
      case 'preisschiessen':
        _applyPs(data);
        _notify();
      case 'fontsizes':
        if (data is Map<String, dynamic>) {
          fontSizes = FontSizes.fromJson(data);
          _notify();
        }
    }
  }

  /// Status anwenden; aendert sich history_gen (Session-, Modus- oder
  /// Disziplinwechsel), werden die Schuesse komplett neu geladen (im
  /// Browser: location.reload()).
  void applyStatusFromAction(dynamic st) => _applyStatusJson(st);

  void _applyStatusJson(dynamic j, {bool rebuildShots = true}) {
    if (j is! Map<String, dynamic>) return;
    final st = LaneStatus.fromJson(j);
    final changed = _historyGen != null && st.historyGen != _historyGen;
    _historyGen = st.historyGen;
    status = st;
    if (st.notification.isNotEmpty) showNotification(st.notification);
    if (changed && rebuildShots) {
      shots = [];
      resync();
    }
    _notify();
  }

  void _applyPs(dynamic j) {
    ps = j is Map<String, dynamic> ? PSInfo.fromJson(j) : null;
    final p = ps;
    if (p == null) {
      _psWasPending = false;
      _psWasNeedsTeilnehmer = false;
      return;
    }
    if (p.needsTeilnehmer) {
      if (!_psWasNeedsTeilnehmer) psOpenRequests.add(null);
      _psWasNeedsTeilnehmer = true;
      _psWasPending = false;
      return;
    }
    _psWasNeedsTeilnehmer = false;
    if (p.pending && !_psWasPending) psOpenRequests.add(null);
    _psWasPending = p.pending;
  }

  void _addShot(Shot s) {
    if (s.rejected) return;
    // Probeschuss verwerfen, wenn schon im Wertungsmodus (Auto-Umschaltung
    // durch den letzten Probeschuss, Shot-Event nach dem Status-Event)
    if (s.mode == 'probe' && mode == 'wertung') return;
    shots = [...shots, s];
  }

  void clearDisplay() {
    shots = [];
    _notify();
  }

  void setZoom(String key) {
    zoomMode = key;
    _notify();
  }

  void showNotification(String msg) {
    notification = msg;
    _notifTimer?.cancel();
    _notifTimer = Timer(const Duration(seconds: 5), () {
      notification = '';
      _notify();
    });
    _notify();
  }

  void _notify() {
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    _sse.stop();
    _periodic?.cancel();
    _notifTimer?.cancel();
    psOpenRequests.close();
    api.close();
    super.dispose();
  }
}
