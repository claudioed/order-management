package weswork

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

// DefaultTimeout bounds a single call to wes-work-planning.
const DefaultTimeout = 5 * time.Second

// ErrUnexpectedStatus wraps a wes-work-planning response status this client
// has no specific handling for.
var ErrUnexpectedStatus = errors.New("wes-work-planning: unexpected response status")

// HTTPDoer is the subset of *http.Client this adapter depends on, so unit
// tests can substitute a fake transport without a real server.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client is a plain net/http implementation of ports.WorkReleaseClient,
// calling wes-work-planning's published contract:
//
//	POST /paths/{pathId}/work-units -> 201 with the work unit
//
// `sku` and `giftWrap` are optional fields that endpoint already accepts
// today — nothing in wes-work-planning changes for this context to exist
// (see ADR 0002). It imports nothing from that module: the shapes below
// are local mirrors of its published wire contract.
type Client struct {
	baseURL string
	doer    HTTPDoer
}

// NewClient builds a Client against baseURL (from WES_WORK_PLANNING_BASE_URL).
// A nil doer defaults to an *http.Client with DefaultTimeout.
func NewClient(baseURL string, doer HTTPDoer) *Client {
	if doer == nil {
		doer = &http.Client{Timeout: DefaultTimeout}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), doer: doer}
}

// enqueueWorkUnitRequest mirrors wes-work-planning's
// EnqueueWorkUnitRequest. cpt is RFC 3339.
type enqueueWorkUnitRequest struct {
	WorkUnitID string `json:"workUnitId"`
	CPT        string `json:"cpt"`
	Reference  string `json:"reference"`
	SKU        string `json:"sku,omitempty"`
	GiftWrap   bool   `json:"giftWrap"`
}

// workUnitResponse mirrors wes-work-planning's WorkUnitResponse. Only `id`
// is consumed — WorkUnit state is owned upstream.
type workUnitResponse struct {
	ID string `json:"id"`
}

// cptFormat is RFC 3339, the format wes-work-planning's `cpt` field takes.
const cptFormat = time.RFC3339

// EnqueueWorkUnit calls POST /paths/{pathId}/work-units for one allocated
// order line. Any non-2xx (including the 409 that endpoint returns for a
// duplicate workUnitId) is a hard error: unlike allocation, there is no
// business fact for this context to record on the line instead.
func (c *Client) EnqueueWorkUnit(ctx context.Context, req ports.WorkUnitRequest) (ports.WorkUnitResult, error) {
	body, err := json.Marshal(enqueueWorkUnitRequest{
		WorkUnitID: req.WorkUnitID,
		CPT:        req.CPT.UTC().Format(cptFormat),
		Reference:  req.Reference.String(),
		SKU:        req.SKU.String(),
		GiftWrap:   req.GiftWrap,
	})
	if err != nil {
		return ports.WorkUnitResult{}, err
	}

	endpoint := fmt.Sprintf("%s/paths/%s/work-units", c.baseURL, url.PathEscape(req.PathID.String()))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return ports.WorkUnitResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.doer.Do(httpReq)
	if err != nil {
		return ports.WorkUnitResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK:
		var decoded workUnitResponse
		if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
			return ports.WorkUnitResult{}, err
		}
		if decoded.ID == "" {
			return ports.WorkUnitResult{}, fmt.Errorf("%w: 2xx response carried no work unit id", ErrUnexpectedStatus)
		}
		return ports.WorkUnitResult{WorkUnitID: decoded.ID}, nil
	default:
		return ports.WorkUnitResult{}, fmt.Errorf("%w: %d", ErrUnexpectedStatus, resp.StatusCode)
	}
}
