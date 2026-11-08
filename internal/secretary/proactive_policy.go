package secretary

// Delivery policy for proactive messages. These are fixed in code on purpose:
// the operator can schedule work, but cannot widen when or how often Donna writes.
const (
	proactiveTimezone        = "Europe/Madrid"
	proactiveWindowStartHour = 8  // first allowed local hour (inclusive)
	proactiveWindowEndHour   = 21 // local hour at which sending stops (exclusive)
	proactiveDailyLimit      = 3  // messages per operator per local day
)
