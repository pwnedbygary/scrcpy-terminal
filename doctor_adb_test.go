package main

// adbAndDevices is the adb check and, when adb runs, the device check, as
// runDoctor prints them, with one paired device or none.
func adbAndDevices(paired bool) []doctorLine {
	n := 0
	if paired {
		n = 1
	}
	adb := doctorADB(n)
	if adb.status != doctorOK {
		return []doctorLine{adb}
	}
	return append([]doctorLine{adb}, doctorDevices(n)...)
}
