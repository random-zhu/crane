package model

import (
	"testing"
	"time"
)

func validLineItem() CostLineItem {
	return CostLineItem{
		Provider:       ProviderAliyun,
		PayerAccountID: "payer-1",
		BillingPeriod:  "2026-07",
		LineItemID:     "line-1",
		Service:        "ECS",
		Category:       CostCategoryCompute,
		ChargeModel:    ChargeModelPayAsYouGo,
		UsageQuantity:  MustDecimal("1.25"),
		ListCost:       Money{Amount: MustDecimal("10"), Currency: CurrencyCNY},
		NetCost:        Money{Amount: MustDecimal("8"), Currency: CurrencyCNY},
		AmortizedCost:  Money{Amount: MustDecimal("8"), Currency: CurrencyCNY},
		ObservedAt:     time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC),
	}
}

func TestCostLineItemIdempotencyKey(t *testing.T) {
	item := validLineItem()
	if err := item.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := item.IdempotencyKey(); got != "aliyun:payer-1:2026-07:line-1" {
		t.Fatalf("unexpected idempotency key %q", got)
	}

	item.LineItemID = ""
	item.Tags = map[string]string{"team": "platform", "env": "prod"}
	first := item.IdempotencyKey()
	item.NetCost.Amount = MustDecimal("7.5") // delayed billing correction
	item.Tags = map[string]string{"env": "prod", "team": "platform"}
	if second := item.IdempotencyKey(); first != second {
		t.Fatalf("correction or map order changed key: %s != %s", first, second)
	}
}

func TestCostLineItemRejectsCurrencyMismatch(t *testing.T) {
	item := validLineItem()
	item.NetCost.Currency = CurrencyUSD
	if err := item.Validate(); err == nil {
		t.Fatal("expected currency mismatch to fail")
	}
}
