package ratecard

import (
	"context"
	"testing"
	"time"

	"github.com/gocrane/crane/pkg/cost/model"
)

func TestCatalogSelectsMostSpecificRate(t *testing.T) {
	rates := []model.Rate{
		{
			Provider: model.ProviderAliyun, ResourceType: "ecs", Unit: "Hour",
			UnitPrice: model.Money{Amount: model.MustDecimal("1"), Currency: model.CurrencyCNY}, Source: "default",
		},
		{
			Provider: model.ProviderAliyun, Region: "cn-hangzhou", ResourceType: "ecs", InstanceType: "ecs.g8i.xlarge",
			ChargeModel: model.ChargeModelPayAsYouGo, Unit: "Hour",
			UnitPrice: model.Money{Amount: model.MustDecimal("2.5"), Currency: model.CurrencyCNY}, Source: "contract",
		},
	}
	catalog, err := New(model.ProviderAliyun, rates)
	if err != nil {
		t.Fatal(err)
	}
	got, err := catalog.GetRates(context.Background(), model.RateQuery{
		Provider: model.ProviderAliyun, Region: "cn-hangzhou", ResourceType: "ecs",
		InstanceType: "ecs.g8i.xlarge", ChargeModel: model.ChargeModelPayAsYouGo,
		EffectiveAt: time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Source != "contract" {
		t.Fatalf("unexpected rates %#v", got)
	}
}

func TestCatalogJSONKeepsExactMoney(t *testing.T) {
	catalog, err := FromJSON(model.ProviderTencent, []byte(`[
  {"resourceType":"cvm","chargeModel":"pay_as_you_go","unit":"Hour","unitPrice":{"amount":"1.2300","currency":"CNY"},"source":"contract"}
]`))
	if err != nil {
		t.Fatal(err)
	}
	rates, err := catalog.GetRates(context.Background(), model.RateQuery{Provider: model.ProviderTencent, ResourceType: "cvm", ChargeModel: model.ChargeModelPayAsYouGo})
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 || rates[0].UnitPrice.Amount != "1.23" {
		t.Fatalf("unexpected rates %#v", rates)
	}
}
