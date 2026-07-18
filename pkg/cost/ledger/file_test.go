package ledger

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/gocrane/crane/pkg/cost/model"
)

func TestFileStoreUpsertCorrectionAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	store, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	item := testItem("line-1", "10")
	result, err := store.Upsert(context.Background(), []model.CostLineItem{item})
	if err != nil {
		t.Fatal(err)
	}
	if result.Inserted != 1 {
		t.Fatalf("unexpected insert result: %+v", result)
	}
	result, err = store.Upsert(context.Background(), []model.CostLineItem{item})
	if err != nil {
		t.Fatal(err)
	}
	if result.Unchanged != 1 {
		t.Fatalf("unexpected unchanged result: %+v", result)
	}
	item.NetCost.Amount = model.MustDecimal("8")
	item.AmortizedCost.Amount = model.MustDecimal("8")
	result, err = store.Upsert(context.Background(), []model.CostLineItem{item})
	if err != nil {
		t.Fatal(err)
	}
	if result.Updated != 1 {
		t.Fatalf("unexpected update result: %+v", result)
	}

	period := model.BillingPeriod{Start: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)}
	cursor := model.BillCursor{Period: period, Token: "next", Metadata: map[string]string{"page": "2"}}
	if err := store.SaveCursor(context.Background(), "aliyun:account", cursor); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	items, err := reopened.Query(context.Background(), Query{Provider: model.ProviderAliyun, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].NetCost.Amount != model.MustDecimal("8") {
		t.Fatalf("unexpected persisted items: %+v", items)
	}
	loaded, ok, err := reopened.LoadCursor(context.Background(), "aliyun:account")
	if err != nil || !ok || loaded.Token != "next" || loaded.Metadata["page"] != "2" {
		t.Fatalf("unexpected persisted cursor: %+v %t %v", loaded, ok, err)
	}
}

func TestFileStoreRejectsInvalidBatchAtomically(t *testing.T) {
	store, err := OpenFile(filepath.Join(t.TempDir(), "ledger.json"))
	if err != nil {
		t.Fatal(err)
	}
	valid := testItem("line-1", "10")
	invalid := testItem("line-2", "20")
	invalid.PayerAccountID = ""
	if _, err := store.Upsert(context.Background(), []model.CostLineItem{valid, invalid}); err == nil {
		t.Fatal("expected invalid batch to fail")
	}
	items, err := store.Query(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("invalid batch was partially stored: %+v", items)
	}
}

func TestSummarizeSeparatesCurrencyAndCluster(t *testing.T) {
	first := testItem("line-1", "10")
	first.ClusterID = "cluster-a"
	second := testItem("line-2", "2.5")
	second.ClusterID = "cluster-a"
	third := testItem("line-3", "3")
	third.ClusterID = "cluster-b"
	summaries, err := Summarize([]model.CostLineItem{first, second, third})
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 || summaries[0].AmortizedCost != model.MustDecimal("12.5") {
		t.Fatalf("unexpected summaries: %+v", summaries)
	}
}

func testItem(id, amount string) model.CostLineItem {
	now := time.Date(2026, 7, 17, 1, 0, 0, 0, time.UTC)
	money := model.Money{Amount: model.MustDecimal(amount), Currency: model.CurrencyCNY}
	return model.CostLineItem{
		Provider: model.ProviderAliyun, PayerAccountID: "account", BillingPeriod: "2026-07",
		LineItemID: id, Service: "ECS", Category: model.CostCategoryCompute,
		ChargeModel: model.ChargeModelPayAsYouGo, UsageQuantity: model.MustDecimal("1"),
		ListCost: money, NetCost: money, AmortizedCost: money, ObservedAt: now,
	}
}
