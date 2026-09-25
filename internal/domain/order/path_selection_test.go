package order

import (
	"testing"

	"github.com/claudioed/order-management/internal/domain/shared"
)

// fakeEligibilitySource is a scripted EligibilitySource for these unit
// tests -- mirrors fakeCatalogue's scripting style used in
// internal/application/usecases's own tests, kept local here since domain
// tests must not depend on the application package.
type fakeEligibilitySource struct {
	eligibility map[shared.PathId]shared.Eligibility
	known       map[shared.PathId]bool
}

func (f fakeEligibilitySource) Eligibility(pathID shared.PathId) (shared.Eligibility, bool) {
	if !f.known[pathID] {
		return shared.Eligibility{}, false
	}
	return f.eligibility[pathID], true
}

func TestPathSelectionPolicySelect(t *testing.T) {
	permissive := fakeEligibilitySource{
		known:       map[shared.PathId]bool{shared.DefaultPathId: true},
		eligibility: map[shared.PathId]shared.Eligibility{shared.DefaultPathId: shared.NewEligibility(nil, nil, nil, false)},
	}

	maxOne := 1
	singlesOnly := fakeEligibilitySource{
		known:       map[shared.PathId]bool{shared.DefaultPathId: true},
		eligibility: map[shared.PathId]shared.Eligibility{shared.DefaultPathId: shared.NewEligibility(&maxOne, nil, nil, false)},
	}

	requiresHazmat := fakeEligibilitySource{
		known:       map[shared.PathId]bool{shared.DefaultPathId: true},
		eligibility: map[shared.PathId]shared.Eligibility{shared.DefaultPathId: shared.NewEligibility(nil, []string{"hazmat"}, nil, false)},
	}

	excludesHazmat := fakeEligibilitySource{
		known:       map[shared.PathId]bool{shared.DefaultPathId: true},
		eligibility: map[shared.PathId]shared.Eligibility{shared.DefaultPathId: shared.NewEligibility(nil, nil, []string{"hazmat"}, false)},
	}

	excludesGiftWrap := fakeEligibilitySource{
		known:       map[shared.PathId]bool{shared.DefaultPathId: true},
		eligibility: map[shared.PathId]shared.Eligibility{shared.DefaultPathId: shared.NewEligibility(nil, nil, []string{"giftWrap"}, false)},
	}

	unknownEligibility := fakeEligibilitySource{known: map[shared.PathId]bool{}}

	tests := []struct {
		name       string
		sku        shared.SKU
		quantity   int
		giftWrap   bool
		attributes []string
		catalogue  EligibilitySource
		wantPathID shared.PathId
		wantOK     bool
	}{
		{
			name: "nil catalogue falls open to the default path (ADR-0013 floor)",
			sku:  "SKU-1", quantity: 1,
			catalogue: nil, wantPathID: shared.DefaultPathId, wantOK: true,
		},
		{
			name: "unknown eligibility falls open to the default path",
			sku:  "SKU-1", quantity: 1,
			catalogue: unknownEligibility, wantPathID: shared.DefaultPathId, wantOK: true,
		},
		{
			name: "permissive eligibility (zero value) admits any line",
			sku:  "SKU-1", quantity: 50, giftWrap: true,
			catalogue: permissive, wantPathID: shared.DefaultPathId, wantOK: true,
		},
		{
			name: "quantity within MaxUnitsPerLine is eligible",
			sku:  "SKU-1", quantity: 1,
			catalogue: singlesOnly, wantPathID: shared.DefaultPathId, wantOK: true,
		},
		{
			name: "quantity exceeding MaxUnitsPerLine is rejected",
			sku:  "SKU-1", quantity: 2,
			catalogue: singlesOnly, wantOK: false,
		},
		{
			name: "a required product attribute present is eligible",
			sku:  "SKU-1", quantity: 1, attributes: []string{"hazmat"},
			catalogue: requiresHazmat, wantPathID: shared.DefaultPathId, wantOK: true,
		},
		{
			name: "a required product attribute missing is rejected",
			sku:  "SKU-1", quantity: 1,
			catalogue: requiresHazmat, wantOK: false,
		},
		{
			name: "an excluded product attribute present is rejected",
			sku:  "SKU-1", quantity: 1, attributes: []string{"hazmat"},
			catalogue: excludesHazmat, wantOK: false,
		},
		{
			name: "no excluded product attribute present is eligible",
			sku:  "SKU-1", quantity: 1,
			catalogue: excludesHazmat, wantPathID: shared.DefaultPathId, wantOK: true,
		},
		{
			name: "giftWrap=true is folded into the evaluated attributes and can be excluded",
			sku:  "SKU-1", quantity: 1, giftWrap: true,
			catalogue: excludesGiftWrap, wantOK: false,
		},
		{
			name: "giftWrap=false never contributes the giftWrap attribute",
			sku:  "SKU-1", quantity: 1, giftWrap: false,
			catalogue: excludesGiftWrap, wantPathID: shared.DefaultPathId, wantOK: true,
		},
	}

	var policy PathSelectionPolicy
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotOK := policy.Select(tt.sku, tt.quantity, tt.giftWrap, tt.attributes, tt.catalogue)
			if gotOK != tt.wantOK {
				t.Fatalf("Select(...) ok = %v, want %v", gotOK, tt.wantOK)
			}
			if gotOK && gotID != tt.wantPathID {
				t.Fatalf("Select(...) = %q, want %q", gotID, tt.wantPathID)
			}
			if !gotOK && gotID != "" {
				t.Fatalf("Select(...) on ok=false must return the zero PathId, got %q", gotID)
			}
		})
	}
}
