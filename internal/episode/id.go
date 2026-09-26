package episode

import (
	"crypto/rand"
	"encoding/hex"
)

// NewID returns an episode identifier of the form ep_<16 hex chars>.
func NewID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return "ep_" + hex.EncodeToString(b[:])
}
