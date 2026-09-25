package io.github.pwnedbygary.scterm.ui

import android.app.AlertDialog
import android.content.ClipData
import android.content.ClipDescription
import android.content.ClipboardManager
import android.content.Intent
import android.graphics.Typeface
import android.media.projection.MediaProjectionConfig
import android.media.projection.MediaProjectionManager
import android.os.Build
import android.os.Bundle
import android.os.PersistableBundle
import android.provider.Settings
import android.text.InputType
import android.util.TypedValue
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.CheckBox
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.RadioButton
import android.widget.RadioGroup
import android.widget.ScrollView
import android.widget.TextView
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.content.ContextCompat
import androidx.core.view.ViewCompat
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import io.github.pwnedbygary.scterm.R
import io.github.pwnedbygary.scterm.ScTermApp
import io.github.pwnedbygary.scterm.peer.ControllerClient
import io.github.pwnedbygary.scterm.peer.PeerException
import io.github.pwnedbygary.scterm.peer.PeerRecord
import io.github.pwnedbygary.scterm.peer.TargetAddress
import io.github.pwnedbygary.scterm.protocol.Grant
import io.github.pwnedbygary.scterm.protocol.Invitation
import io.github.pwnedbygary.scterm.protocol.RejectCodes
import io.github.pwnedbygary.scterm.target.BackendKind
import io.github.pwnedbygary.scterm.target.RemoteInputService
import io.github.pwnedbygary.scterm.target.ServeState
import io.github.pwnedbygary.scterm.target.Serving
import io.github.pwnedbygary.scterm.target.TargetService
import io.github.pwnedbygary.scterm.util.Net
import io.github.pwnedbygary.scterm.util.Permissions
import io.github.pwnedbygary.scterm.viewer.ViewerActivity
import kotlinx.coroutines.launch
import java.net.ConnectException
import java.net.SocketTimeoutException
import javax.net.ssl.SSLException
import kotlin.concurrent.thread

/** One screen for both roles: serve this device, or pair with and control others. */
class MainActivity : ComponentActivity() {
    private val app by lazy { ScTermApp.of(this) }

    private lateinit var nameView: TextView
    private lateinit var identityView: TextView
    private lateinit var addressView: TextView
    private lateinit var backendGroup: RadioGroup
    private lateinit var serveStatus: TextView
    private lateinit var serveButton: Button
    private lateinit var inviteButton: Button
    private lateinit var kickButton: Button
    private lateinit var inputButton: Button
    private lateinit var helperBox: LinearLayout
    private lateinit var helperCommand: TextView
    private lateinit var peersList: LinearLayout

    private var pendingAfterPermissions: (() -> Unit)? = null
    private var storeListener: AutoCloseable? = null

    private val permissionRequest = registerForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) {
        val next = pendingAfterPermissions
        pendingAfterPermissions = null
        // Notifications are optional; local network access is not.
        if (Permissions.missingForNetworking(this, serving = false).isEmpty()) {
            next?.invoke()
        } else {
            toast("Local network access is required to reach other devices.")
        }
    }

    private val projectionConsent = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        val data = result.data
        if (result.resultCode == RESULT_OK && data != null) {
            TargetService.start(this, BackendKind.PROJECTION, result.resultCode, data)
        } else {
            toast("Screen capture was not approved.")
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        WindowCompat.setDecorFitsSystemWindows(window, false)
        setContentView(buildUi())
        lifecycleScope.launch {
            repeatOnLifecycle(Lifecycle.State.STARTED) { Serving.state.collect(::renderServing) }
        }
        handleIntent(intent)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handleIntent(intent)
    }

    override fun onStart() {
        super.onStart()
        storeListener = app.peers.addListener { runOnUiThread { renderPeers() } }
        renderIdentity()
        renderPeers()
        renderServing(Serving.state.value)
    }

    override fun onStop() {
        storeListener?.close()
        storeListener = null
        super.onStop()
    }

    private fun handleIntent(intent: Intent?) {
        val data = intent?.data ?: return
        if (data.scheme == "scterm") showPairDialog(data.toString())
    }

    // -------------------------------------------------------------------- UI

    private fun buildUi(): View {
        val column = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(16), dp(16), dp(16), dp(16))
        }
        column.addView(text(getString(R.string.app_name), 26f, Typeface.BOLD))
        column.addView(text("Serve this device to paired peers, or control theirs.", 14f, color = R.color.muted).apply {
            setPadding(0, 0, 0, dp(16))
        })

        nameView = text("", 17f, Typeface.BOLD)
        identityView = text("", 13f, color = R.color.muted)
        addressView = text("", 13f, color = R.color.muted)
        column.addView(card(
            getString(R.string.section_this_device),
            nameView, identityView, addressView,
            button("Rename") { showRenameDialog() },
        ))

        backendGroup = RadioGroup(this).apply {
            val chosen = app.serveBackend
            for ((kind, label) in listOf(BackendKind.PROJECTION to R.string.backend_projection, BackendKind.HELPER to R.string.backend_helper)) {
                addView(RadioButton(context).apply {
                    id = View.generateViewId()
                    text = getString(label)
                    tag = kind
                    isChecked = kind == chosen
                    // Generated ids differ per instance: the preference, not view state, restores the choice.
                    isSaveEnabled = false
                })
            }
            setOnCheckedChangeListener { group, checkedId ->
                (group.findViewById<RadioButton>(checkedId)?.tag as? BackendKind)?.let { app.serveBackend = it }
            }
        }
        serveStatus = text("", 14f)
        serveButton = button(getString(R.string.serve_start)) { toggleServing() }
        inviteButton = button(getString(R.string.serve_invite)) { showInviteDialog() }
        kickButton = button(getString(R.string.serve_kick)) { TargetService.disconnectAll(this) }
        inputButton = button(getString(R.string.serve_enable_input)) { startActivity(Intent(Settings.ACTION_ACCESSIBILITY_SETTINGS)) }
        helperCommand = text("", 12f).apply {
            typeface = Typeface.MONOSPACE
            setTextIsSelectable(true)
        }
        helperBox = column(
            text("Run once from a computer with adb (the phone connected over USB or wireless debugging):", 13f, color = R.color.muted),
            helperCommand,
            button(getString(R.string.copy)) { copy("scterm helper command", helperCommand.text, sensitive = false) },
        )
        column.addView(card(
            getString(R.string.section_serve),
            backendGroup, serveStatus, row(serveButton, inviteButton), row(kickButton, inputButton), helperBox,
        ))

        peersList = column()
        column.addView(card(getString(R.string.section_peers), peersList, button(getString(R.string.pair_with)) { showPairDialog(null) }))

        return ScrollView(this).apply {
            addView(column)
            ViewCompat.setOnApplyWindowInsetsListener(this) { view, insets ->
                val bars = insets.getInsets(WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.ime())
                view.setPadding(bars.left, bars.top, bars.right, bars.bottom)
                insets
            }
        }
    }

    private fun renderIdentity() {
        nameView.text = app.deviceName
        identityView.text = getString(R.string.identity_format, app.identity.fingerprint.short)
        val addresses = Net.localAddresses()
        addressView.text = if (addresses.isEmpty()) getString(R.string.no_address) else getString(R.string.address_format, addresses.joinToString())
    }

    private fun renderServing(state: ServeState) {
        val kind = (state as? ServeState.Serving)?.kind ?: (state as? ServeState.Starting)?.kind
        for (i in 0 until backendGroup.childCount) {
            val radio = backendGroup.getChildAt(i) as RadioButton
            radio.isEnabled = state is ServeState.Stopped || state is ServeState.Failed
            if (kind != null) radio.isChecked = radio.tag == kind
        }
        inviteButton.visibility = View.GONE
        kickButton.visibility = View.GONE
        inputButton.visibility = View.GONE
        helperBox.visibility = View.GONE
        when (state) {
            ServeState.Stopped -> {
                serveStatus.text = getString(R.string.serve_not_serving)
                serveButton.text = getString(R.string.serve_start)
                serveButton.isEnabled = true
            }
            is ServeState.Starting -> {
                serveStatus.text = getString(R.string.serve_starting)
                serveButton.isEnabled = false
            }
            is ServeState.Failed -> {
                serveStatus.text = getString(R.string.serve_failed, state.message)
                serveButton.text = getString(R.string.serve_start)
                serveButton.isEnabled = true
            }
            is ServeState.Serving -> {
                val address = Net.localAddresses().firstOrNull() ?: "this device"
                serveStatus.text = buildString {
                    append(state.status).append("\nListening on ").append(address).append(':').append(state.port)
                    if (state.sessions.isEmpty()) {
                        append("\nNo one is connected.")
                    } else {
                        for (s in state.sessions) {
                            append("\n• ").append(s.peerName).append(" (").append(s.address).append(")")
                            append(if (s.holdsLease) " controlling" else " viewing")
                        }
                    }
                }
                serveButton.text = getString(R.string.serve_stop)
                serveButton.isEnabled = true
                inviteButton.visibility = View.VISIBLE
                if (state.sessions.isNotEmpty()) kickButton.visibility = View.VISIBLE
                if (state.kind == BackendKind.PROJECTION && !RemoteInputService.isEnabled(this)) inputButton.visibility = View.VISIBLE
                state.helperCommand?.takeIf { !state.ready }?.let {
                    helperCommand.text = it
                    helperBox.visibility = View.VISIBLE
                }
            }
        }
    }

    private fun renderPeers() {
        peersList.removeAllViews()
        val peers = app.peers.all().sortedBy { it.name.lowercase() }
        if (peers.isEmpty()) peersList.addView(text("No paired devices yet.", 14f, color = R.color.muted))
        for (peer in peers) peersList.addView(peerRow(peer))
    }

    private fun peerRow(peer: PeerRecord): View {
        val inbound = peer.inboundGrants
        val lines = mutableListOf<View>(
            text(peer.name, 16f, Typeface.BOLD),
            text("Identity ${peer.id.short}", 12f, color = R.color.muted),
            text(
                if (inbound.isEmpty()) "Cannot connect to this device" else "May ${describe(inbound)} on this device",
                13f,
                color = R.color.muted,
            ),
        )
        peer.target?.let { t ->
            lines += text("Serves at ${t.host}:${t.port}; allows this device to ${describe(Grant.parse(peer.grantedByPeer))}", 13f, color = R.color.muted)
        }
        val buttons = mutableListOf<View>()
        if (peer.target != null) buttons += button(getString(R.string.connect)) { connect(peer) }
        buttons += button("Manage…") { showManageDialog(peer) }
        lines += row(*buttons.toTypedArray())
        return column(*lines.toTypedArray()).apply { setPadding(0, dp(8), 0, dp(8)) }
    }

    // --------------------------------------------------------------- actions

    private fun toggleServing() {
        when (Serving.state.value) {
            is ServeState.Serving, is ServeState.Starting -> TargetService.stop(this)
            else -> startServing()
        }
    }

    private fun startServing() = withNetworkPermissions(serving = true) {
        when (backendGroup.findViewById<RadioButton>(backendGroup.checkedRadioButtonId)?.tag) {
            BackendKind.HELPER -> TargetService.start(this, BackendKind.HELPER)
            else -> {
                val manager = getSystemService(MediaProjectionManager::class.java)
                // Whole-display capture: a single shared app window has no reliable
                // screen mapping, so remote touches could not be placed correctly.
                val consent = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
                    manager.createScreenCaptureIntent(MediaProjectionConfig.createConfigForDefaultDisplay())
                } else {
                    manager.createScreenCaptureIntent()
                }
                projectionConsent.launch(consent)
            }
        }
    }

    private fun connect(peer: PeerRecord) = withNetworkPermissions(serving = false) {
        startActivity(ViewerActivity.intent(this, peer))
    }

    private fun withNetworkPermissions(serving: Boolean, action: () -> Unit) {
        val missing = Permissions.missingForNetworking(this, serving)
        if (missing.isEmpty()) {
            action()
        } else {
            pendingAfterPermissions = action
            permissionRequest.launch(missing.toTypedArray())
        }
    }

    private fun showRenameDialog() {
        val input = EditText(this).apply {
            setText(app.deviceName)
            isSingleLine = true
        }
        AlertDialog.Builder(this)
            .setTitle("Device name")
            .setMessage("Shown to paired devices.")
            .setView(padded(input))
            .setPositiveButton(R.string.done) { _, _ ->
                app.deviceName = input.text.toString()
                renderIdentity()
            }
            .setNegativeButton(R.string.cancel, null)
            .show()
    }

    private fun showInviteDialog() {
        val checks = Grant.entries.associateWith { grant ->
            CheckBox(this).apply {
                text = getString(grantLabel(grant))
                isChecked = grant != Grant.CLIPBOARD
            }
        }
        AlertDialog.Builder(this)
            .setTitle("Invite a device")
            .setMessage("Choose what the invited device may do here. You can change or revoke it later.")
            .setView(padded(column(*checks.values.toTypedArray())))
            .setPositiveButton("Create invitation") { _, _ ->
                val grants = checks.filterValues { it.isChecked }.keys
                val host = Net.localAddresses().firstOrNull()
                val invitation = host?.let { Serving.invite(it, grants) }
                if (invitation == null) toast("Not serving on a network.") else showInvitation(invitation)
            }
            .setNegativeButton(R.string.cancel, null)
            .show()
    }

    private fun showInvitation(invitation: Invitation) {
        val code = text(invitation.manualText, 20f, Typeface.BOLD).apply {
            typeface = Typeface.MONOSPACE
            setTextIsSelectable(true)
            gravity = Gravity.CENTER
        }
        AlertDialog.Builder(this)
            .setTitle("Invitation")
            .setMessage("On the other device choose \"${getString(R.string.pair_with)}\" and enter this. It works once, for 10 minutes.")
            .setView(padded(code))
            .setPositiveButton(R.string.done, null)
            .setNeutralButton(R.string.copy) { _, _ -> copy("scterm invitation", invitation.manualText, sensitive = true) }
            .setNegativeButton(R.string.share) { _, _ ->
                startActivity(
                    Intent.createChooser(
                        Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, invitation.toUri()),
                        getString(R.string.share),
                    ),
                )
            }
            .show()
    }

    private fun showPairDialog(prefill: String?) {
        val input = EditText(this).apply {
            hint = "192.168.1.20:${Invitation.DEFAULT_PORT} ABCD-EFGH-JKMN-PQRS"
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS or InputType.TYPE_TEXT_FLAG_MULTI_LINE
            prefill?.let(::setText)
        }
        val allowBack = CheckBox(this).apply { text = getString(R.string.pair_allow_back) }
        AlertDialog.Builder(this)
            .setTitle(R.string.pair_with)
            .setMessage("Enter the invitation shown on the device you want to control.")
            .setView(padded(column(input, allowBack)))
            .setPositiveButton(R.string.pair) { _, _ -> pair(input.text.toString(), allowBack.isChecked) }
            .setNegativeButton(R.string.cancel, null)
            .show()
    }

    private fun pair(text: String, allowBack: Boolean) {
        val invitation = try {
            Invitation.parse(text)
        } catch (e: IllegalArgumentException) {
            return toast(e.message ?: "Not an invitation")
        }
        withNetworkPermissions(serving = false) {
            toast("Pairing…")
            val servePort = (Serving.state.value as? ServeState.Serving)?.port
            thread(name = "pairing", isDaemon = true) {
                try {
                    val result = ControllerClient(app.identity, app.clientInfo).pair(invitation, servePort)
                    val now = System.currentTimeMillis()
                    app.peers.update(result.fingerprint) { old ->
                        (old ?: PeerRecord(result.fingerprint.hex, result.device.name, pairedAtMs = now)).copy(
                            name = result.device.name,
                            target = TargetAddress(invitation.host, invitation.port),
                            grantedByPeer = Grant.toWire(result.grants),
                            inbound = if (allowBack) Grant.toWire(Grant.FULL) else old?.inbound ?: emptyList(),
                            lastSeenMs = now,
                        )
                    }
                    runOnUiThread { toast("Paired with ${result.device.name}") }
                } catch (e: Exception) {
                    runOnUiThread { alert("Pairing failed", describePairingFailure(e)) }
                }
            }
        }
    }

    private fun showManageDialog(peer: PeerRecord) {
        AlertDialog.Builder(this)
            .setTitle(peer.name)
            .setItems(arrayOf(getString(R.string.edit_address), getString(R.string.edit_access), getString(R.string.forget))) { _, which ->
                when (which) {
                    0 -> showAddressDialog(peer)
                    1 -> showAccessDialog(peer)
                    else -> AlertDialog.Builder(this)
                        .setTitle("Forget ${peer.name}?")
                        .setMessage("Its access here ends now, including any live session. Both devices must pair again to reconnect.")
                        .setPositiveButton(R.string.forget) { _, _ -> app.peers.remove(peer.id) }
                        .setNegativeButton(R.string.cancel, null)
                        .show()
                }
            }
            .show()
    }

    private fun showAddressDialog(peer: PeerRecord) {
        val input = EditText(this).apply {
            setText(peer.target?.let { "${it.host}:${it.port}" } ?: "")
            hint = "192.168.1.20:${Invitation.DEFAULT_PORT}"
            isSingleLine = true
        }
        AlertDialog.Builder(this)
            .setTitle(R.string.edit_address)
            .setView(padded(input))
            .setPositiveButton(R.string.done) { _, _ ->
                try {
                    val (host, port) = Invitation.parseAddress(input.text.toString())
                    app.peers.update(peer.id) { it?.copy(target = TargetAddress(host, port)) }
                } catch (e: IllegalArgumentException) {
                    toast(e.message ?: "Invalid address")
                }
            }
            .setNegativeButton(R.string.cancel, null)
            .show()
    }

    private fun showAccessDialog(peer: PeerRecord) {
        val current = peer.inboundGrants
        val checks = Grant.entries.associateWith { grant ->
            CheckBox(this).apply {
                text = getString(grantLabel(grant))
                isChecked = grant in current
            }
        }
        AlertDialog.Builder(this)
            .setTitle(getString(R.string.edit_access))
            .setMessage("Reducing access ends its live session here.")
            .setView(padded(column(*checks.values.toTypedArray())))
            .setPositiveButton(R.string.done) { _, _ ->
                val grants = checks.filterValues { it.isChecked }.keys
                app.peers.update(peer.id) { it?.copy(inbound = Grant.toWire(grants)) }
            }
            .setNegativeButton(R.string.cancel, null)
            .show()
    }

    // --------------------------------------------------------------- helpers

    private fun describePairingFailure(e: Exception): String = when {
        e is PeerException && e.code == RejectCodes.BAD_PROOF -> "The code was wrong, or the invitation was already used. Create a new invitation."
        e is PeerException && e.code == RejectCodes.NO_INVITATION -> "That device is not waiting to pair (the invitation expired or was replaced)."
        e is SSLException -> "The device at that address is not the one that created the invitation."
        e is ConnectException || e is SocketTimeoutException -> "Could not reach the device. Check the address and that both are on the same network."
        else -> e.message ?: e.javaClass.simpleName
    }

    private fun describe(grants: Set<Grant>): String {
        if (grants.isEmpty()) return "nothing"
        return grants.joinToString(", ") {
            when (it) {
                Grant.VIEW -> "view"
                Grant.AUDIO -> "hear"
                Grant.CONTROL -> "control"
                Grant.CLIPBOARD -> "share the clipboard"
            }
        }
    }

    private fun grantLabel(grant: Grant): Int = when (grant) {
        Grant.VIEW -> R.string.grant_view
        Grant.AUDIO -> R.string.grant_audio
        Grant.CONTROL -> R.string.grant_control
        Grant.CLIPBOARD -> R.string.grant_clipboard
    }

    private fun copy(label: String, value: CharSequence, sensitive: Boolean) {
        val clip = ClipData.newPlainText(label, value)
        if (sensitive && Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            clip.description.extras = PersistableBundle().apply { putBoolean(ClipDescription.EXTRA_IS_SENSITIVE, true) }
        }
        getSystemService(ClipboardManager::class.java)?.setPrimaryClip(clip)
        toast("Copied")
    }

    private fun alert(title: String, message: String) {
        AlertDialog.Builder(this).setTitle(title).setMessage(message).setPositiveButton(R.string.done, null).show()
    }

    private fun toast(message: String) = Toast.makeText(this, message, Toast.LENGTH_SHORT).show()

    private fun dp(value: Int): Int =
        TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, value.toFloat(), resources.displayMetrics).toInt()

    private fun text(value: CharSequence, size: Float, style: Int = Typeface.NORMAL, color: Int? = null) = TextView(this).apply {
        text = value
        textSize = size
        setTypeface(typeface, style)
        color?.let { setTextColor(ContextCompat.getColor(context, it)) }
        setPadding(0, dp(2), 0, dp(2))
    }

    private fun button(label: String, onClick: () -> Unit) = Button(this).apply {
        text = label
        isAllCaps = false
        setOnClickListener { onClick() }
    }

    private fun column(vararg views: View) = LinearLayout(this).apply {
        orientation = LinearLayout.VERTICAL
        views.forEach { addView(it) }
    }

    private fun row(vararg views: View) = LinearLayout(this).apply {
        orientation = LinearLayout.HORIZONTAL
        views.forEach {
            addView(it, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f).apply { marginEnd = dp(8) })
        }
    }

    private fun card(title: String, vararg children: View) = LinearLayout(this).apply {
        orientation = LinearLayout.VERTICAL
        setBackgroundColor(ContextCompat.getColor(context, R.color.card))
        setPadding(dp(16), dp(12), dp(16), dp(12))
        layoutParams = LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
            bottomMargin = dp(12)
        }
        addView(text(title.uppercase(), 13f, Typeface.BOLD, R.color.accent))
        children.forEach { addView(it) }
    }

    private fun padded(view: View) = LinearLayout(this).apply {
        setPadding(dp(20), dp(8), dp(20), 0)
        addView(view, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))
    }
}
