package com.sagisawa.gbfaccelerator.core

import android.app.Application
import android.content.Context
import android.os.Build
import android.util.Log
import org.json.JSONObject
import java.io.BufferedReader
import java.io.File
import java.io.FileOutputStream
import java.io.InputStreamReader
import java.net.HttpURLConnection
import java.net.InetSocketAddress
import java.net.Socket
import java.net.URL
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean
import java.util.zip.ZipFile
import java.util.zip.ZipInputStream

/**
 * Manages the self-contained Go Core daemon (libgbfcore.so) embedded directly inside
 * the All-in-One browser module.
 *
 * Responsibilities:
 * - Detects the main browser process and avoids multi-process duplication.
 * - Extracts libgbfcore.so from nativeLibraryDir or APK container to private app storage.
 * - Initializes clean zero-visual-clutter configuration (RAM cache off, prefetch off).
 * - Spawns and manages daemon lifecycle (127.0.0.1:8124 / 8125).
 */
object EmbeddedCoreManager {

    private const val TAG = "GBF-ACC-EmbeddedCore"
    const val PROXY_PORT = 8124
    const val CONTROL_PORT = 8125

    private val isStarted = AtomicBoolean(false)
    private val isStarting = AtomicBoolean(false)
    private val executor = Executors.newCachedThreadPool()

    @Volatile
    private var coreProcess: Process? = null

    /**
     * Determines whether the current execution context is the primary/main application process.
     */
    fun isMainProcess(context: Context, explicitProcessName: String? = null): Boolean {
        val packageName = context.packageName
        if (!explicitProcessName.isNullOrEmpty() && explicitProcessName != "unknown") {
            return explicitProcessName == packageName
        }

        // Android 9+ (API 28+) canonical API
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            val procName = Application.getProcessName()
            if (!procName.isNullOrEmpty()) {
                return procName == packageName
            }
        }

        // Fallback: query /proc/self/cmdline
        try {
            val cmdline = File("/proc/self/cmdline").readText().trim { it <= ' ' || it == '\u0000' }
            if (cmdline.isNotEmpty()) {
                return cmdline == packageName
            }
        } catch (_: Throwable) {
            // Quiet ignore
        }

        // Secondary fallback via reflection
        try {
            val activityThread = Class.forName("android.app.ActivityThread")
            val currentProcessNameMethod = activityThread.getMethod("currentProcessName")
            val name = currentProcessNameMethod.invoke(null) as? String
            if (!name.isNullOrEmpty()) {
                return name == packageName
            }
        } catch (_: Throwable) {
            // Quiet ignore
        }

        return true
    }

    /**
     * Probes whether a local TCP port is accepting connections.
     */
    fun isPortReachable(port: Int, timeoutMs: Int = 150): Boolean {
        return try {
            Socket().use { socket ->
                socket.connect(InetSocketAddress("127.0.0.1", port), timeoutMs)
                true
            }
        } catch (_: Throwable) {
            false
        }
    }

    /**
     * Triggers asynchronous start of the embedded Go Core daemon if in the main process.
     */
    fun ensureStarted(context: Context, processName: String? = null) {
        if (!isMainProcess(context, processName)) {
            Log.d(TAG, "[GBF-ACC] Non-main process detected; skipping embedded Go Core start")
            return
        }

        if (isStarted.get() || !isStarting.compareAndSet(false, true)) {
            return
        }

        executor.execute {
            try {
                startCoreInternal(context)
            } finally {
                isStarting.set(false)
            }
        }
    }

    private fun startCoreInternal(context: Context) {
        Log.i(TAG, "==================================================")
        Log.i(TAG, "[GBF-ACC] Initializing Embedded Go Core daemon...")

        // 1. Check if ports are already serviced by a healthy instance
        if (isPortReachable(CONTROL_PORT) && isPortReachable(PROXY_PORT)) {
            Log.i(TAG, "[GBF-ACC] Existing Go Core daemon detected on 127.0.0.1:$PROXY_PORT / $CONTROL_PORT")
            isStarted.set(true)
            return
        }

        // 2. Resolve or extract executable libgbfcore.so
        val coreFile = resolveCoreBinary(context)
        if (coreFile == null || !coreFile.exists()) {
            Log.e(TAG, "[GBF-ACC] Unable to locate or extract libgbfcore.so")
            return
        }

        coreFile.setExecutable(true, false)

        // 3. Prepare private working directory and cache directory
        val baseDir = File(context.filesDir, "gbf_core")
        baseDir.mkdirs()
        val cacheDir = File(context.cacheDir, "gbf_cache")
        cacheDir.mkdirs()
        val configFile = File(baseDir, "config.json")

        // Preset policy: RAM cache off, prefetch off, direct connection outbound
        try {
            val json = if (configFile.exists()) {
                runCatching { JSONObject(configFile.readText()) }.getOrDefault(JSONObject())
            } else {
                JSONObject()
            }
            json.put("listen_host", "127.0.0.1")
            json.put("listen_port", PROXY_PORT)
            json.put("control_port", CONTROL_PORT)
            json.put("allow_lan", false)
            json.put("enable_ram_cache", false)
            json.put("enable_prefetch", false)
            configFile.writeText(json.toString(2))
        } catch (e: Throwable) {
            Log.w(TAG, "[GBF-ACC] Failed to write config.json: ${e.message}")
        }

        val cmd = listOf(
            coreFile.absolutePath,
            "-nogui",
            "-base-dir", baseDir.absolutePath,
            "-proxy-port", PROXY_PORT.toString(),
            "-control-port", CONTROL_PORT.toString(),
            "-config", configFile.absolutePath,
            "-cache-dir", cacheDir.absolutePath,
            "-enable-ram-cache=false",
            "-enable-prefetch=false"
        )

        Log.i(TAG, "[GBF-ACC] Spawning Go Core: ${cmd.joinToString(" ")}")

        try {
            val pb = ProcessBuilder(cmd)
            pb.directory(baseDir)
            pb.environment()["HOME"] = baseDir.absolutePath
            pb.redirectErrorStream(true)

            val proc = pb.start()
            coreProcess = proc

            // Drain output to logcat
            executor.execute {
                runCatching {
                    BufferedReader(InputStreamReader(proc.inputStream)).use { reader ->
                        var line: String?
                        while (reader.readLine().also { line = it } != null) {
                            line?.let { Log.i(TAG, "[Core] $it") }
                        }
                    }
                }
            }

            // Register JVM shutdown hook for clean termination
            Runtime.getRuntime().addShutdownHook(Thread {
                runCatching {
                    Log.i(TAG, "[GBF-ACC] Process exiting; stopping embedded Go Core")
                    proc.destroy()
                }
            })

            // Poll for listening state
            val startWait = System.currentTimeMillis()
            var ready = false
            while (System.currentTimeMillis() - startWait < 5000) {
                if (isPortReachable(PROXY_PORT, 150) && isPortReachable(CONTROL_PORT, 150)) {
                    ready = true
                    break
                }
                Thread.sleep(150)
            }

            if (ready) {
                isStarted.set(true)
                Log.i(TAG, "[GBF-ACC] Embedded Go Core successfully listening on 127.0.0.1:$PROXY_PORT / $CONTROL_PORT")
                Log.i(TAG, "[GBF-ACC] Web console accessible at http://127.0.0.1:$CONTROL_PORT")
            } else {
                Log.w(TAG, "[GBF-ACC] Embedded Go Core did not bind within 5s")
            }
        } catch (e: Throwable) {
            Log.e(TAG, "[GBF-ACC] Failed to start embedded Go Core: ${e.message}", e)
        }
    }

    internal fun resolveCoreBinary(context: Context): File? {
        // 1. Direct nativeLibraryDir check
        val nativeDirFile = File(context.applicationInfo.nativeLibraryDir, "libgbfcore.so")
        if (nativeDirFile.exists() && nativeDirFile.length() > 0) {
            return nativeDirFile
        }

        // 2. ClassLoader native library lookup
        try {
            val cl = EmbeddedCoreManager::class.java.classLoader
            val findLibMethod = cl?.javaClass?.getMethod("findLibrary", String::class.java)
            val libPath = findLibMethod?.invoke(cl, "gbfcore") as? String
            if (!libPath.isNullOrEmpty()) {
                val f = File(libPath)
                if (f.exists() && f.length() > 0) {
                    return f
                }
            }
        } catch (_: Throwable) {
        }

        // 3. Root mode module package check (com.sagisawa.gbfaccelerator.xposed installed standalone)
        val apkSources = mutableListOf<String>()
        try {
            val pm = context.packageManager
            val moduleInfo = pm.getApplicationInfo("com.sagisawa.gbfaccelerator.xposed", 0)
            val moduleNativeFile = File(moduleInfo.nativeLibraryDir, "libgbfcore.so")
            if (moduleNativeFile.exists() && moduleNativeFile.length() > 0) {
                return moduleNativeFile
            }
            moduleInfo.sourceDir?.let { apkSources.add(it) }
            moduleInfo.splitSourceDirs?.let { apkSources.addAll(it) }
        } catch (_: Throwable) {
        }

        // 4. Check cached copy in filesDir
        val primaryTargetDir = context.filesDir
        val privateCore = File(primaryTargetDir, "libgbfcore.so")
        val apkFile = context.applicationInfo.sourceDir?.let { File(it) }
        val apkModified = apkFile?.lastModified() ?: 0L

        if (privateCore.exists() && privateCore.length() > 0) {
            if (apkModified == 0L || privateCore.lastModified() >= apkModified) {
                return privateCore
            }
            Log.i(TAG, "[GBF-ACC] Host APK updated (modified: $apkModified vs cache: ${privateCore.lastModified()}); re-extracting core")
        }

        // 5. Extract from APK zip archives (sourceDir, splitSourceDirs, and module sourceDir)
        context.applicationInfo.sourceDir?.let { apkSources.add(it) }
        context.applicationInfo.splitSourceDirs?.let { apkSources.addAll(it) }

        for (apkPath in apkSources.distinct()) {
            val extracted = extractCoreFromApk(File(apkPath), privateCore)
            if (extracted != null && extracted.exists() && extracted.length() > 0) {
                return extracted
            }
        }

        // 6. Secondary fallback to codeCacheDir if filesDir failed
        try {
            val codeCacheCore = File(context.codeCacheDir, "libgbfcore.so")
            if (codeCacheCore.exists() && codeCacheCore.length() > 0) {
                return codeCacheCore
            }
            for (apkPath in apkSources.distinct()) {
                val extracted = extractCoreFromApk(File(apkPath), codeCacheCore)
                if (extracted != null && extracted.exists() && extracted.length() > 0) {
                    return extracted
                }
            }
        } catch (_: Throwable) {
        }

        return null
    }

    internal fun extractCoreFromApk(apkFile: File, targetFile: File): File? {
        if (!apkFile.exists()) return null
        return try {
            ZipFile(apkFile).use { zip ->
                // 1. Direct entry check
                val directEntry = zip.getEntry("lib/arm64-v8a/libgbfcore.so")
                    ?: zip.getEntry("assets/libgbfcore.so")
                    ?: zip.entries().asSequence().firstOrNull { it.name.endsWith("libgbfcore.so") }

                if (directEntry != null) {
                    val tempFile = File(targetFile.parentFile, "${targetFile.name}.tmp")
                    zip.getInputStream(directEntry).use { input ->
                        FileOutputStream(tempFile).use { output ->
                            input.copyTo(output)
                        }
                    }
                    if (targetFile.exists()) {
                        targetFile.delete()
                    }
                    if (!tempFile.renameTo(targetFile)) {
                        tempFile.copyTo(targetFile, overwrite = true)
                        tempFile.delete()
                    }
                    targetFile.setExecutable(true, false)
                    return targetFile
                }

                // 2. Nested archive check (LSPatch embeds modules inside assets/lspatch/modules/*.apk)
                val nestedEntries = zip.entries().asSequence().filter {
                    !it.isDirectory && (it.name.endsWith(".apk") || it.name.endsWith(".zip"))
                }.toList()

                for (nestedEntry in nestedEntries) {
                    var found: Boolean = false
                    try {
                        zip.getInputStream(nestedEntry).use { inStream ->
                            ZipInputStream(inStream).use { nestedZip ->
                                var ze = nestedZip.nextEntry
                                while (ze != null) {
                                    if (!ze.isDirectory && ze.name.endsWith("libgbfcore.so")) {
                                        val tempFile = File(targetFile.parentFile, "${targetFile.name}.tmp")
                                        FileOutputStream(tempFile).use { output ->
                                            nestedZip.copyTo(output)
                                        }
                                        if (targetFile.exists()) {
                                            targetFile.delete()
                                        }
                                        if (!tempFile.renameTo(targetFile)) {
                                            tempFile.copyTo(targetFile, overwrite = true)
                                            tempFile.delete()
                                        }
                                        targetFile.setExecutable(true, false)
                                        Log.i(TAG, "[GBF-ACC] Extracted libgbfcore.so from nested module ${nestedEntry.name}!${ze.name}")
                                        found = true
                                        break
                                    }
                                    ze = nestedZip.nextEntry
                                }
                            }
                        }
                    } catch (e: Throwable) {
                        Log.d(TAG, "[GBF-ACC] Failed reading nested archive ${nestedEntry.name}: ${e.message}")
                    }
                    if (found) {
                        return targetFile
                    }
                }
                null
            }
        } catch (e: Throwable) {
            Log.w(TAG, "[GBF-ACC] Failed to extract libgbfcore.so from ${apkFile.name}: ${e.message}")
            null
        }
    }

    /**
     * Sends a graceful shutdown signal to the daemon.
     */
    fun stopCore() {
        executor.execute {
            runCatching {
                val url = URL("http://127.0.0.1:$CONTROL_PORT/api/app/quit")
                val conn = url.openConnection() as HttpURLConnection
                conn.connectTimeout = 500
                conn.readTimeout = 500
                conn.requestMethod = "POST"
                conn.responseCode
                conn.disconnect()
            }
            coreProcess?.destroy()
            coreProcess = null
            isStarted.set(false)
        }
    }
}
