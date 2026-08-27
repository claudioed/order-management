package shared

// SKU identifies the stock keeping unit an OrderLine is for. It is the
// same identifier inventory-storage reserves against and wes-work-planning
// records on a work unit; this context never invents SKUs, it only carries
// them.
type SKU string

// NewSKU validates and constructs a SKU.
func NewSKU(value string) (SKU, error) {
	if value == "" {
		return "", ErrEmptySKU
	}
	return SKU(value), nil
}

func (s SKU) String() string { return string(s) }
