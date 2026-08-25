package memory

import "time"

// SystemClock implements ports.Clock using wall-clock time.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

// FixedClock implements ports.Clock with a fixed, settable time, so
// promise-date and event-timestamp assertions are exact rather than
// tolerance-based.
type FixedClock struct {
	t time.Time
}

// NewFixedClock constructs a FixedClock reading t.
func NewFixedClock(t time.Time) *FixedClock { return &FixedClock{t: t} }

func (c *FixedClock) Now() time.Time { return c.t }

// Advance moves the clock forward by d.
func (c *FixedClock) Advance(d time.Duration) { c.t = c.t.Add(d) }
