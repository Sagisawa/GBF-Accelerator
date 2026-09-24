package com.sagisawa.gbfaccelerator.ui

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.os.Build
import android.os.Bundle
import android.widget.Button
import android.widget.ScrollView
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AppCompatActivity
import androidx.core.content.ContextCompat
import com.sagisawa.gbfaccelerator.core.CoreManager
import com.sagisawa.gbfaccelerator.core.CoreService
import com.sagisawa.gbfaccelerator.xposed.R
import java.util.Locale

class MainActivity : AppCompatActivity() {

    private lateinit var tvStatusBadge: TextView
    private lateinit var tvPidUptime: TextView
    private lateinit var tvPorts: TextView
    private lateinit var btnToggleService: Button
    private lateinit var btnLaunchSkyleap: Button

    private lateinit var tvMetricRamCache: TextView
    private lateinit var tvMetricHits: TextView
    private lateinit var tvMetricRequests: TextView
    private lateinit var tvMetricReuse: TextView

    private lateinit var svLogs: ScrollView
    private lateinit var tvLogContent: TextView
    private lateinit var tvClearLogs: TextView

    private val stateListener: (CoreManager.State) -> Unit = { state ->
        runOnUiThread { updateStateUi(state) }
    }

    private val metricsListener: (CoreManager.CoreMetrics) -> Unit = { metrics ->
        runOnUiThread { updateMetricsUi(metrics) }
    }

    private val logListener: (String) -> Unit = { _ ->
        runOnUiThread { renderLogs() }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O_MR1) {
            setShowWhenLocked(true)
            setTurnScreenOn(true)
        }
        setContentView(R.layout.activity_main)

        initViews()

        CoreManager.addStateListener(stateListener)
        CoreManager.addMetricsListener(metricsListener)
        CoreManager.addLogListener(logListener)

        renderLogs()
        handleIntentAction(intent)

        if (intent?.getStringExtra("action") == null) {
            requestNotificationPermission()
        }
    }

    override fun onNewIntent(intent: Intent?) {
        super.onNewIntent(intent)
        setIntent(intent)
        handleIntentAction(intent)
    }

    private fun handleIntentAction(intent: Intent?) {
        when (intent?.getStringExtra("action")) {
            "start" -> CoreService.start(this)
            "stop" -> CoreService.stop(this)
            "launch_skyleap" -> {
                val pkg = "com.dena.skyleap"
                val launchIntent = packageManager.getLaunchIntentForPackage(pkg)
                if (launchIntent != null) {
                    startActivity(launchIntent)
                } else {
                    Toast.makeText(this, "SkyLeap ($pkg) not found on device", Toast.LENGTH_SHORT).show()
                }
            }
        }
    }

    private fun initViews() {
        tvStatusBadge = findViewById(R.id.tv_status_badge)
        tvPidUptime = findViewById(R.id.tv_pid_uptime)
        tvPorts = findViewById(R.id.tv_ports)
        btnToggleService = findViewById(R.id.btn_toggle_service)
        btnLaunchSkyleap = findViewById(R.id.btn_launch_skyleap)

        tvMetricRamCache = findViewById(R.id.tv_metric_ram_cache)
        tvMetricHits = findViewById(R.id.tv_metric_hits)
        tvMetricRequests = findViewById(R.id.tv_metric_requests)
        tvMetricReuse = findViewById(R.id.tv_metric_reuse)

        svLogs = findViewById(R.id.sv_logs)
        tvLogContent = findViewById(R.id.tv_log_content)
        tvClearLogs = findViewById(R.id.tv_clear_logs)

        btnToggleService.setOnClickListener {
            when (CoreManager.currentState) {
                CoreManager.State.RUNNING, CoreManager.State.STARTING -> {
                    CoreService.stop(this)
                }
                CoreManager.State.STOPPED, CoreManager.State.CRASHED -> {
                    CoreService.start(this)
                }
                CoreManager.State.STOPPING -> {
                    // Ignored while stopping
                }
            }
        }

        btnLaunchSkyleap.setOnClickListener {
            val pkg = "com.dena.skyleap"
            val launchIntent = packageManager.getLaunchIntentForPackage(pkg)
            if (launchIntent != null) {
                startActivity(launchIntent)
            } else {
                Toast.makeText(this, "SkyLeap ($pkg) not found on device", Toast.LENGTH_SHORT).show()
            }
        }

        tvClearLogs.setOnClickListener {
            tvLogContent.text = ""
        }
    }

    private fun requestNotificationPermission() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            if (ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS)
                != PackageManager.PERMISSION_GRANTED) {
                requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 101)
            }
        }
    }

    private fun updateStateUi(state: CoreManager.State) {
        tvStatusBadge.text = state.name

        val (colorRes, btnText, btnColorRes) = when (state) {
            CoreManager.State.RUNNING -> Triple(R.color.color_success, "Stop Proxy", R.color.color_error)
            CoreManager.State.STARTING -> Triple(R.color.color_warning, "Starting...", R.color.color_warning)
            CoreManager.State.STOPPING -> Triple(R.color.color_warning, "Stopping...", R.color.color_warning)
            CoreManager.State.CRASHED -> Triple(R.color.color_error, "Restart Proxy", R.color.color_success)
            CoreManager.State.STOPPED -> Triple(R.color.color_stopped, "Start Proxy", R.color.color_success)
        }

        tvStatusBadge.setBackgroundColor(ContextCompat.getColor(this, colorRes))
        btnToggleService.text = btnText
        btnToggleService.setBackgroundColor(ContextCompat.getColor(this, btnColorRes))
        btnToggleService.isEnabled = state != CoreManager.State.STOPPING && state != CoreManager.State.STARTING

        if (state == CoreManager.State.STOPPED) {
            tvPidUptime.text = "PID: -- | Uptime: 0.0s"
        }
    }

    private fun updateMetricsUi(m: CoreManager.CoreMetrics) {
        if (m.isRunning) {
            tvPidUptime.text = String.format(Locale.US, "PID: %d | Uptime: %.1fs", m.pid, m.uptimeSeconds)
        }
        tvMetricRamCache.text = String.format(Locale.US, "RAM: %d items (%.2f MB)", m.ramItems, m.ramMb)
        tvMetricHits.text = String.format(Locale.US, "Hits: RAM %d / Disk %d", m.ramHits, m.diskHits)
        tvMetricRequests.text = String.format(Locale.US, "Assets: %d | APIs: %d (Act: %d)", m.totalAssets, m.totalApis, m.activeApiCount)
        tvMetricReuse.text = String.format(Locale.US, "H2 Reuse: %d (%.1f%%)", m.reusedConnections, m.reuseRatePercent)
    }

    private fun renderLogs() {
        val logs = CoreManager.getLogs()
        if (logs.isNotEmpty()) {
            tvLogContent.text = logs.joinToString("\n")
            svLogs.post {
                svLogs.fullScroll(ScrollView.FOCUS_DOWN)
            }
        }
    }

    override fun onDestroy() {
        CoreManager.removeStateListener(stateListener)
        CoreManager.removeMetricsListener(metricsListener)
        CoreManager.removeLogListener(logListener)
        super.onDestroy()
    }
}
