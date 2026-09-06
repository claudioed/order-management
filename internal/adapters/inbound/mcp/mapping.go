package mcp

import (
	"github.com/claudioed/order-management/internal/domain/order"
)

// Compact projections of the read model -- the MCP-facing shape, kept
// separate from the HTTP adapter's own DTOs (dto.go in
// internal/adapters/inbound/http) even though the JSON field names happen
// to match today: this package must never import the http adapter, and a
// future divergence between the two surfaces should not require touching
// this file's own shape.

// orderLineDTO is one line of an orderDTO.
type orderLineDTO struct {
	LineNo   int    `json:"lineNo"`
	SKU      string `json:"sku"`
	Quantity int    `json:"quantity"`
	PathID   string `json:"pathId"`
	GiftWrap bool   `json:"giftWrap"`
	Status   string `json:"status"`
	// ReservationID is inventory-storage's id, present only once the line
	// is allocated. Omitted rather than sent as "" so "not allocated" is
	// unambiguous to a calling model.
	ReservationID *string `json:"reservationId,omitempty"`
}

// orderDTO is the get_order tool's output.
type orderDTO struct {
	ID                   string         `json:"id"`
	Status               string         `json:"status"`
	AllowPartialShipment bool           `json:"allowPartialShipment"`
	PromiseDate          *string        `json:"promiseDate,omitempty"`
	Lines                []orderLineDTO `json:"lines"`
}

func toOrderDTO(o *order.Order) orderDTO {
	lines := o.Lines()
	dtoLines := make([]orderLineDTO, 0, len(lines))
	for _, l := range lines {
		dtoLines = append(dtoLines, orderLineDTO{
			LineNo:        l.LineNo(),
			SKU:           string(l.SKU()),
			Quantity:      l.Quantity(),
			PathID:        string(l.PathID()),
			GiftWrap:      l.GiftWrap(),
			Status:        string(l.Status()),
			ReservationID: l.ReservationID(),
		})
	}

	dto := orderDTO{
		ID:                   string(o.ID()),
		Status:               string(o.Status()),
		AllowPartialShipment: o.AllowPartialShipment(),
		Lines:                dtoLines,
	}
	if pd := o.PromiseDate(); pd != nil {
		formatted := pd.UTC().Format("2006-01-02T15:04:05Z07:00")
		dto.PromiseDate = &formatted
	}
	return dto
}
