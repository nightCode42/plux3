// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package dev.plux.addtoapp.host

import android.view.View
import android.widget.FrameLayout

/**
 * [content] in a view padded by the system bars. From Android 15 an app
 * that targets it is laid out edge to edge, behind the status and
 * navigation bars, so a screen's first controls would sit under them and
 * a tap there would reach the bar instead.
 */
fun withSystemBars(content: View): View =
    FrameLayout(content.context).apply {
        fitsSystemWindows = true
        addView(content)
    }
