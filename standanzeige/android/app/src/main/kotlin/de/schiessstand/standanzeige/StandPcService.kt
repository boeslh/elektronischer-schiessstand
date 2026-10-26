package de.schiessstand.standanzeige

// Eingebauter Stand-PC (Standalone-Betrieb ohne Server): startet das mit
// der App ausgelieferte Stand-PC-Programm (standpc, fuer Android uebersetzt,
// liegt als lib/<abi>/libstandpc.so im APK und wird beim Installieren nach
// nativeLibraryDir entpackt) als eigenen Prozess in einem Vordergrunddienst.
// Arbeitsverzeichnis ist filesDir/standpc (Konfiguration, Schussprotokoll,
// Disziplinen). Beendet sich der Prozess unerwartet, wird er nach 2 s neu
// gestartet. WLAN- und Wake-Lock halten die Verbindung zum ESP32 auch bei
// ausgeschaltetem Bildschirm.

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.wifi.WifiManager
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import java.io.File

class StandPcService : Service() {
    companion object {
        const val EXTRA_CONFIG = "config"
        private const val CHANNEL = "standpc"
        private const val NOTIFICATION_ID = 1
        private const val MAX_LOG_BYTES = 2_000_000L

        @Volatile var running = false
            private set
        @Volatile var lastExit: String = ""
            private set

        fun workDir(ctx: Context): File = File(ctx.filesDir, "standpc").apply { mkdirs() }
        fun logFile(ctx: Context): File = File(workDir(ctx), "standpc.log")
    }

    @Volatile private var stopping = false
    @Volatile private var proc: Process? = null
    private var supervisor: Thread? = null
    private var currentConfig: String? = null
    private var wifiLock: WifiManager.WifiLock? = null
    private var wakeLock: PowerManager.WakeLock? = null

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        startInForeground()
        val config = intent?.getStringExtra(EXTRA_CONFIG)
            ?: File(workDir(this), "config.json").takeIf { it.exists() }?.readText()
        if (config == null) {
            stopSelf()
            return START_NOT_STICKY
        }
        if (config != currentConfig || supervisor?.isAlive != true) {
            currentConfig = config
            File(workDir(this), "config.json").writeText(config)
            restartSupervisor()
        }
        acquireLocks()
        return START_STICKY
    }

    override fun onDestroy() {
        stopping = true
        proc?.destroy()
        supervisor?.interrupt()
        running = false
        wifiLock?.takeIf { it.isHeld }?.release()
        wakeLock?.takeIf { it.isHeld }?.release()
        super.onDestroy()
    }

    private fun restartSupervisor() {
        stopping = true
        proc?.destroy()
        supervisor?.interrupt()
        supervisor?.join(3000)
        stopping = false
        supervisor = Thread({ superviseLoop() }, "standpc").apply { start() }
    }

    private fun superviseLoop() {
        val exe = File(applicationInfo.nativeLibraryDir, "libstandpc.so")
        val dir = workDir(this)
        val log = logFile(this)
        while (!stopping) {
            if (log.length() > MAX_LOG_BYTES) {
                log.renameTo(File(dir, "standpc.log.1"))
            }
            try {
                val p = ProcessBuilder(exe.absolutePath, "-config", File(dir, "config.json").absolutePath)
                    .directory(dir)
                    .redirectErrorStream(true)
                    .redirectOutput(ProcessBuilder.Redirect.appendTo(log))
                    .start()
                proc = p
                running = true
                val code = p.waitFor()
                lastExit = "beendet mit Code $code"
            } catch (e: InterruptedException) {
                break
            } catch (e: Exception) {
                lastExit = "Start fehlgeschlagen: ${e.message}"
                log.appendText("Start fehlgeschlagen: ${e.message}\n")
            } finally {
                running = false
                proc = null
            }
            if (stopping) break
            try {
                Thread.sleep(2000)
            } catch (e: InterruptedException) {
                break
            }
        }
    }

    private fun acquireLocks() {
        if (wifiLock == null) {
            val wm = applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager
            @Suppress("DEPRECATION")
            val mode = if (Build.VERSION.SDK_INT >= 29) WifiManager.WIFI_MODE_FULL_LOW_LATENCY
                       else WifiManager.WIFI_MODE_FULL_HIGH_PERF
            wifiLock = wm.createWifiLock(mode, "standanzeige:standpc").apply { acquire() }
        }
        if (wakeLock == null) {
            val pm = getSystemService(Context.POWER_SERVICE) as PowerManager
            wakeLock = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "standanzeige:standpc").apply { acquire() }
        }
    }

    private fun startInForeground() {
        val nm = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        if (Build.VERSION.SDK_INT >= 26) {
            nm.createNotificationChannel(
                NotificationChannel(CHANNEL, "Stand-PC", NotificationManager.IMPORTANCE_LOW))
        }
        val open = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
        val builder = if (Build.VERSION.SDK_INT >= 26) Notification.Builder(this, CHANNEL)
                      else @Suppress("DEPRECATION") Notification.Builder(this)
        val notification = builder
            .setContentTitle("Stand-PC läuft")
            .setContentText("Schüsse werden erfasst und gespeichert")
            .setSmallIcon(R.mipmap.ic_launcher)
            .setContentIntent(open)
            .setOngoing(true)
            .build()
        if (Build.VERSION.SDK_INT >= 34) {
            startForeground(NOTIFICATION_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        } else {
            startForeground(NOTIFICATION_ID, notification)
        }
    }
}
