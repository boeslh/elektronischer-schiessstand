// Geraete-Einstellungen: Adresse des Stand-PC und Skalierung. Erreichbar per
// langem Druck auf die Standnummer oben rechts (bzw. automatisch beim ersten
// Start).

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'api.dart';
import 'theme.dart';

class AppSettings {
  String url;
  double scale;
  AppSettings({required this.url, required this.scale});

  static Future<AppSettings> load() async {
    final p = await SharedPreferences.getInstance();
    return AppSettings(url: p.getString('standpc_url') ?? '', scale: p.getDouble('scale') ?? 1.0);
  }

  Future<void> save() async {
    final p = await SharedPreferences.getInstance();
    await p.setString('standpc_url', url);
    await p.setDouble('scale', scale);
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
  late double _scale = widget.settings.scale;
  String _test = '';
  bool _testOk = false, _testing = false;

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

  void _save() {
    final url = normalizeUrl(_url.text);
    if (url.isEmpty) {
      setState(() {
        _testOk = false;
        _test = 'Bitte die Adresse des Stand-PC eingeben.';
      });
      return;
    }
    final s = AppSettings(url: url, scale: _scale);
    s.save();
    widget.onSaved(s);
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Standanzeige – Einstellungen'),
        backgroundColor: C.panel,
        automaticallyImplyLeading: !widget.firstStart,
      ),
      body: ListView(padding: const EdgeInsets.all(20), children: [
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
              onPressed: () => SystemNavigator.pop(),
              child: const Text('App beenden', style: TextStyle(color: C.err)),
            ),
        ]),
      ]),
    );
  }
}
