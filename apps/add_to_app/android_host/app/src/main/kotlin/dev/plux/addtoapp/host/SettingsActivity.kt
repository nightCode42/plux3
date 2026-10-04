// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package dev.plux.addtoapp.host

import android.app.Activity
import android.os.Bundle
import android.widget.TextView

/**
 * The host's native settings screen, which a plugin page opens through
 * the module's native route `host-settings`. When it closes, the page's
 * navigation completes.
 */
class SettingsActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val padding = (24 * resources.displayMetrics.density).toInt()
        setContentView(
            withSystemBars(
                TextView(this).apply {
                    text = "Host settings"
                    textSize = 22f
                    setPadding(padding, padding, padding, padding)
                },
            ),
        )
    }

    override fun onDestroy() {
        super.onDestroy()
        if (isFinishing) (application as HostApp).nativeScreenClosed()
    }
}
