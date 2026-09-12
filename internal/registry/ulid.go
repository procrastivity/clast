package registry

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"sync"
	"time"
)

// IDLen is the character length of every identity this package mints — a
// Project id or a Clone id (M15). Nobody types or reads one, so length is
// free and the collision domain is global.
const IDLen = 26

// crockford is Crockford's base32 alphabet, as ULID specifies it.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Source generates monotonic ULIDs: 48 bits of millisecond timestamp plus
// 80 bits of randomness, rendered as 26 Crockford-base32 characters.
// Successive ids from one Source sort strictly ascending as strings, even
// within the same millisecond (the random half is incremented rather than
// redrawn).
//
// Ported from wip's internal/store/ulid.go idSource, which uses this
// property to let an event's own identity double as that store's
// total-order key with no separate sequence column. clast has no event
// log here — a Source only mints Project and Clone ids (M15) — but the
// monotonicity is free and worth keeping regardless.
type Source struct {
	mu       sync.Mutex
	lastMS   uint64
	lastRand [10]byte
	now      func() time.Time
}

// NewSource builds a Source clocked off time.Now.
func NewSource() *Source {
	return newSourceWithClock(time.Now)
}

// newSourceWithClock builds a Source with an injectable clock, so a test
// can force the same-millisecond increment path deterministically.
func newSourceWithClock(now func() time.Time) *Source {
	return &Source{now: now}
}

// Next returns the next monotonic identity.
func (s *Source) Next() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	ms := uint64(s.now().UTC().UnixMilli())
	switch {
	case ms > s.lastMS:
		s.lastMS = ms
		if _, err := rand.Read(s.lastRand[:]); err != nil {
			// A Source cannot mint identities without entropy, and an
			// identity that is not unique corrupts the identity/key
			// separation M15 rests on. There is no degraded mode worth
			// having here.
			panic(fmt.Sprintf("registry: entropy unavailable: %v", err))
		}
	default:
		// Same millisecond (or a clock that went backwards): increment the
		// random half so ordering stays strict.
		ms = s.lastMS
		for i := len(s.lastRand) - 1; i >= 0; i-- {
			s.lastRand[i]++
			if s.lastRand[i] != 0 {
				break
			}
		}
	}

	var raw [16]byte
	var tsBuf [8]byte
	binary.BigEndian.PutUint64(tsBuf[:], ms)
	copy(raw[0:6], tsBuf[2:8])
	copy(raw[6:16], s.lastRand[:])

	return encodeCrockford(raw)
}

// encodeCrockford renders 16 bytes as the 26-character ULID text form.
func encodeCrockford(raw [16]byte) string {
	out := make([]byte, IDLen)
	// The first character carries only the top 3 bits of the 128-bit
	// value, which is why the first ten characters hold exactly the
	// 48-bit timestamp.
	out[0] = crockford[(raw[0]&0xE0)>>5]
	var bitPos uint = 3
	for i := 1; i < IDLen; i++ {
		var v byte
		for j := 0; j < 5; j++ {
			byteIdx := bitPos >> 3
			bitIdx := 7 - (bitPos & 7)
			bit := (raw[byteIdx] >> bitIdx) & 1
			v = v<<1 | bit
			bitPos++
		}
		out[i] = crockford[v]
	}
	return string(out)
}

// IsIdentityShaped reports whether s has the fixed 26-character
// Crockford-Base32 shape every identity this package mints uses — the
// shape test label validation and locator dispatch read against (M16): a
// locator (or a candidate label) of this shape is a ULID, anything else a
// label.
//
// Ported from wip's internal/store/ulid.go IsIdentityShaped.
func IsIdentityShaped(s string) bool {
	if len(s) != IDLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		if crockfordValue(s[i]) < 0 {
			return false
		}
	}
	return true
}

func crockfordValue(c byte) int {
	for i := 0; i < len(crockford); i++ {
		if crockford[i] == c {
			return i
		}
	}
	return -1
}
