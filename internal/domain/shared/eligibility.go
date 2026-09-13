package shared

// Eligibility mirrors process-path-management's own shared.Eligibility
// value object (ADR 0010) in this context's own types — order-management
// never imports process-path-management's Go packages (see
// .claude/rules/bounded-context-boundary.md); this is a minimal,
// independently-owned copy of the same shape, decoded from the wire.
//
// The zero value is fully valid and permissive: no unit cap, no required
// or excluded product attributes, sortable. ADR 0014 step A only carries
// this data through the catalogue so it is available; nothing in this
// service reads it for a routing decision yet — that is step B
// (eligibility-driven PathSelectionPolicy).
type Eligibility struct {
	maxUnitsPerLine           *int
	requiredProductAttributes []string
	excludedProductAttributes []string
	nonSortable               bool
}

// NewEligibility constructs an Eligibility from its parts. maxUnitsPerLine
// nil means unbounded (1 would mean a "singles" path).
func NewEligibility(maxUnitsPerLine *int, requiredProductAttributes, excludedProductAttributes []string, nonSortable bool) Eligibility {
	var maxCopy *int
	if maxUnitsPerLine != nil {
		v := *maxUnitsPerLine
		maxCopy = &v
	}
	return Eligibility{
		maxUnitsPerLine:           maxCopy,
		requiredProductAttributes: append([]string(nil), requiredProductAttributes...),
		excludedProductAttributes: append([]string(nil), excludedProductAttributes...),
		nonSortable:               nonSortable,
	}
}

// MaxUnitsPerLine returns the maximum quantity a single line may carry on
// this path, or nil for unbounded.
func (e Eligibility) MaxUnitsPerLine() *int {
	if e.maxUnitsPerLine == nil {
		return nil
	}
	v := *e.maxUnitsPerLine
	return &v
}

// RequiredProductAttributes returns the attributes a product must have to
// be eligible for this path (e.g. "hazmat").
func (e Eligibility) RequiredProductAttributes() []string {
	return append([]string(nil), e.requiredProductAttributes...)
}

// ExcludedProductAttributes returns the attributes that disqualify a
// product from this path.
func (e Eligibility) ExcludedProductAttributes() []string {
	return append([]string(nil), e.excludedProductAttributes...)
}

// NonSortable reports whether this path is restricted to non-sortable
// freight.
func (e Eligibility) NonSortable() bool { return e.nonSortable }

// Equal reports whether e and other describe the same eligibility rule.
func (e Eligibility) Equal(other Eligibility) bool {
	if e.nonSortable != other.nonSortable {
		return false
	}
	if !intPtrEqual(e.maxUnitsPerLine, other.maxUnitsPerLine) {
		return false
	}
	return stringSliceEqual(e.requiredProductAttributes, other.requiredProductAttributes) &&
		stringSliceEqual(e.excludedProductAttributes, other.excludedProductAttributes)
}

func intPtrEqual(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
