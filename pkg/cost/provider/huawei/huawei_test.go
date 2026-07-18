package huawei

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gocrane/crane/pkg/cost/model"
	"github.com/gocrane/crane/pkg/cost/provider"
)

func TestBillSourceContract(t *testing.T) {
	now := time.Date(2026, 7, 17, 1, 2, 3, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != resourceRecordsPath {
			t.Errorf("unexpected path %q", request.URL.Path)
		}
		if request.Header.Get("X-Auth-Token") != "token-1" {
			t.Errorf("missing auth token")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(writer, `{
 "total_count":1,
 "currency":"CNY",
 "monthly_records":[{
  "cycle":"2026-07",
  "bill_date":"2026-07-01",
  "customer_id":"owner-1",
  "payer_account_id":"payer-1",
  "region":"cn-north-4",
  "az_code":"cn-north-4a",
  "cloud_service_type":"hws.service.type.ec2",
  "cloud_service_type_name":"Elastic Cloud Server",
  "resource_type_code":"hws.resource.type.vm",
  "resource_type_name":"Cloud Server",
  "res_instance_id":"8f5b2f24-1234",
  "resource_name":"worker-1",
  "resource_tag":"{\"team\":\"platform\"}",
  "sku_code":"s7.large.2",
  "charge_mode":3,
  "consume_amount":"18.50000000",
  "official_amount":"20.00000000",
  "period_type":25,
  "period_num":"5",
  "trade_id":"trade-1",
  "id":"detail-1",
  "effective_time":"2026-07-01T00:00:00Z",
  "expire_time":"2026-07-01T05:00:00Z"
 }]
}`)
	}))
	defer server.Close()

	source, err := NewBillSource(Options{
		Endpoint: server.URL,
		Credential: provider.StaticCredentials{Values: provider.Credentials{
			"auth_token": "token-1",
		}},
		HTTPClient: server.Client(),
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := source.Pull(context.Background(), model.BillCursor{Period: model.BillingPeriod{
		Start: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || !page.Complete {
		t.Fatalf("unexpected page %#v", page)
	}
	item := page.Items[0]
	if item.ResourceID != "8f5b2f24-1234" || item.NetCost.Amount != "18.5" || item.Category != model.CostCategoryCompute || item.ChargeModel != model.ChargeModelPayAsYouGo {
		t.Fatalf("unexpected item %#v", item)
	}
	if item.Tags["team"] != "platform" || item.Raw["amortized_cost_source"] != "net_cost_fallback" {
		t.Fatalf("unexpected tags/raw %#v %#v", item.Tags, item.Raw)
	}
}
