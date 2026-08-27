package shared

// OrderId identifies an Order aggregate. It is this bounded context's
// contribution to the fleet: before Order Management existed, "an order"
// was an unvalidated string independently reinvented as OrderRef /
// DemandRef / Reference by three different services. Here it is a
// validated value object, and it is the value sent downstream as
// inventory-storage's `demandRef` and wes-work-planning's `reference`.
type OrderId string

// NewOrderId validates and constructs an OrderId.
func NewOrderId(value string) (OrderId, error) {
	if value == "" {
		return "", ErrEmptyOrderID
	}
	return OrderId(value), nil
}

func (o OrderId) String() string { return string(o) }
