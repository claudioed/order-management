// Package http is the inbound chi adapter: DTOs, handlers, routing, and
// domain-error-to-HTTP-status mapping. Domain structs never cross this
// boundary — every response below is a DTO owned by this package.
package http

type receiveOrderLineRequest struct {
	SKU      string `json:"sku"`
	Quantity int    `json:"quantity"`
	GiftWrap bool   `json:"giftWrap,omitempty"`
}

type receiveOrderRequest struct {
	Lines []receiveOrderLineRequest `json:"lines"`
	// AllowPartialShipment defaults to false — ship-complete (BR3).
	AllowPartialShipment bool `json:"allowPartialShipment,omitempty"`
}

type orderLineResponse struct {
	LineNo   int    `json:"lineNo"`
	SKU      string `json:"sku"`
	Quantity int    `json:"quantity"`
	// PathID reflects an internally-assigned value (see
	// shared.DefaultPathId / shared.NewPathIdOrDefault) — a caller placing
	// an order has no business supplying wes-work-planning's process-path
	// vocabulary at intake, so this is read-only on the wire: it can be
	// seen in every response but never set on the request.
	PathID   string `json:"pathId"`
	GiftWrap bool   `json:"giftWrap"`
	Status   string `json:"status"`
	// ReservationID is inventory-storage's id, present only once the line
	// is allocated. Omitted rather than sent as "" so "not allocated" is
	// unambiguous on the wire.
	ReservationID *string `json:"reservationId,omitempty"`
}

type orderResponse struct {
	ID                   string              `json:"id"`
	Status               string              `json:"status"`
	AllowPartialShipment bool                `json:"allowPartialShipment"`
	PromiseDate          *string             `json:"promiseDate,omitempty"`
	Lines                []orderLineResponse `json:"lines"`
}

// problemDetails is the RFC 7807 (Problem Details for HTTP APIs) response
// body used for every error response in this service — the same shape the
// other five services in this fleet emit.
type problemDetails struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance,omitempty"`
}
