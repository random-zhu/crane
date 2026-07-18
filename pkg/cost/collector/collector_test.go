package collector

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/gocrane/crane/pkg/cost/ledger"
	"github.com/gocrane/crane/pkg/cost/model"
)

type fakeBillSource struct {
	amount model.Decimal
	calls  []model.BillCursor
}

func (f *fakeBillSource) Pull(_ context.Context, cursor model.BillCursor) (model.BillPage, error) {
	f.calls = append(f.calls, cursor)
	if cursor.Offset == 0 {
		next := cursor
		next.Offset = 1
		return model.BillPage{Items: []model.CostLineItem{collectorItem("one", f.amount)}, RawCount: 1, NextCursor: &next}, nil
	}
	return model.BillPage{Items: []model.CostLineItem{collectorItem("two", f.amount)}, RawCount: 1, Complete: true}, nil
}

func TestCollectorPersistsCursorAndReplaysCorrections(t *testing.T) {
	store, err := ledger.OpenFile(filepath.Join(t.TempDir(), "ledger.json"))
	if err != nil {
		t.Fatal(err)
	}
	collector, err := New(store, 10)
	if err != nil {
		t.Fatal(err)
	}
	period := model.BillingPeriod{Start: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)}
	source := &fakeBillSource{amount: model.MustDecimal("10")}
	target := Target{Provider: model.ProviderAliyun, AccountID: "payer", Bills: source}
	result, err := collector.PullMonth(context.Background(), target, period)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || result.Pages != 2 || result.Inserted != 2 {
		t.Fatalf("unexpected first collection: %+v", result)
	}

	source.amount = model.MustDecimal("8")
	result, err = collector.PullMonth(context.Background(), target, period)
	if err != nil {
		t.Fatal(err)
	}
	if len(source.calls) != 4 || source.calls[2].Offset != 0 || result.Updated != 2 {
		t.Fatalf("completed collection did not replay from page one: calls=%+v result=%+v", source.calls, result)
	}
}

func TestBillingMonthsCrossesBoundary(t *testing.T) {
	periods := BillingMonths(time.Date(2026, 7, 3, 12, 0, 0, 0, time.FixedZone("CST", 8*60*60)), 7)
	if len(periods) != 2 || periods[0].Start.Format("2006-01") != "2026-06" || periods[1].Start.Format("2006-01") != "2026-07" {
		t.Fatalf("unexpected billing months: %+v", periods)
	}
}

func TestCollectorRejectsPartialMonthAndPageLimit(t *testing.T) {
	store, err := ledger.OpenFile(filepath.Join(t.TempDir(), "ledger.json"))
	if err != nil {
		t.Fatal(err)
	}
	collector, err := New(store, 1)
	if err != nil {
		t.Fatal(err)
	}
	source := &fakeBillSource{amount: model.MustDecimal("10")}
	target := Target{Provider: model.ProviderAliyun, AccountID: "payer", Bills: source}
	partial := model.BillingPeriod{
		Start: time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}
	if _, err := collector.PullMonth(context.Background(), target, partial); err == nil {
		t.Fatal("partial billing month was accepted")
	}
	period := model.BillingPeriod{
		Start: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}
	result, err := collector.PullMonth(context.Background(), target, period)
	if err == nil || result.Complete || result.Pages != 1 {
		t.Fatalf("page limit did not fail closed: result=%+v err=%v", result, err)
	}
}

func collectorItem(id string, amount model.Decimal) model.CostLineItem {
	money := model.Money{Amount: amount, Currency: model.CurrencyCNY}
	return model.CostLineItem{
		Provider: model.ProviderAliyun, PayerAccountID: "payer", BillingPeriod: "2026-07",
		LineItemID: id, Service: "ECS", UsageQuantity: model.MustDecimal("1"),
		ListCost: money, NetCost: money, AmortizedCost: money, ObservedAt: time.Now(),
	}
}
