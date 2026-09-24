package com.sagisawa.gbfaccelerator.ui

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import android.view.View
import android.widget.AdapterView
import android.widget.ArrayAdapter
import android.widget.Button
import android.widget.ScrollView
import android.widget.Spinner
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AppCompatActivity
import androidx.core.content.ContextCompat
import com.sagisawa.gbfaccelerator.browser.BrowserAdapter
import com.sagisawa.gbfaccelerator.browser.BrowserAdapterRegistry
import com.sagisawa.gbfaccelerator.core.CoreManager
import com.sagisawa.gbfaccelerator.core.CoreService
import com.sagisawa.gbfaccelerator.patch.BrowserLauncher
import com.sagisawa.gbfaccelerator.patch.DefaultBrowserLauncher
import com.sagisawa.gbfaccelerator.patch.LaunchResult
import com.sagisawa.gbfaccelerator.xposed.R
import java.util.Locale

class MainActivity : AppCompatActivity() {

    private lateinit var tvStatusBadge: TextView
    private lateinit var tvProxyBadge: TextView
    private lateinit var tvProxyDetail: TextView
    private lateinit var tvPidUptime: TextView
    private lateinit var tvPorts: TextView
    private lateinit var btnToggleService: Button

    private lateinit var spTargetBrowser: Spinner
    private lateinit var tvBrowserStatus: TextView
    private lateinit var btnLaunchBrowser: Button

    private lateinit var tvMetricRamCache: TextView
    private lateinit var tvMetricHits: TextView
    private lateinit var tvMetricRequests: TextView
    private lateinit var tvMetricReuse: TextView

    private lateinit var svLogs: ScrollView
    private lateinit var tvLogContent: TextView
    private lateinit var tvClearLogs: TextView

    private val browserLauncher: BrowserLauncher = DefaultBrowserLauncher()
    private var availableAdapters: List<BrowserAdapter> = emptyList()
    private var selectedAdapter: BrowserAdapter? = null

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

    override fun onResume() {
        super.onResume()
        updateBrowserUi()
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
            "launch_browser" -> {
                selectedAdapter?.let { adapter ->
                    launchTargetBrowser(adapter)
                }
            }
        }
    }

    private fun initViews() {
        // 1. Status Section
        tvStatusBadge = findViewById(R.id.tv_status_badge)
        tvProxyBadge = findViewById(R.id.tv_proxy_badge)
        tvProxyDetail = findViewById(R.id.tv_proxy_detail)
        tvPidUptime = findViewById(R.id.tv_pid_uptime)
        tvPorts = findViewById(R.id.tv_ports)
        btnToggleService = findViewById(R.id.btn_toggle_service)

        // 2. Browser Management Section
        spTargetBrowser = findViewById(R.id.sp_target_browser)
        tvBrowserStatus = findViewById(R.id.tv_browser_status)
        btnLaunchBrowser = findViewById(R.id.btn_launch_browser)

        // 3. Metrics Section
        tvMetricRamCache = findViewById(R.id.tv_metric_ram_cache)
        tvMetricHits = findViewById(R.id.tv_metric_hits)
        tvMetricRequests = findViewById(R.id.tv_metric_requests)
        tvMetricReuse = findViewById(R.id.tv_metric_reuse)

        // 4. Logs Section
        svLogs = findViewById(R.id.sv_logs)
        tvLogContent = findViewById(R.id.tv_log_content)
        tvClearLogs = findViewById(R.id.tv_clear_logs)

        setupServiceToggle()
        setupBrowserSelector()

        tvClearLogs.setOnClickListener {
            tvLogContent.text = ""
        }
    }

    private fun setupServiceToggle() {
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
    }

    private fun setupBrowserSelector() {
        availableAdapters = BrowserAdapterRegistry.getAllAdapters()
        val adapterLabels = availableAdapters.map { adapter ->
            "${adapter.name} (${adapter.id})"
        }

        val spinnerAdapter = ArrayAdapter(
            this,
            android.R.layout.simple_spinner_dropdown_item,
            adapterLabels
        )
        spTargetBrowser.adapter = spinnerAdapter

        spTargetBrowser.onItemSelectedListener = object : AdapterView.OnItemSelectedListener {
            override fun onItemSelected(parent: AdapterView<*>?, view: View?, position: Int, id: Long) {
                if (position in availableAdapters.indices) {
                    selectedAdapter = availableAdapters[position]
                    updateBrowserUi()
                }
            }

            override fun onNothingSelected(parent: AdapterView<*>?) {
                selectedAdapter = null
                updateBrowserUi()
            }
        }

        if (availableAdapters.isNotEmpty()) {
            selectedAdapter = availableAdapters.first()
            spTargetBrowser.setSelection(0)
            updateBrowserUi()
        }

        btnLaunchBrowser.setOnClickListener {
            selectedAdapter?.let { adapter ->
                launchTargetBrowser(adapter)
            }
        }
    }

    private fun updateBrowserUi() {
        val adapter = selectedAdapter ?: return
        val appInfo = browserLauncher.getAppInfo(this, adapter)

        if (appInfo.isInstalled) {
            val vName = appInfo.versionName ?: "detected"
            tvBrowserStatus.text = String.format(
                Locale.US,
                "Package: %s | %s",
                appInfo.packageName,
                getString(R.string.browser_status_installed, vName)
            )
            btnLaunchBrowser.isEnabled = true
            btnLaunchBrowser.text = String.format(Locale.US, "Launch %s", adapter.name)
        } else {
            tvBrowserStatus.text = String.format(
                Locale.US,
                "Package: %s | %s",
                appInfo.packageName,
                getString(R.string.browser_status_not_installed)
            )
            btnLaunchBrowser.isEnabled = false
            btnLaunchBrowser.text = String.format(Locale.US, "%s Not Installed", adapter.name)
        }
    }

    private fun launchTargetBrowser(adapter: BrowserAdapter) {
        when (val result = browserLauncher.launch(this, adapter)) {
            is LaunchResult.Success -> {
                // Browser launched successfully
            }
            is LaunchResult.NotInstalled -> {
                Toast.makeText(
                    this,
                    String.format(Locale.US, "%s (%s) is not installed", adapter.name, result.packageName),
                    Toast.LENGTH_SHORT
                ).show()
            }
            is LaunchResult.Failure -> {
                Toast.makeText(
                    this,
                    String.format(Locale.US, "Failed to launch: %s", result.reason),
                    Toast.LENGTH_LONG
                ).show()
            }
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
            CoreManager.State.RUNNING -> Triple(R.color.color_success, getString(R.string.btn_stop_proxy), R.color.color_error)
            CoreManager.State.STARTING -> Triple(R.color.color_warning, "Starting...", R.color.color_warning)
            CoreManager.State.STOPPING -> Triple(R.color.color_warning, "Stopping...", R.color.color_warning)
            CoreManager.State.CRASHED -> Triple(R.color.color_error, "Restart Proxy", R.color.color_success)
            CoreManager.State.STOPPED -> Triple(R.color.color_stopped, getString(R.string.btn_start_proxy), R.color.color_success)
        }

        tvStatusBadge.setBackgroundColor(ContextCompat.getColor(this, colorRes))
        btnToggleService.text = btnText
        btnToggleService.setBackgroundColor(ContextCompat.getColor(this, btnColorRes))
        btnToggleService.isEnabled = state != CoreManager.State.STOPPING && state != CoreManager.State.STARTING

        // Update GBF Proxy Status badge and detail
        when (state) {
            CoreManager.State.RUNNING -> {
                tvProxyBadge.text = getString(R.string.proxy_normal)
                tvProxyBadge.setBackgroundColor(ContextCompat.getColor(this, R.color.color_proxy_ok))
                tvProxyDetail.text = getString(R.string.proxy_detail_normal)
            }
            CoreManager.State.CRASHED -> {
                tvProxyBadge.text = getString(R.string.proxy_crashed)
                tvProxyBadge.setBackgroundColor(ContextCompat.getColor(this, R.color.color_error))
                tvProxyDetail.text = getString(R.string.proxy_detail_unavailable)
            }
            else -> {
                tvProxyBadge.text = getString(R.string.proxy_unavailable)
                tvProxyBadge.setBackgroundColor(ContextCompat.getColor(this, R.color.color_proxy_off))
                tvProxyDetail.text = getString(R.string.proxy_detail_unavailable)
            }
        }

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
