package shared

// PathId names the wes-work-planning process path an OrderLine's work is
// enqueued onto at release time.
type PathId string

// DefaultPathId is the process path used when a caller does not supply one.
// A documented v1 simplification: Order Management does not (yet) model
// path selection as a domain policy, in the same spirit as
// fulfillment-execution's documented path_id-prefix convention.
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
