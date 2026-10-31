package main

// Interface 0 is DIAG/QMI, not an AT port; probing it can block IOUSBLib after reboot.
func isUSBATInterface(number, class, subclass, protocol int) bool {
	return (number == 2 || number == 3 || number == 4 || number == 5) && class == 0xff && subclass == 0 && protocol == 0
}
