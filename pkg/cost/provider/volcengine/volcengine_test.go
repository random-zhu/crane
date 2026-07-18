package volcengine

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

func TestSignRequestDeterministic(t *testing.T) {
	now := time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)
	body := []byte(`{"BillPeriod":"2026-07"}`)
	request, _ := http.NewRequest(http.MethodPost, "https://open.volcengineapi.com/?Action=ListBillDetail&Version=2022-01-01", nil)
	SignRequest(request, body, "ak", "sk", "", "cn-north-1", "billing", now)
	authorization := request.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "HMAC-SHA256 Credential=ak/20260717/cn-north-1/billing/request") {
		t.Fatalf("unexpected authorization %q", authorization)
	}
	request2, _ := http.NewRequest(http.MethodPost, request.URL.String(), nil)
	SignRequest(request2, body, "ak", "sk", "", "cn-north-1", "billing", now)
	if request2.Header.Get("Authorization") != authorization {
		t.Fatal("volcengine signature is not deterministic")
	}
}

func TestBillSourceContract(t *testing.T) {
	now := time.Date(2026, 7, 17, 1, 2, 3, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("Action") != "ListAmortizedCostBillDetail" {
			t.Errorf("unexpected action %q", request.URL.Query().Get("Action"))
		}
		for _, header := range []string{"Authorization", "X-Date", "X-Content-Sha256"} {
			if request.Header.Get(header) == "" {
				t.Errorf("missing header %s", header)
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(writer, `{
 "ResponseMetadata":{"RequestId":"request-1"},
 "Result":{
  "Total":1,
  "List":[{
   "PayerID":"210001",
   "OwnerID":"210002",
   "BillID":"bill-1",
   "BillDetailID":"detail-1",
   "Product":"ECS",
   "BillingItem":"ecs.g3i.xlarge",
   "BillingMode":"2",
   "Region":"cn-beijing",
   "Zone":"cn-beijing-a",
   "InstanceNo":"i-yc123",
   "Usage":"4",
   "UsageUnit":"Hour",
   "OriginalCost":"20.0000",
   "DiscountedCost":"16.0000",
   "AmortizedCost":"16.0000",
   "Currency":"CNY",
   "AmortizedDay":"2026-07-01",
   "Tags":{"team":"platform"}
  }]
 }
}`)
	}))
	defer server.Close()

	source, err := NewBillSource(Options{
		Endpoint: server.URL,
		Region:   "cn-north-1",
		Credential: provider.StaticCredentials{Values: provider.Credentials{
			"access_key_id":     "ak",
			"secret_access_key": "sk",
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
	if item.ResourceID != "i-yc123" || item.NetCost.Amount != "16" || item.Category != model.CostCategoryCompute {
		t.Fatalf("unexpected item %#v", item)
	}
}
