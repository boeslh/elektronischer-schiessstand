// Anbindung an den Stand-PC: JSON-Endpunkte und der Live-Stream (/events,
// Server-Sent Events). Der Live-Stream wird hier selbst gelesen statt ueber
// einen Browser - mit eigenem Waechter, der eine haengende Verbindung
// (keine Daten und kein Keepalive) erkennt und neu aufbaut.

import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;

class ApiException implements Exception {
  final String message;
  ApiException(this.message);
  @override
  String toString() => message;
}

class StandPcApi {
  final String baseUrl; // z.B. http://192.168.2.174:9004
  final http.Client _client = http.Client();

  StandPcApi(String url) : baseUrl = url.trim().replaceAll(RegExp(r'/+$'), '');

  Uri uri(String path, [Map<String, String>? query]) =>
      Uri.parse('$baseUrl$path').replace(queryParameters: query);

  Future<dynamic> getJson(String path,
      {Map<String, String>? query, Duration timeout = const Duration(seconds: 8)}) async {
    final r = await _client.get(uri(path, query)).timeout(timeout);
    return _decode(r);
  }

  Future<dynamic> postJson(String path, [Object? body, Duration timeout = const Duration(seconds: 15)]) async {
    final r = await _client
        .post(uri(path),
            headers: {'Content-Type': 'application/json'},
            body: body == null ? null : jsonEncode(body))
        .timeout(timeout);
    return _decode(r);
  }

  Future<String> getText(String path, {Map<String, String>? query}) async {
    final r = await _client.get(uri(path, query)).timeout(const Duration(seconds: 10));
    if (r.statusCode != 200) throw ApiException(_errorText(r));
    return utf8.decode(r.bodyBytes);
  }

  dynamic _decode(http.Response r) {
    if (r.statusCode < 200 || r.statusCode >= 300) throw ApiException(_errorText(r));
    final body = utf8.decode(r.bodyBytes);
    if (body.trim().isEmpty) return null;
    return jsonDecode(body);
  }

  /// Fehlermeldungen kommen je nach Quelle als JSON {"error": ...} (Server,
  /// durchgereicht) oder als Klartext (http.Error am Stand-PC).
  static String _errorText(http.Response r) {
    final body = utf8.decode(r.bodyBytes, allowMalformed: true).trim();
    try {
      final j = jsonDecode(body);
      if (j is Map && j['error'] is String) return j['error'];
    } catch (_) {}
    return body.isNotEmpty ? body : 'HTTP ${r.statusCode}';
  }

  void close() => _client.close();
}

class SseEvent {
  final String event;
  final String data;
  SseEvent(this.event, this.data);
}

/// Liest /events dauerhaft. Bei Abbruch, Fehler oder wenn laenger als
/// [stallTimeout] nichts ankommt (der Stand-PC sendet alle 20 s ein
/// Keepalive), wird die Verbindung verworfen und nach kurzer Pause neu
/// aufgebaut. [onOpen] laeuft nach jedem erfolgreichen (Re-)Connect - dort
/// gleicht die Anzeige ihren kompletten Zustand neu ab.
class SseConnection {
  final StandPcApi api;
  final void Function(SseEvent) onEvent;
  final void Function() onOpen;
  final void Function(String reason) onDisconnect;
  final Duration stallTimeout;

  http.Client? _client;
  StreamSubscription<String>? _sub;
  Timer? _watchdog;
  Timer? _retry;
  DateTime _lastData = DateTime.now();
  bool _running = false;
  int _attempt = 0;

  SseConnection({
    required this.api,
    required this.onEvent,
    required this.onOpen,
    required this.onDisconnect,
    this.stallTimeout = const Duration(seconds: 45),
  });

  void start() {
    if (_running) return;
    _running = true;
    _connect();
    _watchdog = Timer.periodic(const Duration(seconds: 5), (_) {
      if (_sub != null && DateTime.now().difference(_lastData) > stallTimeout) {
        _fail('keine Daten seit ${stallTimeout.inSeconds} s');
      }
    });
  }

  void stop() {
    _running = false;
    _watchdog?.cancel();
    _retry?.cancel();
    _teardown();
  }

  /// Verbindung sofort neu aufbauen (z.B. nach Rueckkehr der App in den
  /// Vordergrund oder Netzwechsel).
  void reconnect() {
    if (!_running) return;
    _retry?.cancel();
    _teardown();
    _connect();
  }

  void _teardown() {
    _sub?.cancel();
    _sub = null;
    _client?.close();
    _client = null;
  }

  void _fail(String reason) {
    if (!_running) return;
    _teardown();
    onDisconnect(reason);
    _retry?.cancel();
    // 1 s, 2 s, 4 s ... max. 10 s
    final delay = Duration(seconds: [1 << _attempt.clamp(0, 4), 10].reduce((a, b) => a < b ? a : b));
    _attempt++;
    _retry = Timer(delay, _connect);
  }

  Future<void> _connect() async {
    if (!_running) return;
    final client = http.Client();
    _client = client;
    try {
      final req = http.Request('GET', api.uri('/events'))
        ..headers['Accept'] = 'text/event-stream'
        ..headers['Cache-Control'] = 'no-cache';
      final resp = await client.send(req).timeout(const Duration(seconds: 8));
      if (resp.statusCode != 200) throw ApiException('HTTP ${resp.statusCode}');
      if (!identical(_client, client)) return; // inzwischen ersetzt
      _lastData = DateTime.now();
      _attempt = 0;
      onOpen();

      var event = 'message';
      final data = StringBuffer();
      _sub = resp.stream
          .transform(utf8.decoder)
          .transform(const LineSplitter())
          .listen((line) {
        _lastData = DateTime.now();
        if (line.isEmpty) {
          if (data.isNotEmpty) {
            var d = data.toString();
            if (d.endsWith('\n')) d = d.substring(0, d.length - 1);
            onEvent(SseEvent(event, d));
          }
          event = 'message';
          data.clear();
        } else if (line.startsWith(':')) {
          // Keepalive-Kommentar
        } else if (line.startsWith('event:')) {
          event = line.substring(6).trim();
        } else if (line.startsWith('data:')) {
          var v = line.substring(5);
          if (v.startsWith(' ')) v = v.substring(1);
          data.write(v);
          data.write('\n');
        }
      }, onError: (e) {
        if (identical(_client, client)) _fail('Fehler: $e');
      }, onDone: () {
        if (identical(_client, client)) _fail('Verbindung beendet');
      }, cancelOnError: true);
    } catch (e) {
      if (identical(_client, client) || _client == null) _fail('$e');
    }
  }
}
