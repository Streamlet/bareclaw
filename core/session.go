package core

import (
	"math/rand"
	"time"
)

// GenerateSessionID returns a unique session identifier.
func GenerateSessionID() string {
	ts := time.Now().Format("20060102-150405")
	rnd := make([]byte, 6)
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	for i := range rnd {
		rnd[i] = charset[rand.Intn(len(charset))]
	}
	return ts + "-" + string(rnd)
}
