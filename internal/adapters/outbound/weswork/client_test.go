package weswork_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/claudioed/order-management/internal/adapters/outbound/weswork"
	"github.com/claudioed/order-management/internal/application/ports"
)

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

func TestEnqueueWorkUnitSendsWesWorkPlanningsPublishedRequestShape(t *testing.T) {
	var got captured
	srv := newServer(t, http.StatusCreated,
		`{"id":"wu-1","pathId":"pick","cpt":"2026-08-27T09:00:00Z","reference":"ord-7","state":"Pending","giftWrap":true}`, &got)

	client := weswork.NewClient(srv.URL+"/", nil)
	cpt := time.Date(2026, 8, 27, 9, 0, 0, 0, time.UTC)

	result, err := client.EnqueueWorkUnit(context.Background(), ports.WorkUnitRequest{
		PathID: "pick", WorkUnitID: "ord-7-line-1", CPT: cpt,
		Reference: "ord-7", SKU: "SKU-1", GiftWrap: true,
	})
	if err != nil {
		t.Fatalf("EnqueueWorkUnit: %v", err)
	}

	if result.WorkUnitID != "wu-1" {
		t.Fatalf("WorkUnitID = %q, want wu-1", result.WorkUnitID)
	}
	if got.method != http.MethodPost || got.path != "/paths/pick/work-units" {
		t.Fatalf("called %s %s, want POST /paths/pick/work-units", got.method, got.path)
	}
	if got.body["workUnitId"] != "ord-7-line-1" || got.body["reference"] != "ord-7" ||
		got.body["sku"] != "SKU-1" || got.body["giftWrap"] != true {
		t.Fatalf("request body = %v", got.body)
	}
	// cpt must be RFC 3339, the format that endpoint documents.
	if got.body["cpt"] != "2026-08-27T09:00:00Z" {
		t.Fatalf("cpt = %v, want RFC 3339 2026-08-27T09:00:00Z", got.body["cpt"])
	}
}

func TestEnqueueWorkUnitNormalisesCPTToUTC(t *testing.T) {
	var got captured
	srv := newServer(t, http.StatusCreated, `{"id":"wu-1"}`, &got)
	client := weswork.NewClient(srv.URL, nil)

	zone := time.FixedZone("UTC+2", 2*60*60)
	cpt := time.Date(2026, 8, 27, 11, 0, 0, 0, zone)

	if _, err := client.EnqueueWorkUnit(context.Background(), ports.WorkUnitRequest{
		PathID: "pick", WorkUnitID: "wu-1", CPT: cpt, Reference: "ord-1",
	}); err != nil {
		t.Fatalf("EnqueueWorkUnit: %v", err)
	}
	if got.body["cpt"] != "2026-08-27T09:00:00Z" {
		t.Fatalf("cpt = %v, want the UTC form 2026-08-27T09:00:00Z", got.body["cpt"])
	}
}

func TestEnqueueWorkUnitOmitsAnAbsentSKU(t *testing.T) {
	var got captured
	srv := newServer(t, http.StatusCreated, `{"id":"wu-1"}`, &got)
	client := weswork.NewClient(srv.URL, nil)

	if _, err := client.EnqueueWorkUnit(context.Background(), ports.WorkUnitRequest{
		PathID: "pick", WorkUnitID: "wu-1", CPT: time.Now(), Reference: "ord-1",
	}); err != nil {
		t.Fatalf("EnqueueWorkUnit: %v", err)
	}
	if _, present := got.body["sku"]; present {
		t.Fatalf("an absent sku must be omitted, not sent as \"\": %v", got.body)
	}
}

func TestEnqueueWorkUnitMapsStatuses(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		responseBody string
		wantErr      error
	}{
		{name: "201 is a work unit", status: http.StatusCreated, responseBody: `{"id":"wu-1"}`},
		{name: "200 is also accepted", status: http.StatusOK, responseBody: `{"id":"wu-1"}`},
		{name: "400 is a failure", status: http.StatusBadRequest, wantErr: weswork.ErrUnexpectedStatus},
		{name: "409 duplicate work unit is a failure", status: http.StatusConflict, wantErr: weswork.ErrUnexpectedStatus},
		{name: "500 is a failure", status: http.StatusInternalServerError, wantErr: weswork.ErrUnexpectedStatus},
		{name: "a 2xx with no id is unusable", status: http.StatusCreated, responseBody: `{}`, wantErr: weswork.ErrUnexpectedStatus},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got captured
			srv := newServer(t, tt.status, tt.responseBody, &got)
			client := weswork.NewClient(srv.URL, nil)

			_, err := client.EnqueueWorkUnit(context.Background(), ports.WorkUnitRequest{
				PathID: "pick", WorkUnitID: "wu-1", CPT: time.Now(), Reference: "ord-1",
			})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestEnqueueWorkUnitOnAnUndecodableBody(t *testing.T) {
	var got captured
	srv := newServer(t, http.StatusCreated, `not json`, &got)
	client := weswork.NewClient(srv.URL, nil)

	if _, err := client.EnqueueWorkUnit(context.Background(), ports.WorkUnitRequest{
		PathID: "pick", WorkUnitID: "wu-1", CPT: time.Now(), Reference: "ord-1",
	}); err == nil {
		t.Fatal("expected an error decoding a non-JSON 201 body")
	}
}

func TestPathIDIsPathEscaped(t *testing.T) {
	var got captured
	srv := newServer(t, http.StatusCreated, `{"id":"wu-1"}`, &got)
	client := weswork.NewClient(srv.URL, nil)

	if _, err := client.EnqueueWorkUnit(context.Background(), ports.WorkUnitRequest{
		PathID: "pick a", WorkUnitID: "wu-1", CPT: time.Now(), Reference: "ord-1",
	}); err != nil {
		t.Fatalf("EnqueueWorkUnit: %v", err)
	}
	if got.path != "/paths/pick a/work-units" {
		t.Fatalf("path = %q, want the path id escaped into one segment", got.path)
	}
}

type errDoer struct{ err error }

func (d errDoer) Do(*http.Request) (*http.Response, error) { return nil, d.err }

func TestTransportFailurePropagates(t *testing.T) {
	boom := errors.New("connection refused")
	client := weswork.NewClient("http://example.invalid", errDoer{err: boom})

	_, err := client.EnqueueWorkUnit(context.Background(), ports.WorkUnitRequest{
		PathID: "pick", WorkUnitID: "wu-1", CPT: time.Now(), Reference: "ord-1",
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
}

func TestPermissiveClientRefusesRatherThanFabricating(t *testing.T) {
	client := weswork.NewPermissiveClient()

	result, err := client.EnqueueWorkUnit(context.Background(), ports.WorkUnitRequest{PathID: "pick"})
	if !errors.Is(err, ports.ErrDownstreamNotConfigured) {
		t.Fatalf("err = %v, want %v", err, ports.ErrDownstreamNotConfigured)
	}
	if result.WorkUnitID != "" {
		t.Fatalf("the permissive client must never fabricate a work unit id, got %q", result.WorkUnitID)
	}
}

func TestNewClientDefaultsItsDoer(t *testing.T) {
	if weswork.NewClient("http://example.invalid", nil) == nil {
		t.Fatal("NewClient returned nil")
	}
}
