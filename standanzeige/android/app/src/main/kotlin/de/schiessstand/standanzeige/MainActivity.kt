package de.schiessstand.standanzeige

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel
import java.net.Inet4Address
import java.net.NetworkInterface

// MethodChannel "standpc": Steuerung des eingebauten Stand-PC
// (StandPcService) aus der Flutter-Oberflaeche.
//   start(config)  Konfiguration (JSON) schreiben und Dienst (neu) starten
//   stop()         Dienst beenden
//   status()       {running, last_exit, log}
//   addresses()    IPv4-Adressen des Geraets (WLAN/Hotspot)
class MainActivity : FlutterActivity() {
    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, "de.schiessstand.standanzeige/standpc")
            .setMethodCallHandler { call, result ->
                when (call.method) {
                    "start" -> {
                        requestNotificationPermission()
                        val intent = Intent(this, StandPcService::class.java)
                            .putExtra(StandPcService.EXTRA_CONFIG, call.argument<String>("config"))
                        if (Build.VERSION.SDK_INT >= 26) startForegroundService(intent) else startService(intent)
                        result.success(true)
                    }
                    "stop" -> {
                        stopService(Intent(this, StandPcService::class.java))
                        result.success(true)
                    }
                    "status" -> {
                        val log = StandPcService.logFile(this)
                        val tail = if (log.exists()) log.readLines().takeLast(30).joinToString("\n") else ""
                        result.success(mapOf(
                            "running" to StandPcService.running,
                            "last_exit" to StandPcService.lastExit,
                            "log" to tail,
                        ))
                    }
                    "addresses" -> result.success(ipv4Addresses())
                    else -> result.notImplemented()
                }
            }
    }

    private fun requestNotificationPermission() {
        if (Build.VERSION.SDK_INT >= 33 &&
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 1)
        }
    }

    private fun ipv4Addresses(): List<String> {
        val out = mutableListOf<String>()
        try {
            for (ni in NetworkInterface.getNetworkInterfaces()) {
                if (!ni.isUp || ni.isLoopback) continue
                for (addr in ni.inetAddresses) {
                    if (addr is Inet4Address) out.add("${addr.hostAddress} (${ni.name})")
                }
            }
        } catch (_: Exception) {
        }
        return out
    }
}
