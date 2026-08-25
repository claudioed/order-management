package inventorystorage_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/claudioed/order-management/internal/adapters/outbound/inventorystorage"
	"github.com/claudioed/order-management/internal/application/ports"
)

// captured records what the client actually put on the wire, so the
// request shape can be asserted against inventory-storage's published
// contract rather than assumed.
type captured struct {
	method string
	path   string
	body   map[string]any
}

func newServer(t *testing.T, status int, responseBody string, got *captured) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.path = r.URL.Path
		if raw, err := io.ReadAll(r.Body); err == nil && len(raw) > 0 {
			_ = json.Unmarshal(raw, &got.body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if responseBody != "" {
			_, _ = io.WriteString(w, responseBody)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestReserveSendsInventoryStoragesPublishedRequestShape(t *testing.T) {
	var got captured
	srv := newServer(t, http.StatusCreated, `{"id":"res-42","sku":"SKU-1","quantity":3,"demandRef":"ord-7","status":"ACTIVE","allocations":[{"stockUnitId":"su-1","quantity":3}],"expiresAt":"2026-08-25T10:00:00Z"}`, &got)

	client := inventorystorage.NewClient(srv.URL+"/", nil)
	result, err := client.Reserve(context.Background(), ports.ReservationRequest{
		SKU: "SKU-1", Quantity: 3, DemandRef: "ord-7",
	})
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	if result.ReservationID != "res-42" {
		t.Fatalf("ReservationID = %q, want res-42", result.ReservationID)
	}
	if got.method != http.MethodPost || got.path != "/reservations" {
		t.Fatalf("called %s %s, want POST /reservations", got.method, got.path)
	}
	if got.body["sku"] != "SKU-1" || got.body["quantity"] != float64(3) || got.body["demandRef"] != "ord-7" {
		t.Fatalf("request body = %v, want {sku, quantity, demandRef}", got.body)
	}
}

// The single most important behaviour in this adapter: only a 409 is the
// "no usable stock" business fact.
func TestReserveMapsStatusesCorrectly(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		responseBody string
		wantErr      error
	}{
		{name: "201 is a reservation", status: http.StatusCreated, responseBody: `{"id":"res-1"}`},
		{name: "200 is also accepted", status: http.StatusOK, responseBody: `{"id":"res-1"}`},
		{
			name: "409 is the insufficient-stock business fact", status: http.StatusConflict,
			responseBody: `{"type":"https://errors.inventory-storage.warehouse-systems.dev/insufficient-usable","title":"Requested quantity exceeds usable inventory","status":409,"detail":"insufficient usable"}`,
			wantErr:      ports.ErrInsufficientStock,
		},
		{name: "400 is NOT a backorder", status: http.StatusBadRequest, wantErr: inventorystorage.ErrUnexpectedStatus},
		{name: "404 is NOT a backorder", status: http.StatusNotFound, wantErr: inventorystorage.ErrUnexpectedStatus},
		{name: "422 is NOT a backorder", status: http.StatusUnprocessableEntity, wantErr: inventorystorage.ErrUnexpectedStatus},
		{name: "500 is NOT a backorder", status: http.StatusInternalServerError, wantErr: inventorystorage.ErrUnexpectedStatus},
		{name: "503 is NOT a backorder", status: http.StatusServiceUnavailable, wantErr: inventorystorage.ErrUnexpectedStatus},
		{
			name: "a 2xx with no id is not a usable reservation", status: http.StatusCreated,
			responseBody: `{}`, wantErr: inventorystorage.ErrUnexpectedStatus,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got captured
			srv := newServer(t, tt.status, tt.responseBody, &got)
			client := inventorystorage.NewClient(srv.URL, nil)

			_, err := client.Reserve(context.Background(), ports.ReservationRequest{SKU: "SKU-1", Quantity: 1, DemandRef: "ord-1"})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			// Whatever the outcome, it must never be BOTH.
			if tt.wantErr != ports.ErrInsufficientStock && errors.Is(err, ports.ErrInsufficientStock) {
				t.Fatalf("status %d was wrongly treated as a backorder", tt.status)
			}
		})
	}
}

func TestReserveOnAnUndecodableBody(t *testing.T) {
	var got captured
	srv := newServer(t, http.StatusCreated, `not json`, &got)
	client := inventorystorage.NewClient(srv.URL, nil)

	if _, err := client.Reserve(context.Background(), ports.ReservationRequest{SKU: "SKU-1", Quantity: 1}); err == nil {
		t.Fatal("expected an error decoding a non-JSON 201 body")
	}
}

func TestRevokeReservation(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		wantErr error
	}{
		{name: "204 is success", status: http.StatusNoContent},
		{name: "200 is success", status: http.StatusOK},
		{name: "404 is idempotent success — the reservation is already gone", status: http.StatusNotFound},
		{name: "409 is a failure", status: http.StatusConflict, wantErr: inventorystorage.ErrUnexpectedStatus},
		{name: "500 is a failure", status: http.StatusInternalServerError, wantErr: inventorystorage.ErrUnexpectedStatus},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got captured
			srv := newServer(t, tt.status, "", &got)
			client := inventorystorage.NewClient(srv.URL, nil)

			err := client.RevokeReservation(context.Background(), "res-42")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got.method != http.MethodDelete || got.path != "/reservations/res-42" {
				t.Fatalf("called %s %s, want DELETE /reservations/res-42", got.method, got.path)
			}
		})
	}
}

func TestReservationIDIsPathEscaped(t *testing.T) {
	var got captured
	srv := newServer(t, http.StatusNoContent, "", &got)
	client := inventorystorage.NewClient(srv.URL, nil)

	if err := client.RevokeReservation(context.Background(), "res/with/slashes"); err != nil {
		t.Fatalf("RevokeReservation: %v", err)
	}
	if got.path != "/reservations/res/with/slashes" {
		t.Fatalf("path = %q — the id must be escaped into a single segment", got.path)
	}
}

// errDoer models a transport failure (connection refused, timeout) — the
// case that must never be mistaken for a backorder.
type errDoer struct{ err error }

func (d errDoer) Do(*http.Request) (*http.Response, error) { return nil, d.err }

func TestTransportFailureIsNeverABackorder(t *testing.T) {
	boom := errors.New("connection refused")
	client := inventorystorage.NewClient("http://example.invalid", errDoer{err: boom})

	_, err := client.Reserve(context.Background(), ports.ReservationRequest{SKU: "SKU-1", Quantity: 1})
	if !errors.Is(err, boom) {
		t.Fatalf("Reserve err = %v, want %v", err, boom)
	}
	if errors.Is(err, ports.ErrInsufficientStock) {
		t.Fatal("a transport failure must never surface as insufficient stock")
	}

	if err := client.RevokeReservation(context.Background(), "res-1"); !errors.Is(err, boom) {
		t.Fatalf("RevokeReservation err = %v, want %v", err, boom)
	}
}

func TestPermissiveClientRefusesRatherThanFabricating(t *testing.T) {
	client := inventorystorage.NewPermissiveClient()

	result, err := client.Reserve(context.Background(), ports.ReservationRequest{SKU: "SKU-1", Quantity: 1})
	if !errors.Is(err, ports.ErrDownstreamNotConfigured) {
		t.Fatalf("Reserve err = %v, want %v", err, ports.ErrDownstreamNotConfigured)
	}
	if result.ReservationID != "" {
		t.Fatalf("the permissive client must never fabricate a reservation id, got %q", result.ReservationID)
	}

	if err := client.RevokeReservation(context.Background(), "res-1"); !errors.Is(err, ports.ErrDownstreamNotConfigured) {
		t.Fatalf("RevokeReservation err = %v, want %v", err, ports.ErrDownstreamNotConfigured)
	}
}

func TestNewClientDefaultsItsDoer(t *testing.T) {
	if inventorystorage.NewClient("http://example.invalid", nil) == nil {
		t.Fatal("NewClient returned nil")
	}
}
