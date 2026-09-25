package io.github.pwnedbygary.scterm.viewer

import android.annotation.SuppressLint
import android.content.ClipData
import android.content.ClipboardManager
import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.net.wifi.WifiManager
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.provider.MediaStore
import android.view.InputDevice
import android.view.KeyEvent
import android.view.MotionEvent
import android.view.PixelCopy
import android.view.Surface
import android.view.SurfaceHolder
import android.view.SurfaceView
import android.view.View
import android.view.WindowManager
import android.view.inputmethod.InputMethodManager
import android.widget.Button
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.TextView
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.addCallback
import androidx.core.graphics.createBitmap
import androidx.core.view.ViewCompat
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import androidx.core.view.updatePadding
import io.github.pwnedbygary.scterm.R
import io.github.pwnedbygary.scterm.ScTermApp
import io.github.pwnedbygary.scterm.peer.ControllerClient
import io.github.pwnedbygary.scterm.peer.ControllerSession
import io.github.pwnedbygary.scterm.peer.PeerException
import io.github.pwnedbygary.scterm.peer.PeerRecord
import io.github.pwnedbygary.scterm.protocol.Codec
import io.github.pwnedbygary.scterm.protocol.ControlMessage
import io.github.pwnedbygary.scterm.protocol.DeviceAction
import io.github.pwnedbygary.scterm.protocol.DeviceMessage
import io.github.pwnedbygary.scterm.protocol.ErrorCodes
import io.github.pwnedbygary.scterm.protocol.Fingerprint
import io.github.pwnedbygary.scterm.protocol.Grant
import io.github.pwnedbygary.scterm.protocol.LeaseState
import io.github.pwnedbygary.scterm.protocol.MediaStreamReader
import io.github.pwnedbygary.scterm.protocol.MediaWire
import io.github.pwnedbygary.scterm.protocol.PeerMessage
import io.github.pwnedbygary.scterm.protocol.RejectCodes
import io.github.pwnedbygary.scterm.protocol.StreamStart
import io.github.pwnedbygary.scterm.util.Net
import java.io.IOException
import java.io.InputStream
import java.net.ConnectException
import java.net.SocketTimeoutException
import java.security.cert.CertificateException
import javax.net.ssl.SSLException
import kotlin.concurrent.thread

/**
 * Views and controls one paired target. Leaving the screen ends the session:
 * a backgrounded viewer never holds the target's lease or its bandwidth.
 */
class ViewerActivity : ComponentActivity() {
    private val main = Handler(Looper.getMainLooper())
    private lateinit var surfaceView: SurfaceView
    private lateinit var videoFrame: AspectFrameLayout
    private lateinit var keyboardSink: RemoteKeyboardView
    private lateinit var stats: TextView
    private lateinit var bar: LinearLayout
    private lateinit var overlay: View
    private lateinit var overlayText: TextView
    private lateinit var takeControl: Button
    private lateinit var input: InputForwarder

    @Volatile
    private var session: ControllerSession? = null

    @Volatile
    private var decoder: VideoDecoder? = null
    private var audio: AudioPlayer? = null
    private var surface: Surface? = null
    private var wifiLock: WifiManager.WifiLock? = null
    private var keyboardShown = false
    private var leaving = false
    private var videoWidth = 0
    private var videoHeight = 0
    private var lastKeyFrameRequest = 0L
    private val frameSamples = ArrayDeque<Pair<Long, Long>>()

    // The surface forwards raw touches to the remote screen rather than acting as a
    // local click target; accessibility users drive the target through the action bar.
    @SuppressLint("ClickableViewAccessibility")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        WindowCompat.setDecorFitsSystemWindows(window, false)
        setContentView(R.layout.activity_viewer)
        WindowCompat.getInsetsController(window, window.decorView).apply {
            hide(WindowInsetsCompat.Type.systemBars())
            systemBarsBehavior = WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
        }
        surfaceView = findViewById(R.id.surface)
        videoFrame = findViewById(R.id.video_frame)
        keyboardSink = findViewById(R.id.keyboard_sink)
        stats = findViewById(R.id.stats)
        bar = findViewById(R.id.bar)
        overlay = findViewById(R.id.overlay)
        overlayText = findViewById(R.id.message)
        findViewById<Button>(R.id.overlay_close).setOnClickListener { finish() }
        ViewCompat.setOnApplyWindowInsetsListener(findViewById(R.id.bar_scroll)) { view, insets ->
            // With gesture navigation the bottom edge belongs to the system even while the bars
            // are hidden: a horizontal swipe there switches apps instead of scrolling the bar.
            view.updatePadding(bottom = insets.getInsets(WindowInsetsCompat.Type.mandatorySystemGestures()).bottom)
            insets
        }

        input = InputForwarder { message -> session?.send(message) }
        keyboardSink.forwarder = input
        // Unbuffered: each touch sample is delivered as it arrives, not batched until the
        // next frame, which saves up to a frame of input latency.
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) surfaceView.requestUnbufferedDispatch(InputDevice.SOURCE_CLASS_POINTER)
        surfaceView.setOnTouchListener { v, e ->
            if (e.actionMasked == MotionEvent.ACTION_DOWN) v.requestUnbufferedDispatch(e)
            input.onTouch(v, e)
        }
        surfaceView.setOnGenericMotionListener { v, e -> input.onGenericMotion(v, e) }
        preferFastestRefresh()
        surfaceView.holder.addCallback(surfaceCallback)
        buildBar()
        // System Back is the remote Back; leaving uses the Disconnect button.
        onBackPressedDispatcher.addCallback(this) {
            if (keyboardShown) hideKeyboard() else input.press(DeviceAction.BACK)
        }
        connect()
    }

    /** The fastest refresh rate at the current resolution: a frame waits less for the display. */
    private fun preferFastestRefresh() {
        val screen = (if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) display else @Suppress("DEPRECATION") windowManager.defaultDisplay) ?: return
        val current = screen.mode
        val fastest = screen.supportedModes
            .filter { it.physicalWidth == current.physicalWidth && it.physicalHeight == current.physicalHeight }
            .maxByOrNull { it.refreshRate } ?: return
        window.attributes = window.attributes.also { it.preferredDisplayModeId = fastest.modeId }
    }

    // Keys no view consumed (the bar's buttons are not focusable, so none steal them).
    override fun onKeyDown(keyCode: Int, event: KeyEvent): Boolean = forwardKey(event) || super.onKeyDown(keyCode, event)

    override fun onKeyUp(keyCode: Int, event: KeyEvent): Boolean = forwardKey(event) || super.onKeyUp(keyCode, event)

    private fun forwardKey(event: KeyEvent): Boolean {
        val s = session ?: return false
        if (Grant.CONTROL !in s.grants) return false
        // This phone's own volume and Back keys stay local; an external keyboard's go to the target.
        val device = event.device
        val external = device != null && if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            device.isExternal
        } else {
            !device.isVirtual && device.keyboardType == InputDevice.KEYBOARD_TYPE_ALPHABETIC
        }
        val local = event.keyCode == KeyEvent.KEYCODE_BACK || event.keyCode == KeyEvent.KEYCODE_VOLUME_UP ||
            event.keyCode == KeyEvent.KEYCODE_VOLUME_DOWN || event.keyCode == KeyEvent.KEYCODE_VOLUME_MUTE
        if (local && !external) return false
        return input.onKey(event)
    }

    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        if (!hasFocus) input.releaseAll()
    }

    override fun onStop() {
        super.onStop()
        if (!isChangingConfigurations) {
            leaving = true
            finish()
        }
    }

    override fun onDestroy() {
        leaving = true
        main.removeCallbacksAndMessages(null)
        input.releaseAll()
        session?.disconnect()
        session = null
        decoder?.release()
        decoder = null
        wifiLock?.release()
        super.onDestroy()
    }

    // ------------------------------------------------------------ connection

    private fun connect() {
        val fingerprint = intent.getStringExtra(EXTRA_FINGERPRINT)?.let(Fingerprint::parse)
        val host = intent.getStringExtra(EXTRA_HOST)
        val port = intent.getIntExtra(EXTRA_PORT, 0)
        val name = intent.getStringExtra(EXTRA_NAME) ?: host
        if (fingerprint == null || host == null || port == 0) return fail("This device has no address to connect to.")
        showOverlay("Connecting to $name…", busy = true)
        thread(name = "viewer-connect", isDaemon = true) {
            try {
                val app = ScTermApp.of(this)
                val connected = ControllerClient(app.identity, app.clientInfo).connect(host, port, fingerprint)
                main.post { onConnected(connected) }
            } catch (e: Exception) {
                main.post { fail(describe(e, "$host:$port")) }
            }
        }
    }

    private fun onConnected(s: ControllerSession) {
        if (leaving) {
            s.disconnect()
            return
        }
        session = s
        s.start(sessionListener)
        val d = VideoDecoder(decoderListener)
        decoder = d
        surface?.let(d::setSurface)
        val video = s.video
        if (video != null) {
            thread(name = "video-net", isDaemon = true) { readVideo(video) }
            hideOverlay()
        } else {
            showOverlay("This device may control ${s.welcome.device?.name ?: "the target"} but not see it.", busy = false)
        }
        s.audio?.let { stream ->
            audio = AudioPlayer(stream) { message -> main.post { toast(message) } }.also { it.start() }
        }
        wifiLock = Net.wifiLock(this, "scterm-viewer", foreground = true)?.also { it.acquire() }
        s.capabilities?.notes?.firstOrNull()?.let(::toast)
        updateLease()
        main.post(statsTicker)
    }

    private fun readVideo(stream: InputStream) {
        try {
            val reader = MediaStreamReader(stream, MediaWire.MAX_VIDEO_PACKET)
            val problem = when (val start = reader.readStart()) {
                is StreamStart.Enabled ->
                    if (start.codec == Codec.H264) null else "The target sends ${start.codec.wireName}; this viewer decodes H.264 only."
                StreamStart.Disabled -> "The target is not sending video."
                StreamStart.Failed -> "The target could not start its video."
            }
            if (problem != null) {
                main.post { fail(problem) }
                return
            }
            while (true) {
                val item = reader.readItem()
                (decoder ?: break).offer(item)
            }
        } catch (_: IOException) {
            // The session ended; its listener reports why.
        }
    }

    private val sessionListener = object : ControllerSession.Listener {
        override fun onLease(state: LeaseState, holder: String?) {
            main.post {
                updateLease()
                if (state == LeaseState.VIEWER && holder != null) toast("$holder took control")
            }
        }

        override fun onError(error: PeerMessage.Error) {
            main.post { toast(describe(error)) }
        }

        override fun onDeviceMessage(message: DeviceMessage) {
            // Device clipboard changes, forwarded only when the clipboard is granted.
            if (message is DeviceMessage.Clipboard) {
                main.post {
                    getSystemService(ClipboardManager::class.java)?.setPrimaryClip(ClipData.newPlainText("scterm", message.text))
                }
            }
        }

        override fun onClosed(reason: String) {
            main.post { if (!leaving) fail("Disconnected: $reason") }
        }
    }

    private val decoderListener = object : VideoDecoder.Listener {
        override fun onVideoSize(width: Int, height: Int) {
            main.post {
                videoWidth = width
                videoHeight = height
                videoFrame.setAspectRatio(width, height)
                input.setVideoSize(width, height)
            }
        }

        override fun onKeyFrameNeeded() {
            main.post { requestKeyFrame() }
        }

        override fun onError(message: String) {
            main.post { toast(message) }
        }
    }

    private val surfaceCallback = object : SurfaceHolder.Callback {
        override fun surfaceCreated(holder: SurfaceHolder) {
            surface = holder.surface
            decoder?.setSurface(holder.surface)
        }

        override fun surfaceChanged(holder: SurfaceHolder, format: Int, width: Int, height: Int) = Unit

        override fun surfaceDestroyed(holder: SurfaceHolder) {
            surface = null
            decoder?.setSurface(null)
        }
    }

    private fun requestKeyFrame() {
        val now = SystemClock.elapsedRealtime()
        if (now - lastKeyFrameRequest < 1_000) return
        lastKeyFrameRequest = now
        session?.send(ControlMessage.ResetVideo)
    }

    // ------------------------------------------------------------ action bar

    private fun buildBar() {
        fun add(label: String, onClick: () -> Unit): Button = Button(this).apply {
            text = label
            isAllCaps = false
            // A focused button would eat D-pad, Enter and Space meant for the target.
            isFocusable = false
            textSize = 13f
            minWidth = 0
            minimumWidth = 0
            setPadding(24, 0, 24, 0)
            setOnClickListener { onClick() }
            bar.addView(this)
        }
        fun device(label: String, action: DeviceAction) = add(label) { input.press(action) }

        device("Back", DeviceAction.BACK)
        device("Home", DeviceAction.HOME)
        device("Recents", DeviceAction.APP_SWITCH)
        add("Keyboard") { if (keyboardShown) hideKeyboard() else showKeyboard() }
        device("Vol −", DeviceAction.VOLUME_DOWN)
        device("Vol +", DeviceAction.VOLUME_UP)
        device("Mute", DeviceAction.MUTE)
        device("Power", DeviceAction.POWER)
        device("Rotate", DeviceAction.ROTATE)
        device("Notifications", DeviceAction.NOTIFICATIONS)
        device("Quick settings", DeviceAction.SETTINGS)
        device("Collapse", DeviceAction.COLLAPSE)
        add("Screenshot") { screenshot() }
        takeControl = add(getString(R.string.viewer_take_control)) { session?.takeover() }
        add(getString(R.string.viewer_disconnect)) {
            leaving = true
            finish()
        }
    }

    private fun updateLease() {
        val s = session
        takeControl.visibility = if (s != null && Grant.CONTROL in s.grants && s.lease != LeaseState.HELD) View.VISIBLE else View.GONE
    }

    private fun showKeyboard() {
        keyboardSink.requestFocus()
        getSystemService(InputMethodManager::class.java)?.showSoftInput(keyboardSink, 0)
        keyboardShown = true
    }

    private fun hideKeyboard() {
        getSystemService(InputMethodManager::class.java)?.hideSoftInputFromWindow(keyboardSink.windowToken, 0)
        keyboardShown = false
    }

    /** Saves exactly what is displayed (no toolbar), like the browser's PNG screenshot. */
    private fun screenshot() {
        if (videoWidth == 0 || surface == null) return toast("No frame to capture yet")
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) return toast("Screenshots need Android 10 or newer on this device")
        val bitmap = createBitmap(videoWidth, videoHeight)
        PixelCopy.request(surfaceView, bitmap, { result ->
            if (result != PixelCopy.SUCCESS) return@request toast("Screenshot failed ($result)")
            thread(name = "screenshot", isDaemon = true) {
                val saved = saveToPictures(bitmap)
                main.post { toast(if (saved) "Screenshot saved to Pictures/scterm" else "Could not save the screenshot") }
            }
        }, main)
    }

    private fun saveToPictures(bitmap: Bitmap): Boolean {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) return false
        val values = ContentValues().apply {
            put(MediaStore.Images.Media.DISPLAY_NAME, "scterm-${System.currentTimeMillis()}.png")
            put(MediaStore.Images.Media.MIME_TYPE, "image/png")
            put(MediaStore.Images.Media.RELATIVE_PATH, "Pictures/scterm")
            // Galleries skip pending items, so they never show a half-written file.
            put(MediaStore.Images.Media.IS_PENDING, 1)
        }
        val uri = contentResolver.insert(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, values) ?: return false
        val written = try {
            contentResolver.openOutputStream(uri)?.use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) } ?: false
        } catch (e: IOException) {
            false
        }
        if (!written) {
            contentResolver.delete(uri, null, null)
            return false
        }
        contentResolver.update(uri, ContentValues().apply { put(MediaStore.Images.Media.IS_PENDING, 0) }, null, null)
        return true
    }

    // ----------------------------------------------------------------- status

    private val statsTicker = object : Runnable {
        override fun run() {
            val s = session ?: return
            val d = decoder
            val now = SystemClock.elapsedRealtime()
            val frames = d?.framesRendered ?: 0
            // Averaged over a window: frames arriving in bursts would make a single interval swing wildly.
            frameSamples.addLast(now to frames)
            while (frameSamples.size > 1 && now - frameSamples.first().first > FPS_WINDOW_MS) frameSamples.removeFirst()
            val (since, base) = frameSamples.first()
            val fps = if (now > since) (frames - base) * 1000f / (now - since) else 0f
            stats.text = buildString {
                append(s.welcome.device?.name ?: "target")
                if (videoWidth > 0) append(" · ${videoWidth}×$videoHeight")
                append(" · %.0f fps".format(fps))
                if (s.roundTripMs >= 0) append(" · RTT ${s.roundTripMs} ms")
                d?.choice?.let {
                    append("\n").append(it.name)
                    append(if (it.hardware) " (hw" else " (sw").append(if (it.lowLatency) ", low-latency)" else ")")
                }
                val dropped = d?.packetsDropped ?: 0
                if (dropped > 0) append(" · $dropped dropped")
                if (s.lease != LeaseState.HELD && Grant.CONTROL in s.grants) append("\nViewing: another device has control")
                if (Grant.CONTROL !in s.grants) append("\nView only")
            }
            main.postDelayed(this, STATS_INTERVAL_MS)
        }
    }

    private fun showOverlay(message: String, busy: Boolean) {
        overlay.visibility = View.VISIBLE
        overlayText.text = message
        findViewById<ProgressBar>(R.id.progress).visibility = if (busy) View.VISIBLE else View.GONE
        findViewById<Button>(R.id.overlay_close).visibility = if (busy) View.GONE else View.VISIBLE
    }

    private fun hideOverlay() {
        overlay.visibility = View.GONE
    }

    private fun fail(message: String) {
        leaving = true
        session?.disconnect()
        showOverlay(message, busy = false)
    }

    private fun toast(message: String) = Toast.makeText(this, message, Toast.LENGTH_SHORT).show()

    companion object {
        private const val EXTRA_FINGERPRINT = "fingerprint"
        private const val EXTRA_HOST = "host"
        private const val EXTRA_PORT = "port"
        private const val EXTRA_NAME = "name"
        private const val STATS_INTERVAL_MS = 500L
        private const val FPS_WINDOW_MS = 2_000L

        fun intent(context: Context, peer: PeerRecord): Intent = Intent(context, ViewerActivity::class.java)
            .putExtra(EXTRA_FINGERPRINT, peer.fingerprint)
            .putExtra(EXTRA_HOST, peer.target?.host)
            .putExtra(EXTRA_PORT, peer.target?.port ?: 0)
            .putExtra(EXTRA_NAME, peer.name)

        fun describe(e: Exception, address: String): String = when {
            e is PeerException -> when (e.code) {
                RejectCodes.NOT_PAIRED -> "The target no longer knows this device. Pair again."
                RejectCodes.FORBIDDEN -> "The target does not allow this device to view or control it."
                RejectCodes.BUSY -> "The target already has the maximum number of viewers."
                RejectCodes.UNAVAILABLE -> "Serving is not ready on the target (screen capture not approved, or the helper not activated)."
                RejectCodes.VERSION -> "The target runs an incompatible version of scterm."
                else -> e.message ?: e.code
            }
            e is SSLException || e.cause is CertificateException ->
                "The device at $address is not the paired target (its identity changed). If it was reinstalled, pair again."
            e is ConnectException || e is SocketTimeoutException ->
                "Could not reach $address. Is serving on, and are both devices on the same network?"
            else -> e.message ?: e.javaClass.simpleName
        }

        fun describe(error: PeerMessage.Error): String = when (error.code) {
            ErrorCodes.NO_LEASE -> "Another device has control. Use Take control."
            ErrorCodes.UNSUPPORTED -> "The target cannot do that with its current serving mode."
            ErrorCodes.FORBIDDEN -> "Not permitted for this device."
            else -> error.message.ifEmpty { error.code }
        }
    }
}
