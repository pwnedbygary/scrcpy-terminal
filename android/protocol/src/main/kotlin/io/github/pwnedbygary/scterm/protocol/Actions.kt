package io.github.pwnedbygary.scterm.protocol

/**
 * Device actions every frontend offers: TUI F-keys and Alt mnemonics, the web
 * action bar, the Android toolbar. Mirrors appkeys.go; both are pinned to
 * protocol/fixtures/actions.json so the tables cannot drift apart.
 */
enum class DeviceAction(val id: String, val mnemonic: Char?, val fKey: Int?, val keycode: Int?) {
    HOME("home", 'h', 1, AndroidInput.KEYCODE_HOME),
    MENU("menu", null, 2, AndroidInput.KEYCODE_MENU),
    APP_SWITCH("appswitch", 't', 3, AndroidInput.KEYCODE_APP_SWITCH),
    POWER("power", 'p', 4, AndroidInput.KEYCODE_POWER),
    VOLUME_DOWN("voldown", 'd', 5, AndroidInput.KEYCODE_VOLUME_DOWN),
    VOLUME_UP("volup", 'u', 6, AndroidInput.KEYCODE_VOLUME_UP),

    /** Speaker mute (164). KEYCODE_MUTE (91) would toggle the microphone. */
    MUTE("mute", null, 7, AndroidInput.KEYCODE_VOLUME_MUTE),
    ROTATE("rotate", 'r', 8, null),
    NOTIFICATIONS("notif", 'n', 9, null),
    SETTINGS("settings", 'e', 10, null),
    COLLAPSE("collapse", 'c', 11, null),
    BACK("back", 'b', null, null),
    RESET_VIDEO("resetvideo", 'k', null, null);

    /** The exact control messages this action sends, in order. */
    fun messages(): List<ControlMessage> {
        keycode?.let {
            return listOf(
                ControlMessage.InjectKeycode(AndroidInput.KEY_ACTION_DOWN, it, 0, 0),
                ControlMessage.InjectKeycode(AndroidInput.KEY_ACTION_UP, it, 0, 0),
            )
        }
        return when (this) {
            BACK -> listOf(
                ControlMessage.BackOrScreenOn(AndroidInput.KEY_ACTION_DOWN),
                ControlMessage.BackOrScreenOn(AndroidInput.KEY_ACTION_UP),
            )
            ROTATE -> listOf(ControlMessage.RotateDevice)
            NOTIFICATIONS -> listOf(ControlMessage.ExpandNotificationPanel)
            SETTINGS -> listOf(ControlMessage.ExpandSettingsPanel)
            COLLAPSE -> listOf(ControlMessage.CollapsePanels)
            RESET_VIDEO -> listOf(ControlMessage.ResetVideo)
            else -> error("action $id has neither a keycode nor a control message")
        }
    }

    companion object {
        fun byId(id: String): DeviceAction? = entries.firstOrNull { it.id == id }

        fun byMnemonic(c: Char): DeviceAction? = entries.firstOrNull { it.mnemonic == c.lowercaseChar() }
    }
}

/** Frontend-local actions sharing the mnemonic/F-key space; they send nothing. */
enum class LocalAction(val id: String, val mnemonic: Char?, val fKey: Int?) {
    GRAB("grab", 'g', 12),
    KEYBOARD("keyboard", 'i', null),
    SCREENSHOT("screenshot", 's', null),
    QUIT("quit", 'q', null),
    TOOLBAR("toolbar", '/', null),
}
