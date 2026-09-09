package http_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/order-management/internal/adapters/inbound/auth"
	inboundhttp "github.com/claudioed/order-management/internal/adapters/inbound/http"
	"github.com/claudioed/order-management/internal/adapters/outbound/memory"
	"github.com/claudioed/order-management/internal/application/usecases"
	"github.com/claudioed/order-management/internal/domain/order"
)

const (
	testReadKey = "r-key"
	testRWKey   = "rw-key"
)

func enforcingMiddleware() auth.Middleware {
	return auth.Middleware{
		Authn: auth.NewStaticKeyAuth(map[string]auth.Scope{testReadKey: auth.ScopeRead, testRWKey: auth.ScopeReadWrite}),
		Mode:  auth.ModeEnforce,
	}
}

// newAuthEnv is newTestEnv with the fleet-standard auth middleware in
// enforce mode (ADR-0011). The problem-type base and logger are left unset
// on purpose so the router's own defaults are what gets exercised.
func newAuthEnv(t *testing.T) *testEnv {
	t.Helper()
	orders := memory.NewOrderRepo()
	inventory := &stubInventory{}
	clock := memory.NewFixedClock(time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC))
	promise := order.NewLeadTimePolicy(24*time.Hour, nil)
	server := &inboundhttp.Server{
		ReceiveOrder:    &usecases.ReceiveOrder{Orders: orders, Events: nopPublisher{}, Clock: clock, Inventory: inventory, Promise: promise},
		RetryAllocation: &usecases.RetryAllocation{Orders: orders, Inventory: inventory, Events: nopPublisher{}, Clock: clock, Promise: promise},
		CancelOrder:     &usecases.CancelOrder{Orders: orders, Inventory: inventory, Events: nopPublisher{}, Clock: clock},
		GetOrder:        &usecases.GetOrder{Orders: orders},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &testEnv{
		handler:   inboundhttp.NewRouter(server, logger, "", enforcingMiddleware()),
		orders:    orders,
		inventory: inventory,
	}
}

func doAuth(t *testing.T, h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func assertAuthProblem(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantSlug string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("want %d got %d body=%s", wantStatus, rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("want application/problem+json, got %q", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not json: %v", err)
	}
	wantType := "https://errors.order-management.warehouse-systems.dev/" + wantSlug
	if body["type"] != wantType {
		t.Fatalf("want problem type %q, got %v", wantType, body["type"])
	}
	if body["status"] != float64(wantStatus) {
		t.Fatalf("want status %d in body, got %v", wantStatus, body["status"])
	}
}

// TestRouter_AuthEnforce is the fleet router table test (rollout brief
// step 7): one GET and one mutating route, every credential class, plus
// the open liveness probe.
func TestRouter_AuthEnforce(t *testing.T) {
	env := newAuthEnv(t)
	const newOrderJSON = `{"lines":[{"sku":"SKU-1","quantity":1}]}`

	t.Run("no token GET -> 401 problem+json with WWW-Authenticate", func(t *testing.T) {
		rec := doAuth(t, env.handler, http.MethodGet, "/orders/ord-1", "", "")
		assertAuthProblem(t, rec, http.StatusUnauthorized, "unauthenticated")
		if !strings.HasPrefix(rec.Header().Get("WWW-Authenticate"), "Bearer") {
			t.Fatalf("401 must carry WWW-Authenticate: Bearer, got %q", rec.Header().Get("WWW-Authenticate"))
		}
	})

	t.Run("no token POST -> 401", func(t *testing.T) {
		rec := doAuth(t, env.handler, http.MethodPost, "/orders", "", newOrderJSON)
		assertAuthProblem(t, rec, http.StatusUnauthorized, "unauthenticated")
	})

	t.Run("bad token GET -> 401", func(t *testing.T) {
		rec := doAuth(t, env.handler, http.MethodGet, "/orders/ord-1", "nope", "")
		assertAuthProblem(t, rec, http.StatusUnauthorized, "unauthenticated")
	})

	t.Run("read key POST -> 403 insufficient-scope", func(t *testing.T) {
		rec := doAuth(t, env.handler, http.MethodPost, "/orders", testReadKey, newOrderJSON)
		assertAuthProblem(t, rec, http.StatusForbidden, "insufficient-scope")
		if rec.Header().Get("WWW-Authenticate") != "" {
			t.Fatal("403 must not carry WWW-Authenticate: the credential was valid")
		}
	})

	t.Run("read key DELETE -> 403", func(t *testing.T) {
		rec := doAuth(t, env.handler, http.MethodDelete, "/orders/ord-1", testReadKey, "")
		assertAuthProblem(t, rec, http.StatusForbidden, "insufficient-scope")
	})

	var created string
	t.Run("rw key POST -> 201", func(t *testing.T) {
		rec := doAuth(t, env.handler, http.MethodPost, "/orders", testRWKey, newOrderJSON)
		if rec.Code != http.StatusCreated {
			t.Fatalf("want 201 got %d body=%s", rec.Code, rec.Body.String())
		}
		var b orderBody
		if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
			t.Fatalf("decode: %v", err)
		}
		created = b.ID
	})

	t.Run("read key GET -> 200", func(t *testing.T) {
		if created == "" {
			t.Skip("depends on the rw POST above")
		}
		rec := doAuth(t, env.handler, http.MethodGet, "/orders/"+created, testReadKey, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("want 200 got %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("rw key GET -> 200", func(t *testing.T) {
		if created == "" {
			t.Skip("depends on the rw POST above")
		}
		rec := doAuth(t, env.handler, http.MethodGet, "/orders/"+created, testRWKey, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("want 200 got %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("/healthz with no token -> 200", func(t *testing.T) {
		rec := doAuth(t, env.handler, http.MethodGet, "/healthz", "", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("want 200 got %d body=%s", rec.Code, rec.Body.String())
		}
	})
}

// TestRouter_AuthOffByDefault pins the composition-root contract every
// existing handler test relies on: a zero-value middleware means "off".
func TestRouter_AuthOffByDefault(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(t, http.MethodGet, "/orders/ord-missing", "")
	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Fatalf("a router built without keys must not authenticate, got %d", rec.Code)
	}
}

// TestRouter_AuthLogMode lets everything through but keeps the middleware
// on the code path (the rollout gate for AUTH_MODE=log).
func TestRouter_AuthLogMode(t *testing.T) {
	var logs strings.Builder
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	m := enforcingMiddleware()
	m.Mode = auth.ModeLog
	server := &inboundhttp.Server{GetOrder: &usecases.GetOrder{Orders: memory.NewOrderRepo()}}
	h := inboundhttp.NewRouter(server, logger, "", m)

	rec := doAuth(t, h, http.MethodGet, "/orders/ord-missing", "", "")
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("log mode must not reject, got %d", rec.Code)
	}
	if !strings.Contains(logs.String(), "auth: would-reject") {
		t.Fatalf("log mode must log the would-reject, logs=%q", logs.String())
	}
}

// TestReportsRouter_AuthRequiresRead: the reader pins its policy to read
// scope on every route, and its /healthz stays open.
func TestReportsRouter_AuthRequiresRead(t *testing.T) {
	store := &stubStore{lag: 5 * time.Second}
	h := inboundhttp.NewReportsRouter(&inboundhttp.ReportsHandlers{Store: store}, nil, enforcingMiddleware())
	const path = "/reports/funnel/freshness"

	t.Run("no token -> 401", func(t *testing.T) {
		rec := doAuth(t, h, http.MethodGet, path, "", "")
		assertAuthProblem(t, rec, http.StatusUnauthorized, "unauthenticated")
		if rec.Header().Get("WWW-Authenticate") == "" {
			t.Fatal("401 must carry WWW-Authenticate")
		}
	})
	t.Run("read key -> 200", func(t *testing.T) {
		rec := doAuth(t, h, http.MethodGet, path, testReadKey, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("want 200 got %d body=%s", rec.Code, rec.Body.String())
		}
	})
	t.Run("rw key -> 200", func(t *testing.T) {
		rec := doAuth(t, h, http.MethodGet, path, testRWKey, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("want 200 got %d body=%s", rec.Code, rec.Body.String())
		}
	})
	t.Run("/healthz no token -> 200", func(t *testing.T) {
		rec := doAuth(t, h, http.MethodGet, "/healthz", "", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("want 200 got %d", rec.Code)
		}
	})
}
