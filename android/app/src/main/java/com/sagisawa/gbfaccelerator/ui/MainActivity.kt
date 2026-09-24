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
import com.sagisawa.gbfaccelerator.R
import com.sagisawa.gbfaccelerator.cert.CaCertManager
import com.sagisawa.gbfaccelerator.core.CoreManager
import com.sagisawa.gbfaccelerator.core.CoreService
import com.sagisawa.gbfaccelerator.patch.BrowserLauncher
import com.sagisawa.gbfaccelerator.patch.BrowserTarget
import com.sagisawa.gbfaccelerator.patch.DefaultBrowserLauncher
import com.sagisawa.gbfaccelerator.patch.LaunchResult
import java.util.Locale

class MainActivity : AppCompatActivity() {

    // 1. Status Section
    private lateinit var tvStatusBadge: TextView
    private lateinit var tvProxyBadge: TextView
    private lateinit var tvProxyDetail: TextView
    private lateinit var tvPidUptime: TextView
    private lateinit var tvPorts: TextView
    private lateinit var btnToggleService: Button

    // 2. Browser Management Section
    private lateinit var spTargetBrowser: Spinner
    private lateinit var tvBrowserStatus: TextView
    private lateinit var btnLaunchBrowser: Button

    // 3. CA Certificate Section
    private lateinit var tvCertStatusBadge: TextView
    private lateinit var tvCertFingerprint: TextView
    private lateinit var tvCertTrust: TextView
    private lateinit var btnInstallCert: Button
    private lateinit var btnExportCert: Button
    private lateinit var btnSecuritySettings: Button

    // 4. Metrics Section
    private lateinit var tvMetricRamCache: TextView
    private lateinit var tvMetricHits: TextView
    private lateinit var tvMetricRequests: TextView
    private lateinit var tvMetricReuse: TextView

    // 5. Logs Section
    private lateinit var svLogs: ScrollView
    private lateinit var tvLogContent: TextView
    private lateinit var tvClearLogs: TextView

    private val browserLauncher: BrowserLauncher = DefaultBrowserLauncher()
    private val availableTargets: List<BrowserTarget> = listOf(
        BrowserTarget(id = "skyleap", name = "SkyLeap", targetPackages = setOf("com.dena.skyleap"))
    )
    private var selectedTarget: BrowserTarget? = availableTargets.firstOrNull()

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
        updateCertUi()
        handleIntentAction(intent)

        if (intent?.getStringExtra("action") == null) {
            requestNotificationPermission()
        }
    }

    override fun onResume() {
        super.onResume()
        updateBrowserUi()
        updateCertUi()
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
                selectedTarget?.let { target ->
                    launchTargetBrowser(target)
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

        // 3. CA Certificate Section
        tvCertStatusBadge = findViewById(R.id.tv_cert_status_badge)
        tvCertFingerprint = findViewById(R.id.tv_cert_fingerprint)
        tvCertTrust = findViewById(R.id.tv_cert_trust)
        btnInstallCert = findViewById(R.id.btn_install_cert)
        btnExportCert = findViewById(R.id.btn_export_cert)
        btnSecuritySettings = findViewById(R.id.btn_security_settings)

        // 4. Metrics Section
        tvMetricRamCache = findViewById(R.id.tv_metric_ram_cache)
        tvMetricHits = findViewById(R.id.tv_metric_hits)
        tvMetricRequests = findViewById(R.id.tv_metric_requests)
        tvMetricReuse = findViewById(R.id.tv_metric_reuse)

        // 5. Logs Section
        svLogs = findViewById(R.id.sv_logs)
        tvLogContent = findViewById(R.id.tv_log_content)
        tvClearLogs = findViewById(R.id.tv_clear_logs)

        setupServiceToggle()
        setupBrowserSelector()
        setupCertActions()

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
        val targetLabels = availableTargets.map { target ->
            "${target.name} (${target.id})"
        }

        val spinnerAdapter = ArrayAdapter(
            this,
            android.R.layout.simple_spinner_dropdown_item,
            targetLabels
        )
        spTargetBrowser.adapter = spinnerAdapter

        spTargetBrowser.onItemSelectedListener = object : AdapterView.OnItemSelectedListener {
            override fun onItemSelected(parent: AdapterView<*>?, view: View?, position: Int, id: Long) {
                if (position in availableTargets.indices) {
                    selectedTarget = availableTargets[position]
                    updateBrowserUi()
                }
            }

            override fun onNothingSelected(parent: AdapterView<*>?) {
                selectedTarget = null
                updateBrowserUi()
            }
        }

        if (availableTargets.isNotEmpty()) {
            selectedTarget = availableTargets.first()
            spTargetBrowser.setSelection(0)
            updateBrowserUi()
        }

        btnLaunchBrowser.setOnClickListener {
            selectedTarget?.let { target ->
                launchTargetBrowser(target)
            }
        }
    }

    private fun setupCertActions() {
        btnInstallCert.setOnClickListener {
            val intent = CaCertManager.createInstallIntent(this)
            if (intent != null) {
                try {
                    startActivity(intent)
                } catch (e: Exception) {
                    Toast.makeText(this, "启动证书安装器失败: ${e.message}", Toast.LENGTH_SHORT).show()
                }
            } else {
                Toast.makeText(this, "证书尚未生成，请先启动 Go Core", Toast.LENGTH_SHORT).show()
            }
        }

        btnExportCert.setOnClickListener {
            val (success, msg) = CaCertManager.exportCaCertToDownloads(this)
            Toast.makeText(this, msg, Toast.LENGTH_LONG).show()
            updateCertUi()
        }

        btnSecuritySettings.setOnClickListener {
            try {
                startActivity(CaCertManager.createSecuritySettingsIntent())
            } catch (e: Exception) {
                Toast.makeText(this, "无法打开系统安全设置: ${e.message}", Toast.LENGTH_SHORT).show()
            }
        }
    }

    private fun updateBrowserUi() {
        val target = selectedTarget ?: return
        val appInfo = browserLauncher.getAppInfo(this, target)

        if (appInfo.isInstalled) {
            val vName = appInfo.versionName ?: "detected"
            tvBrowserStatus.text = String.format(
                Locale.US,
                "Package: %s | %s",
                appInfo.packageName,
                getString(R.string.browser_status_installed, vName)
            )
            btnLaunchBrowser.isEnabled = true
            btnLaunchBrowser.text = String.format(Locale.US, "启动 %s", target.name)
        } else {
            tvBrowserStatus.text = String.format(
                Locale.US,
                "Package: %s | %s",
                appInfo.packageName,
                getString(R.string.browser_status_not_installed)
            )
            btnLaunchBrowser.isEnabled = false
            btnLaunchBrowser.text = String.format(Locale.US, "未安装 %s", target.name)
        }
    }

    private fun updateCertUi() {
        val certInfo = CaCertManager.getCertInfo(this)
        if (certInfo.exists) {
            tvCertStatusBadge.text = getString(R.string.cert_status_ready)
            tvCertStatusBadge.setBackgroundColor(ContextCompat.getColor(this, R.color.color_success))
            tvCertFingerprint.text = String.format(Locale.US, "SHA-256: %s", certInfo.sha256Fingerprint)

            if (certInfo.isTrustedInSystem) {
                tvCertTrust.text = getString(R.string.cert_trust_trusted)
                tvCertTrust.setTextColor(ContextCompat.getColor(this, R.color.color_success))
            } else {
                tvCertTrust.text = getString(R.string.cert_trust_untrusted)
                tvCertTrust.setTextColor(ContextCompat.getColor(this, R.color.color_warning))
            }

            btnInstallCert.isEnabled = true
            btnExportCert.isEnabled = true
        } else {
            tvCertStatusBadge.text = getString(R.string.cert_status_not_ready)
            tvCertStatusBadge.setBackgroundColor(ContextCompat.getColor(this, R.color.color_stopped))
            tvCertFingerprint.text = "SHA-256: -- (启动核心后就绪)"
            tvCertTrust.text = getString(R.string.cert_trust_untrusted)
            tvCertTrust.setTextColor(ContextCompat.getColor(this, R.color.text_secondary))

            btnInstallCert.isEnabled = false
            btnExportCert.isEnabled = false
        }
    }

    private fun launchTargetBrowser(target: BrowserTarget) {
        when (val result = browserLauncher.launch(this, target)) {
            is LaunchResult.Success -> {
                // Browser launched successfully
            }
            is LaunchResult.NotInstalled -> {
                Toast.makeText(
                    this,
                    String.format(Locale.US, "%s (%s) 未安装", target.name, result.packageName),
                    Toast.LENGTH_SHORT
                ).show()
            }
            is LaunchResult.Failure -> {
                Toast.makeText(
                    this,
                    String.format(Locale.US, "启动失败: %s", result.reason),
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
            CoreManager.State.STARTING -> Triple(R.color.color_warning, "启动中...", R.color.color_warning)
            CoreManager.State.STOPPING -> Triple(R.color.color_warning, "停止中...", R.color.color_warning)
            CoreManager.State.CRASHED -> Triple(R.color.color_error, "重启代理核心", R.color.color_success)
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
            tvPidUptime.text = "PID: -- | 运行时间: 0.0s"
        }

        // Whenever Core reaches RUNNING or changes state, refresh cert status in case ca.crt was generated
        updateCertUi()
    }

    private fun updateMetricsUi(m: CoreManager.CoreMetrics) {
        if (m.isRunning) {
            tvPidUptime.text = String.format(Locale.US, "PID: %d | 运行时间: %.1fs", m.pid, m.uptimeSeconds)
        }
        tvMetricRamCache.text = String.format(Locale.US, "RAM: %d 条目 (%.2f MB)", m.ramItems, m.ramMb)
        tvMetricHits.text = String.format(Locale.US, "命中: RAM %d / 磁盘 %d", m.ramHits, m.diskHits)
        tvMetricRequests.text = String.format(Locale.US, "静态: %d | API: %d (活跃: %d)", m.totalAssets, m.totalApis, m.activeApiCount)
        tvMetricReuse.text = String.format(Locale.US, "H2 复用: %d (%.1f%%)", m.reusedConnections, m.reuseRatePercent)
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
