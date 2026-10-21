package main

// adbAndDevices is the adb check and, when adb runs, the device check, as
// runDoctor prints them.
func adbAndDevices(paired bool) []doctorLine {
	adb := doctorADB(paired)
	if adb.status != doctorOK {
		return []doctorLine{adb}
	}
	return append([]doctorLine{adb}, doctorDevices(paired)...)
}
