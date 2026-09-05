package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/claudioed/order-management/internal/application/usecases"
	"github.com/claudioed/order-management/internal/domain/order"
	"github.com/claudioed/order-management/internal/domain/shared"
)

// DefaultServiceName labels this service in logs when the caller does not
// supply one.
const DefaultServiceName = "order-management"

// Server holds every use case the HTTP adapter depends on.
type Server struct {
	ReceiveOrder    *usecases.ReceiveOrder
	RetryAllocation *usecases.RetryAllocation
	CancelOrder     *usecases.CancelOrder
	GetOrder        *usecases.GetOrder
}

// NewRouter builds the chi router for every endpoint in CLAUDE.md's REST
// API. A nil logger defaults to slog.Default().
func NewRouter(s *Server, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(RequestLogger(logger))
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware())

	r.Get("/healthz", s.handleHealthz)
	r.Post("/orders", s.handleReceiveOrder)
	r.Get("/orders/{id}", s.handleGetOrder)
	r.Post("/orders/{id}/retry-allocation", s.handleRetryAllocation)
	r.Delete("/orders/{id}", s.handleCancelOrder)

	return r
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReceiveOrder(w http.ResponseWriter, r *http.Request) {
	var req receiveOrderRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Lines) == 0 {
		writeError(w, r, order.ErrNoLines)
		return
	}

	lines := make([]usecases.NewLine, 0, len(req.Lines))
	for _, l := range req.Lines {
		sku, err := shared.NewSKU(l.SKU)
		if err != nil {
			writeError(w, r, err)
			return
		}
		// PathId is never caller-supplied on this public intake DTO — see
		// receiveOrderLineRequest's doc comment. Every line unconditionally
		// gets the internal default; NewPathIdOrDefault("") always resolves
		// to shared.DefaultPathId.
		pathID, err := shared.NewPathIdOrDefault("")
		if err != nil {
			writeError(w, r, err)
			return
		}
		lines = append(lines, usecases.NewLine{
			SKU: sku, Quantity: l.Quantity, PathID: pathID, GiftWrap: l.GiftWrap,
		})
	}

	o, err := s.ReceiveOrder.Execute(r.Context(), lines, req.AllowPartialShipment)
	if err != nil {
		writeError(w, r, err)
		return
	}

	w.Header().Set("Location", "/orders/"+o.ID().String())
	writeJSON(w, http.StatusCreated, toOrderResponse(o))
}

func (s *Server) handleGetOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := orderIDParam(w, r)
	if !ok {
		return
	}
	o, err := s.GetOrder.Execute(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toOrderResponse(o))
}

func (s *Server) handleRetryAllocation(w http.ResponseWriter, r *http.Request) {
	id, ok := orderIDParam(w, r)
	if !ok {
		return
	}
	o, err := s.RetryAllocation.Execute(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toOrderResponse(o))
}

func (s *Server) handleCancelOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := orderIDParam(w, r)
	if !ok {
		return
	}
	if _, err := s.CancelOrder.Execute(r.Context(), id); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// orderIDParam extracts and validates the {id} path parameter, writing the
// problem response itself when it is not a valid OrderId.
func orderIDParam(w http.ResponseWriter, r *http.Request) (shared.OrderId, bool) {
	id, err := shared.NewOrderId(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, r, err)
		return "", false
	}
	return id, true
}

const timeFormat = time.RFC3339

func toOrderResponse(o *order.Order) orderResponse {
	lines := make([]orderLineResponse, 0, len(o.Lines()))
	for _, l := range o.Lines() {
		lines = append(lines, orderLineResponse{
			LineNo:        l.LineNo(),
			SKU:           l.SKU().String(),
			Quantity:      l.Quantity(),
			PathID:        l.PathID().String(),
			GiftWrap:      l.GiftWrap(),
			Status:        string(l.Status()),
			ReservationID: l.ReservationID(),
		})
	}

	var promiseDate *string
	if d := o.PromiseDate(); d != nil {
		formatted := d.UTC().Format(timeFormat)
		promiseDate = &formatted
	}

	return orderResponse{
		ID:                   o.ID().String(),
		Status:               string(o.Status()),
		AllowPartialShipment: o.AllowPartialShipment(),
		PromiseDate:          promiseDate,
		Lines:                lines,
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dest any) bool {
	if err := json.NewDecoder(r.Body).Decode(dest); err != nil {
		writeProblem(w, http.StatusBadRequest, problemInfo{"malformed-request-body", "The request body is not valid JSON"}, err.Error(), r.URL.Path)
		return false
	}
	return true
}

// writeError writes a domain/application error as an RFC 7807
// (application/problem+json) response.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	writeProblem(w, statusFor(err), problemFor(err), err.Error(), r.URL.Path)
}

func writeProblem(w http.ResponseWriter, status int, info problemInfo, detail, instance string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problemDetails{
		Type:     problemBaseURI + info.slug,
		Title:    info.title,
		Status:   status,
		Detail:   detail,
		Instance: instance,
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// corsMiddleware allows the warehouse-console browser SPA (and this
// service's own future MFE remote dev origin) to call this API directly
// from the browser. Static-bearer-key auth, not cookies, so credentials
// are never needed here. CORS_ALLOWED_ORIGINS overrides the local-dev
// default (comma-separated) for staging/prod deployments.
func corsMiddleware() func(http.Handler) http.Handler {
	origins := []string{"http://localhost:5173", "http://localhost:5181"}
	if v := os.Getenv("CORS_ALLOWED_ORIGINS"); v != "" {
		origins = strings.Split(v, ",")
	}
	return cors.Handler(cors.Options{
		AllowedOrigins:   origins,
		AllowedMethods:   []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete},
		AllowedHeaders:   []string{"Content-Type", "Authorization"},
		AllowCredentials: false,
		MaxAge:           300,
	})
}
