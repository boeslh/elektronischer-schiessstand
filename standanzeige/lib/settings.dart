// Geraete-Einstellungen: Adresse des Stand-PC bzw. eingebauter Stand-PC
// (Standalone-Betrieb, siehe standpc_host.dart) und Skalierung. Erreichbar per
// langem Druck auf die Standnummer oben rechts (bzw. automatisch beim ersten
// Start).

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'api.dart';
import 'standpc_host.dart';
import 'theme.dart';

class AppSettings {
  String url;
  double scale;
  // Eingebauter Stand-PC (Standalone-Betrieb ohne Server)
  bool embedded;
  int laneNo;
  int espPort;
  String psk;
  AppSettings({
    required this.url,
    required this.scale,
    this.embedded = false,
    this.laneNo = 1,
    this.espPort = 9201,
    this.psk = '',
  });

  /// Adresse, mit der sich die Anzeige verbindet.
  String get displayUrl => embedded ? embeddedUrl : url;

  static Future<AppSettings> load() async {
    final p = await SharedPreferences.getInstance();
    return AppSettings(
      url: p.getString('standpc_url') ?? '',
      scale: p.getDouble('scale') ?? 1.0,
      embedded: p.getBool('embedded') ?? false,
      laneNo: p.getInt('embedded_lane') ?? 1,
      espPort: p.getInt('embedded_esp_port') ?? 9201,
      psk: p.getString('embedded_psk') ?? '',
    );
  }

  Future<void> save() async {
    final p = await SharedPreferences.getInstance();
    await p.setString('standpc_url', url);
    await p.setDouble('scale', scale);
    await p.setBool('embedded', embedded);
    await p.setInt('embedded_lane', laneNo);
    await p.setInt('embedded_esp_port', espPort);
    await p.setString('embedded_psk', psk);
  }

  /// Eingebauten Stand-PC passend zu den Einstellungen starten bzw. beenden.
  Future<void> applyEmbedded() async {
    if (embedded) {
      await StandPcHost.start(laneNo: laneNo, espPort: espPort, psk: psk);
    } else {
      await StandPcHost.stop();
    }
  }
}

/// "192.168.2.174:9004" -> "http://192.168.2.174:9004"
String normalizeUrl(String raw) {
  var u = raw.trim().replaceAll(RegExp(r'/+$'), '');
  if (u.isEmpty) return u;
  if (!u.startsWith('http://') && !u.startsWith('https://')) u = 'http://$u';
  return u;
}

class SettingsScreen extends StatefulWidget {
  final AppSettings settings;
  final bool firstStart;
  final void Function(AppSettings) onSaved;
  const SettingsScreen({super.key, required this.settings, required this.onSaved, this.firstStart = false});

  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  late final TextEditingController _url = TextEditingController(text: widget.settings.url);
  late final TextEditingController _lane = TextEditingController(text: '${widget.settings.laneNo}');
  late final TextEditingController _port = TextEditingController(text: '${widget.settings.espPort}');
  late final TextEditingController _psk = TextEditingController(text: widget.settings.psk);
  late double _scale = widget.settings.scale;
  late bool _embedded = widget.settings.embedded;
  String _test = '';
  bool _testOk = false, _testing = false;
  List<String> _addresses = [];
  Map<String, dynamic>? _hostStatus;
  Timer? _statusTimer;

  @override
  void initState() {
    super.initState();
    _refreshHost();
    _statusTimer = Timer.periodic(const Duration(seconds: 3), (_) => _refreshHost());
  }

  @override
  void dispose() {
    _statusTimer?.cancel();
    super.dispose();
  }

  Future<void> _refreshHost() async {
    try {
      final a = await StandPcHost.addresses();
      final st = await StandPcHost.status();
      if (mounted) {
        setState(() {
          _addresses = a;
          _hostStatus = st;
        });
      }
    } catch (_) {/* nur Anzeige */}
  }

  Future<void> _testConnection() async {
    final url = normalizeUrl(_url.text);
    if (url.isEmpty) return;
    setState(() {
      _testing = true;
      _test = 'Prüfe …';
    });
    final api = StandPcApi(url);
    try {
      final st = await api.getJson('/status');
      final lane = st is Map ? st['lane_no'] : null;
      setState(() {
        _testOk = true;
        _test = 'Verbunden: Stand ${lane ?? '?'}';
      });
    } catch (e) {
      setState(() {
        _testOk = false;
        _test = 'Nicht erreichbar: $e';
      });
    } finally {
      api.close();
      setState(() => _testing = false);
    }
  }

  Future<void> _save() async {
    if (_embedded) {
      final lane = int.tryParse(_lane.text.trim());
      final port = int.tryParse(_port.text.trim());
      final psk = _psk.text.trim();
      String? err;
      if (lane == null || lane < 1 || lane > 99) err = 'Standnummer 1–99 eingeben.';
      if (port == null || port < 1025 || port > 65535) err = 'Port 1025–65535 eingeben.';
      if (psk.isNotEmpty && !RegExp(r'^[0-9a-fA-F]{64}$').hasMatch(psk)) {
        err = 'Schlüssel: 64 Hex-Zeichen oder leer.';
      }
      if (err != null) {
        setState(() {
          _testOk = false;
          _test = err!;
        });
        return;
      }
      final s = AppSettings(url: widget.settings.url, scale: _scale, embedded: true,
          laneNo: lane!, espPort: port!, psk: psk);
      await s.save();
      await s.applyEmbedded();
      widget.onSaved(s);
      return;
    }
    final url = normalizeUrl(_url.text);
    if (url.isEmpty) {
      setState(() {
        _testOk = false;
        _test = 'Bitte die Adresse des Stand-PC eingeben.';
      });
      return;
    }
    final s = AppSettings(url: url, scale: _scale, embedded: false,
        laneNo: widget.settings.laneNo, espPort: widget.settings.espPort, psk: widget.settings.psk);
    await s.save();
    if (widget.settings.embedded) await s.applyEmbedded(); // eingebauten Stand-PC beenden
    widget.onSaved(s);
  }

  Widget _embeddedFields() {
    final running = _hostStatus?['running'] == true;
    final ips = _addresses.map((a) => a.split(' ').first).toList();
    final ip = ips.isNotEmpty ? ips.first : '<Adresse dieses Geräts>';
    const hint = TextStyle(color: C.dim, fontSize: 12);
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      Row(children: [
        SizedBox(
          width: 140,
          child: TextField(
            controller: _lane,
            keyboardType: TextInputType.number,
            decoration: const InputDecoration(labelText: 'Standnummer', border: OutlineInputBorder()),
          ),
        ),
        const SizedBox(width: 12),
        SizedBox(
          width: 180,
          child: TextField(
            controller: _port,
            keyboardType: TextInputType.number,
            onChanged: (_) => setState(() {}),
            decoration: const InputDecoration(labelText: 'Port für den ESP32', border: OutlineInputBorder()),
          ),
        ),
      ]),
      const SizedBox(height: 12),
      TextField(
        controller: _psk,
        decoration: const InputDecoration(
          labelText: 'Schlüssel der Anlage (esp32_psk_hex, optional)',
          hintText: '64 Hex-Zeichen wie in der config.json der Stand-PCs',
          border: OutlineInputBorder(),
        ),
      ),
      const SizedBox(height: 12),
      Text(
        widget.settings.embedded
            ? (running ? 'Stand-PC läuft.' : 'Stand-PC läuft nicht. ${_hostStatus?['last_exit'] ?? ''}')
            : 'Wird beim Speichern gestartet.',
        style: TextStyle(color: running || !widget.settings.embedded ? C.acc : C.err),
      ),
      const SizedBox(height: 8),
      Text('Adresse dieses Geräts: ${_addresses.isEmpty ? 'kein Netzwerk' : _addresses.join(', ')}', style: hint),
      const SizedBox(height: 4),
      Text(
        'ESP32 auf dieses Gerät umstellen (über die Admin-Seite des bisherigen Stand-PC): '
        'SET HOST=$ip und SET PORT=${_port.text.trim()}. Das Gerät braucht dafür eine feste Adresse im WLAN '
        '(oder es stellt selbst den Hotspot).',
        style: hint,
      ),
      const SizedBox(height: 4),
      Text(
        'Durchgänge in den Server übernehmen: Standaktion → Lokale Durchgänge importieren, '
        'StandPC-URL http://$ip:$embeddedHttpPort. Dort lassen sich unter „Disziplinen übertragen“ '
        'auch weitere Disziplinen auf dieses Gerät übertragen.',
        style: hint,
      ),
      if (widget.settings.embedded && (_hostStatus?['log'] ?? '').toString().isNotEmpty)
        ExpansionTile(
          tilePadding: EdgeInsets.zero,
          title: const Text('Protokoll des Stand-PC', style: TextStyle(color: C.dim, fontSize: 13)),
          children: [
            Container(
              width: double.infinity,
              padding: const EdgeInsets.all(8),
              color: Colors.black26,
              child: SelectableText(_hostStatus!['log'].toString(),
                  style: const TextStyle(fontFamily: 'monospace', fontSize: 11)),
            ),
          ],
        ),
    ]);
  }

  List<Widget> _externalFields() => [
        const Text('Adresse des Stand-PC', style: TextStyle(color: C.dim)),
        const SizedBox(height: 6),
        TextField(
          controller: _url,
          keyboardType: TextInputType.url,
          decoration: const InputDecoration(
            hintText: 'z.B. 192.168.2.174:9004',
            border: OutlineInputBorder(),
          ),
          onSubmitted: (_) => _testConnection(),
        ),
        const SizedBox(height: 6),
        const Text(
          'Dieselbe Adresse, unter der die Anzeige im Browser läuft (Port je Stand, z.B. 9004 für Stand 4).',
          style: TextStyle(color: C.dim, fontSize: 12),
        ),
        const SizedBox(height: 12),
        Row(children: [
          OutlinedButton(onPressed: _testing ? null : _testConnection, child: const Text('Verbindung testen')),
          const SizedBox(width: 12),
          Expanded(child: Text(_test, style: TextStyle(color: _testOk ? C.acc : C.err))),
        ]),
      ];

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Standanzeige – Einstellungen'),
        backgroundColor: C.panel,
        automaticallyImplyLeading: !widget.firstStart,
      ),
      body: ListView(padding: const EdgeInsets.all(20), children: [
        SwitchListTile(
          contentPadding: EdgeInsets.zero,
          value: _embedded,
          onChanged: (v) => setState(() => _embedded = v),
          title: const Text('Eingebauter Stand-PC (ohne Server)'),
          subtitle: const Text(
            'Dieses Gerät übernimmt die Aufgabe des Stand-PC: der ESP32 verbindet sich per WLAN hierher. '
            'Training mit Disziplinauswahl; jeder Durchgang wird mit Disziplin und Zeitpunkt gespeichert '
            'und kann später vom Server importiert werden.',
            style: TextStyle(color: C.dim, fontSize: 12),
          ),
        ),
        const SizedBox(height: 16),
        if (_embedded) _embeddedFields() else ..._externalFields(),
        if (_embedded && _test.isNotEmpty) ...[
          const SizedBox(height: 8),
          Text(_test, style: TextStyle(color: _testOk ? C.acc : C.err)),
        ],
        const SizedBox(height: 28),
        Text('Skalierung: ${(_scale * 100).round()} %', style: const TextStyle(color: C.dim)),
        Slider(
          value: _scale,
          min: 0.6,
          max: 2.5,
          divisions: 38,
          label: '${(_scale * 100).round()} %',
          onChanged: (v) => setState(() => _scale = v),
        ),
        const Text(
          'Vergrößert bzw. verkleinert Schrift, Knöpfe und Ergebnisspalte auf diesem Gerät '
          '(zusätzlich zu den am Server eingestellten Schriftgrößen). Die Scheibe nutzt immer den ganzen freien Platz.',
          style: TextStyle(color: C.dim, fontSize: 12),
        ),
        const SizedBox(height: 28),
        Row(children: [
          FilledButton(onPressed: _save, child: const Text('Speichern')),
          const Spacer(),
          if (!widget.firstStart)
            TextButton(
              onPressed: () async {
                if (widget.settings.embedded) await StandPcHost.stop();
                SystemNavigator.pop();
              },
              child: const Text('App beenden', style: TextStyle(color: C.err)),
            ),
        ]),
      ]),
    );
  }
}
