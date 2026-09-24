package com.sagisawa.gbfaccelerator.core

import android.content.Context
import android.os.Handler
import android.os.Looper
import android.util.Log
import org.json.JSONObject
import java.io.BufferedReader
import java.io.File
import java.io.InputStreamReader
import java.net.HttpURLConnection
import java.net.InetSocketAddress
import java.net.Socket
import java.net.URL
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger

object CoreManager {

    private const val TAG = "GBF-ACC-Manager"
    const val PROXY_PORT = 8124
    const val CONTROL_PORT = 8125

    enum class State {
        STOPPED,
        STARTING,
        RUNNING,
        CRASHED,
        STOPPING
    }

    data class CoreMetrics(
        val isRunning: Boolean = false,
        val pid: Int = -1,
        val uptimeSeconds: Double = 0.0,
        val ramItems: Int = 0,
        val ramMb: Double = 0.0,
        val ramHits: Long = 0,
        val diskHits: Long = 0,
        val totalAssets: Long = 0,
        val totalApis: Long = 0,
        val reusedConnections: Long = 0,
        val reuseRatePercent: Double = 0.0,
        val activeApiCount: Int = 0,
        val lastError: String = ""
    )

    private val executor = Executors.newCachedThreadPool()
    private val mainHandler = Handler(Looper.getMainLooper())

    @Volatile
    var currentState: State = State.STOPPED
        private set

    @Volatile
    var currentMetrics: CoreMetrics = CoreMetrics()
        private set

    private var coreProcess: Process? = null
    private val isStoppingIntentionally = AtomicBoolean(false)
    private val recentRestarts = AtomicInteger(0)
    private var lastRestartTimestamp = 0L

    private val logBuffer = CopyOnWriteArrayList<String>()
    private const val MAX_LOG_LINES = 100

    private val stateListeners = CopyOnWriteArrayList<(State) -> Unit>()
    private val metricsListeners = CopyOnWriteArrayList<(CoreMetrics) -> Unit>()
    private val logListeners = CopyOnWriteArrayList<(String) -> Unit>()

    fun addStateListener(listener: (State) -> Unit) {
        stateListeners.add(listener)
        mainHandler.post { listener(currentState) }
    }

    fun removeStateListener(listener: (State) -> Unit) {
        stateListeners.remove(listener)
    }

    fun addMetricsListener(listener: (CoreMetrics) -> Unit) {
        metricsListeners.add(listener)
        mainHandler.post { listener(currentMetrics) }
    }

    fun removeMetricsListener(listener: (CoreMetrics) -> Unit) {
        metricsListeners.remove(listener)
    }

    fun addLogListener(listener: (String) -> Unit) {
        logListeners.add(listener)
    }

    fun removeLogListener(listener: (String) -> Unit) {
        logListeners.remove(listener)
    }

    fun getLogs(): List<String> = logBuffer.toList()

    private fun appendLog(line: String) {
        val trimmed = line.trim()
        if (trimmed.isEmpty()) return

        Log.i(TAG, "[Core] $trimmed")
        if (logBuffer.size >= MAX_LOG_LINES) {
            logBuffer.removeAt(0)
        }
        logBuffer.add(trimmed)
        mainHandler.post {
            for (listener in logListeners) {
                listener(trimmed)
            }
        }
    }

    private fun setState(state: State) {
        currentState = state
        Log.i(TAG, "[State] Changed to $state")
        mainHandler.post {
            for (listener in stateListeners) {
                listener(state)
            }
        }
    }

    fun isPortReachable(port: Int, timeoutMs: Int = 200): Boolean {
        return try {
            Socket().use { socket ->
                socket.connect(InetSocketAddress("127.0.0.1", port), timeoutMs)
                true
            }
        } catch (_: Throwable) {
            false
        }
    }

    @Synchronized
    fun startCore(context: Context) {
        if (currentState == State.RUNNING || currentState == State.STARTING) {
            Log.w(TAG, "Core is already running or starting")
            return
        }

        setState(State.STARTING)
        isStoppingIntentionally.set(false)

        executor.execute {
            val coreBinary = File(context.applicationInfo.nativeLibraryDir, "libgbfcore.so")
            if (!coreBinary.exists()) {
                val err = "libgbfcore.so not found in ${context.applicationInfo.nativeLibraryDir}"
                Log.e(TAG, err)
                appendLog("[-] $err")
                setState(State.CRASHED)
                return@execute
            }

            val baseDir = context.filesDir
            val configPath = File(baseDir, "config.json")
            val cacheDir = File(baseDir, "cache/gbf/https")
            cacheDir.mkdirs()

            // Verify if ports 8124/8125 are already occupied by a rogue instance
            if (isPortReachable(CONTROL_PORT)) {
                appendLog("[*] Control port $CONTROL_PORT is already responsive, querying status...")
                val status = queryStatusDirect()
                if (status != null && status.isRunning) {
                    appendLog("[+] Existing Go Core instance detected and healthy!")
                    currentMetrics = status
                    setState(State.RUNNING)
                    startStatusPollingLoop()
                    return@execute
                }
            }

            appendLog("[+] Launching Go Core daemon: ${coreBinary.absolutePath}")
            val cmd = listOf(
                coreBinary.absolutePath,
                "-nogui",
                "-direct-mode",
                "-base-dir", baseDir.absolutePath,
                "-proxy-port", PROXY_PORT.toString(),
                "-control-port", CONTROL_PORT.toString(),
                "-config", configPath.absolutePath,
                "-cache-dir", cacheDir.absolutePath
            )

            try {
                val pb = ProcessBuilder(cmd)
                pb.directory(baseDir)
                pb.environment()["HOME"] = baseDir.absolutePath
                pb.redirectErrorStream(true)

                val proc = pb.start()
                coreProcess = proc

                // Drain output
                executor.execute {
                    runCatching {
                        BufferedReader(InputStreamReader(proc.inputStream)).use { reader ->
                            var line: String?
                            while (reader.readLine().also { line = it } != null) {
                                line?.let { appendLog(it) }
                            }
                        }
                    }
                }

                // Process exit monitor
                executor.execute {
                    val exitCode = runCatching { proc.waitFor() }.getOrDefault(-1)
                    coreProcess = null
                    appendLog("[*] Go Core process exited with code $exitCode")

                    if (!isStoppingIntentionally.get()) {
                        Log.w(TAG, "Go Core crashed unexpectedly (exit $exitCode)")
                        setState(State.CRASHED)
                        handleUnexpectedExit(context)
                    } else {
                        setState(State.STOPPED)
                    }
                }

                // Wait for Core to start listening on 8125 / 8124
                var ready = false
                val startWait = System.currentTimeMillis()
                while (System.currentTimeMillis() - startWait < 5000) {
                    if (isPortReachable(PROXY_PORT, 200) && isPortReachable(CONTROL_PORT, 200)) {
                        val status = queryStatusDirect()
                        if (status != null) {
                            currentMetrics = status
                        }
                        ready = true
                        break
                    }
                    Thread.sleep(150)
                }

                if (ready) {
                    appendLog("[+] Go Core successfully listening on 127.0.0.1:$PROXY_PORT / $CONTROL_PORT")
                    setState(State.RUNNING)
                    startStatusPollingLoop()
                } else {
                    val err = "Go Core did not become ready within 5s"
                    appendLog("[-] $err")
                    stopCore()
                    setState(State.CRASHED)
                }
            } catch (e: Throwable) {
                val err = "Failed to spawn Go Core: ${e.message}"
                Log.e(TAG, err, e)
                appendLog("[-] $err")
                setState(State.CRASHED)
            }
        }
    }

    private fun handleUnexpectedExit(context: Context) {
        val now = System.currentTimeMillis()
        if (now - lastRestartTimestamp > 60000) {
            recentRestarts.set(0)
        }
        lastRestartTimestamp = now

        val restarts = recentRestarts.incrementAndGet()
        if (restarts <= 3) {
            appendLog("[!] Unexpected crash detected. Attempting auto-recovery ($restarts/3) in 1s...")
            mainHandler.postDelayed({
                startCore(context)
            }, 1000)
        } else {
            appendLog("[-] Max auto-recovery restarts reached (3). Halting to prevent spin loop.")
            setState(State.CRASHED)
        }
    }

    @Synchronized
    fun stopCore() {
        if (currentState == State.STOPPED) return

        setState(State.STOPPING)
        isStoppingIntentionally.set(true)

        executor.execute {
            appendLog("[*] Stopping Go Core daemon...")

            // 1. Try graceful shutdown via HTTP signal
            runCatching {
                val url = URL("http://127.0.0.1:$CONTROL_PORT/api/app/quit")
                val conn = url.openConnection() as HttpURLConnection
                conn.connectTimeout = 500
                conn.readTimeout = 500
                conn.requestMethod = "POST"
                conn.responseCode
                conn.disconnect()
            }

            // 2. Wait up to 1.5s for process to exit
            val proc = coreProcess
            if (proc != null) {
                var exited = false
                val start = System.currentTimeMillis()
                while (System.currentTimeMillis() - start < 1500) {
                    if (!proc.isAlive) {
                        exited = true
                        break
                    }
                    Thread.sleep(100)
                }

                if (!exited) {
                    appendLog("[*] Force terminating Go Core process...")
                    runCatching { proc.destroyForcibly() }
                }
            }

            coreProcess = null
            appendLog("[+] Go Core stopped. Ports 8124/8125 released.")
            setState(State.STOPPED)
        }
    }

    private fun startStatusPollingLoop() {
        executor.execute {
            while (currentState == State.RUNNING) {
                val status = queryStatusDirect()
                if (status != null) {
                    currentMetrics = status
                    mainHandler.post {
                        for (listener in metricsListeners) {
                            listener(status)
                        }
                    }
                }
                Thread.sleep(1500)
            }
        }
    }

    private fun queryStatusDirect(): CoreMetrics? {
        return try {
            val url = URL("http://127.0.0.1:$CONTROL_PORT/api/status")
            val conn = url.openConnection() as HttpURLConnection
            conn.connectTimeout = 800
            conn.readTimeout = 800
            conn.requestMethod = "GET"

            if (conn.responseCode == 200) {
                val resp = conn.inputStream.bufferedReader().use { it.readText() }
                conn.disconnect()

                val json = JSONObject(resp)
                val cacheObj = json.optJSONObject("cache")
                val reqObj = json.optJSONObject("requests")
                val telemObj = json.optJSONObject("telemetry")

                CoreMetrics(
                    isRunning = json.optBoolean("proxy_running", false),
                    pid = json.optInt("pid", -1),
                    uptimeSeconds = json.optDouble("uptime_seconds", 0.0),
                    ramItems = cacheObj?.optInt("ram_items", 0) ?: 0,
                    ramMb = cacheObj?.optDouble("ram_mb", 0.0) ?: 0.0,
                    ramHits = reqObj?.optLong("ram_hits", 0L) ?: 0L,
                    diskHits = reqObj?.optLong("disk_hits", 0L) ?: 0L,
                    totalAssets = reqObj?.optLong("total_assets", 0L) ?: 0L,
                    totalApis = reqObj?.optLong("total_apis", 0L) ?: 0L,
                    reusedConnections = telemObj?.optLong("reused_connections", 0L) ?: 0L,
                    reuseRatePercent = telemObj?.optDouble("reuse_rate_percent", 0.0) ?: 0.0,
                    activeApiCount = json.optInt("active_api_count", 0),
                    lastError = json.optString("last_error", "")
                )
            } else {
                conn.disconnect()
                null
            }
        } catch (e: Throwable) {
            Log.w(TAG, "queryStatusDirect exception: ${e.message}")
            null
        }
    }
}
