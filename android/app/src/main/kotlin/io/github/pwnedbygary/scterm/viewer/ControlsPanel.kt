package io.github.pwnedbygary.scterm.viewer

import android.annotation.SuppressLint
import android.app.Activity
import android.app.Dialog
import android.graphics.drawable.GradientDrawable
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.view.Gravity
import android.view.MotionEvent
import android.view.View
import android.view.ViewGroup
import android.view.Window
import android.view.WindowManager
import android.widget.HorizontalScrollView
import androidx.core.view.ViewCompat
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import io.github.pwnedbygary.scterm.R
import kotlin.math.roundToInt

/**
 * The viewer's controls over a fullscreen video: a frosted strip along the
 * bottom. It is a window of its own because the video is a separate surface
 * no view can blur, while Android 12+ can blur whatever lies behind a window.
 * The window takes no focus, so keys still reach the viewer, and touches
 * outside it still reach the video. It hides [hideAfterMs] after the last
 * touch on it, unless [alwaysVisible].
 */
class ControlsPanel(
    private val activity: Activity,
    val alwaysVisible: Boolean,
    private val hideAfterMs: Long,
    private val onVisibilityChanged: () -> Unit,
) {
    private val main = Handler(Looper.getMainLooper())
    private val hideLater = Runnable { hide() }

    // A dialog's content has no parent until the dialog shows.
    @SuppressLint("InflateParams")
    private val content: View = activity.layoutInflater.inflate(R.layout.viewer_controls_panel, null)

    /** Holds the viewer's button row while in landscape. */
    val scroll: HorizontalScrollView = content.findViewById(R.id.panel_scroll)

    private val dialog = object : Dialog(activity, R.style.Theme_ScTerm_ControlsPanel) {
        override fun dispatchTouchEvent(ev: MotionEvent): Boolean {
            restartTimer()
            return super.dispatchTouchEvent(ev)
        }
    }.apply {
        setContentView(content)
        setCancelable(false)
    }

    val isShowing: Boolean get() = dialog.isShowing

    init {
        content.findViewById<View>(R.id.controls_collapse).apply {
            visibility = if (alwaysVisible) View.GONE else View.VISIBLE
            setOnClickListener { hide() }
        }
    }

    fun show() {
        // A dialog needs its activity's window to be added first.
        if (activity.isFinishing || activity.isDestroyed || !activity.window.decorView.isAttachedToWindow) return
        if (!dialog.isShowing) {
            val window = dialog.window ?: return
            val blur = configure(window)
            dialog.show()
            // Android ignores a blur set before the window is attached, and ties
            // it to that attachment: set it on every show, clear it on every hide.
            if (blur && Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) window.setBackgroundBlurRadius(dp(BLUR_RADIUS_DP))
            onVisibilityChanged()
        }
        restartTimer()
    }

    fun hide() {
        main.removeCallbacks(hideLater)
        if (dialog.isShowing) {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) dialog.window?.setBackgroundBlurRadius(0)
            dialog.dismiss()
            onVisibilityChanged()
        }
    }

    private fun restartTimer() {
        main.removeCallbacks(hideLater)
        if (!alwaysVisible) main.postDelayed(hideLater, hideAfterMs)
    }

    private fun dp(value: Int) = (value * activity.resources.displayMetrics.density).roundToInt()

    /** Returns whether the background will be blurred. */
    private fun configure(window: Window): Boolean {
        val blur = Build.VERSION.SDK_INT >= Build.VERSION_CODES.S && activity.windowManager.isCrossWindowBlurEnabled
        window.setBackgroundDrawable(
            GradientDrawable().apply {
                cornerRadius = dp(20).toFloat()
                // Light over the blurred video; without the blur, dark enough to read on anything.
                setColor(if (blur) 0x59101418 else 0xD9101418.toInt())
                setStroke(dp(1), 0x33FFFFFF)
            },
        )
        window.addFlags(WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE or WindowManager.LayoutParams.FLAG_NOT_TOUCH_MODAL)
        window.clearFlags(WindowManager.LayoutParams.FLAG_DIM_BEHIND)
        // Above the bottom gesture area: a horizontal swipe there switches apps instead of scrolling the row.
        val gestures = ViewCompat.getRootWindowInsets(activity.window.decorView)
            ?.getInsets(WindowInsetsCompat.Type.mandatorySystemGestures())?.bottom ?: 0
        window.setGravity(Gravity.BOTTOM or Gravity.CENTER_HORIZONTAL)
        window.setLayout(activity.resources.displayMetrics.widthPixels - 2 * dp(16), ViewGroup.LayoutParams.WRAP_CONTENT)
        window.attributes = window.attributes.apply { y = gestures + dp(8) }
        WindowCompat.getInsetsController(window, window.decorView).apply {
            hide(WindowInsetsCompat.Type.systemBars())
            systemBarsBehavior = WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
        }
        return blur
    }

    private companion object {
        const val BLUR_RADIUS_DP = 24
    }
}
