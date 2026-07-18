package aliyun

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

func TestSignRPCParamsDeterministic(t *testing.T) {
	parameters := map[string]string{
		"Action":           "DescribeInstanceBill",
		"Version":          "2017-12-14",
		"AccessKeyId":      "testid",
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureNonce":   "nonce",
		"SignatureVersion": "1.0",
		"Timestamp":        "2026-07-17T00:00:00Z",
		"BillingCycle":     "2026-07",
	}
	first := SignRPCParams(parameters, "secret")
	parameters["Signature"] = "ignored"
	if second := SignRPCParams(parameters, "secret"); second != first {
		t.Fatalf("signature is not deterministic: %q != %q", first, second)
	}
	if first == "" {
		t.Fatal("signature is empty")
	}
}

func TestBillSourceContract(t *testing.T) {
	now := time.Date(2026, 7, 17, 1, 2, 3, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		for _, key := range []string{"Action", "Version", "AccessKeyId", "Signature", "BillingCycle", "MaxResults"} {
			if query.Get(key) == "" {
				t.Errorf("missing query parameter %s", key)
			}
		}
		if query.Get("SecurityToken") != "sts-token" {
			t.Errorf("missing STS security token")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(writer, `{
  "Code":"Success",
  "Data":{
    "NextToken":"next-page",
    "Items":{"Item":[{
      "BillAccountID":"payer-1",
      "BillOwnerID":"owner-1",
      "BillingDate":"2026-07-01",
      "BillNumber":"bill-1",
      "LineItemId":"line-1",
      "ProductCode":"ecs",
      "ProductName":"Elastic Compute Service",
      "PipCode":"ecs.g8i.xlarge",
      "Region":"cn-hangzhou",
      "Zone":"cn-hangzhou-h",
      "InstanceID":"i-bp123",
      "SubscriptionType":"PayAsYouGo",
      "Usage":"2.5",
      "UsageUnit":"Hour",
      "PretaxGrossAmount":"10.0000",
      "PretaxAmount":"8.2500",
      "Currency":"CNY",
      "Tags":{"team":"platform"}
    }]}
  }
}`)
	}))
	defer server.Close()

	source, err := NewBillSource(Options{
		Endpoint:  server.URL,
		AccountID: "fallback-account",
		Credential: provider.StaticCredentials{Values: provider.Credentials{
			"access_key_id":     "testid",
			"access_key_secret": "testsecret",
			"security_token":    "sts-token",
		}},
		HTTPClient: server.Client(),
		Now:        func() time.Time { return now },
		Nonce:      func() string { return "fixed-nonce" },
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
	if len(page.Items) != 1 || page.Complete || page.NextCursor == nil || page.NextCursor.Token != "next-page" {
		t.Fatalf("unexpected page %#v", page)
	}
	item := page.Items[0]
	if item.Provider != model.ProviderAliyun || item.ResourceID != "i-bp123" || item.NetCost.Amount != "8.25" || item.Category != model.CostCategoryCompute {
		t.Fatalf("unexpected item %#v", item)
	}
	if item.IdempotencyKey() != "aliyun:payer-1:2026-07:line-1" {
		t.Fatalf("unexpected key %s", item.IdempotencyKey())
	}
}
