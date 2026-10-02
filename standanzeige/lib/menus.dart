// Menues und Aktionen der Kopfzeile - entsprechen den Overlays in
// standpc/web/index.html (Preisschiessen, Ergebnis, Disziplin, Zoom,
// Stand freigeben, Probe/Wertung).

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_svg/flutter_svg.dart';

import 'display_state.dart';
import 'models.dart';
import 'target_painter.dart';
import 'theme.dart';

// ---------------------------------------------------------------------------
// Hilfen
// ---------------------------------------------------------------------------

Future<bool> confirm(BuildContext context, String text) async {
  final r = await showDialog<bool>(
    context: context,
    builder: (ctx) => AlertDialog(
      content: Text(text),
      actions: [
        TextButton(onPressed: () => Navigator.pop(ctx, false), child: const Text('Abbrechen')),
        FilledButton(onPressed: () => Navigator.pop(ctx, true), child: const Text('OK')),
      ],
    ),
  );
  return r == true;
}

Future<void> alert(BuildContext context, String text) => showDialog<void>(
      context: context,
      builder: (ctx) => AlertDialog(
        content: Text(text),
        actions: [FilledButton(onPressed: () => Navigator.pop(ctx), child: const Text('OK'))],
      ),
    );

/// Dialograhmen im Stil der Browser-Menues (Kopf mit Titel + ✕).
class MenuDialog extends StatelessWidget {
  final String title;
  final String? subtitle;
  final double width;
  final double scale;
  final Widget child;
  const MenuDialog({
    super.key,
    required this.title,
    this.subtitle,
    required this.width,
    required this.scale,
    required this.child,
  });

  @override
  Widget build(BuildContext context) {
    final fs = 14 * scale;
    return Dialog(
      backgroundColor: C.panel,
      insetPadding: const EdgeInsets.all(12),
      shape: RoundedRectangleBorder(side: const BorderSide(color: C.line), borderRadius: BorderRadius.circular(8)),
      child: ConstrainedBox(
        constraints: BoxConstraints(maxWidth: width * scale),
        child: Column(mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.stretch, children: [
          Container(
            padding: EdgeInsets.fromLTRB(14 * scale, 10 * scale, 6 * scale, 10 * scale),
            decoration: const BoxDecoration(border: Border(bottom: BorderSide(color: C.line))),
            child: Row(children: [
              Expanded(
                child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                  Text(title.toUpperCase(),
                      style: TextStyle(fontSize: fs - 1, fontWeight: FontWeight.w600, color: C.dim, letterSpacing: 1)),
                  if (subtitle != null && subtitle!.isNotEmpty)
                    Text(subtitle!, style: TextStyle(fontSize: fs - 3, color: C.dim)),
                ]),
              ),
              IconButton(
                onPressed: () => Navigator.pop(context),
                icon: Icon(Icons.close, color: C.dim, size: fs + 6),
              ),
            ]),
          ),
          Flexible(child: SingleChildScrollView(padding: EdgeInsets.symmetric(vertical: 8 * scale), child: child)),
        ]),
      ),
    );
  }
}

/// Listeneintrag (Disziplin, Zoom, Scheibe, Angebot ...)
class MenuItem extends StatelessWidget {
  final String title;
  final String? detail;
  final String? trailing;
  final bool active;
  final double fontSize;
  final VoidCallback? onTap;
  const MenuItem({
    super.key,
    required this.title,
    this.detail,
    this.trailing,
    this.active = false,
    required this.fontSize,
    this.onTap,
  });

  @override
  Widget build(BuildContext context) => InkWell(
        onTap: onTap,
        child: Padding(
          padding: EdgeInsets.symmetric(horizontal: 14, vertical: fontSize * 0.6),
          child: Row(children: [
            Expanded(
              child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                Text(title,
                    style: TextStyle(
                        fontSize: fontSize,
                        color: active ? C.acc : C.text,
                        fontWeight: active ? FontWeight.w600 : FontWeight.w400)),
                if (detail != null) Text(detail!, style: TextStyle(fontSize: fontSize - 3, color: C.dim)),
              ]),
            ),
            if (trailing != null) Text(trailing!, style: TextStyle(fontSize: fontSize - 2, color: C.dim)),
          ]),
        ),
      );
}

class _SubHead extends StatelessWidget {
  final String text;
  final double fontSize;
  const _SubHead(this.text, this.fontSize);
  @override
  Widget build(BuildContext context) => Padding(
        padding: EdgeInsets.fromLTRB(14, fontSize * 0.6, 14, 4),
        child: Text(text.toUpperCase(), style: TextStyle(fontSize: fontSize - 4, color: C.dim, letterSpacing: 1)),
      );
}

// ---------------------------------------------------------------------------
// Probe/Wertung, Stand freigeben
// ---------------------------------------------------------------------------

Future<void> toggleMode(BuildContext context, DisplayState st) async {
  final next = st.mode == 'probe' ? 'wertung' : 'probe';
  try {
    final res = await st.api.postJson('/mode', {'mode': next});
    st.applyStatusFromAction(res);
  } catch (e) {
    if (context.mounted) await alert(context, 'Umschalten nicht möglich:\n$e');
  }
}

Future<void> releaseLane(BuildContext context, DisplayState st) async {
  if (!await confirm(context, st.releaseConfirmText)) return;
  try {
    await st.api.postJson('/freigeben');
    st.clearDisplay();
  } catch (e) {
    if (context.mounted) await alert(context, 'Fehler: $e');
  }
}

// ---------------------------------------------------------------------------
// Disziplin / Zoom
// ---------------------------------------------------------------------------

Future<void> openDisciplineMenu(BuildContext context, DisplayState st, double scale) => showDialog(
      context: context,
      builder: (ctx) {
        final fs = st.fontSizes.menu * scale;
        final current = st.discipline?.name ?? '';
        return MenuDialog(
          title: 'Disziplin wählen',
          width: 380,
          scale: scale,
          child: Column(children: [
            for (final d in st.disciplines)
              MenuItem(
                title: d.name,
                detail: '${d.trialShots > 0 ? '${d.trialShots} Probe · ${d.scoringShots} Wertung' : '${d.scoringShots} Wertung (ohne Probe)'}'
                    ' · ${d.shotsPerSeries} je Serie${d.decimalScoring ? ' · Zehntel' : ''}',
                active: d.name == current,
                fontSize: fs,
                onTap: () async {
                  try {
                    final res = await st.api.postJson('/discipline', {'name': d.name});
                    st.applyStatusFromAction(res);
                    st.clearDisplay();
                    if (ctx.mounted) Navigator.pop(ctx);
                  } catch (e) {
                    if (ctx.mounted) await alert(ctx, 'Fehler: $e');
                  }
                },
              ),
            if (st.disciplines.isEmpty)
              Padding(
                padding: const EdgeInsets.all(14),
                child: Text('Keine Disziplinen geladen.', style: TextStyle(color: C.dim, fontSize: fs)),
              ),
          ]),
        );
      },
    );

Future<void> openZoomMenu(BuildContext context, DisplayState st, double scale) => showDialog(
      context: context,
      builder: (ctx) {
        final fs = st.fontSizes.menu * scale;
        final outerR = st.target?.outerR ?? 0;
        return MenuDialog(
          title: 'Zoom',
          width: 340,
          scale: scale,
          child: Column(children: [
            for (final z in zoomOptions)
              MenuItem(
                title: z.label,
                detail: z.fraction != null
                    ? '±${(z.fraction! * outerR).toStringAsFixed(1)} mm Radius'
                    : 'passt sich der aktuellen Serie an',
                active: z.key == st.zoomMode,
                fontSize: fs,
                onTap: () {
                  st.setZoom(z.key);
                  Navigator.pop(ctx);
                },
              ),
          ]),
        );
      },
    );

// ---------------------------------------------------------------------------
// Preisschiessen
// ---------------------------------------------------------------------------

bool _psMenuOpen = false;

Future<void> openPsMenu(BuildContext context, DisplayState st, double scale) async {
  if (_psMenuOpen || st.ps == null) return;
  _psMenuOpen = true;
  try {
    await showDialog(context: context, builder: (_) => _PsMenu(st: st, scale: scale));
  } finally {
    _psMenuOpen = false;
  }
}

class _PsMenu extends StatefulWidget {
  final DisplayState st;
  final double scale;
  const _PsMenu({required this.st, required this.scale});
  @override
  State<_PsMenu> createState() => _PsMenuState();
}

class _PsMenuState extends State<_PsMenu> {
  DisplayState get st => widget.st;
  final _query = TextEditingController();
  Timer? _searchTimer;
  List<Map<String, dynamic>>? _results;

  @override
  void initState() {
    super.initState();
    st.addListener(_onState);
  }

  @override
  void dispose() {
    st.removeListener(_onState);
    _searchTimer?.cancel();
    _query.dispose();
    super.dispose();
  }

  // Preisschiessen beendet -> Menue schliessen
  void _onState() {
    if (!mounted) return;
    if (st.ps == null) {
      Navigator.of(context).pop();
    } else {
      setState(() {});
    }
  }

  Future<void> _post(String path, [Object? body, bool close = true, bool clear = true]) async {
    try {
      await st.api.postJson(path, body);
      if (clear) st.clearDisplay();
      if (close && mounted) Navigator.of(context).pop();
    } catch (e) {
      if (mounted) await alert(context, 'Fehler: $e');
    }
  }

  void _search(String q) {
    _searchTimer?.cancel();
    q = q.trim();
    if (q.isEmpty) {
      setState(() => _results = null);
      return;
    }
    _searchTimer = Timer(const Duration(milliseconds: 300), () async {
      try {
        final list = await st.api.getJson('/preisschiessen/teilnehmer-suche', query: {'q': q});
        if (mounted) {
          setState(() => _results = (list as List? ?? []).whereType<Map<String, dynamic>>().toList());
        }
      } catch (e) {
        if (mounted) await alert(context, 'Fehler: $e');
      }
    });
  }

  Future<void> _buchen(PSInfo p, PSAngebotItem item, String typ) async {
    if (st.buchenConfirmedFor != p.teilnehmerId) {
      if (!await confirm(context,
          '"${item.name}" für ${formatEuro(item.price)} buchen?\nFalls Ihr Guthaben negativ ist, gehen Sie nach dem Schießen zur Kasse.')) {
        return;
      }
      st.buchenConfirmedFor = p.teilnehmerId;
    }
    try {
      await st.api.postJson('/preisschiessen/buchen', {
        'items': [
          typ == 'scheibe' ? {'typ': 'scheibe', 'scheibe_id': item.id} : {'typ': 'set', 'set_id': item.id}
        ]
      });
      st.showNotification('${item.name} gebucht.');
    } catch (e) {
      if (mounted) await alert(context, 'Buchen nicht möglich:\n$e');
    }
  }

  @override
  Widget build(BuildContext context) {
    final p = st.ps;
    final scale = widget.scale;
    final fs = st.fontSizes.menuPs * scale;
    if (p == null) return const SizedBox.shrink();

    final children = <Widget>[];
    if (p.needsTeilnehmer) {
      children.addAll([
        _SubHead('Teilnehmer suchen', fs),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 4),
          child: TextField(
            controller: _query,
            autofocus: true,
            style: TextStyle(fontSize: fs),
            decoration: const InputDecoration(
              hintText: 'Teilnehmernummer oder Name…',
              border: OutlineInputBorder(),
              isDense: true,
            ),
            onChanged: _search,
          ),
        ),
        if (_results != null && _results!.isEmpty)
          Padding(
            padding: const EdgeInsets.fromLTRB(14, 4, 14, 8),
            child: Text('Keine Treffer.', style: TextStyle(color: C.dim, fontSize: fs - 2)),
          ),
        for (final t in _results ?? const <Map<String, dynamic>>[])
          MenuItem(
            title: '#${t['teilnehmer_nr']} ${t['shooter_name']}',
            fontSize: fs,
            onTap: () async {
              try {
                await st.api.postJson('/preisschiessen/teilnehmer', {'teilnehmer_id': t['teilnehmer_id']});
                _query.clear();
                setState(() => _results = null);
              } catch (e) {
                if (mounted) await alert(this.context, 'Fehler: $e');
              }
            },
          ),
      ]);
    } else {
      children.add(Padding(
        padding: const EdgeInsets.fromLTRB(14, 2, 14, 8),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Text('#${p.teilnehmerNr} ${p.shooterName}', style: TextStyle(fontSize: fs, fontWeight: FontWeight.w600)),
          Text('Guthaben: ${formatEuro(p.guthaben)}', style: TextStyle(fontSize: fs - 2, color: C.dim)),
        ]),
      ));
      if (p.pending) {
        children.add(_SubHead('Scheibe wählen', fs));
        for (final typ in p.scheibenTypen) {
          children.add(MenuItem(
            title: '${typ.name} (${typ.gekauft}/${typ.beendet})',
            fontSize: fs,
            onTap: () => _post('/preisschiessen/scheibe', {'scheibe_id': typ.scheibeId}),
          ));
        }
        if (p.scheibenTypen.isEmpty) {
          children.add(Padding(
            padding: const EdgeInsets.fromLTRB(14, 4, 14, 8),
            child: Text('Keine weitere Scheibe verfügbar.', style: TextStyle(color: C.dim, fontSize: fs - 2)),
          ));
        }
      }
      children.add(_SubHead('Weitere Scheibe/Set buchen', fs));
      for (final s in p.angebotSets) {
        children.add(MenuItem(title: s.name, trailing: formatEuro(s.price), fontSize: fs, onTap: () => _buchen(p, s, 'set')));
      }
      for (final s in p.angebotScheiben) {
        children.add(
            MenuItem(title: s.name, trailing: formatEuro(s.price), fontSize: fs, onTap: () => _buchen(p, s, 'scheibe')));
      }
      if (p.angebotSets.isEmpty && p.angebotScheiben.isEmpty) {
        children.add(Padding(
          padding: const EdgeInsets.fromLTRB(14, 4, 14, 8),
          child: Text('Nichts verfügbar', style: TextStyle(color: C.dim, fontSize: fs - 2)),
        ));
      }
      children.add(_OutlineAction(
        label: 'Stand freigeben',
        color: C.hit,
        border: const Color(0xFF5A3A1A),
        fontSize: fs,
        onTap: () async {
          if (await confirm(context, st.releaseConfirmText)) _post('/preisschiessen/freigeben');
        },
      ));
      if (!p.pending) {
        children.add(_OutlineAction(
          label: 'Scheibe abschließen',
          color: const Color(0xFFFF5C5C),
          border: const Color(0xFF5A1A1A),
          fontSize: fs,
          onTap: () async {
            if (await confirm(
                context, 'Scheibe abschließen? Sie gilt danach als beendet, so als wären alle Schüsse abgegeben worden.')) {
              _post('/preisschiessen/scheibe-abschliessen');
            }
          },
        ));
      }
    }
    // "Preisschiessmodus verlassen": bewusst unauffaellig
    children.add(Container(
      margin: const EdgeInsets.only(top: 16),
      padding: const EdgeInsets.only(top: 6),
      decoration: const BoxDecoration(border: Border(top: BorderSide(color: C.line))),
      child: Align(
        alignment: Alignment.centerLeft,
        child: TextButton(
          onPressed: () async {
            if (await confirm(context,
                'Preisschießmodus für diesen Stand komplett verlassen?\nDas beendet nicht nur den aktuellen Teilnehmer, sondern die gesamte Selbstbedienungs-Reservierung dieses Stands.')) {
              _post('/preisschiessen/verlassen');
            }
          },
          child: Text('Preisschießmodus verlassen',
              style: TextStyle(color: C.dim, fontSize: fs - 3, decoration: TextDecoration.underline)),
        ),
      ),
    ));

    return MenuDialog(
      title: 'Preisschießen',
      subtitle: p.preisschiessenName,
      width: 560,
      scale: scale,
      child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: children),
    );
  }
}

class _OutlineAction extends StatelessWidget {
  final String label;
  final Color color, border;
  final double fontSize;
  final VoidCallback onTap;
  const _OutlineAction({
    required this.label,
    required this.color,
    required this.border,
    required this.fontSize,
    required this.onTap,
  });
  @override
  Widget build(BuildContext context) => Padding(
        padding: const EdgeInsets.fromLTRB(14, 8, 14, 2),
        child: OutlinedButton(
          style: OutlinedButton.styleFrom(
            side: BorderSide(color: border),
            foregroundColor: color,
            padding: EdgeInsets.symmetric(vertical: fontSize * 0.6),
            shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(6)),
          ),
          onPressed: onTap,
          child: Text(label, style: TextStyle(fontSize: fontSize)),
        ),
      );
}

// ---------------------------------------------------------------------------
// Ergebnis mitnehmen (E-Mail / Druck / QR-Code)
// ---------------------------------------------------------------------------

Future<void> openErgebnisMenu(BuildContext context, DisplayState st, double scale) =>
    showDialog(context: context, builder: (_) => _ErgebnisMenu(st: st, scale: scale));

class _ErgebnisMenu extends StatefulWidget {
  final DisplayState st;
  final double scale;
  const _ErgebnisMenu({required this.st, required this.scale});
  @override
  State<_ErgebnisMenu> createState() => _ErgebnisMenuState();
}

class _ErgebnisMenuState extends State<_ErgebnisMenu> {
  Map<String, dynamic>? _info;
  String? _loadError;
  String _msg = '';
  bool _msgErr = false, _busy = false;
  String? _qrSvg;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final d = await widget.st.api.getJson('/ergebnis');
      setState(() => _info = d as Map<String, dynamic>);
    } catch (e) {
      setState(() => _loadError = '$e');
    }
  }

  Future<void> _post(String path, String busy, String Function(Map<String, dynamic>) ok) async {
    setState(() {
      _busy = true;
      _msg = busy;
      _msgErr = false;
    });
    try {
      final d = await widget.st.api
          .postJson(path, {'session_id': _info!['session_id']}, const Duration(seconds: 75));
      setState(() => _msg = ok(d as Map<String, dynamic>));
    } catch (e) {
      setState(() {
        _msg = 'Fehler: $e';
        _msgErr = true;
      });
    } finally {
      setState(() => _busy = false);
    }
  }

  Future<void> _showQr() async {
    setState(() {
      _msg = '';
      _qrSvg = null;
    });
    try {
      final svg = await widget.st.api.getText('/ergebnis/qr.svg', query: {
        'session_id': '${_info!['session_id']}',
        't': '${DateTime.now().millisecondsSinceEpoch}',
      });
      setState(() => _qrSvg = svg);
    } catch (e) {
      setState(() {
        _msg = 'QR-Code konnte nicht erzeugt werden: $e';
        _msgErr = true;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final scale = widget.scale;
    final fs = widget.st.fontSizes.menu * scale;
    final d = _info;
    final children = <Widget>[];

    if (d == null) {
      children.add(Padding(
        padding: const EdgeInsets.all(14),
        child: Text(_loadError ?? 'Lade …', style: TextStyle(fontSize: fs)),
      ));
    } else {
      final t = DateTime.tryParse('${d['started_at']}')?.toLocal();
      String two(int v) => v.toString().padLeft(2, '0');
      final when = t == null ? '' : '${two(t.day)}.${two(t.month)}.${t.year} ${two(t.hour)}:${two(t.minute)}';
      final mailOk = d['mail_configured'] == true && d['has_email'] == true;
      final printOk = d['print_available'] == true;
      final qrOk = (d['shot_count'] as num? ?? 0) <= (d['qr_max_shots'] as num? ?? 60);
      children.addAll([
        Padding(
          padding: const EdgeInsets.fromLTRB(14, 4, 14, 10),
          child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Text('${d['shooter_name'] ?? ''}'.isEmpty ? 'Ohne Schützen' : '${d['shooter_name']}',
                style: TextStyle(fontSize: fs)),
            Text('${d['discipline']} · ${d['shot_count']} Wertungsschüsse · $when',
                style: TextStyle(fontSize: fs - 3, color: C.dim)),
          ]),
        ),
        _ErgButton(
          label: '✉️ Per E-Mail senden',
          hint: d['mail_configured'] != true
              ? 'Kein Mailserver eingerichtet'
              : (d['has_email'] == true ? 'an ${d['email_masked']}' : 'Keine E-Mail-Adresse in den Stammdaten'),
          fontSize: fs,
          onTap: mailOk && !_busy
              ? () => _post('/ergebnis/mail', 'E-Mail wird gesendet …', (r) => 'E-Mail an ${r['email_masked']} gesendet.')
              : null,
        ),
        _ErgButton(
          label: '🖨️ Ausdrucken',
          hint: printOk ? '' : 'Kein Drucker eingerichtet',
          fontSize: fs,
          onTap: printOk && !_busy
              ? () => _post('/ergebnis/print', 'Druckauftrag wird gesendet …', (r) => 'Druckauftrag an ${r['printer']} gesendet.')
              : null,
        ),
        _ErgButton(
          label: '📱 QR-Code für die App',
          hint: qrOk ? 'Schüsse mit Trefferlage in die App übernehmen' : 'Nur bis ${d['qr_max_shots']} Schüsse möglich',
          fontSize: fs,
          onTap: qrOk ? _showQr : null,
        ),
      ]);
      if (_msg.isNotEmpty) {
        children.add(Container(
          margin: const EdgeInsets.fromLTRB(14, 8, 14, 4),
          padding: const EdgeInsets.all(10),
          decoration: BoxDecoration(color: _msgErr ? C.errBg : C.okBg, borderRadius: BorderRadius.circular(6)),
          child: Text(_msg, style: TextStyle(color: _msgErr ? C.err : C.acc, fontSize: fs - 2)),
        ));
      }
      if (_qrSvg != null) {
        children.add(LayoutBuilder(builder: (context, box) {
          final h = MediaQuery.of(context).size.height;
          final size = [box.maxWidth - 28, h - 220, 480.0 * scale].reduce((a, b) => a < b ? a : b);
          return Padding(
            padding: const EdgeInsets.fromLTRB(14, 8, 14, 4),
            child: Column(children: [
              Container(
                width: size,
                height: size,
                decoration: BoxDecoration(color: Colors.white, borderRadius: BorderRadius.circular(6)),
                child: SvgPicture.string(_qrSvg!, fit: BoxFit.contain),
              ),
              const SizedBox(height: 6),
              Text('Mit der App „My Shootings“ → QR-Code scannen',
                  style: TextStyle(color: C.dim, fontSize: fs - 3)),
            ]),
          );
        }));
      }
    }

    return MenuDialog(
      title: 'Ergebnis mitnehmen',
      width: 520,
      scale: scale,
      child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: children),
    );
  }
}

class _ErgButton extends StatelessWidget {
  final String label, hint;
  final double fontSize;
  final VoidCallback? onTap;
  const _ErgButton({required this.label, required this.hint, required this.fontSize, this.onTap});
  @override
  Widget build(BuildContext context) => Padding(
        padding: const EdgeInsets.fromLTRB(14, 4, 14, 4),
        child: Opacity(
          opacity: onTap == null ? 0.45 : 1,
          child: Material(
            color: C.bg,
            shape: RoundedRectangleBorder(side: const BorderSide(color: C.line), borderRadius: BorderRadius.circular(6)),
            child: InkWell(
              onTap: onTap,
              borderRadius: BorderRadius.circular(6),
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 11),
                child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                  Text(label, style: TextStyle(fontSize: fontSize, color: C.text)),
                  if (hint.isNotEmpty) Text(hint, style: TextStyle(fontSize: fontSize - 4, color: C.dim)),
                ]),
              ),
            ),
          ),
        ),
      );
}
