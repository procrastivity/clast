package llm

import "time"

// SetTimeouts overrides the first-byte, idle and total limits so tests
// need not wait out the 30s and 5m production values.
func SetTimeouts(c *Client, firstByte, idle, total time.Duration) {
	c.firstByte, c.idle, c.max = firstByte, idle, total
	c.buildHTTP()
}
