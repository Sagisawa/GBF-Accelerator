package com.sagisawa.gbfaccelerator.core

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import com.sagisawa.gbfaccelerator.ui.MainActivity

class CoreService : Service() {

    companion object {
        private const val TAG = "GBF-ACC-Service"
        const val CHANNEL_ID = "gbf_accelerator_core_channel"
        const val NOTIFICATION_ID = 8124

        const val ACTION_START = "com.sagisawa.gbfaccelerator.ACTION_START"
        const val ACTION_STOP = "com.sagisawa.gbfaccelerator.ACTION_STOP"

        fun start(context: Context) {
            val intent = Intent(context, CoreService::class.java).apply {
                action = ACTION_START
            }
            try {
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                    context.startForegroundService(intent)
                } else {
                    context.startService(intent)
                }
            } catch (e: Throwable) {
                Log.w(TAG, "Foreground service start failed: ${e.message}, falling back to direct CoreManager start")
                CoreManager.startCore(context.applicationContext)
            }
        }

        fun stop(context: Context) {
            val intent = Intent(context, CoreService::class.java).apply {
                action = ACTION_STOP
            }
            context.startService(intent)
        }
    }

    private var isForeground = false

    private val stateListener: (CoreManager.State) -> Unit = { state ->
        updateNotification(state)
        if (state == CoreManager.State.STOPPED && isForeground) {
            stopSelf()
        }
    }

    override fun onCreate() {
        super.onCreate()
        Log.i(TAG, "CoreService created")
        createNotificationChannel()
        CoreManager.addStateListener(stateListener)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val action = intent?.action ?: ACTION_START
        Log.i(TAG, "onStartCommand action: $action")

        if (action == ACTION_STOP) {
            CoreManager.stopCore()
            stopSelf()
            return START_NOT_STICKY
        }

        // Start Foreground Service
        startAsForeground(CoreManager.currentState)

        // Launch Go Core if not already running
        if (CoreManager.currentState == CoreManager.State.STOPPED || CoreManager.currentState == CoreManager.State.CRASHED) {
            CoreManager.startCore(applicationContext)
        }

        return START_STICKY
    }

    private fun startAsForeground(state: CoreManager.State) {
        val notification = buildNotification(state)
        val fgsType = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE
        } else {
            0
        }
        ServiceCompat.startForeground(this, NOTIFICATION_ID, notification, fgsType)
        isForeground = true
    }

    private fun updateNotification(state: CoreManager.State) {
        if (!isForeground) return
        val notification = buildNotification(state)
        val nm = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        nm.notify(NOTIFICATION_ID, notification)
    }

    private fun buildNotification(state: CoreManager.State): Notification {
        val contentText = when (state) {
            CoreManager.State.RUNNING -> "Proxy listening on 127.0.0.1:8124 (PID ${CoreManager.currentMetrics.pid})"
            CoreManager.State.STARTING -> "Launching Go Core daemon..."
            CoreManager.State.STOPPING -> "Stopping Go Core..."
            CoreManager.State.STOPPED -> "Proxy is stopped"
            CoreManager.State.CRASHED -> "Proxy crashed or failed to start"
        }

        val launchIntent = Intent(this, MainActivity::class.java).apply {
            flags = Intent.FLAG_ACTIVITY_SINGLE_TOP
        }
        val pendingIntent = PendingIntent.getActivity(
            this,
            0,
            launchIntent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )

        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setContentTitle("GBF-Accelerator Service")
            .setContentText(contentText)
            .setSmallIcon(android.R.drawable.ic_dialog_info)
            .setContentIntent(pendingIntent)
            .setOngoing(state == CoreManager.State.RUNNING || state == CoreManager.State.STARTING)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .build()
    }

    private fun createNotificationChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val channel = NotificationChannel(
                CHANNEL_ID,
                "GBF-Accelerator Background Service",
                NotificationManager.IMPORTANCE_LOW
            ).apply {
                description = "Keeps the local Go Core proxy active in background"
                setShowBadge(false)
            }
            val nm = getSystemService(NotificationManager::class.java)
            nm?.createNotificationChannel(channel)
        }
    }

    override fun onDestroy() {
        Log.i(TAG, "CoreService destroying, stopping CoreManager...")
        CoreManager.removeStateListener(stateListener)
        CoreManager.stopCore()
        isForeground = false
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null
}
