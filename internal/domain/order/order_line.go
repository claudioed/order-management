package order

import "github.com/claudioed/order-management/internal/domain/shared"

// OrderLine is an entity inside the Order aggregate: one SKU, one
// quantity, one process path, one lifecycle state. It is only ever
// reachable through its Order — nothing outside the aggregate mutates a
// line directly.
type OrderLine struct {
	lineNo        int
	sku           shared.SKU
	quantity      int
	pathID        shared.PathId
	giftWrap      bool
	status        LineStatus
	reservationID *string
}

// NewOrderLine validates and constructs a line in LinePending state.
// lineNo is the line's 1-based position within its order and is how every
// Order method addresses a line.
func NewOrderLine(lineNo int, sku shared.SKU, quantity int, pathID shared.PathId, giftWrap bool) (*OrderLine, error) {
	if sku == "" {
		return nil, shared.ErrEmptySKU
	}
	if quantity <= 0 {
		return nil, shared.ErrNonPositiveQuantity
	}
	if pathID == "" {
		pathID = shared.DefaultPathId
	}
	return &OrderLine{
		lineNo:   lineNo,
		sku:      sku,
		quantity: quantity,
		pathID:   pathID,
		giftWrap: giftWrap,
		status:   LinePending,
	}, nil
}

// RehydrateOrderLine rebuilds a line from persisted state without
// re-running construction invariants. Only outbound repository adapters
// call this — it is the read side of Save, not a second constructor.
func RehydrateOrderLine(lineNo int, sku shared.SKU, quantity int, pathID shared.PathId, giftWrap bool, status LineStatus, reservationID *string) *OrderLine {
	return &OrderLine{
		lineNo:        lineNo,
		sku:           sku,
		quantity:      quantity,
		pathID:        pathID,
		giftWrap:      giftWrap,
		status:        status,
		reservationID: reservationID,
	}
}

func (l *OrderLine) LineNo() int           { return l.lineNo }
func (l *OrderLine) SKU() shared.SKU       { return l.sku }
func (l *OrderLine) Quantity() int         { return l.quantity }
func (l *OrderLine) PathID() shared.PathId { return l.pathID }
func (l *OrderLine) GiftWrap() bool        { return l.giftWrap }
func (l *OrderLine) Status() LineStatus    { return l.status }

// ReservationID is inventory-storage's reservation id, set once the line is
// allocated and needed to revoke on cancellation. Nil until allocated.
// This context stores the reference and nothing else: it does not model a
// local Reservation aggregate, because inventory-storage is the sole owner
// of reservation state.
func (l *OrderLine) ReservationID() *string {
	if l.reservationID == nil {
		return nil
	}
	id := *l.reservationID
	return &id
}
