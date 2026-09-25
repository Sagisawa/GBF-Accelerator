package com.sagisawa.gbfaccelerator.browser

import com.sagisawa.gbfaccelerator.browser.skyleap.SkyLeapAdapter
import java.util.concurrent.CopyOnWriteArrayList

/**
 * Registry for browser adapters.
 * Manages active adapters and provides lookup by package or process name.
 */
object BrowserAdapterRegistry {

    private val adapters = CopyOnWriteArrayList<BrowserAdapter>()

    init {
        resetToDefaults()
    }

    /**
     * Registers a new browser adapter. If an adapter with the same id exists, it will not be duplicated.
     */
    fun register(adapter: BrowserAdapter) {
        if (adapters.none { it.id == adapter.id }) {
            adapters.add(adapter)
        }
    }

    /**
     * Unregisters an adapter by ID.
     */
    fun unregister(adapterId: String) {
        adapters.removeAll { it.id == adapterId }
    }

    /**
     * Clears all registered adapters.
     */
    fun clear() {
        adapters.clear()
    }

    /**
     * Resets registry to default configuration with built-in SkyLeap adapter.
     */
    fun resetToDefaults() {
        adapters.clear()
        adapters.add(SkyLeapAdapter())
    }

    /**
     * Finds the first adapter matching the given package name.
     */
    fun findAdapterByPackage(packageName: String): BrowserAdapter? {
        return adapters.firstOrNull { it.matchesPackage(packageName) }
    }

    /**
     * Finds the first adapter matching the given process name.
     */
    fun findAdapterByProcess(processName: String): BrowserAdapter? {
        return adapters.firstOrNull { it.matchesProcess(processName) }
    }

    /**
     * Finds an adapter by its unique identifier.
     */
    fun findAdapterById(id: String): BrowserAdapter? {
        return adapters.firstOrNull { it.id == id }
    }

    /**
     * Returns an unmodifiable snapshot of all registered adapters.
     */
    fun getAllAdapters(): List<BrowserAdapter> = adapters.toList()
}
