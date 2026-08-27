package inventorystorage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/claudioed/order-management/internal/application/ports"
)

// DefaultTimeout bounds a single call to inventory-storage, so a slow or
// hanging Supplier does not stall AllocateOrder indefinitely.
const DefaultTimeout = 5 * time.Second

// ErrUnexpectedStatus wraps an inventory-storage response status this
// client has no specific handling for. It is deliberately NOT
// ports.ErrInsufficientStock: only a 409 means "no usable stock", and
// anything else must fail AllocateOrder outright rather than be recorded
// as a backorder.
var ErrUnexpectedStatus = errors.New("inventory-storage: unexpected response status")

// HTTPDoer is the subset of *http.Client this adapter depends on, so unit
// tests can substitute a fake transport without a real server.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client is a plain net/http implementation of
// ports.InventoryReservationClient, calling inventory-storage's published
// contract:
//
//	POST   /reservations       -> 201 with the reservation, or 409 (RFC 7807)
//	                              when there is not enough usable stock
//	DELETE /reservations/{id}  -> 204
//
// It imports nothing from the inventory-storage module: the request and
// response shapes below are local mirrors of that service's published
// wire contract (see ADR 0002).
type Client struct {
	baseURL string
	doer    HTTPDoer
}

// NewClient builds a Client against baseURL (from INVENTORY_STORAGE_BASE_URL).
// A nil doer defaults to an *http.Client with DefaultTimeout.
func NewClient(baseURL string, doer HTTPDoer) *Client {
	if doer == nil {
		doer = &http.Client{Timeout: DefaultTimeout}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), doer: doer}
}

// reserveRequest mirrors inventory-storage's ReserveStockRequest.
type reserveRequest struct {
	SKU       string `json:"sku"`
	Quantity  int    `json:"quantity"`
	DemandRef string `json:"demandRef"`
}

// reservationResponse mirrors inventory-storage's Reservation response.
// Only `id` is consumed: this context stores the reference and nothing
// else, because inventory-storage owns reservation state.
type reservationResponse struct {
	ID string `json:"id"`
}

// Reserve calls POST /reservations for one order line.
//
//   - 201 -> the reservation id.
//   - 409 -> ports.ErrInsufficientStock, the business fact that maps to a
//     Backordered line.
//   - anything else, including any transport error -> a hard error.
func (c *Client) Reserve(ctx context.Context, req ports.ReservationRequest) (ports.ReservationResult, error) {
	body, err := json.Marshal(reserveRequest{
		SKU:       req.SKU.String(),
		Quantity:  req.Quantity,
		DemandRef: req.DemandRef.String(),
	})
	if err != nil {
		return ports.ReservationResult{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/reservations", bytes.NewReader(body))
	if err != nil {
		return ports.ReservationResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.doer.Do(httpReq)
	if err != nil {
		return ports.ReservationResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK:
		var decoded reservationResponse
		if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
			return ports.ReservationResult{}, err
		}
		if decoded.ID == "" {
			return ports.ReservationResult{}, fmt.Errorf("%w: 2xx response carried no reservation id", ErrUnexpectedStatus)
		}
		return ports.ReservationResult{ReservationID: decoded.ID}, nil
	case http.StatusConflict:
		return ports.ReservationResult{}, ports.ErrInsufficientStock
	default:
		return ports.ReservationResult{}, fmt.Errorf("%w: %d", ErrUnexpectedStatus, resp.StatusCode)
	}
}

// RevokeReservation calls DELETE /reservations/{id}.
//
// A 404 is treated as success: the reservation this context wanted gone is
// gone. That is idempotence, not fail-open — the desired end state holds
// either way, so a cancellation retry after a partial failure converges
// instead of deadlocking.
func (c *Client) RevokeReservation(ctx context.Context, reservationID string) error {
	endpoint := fmt.Sprintf("%s/reservations/%s", c.baseURL, url.PathEscape(reservationID))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.doer.Do(httpReq)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusOK, http.StatusNotFound:
		return nil
	default:
		return fmt.Errorf("%w: %d", ErrUnexpectedStatus, resp.StatusCode)
	}
}
