package io.github.pwnedbygary.scterm.viewer

import androidx.annotation.StringRes
import io.github.pwnedbygary.scterm.R
import io.github.pwnedbygary.scterm.protocol.DeviceAction
import io.github.pwnedbygary.scterm.protocol.LocalAction

/**
 * A button of the viewer's action bar: the action [id] of the catalog every
 * frontend shares (protocol/fixtures/actions.json), showing its short name, or
 * its one-word label where the short name is clipped (Rec, Rot, Notif, Keys,
 * Shot), and read out by its label ([name]).
 */
class BarAction(val id: String, @StringRes val text: Int, @StringRes val description: Int) {
    /**
     * The label, after the shown text where the label does not contain it
     * ("Vol-, Volume down"): voice control matches what the user says, which is
     * what the button shows (WCAG 2.5.3, label in name).
     */
    fun name(string: (Int) -> String): String {
        val shown = string(text)
        val label = string(description)
        return if (label.contains(shown, ignoreCase = true)) label else "$shown, $label"
    }
}

/**
 * The bar in order: the navigation keys as on the phone itself, the keyboard,
 * the other keys, rotation and the panels, then the viewer's own screenshot and
 * refresh. Take control and Disconnect follow; they belong to the session, not
 * the catalog.
 */
val VIEWER_BAR: List<BarAction> = listOf(
    BarAction(DeviceAction.BACK.id, R.string.action_back_short, R.string.action_back_label),
    BarAction(DeviceAction.HOME.id, R.string.action_home_short, R.string.action_home_label),
    BarAction(DeviceAction.APP_SWITCH.id, R.string.action_appswitch_label, R.string.action_appswitch_label),
    BarAction(LocalAction.KEYBOARD.id, R.string.action_keyboard_label, R.string.action_keyboard_label),
    BarAction(DeviceAction.VOLUME_DOWN.id, R.string.action_voldown_short, R.string.action_voldown_label),
    BarAction(DeviceAction.VOLUME_UP.id, R.string.action_volup_short, R.string.action_volup_label),
    BarAction(DeviceAction.MUTE.id, R.string.action_mute_short, R.string.action_mute_label),
    BarAction(DeviceAction.POWER.id, R.string.action_power_short, R.string.action_power_label),
    BarAction(DeviceAction.MENU.id, R.string.action_menu_short, R.string.action_menu_label),
    BarAction(DeviceAction.ROTATE.id, R.string.action_rotate_label, R.string.action_rotate_label),
    BarAction(DeviceAction.NOTIFICATIONS.id, R.string.action_notif_label, R.string.action_notif_label),
    BarAction(DeviceAction.SETTINGS.id, R.string.action_settings_short, R.string.action_settings_label),
    BarAction(DeviceAction.COLLAPSE.id, R.string.action_collapse_short, R.string.action_collapse_label),
    BarAction(LocalAction.SCREENSHOT.id, R.string.action_screenshot_label, R.string.action_screenshot_label),
    BarAction(DeviceAction.RESET_VIDEO.id, R.string.action_resetvideo_short, R.string.action_resetvideo_label),
)
