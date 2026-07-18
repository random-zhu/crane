package tencent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gocrane/crane/pkg/cost/model"
	"github.com/gocrane/crane/pkg/cost/provider"
)

func TestSignTC3Deterministic(t *testing.T) {
	now := time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)
	body := []byte(`{"Month":"2026-07"}`)
	request, _ := http.NewRequest(http.MethodPost, "https://billing.tencentcloudapi.com/", nil)
	SignTC3(request, body, "id", "key", "ap-beijing", "DescribeBillDetail", now)
	authorization := request.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "TC3-HMAC-SHA256 Credential=id/2026-07-17/billing/tc3_request") {
		t.Fatalf("unexpected authorization %q", authorization)
	}
	request2, _ := http.NewRequest(http.MethodPost, "https://billing.tencentcloudapi.com/", nil)
	SignTC3(request2, body, "id", "key", "ap-beijing", "DescribeBillDetail", now)
	if request2.Header.Get("Authorization") != authorization {
		t.Fatal("TC3 signature is not deterministic")
	}
}

func TestBillSourceContract(t *testing.T) {
	now := time.Date(2026, 7, 17, 1, 2, 3, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		for _, header := range []string{"Authorization", "X-TC-Action", "X-TC-Version", "X-TC-Timestamp"} {
			if request.Header.Get(header) == "" {
				t.Errorf("missing header %s", header)
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(writer, `{
 "Response":{
  "RequestId":"request-1",
  "Total":1,
  "DetailSet":[{
   "PayerUin":"10001",
   "OwnerUin":"10002",
   "BillId":"bill-1",
   "BillDetailId":"detail-1",
   "BusinessCodeName":"Cloud Virtual Machine",
   "ProductCodeName":"CVM",
   "SubProductCodeName":"Standard S5",
   "PayModeName":"PayAsYouGo",
   "RegionName":"ap-beijing",
   "ZoneName":"ap-beijing-3",
   "ResourceId":"ins-abc",
   "ResourceName":"worker-1",
   "UsedAmount":"3.5",
   "UsedAmountUnit":"Hour",
   "TotalCost":"12.0000",
   "RealTotalCost":"9.6000",
   "Currency":"CNY",
   "FeeBeginTime":"2026-07-01 00:00:00",
   "FeeEndTime":"2026-07-01 03:30:00",
   "Tags":{"team":"platform"}
  }]
 }
}`)
	}))
	defer server.Close()

	source, err := NewBillSource(Options{
		Endpoint: server.URL,
		Region:   "ap-beijing",
		Credential: provider.StaticCredentials{Values: provider.Credentials{
			"secret_id":  "id",
			"secret_key": "key",
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
	if item.ResourceID != "ins-abc" || item.NetCost.Amount != "9.6" || item.Category != model.CostCategoryCompute {
		t.Fatalf("unexpected item %#v", item)
	}
}
