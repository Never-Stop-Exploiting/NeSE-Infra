package runner

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// GenerateInstanceID returns a short random hex suffix for runner instances.
func GenerateInstanceID() string {
	buf := make([]byte, 2)
	if _, err := rand.Read(buf); err == nil {
		return hex.EncodeToString(buf)
	}
	return fmt.Sprintf("%04x", time.Now().UnixNano()&0xffff)
}
