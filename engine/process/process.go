package process

var protectedPorts = map[int]bool{
	80:    true,
	443:   true,
	6152:  true, // Surge mac
	7890:  true, // Clash
	7891:  true, // Clash Verge / Mihomo
	7897:  true, // Clash Verge Mixed
	8099:  true, // 岛风 GO
	8123:  true, // ACGPower
	10808: true, // v2rayN HTTP
	10809: true, // v2rayN SOCKS/HTTP
}

// IsPortProtected returns true if the port is a known upstream proxy or standard web port.
// Processes listening on protected ports must never be killed.
func IsPortProtected(port int) bool {
	return protectedPorts[port]
}

// KillProcessOnPort safely terminates previous GBF Accelerator / gbf_proxy instances
// listening on the given port.
func KillProcessOnPort(port int) (bool, error) {
	if IsPortProtected(port) {
		return false, nil
	}
	return killProcessOnPort(port)
}
