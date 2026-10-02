// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package dev.plux.addtoapp.host

import android.app.Activity
import android.os.Bundle
import android.widget.Button
import android.widget.LinearLayout
import android.widget.TextView

/**
 * The host's native home screen. Its buttons open Plux pages by route:
 * full screen in a FlutterActivity, or below a native header in a
 * FlutterFragment. The intent's extras, when it has them, are the
 * runtime's settings (HostApp.configure); the UI tests pass the test
 * server's.
 */
class MainActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val app = application as HostApp
        intent.extras?.let { app.configure(it) }
        val padding = (24 * resources.displayMetrics.density).toInt()
        setContentView(
            LinearLayout(this).apply {
                orientation = LinearLayout.VERTICAL
                setPadding(padding, padding, padding, padding)
                addView(
                    TextView(this@MainActivity).apply {
                        text = "Native home"
                        textSize = 22f
                    },
                )
                addView(button("Open welcome") { app.openPage(this@MainActivity, "welcome") })
                addView(button("Open host link") { app.openEmbedded(this@MainActivity, "host-link") })
            },
        )
    }

    private fun button(label: String, onClick: () -> Unit) =
        Button(this).apply {
            text = label
            isAllCaps = false
            setOnClickListener { onClick() }
        }
}
