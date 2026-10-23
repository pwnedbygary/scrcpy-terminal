package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"scterm/peer"
)

// ---------------------------------------------------------------------------
// scterm doctor checks what scterm needs on this computer: one line for each
// check, and under a problem what to do. Every check here runs on the test's
// own computer: adb and pactl are shell scripts in a directory that is all of
// PATH, and the CPU flags, browsers, display sockets, home and port are the
// test's, so no test reaches this host's adb, sound server, phone or display.
// ---------------------------------------------------------------------------

// doctorHost gives scterm doctor a computer of the test's own, and returns the
// directory that is all of PATH, for the fake programs the test writes there.
// The CPU has AVX2, nothing is paired, and there is no display.
func doctorHost(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	for _, v := range []string{"ANDROID_SERIAL", "PULSE_SERVER", "SCT_AUDIO_DEVICE", "SCTERM_CHROME", "CHROME_BIN", "CHROMIUM_BIN",
		"SCTERM_FIREFOX_KIOSK", webHostsEnv} {
		t.Setenv(v, "")
	}
	t.Setenv("LC_ALL", "fr_FR.UTF-8") // doctor reads its programs in the C locale whatever the user's
	t.Setenv("LANGUAGE", "fr")        // and without the user's LANGUAGE
	t.Setenv("SCTERM_HOME", t.TempDir())
	noDisplayEnv(t)
	oldGlobs, oldCPU, oldTimeout := chromiumGlobs, cpuinfoPath, doctorTimeout
	t.Cleanup(func() { chromiumGlobs, cpuinfoPath, doctorTimeout = oldGlobs, oldCPU, oldTimeout })
	chromiumGlobs = nil // where this host's own browsers are installed
	cpuinfoPath = cpuinfoFile(t, "fpu sse2 avx avx2 bmi2")
	return bin
}

// cpuinfoFile writes a /proc/cpuinfo whose two CPUs have these flags.
func cpuinfoFile(t *testing.T, flags string) string {
	t.Helper()
	const cpu = "processor\t: %d\nvendor_id\t: GenuineIntel\nmodel name\t: Test CPU\nflags\t\t: %s\nbugs\t\t: spectre_v1\n\n"
	path := filepath.Join(t.TempDir(), "cpuinfo")
	if err := os.WriteFile(path, []byte(fmt.Sprintf(cpu, 0, flags)+fmt.Sprintf(cpu, 1, flags)), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeTemp writes text to a file of its own, and returns its path.
func writeTemp(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeProgram writes a shell script named name into dir, and returns its path.
func fakeProgram(t *testing.T, dir, name, script string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// adbScript is an adb that answers adb version, lists devices as
// adb devices -l does, and answers getprop with the release and SDK given for
// each serial, as "14 34"; it refuses anything else.
func adbScript(listing string, versions map[string]string) string {
	var b strings.Builder
	b.WriteString("case \"$*\" in\n")
	b.WriteString("version) printf 'Android Debug Bridge version 1.0.41\\nVersion 35.0.2-12147458\\nInstalled as /usr/lib/android-sdk/platform-tools/adb\\n' ;;\n")
	fmt.Fprintf(&b, "'devices -l') printf '%s' ;;\n", strings.NewReplacer("\n", `\n`, "\t", `\t`).Replace(listing))
	for serial, v := range versions {
		release, sdk, _ := strings.Cut(v, " ")
		fmt.Fprintf(&b, "'-s %s shell getprop ro.build.version.release') echo '%s' ;;\n", serial, release)
		fmt.Fprintf(&b, "'-s %s shell getprop ro.build.version.sdk') echo '%s' ;;\n", serial, sdk)
	}
	b.WriteString("*) echo \"adb: unexpected $*\" >&2; exit 1 ;;\nesac\n")
	return b.String()
}

// The fake pactl of a working desktop: pactl info in the C locale without
// LANGUAGE (and in French otherwise), and its sinks.
const workingPactl = `case "$*" in
info)
  if [ "$LC_ALL" = C ] && [ -z "${LANGUAGE+set}" ]; then
    printf 'Server String: /run/user/1000/pulse/native\nServer Name: PulseAudio (on PipeWire 1.4.2)\nDefault Sink: alsa_output.pci-0000_0c_00.4.analog-stereo\n'
  else
    printf 'Nom du serveur : PulseAudio (on PipeWire 1.4.2)\n'
  fi ;;
'list sinks short')
  printf '0\talsa_output.pci-0000_0c_00.4.analog-stereo\tPipeWire\ts32le 2ch 48000Hz\tSUSPENDED\n1\tbluez_output.AA_BB.1\tPipeWire\ts16le 2ch 48000Hz\tIDLE\n' ;;
*) exit 1 ;;
esac
`

// refusedPactl is pactl with no sound server to reach.
const refusedPactl = "echo 'Connection failure: Connection refused' >&2\nexit 1\n"

const devicesHeader = "List of devices attached\n"

// doctorText is lines as scterm doctor prints them.
func doctorText(lines []doctorLine) string {
	var b strings.Builder
	printDoctor(&b, lines)
	return b.String()
}

// buildLine is the build line on this test's computer, whose CPU has AVX2.
func buildLine() string {
	if runtime.GOARCH != "amd64" {
		return "ok    build   scterm dev for linux/" + runtime.GOARCH + "\n"
	}
	return "ok    build   scterm dev for linux/amd64; the CPU has AVX2\n"
}

// TestDoctorPrintsOneLinePerCheck: a status word, the check and what was
// found, in columns, then what to do, indented under what was found; text
// from programs and devices loses its control characters; and only a FAIL
// counts as a failure.
func TestDoctorPrintsOneLinePerCheck(t *testing.T) {
	var b strings.Builder
	failed := printDoctor(&b, []doctorLine{
		{doctorOK, "adb", "found", ""},
		{doctorWarn, "device", "Pixel\x1b[2J 8\r is odd", "do\x07 this"},
	})
	want := "ok    adb     found\n" +
		"warn  device  Pixel[2J 8 is odd\n" +
		"              do this\n"
	if b.String() != want || failed {
		t.Errorf("printed %q, failed=%v; want %q, failed=false", b.String(), failed, want)
	}
	if !printDoctor(&b, []doctorLine{{doctorOK, "build", "x", ""}, {doctorFail, "adb", "y", "z"}}) {
		t.Error("a FAIL line did not count as a failure")
	}
}

// TestDoctorChecksForAVX2: on amd64 the CPU must have AVX2 (README: every
// amd64 build needs it); other architectures need no such flag.
func TestDoctorChecksForAVX2(t *testing.T) {
	doctorHost(t)
	for _, tc := range []struct {
		name, arch, cpuinfo, want string
	}{
		{"AVX2", "amd64", cpuinfoFile(t, "fpu sse2 avx avx2"), "ok    build   scterm dev for linux/amd64; the CPU has AVX2\n"},
		{"AVX only", "amd64", cpuinfoFile(t, "fpu sse2 avx avx512_bf16"),
			"FAIL  build   scterm dev for linux/amd64; the CPU has no AVX2, which every x86-64 build of scterm uses\n" +
				"              scterm cannot run on this CPU, built from source or not: it stops with SIGILL: illegal instruction\n"},
		{"unreadable", "amd64", filepath.Join(t.TempDir(), "missing"),
			"warn  build   scterm dev for linux/amd64; whether the CPU has AVX2 is unknown (open <cpuinfo>: no such file or directory)\n" +
				"              scterm needs AVX2 on x86-64: without it, it stops with SIGILL: illegal instruction\n"},
		{"no flags line", "amd64", writeTemp(t, "processor\t: 0\nmodel name\t: Test CPU\n"),
			"warn  build   scterm dev for linux/amd64; whether the CPU has AVX2 is unknown (<cpuinfo> lists no CPU flags)\n" +
				"              scterm needs AVX2 on x86-64: without it, it stops with SIGILL: illegal instruction\n"},
		{"arm64", "arm64", cpuinfoFile(t, "fp asimd evtstrm"), "ok    build   scterm dev for linux/arm64\n"},
	} {
		cpuinfoPath = tc.cpuinfo
		if got, want := doctorText([]doctorLine{doctorBuild(tc.arch)}), strings.ReplaceAll(tc.want, "<cpuinfo>", tc.cpuinfo); got != want {
			t.Errorf("%s: printed\n%s\nwant\n%s", tc.name, got, want)
		}
	}
}

// TestDoctorWithoutADB: no adb on PATH fails with the hint scterm itself gives,
// and so does no device; with a paired device, peer mode needs neither, so
// both are warnings. The advice gives --peer as it can be typed: --peer NAME,
// and with one paired device --peer=, which takes it.
func TestDoctorWithoutADB(t *testing.T) {
	bin := doctorHost(t)
	const hint = "install Android platform-tools (often packaged as android-tools or adb), or use --peer NAME to reach the scterm Android app without it"
	if !strings.HasSuffix(errADBNotFound.Error(), ": "+hint) {
		t.Errorf("scterm's own error %q does not end with the hint", errADBNotFound)
	}
	for paired, want := range map[bool]string{
		false: "FAIL  adb     not found on PATH\n              " + hint + "\n",
		true: "warn  adb     not found on PATH\n" +
			"              install Android platform-tools (often packaged as android-tools or adb), or use --peer= to reach the scterm Android app without it\n",
	} {
		if got := doctorText(adbAndDevices(paired)); got != want {
			t.Errorf("paired=%v: printed\n%s\nwant\n%s", paired, got, want)
		}
	}
	fakeProgram(t, bin, "adb", adbScript(devicesHeader+"\n", nil))
	for paired, want := range map[bool]string{
		false: "FAIL  device  no device found\n" +
			"              connect one with USB debugging turned on (or over Wi-Fi with adb pair and adb connect), or use --peer NAME for the scterm Android app\n",
		true: "warn  device  no device found\n" +
			"              connect one with USB debugging turned on (or over Wi-Fi with adb pair and adb connect), or use --peer= for the scterm Android app\n",
	} {
		want = "ok    adb     " + bin + "/adb: Android Debug Bridge version 1.0.41, Version 35.0.2-12147458\n" + want
		if got := doctorText(adbAndDevices(paired)); got != want {
			t.Errorf("no device, paired=%v: printed\n%s\nwant\n%s", paired, got, want)
		}
	}
}

// TestDoctorExplainsEachDeviceState: each device adb lists gets a line; one
// that is not ready gets scterm's own explanation of its state. Only the
// device scterm would use is read, and when it is too old for the bundled
// server, it says so: a failure when it is that device or scterm has none it
// can mirror, and a warning otherwise. Another ready device is listed as adb
// lists it, and may be one scterm can mirror. Several ready devices need -s
// or ANDROID_SERIAL, which picks the device scterm uses.
func TestDoctorExplainsEachDeviceState(t *testing.T) {
	const pixel = "\tdevice usb:1-1 product:shiba model:Pixel_8 device:shiba transport_id:1\n"
	const tab = "\tdevice usb:1-2 product:gts9 model:Tab_S9 device:gts9 transport_id:2\n"
	const readyPixel = "ok    device  R5CT (Pixel_8) is ready: Android 14 (SDK 34); sound and playback capture work"
	const tooOld = "; the bundled server needs Android 5.0 or later\n              scterm cannot mirror it: use a device with Android 5.0 or later\n"
	const noneNamed = "              connect that device, or unset ANDROID_SERIAL, or set it as adb takes it: a serial, a network device's host " +
		"without its port (tcp: or udp: may come first), a USB path such as usb:1-1, or product:, model: or device: and a name, as adb devices -l shows them\n"
	const unread = " is ready; scterm would not use it, so doctor runs nothing on it\n"
	for _, tc := range []struct {
		name, listing, serial string
		versions              map[string]string
		want                  string
	}{
		{"one ready", devicesHeader + "R5CT" + pixel, "", map[string]string{"R5CT": "14 34"}, readyPixel + "\n"},
		{"adb starting its server", "* daemon not running; starting now at tcp:5037\n* daemon started successfully\n" +
			devicesHeader + "R5CT" + pixel, "", map[string]string{"R5CT": "14 34"}, readyPixel + "\n"},
		{"unauthorized", devicesHeader + "R5CT\tunauthorized usb:1-1 transport_id:3\n", "", nil,
			"FAIL  device  R5CT has not allowed USB debugging from this computer\n" +
				"              unlock it, accept the \"Allow USB debugging?\" prompt, and run scterm again\n"},
		{"a serial and a state with control characters", devicesHeader + "R5\x1b]0;owned\x07CT\u202e\x9b\t\x1b[2Jrecovery usb:1-1 transport_id:6\n", "", nil,
			"FAIL  device  R5 ]0;owned CT \uFFFD is in [2Jrecovery mode, not ready for scterm\n              start Android on it normally\n"},
		{"no permissions", devicesHeader + "R5CT\tno permissions (missing udev rules? user is in the plugdev group); " +
			"see [http://developer.android.com/tools/device.html] usb:1-1 transport_id:2\n", "", nil,
			"FAIL  device  adb has no permission to open R5CT\n" +
				"              add a udev rule for it (or join the plugdev group), then unplug and replug it\n"},
		{"offline", devicesHeader + "R5CT\toffline usb:1-1 transport_id:4\n", "", nil,
			"FAIL  device  R5CT is offline\n" +
				"              unplug and replug it, or restart adb with 'adb kill-server'\n"},
		{"authorizing", devicesHeader + "R5CT\tauthorizing usb:1-1 transport_id:5\n", "", nil,
			"FAIL  device  R5CT is still authorizing\n              try again in a moment\n"},
		{"recovery", devicesHeader + "R5CT\trecovery usb:1-1 transport_id:6\n", "", nil,
			"FAIL  device  R5CT is in recovery mode, not ready for scterm\n              start Android on it normally\n"},
		{"detached", devicesHeader + "R5CT\tdetached usb:1-1 transport_id:7\n", "", nil,
			"FAIL  device  R5CT is detached from adb\n              attach it again with 'adb -s R5CT attach'\n"},
		{"none", devicesHeader + "\n", "", nil,
			"FAIL  device  no device found\n" +
				"              connect one with USB debugging turned on (or over Wi-Fi with adb pair and adb connect), or use --peer NAME for the scterm Android app\n"},
		{"two ready", devicesHeader + "GT9" + tab + "R5CT" + pixel, "", map[string]string{"GT9": "13 33", "R5CT": "14 34"},
			"ok    device  GT9 (Tab_S9)" + unread + "ok    device  R5CT (Pixel_8)" + unread +
				"warn  device  2 devices are connected (GT9, R5CT)\n" +
				"              choose one with -s SERIAL, or set ANDROID_SERIAL\n"},
		{"ready beside unauthorized", devicesHeader + "GT9\tunauthorized usb:1-2 transport_id:3\nR5CT" + pixel, "",
			map[string]string{"R5CT": "14 34"},
			"warn  device  GT9 has not allowed USB debugging from this computer\n" +
				"              unlock it, accept the \"Allow USB debugging?\" prompt, and run scterm again\n" + readyPixel + "\n"},
		{"two ready, one too old, neither chosen", devicesHeader + "OLD\tdevice model:Nexus_7 transport_id:9\nR5CT" + pixel, "",
			map[string]string{"OLD": "4.4 19", "R5CT": "14 34"},
			"ok    device  OLD (Nexus_7)" + unread + "ok    device  R5CT (Pixel_8)" + unread +
				"warn  device  2 devices are connected (OLD, R5CT)\n" +
				"              choose one with -s SERIAL, or set ANDROID_SERIAL\n"},
		{"a ready device, then one too old, neither chosen", devicesHeader + "R5CT" + pixel + "OLD\tdevice model:Nexus_7 transport_id:9\n", "",
			map[string]string{"OLD": "4.4 19", "R5CT": "14 34"},
			"ok    device  R5CT (Pixel_8)" + unread + "ok    device  OLD (Nexus_7)" + unread +
				"warn  device  2 devices are connected (R5CT, OLD)\n" +
				"              choose one with -s SERIAL, or set ANDROID_SERIAL\n"},
		// Neither is read, so neither is known to be too old: each may be one
		// scterm can mirror, once it is chosen.
		{"two ready, both too old, neither chosen", devicesHeader + "OLD1\tdevice model:Nexus_7 transport_id:1\nOLD2\tdevice model:Nexus_4 transport_id:2\n", "",
			map[string]string{"OLD1": "4.4 19", "OLD2": "4.2 17"},
			"ok    device  OLD1 (Nexus_7)" + unread + "ok    device  OLD2 (Nexus_4)" + unread +
				"warn  device  2 devices are connected (OLD1, OLD2)\n" +
				"              choose one with -s SERIAL, or set ANDROID_SERIAL\n"},
		{"two ready, both too old, neither chosen, beside one not ready",
			devicesHeader + "OLD1\tdevice model:Nexus_7 transport_id:1\nOLD2\tdevice model:Nexus_4 transport_id:2\nR5CT\tunauthorized usb:1-1 transport_id:3\n", "",
			map[string]string{"OLD1": "4.4 19", "OLD2": "4.2 17"},
			"ok    device  OLD1 (Nexus_7)" + unread + "ok    device  OLD2 (Nexus_4)" + unread +
				"warn  device  R5CT has not allowed USB debugging from this computer\n" +
				"              unlock it, accept the \"Allow USB debugging?\" prompt, and run scterm again\n" +
				"warn  device  2 devices are connected (OLD1, OLD2)\n" +
				"              choose one with -s SERIAL, or set ANDROID_SERIAL\n"},
		{"two ready, one too old, the other's version unknown", devicesHeader + "OLD\tdevice model:Nexus_7 transport_id:9\nR5CT" + pixel, "",
			map[string]string{"OLD": "4.4 19", "R5CT": "14 fourteen"},
			"ok    device  OLD (Nexus_7)" + unread + "ok    device  R5CT (Pixel_8)" + unread +
				"warn  device  2 devices are connected (OLD, R5CT)\n" +
				"              choose one with -s SERIAL, or set ANDROID_SERIAL\n"},
		{"one ready but too old, beside one not ready", devicesHeader + "OLD\tdevice model:Nexus_7 transport_id:9\nR5CT\tunauthorized usb:1-1 transport_id:3\n", "",
			map[string]string{"OLD": "4.4 19"},
			"FAIL  device  OLD (Nexus_7) is ready: Android 4.4 (SDK 19)" + tooOld +
				"FAIL  device  R5CT has not allowed USB debugging from this computer\n" +
				"              unlock it, accept the \"Allow USB debugging?\" prompt, and run scterm again\n"},
		{"ANDROID_SERIAL picks one of two", devicesHeader + "GT9" + tab + "R5CT" + pixel, "R5CT",
			map[string]string{"GT9": "13 33", "R5CT": "14 34"},
			"ok    device  GT9 (Tab_S9)" + unread + readyPixel + "; ANDROID_SERIAL names it\n"},
		{"ANDROID_SERIAL beside one too old", devicesHeader + "OLD\tdevice model:Nexus_7 transport_id:9\nR5CT" + pixel, "R5CT",
			map[string]string{"OLD": "4.4 19", "R5CT": "14 34"},
			"ok    device  OLD (Nexus_7)" + unread + readyPixel + "; ANDROID_SERIAL names it\n"},
		{"ANDROID_SERIAL names one too old", devicesHeader + "OLD\tdevice model:Nexus_7 transport_id:9\nR5CT" + pixel, "OLD",
			map[string]string{"OLD": "4.4 19", "R5CT": "14 34"},
			"FAIL  device  OLD (Nexus_7) is ready: Android 4.4 (SDK 19); the bundled server needs Android 5.0 or later; ANDROID_SERIAL names it\n" +
				"              scterm cannot mirror it: use a device with Android 5.0 or later\n" + "ok    device  R5CT (Pixel_8)" + unread},
		{"ANDROID_SERIAL names one not ready", devicesHeader + "GT9" + tab + "R5CT\toffline usb:1-1 transport_id:4\n", "R5CT",
			map[string]string{"GT9": "13 33"},
			"ok    device  GT9 (Tab_S9)" + unread +
				"FAIL  device  R5CT is offline; ANDROID_SERIAL names it\n" +
				"              unplug and replug it, or restart adb with 'adb kill-server'\n"},
		{"ANDROID_SERIAL names one not listed", devicesHeader + "GT9" + tab + "R5CT\toffline usb:1-1 transport_id:4\n", "192.168.1.20:5555",
			map[string]string{"GT9": "13 33"},
			"ok    device  GT9 (Tab_S9)" + unread +
				"warn  device  R5CT is offline\n" +
				"              unplug and replug it, or restart adb with 'adb kill-server'\n" +
				"FAIL  device  ANDROID_SERIAL is 192.168.1.20:5555, which names no device adb lists\n" + noneNamed},
		{"ANDROID_SERIAL and no device", devicesHeader, "R5CT", nil,
			"FAIL  device  ANDROID_SERIAL is R5CT, which names no device adb lists\n" + noneNamed},
		{"ANDROID_SERIAL names one not listed, beside one too old", devicesHeader + "OLD\tdevice model:Nexus_7 transport_id:9\n", "R5CT",
			map[string]string{"OLD": "4.4 19"},
			"ok    device  OLD (Nexus_7)" + unread +
				"FAIL  device  ANDROID_SERIAL is R5CT, which names no device adb lists\n" + noneNamed},
	} {
		bin := doctorHost(t)
		t.Setenv("ANDROID_SERIAL", tc.serial)
		fakeProgram(t, bin, "adb", adbScript(tc.listing, tc.versions))
		lines := adbAndDevices(false)
		if len(lines) == 0 || lines[0].check != "adb" {
			t.Errorf("%s: no adb line first: %q", tc.name, doctorText(lines))
			continue
		}
		if got := doctorText(lines[1:]); got != tc.want {
			t.Errorf("%s: printed\n%s\nwant\n%s", tc.name, got, tc.want)
		}
		// A device alone, not ready, is explained in scterm's own words: the
		// error it stops with when that device is all there is.
		if _, err := pickDevice(tc.listing); err != nil && tc.serial == "" && len(lines) == 2 && lines[1].status == doctorFail {
			if said := lines[1].detail + ": " + lines[1].todo; said != err.Error() {
				t.Errorf("%s: doctor says %q, scterm says %q", tc.name, said, err)
			}
		}
	}
}

// TestDoctorTakesANDROID_SERIALAsADBDoes: ANDROID_SERIAL names a device in
// each form adb takes for -s (atransport::MatchesTarget, transport.cpp lines
// 1289-1323 at 1cf2f017): its serial; a network device's host without its
// port, and either with tcp: or udp: before it; its USB path; and product:,
// model: or device: with the name adb devices -l shows. The line of the
// device it names says so, and only that device is read. A value that names
// no device, or more than one, fails, as adb then takes none
// (acquire_one_transport, lines 942-950), and a device adb has no permission
// to open is passed over (lines 932-935), but one in any other state that is
// not ready, such as offline, is not.
func TestDoctorTakesANDROID_SERIALAsADBDoes(t *testing.T) {
	const listing = devicesHeader +
		"192.168.1.20:5555      device product:shiba model:Pixel_8 device:shiba transport_id:2\n" +
		"R5CT                   device usb:1-1 product:gts9 model:Tab_S9 device:gts9 transport_id:1\n"
	versions := map[string]string{"192.168.1.20:5555": "14 34", "R5CT": "13 33"}
	const wifi = "ok    device  192.168.1.20:5555 (Pixel_8) is ready: Android 14 (SDK 34); sound and playback capture work"
	const usb = "ok    device  R5CT (Tab_S9) is ready: Android 13 (SDK 33); sound and playback capture work"
	const unread = " is ready; scterm would not use it, so doctor runs nothing on it\n"
	const wifiUnread = "ok    device  192.168.1.20:5555 (Pixel_8)" + unread
	const usbUnread = "ok    device  R5CT (Tab_S9)" + unread
	const noneNamed = "FAIL  device  ANDROID_SERIAL is <value>, which names no device adb lists\n" +
		"              connect that device, or unset ANDROID_SERIAL, or set it as adb takes it: a serial, a network device's host " +
		"without its port (tcp: or udp: may come first), a USB path such as usb:1-1, or product:, model: or device: and a name, as adb devices -l shows them\n"
	both := wifiUnread + usbUnread
	for _, tc := range []struct{ value, want string }{
		{"", both + "warn  device  2 devices are connected (192.168.1.20:5555, R5CT)\n" +
			"              choose one with -s SERIAL, or set ANDROID_SERIAL\n"},
		{"192.168.1.20:5555", wifi + "; ANDROID_SERIAL names it\n" + usbUnread},
		{"192.168.1.20", wifi + "; ANDROID_SERIAL=192.168.1.20 names it\n" + usbUnread},
		{"tcp:192.168.1.20", wifi + "; ANDROID_SERIAL=tcp:192.168.1.20 names it\n" + usbUnread},
		{"udp:192.168.1.20:5555", wifi + "; ANDROID_SERIAL=udp:192.168.1.20:5555 names it\n" + usbUnread},
		{"R5CT", wifiUnread + usb + "; ANDROID_SERIAL names it\n"},
		{"usb:1-1", wifiUnread + usb + "; ANDROID_SERIAL=usb:1-1 names it\n"},
		{"product:gts9", wifiUnread + usb + "; ANDROID_SERIAL=product:gts9 names it\n"},
		{"model:Pixel_8", wifi + "; ANDROID_SERIAL=model:Pixel_8 names it\n" + usbUnread},
		{"device:shiba", wifi + "; ANDROID_SERIAL=device:shiba names it\n" + usbUnread},
		// Values that name neither: another host or port, a host for a USB
		// device, which has none, a model as the device calls itself rather
		// than as adb devices -l writes it, and a name without its form.
		{"192.168.1.2", both + noneNamed},
		{"192.168.1.20:5556", both + noneNamed},
		{"R5CT:5555", both + noneNamed},
		{"tcp:R5CT", both + noneNamed},
		{"usb:1-2", both + noneNamed},
		{"model:Pixel 8", both + noneNamed},
		{"model:", both + noneNamed},
		{"Pixel_8", both + noneNamed},
	} {
		bin := doctorHost(t)
		t.Setenv("ANDROID_SERIAL", tc.value)
		fakeProgram(t, bin, "adb", adbScript(listing, versions))
		if got, want := doctorText(adbAndDevices(false)[1:]), strings.ReplaceAll(tc.want, "<value>", tc.value); got != want {
			t.Errorf("ANDROID_SERIAL=%s: printed\n%s\nwant\n%s", tc.value, got, want)
		}
	}

	const shiba = "\tdevice usb:1-%d product:shiba model:Pixel_8 device:shiba transport_id:%d\n"
	const noPermission = "\tno permissions (missing udev rules? user is in the plugdev group); see [http://developer.android.com/tools/device.html] usb:1-1 transport_id:1\n"
	const cheap = "ok    device  0123456789ABCDEF (Tab_7) is ready: Android 13 (SDK 33); sound and playback capture work"
	for _, tc := range []struct {
		name, listing, value string
		versions             map[string]string
		want                 string
	}{
		{"two devices of one model", devicesHeader + "R5CT" + fmt.Sprintf(shiba, 1, 1) + "R5CU" + fmt.Sprintf(shiba, 2, 3), "model:Pixel_8",
			map[string]string{"R5CT": "14 34", "R5CU": "14 34"},
			"ok    device  R5CT (Pixel_8)" + unread + "ok    device  R5CU (Pixel_8)" + unread +
				"FAIL  device  ANDROID_SERIAL is model:Pixel_8, which names 2 devices (R5CT, R5CU), so adb takes none of them\n" +
				"              set it to the serial of the one to use\n"},
		{"two devices of one model, one of them offline", devicesHeader + "R5CT" + fmt.Sprintf(shiba, 1, 1) +
			"R5CU\toffline usb:1-2 product:shiba model:Pixel_8 device:shiba transport_id:3\n", "model:Pixel_8",
			map[string]string{"R5CT": "14 34"},
			"ok    device  R5CT (Pixel_8)" + unread +
				"warn  device  R5CU is offline\n" +
				"              unplug and replug it, or restart adb with 'adb kill-server'\n" +
				"FAIL  device  ANDROID_SERIAL is model:Pixel_8, which names 2 devices (R5CT, R5CU), so adb takes none of them\n" +
				"              set it to the serial of the one to use\n"},
		{"one serial, and one of the two not open to adb", devicesHeader + "0123456789ABCDEF" + noPermission +
			"0123456789ABCDEF\tdevice usb:1-2 product:x7 model:Tab_7 device:x7 transport_id:2\n", "0123456789ABCDEF",
			map[string]string{"0123456789ABCDEF": "13 33"},
			"warn  device  adb has no permission to open 0123456789ABCDEF\n" +
				"              add a udev rule for it (or join the plugdev group), then unplug and replug it\n" +
				cheap + "; ANDROID_SERIAL names it\n"},
		{"only a device not open to adb", devicesHeader + "0123456789ABCDEF" + noPermission +
			"0123456789ABCDEF\tdevice usb:1-2 product:x7 model:Tab_7 device:x7 transport_id:2\n", "usb:1-1",
			map[string]string{"0123456789ABCDEF": "13 33"},
			"FAIL  device  adb has no permission to open 0123456789ABCDEF; ANDROID_SERIAL=usb:1-1 names it\n" +
				"              add a udev rule for it (or join the plugdev group), then unplug and replug it\n" +
				"ok    device  0123456789ABCDEF (Tab_7)" + unread},
	} {
		bin := doctorHost(t)
		t.Setenv("ANDROID_SERIAL", tc.value)
		fakeProgram(t, bin, "adb", adbScript(tc.listing, tc.versions))
		if got := doctorText(adbAndDevices(false)[1:]); got != tc.want {
			t.Errorf("%s: printed\n%s\nwant\n%s", tc.name, got, tc.want)
		}
	}
}

// TestDoctorComparesTheAndroidVersion: a ready device's release and SDK, read
// with getprop, against the README: the bundled server needs Android 5.0 (SDK
// 21), sound Android 11 (SDK 30, unlocked as scterm starts), and playback
// capture Android 13 (SDK 33).
func TestDoctorComparesTheAndroidVersion(t *testing.T) {
	const name = "ok    device  R5CT (Pixel_8) is ready: "
	const noPlayback = "; sound works, but playback capture (--audio-source playback, --audio-dup) needs Android 13 or later\n"
	for _, tc := range []struct{ version, want string }{
		{"4.4 19", "FAIL  device  R5CT (Pixel_8) is ready: Android 4.4 (SDK 19); the bundled server needs Android 5.0 or later\n" +
			"              scterm cannot mirror it: use a device with Android 5.0 or later\n"},
		{"4.4W 20", "FAIL  device  R5CT (Pixel_8) is ready: Android 4.4W (SDK 20); the bundled server needs Android 5.0 or later\n" +
			"              scterm cannot mirror it: use a device with Android 5.0 or later\n"},
		{"5.0 21", "warn  device  R5CT (Pixel_8) is ready: Android 5.0 (SDK 21); it sends no sound, which needs Android 11 or later\n" +
			"              scterm shows and controls it without sound\n"},
		{"10 29", "warn  device  R5CT (Pixel_8) is ready: Android 10 (SDK 29); it sends no sound, which needs Android 11 or later\n" +
			"              scterm shows and controls it without sound\n"},
		{"11 30", name + "Android 11 (SDK 30)" + noPlayback + "              unlock it before you start scterm, for sound\n"},
		{"12 31", name + "Android 12 (SDK 31)" + noPlayback},
		{"12 32", name + "Android 12 (SDK 32)" + noPlayback},
		{"13 33", name + "Android 13 (SDK 33); sound and playback capture work\n"},
		{"16 36", name + "Android 16 (SDK 36); sound and playback capture work\n"},
		{" 34", name + "SDK 34; sound and playback capture work\n"},
		{"14 fourteen", "warn  device  R5CT (Pixel_8) is ready, but its Android version could not be read (its SDK reads \"fourteen\")\n" +
			"              see what adb says: adb -s R5CT shell getprop ro.build.version.sdk\n"},
	} {
		bin := doctorHost(t)
		fakeProgram(t, bin, "adb", adbScript(devicesHeader+"R5CT\tdevice usb:1-1 model:Pixel_8 transport_id:1\n",
			map[string]string{"R5CT": tc.version}))
		lines := adbAndDevices(false)
		if got := doctorText(lines[1:]); got != tc.want {
			t.Errorf("%q: printed\n%s\nwant\n%s", tc.version, got, tc.want)
		}
	}
	// A device that stops answering between the listing and getprop: the cause
	// adb gives, after the progress lines of a server it had to start again,
	// and the first line of an error that takes several (transport.cpp, lines
	// 997-1004 at 1cf2f017). The advice gives the read that failed: the
	// release, read first, or the SDK after it.
	for _, tc := range []struct{ name, stderr, cause string }{
		{"closed", "error: closed", "error: closed"},
		{"the server started again", "* daemon not running; starting now at tcp:5037\n* daemon started successfully\nadb: device 'R5CT' not found",
			"adb: device 'R5CT' not found"},
		{"no longer allowed", "error: device unauthorized.\nThis adb server's $ADB_VENDOR_KEYS is not set\n" +
			"Try 'adb kill-server' if that seems wrong.\nOtherwise check for a confirmation dialog on your device.", "error: device unauthorized."},
	} {
		for _, failing := range []string{"ro.build.version.release", "ro.build.version.sdk"} {
			bin := doctorHost(t)
			fakeProgram(t, bin, "adb", `case "$*" in
version) echo 'Android Debug Bridge version 1.0.41' ;;
'devices -l') printf 'List of devices attached\nR5CT\tdevice model:Pixel_8\n' ;;
*`+failing+`) while IFS= read -r l; do printf '%s\n' "$l" >&2; done <<'EOF'
`+tc.stderr+`
EOF
   exit 1 ;;
*release) echo 14 ;;
*) echo 'adb: unexpected' >&2; exit 1 ;;
esac
`)
			want := "warn  device  R5CT (Pixel_8) is ready, but its Android version could not be read (" + tc.cause + ")\n" +
				"              see what adb says: adb -s R5CT shell getprop " + failing + "\n"
			if got := doctorText(adbAndDevices(false)[1:]); got != want {
				t.Errorf("getprop %s failing, %s: printed\n%s\nwant\n%s", failing, tc.name, got, want)
			}
		}
	}
}

// TestDoctorWhenADBFails: an adb that does not run, a server that cannot be
// reached or started, and one that does not answer each fail, with the cause
// adb gives; the last within the time limit, without waiting for what it left
// running, which holds its output open. With a paired device, peer mode needs
// no adb, so each is a warning, whose advice offers --peer as well, as it is
// typed for the only paired device.
func TestDoctorWhenADBFails(t *testing.T) {
	if _, err := os.Stat("/bin/sleep"); err != nil {
		t.Skip("no /bin/sleep for an adb that hangs")
	}
	for _, tc := range []struct{ name, script, want string }{
		{"does not run", "echo 'adb: error while loading shared libraries: libc++.so: cannot open shared object file' >&2\nexit 127\n",
			"<status>  adb     <bin>/adb does not run (adb: error while loading shared libraries: libc++.so: cannot open shared object file)\n" +
				"              install Android platform-tools again<peer>\n"},
		{"no server", "case \"$1\" in\nversion) echo 'Android Debug Bridge version 1.0.41' ;;\n" +
			"*) echo 'cannot connect to daemon at tcp:5037: Connection refused' >&2; exit 1 ;;\nesac\n",
			"ok    adb     <bin>/adb: Android Debug Bridge version 1.0.41\n" +
				"<status>  device  adb devices -l failed (cannot connect to daemon at tcp:5037: Connection refused)\n" +
				"              restart adb with 'adb kill-server', then try again<peer>\n"},
		// What adb writes when its server does not start: its progress, the
		// server's log, then the error it stops with (client/adb_client.cpp,
		// lines 262-281, adb.cpp, lines 789-807, and the error_exit of
		// client/commandline.cpp, line 1771, at 1cf2f017).
		{"the server does not start", "case \"$1\" in\nversion) echo 'Android Debug Bridge version 1.0.41' ;;\n" +
			"*) while IFS= read -r l; do printf '%s\\n' \"$l\" >&2; done <<'EOF'\n" +
			"* daemon not running; starting now at tcp:5037\n" +
			"ADB server didn't ACK\n" +
			"Full server startup log: /tmp/adb.1000.log\n" +
			"Server had pid: 4242\n" +
			"--- adb starting (pid 4242) ---\n" +
			"10-21 03:44:00.123  4242  4242 I adb     : main.cpp:63 Android Debug Bridge version 1.0.41\n" +
			"10-21 03:44:00.130  4242  4242 F adb     : main.cpp:166 could not install *smartsocket* listener: Address already in use\n" +
			"\n" +
			"* failed to start daemon\n" +
			"adb: failed to check server version: cannot connect to daemon\n" +
			"EOF\n   exit 1 ;;\nesac\n",
			"ok    adb     <bin>/adb: Android Debug Bridge version 1.0.41\n" +
				"<status>  device  adb devices -l failed (adb: failed to check server version: cannot connect to daemon)\n" +
				"              restart adb with 'adb kill-server', then try again<peer>\n"},
		{"no answer", "case \"$1\" in\nversion) echo 'Android Debug Bridge version 1.0.41' ;;\n*) /bin/sleep 5 & wait ;;\nesac\n",
			"ok    adb     <bin>/adb: Android Debug Bridge version 1.0.41\n" +
				"<status>  device  adb devices -l failed (no answer within 200ms)\n" +
				"              restart adb with 'adb kill-server', then try again<peer>\n"},
	} {
		for _, p := range []struct {
			paired       bool
			status, peer string
		}{
			{false, "FAIL", ""},
			{true, "warn", ", or use --peer= to reach the scterm Android app without it"},
		} {
			bin := doctorHost(t)
			doctorTimeout = 200 * time.Millisecond
			fakeProgram(t, bin, "adb", tc.script)
			start := time.Now()
			got := doctorText(adbAndDevices(p.paired))
			if want := strings.NewReplacer("<bin>", bin, "<status>", p.status, "<peer>", p.peer).Replace(tc.want); got != want {
				t.Errorf("%s, paired=%v: printed\n%s\nwant\n%s", tc.name, p.paired, got, want)
			}
			if took := time.Since(start); took > 3*time.Second {
				t.Errorf("%s, paired=%v: took %s", tc.name, p.paired, took)
			}
		}
	}
}

// TestDoctorGoesOnWhenAProgramKeepsItsOutputOpen: a program that answered and
// exited 0, but left something running that holds its output open, answered:
// doctor stops waiting for that output a second later, and reads what the
// program printed.
func TestDoctorGoesOnWhenAProgramKeepsItsOutputOpen(t *testing.T) {
	if _, err := os.Stat("/bin/sleep"); err != nil {
		t.Skip("no /bin/sleep to leave running")
	}
	bin := doctorHost(t)
	fakeProgram(t, bin, "adb", `case "$*" in
version) echo 'Android Debug Bridge version 1.0.41' ;;
'devices -l') printf 'List of devices attached\nR5CT\tdevice usb:1-1 model:Pixel_8\n'; /bin/sleep 3 & ;;
*release) echo 14 ;;
*sdk) echo 34 ;;
*) exit 1 ;;
esac
`)
	start := time.Now()
	want := "ok    adb     " + bin + "/adb: Android Debug Bridge version 1.0.41\n" +
		"ok    device  R5CT (Pixel_8) is ready: Android 14 (SDK 34); sound and playback capture work\n"
	if got := doctorText(adbAndDevices(false)); got != want {
		t.Errorf("printed\n%s\nwant\n%s", got, want)
	}
	if took := time.Since(start); took > 2500*time.Millisecond {
		t.Errorf("took %s", took)
	}
}

// socketOnlyPactl is pactl on a desktop whose sound server listens only at
// the user's socket, under a PULSE_SERVER left from another session.
const socketOnlyPactl = "[ \"$PULSE_SERVER\" = \"unix:$XDG_RUNTIME_DIR/pulse/native\" ] ||\n" +
	"  { echo 'Connection failure: Connection refused' >&2; exit 1; }\n" + workingPactl

// TestDoctorChecksTheSound: pactl, a sound server it reaches (asked in the C
// locale, whose words doctor reads), where scterm's stream looks for one
// (libpulse's default, and when PULSE_SERVER reaches nothing the user's
// socket), XDG_RUNTIME_DIR when no server was reached, and the sink
// SCT_AUDIO_DEVICE names. All are warnings at worst: scterm plays on the
// server's default output without pactl (README), and the browser display
// plays the sound without the rest.
func TestDoctorChecksTheSound(t *testing.T) {
	const reaches = "ok    sound   pactl reaches PulseAudio (on PipeWire 1.4.2); the default output is alsa_output.pci-0000_0c_00.4.analog-stereo\n"
	const noPactl = "warn  sound   pactl not found on PATH; without it, scterm plays on the sound server's default output, " +
		"and the sound moves to another output only when the server moves it\n" +
		"              install pactl (pulseaudio-utils on Debian and Ubuntu), so that the sound follows the output you pick\n"
	const startServer = "              start PulseAudio, or PipeWire with its PulseAudio service (pipewire-pulse); the browser display (--web, --window) plays the sound without it\n"
	const noServer = "warn  sound   pactl reaches no sound server (Connection failure: Connection refused)\n" + startServer
	const noRuntimeDir = "warn  sound   XDG_RUNTIME_DIR is not set, and the sound server's socket is normally in it\n" +
		"              run scterm from a terminal of your desktop session, where it is set\n"
	const refused = "(Connection failure: Connection refused)"
	for _, tc := range []struct {
		name, pactl string // pactl is "" for none
		runtimeDir  bool
		pulseServer string // <rt> is XDG_RUNTIME_DIR
		sink        string
		want        string
	}{
		{"working", workingPactl, true, "", "", reaches},
		{"no pactl", "", true, "", "", noPactl},
		{"no sound server", refusedPactl, true, "", "", noServer},
		{"no XDG_RUNTIME_DIR, but a sound server", workingPactl, false, "", "", reaches},
		{"no XDG_RUNTIME_DIR and no sound server", refusedPactl, false, "", "", noServer + noRuntimeDir},
		{"a sink the server has", workingPactl, true, "", "bluez_output.AA_BB.1", reaches +
			"ok    sound   SCT_AUDIO_DEVICE names the sink bluez_output.AA_BB.1, which the sound server has\n"},
		{"a sink the server lacks", workingPactl, true, "", "alsa_output.pci-0000_0c_00.4", reaches +
			"warn  sound   SCT_AUDIO_DEVICE names alsa_output.pci-0000_0c_00.4, which is not one of the sound server's sinks\n" +
			"              set it to a name that pactl list sinks short shows, or unset it to follow the desktop's output\n"},
		{"a sink and no pactl", "", true, "", "alsa_output.usb", noPactl +
			"ok    sound   SCT_AUDIO_DEVICE names the sink alsa_output.usb (not checked)\n"},
		{"a sink and no sound server", refusedPactl, true, "", "alsa_output.usb", noServer +
			"ok    sound   SCT_AUDIO_DEVICE names the sink alsa_output.usb (not checked)\n"},
		{"a PULSE_SERVER that works", workingPactl, true, "tcp:mixer.lan", "", reaches},
		{"a stale PULSE_SERVER beside the user's socket", socketOnlyPactl, true, "unix:/gone/native", "bluez_output.AA_BB.1",
			"warn  sound   pactl reaches no sound server at PULSE_SERVER=unix:/gone/native " + refused +
				"; scterm falls back to unix:<rt>/pulse/native, where pactl reaches PulseAudio (on PipeWire 1.4.2); " +
				"the default output is alsa_output.pci-0000_0c_00.4.analog-stereo\n" +
				"              unset PULSE_SERVER, or set it to a sound server that runs\n" +
				"ok    sound   SCT_AUDIO_DEVICE names the sink bluez_output.AA_BB.1, which the sound server has\n"},
		{"neither PULSE_SERVER nor the user's socket", refusedPactl, true, "unix:/gone/native", "",
			"warn  sound   pactl reaches no sound server at PULSE_SERVER=unix:/gone/native " + refused +
				", nor at unix:<rt>/pulse/native " + refused + "\n" + startServer},
		{"PULSE_SERVER is the user's socket", refusedPactl, true, "unix:<rt>/pulse/native", "",
			"warn  sound   pactl reaches no sound server at PULSE_SERVER=unix:<rt>/pulse/native " + refused + "\n" + startServer},
		{"PULSE_SERVER, and no XDG_RUNTIME_DIR", refusedPactl, false, "unix:/gone/native", "",
			"warn  sound   pactl reaches no sound server at PULSE_SERVER=unix:/gone/native " + refused + "\n" + startServer + noRuntimeDir},
	} {
		bin := doctorHost(t)
		rt := os.Getenv("XDG_RUNTIME_DIR")
		if tc.pactl != "" {
			fakeProgram(t, bin, "pactl", tc.pactl)
		}
		if !tc.runtimeDir {
			t.Setenv("XDG_RUNTIME_DIR", "")
		}
		t.Setenv("PULSE_SERVER", strings.ReplaceAll(tc.pulseServer, "<rt>", rt))
		t.Setenv("SCT_AUDIO_DEVICE", tc.sink)
		lines := doctorSound()
		if got, want := doctorText(lines), strings.ReplaceAll(tc.want, "<rt>", rt); got != want {
			t.Errorf("%s: printed\n%s\nwant\n%s", tc.name, got, want)
		}
		for _, l := range lines {
			if l.status == doctorFail {
				t.Errorf("%s: a sound problem failed: %q", tc.name, l.detail)
			}
		}
	}
}

// TestDoctorNamesTheWindowBrowser: the browser --window would start, found as
// it finds one: SCTERM_CHROME, CHROME_BIN or CHROMIUM_BIN first (each one that
// names no program said), then the Chromium family on PATH or in its usual
// places, then Firefox; none at all is a warning.
func TestDoctorNamesTheWindowBrowser(t *testing.T) {
	const firefoxTodo = "              install a Chromium-family browser for a window without toolbars, sized to the device\n"
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, bin, other string)
		want  string
	}{
		{"chromium on PATH", func(t *testing.T, bin, other string) { fakeProgram(t, bin, "chromium", "") },
			"ok    window  browser <bin>/chromium\n"},
		{"another Chromium name", func(t *testing.T, bin, other string) { fakeProgram(t, bin, "brave-browser", "") },
			"ok    window  browser <bin>/brave-browser\n"},
		{"in a usual place", func(t *testing.T, bin, other string) {
			os.MkdirAll(filepath.Join(other, "brave.com", "nightly"), 0o755)
			fakeProgram(t, filepath.Join(other, "brave.com", "nightly"), "brave", "")
			chromiumGlobs = []string{filepath.Join(other, "brave.com", "*", "brave")}
		}, "ok    window  browser <other>/brave.com/nightly/brave\n"},
		{"Chromium before Firefox", func(t *testing.T, bin, other string) {
			fakeProgram(t, bin, "firefox", "")
			fakeProgram(t, bin, "google-chrome", "")
		}, "ok    window  browser <bin>/google-chrome\n"},
		{"SCTERM_CHROME", func(t *testing.T, bin, other string) {
			fakeProgram(t, bin, "chromium", "")
			t.Setenv("SCTERM_CHROME", fakeProgram(t, other, "my-chromium", ""))
		}, "ok    window  browser <other>/my-chromium (SCTERM_CHROME)\n"},
		{"SCTERM_CHROME before CHROME_BIN", func(t *testing.T, bin, other string) {
			t.Setenv("SCTERM_CHROME", fakeProgram(t, other, "my-chromium", ""))
			t.Setenv("CHROME_BIN", filepath.Join(other, "never-asked"))
		}, "ok    window  browser <other>/my-chromium (SCTERM_CHROME)\n"},
		{"CHROMIUM_BIN by name", func(t *testing.T, bin, other string) {
			fakeProgram(t, bin, "chromium", "")
			fakeProgram(t, bin, "vivaldi", "")
			t.Setenv("CHROMIUM_BIN", "vivaldi")
		}, "ok    window  browser <bin>/vivaldi (CHROMIUM_BIN)\n"},
		{"SCTERM_CHROME names nothing", func(t *testing.T, bin, other string) {
			fakeProgram(t, bin, "chromium", "")
			t.Setenv("SCTERM_CHROME", filepath.Join(other, "gone"))
		}, "warn  window  SCTERM_CHROME is \"<other>/gone\", which is not a program scterm can start\n" +
			"              set it to a Chromium-family browser, by name or path, or unset it\n" +
			"ok    window  browser <bin>/chromium\n"},
		{"CHROME_BIN not executable", func(t *testing.T, bin, other string) {
			fakeProgram(t, bin, "firefox", "")
			path := filepath.Join(other, "chrome")
			os.WriteFile(path, []byte("not a program"), 0o644)
			t.Setenv("CHROME_BIN", path)
		}, "warn  window  CHROME_BIN is \"<other>/chrome\", which is not a program scterm can start\n" +
			"              set it to a Chromium-family browser, by name or path, or unset it\n" +
			"ok    window  browser <bin>/firefox, in a normal window\n" + firefoxTodo},
		{"Firefox", func(t *testing.T, bin, other string) { fakeProgram(t, bin, "firefox", "") },
			"ok    window  browser <bin>/firefox, in a normal window\n" + firefoxTodo},
		{"Firefox, kiosk", func(t *testing.T, bin, other string) {
			fakeProgram(t, bin, "firefox", "")
			t.Setenv("SCTERM_FIREFOX_KIOSK", "1")
		}, "ok    window  browser <bin>/firefox, full screen (SCTERM_FIREFOX_KIOSK)\n" + firefoxTodo},
		{"none", func(t *testing.T, bin, other string) {},
			"warn  window  no Chromium-family browser or Firefox found\n" +
				"              install one, or set SCTERM_CHROME; --web works with any browser\n"},
	} {
		bin, other := doctorHost(t), t.TempDir()
		tc.setup(t, bin, other)
		lines := doctorWindow()
		want := strings.NewReplacer("<bin>", bin, "<other>", other).Replace(tc.want)
		if got := doctorText(lines[:len(lines)-1]); got != want {
			t.Errorf("%s: printed\n%s\nwant\n%s", tc.name, got, want)
		}
		if lines[len(lines)-1].detail != doctorDisplay().detail {
			t.Errorf("%s: the last window line is not the display: %q", tc.name, lines[len(lines)-1].detail)
		}
	}
}

// TestDoctorFindsADisplay: a display for --window by WAYLAND_DISPLAY, or by the
// sockets scterm finds itself when no variable names a usable one, as in a
// Zellij pane; with no socket at all, a warning. (A set DISPLAY is checked
// against the machine's own /tmp/.X11-unix, so it is left out here.)
func TestDoctorFindsADisplay(t *testing.T) {
	const none = "warn  window  no Wayland or X socket for this user: no browser window can open here (the web UI still works)\n" +
		"              run scterm in your desktop session, or use --web and open its address in a browser\n"
	for _, tc := range []struct {
		name               string
		wayland, x11, vars string // sockets made, as wayland-0 or X0, and the variables set
		want               string
	}{
		{"WAYLAND_DISPLAY", "wayland-0", "", "WAYLAND_DISPLAY=wayland-0 DISPLAY=:0", "ok    window  display WAYLAND_DISPLAY=wayland-0, DISPLAY=:0\n"},
		{"a compositor, unnamed", "wayland-0", "", "",
			"ok    window  no usable WAYLAND_DISPLAY in this session; using the Wayland compositor wayland-0\n"},
		{"an X server, unnamed", "", "X0", "", "ok    window  no DISPLAY in this session; using the X server :0\n"},
		{"a WAYLAND_DISPLAY with no socket", "", "", "WAYLAND_DISPLAY=wayland-9", none},
		{"nothing", "", "", "", none},
	} {
		doctorHost(t)
		if tc.wayland != "" {
			makeSocket(t, filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), tc.wayland))
		}
		if tc.x11 != "" {
			x11SocketDir = t.TempDir()
			makeSocket(t, filepath.Join(x11SocketDir, tc.x11))
		}
		for _, kv := range strings.Fields(tc.vars) {
			k, v, _ := strings.Cut(kv, "=")
			t.Setenv(k, v)
		}
		if got := doctorText([]doctorLine{doctorDisplay()}); got != tc.want {
			t.Errorf("%s: printed\n%s\nwant\n%s", tc.name, got, tc.want)
		}
	}
}

// TestDoctorChecksTheWebPort: the port is tried by listening on it, on the
// address given, and let go at once; one that something holds is a warning.
func TestDoctorChecksTheWebPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	want := fmt.Sprintf("warn  web     port %d on 127.0.0.1 is in use\n", port) +
		"              stop the program that uses it, or choose another port with --web-port PORT (0 picks a free one)\n"
	if got := doctorText([]doctorLine{doctorWebPort("127.0.0.1", port)}); got != want {
		t.Errorf("held: printed\n%s\nwant\n%s", got, want)
	}
	ln.Close()
	want = fmt.Sprintf("ok    web     port %d on 127.0.0.1 is free\n", port)
	if got := doctorText([]doctorLine{doctorWebPort("127.0.0.1", port)}); got != want {
		t.Errorf("free: printed\n%s\nwant\n%s", got, want)
	}
	again, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("doctor kept the port: %v", err)
	}
	again.Close()
	want = "warn  web     cannot listen on port 70000 on 127.0.0.1 (listen tcp: address 70000: invalid port)\n" +
		"              choose another port with --web-port PORT (0 picks a free one)\n"
	if got := doctorText([]doctorLine{doctorWebPort("127.0.0.1", 70000)}); got != want {
		t.Errorf("impossible port: printed\n%s\nwant\n%s", got, want)
	}
}

// TestDoctorReadsWebHosts: the names SCTERM_WEB_HOSTS adds, read as the
// browser display reads them, each once, as the server keeps a set of them;
// and each entry the server ignores (one that is not a name, or a name with an
// empty label), said once for each time it appears, quoted and advised as the
// server's notice does (web.go, allowedNames).
func TestDoctorReadsWebHosts(t *testing.T) {
	const notAName = "              an entry must be a name, or a name and a port, such as mirror.lan or mirror.lan:8443\n"
	const noMatch = "              no request can match a name that starts with a dot or has two dots in a row; " +
		"list each name in full, such as mirror.example.lan\n"
	for _, tc := range []struct{ value, want string }{
		{"", ""},
		{" , ", ""},
		{" Mirror.Example.lan,,scterm.example.org:8443 , ", "ok    web     SCTERM_WEB_HOSTS adds mirror.example.lan, scterm.example.org\n"},
		{"mirror.lan,MIRROR.lan:8443, mirror.lan., scterm.lan,mirror.lan", "ok    web     SCTERM_WEB_HOSTS adds mirror.lan, scterm.lan\n"},
		{"http://x, http://x", "warn  web     SCTERM_WEB_HOSTS has \"http://x\", which scterm ignores\n" + notAName +
			"warn  web     SCTERM_WEB_HOSTS has \"http://x\", which scterm ignores\n" + notAName},
		{"http://mirror.lan, good.lan,[::1", "ok    web     SCTERM_WEB_HOSTS adds good.lan\n" +
			"warn  web     SCTERM_WEB_HOSTS has \"http://mirror.lan\", which scterm ignores\n" + notAName +
			"warn  web     SCTERM_WEB_HOSTS has \"[::1\", which scterm ignores\n" + notAName},
		{".example.lan,box..lan,box.lan..,box.lan.", "ok    web     SCTERM_WEB_HOSTS adds box.lan\n" +
			"warn  web     SCTERM_WEB_HOSTS has \".example.lan\", which scterm ignores\n" + noMatch +
			"warn  web     SCTERM_WEB_HOSTS has \"box..lan\", which scterm ignores\n" + noMatch +
			"warn  web     SCTERM_WEB_HOSTS has \"box.lan..\", which scterm ignores\n" + noMatch},
		{"\u212aitchen.lan", "warn  web     SCTERM_WEB_HOSTS has \"\\u212aitchen.lan\", which scterm ignores\n" +
			"              an entry must be a name, or a name and a port, such as mirror.lan or mirror.lan:8443; " +
			"write an international name in its xn-- form\n"},
	} {
		t.Setenv(webHostsEnv, tc.value)
		if got := doctorText(doctorWebHosts()); got != tc.want {
			t.Errorf("%q: printed\n%s\nwant\n%s", tc.value, got, tc.want)
		}
	}
}

// TestDoctorChecksThePairings: where peer mode keeps this computer's identity
// and its pairings, whether the identity is there, can be read, and can be
// read by other users too (scterm writes it with mode 0600), and how many
// devices are paired, without creating anything or connecting anywhere. A
// file that cannot be read for its permissions gets them fixed, which keeps
// the pairings; a damaged one is moved aside. The count runDoctor gets is of
// the paired devices peer mode can reach: none without an identity it can
// load, and none whose identity in peers.json does not parse.
func TestDoctorChecksThePairings(t *testing.T) {
	const pairTodo = "              scterm pair pairs with a device that runs the scterm app, to reach it without adb\n"
	identity := func(t *testing.T, home string) string {
		id, err := peer.LoadOrCreateIdentity(home)
		if err != nil {
			t.Fatal(err)
		}
		return id.Fingerprint.Short()
	}
	// withMode gives a file of home the permissions mode, once made.
	withMode := func(name string, mode os.FileMode, setup func(*testing.T, string) string) func(*testing.T, string) string {
		return func(t *testing.T, home string) string {
			fp := setup(t, home)
			if err := os.Chmod(filepath.Join(home, name), mode); err != nil {
				t.Fatal(err)
			}
			return fp
		}
	}
	readableBy := func(mode string) string {
		return "ok    peers   this computer is <fp> (identity in <home> (SCTERM_HOME))\n" +
			"warn  peers   other users can read <home>/identity.pem (mode " + mode + "), which holds this computer's private key\n" +
			"              chmod 600 <home>/identity.pem\n" +
			"ok    peers   no paired devices\n" + pairTodo
	}
	root := os.Geteuid() == 0 // who reads files whatever their mode
	paired := func(t *testing.T, home string, names ...string) {
		for i, n := range names {
			rec := peer.Record{Fingerprint: fmt.Sprintf("%064x", i+1), Name: n, Host: "192.0.2.1", Port: 27300, Grants: []string{"view"}, PairedAtMs: 1}
			if err := peer.OpenStore(home).Put(rec); err != nil {
				t.Fatal(err)
			}
		}
	}
	pairedWith := func(t *testing.T, home, fingerprint, name string) {
		rec := peer.Record{Fingerprint: fingerprint, Name: name, Host: "192.0.2.1", Port: 27300, Grants: []string{"view"}, PairedAtMs: 1}
		if err := peer.OpenStore(home).Put(rec); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, home string) (fingerprint string)
		want  string
		count int
	}{
		{"nothing yet", func(t *testing.T, home string) string { return "" },
			"ok    peers   nothing paired yet; pairings are kept in <home> (SCTERM_HOME)\n" + pairTodo, 0},
		{"an identity", identity,
			"ok    peers   this computer is <fp> (identity in <home> (SCTERM_HOME))\n" +
				"ok    peers   no paired devices\n" + pairTodo, 0},
		{"one paired", func(t *testing.T, home string) string {
			paired(t, home, "Pixel 8")
			return identity(t, home)
		}, "ok    peers   this computer is <fp> (identity in <home> (SCTERM_HOME))\n" +
			"ok    peers   1 paired device: Pixel 8\n" +
			"              scterm peers --check tests whether it is reachable\n", 1},
		{"two paired", func(t *testing.T, home string) string {
			paired(t, home, "Pixel 8", "Tab\x1b[2J S9")
			return identity(t, home)
		}, "ok    peers   this computer is <fp> (identity in <home> (SCTERM_HOME))\n" +
			"ok    peers   2 paired devices: Pixel 8, Tab[2J S9\n" +
			"              scterm peers --check tests whether each one is reachable\n", 2},
		// Without an identity it can load, peer mode reaches none of them.
		{"paired, the identity gone", func(t *testing.T, home string) string { paired(t, home, "Pixel 8"); return "" },
			"FAIL  peers   no identity in <home> (SCTERM_HOME), so the paired devices no longer know this computer\n" +
				"              pair each device again with scterm pair\n" +
				"ok    peers   1 paired device: Pixel 8\n" +
				"              scterm peers --check tests whether it is reachable\n", 0},
		{"a damaged identity", func(t *testing.T, home string) string {
			os.WriteFile(filepath.Join(home, "identity.pem"), []byte("not a key"), 0o600)
			return ""
		}, "FAIL  peers   <home>/identity.pem: tls: failed to find any PEM data in certificate input\n" +
			"              move <home>/identity.pem aside, then pair each device again (scterm pair makes a new identity)\n" +
			"ok    peers   no paired devices\n" + pairTodo, 0},
		{"paired, the identity damaged", func(t *testing.T, home string) string {
			paired(t, home, "Pixel 8")
			os.WriteFile(filepath.Join(home, "identity.pem"), []byte("not a key"), 0o600)
			return ""
		}, "FAIL  peers   <home>/identity.pem: tls: failed to find any PEM data in certificate input\n" +
			"              move <home>/identity.pem aside, then pair each device again (scterm pair makes a new identity)\n" +
			"ok    peers   1 paired device: Pixel 8\n" +
			"              scterm peers --check tests whether it is reachable\n", 0},
		// A pairing whose identity does not parse is left out by scterm peers
		// and --peer, so it is not counted, and says so.
		{"a pairing that does not parse", func(t *testing.T, home string) string {
			pairedWith(t, home, "not-a-fingerprint", "Pixel 8")
			return identity(t, home)
		}, "ok    peers   this computer is <fp> (identity in <home> (SCTERM_HOME))\n" +
			"ok    peers   no paired devices\n" + pairTodo +
			"warn  peers   1 pairing in <home>/peers.json has an identity that does not parse, so peer mode leaves it out: Pixel 8\n" +
			"              pair that device again with scterm pair\n", 0},
		{"one of two pairings does not parse", func(t *testing.T, home string) string {
			paired(t, home, "Tab S9")
			pairedWith(t, home, fmt.Sprintf("%063x", 2), "Pixel 8")
			return identity(t, home)
		}, "ok    peers   this computer is <fp> (identity in <home> (SCTERM_HOME))\n" +
			"ok    peers   1 paired device: Tab S9\n" +
			"              scterm peers --check tests whether it is reachable\n" +
			"warn  peers   1 pairing in <home>/peers.json has an identity that does not parse, so peer mode leaves it out: Pixel 8\n" +
			"              pair that device again with scterm pair\n", 1},
		{"two pairings do not parse", func(t *testing.T, home string) string {
			pairedWith(t, home, "", "Pixel 8")
			pairedWith(t, home, fmt.Sprintf("%064x", 3)+"0", "Tab S9")
			return identity(t, home)
		}, "ok    peers   this computer is <fp> (identity in <home> (SCTERM_HOME))\n" +
			"ok    peers   no paired devices\n" + pairTodo +
			"warn  peers   2 pairings in <home>/peers.json have identities that do not parse, so peer mode leaves them out: Pixel 8, Tab S9\n" +
			"              pair those devices again with scterm pair\n", 0},
		{"damaged pairings", func(t *testing.T, home string) string {
			os.WriteFile(filepath.Join(home, "peers.json"), []byte("[{"), 0o600)
			return ""
		}, "ok    peers   no identity in <home> (SCTERM_HOME) yet; scterm pair makes one\n" +
			"FAIL  peers   <home>/peers.json: unexpected end of JSON input\n" +
			"              move peers.json aside, then pair each device again\n", 0},
		{"an identity this user cannot read", withMode("identity.pem", 0, identity),
			"FAIL  peers   open <home>/identity.pem: permission denied\n" +
				"              let your user read it again (as their owner: chmod 700 <home>; chmod 600 <home>/identity.pem); that keeps the pairings\n" +
				"ok    peers   no paired devices\n" + pairTodo, 0},
		{"pairings this user cannot read", withMode("peers.json", 0o200, func(t *testing.T, home string) string {
			paired(t, home, "Pixel 8")
			return identity(t, home)
		}), "ok    peers   this computer is <fp> (identity in <home> (SCTERM_HOME))\n" +
			"FAIL  peers   open <home>/peers.json: permission denied\n" +
			"              let your user read it again (as their owner: chmod 700 <home>; chmod 600 <home>/peers.json); that keeps the pairings\n", 0},
		{"a home this user cannot search", func(t *testing.T, home string) string {
			paired(t, home, "Pixel 8")
			identity(t, home)
			t.Cleanup(func() { os.Chmod(home, 0o700) }) // before the TempDir is removed
			if err := os.Chmod(home, 0o600); err != nil {
				t.Fatal(err)
			}
			return ""
		}, "FAIL  peers   open <home>/identity.pem: permission denied\n" +
			"              let your user read it again (as their owner: chmod 700 <home>; chmod 600 <home>/identity.pem); that keeps the pairings\n" +
			"FAIL  peers   open <home>/peers.json: permission denied\n" +
			"              let your user read it again (as their owner: chmod 700 <home>; chmod 600 <home>/peers.json); that keeps the pairings\n", 0},
		{"an identity its group can read", withMode("identity.pem", 0o640, identity), readableBy("0640"), 0},
		{"an identity anyone can read", withMode("identity.pem", 0o604, identity), readableBy("0604"), 0},
		{"an identity its group can write but not read", withMode("identity.pem", 0o620, identity),
			"ok    peers   this computer is <fp> (identity in <home> (SCTERM_HOME))\n" +
				"ok    peers   no paired devices\n" + pairTodo, 0},
		{"an identity only its owner can use", withMode("identity.pem", 0o700, identity),
			"ok    peers   this computer is <fp> (identity in <home> (SCTERM_HOME))\n" +
				"ok    peers   no paired devices\n" + pairTodo, 0},
	} {
		if root && (strings.Contains(tc.name, "cannot read") || strings.Contains(tc.name, "cannot search")) {
			continue
		}
		doctorHost(t)
		home := os.Getenv("SCTERM_HOME")
		fp := tc.setup(t, home)
		before := files(t, home)
		lines, count := doctorPeers()
		want := strings.NewReplacer("<home>", home, "<fp>", fp).Replace(tc.want)
		if got := doctorText(lines); got != want || count != tc.count {
			t.Errorf("%s: printed\n%s\nand %d paired; want\n%s\nand %d", tc.name, got, count, want, tc.count)
		}
		if after := files(t, home); fmt.Sprint(after) != fmt.Sprint(before) {
			t.Errorf("%s: the files went from %q to %q", tc.name, before, after)
		}
	}

	// A home that does not exist yet stays so.
	doctorHost(t)
	home := filepath.Join(os.Getenv("SCTERM_HOME"), "later")
	t.Setenv("SCTERM_HOME", home)
	if lines, _ := doctorPeers(); len(lines) != 1 || lines[0].status != doctorOK {
		t.Errorf("a home not made yet: %q", doctorText(lines))
	}
	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("doctor made %s: %v", home, err)
	}

	// Without SCTERM_HOME, XDG_CONFIG_HOME or HOME there is nowhere to look.
	t.Setenv("SCTERM_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	want := "warn  peers   no place to keep peer mode's identity and pairings (neither $XDG_CONFIG_HOME nor $HOME are defined)\n" +
		"              set HOME, or set SCTERM_HOME to a directory for them\n"
	if lines, count := doctorPeers(); doctorText(lines) != want || count != 0 {
		t.Errorf("no home: printed\n%s\nwant\n%s", doctorText(lines), want)
	}
}

// TestDoctorOnAHealthyAndAnUnhealthyComputer: every check in order, as scterm
// doctor prints them, on a computer that has all scterm needs, and on one
// that has little of it.
func TestDoctorOnAHealthyAndAnUnhealthyComputer(t *testing.T) {
	bin := doctorHost(t)
	home := os.Getenv("SCTERM_HOME")
	fakeProgram(t, bin, "adb", adbScript(devicesHeader+"R5CT\tdevice usb:1-1 product:shiba model:Pixel_8 device:shiba transport_id:1\n",
		map[string]string{"R5CT": "14 34"}))
	fakeProgram(t, bin, "pactl", workingPactl)
	fakeProgram(t, bin, "chromium", "")
	makeSocket(t, filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "wayland-0"))
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	id, err := peer.LoadOrCreateIdentity(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := peer.OpenStore(home).Put(peer.Record{Fingerprint: fmt.Sprintf("%064x", 1), Name: "Pixel 8", Host: "192.0.2.1", Port: 27300, PairedAtMs: 1}); err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	cfg.webPort = freePort(t)
	want := buildLine() +
		"ok    adb     <bin>/adb: Android Debug Bridge version 1.0.41, Version 35.0.2-12147458\n" +
		"ok    device  R5CT (Pixel_8) is ready: Android 14 (SDK 34); sound and playback capture work\n" +
		"ok    sound   pactl reaches PulseAudio (on PipeWire 1.4.2); the default output is alsa_output.pci-0000_0c_00.4.analog-stereo\n" +
		"ok    window  browser <bin>/chromium\n" +
		"ok    window  display WAYLAND_DISPLAY=wayland-0\n" +
		"ok    web     port <port> on 127.0.0.1 is free\n" +
		"ok    peers   this computer is <fp> (identity in <home> (SCTERM_HOME))\n" +
		"ok    peers   1 paired device: Pixel 8\n" +
		"              scterm peers --check tests whether it is reachable\n"
	want = strings.NewReplacer("<bin>", bin, "<home>", home, "<fp>", id.Fingerprint.Short(), "<port>", strconv.Itoa(cfg.webPort)).Replace(want)
	var b strings.Builder
	if failed := runDoctor(&b, cfg); b.String() != want || failed {
		t.Errorf("healthy: printed\n%s\nfailed=%v; want\n%s", b.String(), failed, want)
	}

	bin = doctorHost(t)
	home = os.Getenv("SCTERM_HOME")
	cpuinfoPath = cpuinfoFile(t, "fpu sse2 avx")
	fakeProgram(t, bin, "pactl", refusedPactl)
	t.Setenv(webHostsEnv, "http://mirror.lan")
	os.WriteFile(filepath.Join(home, "peers.json"), []byte("[{"), 0o600)
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	cfg.webPort = held.Addr().(*net.TCPAddr).Port
	build := "FAIL  build   scterm dev for linux/amd64; the CPU has no AVX2, which every x86-64 build of scterm uses\n" +
		"              scterm cannot run on this CPU, built from source or not: it stops with SIGILL: illegal instruction\n"
	if runtime.GOARCH != "amd64" {
		build = buildLine()
	}
	want = build +
		"FAIL  adb     not found on PATH\n" +
		"              install Android platform-tools (often packaged as android-tools or adb), or use --peer NAME to reach the scterm Android app without it\n" +
		"warn  sound   pactl reaches no sound server (Connection failure: Connection refused)\n" +
		"              start PulseAudio, or PipeWire with its PulseAudio service (pipewire-pulse); the browser display (--web, --window) plays the sound without it\n" +
		"warn  window  no Chromium-family browser or Firefox found\n" +
		"              install one, or set SCTERM_CHROME; --web works with any browser\n" +
		"warn  window  no Wayland or X socket for this user: no browser window can open here (the web UI still works)\n" +
		"              run scterm in your desktop session, or use --web and open its address in a browser\n" +
		"warn  web     port <port> on 127.0.0.1 is in use\n" +
		"              stop the program that uses it, or choose another port with --web-port PORT (0 picks a free one)\n" +
		"warn  web     SCTERM_WEB_HOSTS has \"http://mirror.lan\", which scterm ignores\n" +
		"              an entry must be a name, or a name and a port, such as mirror.lan or mirror.lan:8443\n" +
		"ok    peers   no identity in <home> (SCTERM_HOME) yet; scterm pair makes one\n" +
		"FAIL  peers   <home>/peers.json: unexpected end of JSON input\n" +
		"              move peers.json aside, then pair each device again\n"
	want = strings.NewReplacer("<home>", home, "<port>", strconv.Itoa(cfg.webPort)).Replace(want)
	b.Reset()
	if failed := runDoctor(&b, cfg); b.String() != want || !failed {
		t.Errorf("unhealthy: printed\n%s\nfailed=%v; want\n%s", b.String(), failed, want)
	}
}

// TestDoctorOnAPeerOnlyComputer: a computer that reaches its device through
// the scterm app passes without adb, and with an adb that lists no device:
// each is a warning there, whose advice gives --peer as it can be typed:
// --peer= for the only paired device, --peer NAME for one of several. Only a
// pairing peer mode can use counts: with no identity, a damaged one, or a
// pairing whose identity does not parse, both fail. And what adb does list is
// what plain scterm would use, so a pairing changes nothing about it: an
// offline device, and an ANDROID_SERIAL that names no device or several,
// still fail.
func TestDoctorOnAPeerOnlyComputer(t *testing.T) {
	const (
		adbOK       = "ok    adb     <bin>/adb: Android Debug Bridge version 1.0.41, Version 35.0.2-12147458\n"
		pixel       = "\tdevice usb:1-1 product:shiba model:Pixel_8 device:shiba transport_id:1\n"
		pixelUnread = " (Pixel_8) is ready; scterm would not use it, so doctor runs nothing on it\n"
		oneListed   = "ok    peers   1 paired device: Pixel 8\n              scterm peers --check tests whether it is reachable\n"
		identityOK  = "ok    peers   this computer is <fp> (identity in <home> (SCTERM_HOME))\n"
	)
	pair := func(t *testing.T, home, fingerprint, name string) {
		if err := peer.OpenStore(home).Put(peer.Record{Fingerprint: fingerprint, Name: name, Host: "192.0.2.1", Port: 27300, PairedAtMs: 1}); err != nil {
			t.Fatal(err)
		}
	}
	identity := func(t *testing.T, home string) string {
		id, err := peer.LoadOrCreateIdentity(home)
		if err != nil {
			t.Fatal(err)
		}
		return id.Fingerprint.Short()
	}
	onePaired := func(t *testing.T, home string) string {
		pair(t, home, fmt.Sprintf("%064x", 1), "Pixel 8")
		return identity(t, home)
	}
	for _, tc := range []struct {
		name, adb, serial string
		pairings          func(t *testing.T, home string) (fingerprint string)
		want, peers       string
		failed            bool
	}{
		{"no adb", "", "", onePaired,
			"warn  adb     not found on PATH\n" +
				"              install Android platform-tools (often packaged as android-tools or adb), or use --peer= to reach the scterm Android app without it\n",
			identityOK + oneListed, false},
		{"an adb that lists no device", adbScript(devicesHeader+"\n", nil), "", onePaired,
			adbOK + "warn  device  no device found\n" +
				"              connect one with USB debugging turned on (or over Wi-Fi with adb pair and adb connect), or use --peer= for the scterm Android app\n",
			identityOK + oneListed, false},
		{"no adb, two paired", "", "", func(t *testing.T, home string) string {
			pair(t, home, fmt.Sprintf("%064x", 1), "Pixel 8")
			pair(t, home, fmt.Sprintf("%064x", 2), "Tab S9")
			return identity(t, home)
		}, "warn  adb     not found on PATH\n" +
			"              install Android platform-tools (often packaged as android-tools or adb), or use --peer NAME to reach the scterm Android app without it\n",
			identityOK + "ok    peers   2 paired devices: Pixel 8, Tab S9\n              scterm peers --check tests whether each one is reachable\n", false},

		// Pairings peer mode cannot use.
		{"no adb, the identity gone", "", "", func(t *testing.T, home string) string {
			pair(t, home, fmt.Sprintf("%064x", 1), "Pixel 8")
			return ""
		}, "FAIL  adb     not found on PATH\n" +
			"              install Android platform-tools (often packaged as android-tools or adb), or use --peer NAME to reach the scterm Android app without it\n",
			"FAIL  peers   no identity in <home> (SCTERM_HOME), so the paired devices no longer know this computer\n" +
				"              pair each device again with scterm pair\n" + oneListed, true},
		{"no adb, a pairing that does not parse", "", "", func(t *testing.T, home string) string {
			pair(t, home, "not-a-fingerprint", "Pixel 8")
			return identity(t, home)
		}, "FAIL  adb     not found on PATH\n" +
			"              install Android platform-tools (often packaged as android-tools or adb), or use --peer NAME to reach the scterm Android app without it\n",
			identityOK + "ok    peers   no paired devices\n" +
				"              scterm pair pairs with a device that runs the scterm app, to reach it without adb\n" +
				"warn  peers   1 pairing in <home>/peers.json has an identity that does not parse, so peer mode leaves it out: Pixel 8\n" +
				"              pair that device again with scterm pair\n", true},
		{"an adb that lists no device, the identity damaged", adbScript(devicesHeader+"\n", nil), "", func(t *testing.T, home string) string {
			pair(t, home, fmt.Sprintf("%064x", 1), "Pixel 8")
			if err := os.WriteFile(filepath.Join(home, "identity.pem"), []byte("not a key"), 0o600); err != nil {
				t.Fatal(err)
			}
			return ""
		}, adbOK + "FAIL  device  no device found\n" +
			"              connect one with USB debugging turned on (or over Wi-Fi with adb pair and adb connect), or use --peer NAME for the scterm Android app\n",
			"FAIL  peers   <home>/identity.pem: tls: failed to find any PEM data in certificate input\n" +
				"              move <home>/identity.pem aside, then pair each device again (scterm pair makes a new identity)\n" + oneListed, true},

		// What adb lists, with a pairing.
		{"an offline device", adbScript(devicesHeader+"R5CT\toffline usb:1-1 transport_id:4\n", nil), "", onePaired,
			adbOK + "FAIL  device  R5CT is offline\n" +
				"              unplug and replug it, or restart adb with 'adb kill-server'\n",
			identityOK + oneListed, true},
		{"ANDROID_SERIAL names no device", adbScript(devicesHeader+"R5CT"+pixel, map[string]string{"R5CT": "14 34"}), "R5CU", onePaired,
			adbOK + "ok    device  R5CT" + pixelUnread +
				"FAIL  device  ANDROID_SERIAL is R5CU, which names no device adb lists\n" +
				"              connect that device, or unset ANDROID_SERIAL, or set it as adb takes it: a serial, a network device's host " +
				"without its port (tcp: or udp: may come first), a USB path such as usb:1-1, or product:, model: or device: and a name, as adb devices -l shows them\n",
			identityOK + oneListed, true},
		{"ANDROID_SERIAL names two devices", adbScript(devicesHeader+"R5CT"+pixel+"R5CU"+pixel, map[string]string{"R5CT": "14 34", "R5CU": "14 34"}), "model:Pixel_8", onePaired,
			adbOK + "ok    device  R5CT" + pixelUnread + "ok    device  R5CU" + pixelUnread +
				"FAIL  device  ANDROID_SERIAL is model:Pixel_8, which names 2 devices (R5CT, R5CU), so adb takes none of them\n" +
				"              set it to the serial of the one to use\n",
			identityOK + oneListed, true},
	} {
		bin := doctorHost(t)
		home := os.Getenv("SCTERM_HOME")
		if tc.adb != "" {
			fakeProgram(t, bin, "adb", tc.adb)
		}
		t.Setenv("ANDROID_SERIAL", tc.serial)
		fakeProgram(t, bin, "pactl", workingPactl)
		fakeProgram(t, bin, "chromium", "")
		makeSocket(t, filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "wayland-0"))
		t.Setenv("WAYLAND_DISPLAY", "wayland-0")
		fp := tc.pairings(t, home)
		cfg := defaultConfig()
		cfg.webPort = freePort(t)
		want := buildLine() + tc.want +
			"ok    sound   pactl reaches PulseAudio (on PipeWire 1.4.2); the default output is alsa_output.pci-0000_0c_00.4.analog-stereo\n" +
			"ok    window  browser <bin>/chromium\n" +
			"ok    window  display WAYLAND_DISPLAY=wayland-0\n" +
			"ok    web     port <port> on 127.0.0.1 is free\n" + tc.peers
		want = strings.NewReplacer("<bin>", bin, "<home>", home, "<fp>", fp, "<port>", strconv.Itoa(cfg.webPort)).Replace(want)
		var b strings.Builder
		if failed := runDoctor(&b, cfg); b.String() != want || failed != tc.failed {
			t.Errorf("%s: printed\n%s\nfailed=%v; want\n%s\nfailed=%v", tc.name, b.String(), failed, want, tc.failed)
		}
	}
}

// TestDoctorPrintsEachCheckAsItEnds: the lines of each check are out before
// the next check runs a program, so that a slow adb or sound server leaves
// what came before it on the screen. Each fake program here answers only once
// doctor has printed the line that comes before its own check.
func TestDoctorPrintsEachCheckAsItEnds(t *testing.T) {
	bin := doctorHost(t)
	home := os.Getenv("SCTERM_HOME")
	out := filepath.Join(t.TempDir(), "out")
	// need answers only once a line that starts with $1 is out (shell
	// builtins only: PATH holds nothing else).
	need := `need() {
  while IFS= read -r l; do case "$l" in "$1"*) return ;; esac; done < '` + out + `'
  echo "doctor has not printed $1 yet" >&2; exit 1
}
`
	fakeProgram(t, bin, "adb", need+`case "$*" in
version) need 'ok    build'; echo 'Android Debug Bridge version 1.0.41' ;;
'devices -l') need 'ok    adb'; printf 'List of devices attached\nR5CT\tdevice usb:1-1 model:Pixel_8\n' ;;
*release) need 'ok    adb'; echo 14 ;;
*sdk) need 'ok    adb'; echo 34 ;;
*) exit 1 ;;
esac
`)
	fakeProgram(t, bin, "pactl", need+"need 'ok    device'\n"+workingPactl)
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg := defaultConfig()
	cfg.webPort = freePort(t)
	failed := runDoctor(f, cfg)
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	want := buildLine() +
		"ok    adb     <bin>/adb: Android Debug Bridge version 1.0.41\n" +
		"ok    device  R5CT (Pixel_8) is ready: Android 14 (SDK 34); sound and playback capture work\n" +
		"ok    sound   pactl reaches PulseAudio (on PipeWire 1.4.2); the default output is alsa_output.pci-0000_0c_00.4.analog-stereo\n" +
		"warn  window  no Chromium-family browser or Firefox found\n" +
		"              install one, or set SCTERM_CHROME; --web works with any browser\n" +
		"warn  window  no Wayland or X socket for this user: no browser window can open here (the web UI still works)\n" +
		"              run scterm in your desktop session, or use --web and open its address in a browser\n" +
		"ok    web     port <port> on 127.0.0.1 is free\n" +
		"ok    peers   nothing paired yet; pairings are kept in <home> (SCTERM_HOME)\n" +
		"              scterm pair pairs with a device that runs the scterm app, to reach it without adb\n"
	want = strings.NewReplacer("<bin>", bin, "<home>", home, "<port>", strconv.Itoa(cfg.webPort)).Replace(want)
	if string(got) != want || failed {
		t.Errorf("printed\n%s\nfailed=%v; want\n%s", got, failed, want)
	}
}

// freePort is a loopback port that was free a moment ago.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// TestDoctorCommandLine runs scterm doctor as a user does, in a child process
// whose environment holds only PATH, a directory with nothing but a fake adb
// and pactl, and XDG_RUNTIME_DIR, an empty directory (so this host's display
// sockets stay out): what it prints on stdout, nothing on stderr, and its exit
// status, 1 when a check fails and 0 with warnings only. SCTERM_TEST_DOCTOR
// gives the child the test's CPU flags and X socket directory, and the usual
// places of this host's browsers are out of its reach. Port 6969 is held here
// when it is free, so that its line reads the same either way.
func TestDoctorCommandLine(t *testing.T) {
	if dir := os.Getenv("SCTERM_TEST_DOCTOR"); dir != "" {
		cpuinfoPath = filepath.Join(dir, "cpuinfo")
		x11SocketDir = filepath.Join(dir, "x11")
		chromiumGlobs = nil
		os.Args = []string{"scterm", "doctor"}
		main()
		os.Exit(0) // as when main returns, without the test's own report
	}
	if ln, err := net.Listen("tcp", "127.0.0.1:6969"); err == nil {
		defer ln.Close()
	}
	const unauthorized = "List of devices attached\nR5CT\tunauthorized usb:1-1 transport_id:3\n"
	const ready = "List of devices attached\nR5CT\tdevice usb:1-1 product:shiba model:Pixel_8 device:shiba transport_id:1\n"
	const rest = "warn  window  no Chromium-family browser or Firefox found\n" +
		"              install one, or set SCTERM_CHROME; --web works with any browser\n" +
		"warn  window  no Wayland or X socket for this user: no browser window can open here (the web UI still works)\n" +
		"              run scterm in your desktop session, or use --web and open its address in a browser\n" +
		"warn  web     port 6969 on 127.0.0.1 is in use\n" +
		"              stop the program that uses it, or choose another port with --web-port PORT (0 picks a free one)\n" +
		"warn  peers   no place to keep peer mode's identity and pairings (neither $XDG_CONFIG_HOME nor $HOME are defined)\n" +
		"              set HOME, or set SCTERM_HOME to a directory for them\n"
	for _, tc := range []struct {
		name, adb, pactl string
		status           int
		stdout           string
	}{
		{"a check fails", adbScript(unauthorized, nil), refusedPactl, 1, buildLine() +
			"ok    adb     <bin>/adb: Android Debug Bridge version 1.0.41, Version 35.0.2-12147458\n" +
			"FAIL  device  R5CT has not allowed USB debugging from this computer\n" +
			"              unlock it, accept the \"Allow USB debugging?\" prompt, and run scterm again\n" +
			"warn  sound   pactl reaches no sound server (Connection failure: Connection refused)\n" +
			"              start PulseAudio, or PipeWire with its PulseAudio service (pipewire-pulse); the browser display (--web, --window) plays the sound without it\n" +
			rest},
		{"warnings only", adbScript(ready, map[string]string{"R5CT": "14 34"}), workingPactl, 0, buildLine() +
			"ok    adb     <bin>/adb: Android Debug Bridge version 1.0.41, Version 35.0.2-12147458\n" +
			"ok    device  R5CT (Pixel_8) is ready: Android 14 (SDK 34); sound and playback capture work\n" +
			"ok    sound   pactl reaches PulseAudio (on PipeWire 1.4.2); the default output is alsa_output.pci-0000_0c_00.4.analog-stereo\n" +
			rest},
	} {
		dir, bin, runtimeDir := t.TempDir(), t.TempDir(), t.TempDir()
		if err := os.Rename(cpuinfoFile(t, "fpu sse2 avx avx2"), filepath.Join(dir, "cpuinfo")); err != nil {
			t.Fatal(err)
		}
		fakeProgram(t, bin, "adb", tc.adb)
		fakeProgram(t, bin, "pactl", tc.pactl)
		cmd := exec.Command(os.Args[0], "-test.run=^TestDoctorCommandLine$")
		cmd.Env = []string{"SCTERM_TEST_DOCTOR=" + dir, "PATH=" + bin, "XDG_RUNTIME_DIR=" + runtimeDir}
		if cover, ok := os.LookupEnv("GOCOVERDIR"); ok {
			cmd.Env = append(cmd.Env, "GOCOVERDIR="+cover)
		}
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		status := 0
		var exit *exec.ExitError
		if err := cmd.Run(); errors.As(err, &exit) {
			status = exit.ExitCode()
		} else if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		want := strings.ReplaceAll(tc.stdout, "<bin>", bin)
		if status != tc.status || stdout.String() != want || stderr.String() != "" {
			t.Errorf("%s: status %d, stdout\n%s\nstderr %q; want %d, stdout\n%s\nand nothing on stderr",
				tc.name, status, stdout.String(), stderr.String(), tc.status, want)
		}
	}
}
