package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Provider string

const (
	ProviderAliyun   Provider = "aliyun"
	ProviderVolc     Provider = "volcengine"
	ProviderHuawei   Provider = "huawei"
	ProviderTencent  Provider = "tencent"
	ProviderContract Provider = "contract"
)

func (p Provider) Validate() error {
	switch p {
	case ProviderAliyun, ProviderVolc, ProviderHuawei, ProviderTencent, ProviderContract:
		return nil
	default:
		return fmt.Errorf("unsupported cloud provider %q", p)
	}
}

type Currency string

const (
	CurrencyCNY Currency = "CNY"
	CurrencyUSD Currency = "USD"
)

func (c Currency) Validate() error {
	if len(c) != 3 || strings.ToUpper(string(c)) != string(c) {
		return fmt.Errorf("currency %q must be an ISO-4217 uppercase code", c)
	}
	return nil
}

type ChargeModel string

const (
	ChargeModelPayAsYouGo   ChargeModel = "pay_as_you_go"
	ChargeModelSubscription ChargeModel = "subscription"
	ChargeModelSpot         ChargeModel = "spot"
	ChargeModelContract     ChargeModel = "contract"
	ChargeModelUnknown      ChargeModel = "unknown"
)

type CostCategory string

const (
	CostCategoryCompute       CostCategory = "compute"
	CostCategoryStorage       CostCategory = "storage"
	CostCategoryNetwork       CostCategory = "network"
	CostCategoryControlPlane  CostCategory = "control_plane"
	CostCategoryObservability CostCategory = "observability"
	CostCategoryDatabase      CostCategory = "database"
	CostCategoryOther         CostCategory = "other"
)

// Money is an exact amount in its source currency.
type Money struct {
	Amount   Decimal  `json:"amount"`
	Currency Currency `json:"currency"`
}

func (m Money) Validate() error {
	if err := m.Amount.Validate(); err != nil {
		return fmt.Errorf("amount: %w", err)
	}
	if err := m.Currency.Validate(); err != nil {
		return err
	}
	return nil
}

// KubernetesNode is the provider-neutral subset needed to map a Kubernetes
// Node to a billable cloud resource. It intentionally avoids importing the
// Kubernetes API into the cost core.
type KubernetesNode struct {
	Name         string            `json:"name"`
	ProviderID   string            `json:"providerId"`
	Region       string            `json:"region,omitempty"`
	Zone         string            `json:"zone,omitempty"`
	InstanceType string            `json:"instanceType,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
}

type CloudResourceIdentity struct {
	Provider     Provider          `json:"provider"`
	AccountID    string            `json:"accountId,omitempty"`
	Region       string            `json:"region,omitempty"`
	Zone         string            `json:"zone,omitempty"`
	ResourceID   string            `json:"resourceId"`
	ResourceType string            `json:"resourceType"`
	ClusterID    string            `json:"clusterId,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

func (i CloudResourceIdentity) Validate() error {
	if err := i.Provider.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(i.ResourceID) == "" {
		return fmt.Errorf("resource id is required")
	}
	if strings.TrimSpace(i.ResourceType) == "" {
		return fmt.Errorf("resource type is required")
	}
	return nil
}

type RateQuery struct {
	Provider     Provider    `json:"provider"`
	AccountID    string      `json:"accountId,omitempty"`
	Region       string      `json:"region"`
	Zone         string      `json:"zone,omitempty"`
	ResourceType string      `json:"resourceType"`
	InstanceType string      `json:"instanceType,omitempty"`
	ChargeModel  ChargeModel `json:"chargeModel"`
	EffectiveAt  time.Time   `json:"effectiveAt"`
}

type Rate struct {
	Provider     Provider          `json:"provider"`
	AccountID    string            `json:"accountId,omitempty"`
	Region       string            `json:"region"`
	Zone         string            `json:"zone,omitempty"`
	ResourceType string            `json:"resourceType"`
	InstanceType string            `json:"instanceType,omitempty"`
	SKU          string            `json:"sku,omitempty"`
	ChargeModel  ChargeModel       `json:"chargeModel"`
	Unit         string            `json:"unit"`
	UnitPrice    Money             `json:"unitPrice"`
	ValidFrom    time.Time         `json:"validFrom,omitempty"`
	ValidUntil   time.Time         `json:"validUntil,omitempty"`
	Source       string            `json:"source"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

func (r Rate) Validate() error {
	if err := r.Provider.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.ResourceType) == "" {
		return fmt.Errorf("rate resource type is required")
	}
	if strings.TrimSpace(r.Unit) == "" {
		return fmt.Errorf("rate unit is required")
	}
	if err := r.UnitPrice.Validate(); err != nil {
		return fmt.Errorf("unit price: %w", err)
	}
	if !r.ValidUntil.IsZero() && !r.ValidFrom.IsZero() && r.ValidUntil.Before(r.ValidFrom) {
		return fmt.Errorf("rate validUntil must not precede validFrom")
	}
	return nil
}

type BillingPeriod struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

func (p BillingPeriod) Validate() error {
	if p.Start.IsZero() || p.End.IsZero() {
		return fmt.Errorf("billing period start and end are required")
	}
	if !p.End.After(p.Start) {
		return fmt.Errorf("billing period end must be after start")
	}
	return nil
}

type BillCursor struct {
	Period    BillingPeriod     `json:"period"`
	Token     string            `json:"token,omitempty"`
	Offset    int64             `json:"offset,omitempty"`
	UpdatedAt time.Time         `json:"updatedAt,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type BillPage struct {
	Items      []CostLineItem `json:"items"`
	NextCursor *BillCursor    `json:"nextCursor,omitempty"`
	RawCount   int            `json:"rawCount"`
	Complete   bool           `json:"complete"`
}

type CostLineItem struct {
	Provider       Provider          `json:"provider"`
	PayerAccountID string            `json:"payerAccountId"`
	OwnerAccountID string            `json:"ownerAccountId,omitempty"`
	BillingPeriod  string            `json:"billingPeriod"`
	InvoiceID      string            `json:"invoiceId,omitempty"`
	LineItemID     string            `json:"lineItemId,omitempty"`
	Service        string            `json:"service"`
	Product        string            `json:"product,omitempty"`
	SKU            string            `json:"sku,omitempty"`
	Category       CostCategory      `json:"category"`
	Region         string            `json:"region,omitempty"`
	Zone           string            `json:"zone,omitempty"`
	ResourceID     string            `json:"resourceId,omitempty"`
	ResourceName   string            `json:"resourceName,omitempty"`
	ClusterID      string            `json:"clusterId,omitempty"`
	ChargeModel    ChargeModel       `json:"chargeModel"`
	UsageStart     time.Time         `json:"usageStart,omitempty"`
	UsageEnd       time.Time         `json:"usageEnd,omitempty"`
	UsageQuantity  Decimal           `json:"usageQuantity"`
	UsageUnit      string            `json:"usageUnit,omitempty"`
	ListCost       Money             `json:"listCost"`
	NetCost        Money             `json:"netCost"`
	AmortizedCost  Money             `json:"amortizedCost"`
	Tags           map[string]string `json:"tags,omitempty"`
	Raw            map[string]string `json:"raw,omitempty"`
	SourceVersion  string            `json:"sourceVersion,omitempty"`
	ObservedAt     time.Time         `json:"observedAt"`
}

func (i CostLineItem) Validate() error {
	if err := i.Provider.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(i.PayerAccountID) == "" {
		return fmt.Errorf("payer account id is required")
	}
	if strings.TrimSpace(i.BillingPeriod) == "" {
		return fmt.Errorf("billing period is required")
	}
	if strings.TrimSpace(i.Service) == "" {
		return fmt.Errorf("service is required")
	}
	if err := i.UsageQuantity.Validate(); err != nil {
		return fmt.Errorf("usage quantity: %w", err)
	}
	for name, money := range map[string]Money{
		"listCost": i.ListCost, "netCost": i.NetCost, "amortizedCost": i.AmortizedCost,
	} {
		if err := money.Validate(); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if i.ListCost.Currency != i.NetCost.Currency || i.ListCost.Currency != i.AmortizedCost.Currency {
		return fmt.Errorf("all line item costs must use the same source currency")
	}
	if !i.UsageEnd.IsZero() && !i.UsageStart.IsZero() && i.UsageEnd.Before(i.UsageStart) {
		return fmt.Errorf("usage end must not precede usage start")
	}
	if i.ObservedAt.IsZero() {
		return fmt.Errorf("observedAt is required")
	}
	return nil
}

// IdempotencyKey is stable across a bill backfill. A provider line-item ID is
// preferred. When a provider does not expose one, the key is a hash of stable
// billing identity fields and sorted tags. Mutable amounts are intentionally
// excluded so a delayed correction replaces the earlier record.
func (i CostLineItem) IdempotencyKey() string {
	if i.LineItemID != "" {
		return strings.Join([]string{string(i.Provider), i.PayerAccountID, i.BillingPeriod, i.LineItemID}, ":")
	}

	parts := []string{
		string(i.Provider), i.PayerAccountID, i.OwnerAccountID, i.BillingPeriod,
		i.InvoiceID, i.Service, i.Product, i.SKU, i.Region, i.Zone,
		i.ResourceID, i.ChargeModel.String(), i.UsageStart.UTC().Format(time.RFC3339Nano),
		i.UsageEnd.UTC().Format(time.RFC3339Nano), i.UsageUnit,
	}
	keys := make([]string, 0, len(i.Tags))
	for key := range i.Tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, key+"="+i.Tags[key])
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return string(i.Provider) + ":sha256:" + hex.EncodeToString(sum[:])
}

func (m ChargeModel) String() string { return string(m) }
