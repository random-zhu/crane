package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gocrane/crane/pkg/cost/model"
)

func TestConfigDefaultsProviderCredentials(t *testing.T) {
	config := Config{
		DataFile:  filepath.Join(t.TempDir(), "ledger.json"),
		Providers: []ProviderConfig{{Name: model.ProviderTencent, AccountID: "payer"}},
	}
	config.setDefaults()
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	provider := config.Providers[0]
	if provider.CredentialEnvPrefix != "COST_TENCENT_" || len(provider.CredentialKeys) != 2 || len(provider.OptionalCredentialKeys) != 1 {
		t.Fatalf("unexpected provider defaults: %+v", provider)
	}
}

func TestCostAPIKeepsExactDecimalStrings(t *testing.T) {
	service := newTestService(t)
	amount := model.Money{Amount: model.MustDecimal("12.34000001"), Currency: model.CurrencyCNY}
	item := model.CostLineItem{
		Provider: model.ProviderAliyun, PayerAccountID: "payer", BillingPeriod: "2026-07",
		LineItemID: "line", Service: "ECS", ResourceID: "i-test", UsageQuantity: model.MustDecimal("1"),
		ListCost: amount, NetCost: amount, AmortizedCost: amount, ObservedAt: time.Now(),
	}
	if _, err := service.store.Upsert(context.Background(), []model.CostLineItem{item}); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/costs/summary?provider=aliyun&billing_period=2026-07", nil)
	response := httptest.NewRecorder()
	service.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		LineItems int    `json:"lineItems"`
		CostType  string `json:"costType"`
		Summaries []struct {
			AmortizedCost string `json:"amortizedCost"`
		} `json:"summaries"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.CostType != "actual_bill" || payload.LineItems != 1 || len(payload.Summaries) != 1 || payload.Summaries[0].AmortizedCost != "12.34000001" {
		t.Fatalf("unexpected summary response: %s", response.Body.String())
	}
}

func TestReadyRequiresSuccessfulCollection(t *testing.T) {
	service := newTestService(t)
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response := httptest.NewRecorder()
	service.routes().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unexpected readiness status %d", response.Code)
	}
}

func TestAllocateAPIRejectsNumericDecimal(t *testing.T) {
	service := newTestService(t)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/allocate", strings.NewReader(`{"item":{"usageQuantity":1},"targets":[],"scale":2}`))
	response := httptest.NewRecorder()
	service.routes().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("numeric financial decimal should be rejected, got %d", response.Code)
	}
}

func TestCostAPIReadsRotatingBearerToken(t *testing.T) {
	service := newTestService(t)
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("first-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service.config.APITokenFile = tokenFile

	request := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
	response := httptest.NewRecorder()
	service.routes().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("request without token returned %d", response.Code)
	}

	if err := os.WriteFile(tokenFile, []byte("second-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
	request.Header.Set("Authorization", "Bearer second-token")
	response = httptest.NewRecorder()
	service.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("request with rotated token returned %d: %s", response.Code, response.Body.String())
	}
}

func TestRateAPIIsExplicitlyEstimated(t *testing.T) {
	config := Config{
		ListenAddress: "127.0.0.1:0", DataFile: filepath.Join(t.TempDir(), "ledger.json"), PullInterval: "1h",
		LookbackDays: 7, MaxPages: 10,
		Providers: []ProviderConfig{{
			Name: model.ProviderAliyun, AccountID: "payer", Options: map[string]string{
				"rate_card_json": `[{"region":"cn-hangzhou","resourceType":"ecs","instanceType":"ecs.g8i.xlarge","chargeModel":"pay_as_you_go","unit":"hour","unitPrice":{"amount":"1.25","currency":"CNY"},"source":"contract"}]`,
			},
		}},
	}
	service, err := New(config, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/rates?provider=aliyun&account_id=payer&resource_type=ecs&region=cn-hangzhou", nil)
	response := httptest.NewRecorder()
	service.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("rate request returned %d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		CostType string       `json:"costType"`
		Rates    []model.Rate `json:"rates"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.CostType != "estimated_rate" || len(payload.Rates) != 1 || payload.Rates[0].UnitPrice.Amount != "1.25" {
		t.Fatalf("unexpected rate response: %s", response.Body.String())
	}
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	config := Config{
		ListenAddress: "127.0.0.1:0", DataFile: filepath.Join(t.TempDir(), "ledger.json"), PullInterval: "1h",
		LookbackDays: 7, MaxPages: 10,
		Providers: []ProviderConfig{{Name: model.ProviderAliyun, AccountID: "payer"}},
	}
	service, err := New(config, nil)
	if err != nil {
		t.Fatal(err)
	}
	return service
}
