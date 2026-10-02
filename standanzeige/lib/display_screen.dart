// Hauptanzeige: Kopfzeile, Scheibe, Ergebnisspalte. Querformat: Scheibe
// links, Ergebnisse rechts (wie im Browser). Hochformat (Handy): Scheibe oben,
// Ergebnisse darunter.

import 'dart:async';

import 'package:flutter/material.dart';

import 'display_state.dart';
import 'menus.dart';
import 'settings.dart';
import 'target_painter.dart';
import 'theme.dart';

class DisplayScreen extends StatefulWidget {
  final DisplayState state;
  final AppSettings settings;
  final VoidCallback onOpenSettings;
  const DisplayScreen({super.key, required this.state, required this.settings, required this.onOpenSettings});

  @override
  State<DisplayScreen> createState() => _DisplayScreenState();
}

class _DisplayScreenState extends State<DisplayScreen> with WidgetsBindingObserver {
  StreamSubscription<void>? _psSub;
  DisplayState get st => widget.state;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _psSub = st.psOpenRequests.stream.listen((_) {
      if (mounted) openPsMenu(context, st, widget.settings.scale);
    });
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _psSub?.cancel();
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState s) {
    // Nach Standby/Bildschirm aus: Verbindung sofort neu aufbauen statt auf
    // den Waechter zu warten.
    if (s == AppLifecycleState.resumed) {
      st.reconnect();
      enterFullscreen();
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: C.bg,
      body: ListenableBuilder(
        listenable: st,
        builder: (context, _) {
          final ui = Ui(st.fontSizes, widget.settings.scale);
          return Stack(children: [
            LayoutBuilder(builder: (context, box) {
              final landscape = box.maxWidth >= box.maxHeight;
              final gap = 8.0 * ui.k;
              final header = _Header(st: st, ui: ui, onOpenSettings: widget.onOpenSettings);
              final target = _Panel(child: _TargetView(st: st));
              if (landscape) {
                final sideW = (box.maxWidth * 0.30).clamp(260.0 * ui.k, 520.0 * ui.k);
                return Padding(
                  padding: EdgeInsets.all(gap),
                  child: Column(children: [
                    header,
                    SizedBox(height: gap),
                    Expanded(
                      child: Row(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
                        Expanded(child: target),
                        SizedBox(width: gap),
                        SizedBox(width: sideW, child: _Panel(child: _Sidebar(st: st, ui: ui))),
                      ]),
                    ),
                  ]),
                );
              }
              return Padding(
                padding: EdgeInsets.all(gap),
                child: Column(children: [
                  header,
                  SizedBox(height: gap),
                  SizedBox(height: box.maxWidth - 2 * gap, child: target),
                  SizedBox(height: gap),
                  Expanded(child: _Panel(child: _Sidebar(st: st, ui: ui))),
                ]),
              );
            }),
            if (st.notification.isNotEmpty)
              Positioned(
                top: 0,
                left: 0,
                right: 0,
                child: Container(
                  color: const Color(0xFFE65C00),
                  padding: EdgeInsets.symmetric(vertical: 10 * ui.k, horizontal: 16 * ui.k),
                  child: Text(st.notification,
                      textAlign: TextAlign.center,
                      style: TextStyle(color: Colors.white, fontSize: 20 * ui.k, fontWeight: FontWeight.bold)),
                ),
              ),
          ]);
        },
      ),
    );
  }
}

class _Panel extends StatelessWidget {
  final Widget child;
  const _Panel({required this.child});
  @override
  Widget build(BuildContext context) => Container(
        decoration: BoxDecoration(
          color: C.panel,
          border: Border.all(color: C.line),
          borderRadius: BorderRadius.circular(8),
        ),
        clipBehavior: Clip.antiAlias,
        child: child,
      );
}

class _Header extends StatelessWidget {
  final DisplayState st;
  final Ui ui;
  final VoidCallback onOpenSettings;
  const _Header({required this.st, required this.ui, required this.onOpenSettings});

  @override
  Widget build(BuildContext context) {
    final wertung = st.mode == 'wertung';
    final d = st.discipline;
    final noProbePhase = d != null && d.trialShots == 0;
    final modeBtnEnabled = !wertung || (!((st.status?.wertungCount ?? 0) > 0) && !noProbePhase);
    final event = st.eventHeader;

    final connText = switch (st.conn) {
      ConnState.live => '● live',
      ConnState.connecting => 'verbinde…',
      ConnState.offline => 'Verbindung unterbrochen – reconnect…',
    };

    final buttons = <Widget>[
      _Badge(
        text: wertung ? 'WERTUNG' : 'PROBE',
        fg: wertung ? C.wertungFg : C.probeFg,
        bg: wertung ? C.wertungBg : C.probeBg,
        size: ui.fs.status * ui.k,
      ),
      _HeaderButton(
        label: wertung ? '← Probe' : '→ Wertung',
        size: ui.fs.status * ui.k,
        onTap: modeBtnEnabled ? () => toggleMode(context, st) : null,
      ),
      if (st.showPsButton) _HeaderButton(label: '🏆', size: 18 * ui.k, onTap: () => openPsMenu(context, st, ui.scale)),
      if (st.showReleaseButton) _HeaderButton(label: '🔓', size: 18 * ui.k, onTap: () => releaseLane(context, st)),
      _HeaderButton(label: '📤', size: 18 * ui.k, onTap: () => openErgebnisMenu(context, st, ui.scale)),
      if (st.showDisciplineButton)
        _HeaderButton(label: '🎯', size: 18 * ui.k, onTap: () => openDisciplineMenu(context, st, ui.scale)),
      _HeaderButton(label: '🔍', size: 18 * ui.k, onTap: () => openZoomMenu(context, st, ui.scale)),
    ];

    final lane = GestureDetector(
      // Einstellungen bewusst versteckt (langer Druck auf die Standnummer),
      // damit Schuetzen sie nicht versehentlich oeffnen.
      onLongPress: onOpenSettings,
      child: Padding(
        padding: EdgeInsets.symmetric(horizontal: 4 * ui.k, vertical: 4 * ui.k),
        child: Column(crossAxisAlignment: CrossAxisAlignment.end, mainAxisSize: MainAxisSize.min, children: [
          Text((st.status?.laneNo ?? 0) > 0 ? 'Stand ${st.status!.laneNo}' : 'Stand ?',
              style: TextStyle(fontSize: 14 * ui.k, fontWeight: FontWeight.w600, color: C.dim)),
          Text(connText,
              style: TextStyle(fontSize: 11 * ui.k, color: st.conn == ConnState.live ? C.acc : C.dim)),
        ]),
      ),
    );

    final titles = Column(crossAxisAlignment: CrossAxisAlignment.start, mainAxisSize: MainAxisSize.min, children: [
      Text(st.shooterHeader,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: TextStyle(fontSize: ui.fs.name * ui.k, fontWeight: FontWeight.w600, color: C.text)),
      if (st.disciplineHeader.isNotEmpty)
        Text(st.disciplineHeader,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: TextStyle(fontSize: ui.fs.scheibe * ui.k, color: C.dim)),
      if (event.isNotEmpty)
        Text(event, maxLines: 1, overflow: TextOverflow.ellipsis, style: TextStyle(fontSize: 11 * ui.k, color: C.acc)),
    ]);

    return LayoutBuilder(builder: (context, box) {
      final row = Wrap(spacing: 6 * ui.k, runSpacing: 6 * ui.k, crossAxisAlignment: WrapCrossAlignment.center, children: buttons);
      if (box.maxWidth < 700 * ui.k) {
        // schmal (Handy hochkant): Knoepfe in eigener Zeile
        return Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
          Row(children: [Expanded(child: titles), lane]),
          SizedBox(height: 6 * ui.k),
          row,
        ]);
      }
      return Row(children: [
        Expanded(child: titles),
        SizedBox(width: 12 * ui.k),
        row,
        SizedBox(width: 12 * ui.k),
        lane,
      ]);
    });
  }
}

class _Badge extends StatelessWidget {
  final String text;
  final Color fg, bg;
  final double size;
  const _Badge({required this.text, required this.fg, required this.bg, required this.size});
  @override
  Widget build(BuildContext context) => Container(
        padding: EdgeInsets.symmetric(horizontal: size * 0.9, vertical: size * 0.5),
        decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(4)),
        child: Text(text,
            style: TextStyle(color: fg, fontSize: size, fontWeight: FontWeight.w700, letterSpacing: 1.5)),
      );
}

class _HeaderButton extends StatelessWidget {
  final String label;
  final double size;
  final VoidCallback? onTap;
  const _HeaderButton({required this.label, required this.size, this.onTap});
  @override
  Widget build(BuildContext context) => Opacity(
        opacity: onTap == null ? 0.35 : 1,
        child: Material(
          color: C.panel,
          shape: RoundedRectangleBorder(side: const BorderSide(color: C.line), borderRadius: BorderRadius.circular(4)),
          child: InkWell(
            onTap: onTap,
            borderRadius: BorderRadius.circular(4),
            child: Padding(
              padding: EdgeInsets.symmetric(horizontal: size * 0.7, vertical: size * 0.4),
              child: Text(label, style: TextStyle(fontSize: size, color: C.text, height: 1.1)),
            ),
          ),
        ),
      );
}

class _TargetView extends StatelessWidget {
  final DisplayState st;
  const _TargetView({required this.st});
  @override
  Widget build(BuildContext context) {
    final t = st.target;
    final viewR = computeViewR(t, st.zoomMode, st.shots, st.shotsPerSeries);
    return CustomPaint(
      size: Size.infinite,
      painter: TargetPainter(
        target: t,
        shots: st.shots,
        perSeries: st.shotsPerSeries,
        viewR: viewR,
        display: st.shotDisplay,
        targetColor: st.targetColor,
        hideShots: st.fullyHidden,
      ),
    );
  }
}

class _Sidebar extends StatelessWidget {
  final DisplayState st;
  final Ui ui;
  const _Sidebar({required this.st, required this.ui});

  @override
  Widget build(BuildContext context) {
    final fe = ui.fs.ergebnisse * ui.k;
    final shots = st.shots;
    final last = shots.isNotEmpty ? shots.last : null;
    final masked = st.masked;

    String lastRing = '–';
    String lastInfo = 'Warte auf ersten Schuss';
    if (last != null) {
      lastRing = masked ? '–' : (st.decimal ? last.decimal.toStringAsFixed(1) : '${last.ring}');
      lastInfo = masked
          ? 'Schuss ${shots.length}'
          : 'Schuss ${shots.length} · x ${last.xMm.toStringAsFixed(1)} / y ${last.yMm.toStringAsFixed(1)} mm'
              '${last.innerTen ? ' · Innenzehner' : ''}';
    }

    final n = st.shotsPerSeries;
    final cells = <Widget>[];
    for (var i = 0; i < shots.length; i += n) {
      final chunk = shots.sublist(i, (i + n).clamp(0, shots.length));
      final complete = chunk.length == n;
      cells.add(Container(
        color: C.panel,
        padding: EdgeInsets.symmetric(vertical: 4 * ui.k),
        child: Column(mainAxisSize: MainAxisSize.min, children: [
          Text('S ${i ~/ n + 1}', style: TextStyle(fontSize: 10 * ui.k, color: C.dim)),
          Text(st.fmtScore(st.scoreOf(chunk)),
              style: TextStyle(
                  fontSize: fe + 6 * ui.k,
                  fontWeight: FontWeight.w700,
                  height: 1.1,
                  color: complete ? C.text : C.hit)),
          if (!complete) Text('${chunk.length} / $n', style: TextStyle(fontSize: 10 * ui.k, color: C.dim)),
        ]),
      ));
    }

    TextStyle cell([bool first = false]) =>
        TextStyle(fontSize: fe, color: first ? C.hit : C.text, fontWeight: first ? FontWeight.w600 : FontWeight.w400);
    final rows = <TableRow>[
      TableRow(
        decoration: const BoxDecoration(border: Border(bottom: BorderSide(color: C.line))),
        children: ['#', 'Ring', 'Zehntel', 'Teiler']
            .map((h) => Padding(
                  padding: EdgeInsets.symmetric(vertical: 6 * ui.k, horizontal: 10 * ui.k),
                  child: Text(h,
                      textAlign: h == '#' ? TextAlign.left : TextAlign.right,
                      style: TextStyle(fontSize: 11 * ui.k, color: C.dim)),
                ))
            .toList(),
      ),
    ];
    for (var i = shots.length - 1; i >= 0; i--) {
      final s = shots[i];
      final first = i == shots.length - 1;
      final vals = masked
          ? ['${i + 1}', '–', '–', '–']
          : ['${i + 1}', '${s.ring}', s.decimal.toStringAsFixed(1), s.centerDistance.toStringAsFixed(1)];
      rows.add(TableRow(
        decoration: const BoxDecoration(border: Border(bottom: BorderSide(color: C.line))),
        children: [
          for (var c = 0; c < 4; c++)
            Padding(
              padding: EdgeInsets.symmetric(vertical: 6 * ui.k, horizontal: 10 * ui.k),
              child: Text(vals[c], textAlign: c == 0 ? TextAlign.left : TextAlign.right, style: cell(first)),
            ),
        ],
      ));
    }

    final total = shots.isEmpty ? '–' : st.fmtScore(st.scoreOf(shots));

    return Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
      // letzter Schuss
      Container(
        padding: EdgeInsets.fromLTRB(12 * ui.k, 12 * ui.k, 12 * ui.k, 8 * ui.k),
        decoration: const BoxDecoration(border: Border(bottom: BorderSide(color: C.line))),
        child: Column(children: [
          FittedBox(
            fit: BoxFit.scaleDown,
            child: Text(lastRing,
                style: TextStyle(fontSize: fe + 51 * ui.k, fontWeight: FontWeight.w700, color: C.acc, height: 1)),
          ),
          SizedBox(height: 6 * ui.k),
          Text(lastInfo, textAlign: TextAlign.center, style: TextStyle(fontSize: 12 * ui.k, color: C.dim)),
        ]),
      ),
      // Gesamt
      Container(
        padding: EdgeInsets.symmetric(vertical: 6 * ui.k, horizontal: 12 * ui.k),
        decoration: const BoxDecoration(border: Border(bottom: BorderSide(color: C.line))),
        child: Row(
          mainAxisAlignment: MainAxisAlignment.center,
          crossAxisAlignment: CrossAxisAlignment.baseline,
          textBaseline: TextBaseline.alphabetic,
          children: [
            Text(total, style: TextStyle(fontSize: fe + 15 * ui.k, fontWeight: FontWeight.w700, color: C.acc)),
            SizedBox(width: 8 * ui.k),
            Text('RINGE GESAMT', style: TextStyle(fontSize: 11 * ui.k, color: C.dim, letterSpacing: 1)),
          ],
        ),
      ),
      // Serien (4 Spalten, max. ~4,5 Reihen, danach scrollbar)
      if (cells.isNotEmpty)
        ConstrainedBox(
          constraints: BoxConstraints(maxHeight: 4.5 * (fe + 34 * ui.k)),
          child: Container(
            color: C.line,
            child: SingleChildScrollView(
              child: Column(children: [
                for (var r = 0; r < cells.length; r += 4)
                  Padding(
                    padding: const EdgeInsets.only(bottom: 1),
                    child: IntrinsicHeight(
                      child: Row(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
                        for (var c = 0; c < 4; c++) ...[
                          if (c > 0) const SizedBox(width: 1),
                          Expanded(child: r + c < cells.length ? cells[r + c] : Container(color: C.panel)),
                        ],
                      ]),
                    ),
                  ),
              ]),
            ),
          ),
        ),
      // Schussliste
      Expanded(
        child: SingleChildScrollView(
          child: Table(columnWidths: const {0: FlexColumnWidth(0.7)}, children: rows),
        ),
      ),
    ]);
  }
}
