package reconcile

import (
	"testing"
	"time"

	"github.com/gocrane/crane/pkg/cost/model"
)

func TestReconcilerAccountPrecedenceAndTagMerge(t *testing.T) {
	reconciler, err := New([]Mapping{
		{Provider: model.ProviderHuawei, ResourceID: "resource-1", ClusterID: "fallback"},
		{Provider: model.ProviderHuawei, AccountID: "owner", ResourceID: "resource-1", ClusterID: "cce-a", Tags: map[string]string{"cost-center": "platform", "team": "default"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	item := reconcileItem()
	item.Tags = map[string]string{"team": "application"}
	result, matched, err := reconciler.Apply(item)
	if err != nil {
		t.Fatal(err)
	}
	if !matched || result.ClusterID != "cce-a" || result.Tags["cost-center"] != "platform" || result.Tags["team"] != "application" {
		t.Fatalf("unexpected reconciliation: %+v", result)
	}
}

func TestReconcilerRejectsClusterConflict(t *testing.T) {
	reconciler, err := New([]Mapping{{Provider: model.ProviderHuawei, ResourceID: "resource-1", ClusterID: "expected"}})
	if err != nil {
		t.Fatal(err)
	}
	item := reconcileItem()
	item.ClusterID = "other"
	if _, _, err := reconciler.Apply(item); err == nil {
		t.Fatal("expected cluster conflict")
	}
}

func reconcileItem() model.CostLineItem {
	money := model.Money{Amount: model.MustDecimal("1"), Currency: model.CurrencyCNY}
	return model.CostLineItem{
		Provider: model.ProviderHuawei, PayerAccountID: "payer", OwnerAccountID: "owner",
		BillingPeriod: "2026-07", LineItemID: "line", Service: "ECS", ResourceID: "resource-1",
		ChargeModel: model.ChargeModelPayAsYouGo, UsageQuantity: model.MustDecimal("1"),
		ListCost: money, NetCost: money, AmortizedCost: money, ObservedAt: time.Now(),
	}
}
