package allocation

import (
	"testing"
	"time"

	"github.com/gocrane/crane/pkg/cost/model"
)

func TestAllocateBalancesRoundedShares(t *testing.T) {
	item := allocationItem("10")
	result, err := Allocate(item, []Target{
		{Namespace: "a", Weight: model.MustDecimal("1")},
		{Namespace: "b", Weight: model.MustDecimal("1")},
		{Namespace: "c", Weight: model.MustDecimal("1")},
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Balanced || result.Allocated.Amount != model.MustDecimal("10") || !result.Unallocated.Amount.IsZero() {
		t.Fatalf("allocation did not balance: %+v", result)
	}
	got := []model.Decimal{result.Shares[0].Amount.Amount, result.Shares[1].Amount.Amount, result.Shares[2].Amount.Amount}
	want := []model.Decimal{model.MustDecimal("3.33"), model.MustDecimal("3.33"), model.MustDecimal("3.34")}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("share %d = %s, want %s", index, got[index], want[index])
		}
	}
}

func TestAllocateRefundAndNoTargets(t *testing.T) {
	item := allocationItem("-1")
	result, err := Allocate(item, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Allocated.Amount.IsZero() || result.Unallocated.Amount != model.MustDecimal("-1") || !result.Balanced {
		t.Fatalf("unexpected unallocated refund: %+v", result)
	}
}

func TestAllocateRejectsNegativeWeight(t *testing.T) {
	_, err := Allocate(allocationItem("1"), []Target{{Namespace: "bad", Weight: model.MustDecimal("-1")}}, 2)
	if err == nil {
		t.Fatal("expected negative weight to fail")
	}
}

func allocationItem(amount string) model.CostLineItem {
	money := model.Money{Amount: model.MustDecimal(amount), Currency: model.CurrencyCNY}
	return model.CostLineItem{
		Provider: model.ProviderTencent, PayerAccountID: "payer", BillingPeriod: "2026-07",
		LineItemID: "line", Service: "CVM", ChargeModel: model.ChargeModelPayAsYouGo,
		UsageQuantity: model.MustDecimal("1"), ListCost: money, NetCost: money,
		AmortizedCost: money, ObservedAt: time.Now(),
	}
}
