package com.sagisawa.gbfaccelerator.core

import android.content.Context
import android.content.SharedPreferences

object AppPreferences {
    private const val PREFS_NAME = "gbf_accelerator_prefs"
    private const val KEY_ENABLE_RAM_CACHE = "enable_ram_cache"
    private const val KEY_ENABLE_PREFETCH = "enable_prefetch"

    private fun getPrefs(context: Context): SharedPreferences {
        return context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
    }

    fun isRamCacheEnabled(context: Context): Boolean {
        // Mobile devices default to false (OFF) to conserve limited RAM
        return getPrefs(context).getBoolean(KEY_ENABLE_RAM_CACHE, false)
    }

    fun setRamCacheEnabled(context: Context, enabled: Boolean) {
        getPrefs(context).edit().putBoolean(KEY_ENABLE_RAM_CACHE, enabled).apply()
    }

    fun isPrefetchEnabled(context: Context): Boolean {
        // Mobile devices default to false (OFF) to prevent network/rendering stutter
        return getPrefs(context).getBoolean(KEY_ENABLE_PREFETCH, false)
    }

    fun setPrefetchEnabled(context: Context, enabled: Boolean) {
        getPrefs(context).edit().putBoolean(KEY_ENABLE_PREFETCH, enabled).apply()
    }
}
