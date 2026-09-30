package io.github.pwnedbygary.scterm.ui

import android.app.AlertDialog
import android.content.ActivityNotFoundException
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
import android.text.format.DateFormat
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
import androidx.core.net.toUri
import androidx.core.view.ViewCompat
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import io.github.pwnedbygary.scterm.BuildConfig
import io.github.pwnedbygary.scterm.R
import io.github.pwnedbygary.scterm.ScTermApp
import io.github.pwnedbygary.scterm.peer.ControllerClient
import io.github.pwnedbygary.scterm.peer.NearbyPairing
import io.github.pwnedbygary.scterm.peer.PeerException
import io.github.pwnedbygary.scterm.peer.PeerRecord
import io.github.pwnedbygary.scterm.peer.TargetAddress
import io.github.pwnedbygary.scterm.peer.TargetServer
import io.github.pwnedbygary.scterm.protocol.Grant
import io.github.pwnedbygary.scterm.protocol.Invitation
import io.github.pwnedbygary.scterm.protocol.ProtocolException
import io.github.pwnedbygary.scterm.protocol.RejectCodes
import io.github.pwnedbygary.scterm.protocol.ShortCode
import io.github.pwnedbygary.scterm.target.BackendKind
import io.github.pwnedbygary.scterm.target.RemoteInputService
import io.github.pwnedbygary.scterm.target.ServeState
import io.github.pwnedbygary.scterm.target.Serving
import io.github.pwnedbygary.scterm.target.ShizukuActivation
import io.github.pwnedbygary.scterm.target.TargetService
import io.github.pwnedbygary.scterm.update.AppUpdates
import io.github.pwnedbygary.scterm.util.Nearby
import io.github.pwnedbygary.scterm.util.Net
import io.github.pwnedbygary.scterm.util.Permissions
import io.github.pwnedbygary.scterm.viewer.ViewerActivity
import kotlinx.coroutines.launch
import java.net.ConnectException
import java.net.SocketTimeoutException
import java.util.Date
import java.util.concurrent.CancellationException
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
    private lateinit var shizukuButton: Button
    private var helperShellCommand: String? = null
    private lateinit var peersList: LinearLayout

    private var pendingAfterPermissions: (() -> Unit)? = null
    private var storeListener: AutoCloseable? = null

    private lateinit var versionView: TextView
    private lateinit var updateButton: Button
    private var availableUpdate: AppUpdates.Release? = null
    private var awaitingInstallPermission: AppUpdates.Release? = null

    private var invitationExpiresAtMs: Long? = null
    private var invitationDialog: AlertDialog? = null
    private var requestDialog: AlertDialog? = null
    private var requestShown: TargetServer.PairingRequest? = null
    private var nearbyPairing: NearbyPairing? = null

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
            repeatOnLifecycle(Lifecycle.State.STARTED) {
                launch { Serving.state.collect(::renderServing) }
                launch { Serving.invitationExpiresAtMs.collect(::renderInvitation) }
                launch { Serving.pairingRequest.collect(::renderPairingRequest) }
            }
        }
        handleIntent(intent)
    }

    override fun onDestroy() {
        // Dialogs die with this window; a pairing waiting on one cannot finish.
        nearbyPairing?.cancel()
        invitationDialog?.dismiss()
        requestDialog?.dismiss()
        super.onDestroy()
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handleIntent(intent)
    }

    override fun onStart() {
        super.onStart()
        Serving.uiVisible = true
        storeListener = app.peers.addListener { runOnUiThread { renderPeers() } }
        renderIdentity()
        renderPeers()
        renderServing(Serving.state.value)
        renderUpdate()
        // Back from the "install unknown apps" setting.
        awaitingInstallPermission?.takeIf { packageManager.canRequestPackageInstalls() }?.let(::startUpdate)
        maybeCheckForUpdate()
    }

    override fun onStop() {
        Serving.uiVisible = false
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
        versionView = text("", 13f, color = R.color.muted)
        updateButton = button(getString(R.string.update_check)) { onUpdateButton() }
        column.addView(card(
            getString(R.string.section_this_device),
            nameView, identityView, addressView, versionView,
            row(button("Rename") { showRenameDialog() }, updateButton),
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
        shizukuButton = button(getString(R.string.start_with_shizuku)) { startHelperWithShizuku() }
        helperBox = column(
            text("Run once from a computer with adb (the phone connected over USB or wireless debugging), or start it with Shizuku if it runs on this device:", 13f, color = R.color.muted),
            helperCommand,
            row(button(getString(R.string.copy)) { copy("scterm helper command", helperCommand.text, sensitive = false) }, shizukuButton),
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
                    invitationExpiresAtMs?.let {
                        append("\nInvitation open until ").append(DateFormat.getTimeFormat(this@MainActivity).format(Date(it)))
                        append(": nearby devices can find this one.")
                    }
                }
                serveButton.text = getString(R.string.serve_stop)
                serveButton.isEnabled = true
                inviteButton.visibility = View.VISIBLE
                if (state.sessions.isNotEmpty()) kickButton.visibility = View.VISIBLE
                if (state.kind == BackendKind.PROJECTION && !RemoteInputService.isEnabled(this)) inputButton.visibility = View.VISIBLE
                state.helperCommand?.takeIf { !state.ready }?.let {
                    helperCommand.text = it
                    helperShellCommand = state.helperShellCommand
                    shizukuButton.visibility = if (ShizukuActivation.installed(this)) View.VISIBLE else View.GONE
                    helperBox.visibility = View.VISIBLE
                }
            }
        }
    }

    private fun renderInvitation(expiresAtMs: Long?) {
        invitationExpiresAtMs = expiresAtMs
        if (expiresAtMs == null) {
            // Used, cancelled or expired: its code is useless now.
            invitationDialog?.dismiss()
            invitationDialog = null
        }
        renderServing(Serving.state.value)
    }

    private fun renderPairingRequest(request: TargetServer.PairingRequest?) {
        if (request === requestShown) return
        requestDialog?.dismiss()
        requestDialog = null
        requestShown = request
        if (request == null) return
        requestDialog = AlertDialog.Builder(this)
            .setTitle(getString(R.string.pairing_request_title, request.peerName))
            .setMessage("Accept only if ${request.peerName} shows this same code.")
            .setView(padded(codeView(request.code)))
            .setPositiveButton(R.string.pairing_accept) { _, _ ->
                request.accept()
                toast("Waiting for ${request.peerName} to confirm…")
            }
            .setNegativeButton(R.string.pairing_decline) { _, _ -> request.decline() }
            .setCancelable(false)
            .show()
    }

    private fun renderPeers() {
        peersList.removeAllViews()
        val peers = app.peers.all().sortedBy { it.name.lowercase() }
        if (peers.isEmpty()) peersList.addView(text("No paired devices yet.", 14f, color = R.color.muted))
        for (peer in peers) peersList.addView(peerRow(peer))
    }

    private fun peerRow(peer: PeerRecord): View {
        val lines = mutableListOf<View>(
            text(peer.name, 16f, Typeface.BOLD),
            text("Identity ${peer.id.short}", 12f, color = R.color.muted),
        )
        // Name who acts on whom in every line: under a peer's name, a bare "this device" reads both ways.
        peer.target?.let { t ->
            val granted = Grant.parse(peer.grantedByPeer)
            lines += text("Address ${t.host}:${t.port}", 13f, color = R.color.muted)
            lines += text(
                if (granted.isEmpty()) "It gives you no access." else "You can ${describeAccess(granted, theirs = true)}.",
                13f,
                color = R.color.muted,
            )
        }
        val inbound = peer.inboundGrants
        lines += text(
            if (inbound.isEmpty()) "It can't connect to your device." else "It can ${describeAccess(inbound, theirs = false)}.",
            13f,
            color = R.color.muted,
        )
        val buttons = mutableListOf<View>()
        if (peer.target != null) buttons += button(getString(R.string.connect)) { connect(peer) }
        buttons += button("Manage…") { showManageDialog(peer) }
        lines += row(*buttons.toTypedArray())
        return column(*lines.toTypedArray()).apply { setPadding(0, dp(8), 0, dp(8)) }
    }

    // --------------------------------------------------------------- actions

    private fun startHelperWithShizuku() {
        val command = helperShellCommand ?: return
        shizukuButton.isEnabled = false
        ShizukuActivation.start(this, command) { error ->
            shizukuButton.isEnabled = true
            if (error == null) toast("Starting the helper through Shizuku…") else alert("Shizuku", error)
        }
    }

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
        val body = column(
            text(
                "On the other device choose \"${getString(R.string.pair_with)}\": this device is listed there under " +
                    "${getString(R.string.pairing_nearby)}. Or enter this there:",
                14f,
            ),
            code,
            text("It works once, for 10 minutes.", 13f, color = R.color.muted),
            row(
                button(getString(R.string.copy)) { copy("scterm invitation", invitation.manualText, sensitive = true) },
                button(getString(R.string.share)) {
                    startActivity(
                        Intent.createChooser(
                            Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, invitation.toUri()),
                            getString(R.string.share),
                        ),
                    )
                },
            ),
        )
        invitationDialog?.dismiss()
        invitationDialog = AlertDialog.Builder(this)
            .setTitle("Invitation")
            .setView(padded(body))
            .setPositiveButton(R.string.hide, null)
            .setNegativeButton(R.string.stop_inviting) { _, _ -> Serving.cancelInvitation() }
            .show()
    }

    private fun showPairDialog(prefill: String?) = withNetworkPermissions(serving = false) {
        val input = EditText(this).apply {
            hint = "192.168.1.20:${Invitation.DEFAULT_PORT} ABCD-EFGH-JKMN-PQRS"
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS or InputType.TYPE_TEXT_FLAG_MULTI_LINE
            prefill?.let(::setText)
        }
        val allowBack = CheckBox(this).apply { text = getString(R.string.pair_allow_back) }
        val searching = text(
            "Searching this network. On the device you want to control, tap \"${getString(R.string.serve_start)}\", then " +
                "\"${getString(R.string.serve_invite)}\": it appears here while its invitation is open.",
            14f,
            color = R.color.muted,
        )
        val nearbyList = column(searching)
        lateinit var dialog: AlertDialog
        val browser = Nearby.Browser(this) { services ->
            nearbyList.removeAllViews()
            if (services.isEmpty()) nearbyList.addView(searching)
            for (service in services) {
                nearbyList.addView(button(service.name) {
                    dialog.dismiss()
                    pairNearby(service, allowBack.isChecked)
                })
            }
        }
        val body = column(
            sectionLabel(getString(R.string.pairing_nearby)),
            nearbyList,
            sectionLabel(getString(R.string.pairing_enter_invitation)).apply { setPadding(0, dp(16), 0, dp(2)) },
            input,
            allowBack,
        )
        dialog = AlertDialog.Builder(this)
            .setTitle(R.string.pair_with)
            .setView(ScrollView(this).apply { addView(padded(body)) })
            .setPositiveButton(R.string.pair) { _, _ ->
                if (input.text.isBlank()) toast("Pick a nearby device, or enter an invitation.") else pair(input.text.toString(), allowBack.isChecked)
            }
            .setNegativeButton(R.string.cancel, null)
            .setOnDismissListener { browser.stop() }
            .show()
        browser.start()
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
                    savePairing(result, invitation.host, invitation.port, allowBack)
                    runOnUiThread { toast("Paired with ${result.device.name}") }
                } catch (e: Exception) {
                    runOnUiThread { alert("Pairing failed", describePairingFailure(e)) }
                }
            }
        }
    }

    /** Connects to a device found nearby; both screens then show a code to compare. */
    private fun pairNearby(service: Nearby.Service, allowBack: Boolean) {
        var cancelled = false
        val progress = AlertDialog.Builder(this)
            .setTitle(getString(R.string.pairing_request_title, service.name))
            .setMessage("Connecting…")
            .setNegativeButton(R.string.cancel) { _, _ -> cancelled = true }
            .setOnCancelListener { cancelled = true }
            .show()
        val servePort = (Serving.state.value as? ServeState.Serving)?.port
        Nearby.resolve(this, service) { address ->
            if (cancelled || isDestroyed) return@resolve
            if (address == null) {
                progress.dismiss()
                alert("Pairing failed", "${service.name} can no longer be found on this network.")
                return@resolve
            }
            val (host, port) = address
            thread(name = "pairing", isDaemon = true) {
                val started = runCatching { ControllerClient(app.identity, app.clientInfo).startNearbyPairing(host, port, servePort) }
                runOnUiThread {
                    progress.dismiss()
                    val pairing = started.getOrNull()
                    when {
                        cancelled || isDestroyed -> pairing?.cancel()
                        pairing != null -> confirmNearby(pairing, host, port, allowBack)
                        else -> alert("Pairing failed", describePairingFailure(started.exceptionOrNull()!!, nearby = true))
                    }
                }
            }
        }
    }

    private fun confirmNearby(pairing: NearbyPairing, host: String, port: Int, allowBack: Boolean) {
        val name = pairing.device.name
        nearbyPairing = pairing
        val status = text("Pair only if $name shows this same code, then accept there too.", 14f).apply { gravity = Gravity.CENTER }
        val dialog = AlertDialog.Builder(this)
            .setTitle(getString(R.string.pairing_request_title, name))
            .setView(padded(column(codeView(pairing.code), status)))
            .setPositiveButton(R.string.pair, null)
            .setNegativeButton(R.string.cancel, null)
            .setCancelable(false)
            .show()
        // Replaced listeners keep the dialog up while waiting for the other device.
        dialog.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener { button ->
            button.isEnabled = false
            status.text = getString(R.string.pairing_waiting, name)
            pairing.confirm()
        }
        dialog.getButton(AlertDialog.BUTTON_NEGATIVE).setOnClickListener { pairing.cancel() }
        pairing.result.whenComplete { result, error ->
            // Saved even if this screen is gone: the other device has already saved this one.
            result?.let { savePairing(it, host, port, allowBack) }
            runOnUiThread {
                if (nearbyPairing === pairing) nearbyPairing = null
                if (isDestroyed) return@runOnUiThread
                dialog.dismiss()
                when {
                    result != null -> toast("Paired with ${result.device.name}")
                    error != null && error !is CancellationException -> alert("Pairing failed", describePairingFailure(error, nearby = true))
                }
            }
        }
    }

    private fun savePairing(result: ControllerClient.PairResult, host: String, port: Int, allowBack: Boolean) {
        val now = System.currentTimeMillis()
        app.peers.update(result.fingerprint) { old ->
            (old ?: PeerRecord(result.fingerprint.hex, result.device.name, pairedAtMs = now)).copy(
                name = result.device.name,
                target = TargetAddress(host, port),
                grantedByPeer = Grant.toWire(result.grants),
                inbound = if (allowBack) Grant.toWire(Grant.FULL) else old?.inbound ?: emptyList(),
                lastSeenMs = now,
            )
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

    // --------------------------------------------------------------- updates

    private fun renderUpdate() {
        val update = availableUpdate
        versionView.text = buildString {
            append(if (AppUpdates.isReleaseBuild) getString(R.string.version_format, BuildConfig.VERSION_NAME) else getString(R.string.version_dev))
            if (update != null) append('\n').append(getString(R.string.update_available, update.version))
        }
        updateButton.text = if (update != null) getString(R.string.update_to, update.version) else getString(R.string.update_check)
    }

    /** Release builds look for a newer release at most once a day, and only say so. */
    private fun maybeCheckForUpdate() {
        val now = System.currentTimeMillis()
        if (!AppUpdates.isReleaseBuild || availableUpdate != null || now - app.lastUpdateCheckMs < UPDATE_CHECK_INTERVAL_MS) return
        app.lastUpdateCheckMs = now
        thread(name = "update-check", isDaemon = true) {
            val release = runCatching { AppUpdates.fetchLatest() }.getOrNull()
            runOnUiThread {
                if (release != null && release.versionCode > BuildConfig.VERSION_CODE && !isDestroyed) {
                    availableUpdate = release
                    renderUpdate()
                }
            }
        }
    }

    private fun onUpdateButton() {
        availableUpdate?.let { return confirmUpdate(it) }
        updateButton.isEnabled = false
        toast(getString(R.string.update_checking))
        thread(name = "update-check", isDaemon = true) {
            val result = runCatching { AppUpdates.fetchLatest() }
            runOnUiThread {
                if (isDestroyed) return@runOnUiThread
                updateButton.isEnabled = true
                app.lastUpdateCheckMs = System.currentTimeMillis()
                val release = result.getOrNull()
                when {
                    result.isFailure -> alert(getString(R.string.update_check), "Could not reach the release list: ${result.exceptionOrNull()?.message}")
                    release == null -> alert(getString(R.string.update_check), "The latest release has no Android app.")
                    release.versionCode <= BuildConfig.VERSION_CODE -> toast(getString(R.string.update_none, BuildConfig.VERSION_NAME))
                    else -> {
                        availableUpdate = release
                        renderUpdate()
                        confirmUpdate(release)
                    }
                }
            }
        }
    }

    private fun confirmUpdate(release: AppUpdates.Release) {
        val notes = release.notes.trim().let { if (it.length > 700) it.take(700).trimEnd() + "…" else it }
        AlertDialog.Builder(this)
            .setTitle(getString(R.string.update_title, release.version))
            .setMessage(buildString {
                append(getString(R.string.update_you_have, BuildConfig.VERSION_NAME))
                if (notes.isNotEmpty()) append("\n\n").append(notes)
                append("\n\n").append(getString(R.string.update_serving_note))
            })
            .setPositiveButton(R.string.update_install) { _, _ -> startUpdate(release) }
            .setNeutralButton(R.string.update_page) { _, _ -> openPage(release.page) }
            .setNegativeButton(R.string.update_later, null)
            .show()
    }

    private fun startUpdate(release: AppUpdates.Release) {
        if (!packageManager.canRequestPackageInstalls()) {
            awaitingInstallPermission = release
            AlertDialog.Builder(this)
                .setTitle(R.string.update_permission_title)
                .setMessage(R.string.update_permission_text)
                .setPositiveButton(R.string.update_permission_open) { _, _ ->
                    startActivity(Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, "package:$packageName".toUri()))
                }
                .setNegativeButton(R.string.cancel) { _, _ -> awaitingInstallPermission = null }
                .show()
            return
        }
        awaitingInstallPermission = null
        var cancelled = false
        val status = text(getString(R.string.update_downloading, release.version, 0), 14f)
        val dialog = AlertDialog.Builder(this)
            .setTitle(getString(R.string.update_title, release.version))
            .setView(padded(status))
            .setNegativeButton(R.string.cancel) { _, _ -> cancelled = true }
            .setCancelable(false)
            .show()
        thread(name = "update-download", isDaemon = true) {
            val outcome = runCatching {
                val apk = AppUpdates.download(this, release, progress = { p ->
                    runOnUiThread { status.text = getString(R.string.update_downloading, release.version, (p * 100).toInt()) }
                }, cancelled = { cancelled })
                AppUpdates.problemWith(this, apk)?.let { throw AppUpdates.Refused(it) }
                app.updatingTo = release.version
                AppUpdates.install(this, apk)
            }
            runOnUiThread {
                if (isDestroyed) return@runOnUiThread
                dialog.dismiss()
                val error = outcome.exceptionOrNull() ?: return@runOnUiThread
                app.updatingTo = null
                if (error is AppUpdates.Cancelled) return@runOnUiThread
                alert(
                    getString(R.string.update_failed_title),
                    if (error is AppUpdates.Refused) error.message.orEmpty() else "The update could not be downloaded: ${error.message}",
                )
            }
        }
    }

    private fun openPage(url: String) {
        try {
            startActivity(Intent(Intent.ACTION_VIEW, url.toUri()))
        } catch (_: ActivityNotFoundException) {
            toast(url)
        }
    }

    // --------------------------------------------------------------- helpers

    private fun describePairingFailure(e: Throwable, nearby: Boolean = false): String = when {
        e is PeerException && e.code == RejectCodes.DECLINED -> e.message.orEmpty().replaceFirstChar(Char::uppercaseChar) + "."
        e is PeerException && e.code == RejectCodes.UNAVAILABLE -> "That device is already pairing with another one. Try again in a minute."
        e is PeerException && e.code == RejectCodes.NO_INVITATION ->
            if (nearby) {
                "That device is no longer inviting. Create a new invitation on it."
            } else {
                "That device is not waiting to pair (the invitation expired or was replaced)."
            }
        nearby && (e is ProtocolException || e is PeerException && e.code == RejectCodes.BAD_PROOF) ->
            "That device did not follow the pairing protocol, so nothing was paired."
        e is PeerException && e.code == RejectCodes.BAD_PROOF -> "The code was wrong, or the invitation was already used. Create a new invitation."
        e is SSLException ->
            if (nearby) "Could not set up a secure connection with that device." else "The device at that address is not the one that created the invitation."
        e is ConnectException || e is SocketTimeoutException ->
            if (nearby) {
                "Could not reach that device. Check that both are on the same network."
            } else {
                "Could not reach the device. Check the address and that both are on the same network."
            }
        else -> e.message ?: e.javaClass.simpleName
    }

    /** A pairing code, large and grouped the same way on both screens. */
    private fun codeView(code: String) = text(ShortCode.display(code), 36f).apply {
        setTypeface(Typeface.MONOSPACE, Typeface.BOLD)
        gravity = Gravity.CENTER
        setPadding(0, dp(12), 0, dp(12))
    }

    private fun sectionLabel(title: String) = text(title.uppercase(), 13f, Typeface.BOLD, R.color.accent)

    /** "see its screen, hear its audio and control it", or the same about "your" device. */
    private fun describeAccess(grants: Set<Grant>, theirs: Boolean): String {
        val owner = if (theirs) "its" else "your"
        val phrases = Grant.entries.filter { it in grants }.map {
            when (it) {
                Grant.VIEW -> "see $owner screen"
                Grant.AUDIO -> "hear $owner audio"
                Grant.CONTROL -> if (theirs) "control it" else "control your device"
                Grant.CLIPBOARD -> "share $owner clipboard"
            }
        }
        return if (phrases.size < 2) phrases.joinToString() else phrases.dropLast(1).joinToString(", ") + " and " + phrases.last()
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

    private companion object {
        const val UPDATE_CHECK_INTERVAL_MS = 24 * 60 * 60 * 1000L
    }
}
