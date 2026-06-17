package api

import "time"

// nowUTC returns the current UTC time. Wrapped so tests can stub it later
// if we add them; right now it's just a single point of clock access.
func nowUTC() time.Time {
	return time.Now().UTC()
}
