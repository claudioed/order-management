package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	inboundhttp "github.com/claudioed/order-management/internal/adapters/inbound/http"
	"github.com/claudioed/order-management/internal/adapters/outbound/memory"
	"github.com/claudioed/order-management/internal/application/ports"
	"github.com/claudioed/order-management/internal/application/usecases"
	"github.com/claudioed/order-management/internal/domain/order"
	"github.com/claudioed/order-management/internal/domain/shared"
)

// stubInventory is a scripted ports.InventoryReservationClient for the
// httptest suite. No test in this package reaches a real network.
type stubInventory struct {
	mu         sync.Mutex
	reserveErr error
	revokeErr  error
	nextID     int
}

func (s *stubInventory) Reserve(context.Context, ports.ReservationRequest) (ports.ReservationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reserveErr != nil {
		return ports.ReservationResult{}, s.reserveErr
	}
	s.nextID++
	return ports.ReservationResult{ReservationID: "res-stub"}, nil
}

func (s *stubInventory) RevokeReservation(context.Context, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revokeErr
}

// stubWork is a scripted ports.WorkReleaseClient.
type stubWork struct {
	mu  sync.Mutex
	err error
}

func (s *stubWork) EnqueueWorkUnit(_ context.Context, req ports.WorkUnitRequest) (ports.WorkUnitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return ports.WorkUnitResult{}, s.err
	}
	return ports.WorkUnitResult{WorkUnitID: req.WorkUnitID}, nil
}

type nopPublisher struct{}

func (nopPublisher) Publish(context.Context, shared.DomainEvent) error { return nil }

type testEnv struct {
	handler   http.Handler
	orders    ports.OrderRepo
	inventory *stubInventory
	work      *stubWork
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	orders := memory.NewOrderRepo()
	inventory := &stubInventory{}
	work := &stubWork{}
	publisher := nopPublisher{}
	clock := memory.NewFixedClock(time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC))
	promise := order.NewLeadTimePolicy(24*time.Hour, nil)

	server := &inboundhttp.Server{
		ReceiveOrder:    &usecases.ReceiveOrder{Orders: orders, Events: publisher, Clock: clock},
		AllocateOrder:   &usecases.AllocateOrder{Orders: orders, Inventory: inventory, Events: publisher, Clock: clock, Promise: promise},
		RetryAllocation: &usecases.RetryAllocation{Orders: orders, Inventory: inventory, Events: publisher, Clock: clock, Promise: promise},
		ReleaseOrder:    &usecases.ReleaseOrder{Orders: orders, Work: work, Events: publisher, Clock: clock},
		CancelOrder:     &usecases.CancelOrder{Orders: orders, Inventory: inventory, Events: publisher, Clock: clock},
		GetOrder:        &usecases.GetOrder{Orders: orders},
	}

	// A discard logger keeps the middleware on the code path (so it is
	// exercised) without flooding test output.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	return &testEnv{
		handler:   inboundhttp.NewRouter(server, logger),
		orders:    orders,
		inventory: inventory,
		work:      work,
	}
}

func (e *testEnv) do(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec
}

type orderBody struct {
	ID                   string  `json:"id"`
	Status               string  `json:"status"`
	AllowPartialShipment bool    `json:"allowPartialShipment"`
	PromiseDate          *string `json:"promiseDate"`
	Lines                []struct {
		LineNo        int     `json:"lineNo"`
		SKU           string  `json:"sku"`
		Quantity      int     `json:"quantity"`
		PathID        string  `json:"pathId"`
		GiftWrap      bool    `json:"giftWrap"`
		Status        string  `json:"status"`
		ReservationID *string `json:"reservationId"`
	} `json:"lines"`
}

type problemBody struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
}

func decodeOrder(t *testing.T, rec *httptest.ResponseRecorder) orderBody {
	t.Helper()
	var body orderBody
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&body); err != nil {
		t.Fatalf("decode order response: %v (body: %s)", err, rec.Body.String())
	}
	return body
}

// assertProblem checks that the response is a well-formed RFC 7807
// problem+json with the expected status.
func assertProblem(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int) problemBody {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, wantStatus, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}
	var body problemBody
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&body); err != nil {
		t.Fatalf("decode problem response: %v (body: %s)", err, rec.Body.String())
	}
	if body.Status != wantStatus {
		t.Fatalf("problem.status = %d, want %d", body.Status, wantStatus)
	}
	if !strings.HasPrefix(body.Type, "https://errors.order-management.warehouse-systems.dev/") {
		t.Fatalf("problem.type = %q, want this service's error namespace", body.Type)
	}
	if body.Title == "" || body.Detail == "" {
		t.Fatalf("problem must carry a title and a detail: %+v", body)
	}
	return body
}

// receiveOrder posts a two-line order and returns its id.
func (e *testEnv) receiveOrder(t *testing.T, allowPartialShipment bool) string {
	t.Helper()
	body := `{"lines":[{"sku":"SKU-1","quantity":2,"pathId":"pick"},{"sku":"SKU-2","quantity":1,"giftWrap":true}],"allowPartialShipment":` +
		map[bool]string{true: "true", false: "false"}[allowPartialShipment] + `}`
	rec := e.do(t, http.MethodPost, "/orders", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /orders status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	return decodeOrder(t, rec).ID
}

func TestHealthz(t *testing.T) {
	e := newTestEnv(t)
	rec := e.do(t, http.MethodGet, "/healthz", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("body = %v, want status ok", body)
	}
}

func TestPostOrders(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		e := newTestEnv(t)
		rec := e.do(t, http.MethodPost, "/orders",
			`{"lines":[{"sku":"SKU-1","quantity":2,"pathId":"singles","giftWrap":true},{"sku":"SKU-2","quantity":1}]}`)

		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
		}
		body := decodeOrder(t, rec)
		if body.Status != string(order.StatusReceived) {
			t.Fatalf("status = %q, want %q", body.Status, order.StatusReceived)
		}
		if body.AllowPartialShipment {
			t.Fatal("allowPartialShipment must default to false (ship-complete)")
		}
		if len(body.Lines) != 2 {
			t.Fatalf("lines = %d, want 2", len(body.Lines))
		}
		if body.Lines[0].PathID != "singles" || !body.Lines[0].GiftWrap {
			t.Fatalf("line 1 = %+v", body.Lines[0])
		}
		// An omitted pathId defaults to "pick".
		if body.Lines[1].PathID != string(shared.DefaultPathId) {
			t.Fatalf("line 2 pathId = %q, want %q", body.Lines[1].PathID, shared.DefaultPathId)
		}
		if body.Lines[0].ReservationID != nil {
			t.Fatal("a received order's lines must have no reservation id")
		}
		if body.PromiseDate != nil {
			t.Fatal("a received order must have no promise date")
		}
		if loc := rec.Header().Get("Location"); loc != "/orders/"+body.ID {
			t.Fatalf("Location = %q, want /orders/%s", loc, body.ID)
		}
	})

	t.Run("error: no lines", func(t *testing.T) {
		e := newTestEnv(t)
		rec := e.do(t, http.MethodPost, "/orders", `{"lines":[]}`)
		p := assertProblem(t, rec, http.StatusBadRequest)
		if !strings.HasSuffix(p.Type, "order-without-lines") {
			t.Fatalf("problem.type = %q", p.Type)
		}
	})

	t.Run("error: empty sku", func(t *testing.T) {
		e := newTestEnv(t)
		rec := e.do(t, http.MethodPost, "/orders", `{"lines":[{"sku":"","quantity":1}]}`)
		p := assertProblem(t, rec, http.StatusBadRequest)
		if !strings.HasSuffix(p.Type, "empty-sku") {
			t.Fatalf("problem.type = %q", p.Type)
		}
	})

	t.Run("error: non-positive quantity", func(t *testing.T) {
		e := newTestEnv(t)
		rec := e.do(t, http.MethodPost, "/orders", `{"lines":[{"sku":"SKU-1","quantity":0}]}`)
		p := assertProblem(t, rec, http.StatusUnprocessableEntity)
		if !strings.HasSuffix(p.Type, "non-positive-quantity") {
			t.Fatalf("problem.type = %q", p.Type)
		}
	})

	t.Run("error: malformed body", func(t *testing.T) {
		e := newTestEnv(t)
		rec := e.do(t, http.MethodPost, "/orders", `{"lines":`)
		p := assertProblem(t, rec, http.StatusBadRequest)
		if !strings.HasSuffix(p.Type, "malformed-request-body") {
			t.Fatalf("problem.type = %q", p.Type)
		}
	})
}

func TestGetOrder(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		e := newTestEnv(t)
		id := e.receiveOrder(t, false)

		rec := e.do(t, http.MethodGet, "/orders/"+id, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		body := decodeOrder(t, rec)
		if body.ID != id || body.Status != string(order.StatusReceived) {
			t.Fatalf("body = %+v", body)
		}
	})

	t.Run("error: unknown order", func(t *testing.T) {
		e := newTestEnv(t)
		rec := e.do(t, http.MethodGet, "/orders/ord-missing", "")
		p := assertProblem(t, rec, http.StatusNotFound)
		if !strings.HasSuffix(p.Type, "order-not-found") {
			t.Fatalf("problem.type = %q", p.Type)
		}
		if p.Instance != "/orders/ord-missing" {
			t.Fatalf("problem.instance = %q", p.Instance)
		}
	})
}

func TestPostAllocate(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		e := newTestEnv(t)
		id := e.receiveOrder(t, false)

		rec := e.do(t, http.MethodPost, "/orders/"+id+"/allocate", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
		body := decodeOrder(t, rec)
		if body.Status != string(order.StatusAllocated) {
			t.Fatalf("status = %q, want %q", body.Status, order.StatusAllocated)
		}
		if body.PromiseDate == nil {
			t.Fatal("an allocated order must carry a promise date")
		}
		if _, err := time.Parse(time.RFC3339, *body.PromiseDate); err != nil {
			t.Fatalf("promiseDate %q is not RFC 3339: %v", *body.PromiseDate, err)
		}
		for _, l := range body.Lines {
			if l.ReservationID == nil || *l.ReservationID != "res-stub" {
				t.Fatalf("line %d reservationId = %v, want res-stub", l.LineNo, l.ReservationID)
			}
		}
	})

	t.Run("success: a 409 from inventory-storage backorders the order", func(t *testing.T) {
		e := newTestEnv(t)
		id := e.receiveOrder(t, false)
		e.inventory.reserveErr = ports.ErrInsufficientStock

		rec := e.do(t, http.MethodPost, "/orders/"+id+"/allocate", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 — a backorder is a business fact, not an error", rec.Code)
		}
		if body := decodeOrder(t, rec); body.Status != string(order.StatusBackordered) {
			t.Fatalf("status = %q, want %q", body.Status, order.StatusBackordered)
		}
	})

	t.Run("error: unknown order", func(t *testing.T) {
		e := newTestEnv(t)
		rec := e.do(t, http.MethodPost, "/orders/ord-missing/allocate", "")
		assertProblem(t, rec, http.StatusNotFound)
	})

	t.Run("error: downstream not configured", func(t *testing.T) {
		e := newTestEnv(t)
		id := e.receiveOrder(t, false)
		e.inventory.reserveErr = ports.ErrDownstreamNotConfigured

		rec := e.do(t, http.MethodPost, "/orders/"+id+"/allocate", "")
		p := assertProblem(t, rec, http.StatusServiceUnavailable)
		if !strings.HasSuffix(p.Type, "downstream-not-configured") {
			t.Fatalf("problem.type = %q", p.Type)
		}
	})
}

func TestPostRetryAllocation(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		e := newTestEnv(t)
		id := e.receiveOrder(t, false)
		e.inventory.reserveErr = ports.ErrInsufficientStock
		if rec := e.do(t, http.MethodPost, "/orders/"+id+"/allocate", ""); rec.Code != http.StatusOK {
			t.Fatalf("allocate status = %d", rec.Code)
		}

		e.inventory.reserveErr = nil
		rec := e.do(t, http.MethodPost, "/orders/"+id+"/retry-allocation", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
		if body := decodeOrder(t, rec); body.Status != string(order.StatusAllocated) {
			t.Fatalf("status = %q, want %q", body.Status, order.StatusAllocated)
		}
	})

	t.Run("error: nothing backordered", func(t *testing.T) {
		e := newTestEnv(t)
		id := e.receiveOrder(t, false)

		rec := e.do(t, http.MethodPost, "/orders/"+id+"/retry-allocation", "")
		p := assertProblem(t, rec, http.StatusConflict)
		if !strings.HasSuffix(p.Type, "no-backordered-lines") {
			t.Fatalf("problem.type = %q", p.Type)
		}
	})
}

func TestPostRelease(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		e := newTestEnv(t)
		id := e.receiveOrder(t, false)
		if rec := e.do(t, http.MethodPost, "/orders/"+id+"/allocate", ""); rec.Code != http.StatusOK {
			t.Fatalf("allocate status = %d", rec.Code)
		}

		rec := e.do(t, http.MethodPost, "/orders/"+id+"/release", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
		body := decodeOrder(t, rec)
		if body.Status != string(order.StatusReleased) {
			t.Fatalf("status = %q, want %q", body.Status, order.StatusReleased)
		}
	})

	t.Run("error: ship-complete order with a backordered line", func(t *testing.T) {
		e := newTestEnv(t)
		id := e.receiveOrder(t, false)
		e.inventory.reserveErr = ports.ErrInsufficientStock
		if rec := e.do(t, http.MethodPost, "/orders/"+id+"/allocate", ""); rec.Code != http.StatusOK {
			t.Fatalf("allocate status = %d", rec.Code)
		}

		rec := e.do(t, http.MethodPost, "/orders/"+id+"/release", "")
		p := assertProblem(t, rec, http.StatusConflict)
		if !strings.HasSuffix(p.Type, "ship-complete-blocked") {
			t.Fatalf("problem.type = %q", p.Type)
		}
	})

	t.Run("error: wes-work-planning failure", func(t *testing.T) {
		e := newTestEnv(t)
		id := e.receiveOrder(t, false)
		if rec := e.do(t, http.MethodPost, "/orders/"+id+"/allocate", ""); rec.Code != http.StatusOK {
			t.Fatalf("allocate status = %d", rec.Code)
		}
		e.work.err = ports.ErrDownstreamNotConfigured

		rec := e.do(t, http.MethodPost, "/orders/"+id+"/release", "")
		assertProblem(t, rec, http.StatusServiceUnavailable)
	})
}

func TestDeleteOrder(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		e := newTestEnv(t)
		id := e.receiveOrder(t, false)
		if rec := e.do(t, http.MethodPost, "/orders/"+id+"/allocate", ""); rec.Code != http.StatusOK {
			t.Fatalf("allocate status = %d", rec.Code)
		}

		rec := e.do(t, http.MethodDelete, "/orders/"+id, "")
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
		}
		if rec.Body.Len() != 0 {
			t.Fatalf("204 must have an empty body, got %q", rec.Body.String())
		}

		got := e.do(t, http.MethodGet, "/orders/"+id, "")
		if body := decodeOrder(t, got); body.Status != string(order.StatusCancelled) {
			t.Fatalf("status = %q, want %q", body.Status, order.StatusCancelled)
		}
	})

	// BR6 over the wire.
	t.Run("error: already released", func(t *testing.T) {
		e := newTestEnv(t)
		id := e.receiveOrder(t, false)
		if rec := e.do(t, http.MethodPost, "/orders/"+id+"/allocate", ""); rec.Code != http.StatusOK {
			t.Fatalf("allocate status = %d", rec.Code)
		}
		if rec := e.do(t, http.MethodPost, "/orders/"+id+"/release", ""); rec.Code != http.StatusOK {
			t.Fatalf("release status = %d", rec.Code)
		}

		rec := e.do(t, http.MethodDelete, "/orders/"+id, "")
		p := assertProblem(t, rec, http.StatusConflict)
		if !strings.HasSuffix(p.Type, "order-already-released") {
			t.Fatalf("problem.type = %q", p.Type)
		}
	})

	t.Run("error: unknown order", func(t *testing.T) {
		e := newTestEnv(t)
		rec := e.do(t, http.MethodDelete, "/orders/ord-missing", "")
		assertProblem(t, rec, http.StatusNotFound)
	})
}

// An unmapped error must still produce a well-formed 500 problem, never a
// bare panic or an empty body.
func TestUnmappedErrorBecomesAProblem500(t *testing.T) {
	e := newTestEnv(t)
	id := e.receiveOrder(t, false)
	e.inventory.reserveErr = errUnmapped

	rec := e.do(t, http.MethodPost, "/orders/"+id+"/allocate", "")
	p := assertProblem(t, rec, http.StatusInternalServerError)
	if !strings.HasSuffix(p.Type, "internal-error") {
		t.Fatalf("problem.type = %q", p.Type)
	}
}
