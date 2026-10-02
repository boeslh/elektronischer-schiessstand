// Farben (wie die CSS-Variablen in standpc/web/index.html), Schrift-
// skalierung und Vollbild.

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'models.dart';

class C {
  static const bg = Color(0xFF11151A);
  static const panel = Color(0xFF1A2028);
  static const line = Color(0xFF2A3340);
  static const text = Color(0xFFD8E0E8);
  static const dim = Color(0xFF6A7888);
  static const acc = Color(0xFF4AB8A0);
  static const hit = Color(0xFFFFB347);
  static const probeBg = Color(0xFF2A2010);
  static const probeFg = Color(0xFFF0A830);
  static const wertungBg = Color(0xFF0D2318);
  static const wertungFg = Color(0xFF3ECF7A);
  static const okBg = Color(0xFF1A3A2A);
  static const errBg = Color(0xFF3A1A1A);
  static const err = Color(0xFFE05C5C);
}

/// Schriftgroessen vom Server (px wie im Browser) x Skalierung dieses Geraets
/// (Einstellungen in der App, z.B. groesser fuer grosse Tablets).
class Ui {
  final FontSizes fs;
  final double scale;
  const Ui(this.fs, this.scale);
  double get k => scale;
}

/// Echtes Vollbild: Status- und Navigationsleiste ausblenden; ein Wischen vom
/// Rand blendet sie nur kurz ein.
void enterFullscreen() {
  SystemChrome.setEnabledSystemUIMode(SystemUiMode.immersiveSticky);
}

ThemeData appTheme() => ThemeData(
      brightness: Brightness.dark,
      scaffoldBackgroundColor: C.bg,
      colorScheme: const ColorScheme.dark(primary: C.acc, surface: C.panel),
      dialogTheme: const DialogThemeData(backgroundColor: C.panel),
      useMaterial3: true,
    );
