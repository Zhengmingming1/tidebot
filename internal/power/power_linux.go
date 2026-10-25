package power

func read() State {
	return ReadSysfs("/sys/class/power_supply", "/sys/firmware/acpi/platform_profile")
}
