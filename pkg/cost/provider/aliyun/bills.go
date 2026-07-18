package aliyun

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gocrane/crane/pkg/cost/model"
	"github.com/gocrane/crane/pkg/cost/provider"
	"github.com/gocrane/crane/pkg/cost/provider/internal/httpx"
	"github.com/gocrane/crane/pkg/cost/provider/internal/jsonx"
	"github.com/gocrane/crane/pkg/cost/provider/internal/normalize"
)

type BillSource struct {
	endpoint   *url.URL
	accountID  string
	clusterID  string
	credential provider.CredentialProvider
	client     httpx.Doer
	now        func() time.Time
	nonce      func() string
	pageSize   int
}

func (s *BillSource) Pull(ctx context.Context, cursor model.BillCursor) (model.BillPage, error) {
	if err := cursor.Period.Validate(); err != nil {
		return model.BillPage{}, err
	}
	if cursor.Period.Start.Format("2006-01") != cursor.Period.End.Add(-time.Nanosecond).Format("2006-01") {
		return model.BillPage{}, fmt.Errorf("aliyun DescribeInstanceBill cursor must stay within one billing month")
	}
	credentials, err := s.credential.Retrieve(ctx)
	if err != nil {
		return model.BillPage{}, fmt.Errorf("retrieve aliyun credentials: %w", err)
	}
	accessKeyID := credentials["access_key_id"]
	accessKeySecret := credentials["access_key_secret"]
	if accessKeyID == "" || accessKeySecret == "" {
		return model.BillPage{}, fmt.Errorf("aliyun credentials require access_key_id and access_key_secret")
	}

	parameters := map[string]string{
		"Action":           "DescribeInstanceBill",
		"Version":          "2017-12-14",
		"Format":           "JSON",
		"AccessKeyId":      accessKeyID,
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureNonce":   s.nonce(),
		"SignatureVersion": "1.0",
		"Timestamp":        s.now().UTC().Format("2006-01-02T15:04:05Z"),
		"BillingCycle":     cursor.Period.Start.Format("2006-01"),
		"Granularity":      "MONTHLY",
		"MaxResults":       strconv.Itoa(s.pageSize),
	}
	if securityToken := credentials["security_token"]; securityToken != "" {
		parameters["SecurityToken"] = securityToken
	}
	if cursor.Token != "" {
		parameters["NextToken"] = cursor.Token
	}
	parameters["Signature"] = SignRPCParams(parameters, accessKeySecret)
	request, err := requestWithParams(s.endpoint, parameters)
	if err != nil {
		return model.BillPage{}, err
	}

	var response jsonx.Object
	if err := httpx.DoJSON(ctx, s.client, request, &response); err != nil {
		return model.BillPage{}, err
	}
	return s.decode(response, cursor)
}

func (s *BillSource) decode(response jsonx.Object, cursor model.BillCursor) (model.BillPage, error) {
	if code := response.String("Code"); code != "" && !strings.EqualFold(code, "Success") {
		return model.BillPage{}, fmt.Errorf("aliyun billing API error %s: %s", code, response.String("Message"))
	}
	data, ok, err := response.Object("Data")
	if err != nil {
		return model.BillPage{}, fmt.Errorf("decode aliyun billing data: %w", err)
	}
	if !ok {
		return model.BillPage{}, fmt.Errorf("aliyun billing response has no Data object")
	}
	rawItems, _, err := data.Array("Items", "Item")
	if err != nil {
		return model.BillPage{}, fmt.Errorf("decode aliyun bill items: %w", err)
	}
	items := make([]model.CostLineItem, 0, len(rawItems))
	for index, raw := range rawItems {
		item, err := s.normalize(raw, cursor)
		if err != nil {
			return model.BillPage{}, fmt.Errorf("normalize aliyun bill item %d: %w", index, err)
		}
		items = append(items, item)
	}

	nextToken := data.String("NextToken")
	page := model.BillPage{Items: items, RawCount: len(rawItems), Complete: nextToken == ""}
	if nextToken != "" {
		next := cursor
		next.Token = nextToken
		next.Offset += int64(len(rawItems))
		next.UpdatedAt = s.now().UTC()
		page.NextCursor = &next
	}
	return page, nil
}

func (s *BillSource) normalize(raw jsonx.Object, cursor model.BillCursor) (model.CostLineItem, error) {
	listCost, err := normalize.Decimal(raw, "PretaxGrossAmount", "OriginalAmount", "ListPrice", "InvoiceDiscount")
	if err != nil {
		return model.CostLineItem{}, err
	}
	netCost, err := normalize.Decimal(raw, "PretaxAmount", "PaymentAmount", "CashAmount")
	if err != nil {
		return model.CostLineItem{}, err
	}
	if netCost.IsZero() && !listCost.IsZero() {
		netCost = listCost
	}
	amortized, err := normalize.Decimal(raw, "AmortizedCost", "PretaxAmount", "PaymentAmount", "CashAmount")
	if err != nil {
		return model.CostLineItem{}, err
	}
	if amortized.IsZero() && !netCost.IsZero() {
		amortized = netCost
	}
	usage, err := normalize.Decimal(raw, "Usage", "UsageAmount", "UsageQuantity")
	if err != nil {
		return model.CostLineItem{}, err
	}
	usageStart, err := normalize.Time(raw, "UsageStartTime", "BillingDate", "UsageDate")
	if err != nil {
		return model.CostLineItem{}, err
	}
	usageEnd, err := normalize.Time(raw, "UsageEndTime")
	if err != nil {
		return model.CostLineItem{}, err
	}
	service := firstNonEmpty(raw.String("ProductName"), raw.String("ProductCode"), "Alibaba Cloud")
	currency := normalize.Currency(raw.String("Currency", "CurrencyUnit"))
	item := model.CostLineItem{
		Provider:       model.ProviderAliyun,
		PayerAccountID: firstNonEmpty(raw.String("BillAccountID", "PayerAccountId", "AccountID"), s.accountID),
		OwnerAccountID: raw.String("BillOwnerID", "OwnerAccountId"),
		BillingPeriod:  cursor.Period.Start.Format("2006-01"),
		InvoiceID:      raw.String("BillNumber", "BillNo"),
		LineItemID:     raw.String("LineItemId", "ItemId", "OrderId"),
		Service:        service,
		Product:        raw.String("ProductCode", "ProductName"),
		SKU:            raw.String("PipCode", "Item", "InstanceSpec"),
		Category:       normalize.Category(service, raw.String("ProductCode")),
		Region:         raw.String("Region", "RegionNo"),
		Zone:           raw.String("Zone", "ZoneName"),
		ResourceID:     raw.String("InstanceID", "InstanceId"),
		ResourceName:   raw.String("InstanceName", "NickName"),
		ChargeModel:    normalize.ChargeModel(raw.String("SubscriptionType", "BillingType")),
		UsageStart:     usageStart,
		UsageEnd:       usageEnd,
		UsageQuantity:  usage,
		UsageUnit:      raw.String("UsageUnit", "Unit"),
		ListCost:       model.Money{Amount: listCost, Currency: currency},
		NetCost:        model.Money{Amount: netCost, Currency: currency},
		AmortizedCost:  model.Money{Amount: amortized, Currency: currency},
		Tags:           raw.StringMap("Tags", "Tag"),
		Raw:            compactRaw(raw),
		SourceVersion:  "BSSOpenAPI/2017-12-14/DescribeInstanceBill",
		ObservedAt:     s.now().UTC(),
	}
	if err := item.Validate(); err != nil {
		return model.CostLineItem{}, err
	}
	return item, nil
}

func compactRaw(raw jsonx.Object) map[string]string {
	keys := []string{"BillingDate", "Item", "OrderId", "BillNumber", "DeductedByCoupons", "OutstandingAmount"}
	result := make(map[string]string)
	for _, key := range keys {
		if value := raw.String(key); value != "" {
			result[key] = value
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
