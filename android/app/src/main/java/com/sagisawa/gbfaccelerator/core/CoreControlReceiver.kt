package com.sagisawa.gbfaccelerator.core

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.util.Log

class CoreControlReceiver : BroadcastReceiver() {
    companion object {
        private const val TAG = "GBF-ACC-Receiver"
    }

    override fun onReceive(context: Context, intent: Intent?) {
        val action = intent?.action ?: return
        Log.i(TAG, "Received broadcast action: $action")
        when (action) {
            CoreService.ACTION_START -> {
                Log.i(TAG, "Starting CoreService from receiver")
                CoreService.start(context)
            }
            CoreService.ACTION_STOP -> {
                Log.i(TAG, "Stopping CoreService from receiver")
                CoreService.stop(context)
            }
        }
    }
}
