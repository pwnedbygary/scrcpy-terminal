package io.github.pwnedbygary.scterm.viewer

import android.annotation.SuppressLint
import android.content.ClipData
import android.content.ClipboardManager
import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.content.res.Configuration
import android.graphics.Bitmap
import android.graphics.Color
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
import android.view.ViewGroup
import android.view.WindowManager
import android.view.inputmethod.InputMethodManager
import android.widget.Button
import android.widget.HorizontalScrollView
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
import androidx.core.view.updateLayoutParams
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
import io.github.pwnedbygary.scterm.protocol.LocalAction
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
    private lateinit var barScroll: HorizontalScrollView
    private lateinit var handle: View
    private lateinit var panel: ControlsPanel
    private var landscape = false
    private var statsAlways = false
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
    private val attempts = ViewerAttempts()
    private var cleanupReady = true
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
        barScroll = findViewById(R.id.bar_scroll)
        handle = findViewById(R.id.controls_handle)
        val app = ScTermApp.of(this)
        statsAlways = app.viewerStatsAlwaysVisible
        panel = ControlsPanel(this, app.viewerControlsAlwaysVisible, app.viewerControlsHideSeconds * 1000L) { renderChrome() }
        handle.setOnClickListener { panel.show() }
        ViewCompat.setOnApplyWindowInsetsListener(handle) { view, insets ->
            val gestures = insets.getInsets(WindowInsetsCompat.Type.mandatorySystemGestures()).bottom
            view.updateLayoutParams<ViewGroup.MarginLayoutParams> { bottomMargin = gestures + (6 * resources.displayMetrics.density).toInt() }
            insets
        }
        // The panel is a window of its own: it can only be added once this one is.
        window.decorView.post { applyOrientation(resources.configuration.orientation == Configuration.ORIENTATION_LANDSCAPE) }
        // During a session System Back is the remote Back, and leaving uses the
        // Disconnect button. While connecting, or after a session ended, there is
        // nothing to send it to, so Back leaves.
        onBackPressedDispatcher.addCallback(this) {
            when {
                keyboardShown -> hideKeyboard()
                session == null -> finish()
                else -> input.press(DeviceAction.BACK)
            }
        }
        findViewById<Button>(R.id.overlay_retry).setOnClickListener {
            if (cleanupReady) {
                leaving = false
                connect()
            }
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

    override fun onConfigurationChanged(newConfig: Configuration) {
        super.onConfigurationChanged(newConfig)
        // Other changes (a controller connecting, night mode) arrive here too.
        val landscape = newConfig.orientation == Configuration.ORIENTATION_LANDSCAPE
        if (landscape != this.landscape) applyOrientation(landscape)
    }

    /**
     * Portrait docks the controls below the video; landscape gives the video
     * the whole screen and floats the controls over it, brought up by the
     * handle unless set to always show. They start hidden there because they
     * cover the bottom of the video, where games put their touch controls.
     */
    private fun applyOrientation(landscape: Boolean) {
        if (isFinishing || isDestroyed) return
        this.landscape = landscape
        val home = if (landscape) panel.scroll else barScroll
        if (bar.parent !== home) {
            (bar.parent as? ViewGroup)?.removeView(bar)
            home.addView(bar)
        }
        barScroll.visibility = if (landscape) View.GONE else View.VISIBLE
        if (landscape && panel.alwaysVisible) panel.show() else panel.hide()
        renderChrome()
    }

    /** The handle shows while the floating controls are hidden; the statistics show with the controls. */
    private fun renderChrome() {
        val controlsShown = !landscape || panel.isShowing
        handle.visibility = if (controlsShown) View.GONE else View.VISIBLE
        stats.visibility = if (controlsShown || statsAlways) View.VISIBLE else View.GONE
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
        attempts.invalidate()
        if (::panel.isInitialized) panel.hide()
        main.removeCallbacksAndMessages(null)
        releaseSession()
        super.onDestroy()
    }

    // ------------------------------------------------------------ connection

    private fun connect() {
        val fingerprint = intent.getStringExtra(EXTRA_FINGERPRINT)?.let(Fingerprint::parse)
        val remembered = fingerprint?.let { ScTermApp.of(this).peers.find(it) }
        val host = remembered?.target?.host ?: intent.getStringExtra(EXTRA_HOST)
        val port = remembered?.target?.port ?: intent.getIntExtra(EXTRA_PORT, 0)
        val name = intent.getStringExtra(EXTRA_NAME) ?: host
        if (fingerprint == null || host == null || port == 0) return fail("This device has no address to connect to.")
        val attempt = attempts.begin()
        showOverlay("Connecting to $name…", busy = true)
        thread(name = "viewer-connect", isDaemon = true) {
            var pending: ControllerSession? = null
            try {
                val app = ScTermApp.of(this)
                val connected = ControllerClient(app.identity, app.clientInfo).connect(host, port, fingerprint)
                pending = connected
                if (!attempts.isCurrent(attempt)) { connected.closeNow(); return@thread }
                app.peers.update(fingerprint) { it?.copy(grantedByPeer = Grant.toWire(connected.grants), lastSeenMs = System.currentTimeMillis()) }
                // Not through main, whose queue onDestroy empties: a session that arrives
                // during onDestroy must still reach onConnected, which closes it.
                runOnUiThread { onConnected(connected, attempt) }
            } catch (e: Exception) {
                pending?.closeNow()
                main.post { if (attempts.isCurrent(attempt) && !leaving) fail(describe(e, "$host:$port", name ?: "The target")) }
            }
        }
    }

    private fun onConnected(s: ControllerSession, attempt: Long) {
        if (leaving || !attempts.isCurrent(attempt)) {
            s.closeNow()
            return
        }
        session = s
        intent.getStringExtra(EXTRA_FINGERPRINT)?.let { ScTermApp.of(this).peerMonitor.noteViewer(it, s) }
        s.start(sessionListener(attempt))
        val d = VideoDecoder(decoderListener(attempt))
        decoder = d
        surface?.let(d::setSurface)
        val video = s.video
        if (video != null) {
            thread(name = "video-net", isDaemon = true) { readVideo(video, d, attempt) }
            hideOverlay()
        } else {
            showOverlay("This device may control ${s.welcome.device?.name ?: "the target"} but not see it.", busy = false)
        }
        s.audio?.let { stream ->
            audio = AudioPlayer(stream) { message -> main.post { if (attempts.isCurrent(attempt)) toast(message) } }.also { it.start() }
        }
        wifiLock = Net.wifiLock(this, "scterm-viewer", foreground = true)?.also { it.acquire() }
        s.capabilities?.notes?.firstOrNull()?.let(::toast)
        updateLease()
        main.post(statsTicker)
    }

    private fun readVideo(stream: InputStream, owner: VideoDecoder, attempt: Long) {
        try {
            val reader = MediaStreamReader(stream, MediaWire.MAX_VIDEO_PACKET)
            val problem = when (val start = reader.readStart()) {
                is StreamStart.Enabled ->
                    if (start.codec == Codec.H264) null else "The target sends ${start.codec.wireName}; this viewer decodes H.264 only."
                StreamStart.Disabled -> "The target is not sending video."
                StreamStart.Failed -> "The target could not start its video."
            }
            if (problem != null) {
                main.post { if (attempts.isCurrent(attempt)) fail(problem) }
                return
            }
            while (attempts.isCurrent(attempt)) {
                val item = reader.readItem()
                if (attempts.isCurrent(attempt)) owner.offer(item)
            }
        } catch (_: IOException) {
            // The session ended; its listener reports why.
        }
    }

    private fun sessionListener(attempt: Long) = object : ControllerSession.Listener {
        override fun onLease(state: LeaseState, holder: String?) {
            main.post {
                if (!attempts.isCurrent(attempt)) return@post
                updateLease()
                intent.getStringExtra(EXTRA_FINGERPRINT)?.let { ScTermApp.of(this@ViewerActivity).peerMonitor.noteViewer(it, session) }
                if (state == LeaseState.VIEWER && holder != null) toast("$holder took control")
            }
        }

        override fun onStatus(status: PeerMessage.Status) {
            main.post {
                if (!attempts.isCurrent(attempt)) return@post
                intent.getStringExtra(EXTRA_FINGERPRINT)?.let { ScTermApp.of(this@ViewerActivity).peerMonitor.noteViewer(it, session) }
            }
        }

        override fun onError(error: PeerMessage.Error) {
            main.post { if (attempts.isCurrent(attempt)) toast(describe(error)) }
        }

        override fun onDeviceMessage(message: DeviceMessage) {
            // Device clipboard changes, forwarded only when the clipboard is granted.
            if (message is DeviceMessage.Clipboard) {
                main.post {
                    if (!attempts.isCurrent(attempt)) return@post
                    getSystemService(ClipboardManager::class.java)?.setPrimaryClip(ClipData.newPlainText("scterm", message.text))
                }
            }
        }

        override fun onClosed(reason: String) {
            main.post { if (attempts.isCurrent(attempt) && !leaving) fail("Disconnected: $reason") }
        }
    }

    private fun decoderListener(attempt: Long) = object : VideoDecoder.Listener {
        override fun onVideoSize(width: Int, height: Int) {
            main.post {
                if (!attempts.isCurrent(attempt)) return@post
                videoWidth = width
                videoHeight = height
                videoFrame.setAspectRatio(width, height)
                input.setVideoSize(width, height)
            }
        }

        override fun onKeyFrameNeeded() {
            main.post { if (attempts.isCurrent(attempt)) requestKeyFrame() }
        }

        override fun onError(message: String) {
            main.post { if (attempts.isCurrent(attempt)) toast(message) }
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
        val density = resources.displayMetrics.density
        fun dp(value: Int) = (value * density).toInt()
        fun add(label: String, description: String? = null, onClick: () -> Unit): Button = Button(this).apply {
            text = label
            contentDescription = description
            isAllCaps = false
            // A focused button would eat D-pad, Enter and Space meant for the target.
            isFocusable = false
            textSize = 13f
            // 48dp touch targets; the background draws a 40dp pill inside.
            minWidth = dp(48)
            minimumWidth = dp(48)
            minHeight = 0
            minimumHeight = 0
            setBackgroundResource(R.drawable.bar_button_background)
            setTextColor(Color.WHITE)
            stateListAnimator = null
            setPadding(dp(14), 0, dp(14), 0)
            setOnClickListener { onClick() }
            bar.addView(this, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, dp(48)).apply {
                marginStart = dp(3)
                marginEnd = dp(3)
            })
        }

        for (action in VIEWER_BAR) add(getString(action.text), action.name(::getString)) { perform(action.id) }
        takeControl = add(getString(R.string.viewer_take_control)) { session?.takeover() }
        add(getString(R.string.viewer_disconnect)) {
            leaving = true
            finish()
        }
    }

    private fun perform(id: String) {
        val device = DeviceAction.byId(id)
        when {
            device != null -> input.press(device)
            id == LocalAction.KEYBOARD.id -> if (keyboardShown) hideKeyboard() else showKeyboard()
            id == LocalAction.SCREENSHOT.id -> screenshot()
        }
    }

    private fun updateLease() {
        val s = session
        takeControl.visibility = if (s != null && Grant.CONTROL in s.grants && s.lease != LeaseState.HELD) View.VISIBLE else View.GONE
    }

    private fun showKeyboard() {
        // The panel takes no focus, so Android layers it over the keyboard.
        panel.hide()
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
            // The target only re-asks for viewers it knows are waiting; a decoder that
            // restarted after its keyframe arrived must keep asking itself.
            if (d != null && d.waitingForKeyFrame && now - lastKeyFrameRequest >= KEYFRAME_RETRY_MS) requestKeyFrame()
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
                if (d?.waitingForKeyFrame == true) append("\nWaiting for a keyframe")
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
        // A connect can wait out several timeouts: it can always be left.
        findViewById<Button>(R.id.overlay_close).apply {
            visibility = View.VISIBLE
            setText(if (busy) R.string.cancel else R.string.close)
        }
        findViewById<Button>(R.id.overlay_retry).apply {
            visibility = if (busy || !leaving) View.GONE else View.VISIBLE
            isEnabled = cleanupReady
        }
    }

    private fun hideOverlay() {
        overlay.visibility = View.GONE
    }

    private fun fail(message: String) {
        leaving = true
        attempts.invalidate()
        releaseSession()
        showOverlay(message, busy = false)
    }

    /** Detach on main, then wait for owned media resources before permitting Retry. */
    private fun releaseSession() {
        input.releaseAll()
        main.removeCallbacks(statsTicker)
        val previous = session
        if (previous != null) intent.getStringExtra(EXTRA_FINGERPRINT)?.let { ScTermApp.of(this).peerMonitor.noteViewer(it, null) }
        val previousAudio = audio
        val previousDecoder = decoder
        previousDecoder?.release()
        session = null
        audio = null
        decoder = null
        videoWidth = 0
        videoHeight = 0
        lastKeyFrameRequest = 0
        frameSamples.clear()
        wifiLock?.release()
        wifiLock = null
        if (previous == null && previousAudio == null && previousDecoder == null) return
        cleanupReady = false
        thread(name = "viewer-cleanup", isDaemon = true) {
            val resourcesClosed = ViewerCleanup.release(
                { previous?.closeNow() },
                { previousAudio?.stopAndWait() ?: true },
                { previousDecoder?.releaseAndWait() ?: true },
            )
            main.post {
                if (isDestroyed || isFinishing) return@post
                cleanupReady = resourcesClosed
                findViewById<Button>(R.id.overlay_retry).isEnabled = cleanupReady
                if (!cleanupReady) overlayText.append("\nThe previous media session is still closing. Close and reopen the viewer.")
            }
        }
    }

    private fun toast(message: String) = Toast.makeText(this, message, Toast.LENGTH_SHORT).show()

    companion object {
        private const val EXTRA_FINGERPRINT = "fingerprint"
        private const val EXTRA_HOST = "host"
        private const val EXTRA_PORT = "port"
        private const val EXTRA_NAME = "name"
        private const val STATS_INTERVAL_MS = 500L
        private const val FPS_WINDOW_MS = 2_000L
        private const val KEYFRAME_RETRY_MS = 2_000L

        fun intent(context: Context, peer: PeerRecord): Intent = Intent(context, ViewerActivity::class.java)
            .putExtra(EXTRA_FINGERPRINT, peer.fingerprint)
            .putExtra(EXTRA_HOST, peer.target?.host)
            .putExtra(EXTRA_PORT, peer.target?.port ?: 0)
            .putExtra(EXTRA_NAME, peer.name)

        fun describe(e: Exception, address: String, name: String = "The target"): String = when {
            e is PeerException -> when (e.code) {
                RejectCodes.NOT_PAIRED -> "The target no longer knows this device. Pair again."
                RejectCodes.FORBIDDEN -> "The target does not allow this device to view or control it."
                RejectCodes.BUSY -> "The target already has the maximum number of viewers."
                RejectCodes.UNAVAILABLE -> e.message?.takeIf { it.isNotBlank() }?.let { "$name isn't ready: $it." }
                    ?: "Serving is not ready on the target (screen capture not approved, or the helper not activated)."
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
