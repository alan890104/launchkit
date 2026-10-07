package provider

import "time"

// orDefault returns d if d > 0, otherwise returns def.
func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}
