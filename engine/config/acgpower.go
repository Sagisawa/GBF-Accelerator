package config

// AutoDetectACGPowerCache detects ACGPower installation and cache directories across
// active running processes, relative locations, and standard system drives.
func AutoDetectACGPowerCache() string {
	return autoDetectACGPowerCache()
}
