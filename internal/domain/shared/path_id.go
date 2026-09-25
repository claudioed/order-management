package shared

// PathId names the wes-work-planning process path an OrderLine's work is
// enqueued onto at release time.
type PathId string

// DefaultPathId is the process path used when a caller does not supply one,
// or when order.PathSelectionPolicy's current rule set resolves to no
// more specific path (see ADR-0013 — path selection is now a real domain
// policy, validated against process-path-management's live catalogue at
// intake time, not a hardcoded adapter-layer default).
const DefaultPathId PathId = "pick"

// NewPathId validates and constructs a PathId. Use NewPathIdOrDefault when
// an absent value should fall back to DefaultPathId rather than fail.
func NewPathId(value string) (PathId, error) {
	if value == "" {
		return "", ErrEmptyPathID
	}
	return PathId(value), nil
}

// NewPathIdOrDefault returns DefaultPathId for an empty value, and
// otherwise behaves exactly like NewPathId.
func NewPathIdOrDefault(value string) (PathId, error) {
	if value == "" {
		return DefaultPathId, nil
	}
	return NewPathId(value)
}

func (p PathId) String() string { return string(p) }
