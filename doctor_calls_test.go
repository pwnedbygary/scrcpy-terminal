package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestDoctorRunsNothingOnOtherDevices: scterm doctor runs commands only on
// the device scterm would use, the one ANDROID_SERIAL names or else the only
// ready one, as another program may be using the others. They are listed as
// adb devices -l shows them. With several ready and none chosen, or an
// ANDROID_SERIAL that names none or several, it runs nothing on any device.
// The fake adb notes every call, so no call can name another device unseen.
// The devices are those of the first real-device run: a handheld over USB
// and over Wi-Fi, and the phone ANDROID_SERIAL named.
func TestDoctorRunsNothingOnOtherDevices(t *testing.T) {
	const three = devicesHeader +
		"49016109               device usb:1-4 product:RP6 model:Retroid_Pocket_6 device:RP6 transport_id:1\n" +
		"192.168.1.241:5555     device product:RP6 model:Retroid_Pocket_6 device:RP6 transport_id:2\n" +
		"192.168.1.251:37199    device product:NX769J model:NX769J device:NX769J transport_id:3\n"
	serials := []string{"49016109", "192.168.1.241:5555", "192.168.1.251:37199", "R5CT", "GT9"}
	versions := map[string]string{"49016109": "13 33", "192.168.1.241:5555": "13 33", "192.168.1.251:37199": "16 36", "R5CT": "14 34"}
	const unread = " is ready; scterm would not use it, so doctor runs nothing on it\n"
	const handhelds = "ok    device  49016109 (Retroid_Pocket_6)" + unread + "ok    device  192.168.1.241:5555 (Retroid_Pocket_6)" + unread
	const phone = "192.168.1.251:37199 (NX769J) is ready: Android 16 (SDK 36); sound and playback capture work"
	reads := func(serial string) []string {
		return []string{"version", "devices -l",
			"-s " + serial + " shell getprop ro.build.version.release", "-s " + serial + " shell getprop ro.build.version.sdk"}
	}
	listOnly := []string{"version", "devices -l"}
	for _, tc := range []struct {
		name, listing, serial string
		used                  string // the device commands may name, if any
		calls                 []string
		want                  string
	}{
		{"ANDROID_SERIAL names one of three ready devices", three, "192.168.1.251:37199", "192.168.1.251:37199",
			reads("192.168.1.251:37199"), handhelds + "ok    device  " + phone + "; ANDROID_SERIAL names it\n"},
		{"ANDROID_SERIAL names one of three by its model", three, "model:NX769J", "192.168.1.251:37199",
			reads("192.168.1.251:37199"), handhelds + "ok    device  " + phone + "; ANDROID_SERIAL=model:NX769J names it\n"},
		{"the only ready device", devicesHeader + "GT9\tunauthorized usb:1-2 transport_id:4\nR5CT\tdevice usb:1-1 model:Pixel_8 transport_id:5\n", "", "R5CT",
			reads("R5CT"),
			"warn  device  GT9 has not allowed USB debugging from this computer\n" +
				"              unlock it, accept the \"Allow USB debugging?\" prompt, and run scterm again\n" +
				"ok    device  R5CT (Pixel_8) is ready: Android 14 (SDK 34); sound and playback capture work\n"},
		{"several ready and none chosen", three, "", "", listOnly,
			handhelds + "ok    device  192.168.1.251:37199 (NX769J)" + unread +
				"warn  device  3 devices are connected (49016109, 192.168.1.241:5555, 192.168.1.251:37199)\n" +
				"              choose one with -s SERIAL, or set ANDROID_SERIAL\n"},
		{"an ambiguous ANDROID_SERIAL", three, "model:Retroid_Pocket_6", "", listOnly,
			handhelds + "ok    device  192.168.1.251:37199 (NX769J)" + unread +
				"FAIL  device  ANDROID_SERIAL is model:Retroid_Pocket_6, which names 2 devices (49016109, 192.168.1.241:5555), so adb takes none of them\n" +
				"              set it to the serial of the one to use\n"},
		{"an ANDROID_SERIAL that names no device", three, "R5CT", "", listOnly,
			handhelds + "ok    device  192.168.1.251:37199 (NX769J)" + unread +
				"FAIL  device  ANDROID_SERIAL is R5CT, which names no device adb lists\n" +
				"              " + serialForms + "\n"},
	} {
		bin := doctorHost(t)
		t.Setenv("ANDROID_SERIAL", tc.serial)
		log := filepath.Join(t.TempDir(), "calls")
		fakeProgram(t, bin, "adb", "printf '%s\\n' \"$*\" >> '"+log+"'\n"+adbScript(tc.listing, versions))
		lines := adbAndDevices(false)
		data, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		calls := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		t.Logf("%s: adb was run as: %q", tc.name, calls)
		if !slices.Equal(calls, tc.calls) {
			t.Errorf("%s: adb was run as\n%q\nwant\n%q", tc.name, calls, tc.calls)
		}
		for _, call := range calls {
			for _, s := range serials {
				if s != tc.used && slices.Contains(strings.Fields(call), s) {
					t.Errorf("%s: adb %s names %s, a device scterm would not use", tc.name, call, s)
				}
			}
		}
		if got := doctorText(lines[1:]); got != tc.want {
			t.Errorf("%s: printed\n%s\nwant\n%s", tc.name, got, tc.want)
		}
	}
}
