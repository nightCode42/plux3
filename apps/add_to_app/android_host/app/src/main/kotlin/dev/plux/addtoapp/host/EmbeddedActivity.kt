// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package dev.plux.addtoapp.host

import android.content.Intent
import android.os.Bundle
import android.view.ViewGroup
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.TextView
import androidx.fragment.app.FragmentActivity
import io.flutter.embedding.android.FlutterFragment

/**
 * A native screen that shows a Plux page below its native header, in a
 * FlutterFragment on the cached engine. The fragment handles back itself:
 * back pops the page, and the module then closes this screen.
 */
class EmbeddedActivity : FragmentActivity() {
    private lateinit var flutter: FlutterFragment

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val padding = (16 * resources.displayMetrics.density).toInt()
        setContentView(
            withSystemBars(
                LinearLayout(this).apply {
                    orientation = LinearLayout.VERTICAL
                    addView(
                        TextView(this@EmbeddedActivity).apply {
                            text = "Native header"
                            textSize = 18f
                            setPadding(padding, padding, padding, padding)
                        },
                    )
                    addView(
                        FrameLayout(this@EmbeddedActivity).apply { id = R.id.flutter_container },
                        LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f),
                    )
                },
            ),
        )
        flutter =
            supportFragmentManager.findFragmentByTag(TAG) as FlutterFragment?
                ?: FlutterFragment
                    .withCachedEngine(HostApp.ENGINE)
                    .shouldAutomaticallyHandleOnBackPressed(true)
                    .build<FlutterFragment>()
                    .also {
                        supportFragmentManager
                            .beginTransaction()
                            .add(R.id.flutter_container, it, TAG)
                            .commit()
                    }
    }

    // The callbacks a FlutterFragment's activity forwards to it.

    override fun onPostResume() {
        super.onPostResume()
        flutter.onPostResume()
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        flutter.onNewIntent(intent)
    }

    override fun onUserLeaveHint() {
        super.onUserLeaveHint()
        flutter.onUserLeaveHint()
    }

    override fun onTrimMemory(level: Int) {
        super.onTrimMemory(level)
        flutter.onTrimMemory(level)
    }

    private companion object {
        const val TAG = "plux"
    }
}
