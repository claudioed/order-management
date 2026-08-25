package order

import (
	"errors"
	"time"

	"github.com/claudioed/order-management/internal/domain/shared"
)

var (
	// ErrNoLines is returned when an Order is constructed with no lines.
	// An order that asks for nothing is not a business fact.
	ErrNoLines = errors.New("order must have at least one line")

	// ErrLineNotFound is returned when a line number does not address a
	// line on this order.
	ErrLineNotFound = errors.New("order line not found")

	// ErrLineAlreadyAllocated enforces "cannot allocate the same line
	// twice": Allocate only accepts a Pending line.
	ErrLineAlreadyAllocated = errors.New("order line is already allocated")

	// ErrLineNotPending is returned when Allocate is called on a line that
	// is neither Pending nor already Allocated — most importantly a
	// Backordered line, which may only return to Allocated via
	// RetryAllocate.
	ErrLineNotPending = errors.New("order line is not pending allocation")

	// ErrLineNotBackordered is returned when RetryAllocate is called on a
	// line that is not Backordered. Together with ErrLineNotPending this
	// is the whole of the "Backordered -> Allocated only via
	// RetryAllocation" invariant.
	ErrLineNotBackordered = errors.New("order line is not backordered")

	// ErrLineNotAllocated enforces "cannot release a line that isn't
	// Allocated".
	ErrLineNotAllocated = errors.New("order line is not allocated")

	// ErrOrderAlreadyReleased enforces BR6: cancellation is legal only
	// while no line has reached Released.
	ErrOrderAlreadyReleased = errors.New("order already has released lines and can no longer be cancelled")

	// ErrShipCompleteBlocked enforces BR3 at release time: an order with
	// AllowPartialShipment=false may not release any line until every
	// line is allocated.
	ErrShipCompleteBlocked = errors.New("ship-complete order cannot be released while any line is unallocated")
)

// Order is the aggregate root: the unit of consistency for intake,
// allocation, release and cancellation. Every line mutation goes through
// an Order method, so the invariants below cannot be bypassed.
type Order struct {
	id                   shared.OrderId
	lines                []*OrderLine
	allowPartialShipment bool
	promiseDate          *time.Time
}

// New constructs an Order in Received status. lines must be non-empty;
// each line is numbered by its 1-based position.
func New(id shared.OrderId, lines []*OrderLine, allowPartialShipment bool) (*Order, error) {
	if id == "" {
		return nil, shared.ErrEmptyOrderID
	}
	if len(lines) == 0 {
		return nil, ErrNoLines
	}
	numbered := make([]*OrderLine, 0, len(lines))
	for i, l := range lines {
		l.lineNo = i + 1
		numbered = append(numbered, l)
	}
	return &Order{id: id, lines: numbered, allowPartialShipment: allowPartialShipment}, nil
}

// Rehydrate rebuilds an Order from persisted state without re-running
// construction invariants. Only outbound repository adapters call this.
func Rehydrate(id shared.OrderId, lines []*OrderLine, allowPartialShipment bool, promiseDate *time.Time) *Order {
	return &Order{id: id, lines: lines, allowPartialShipment: allowPartialShipment, promiseDate: promiseDate}
}

func (o *Order) ID() shared.OrderId         { return o.id }
func (o *Order) AllowPartialShipment() bool { return o.allowPartialShipment }

// Lines returns the order's lines. The slice is a copy, but the
// *OrderLine values are the aggregate's own entities: they are read-only
// from outside, since every mutating operation lives on Order.
func (o *Order) Lines() []*OrderLine {
	out := make([]*OrderLine, len(o.lines))
	copy(out, o.lines)
	return out
}

// PromiseDate is the date this order is promised for, computed at
// allocation time by the LeadTimePolicy. Nil until at least one line is
// allocated.
func (o *Order) PromiseDate() *time.Time {
	if o.promiseDate == nil {
		return nil
	}
	d := *o.promiseDate
	return &d
}

// SetPromiseDate records the promise date computed by the domain's
// LeadTimePolicy at allocation time.
func (o *Order) SetPromiseDate(d time.Time) { o.promiseDate = &d }

// Status derives the order-level status from the line statuses. There is
// deliberately no stored Status field: a derived status cannot drift out
// of sync with the lines it summarises.
//
// The derivation, in precedence order:
//
//   - every line Cancelled                  -> Cancelled
//   - any line Released, all lines Released -> Released
//   - any line Released, some not           -> PartiallyReleased
//   - any line Backordered, ship-complete   -> Backordered  (BR3: no line
//     proceeds to release until RetryAllocation clears the backorder)
//   - any line Backordered, partial allowed, at least one line Allocated
//     -> PartiallyAllocated
//   - any line Backordered, partial allowed, nothing allocated
//     -> Backordered
//   - every line Allocated                  -> Allocated
//   - some lines Allocated, rest Pending    -> PartiallyAllocated
//   - otherwise                             -> Received
func (o *Order) Status() Status {
	var allocated, backordered, released, cancelled int
	for _, l := range o.lines {
		switch l.status {
		case LineAllocated:
			allocated++
		case LineBackordered:
			backordered++
		case LineReleased:
			released++
		case LineCancelled:
			cancelled++
		}
	}
	total := len(o.lines)

	switch {
	case cancelled == total:
		return StatusCancelled
	case released == total:
		return StatusReleased
	case released > 0:
		return StatusPartiallyReleased
	case backordered > 0:
		if o.allowPartialShipment && allocated > 0 {
			return StatusPartiallyAllocated
		}
		return StatusBackordered
	case allocated == total:
		return StatusAllocated
	case allocated > 0:
		return StatusPartiallyAllocated
	default:
		return StatusReceived
	}
}

// Allocate records inventory-storage's reservation against a Pending line.
//
// Invariants enforced here:
//   - a line cannot be allocated twice (ErrLineAlreadyAllocated)
//   - a Backordered line cannot come back this way; only RetryAllocate
//     may do that (ErrLineNotPending)
func (o *Order) Allocate(lineNo int, reservationID string) error {
	line, err := o.line(lineNo)
	if err != nil {
		return err
	}
	switch line.status {
	case LineAllocated:
		return ErrLineAlreadyAllocated
	case LinePending:
		line.status = LineAllocated
		id := reservationID
		line.reservationID = &id
		return nil
	default:
		return ErrLineNotPending
	}
}

// RetryAllocate is the ONLY transition from Backordered back to Allocated.
func (o *Order) RetryAllocate(lineNo int, reservationID string) error {
	line, err := o.line(lineNo)
	if err != nil {
		return err
	}
	if line.status != LineBackordered {
		return ErrLineNotBackordered
	}
	line.status = LineAllocated
	id := reservationID
	line.reservationID = &id
	return nil
}

// MarkBackordered records the business fact that inventory-storage has no
// usable stock for this line (its 409). A line already Backordered stays
// Backordered — a failed retry is not an error.
func (o *Order) MarkBackordered(lineNo int) error {
	line, err := o.line(lineNo)
	if err != nil {
		return err
	}
	switch line.status {
	case LinePending, LineBackordered:
		line.status = LineBackordered
		return nil
	case LineAllocated:
		return ErrLineAlreadyAllocated
	default:
		return ErrLineNotPending
	}
}

// Release marks a line as released once wes-work-planning has accepted its
// work unit. Only an Allocated line may be released.
func (o *Order) Release(lineNo int) error {
	line, err := o.line(lineNo)
	if err != nil {
		return err
	}
	if line.status != LineAllocated {
		return ErrLineNotAllocated
	}
	line.status = LineReleased
	return nil
}

// EnsureReleasable enforces BR3 at the release boundary: an order that
// does NOT allow partial shipment may not release anything until every
// line is Allocated (or already Released). An order that allows partial
// shipment releases its allocated lines independently.
func (o *Order) EnsureReleasable() error {
	if o.allowPartialShipment {
		return nil
	}
	for _, l := range o.lines {
		if l.status != LineAllocated && l.status != LineReleased {
			return ErrShipCompleteBlocked
		}
	}
	return nil
}

// EnsureCancellable enforces BR6 without mutating anything, so a use case
// can check the boundary BEFORE revoking reservations upstream.
func (o *Order) EnsureCancellable() error {
	for _, l := range o.lines {
		if l.status == LineReleased {
			return ErrOrderAlreadyReleased
		}
	}
	return nil
}

// Cancel cancels every line. Legal only while no line has reached
// Released (BR6) — the check is repeated here so the invariant holds even
// if a caller skips EnsureCancellable.
//
// v1 deliberately does NOT claw back work already released to
// wes-work-planning; see ADR 0004's known-gap section.
func (o *Order) Cancel() error {
	if err := o.EnsureCancellable(); err != nil {
		return err
	}
	for _, l := range o.lines {
		l.status = LineCancelled
	}
	return nil
}

// AllocatedReservationIDs returns the reservation ids that CancelOrder
// must revoke on inventory-storage, in line order.
func (o *Order) AllocatedReservationIDs() []string {
	var ids []string
	for _, l := range o.lines {
		if l.status == LineAllocated && l.reservationID != nil {
			ids = append(ids, *l.reservationID)
		}
	}
	return ids
}

// LinesWithStatus returns the lines currently in the given status, in line
// order. Use cases iterate this rather than reaching into o.lines.
func (o *Order) LinesWithStatus(status LineStatus) []*OrderLine {
	var out []*OrderLine
	for _, l := range o.lines {
		if l.status == status {
			out = append(out, l)
		}
	}
	return out
}

func (o *Order) line(lineNo int) (*OrderLine, error) {
	for _, l := range o.lines {
		if l.lineNo == lineNo {
			return l, nil
		}
	}
	return nil, ErrLineNotFound
}
