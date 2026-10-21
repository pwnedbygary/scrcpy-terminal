package main

import "testing"

// TestMatchesTargetAsADBDoes: which values adb -s takes for a device, as
// atransport::MatchesTarget (AOSP packages/modules/adb transport.cpp, lines
// 1289-1323 at 1cf2f017) and qual_match (lines 861-882) decide: the serial;
// for a network device (here, one listed without a USB path), its host and
// port, the port left out or not and tcp: or udp: before them or not; the USB
// path; and product:, model: or device: with a name the device has.
func TestMatchesTargetAsADBDoes(t *testing.T) {
	wifi := adbDevice{serial: "192.168.1.20:5555", state: "device", product: "shiba", model: "Pixel_8", device: "shiba"}
	usb := adbDevice{serial: "R5CT", state: "device", devpath: "usb:1-1", product: "gts9", model: "Tab_S9", device: "gts9"}
	offline := adbDevice{serial: "192.168.1.30:5555", state: "offline"}
	mdns := adbDevice{serial: "adb-R5CT-AbCdEf._adb-tls-connect._tcp", state: "device"}
	emulator := adbDevice{serial: "emulator-5554", state: "device"}
	ipv6 := adbDevice{serial: "[fe80::1]:5555", state: "device"}
	for _, tc := range []struct {
		d      adbDevice
		target string
		want   bool
	}{
		{wifi, "192.168.1.20:5555", true},
		{wifi, "192.168.1.20", true},
		{wifi, "tcp:192.168.1.20", true},
		{wifi, "udp:192.168.1.20:5555", true},
		{wifi, "192.168.1.20:5555x", true}, // sscanf reads the port's digits and stops
		{wifi, "192.168.1.20:5556", false},
		{wifi, "192.168.1.20:", false},
		{wifi, "192.168.1.2", false},
		{wifi, "TCP:192.168.1.20", false},
		{wifi, "tcp:", false},
		{wifi, "model:Pixel_8", true},
		{wifi, "product:shiba", true},
		{wifi, "device:shiba", true},
		{wifi, "model:pixel_8", false},
		{wifi, "shiba", false},
		{usb, "R5CT", true},
		{usb, "usb:1-1", true},
		{usb, "product:gts9", true},
		{usb, "model:Tab_S9", true},
		{usb, "device:gts9", true},
		{usb, "tcp:R5CT", false},
		{usb, "R5CT:5555", false},
		{usb, "usb:1-2", false},
		{offline, "192.168.1.30", true},
		{offline, "product:", false},
		{offline, "model:", false},
		{offline, "device:", false},
		{mdns, "tcp:adb-R5CT-AbCdEf._adb-tls-connect._tcp", true},
		{mdns, "adb-R5CT-AbCdEf._adb-tls-connect._tcp:5555", false},
		{emulator, "tcp:emulator-5554", true},
		{emulator, "emulator-5554:5554", false},
		{ipv6, "fe80::1", true},
		{ipv6, "tcp:[fe80::1]:5555", true},
		{ipv6, "[fe80::1]", false},
		{ipv6, "fe80::2", false},
	} {
		if got := tc.d.matchesTarget(tc.target); got != tc.want {
			t.Errorf("%s answers to %q: %v, want %v", tc.d.serial, tc.target, got, tc.want)
		}
	}
}

// TestMatchesTargetCannotTellAUSBDeviceWithoutAPath: a USB device listed
// without a USB path (adb's native Linux backend, when it cannot read the
// device's sysfs link) answers as a network device would, where adb takes
// only its serial: to that serial after tcp: or udp:, and, when the serial
// reads as a host and a port, to the host alone.
func TestMatchesTargetCannotTellAUSBDeviceWithoutAPath(t *testing.T) {
	colon := adbDevice{serial: "ABC:12", state: "device"}
	plain := adbDevice{serial: "R5CT", state: "device"}
	for _, tc := range []struct {
		d      adbDevice
		target string
		want   bool
	}{
		{colon, "ABC:12", true},
		{colon, "ABC", true},
		{colon, "tcp:ABC", true},
		{colon, "udp:ABC:12", true},
		{colon, "ABC:13", false},
		{plain, "R5CT", true},
		{plain, "tcp:R5CT", true},
		{plain, "R5CT:5555", false},
	} {
		if got := tc.d.matchesTarget(tc.target); got != tc.want {
			t.Errorf("%s answers to %q: %v, want %v", tc.d.serial, tc.target, got, tc.want)
		}
	}
}

// TestParseNetAddressAsLibbaseDoes: network addresses read as
// android::base::ParseNetAddress reads them (AOSP system/libbase
// parsenetaddress.cpp, lines 27-79 at 6d19b5c6), the port with sscanf's %d.
func TestParseNetAddressAsLibbaseDoes(t *testing.T) {
	for _, tc := range []struct {
		address  string
		port     int // when the address gives none
		host     string
		wantPort int
		ok       bool
	}{
		{"192.168.1.20:5555", -1, "192.168.1.20", 5555, true},
		{"192.168.1.20", 5555, "192.168.1.20", 5555, true},
		{"mirror.lan", -1, "mirror.lan", -1, true},
		{"[::1]:5037", -1, "::1", 5037, true},
		{"[a]:1]:2", -1, "a", 2, true}, // the host ends at the first "]:", the port starts after the last
		{"[::1]", 7, "", 0, false},
		{"[]:5037", -1, "", 0, false},
		{"::1", 7, "::1", 7, true},
		{"1:2:3:4:5:6:7:8", 7, "1:2:3:4:5:6:7:8", 7, true},
		{"1:2:3:4:5:6:7:8:9", 7, "", 0, false},
		{"::ffff:1.2.3.4", 7, "", 0, false},
		{":5555", -1, "", 0, false},
		{"", 5, "", 0, false},
		{"mirror.lan:1:2", -1, "", 0, false},
		{"host:1999", -1, "host", 1999, true},
		{"host:65535", -1, "host", 65535, true},
		{"host:65536", -1, "", 0, false},
		{"host:0", -1, "", 0, false},
		{"host:-1", -1, "", 0, false},
		{"host: \t5555", -1, "host", 5555, true},
		{"host:+5555", -1, "host", 5555, true},
		{"host:5555x", -1, "host", 5555, true},
		{"host:x5555", -1, "", 0, false},
		{"host:+", -1, "", 0, false},
		{"host:", -1, "", 0, false},
		{"host:99999999999999999999", -1, "", 0, false},
	} {
		host, port, ok := parseNetAddress(tc.address, tc.port)
		if host != tc.host || port != tc.wantPort || ok != tc.ok {
			t.Errorf("%q (port %d): %q, %d, %v; want %q, %d, %v", tc.address, tc.port, host, port, ok, tc.host, tc.wantPort, tc.ok)
		}
	}
}
