package com.sagisawa.gbfaccelerator.ui

import android.Manifest
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.PowerManager
import android.provider.Settings
import android.util.Log
import android.view.View
import android.widget.AdapterView
import android.widget.ArrayAdapter
import android.widget.Button
import android.widget.ScrollView
import android.widget.Spinner
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import androidx.appcompat.widget.SwitchCompat
import androidx.core.content.ContextCompat
import com.sagisawa.gbfaccelerator.R
import com.sagisawa.gbfaccelerator.core.AppPreferences
import com.sagisawa.gbfaccelerator.core.CoreManager
import com.sagisawa.gbfaccelerator.core.CoreService
import com.sagisawa.gbfaccelerator.patch.BrowserLauncher
import com.sagisawa.gbfaccelerator.patch.BrowserTarget
import com.sagisawa.gbfaccelerator.patch.DefaultBrowserLauncher
import com.sagisawa.gbfaccelerator.patch.LaunchResult
import org.json.JSONObject
import java.io.File
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

    // 3. Cache Management & Maintenance Section
    private lateinit var tvCacheSummaryInline: TextView
    private lateinit var btnOpenCacheDir: Button
    private lateinit var btnSlimCache: Button
    private lateinit var btnClearCache: Button

    // 4. Performance & Cache Preferences Section
    private lateinit var switchRamCache: SwitchCompat
    private lateinit var switchPrefetch: SwitchCompat

    // 5. Metrics Section
    private lateinit var tvMetricRamCache: TextView
    private lateinit var tvMetricHits: TextView
    private lateinit var tvMetricRequests: TextView
    private lateinit var tvMetricReuse: TextView

    // 6. Logs Section
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
        updateCacheSummary()
        handleIntentAction(intent)

        if (intent?.getStringExtra("action") == null) {
            requestNotificationPermission()
        }
    }

    override fun onResume() {
        super.onResume()
        updateBrowserUi()
        updateCacheSummary()
        updatePreferencesUi()
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

        // 3. Cache Management & Maintenance Section
        tvCacheSummaryInline = findViewById(R.id.tv_cache_summary_inline)
        btnOpenCacheDir = findViewById(R.id.btn_open_cache_dir)
        btnSlimCache = findViewById(R.id.btn_slim_cache)
        btnClearCache = findViewById(R.id.btn_clear_cache)

        // 4. Performance & Cache Preferences Section
        switchRamCache = findViewById(R.id.switch_ram_cache)
        switchPrefetch = findViewById(R.id.switch_prefetch)

        // 5. Metrics Section
        tvMetricRamCache = findViewById(R.id.tv_metric_ram_cache)
        tvMetricHits = findViewById(R.id.tv_metric_hits)
        tvMetricRequests = findViewById(R.id.tv_metric_requests)
        tvMetricReuse = findViewById(R.id.tv_metric_reuse)

        // 6. Logs Section
        svLogs = findViewById(R.id.sv_logs)
        tvLogContent = findViewById(R.id.tv_log_content)
        tvClearLogs = findViewById(R.id.tv_clear_logs)

        setupServiceToggle()
        setupBrowserSelector()
        setupCacheActions()
        setupPreferences()

        tvClearLogs.setOnClickListener {
            tvLogContent.text = ""
        }
    }

    private fun updatePreferencesUi() {
        val ramPref = AppPreferences.isRamCacheEnabled(this)
        if (switchRamCache.isChecked != ramPref) {
            switchRamCache.isChecked = ramPref
        }
        val prefetchPref = AppPreferences.isPrefetchEnabled(this)
        if (switchPrefetch.isChecked != prefetchPref) {
            switchPrefetch.isChecked = prefetchPref
        }
    }

    private fun setupPreferences() {
        updatePreferencesUi()

        switchRamCache.setOnCheckedChangeListener { _, isChecked ->
            if (isChecked != AppPreferences.isRamCacheEnabled(this)) {
                AppPreferences.setRamCacheEnabled(this, isChecked)
                updateConfigJsonPreference("enable_ram_cache", isChecked)
                if (CoreManager.currentState == CoreManager.State.RUNNING) {
                    CoreManager.applyRuntimeConfig(mapOf("enable_ram_cache" to isChecked))
                }
            }
        }

        switchPrefetch.setOnCheckedChangeListener { _, isChecked ->
            if (isChecked != AppPreferences.isPrefetchEnabled(this)) {
                AppPreferences.setPrefetchEnabled(this, isChecked)
                updateConfigJsonPreference("enable_prefetch", isChecked)
                if (CoreManager.currentState == CoreManager.State.RUNNING) {
                    CoreManager.applyRuntimeConfig(mapOf("enable_prefetch" to isChecked))
                }
            }
        }
    }

    private fun updateConfigJsonPreference(key: String, value: Any) {
        try {
            val configFile = File(filesDir, "config.json")
            val json = if (configFile.exists()) {
                JSONObject(configFile.readText())
            } else {
                JSONObject()
            }
            json.put(key, value)
            configFile.writeText(json.toString(2))
        } catch (e: Throwable) {
            Log.w("MainActivity", "Failed to update config.json with $key=$value: ${e.message}")
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

    private fun getGbfCacheDir(): File {
        return File(filesDir, "cache/gbf/https")
    }

    private fun getDirStats(dir: File): Pair<Int, Long> {
        var count = 0
        var size = 0L
        if (dir.exists() && dir.isDirectory) {
            dir.walkTopDown().forEach { file ->
                if (file.isFile) {
                    count++
                    size += file.length()
                }
            }
        }
        return Pair(count, size)
    }

    private fun formatSize(bytes: Long): String {
        val mb = bytes.toDouble() / (1024 * 1024)
        return if (mb >= 1024) {
            String.format(Locale.US, "%.2f GB", mb / 1024.0)
        } else {
            String.format(Locale.US, "%.1f MB", mb)
        }
    }

    private fun updateCacheSummary() {
        Thread {
            val (count, sizeBytes) = getDirStats(getGbfCacheDir())
            val sizeStr = formatSize(sizeBytes)
            runOnUiThread {
                tvCacheSummaryInline.text = "本地缓存: $count 个文件 | 占用 $sizeStr"
            }
        }.start()
    }

    private fun setupCacheActions() {
        btnOpenCacheDir.setOnClickListener {
            val cacheDir = getGbfCacheDir()
            Thread {
                val (count, sizeBytes) = getDirStats(cacheDir)
                val sizeStr = formatSize(sizeBytes)
                val path = cacheDir.absolutePath

                runOnUiThread {
                    val summaryText = getString(R.string.cache_dir_summary, path, count, sizeStr)
                    AlertDialog.Builder(this)
                        .setTitle(R.string.cache_dir_dialog_title)
                        .setMessage(summaryText)
                        .setPositiveButton(R.string.cache_copy_path) { _, _ ->
                            val clipboard = getSystemService(Context.CLIPBOARD_SERVICE) as? android.content.ClipboardManager
                            val clip = android.content.ClipData.newPlainText("GBF Cache Path", path)
                            clipboard?.setPrimaryClip(clip)
                            Toast.makeText(this, "缓存路径已复制到剪贴板", Toast.LENGTH_SHORT).show()
                        }
                        .setNeutralButton(R.string.cache_export_download) { _, _ ->
                            exportCacheToDownload(cacheDir)
                        }
                        .setNegativeButton("关闭", null)
                        .show()
                }
            }.start()
        }

        btnSlimCache.setOnClickListener {
            if (CoreManager.currentState != CoreManager.State.RUNNING) {
                Toast.makeText(this, "请先启动代理核心后再执行瘦身", Toast.LENGTH_SHORT).show()
                return@setOnClickListener
            }

            AlertDialog.Builder(this)
                .setTitle(R.string.cache_slim_dialog_title)
                .setMessage(R.string.cache_slim_dialog_msg)
                .setPositiveButton(R.string.dialog_confirm) { _, _ ->
                    CoreManager.slimCache { success, msg ->
                        if (success) {
                            Toast.makeText(this, "瘦身任务已在后台执行", Toast.LENGTH_SHORT).show()
                            tvCacheSummaryInline.postDelayed({ updateCacheSummary() }, 3000)
                        } else {
                            Toast.makeText(this, "启动瘦身失败: $msg", Toast.LENGTH_SHORT).show()
                        }
                    }
                }
                .setNegativeButton(R.string.dialog_cancel, null)
                .show()
        }

        btnClearCache.setOnClickListener {
            AlertDialog.Builder(this)
                .setTitle(R.string.cache_clear_dialog_title)
                .setMessage(R.string.cache_clear_dialog_msg)
                .setPositiveButton(R.string.dialog_confirm) { _, _ ->
                    if (CoreManager.currentState == CoreManager.State.RUNNING) {
                        CoreManager.clearCache { success, deleted, freed ->
                            if (success) {
                                val freedStr = formatSize(freed)
                                Toast.makeText(this, "清空完成: 已删除 $deleted 个文件，释放 $freedStr", Toast.LENGTH_LONG).show()
                            } else {
                                Toast.makeText(this, "清空缓存失败，请检查核心日志", Toast.LENGTH_SHORT).show()
                            }
                            updateCacheSummary()
                        }
                    } else {
                        // Offline manual deletion
                        Thread {
                            val cacheDir = getGbfCacheDir()
                            var deleted = 0
                            var freed = 0L
                            if (cacheDir.exists()) {
                                cacheDir.walkBottomUp().forEach { f ->
                                    if (f.isFile) {
                                        deleted++
                                        freed += f.length()
                                        f.delete()
                                    } else if (f != cacheDir) {
                                        f.delete()
                                    }
                                }
                            }
                            val freedStr = formatSize(freed)
                            runOnUiThread {
                                Toast.makeText(this, "清空完成: 已删除 $deleted 个文件，释放 $freedStr", Toast.LENGTH_LONG).show()
                                updateCacheSummary()
                            }
                        }.start()
                    }
                }
                .setNegativeButton(R.string.dialog_cancel, null)
                .show()
        }
    }

    private fun exportCacheToDownload(cacheDir: File) {
        Thread {
            try {
                val downloadDir = android.os.Environment.getExternalStoragePublicDirectory(android.os.Environment.DIRECTORY_DOWNLOADS)
                val targetDir = File(downloadDir, "GBF_Cache")
                targetDir.mkdirs()

                var copied = 0
                if (cacheDir.exists()) {
                    cacheDir.copyRecursively(targetDir, overwrite = true) { _, _ ->
                        OnErrorAction.SKIP
                    }
                    val (count, _) = getDirStats(targetDir)
                    copied = count
                }

                runOnUiThread {
                    Toast.makeText(this, "成功导出 $copied 个缓存文件到: ${targetDir.absolutePath}", Toast.LENGTH_LONG).show()
                }
            } catch (e: Throwable) {
                runOnUiThread {
                    Toast.makeText(this, "导出缓存失败: ${e.message}", Toast.LENGTH_LONG).show()
                }
            }
        }.start()
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
        checkBatteryOptimizations()
    }

    private fun checkBatteryOptimizations() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            val powerManager = getSystemService(Context.POWER_SERVICE) as? PowerManager
            if (powerManager != null && !powerManager.isIgnoringBatteryOptimizations(packageName)) {
                try {
                    val intent = Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS).apply {
                        data = Uri.parse("package:$packageName")
                    }
                    startActivity(intent)
                } catch (e: Throwable) {
                    Log.w("MainActivity", "Cannot launch battery optimization settings: ${e.message}")
                }
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

        // Whenever Core reaches RUNNING or changes state, refresh cache summary
        updateCacheSummary()
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
