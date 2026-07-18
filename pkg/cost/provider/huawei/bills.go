package huawei

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gocrane/crane/pkg/cost/model"
	"github.com/gocrane/crane/pkg/cost/provider"
	"github.com/gocrane/crane/pkg/cost/provider/internal/httpx"
	"github.com/gocrane/crane/pkg/cost/provider/internal/jsonx"
	"github.com/gocrane/crane/pkg/cost/provider/internal/normalize"
)

const resourceRecordsPath = "/v2/bills/customer-bills/res-records/query"

type BillSource struct {
	endpoint   *url.URL
	accountID  string
	clusterID  string
	credential provider.CredentialProvider
	client     httpx.Doer
	now        func() time.Time
	pageSize   int
}

func (s *BillSource) Pull(ctx context.Context, cursor model.BillCursor) (model.BillPage, error) {
	if err := cursor.Period.Validate(); err != nil {
		return model.BillPage{}, err
	}
	if cursor.Period.Start.Format("2006-01") != cursor.Period.End.Add(-time.Nanosecond).Format("2006-01") {
		return model.BillPage{}, fmt.Errorf("huawei resource detail cursor must stay within one billing month")
	}
	credentials, err := s.credential.Retrieve(ctx)
	if err != nil {
		return model.BillPage{}, fmt.Errorf("retrieve huawei credentials: %w", err)
	}
	authToken := credentials["auth_token"]
	if authToken == "" {
		return model.BillPage{}, fmt.Errorf("huawei credentials require a rotating auth_token")
	}

	payload := map[string]interface{}{
		"cycle":               cursor.Period.Start.Format("2006-01"),
		"include_zero_record": true,
		"method":              "all",
		"offset":              cursor.Offset,
		"limit":               s.pageSize,
		"statistic_type":      3,
		"query_type":          "BILLCYCLE",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return model.BillPage{}, err
	}
	requestURL := s.endpoint.ResolveReference(&url.URL{Path: resourceRecordsPath})
	request, err := http.NewRequest(http.MethodPost, requestURL.String(), bytes.NewReader(body))
	if err != nil {
		return model.BillPage{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Auth-Token", authToken)
	request.Header.Set("X-Language", "zh_CN")

	var rawResponse jsonx.Object
	if err := httpx.DoJSON(ctx, s.client, request, &rawResponse); err != nil {
		return model.BillPage{}, err
	}
	return s.decode(rawResponse, cursor)
}

func (s *BillSource) decode(rawResponse jsonx.Object, cursor model.BillCursor) (model.BillPage, error) {
	if code := rawResponse.String("error_code", "errorCode"); code != "" {
		return model.BillPage{}, fmt.Errorf("huawei billing API error %s: %s", code, rawResponse.String("error_msg", "errorMsg"))
	}
	rawItems, _, err := rawResponse.Array("monthly_records", "monthlyRecords")
	if err != nil {
		return model.BillPage{}, fmt.Errorf("decode huawei bill items: %w", err)
	}
	currency := normalize.Currency(rawResponse.String("currency"))
	items := make([]model.CostLineItem, 0, len(rawItems))
	for index, raw := range rawItems {
		item, err := s.normalize(raw, cursor, currency)
		if err != nil {
			return model.BillPage{}, fmt.Errorf("normalize huawei bill item %d: %w", index, err)
		}
		items = append(items, item)
	}
	total, err := rawResponse.Int64("total_count", "totalCount")
	if err != nil {
		return model.BillPage{}, err
	}
	nextOffset := cursor.Offset + int64(len(rawItems))
	complete := len(rawItems) == 0 || (total > 0 && nextOffset >= total) || len(rawItems) < s.pageSize
	page := model.BillPage{Items: items, RawCount: len(rawItems), Complete: complete}
	if !complete {
		next := cursor
		next.Offset = nextOffset
		next.UpdatedAt = s.now().UTC()
		page.NextCursor = &next
	}
	return page, nil
}

func (s *BillSource) normalize(raw jsonx.Object, cursor model.BillCursor, currency model.Currency) (model.CostLineItem, error) {
	listCost, err := normalize.Decimal(raw, "official_amount", "officialAmount")
	if err != nil {
		return model.CostLineItem{}, err
	}
	netCost, err := normalize.Decimal(raw, "consume_amount", "consumeAmount")
	if err != nil {
		return model.CostLineItem{}, err
	}
	if listCost.IsZero() && !netCost.IsZero() {
		listCost = netCost
	}
	usage, err := normalize.Decimal(raw, "period_num", "periodNum")
	if err != nil {
		return model.CostLineItem{}, err
	}
	usageStart, err := normalize.Time(raw, "effective_time", "effectiveTime", "bill_date", "billDate")
	if err != nil {
		return model.CostLineItem{}, err
	}
	usageEnd, err := normalize.Time(raw, "expire_time", "expireTime")
	if err != nil {
		return model.CostLineItem{}, err
	}
	service := firstNonEmpty(raw.String("cloud_service_type_name", "cloudServiceTypeName"), raw.String("cloud_service_type", "cloudServiceType"), "Huawei Cloud")
	resourceType := raw.String("resource_type_name", "resourceTypeName", "resource_Type_code", "resource_type_code", "resourceTypeCode")
	item := model.CostLineItem{
		Provider:       model.ProviderHuawei,
		PayerAccountID: firstNonEmpty(raw.String("payer_account_id", "payerAccountId"), s.accountID),
		OwnerAccountID: raw.String("customer_id", "customerId"),
		BillingPeriod:  firstNonEmpty(raw.String("cycle"), cursor.Period.Start.Format("2006-01")),
		InvoiceID:      raw.String("trade_id", "tradeId"),
		LineItemID:     raw.String("id"),
		Service:        service,
		Product:        resourceType,
		SKU:            raw.String("sku_code", "skuCode", "product_spec_desc", "productSpecDesc"),
		Category:       normalize.Category(service, resourceType),
		Region:         raw.String("region"),
		Zone:           huaweiZone(raw),
		ResourceID:     raw.String("res_instance_id", "resInstanceId"),
		ResourceName:   raw.String("resource_name", "resourceName"),
		ChargeModel:    huaweiChargeModel(raw.String("charge_mode", "chargeMode")),
		UsageStart:     usageStart,
		UsageEnd:       usageEnd,
		UsageQuantity:  usage,
		UsageUnit:      huaweiPeriodUnit(raw.String("period_type", "periodType")),
		ListCost:       model.Money{Amount: listCost, Currency: currency},
		NetCost:        model.Money{Amount: netCost, Currency: currency},
		// The resource-details API is not an amortized-cost API. Preserve the
		// paid amount here and mark the source in Raw until the V4 cost API is
		// reconciled by the ledger service.
		AmortizedCost: model.Money{Amount: netCost, Currency: currency},
		Tags:          huaweiTags(raw.String("resource_tag", "resourceTag")),
		Raw:           compactRaw(raw),
		SourceVersion: "BSS/v2/ListCustomerselfResourceRecordDetails",
		ObservedAt:    s.now().UTC(),
	}
	if item.Raw == nil {
		item.Raw = make(map[string]string)
	}
	item.Raw["amortized_cost_source"] = "net_cost_fallback"
	if err := item.Validate(); err != nil {
		return model.CostLineItem{}, err
	}
	return item, nil
}

func huaweiChargeModel(value string) model.ChargeModel {
	switch strings.TrimSpace(value) {
	case "1":
		return model.ChargeModelSubscription
	case "3":
		return model.ChargeModelPayAsYouGo
	case "10", "11":
		return model.ChargeModelContract
	default:
		return model.ChargeModelUnknown
	}
}

func huaweiPeriodUnit(value string) string {
	switch strings.TrimSpace(value) {
	case "19":
		return "Year"
	case "20":
		return "Month"
	case "24":
		return "Day"
	case "25":
		return "Hour"
	case "5":
		return "OneTime"
	default:
		return ""
	}
}

func huaweiTags(value string) map[string]string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var tags map[string]string
	if err := json.Unmarshal([]byte(value), &tags); err == nil {
		return tags
	}
	return map[string]string{"raw": value}
}

func huaweiZone(raw jsonx.Object) string {
	if zone := raw.String("az_code", "azCode"); zone != "" {
		return zone
	}
	infos, found, err := raw.Array("az_code_infos", "azCodeInfos")
	if err == nil && found && len(infos) > 0 {
		return infos[0].String("az_code", "azCode")
	}
	return ""
}

func compactRaw(raw jsonx.Object) map[string]string {
	keys := []string{
		"bill_type", "enterprise_project_id", "enterprise_project_name", "consume_time",
		"cash_amount", "credit_amount", "coupon_amount", "adjustment_amount", "discount_amount",
	}
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
