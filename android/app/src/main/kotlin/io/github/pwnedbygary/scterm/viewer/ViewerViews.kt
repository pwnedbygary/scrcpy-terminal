package io.github.pwnedbygary.scterm.viewer

import android.content.Context
import android.text.InputType
import android.util.AttributeSet
import android.view.KeyEvent
import android.view.View
import android.view.inputmethod.BaseInputConnection
import android.view.inputmethod.EditorInfo
import android.view.inputmethod.InputConnection
import android.widget.FrameLayout
import io.github.pwnedbygary.scterm.protocol.AndroidInput
import kotlin.math.roundToInt

/** Sizes itself to the largest rectangle of the video's aspect ratio that fits. */
class AspectFrameLayout @JvmOverloads constructor(context: Context, attrs: AttributeSet? = null) : FrameLayout(context, attrs) {
    private var aspect = 0f

    fun setAspectRatio(width: Int, height: Int) {
        val ratio = width.toFloat() / height
        if (ratio != aspect) {
            aspect = ratio
            requestLayout()
        }
    }

    override fun onMeasure(widthMeasureSpec: Int, heightMeasureSpec: Int) {
        if (aspect <= 0f) return super.onMeasure(widthMeasureSpec, heightMeasureSpec)
        val maxWidth = MeasureSpec.getSize(widthMeasureSpec)
        val maxHeight = MeasureSpec.getSize(heightMeasureSpec)
        var width = maxWidth
        var height = (width / aspect).roundToInt()
        if (height > maxHeight) {
            height = maxHeight
            width = (height * aspect).roundToInt()
        }
        super.onMeasure(MeasureSpec.makeMeasureSpec(width, MeasureSpec.EXACTLY), MeasureSpec.makeMeasureSpec(height, MeasureSpec.EXACTLY))
    }
}

/**
 * An invisible editor that receives the soft keyboard and forwards what it
 * types. Suggestions are off (visible-password class) so IMEs commit text as
 * it is typed instead of composing words that would arrive late.
 */
class RemoteKeyboardView @JvmOverloads constructor(context: Context, attrs: AttributeSet? = null) : View(context, attrs) {
    var forwarder: InputForwarder? = null

    init {
        isFocusable = true
        isFocusableInTouchMode = true
    }

    override fun onCheckIsTextEditor() = true

    override fun onCreateInputConnection(outAttrs: EditorInfo): InputConnection {
        outAttrs.inputType = InputType.TYPE_CLASS_TEXT or
            InputType.TYPE_TEXT_VARIATION_VISIBLE_PASSWORD or
            InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS
        outAttrs.imeOptions = EditorInfo.IME_FLAG_NO_EXTRACT_UI or EditorInfo.IME_FLAG_NO_FULLSCREEN or EditorInfo.IME_ACTION_NONE
        return Connection()
    }

    private inner class Connection : BaseInputConnection(this@RemoteKeyboardView, false) {
        private var composing: CharSequence? = null

        override fun commitText(text: CharSequence?, newCursorPosition: Int): Boolean {
            composing = null
            text?.let { forwarder?.type(it.toString()) }
            return true
        }

        override fun setComposingText(text: CharSequence?, newCursorPosition: Int): Boolean {
            composing = text
            return true
        }

        override fun finishComposingText(): Boolean {
            composing?.let { forwarder?.type(it.toString()) }
            composing = null
            return true
        }

        override fun deleteSurroundingText(beforeLength: Int, afterLength: Int): Boolean {
            repeat(beforeLength.coerceAtMost(MAX_DELETE)) { forwarder?.pressKey(AndroidInput.KEYCODE_DEL) }
            repeat(afterLength.coerceAtMost(MAX_DELETE)) { forwarder?.pressKey(AndroidInput.KEYCODE_FORWARD_DEL) }
            return true
        }

        override fun sendKeyEvent(event: KeyEvent): Boolean {
            forwarder?.onKey(event)
            return true
        }

        override fun performEditorAction(actionCode: Int): Boolean {
            forwarder?.pressKey(AndroidInput.KEYCODE_ENTER)
            return true
        }
    }

    private companion object {
        const val MAX_DELETE = 64
    }
}
