// Standanzeige - native Android-App fuer die Schuetzenanzeige am Stand
// (Gegenstueck zu standpc/web/index.html, ohne Testmodus). Verbindet sich
// direkt mit dem Stand-PC (gleiche Endpunkte wie der Browser).

import 'package:flutter/material.dart';
import 'package:wakelock_plus/wakelock_plus.dart';

import 'display_screen.dart';
import 'display_state.dart';
import 'api.dart';
import 'settings.dart';
import 'theme.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  enterFullscreen();
  WakelockPlus.enable(); // Bildschirm bleibt an
  final settings = await AppSettings.load();
  // Eingebauter Stand-PC (Standalone-Betrieb): Dienst beim App-Start starten
  // (laeuft er schon, bleibt er unveraendert)
  if (settings.embedded) {
    try {
      await settings.applyEmbedded();
    } catch (_) {/* Fehler zeigt die Einstellungsseite */}
  }
  runApp(StandanzeigeApp(settings: settings));
}

class StandanzeigeApp extends StatefulWidget {
  final AppSettings settings;
  const StandanzeigeApp({super.key, required this.settings});

  @override
  State<StandanzeigeApp> createState() => _StandanzeigeAppState();
}

class _StandanzeigeAppState extends State<StandanzeigeApp> {
  final _navKey = GlobalKey<NavigatorState>();
  late AppSettings _settings = widget.settings;
  DisplayState? _state;

  @override
  void initState() {
    super.initState();
    if (_settings.displayUrl.isNotEmpty) _startState();
  }

  void _startState() {
    _state?.dispose();
    _state = DisplayState(StandPcApi(_settings.displayUrl))..start();
  }

  void _onSaved(AppSettings s) {
    final urlChanged = s.displayUrl != _settings.displayUrl || _state == null;
    setState(() {
      _settings = s;
      if (urlChanged) _startState();
    });
    final nav = _navKey.currentState;
    if (nav != null && nav.canPop()) nav.pop();
    enterFullscreen();
  }

  void _openSettings() {
    _navKey.currentState?.push(MaterialPageRoute(
      builder: (_) => SettingsScreen(settings: _settings, onSaved: _onSaved),
    ));
  }

  @override
  void dispose() {
    _state?.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final state = _state;
    return MaterialApp(
      title: 'Standanzeige',
      navigatorKey: _navKey,
      debugShowCheckedModeBanner: false,
      theme: appTheme(),
      home: state == null
          ? SettingsScreen(settings: _settings, onSaved: _onSaved, firstStart: true)
          // Zurueck-Taste beendet die Anzeige nicht versehentlich (Beenden
          // ueber die Einstellungen)
          : PopScope(
              canPop: false,
              child: DisplayScreen(
                key: ValueKey(state),
                state: state,
                settings: _settings,
                onOpenSettings: _openSettings,
              ),
            ),
    );
  }
}
