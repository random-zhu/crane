package tencent

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

type BillSource struct {
	endpoint   *url.URL
	accountID  string
	region     string
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
		return model.BillPage{}, fmt.Errorf("tencent DescribeBillDetail cursor must stay within one billing month")
	}
	credentials, err := s.credential.Retrieve(ctx)
	if err != nil {
		return model.BillPage{}, fmt.Errorf("retrieve tencent credentials: %w", err)
	}
	secretID, secretKey := credentials["secret_id"], credentials["secret_key"]
	if secretID == "" || secretKey == "" {
		return model.BillPage{}, fmt.Errorf("tencent credentials require secret_id and secret_key")
	}

	payload := map[string]interface{}{
		"Offset":     cursor.Offset,
		"Limit":      s.pageSize,
		"Month":      cursor.Period.Start.Format("2006-01"),
		"PeriodType": "byUsedTime",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return model.BillPage{}, err
	}
	request, err := http.NewRequest(http.MethodPost, s.endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return model.BillPage{}, err
	}
	SignTC3(request, body, secretID, secretKey, s.region, "DescribeBillDetail", s.now().UTC())
	if token := credentials["token"]; token != "" {
		request.Header.Set("X-TC-Token", token)
	}

	var rawResponse jsonx.Object
	if err := httpx.DoJSON(ctx, s.client, request, &rawResponse); err != nil {
		return model.BillPage{}, err
	}
	return s.decode(rawResponse, cursor)
}

func (s *BillSource) decode(rawResponse jsonx.Object, cursor model.BillCursor) (model.BillPage, error) {
	response, ok, err := rawResponse.Object("Response")
	if err != nil || !ok {
		return model.BillPage{}, fmt.Errorf("decode tencent billing response: missing Response object: %w", err)
	}
	if apiError, found, err := response.Object("Error"); err != nil {
		return model.BillPage{}, err
	} else if found {
		return model.BillPage{}, fmt.Errorf("tencent billing API error %s: %s", apiError.String("Code"), apiError.String("Message"))
	}
	rawItems, _, err := response.Array("DetailSet")
	if err != nil {
		return model.BillPage{}, fmt.Errorf("decode tencent bill items: %w", err)
	}
	items := make([]model.CostLineItem, 0, len(rawItems))
	for index, raw := range rawItems {
		item, err := s.normalize(raw, cursor)
		if err != nil {
			return model.BillPage{}, fmt.Errorf("normalize tencent bill item %d: %w", index, err)
		}
		items = append(items, item)
	}
	total, err := response.Int64("Total")
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

func (s *BillSource) normalize(raw jsonx.Object, cursor model.BillCursor) (model.CostLineItem, error) {
	listCost, err := normalize.Decimal(raw, "TotalCost", "Cost", "OriginalCostWithRI")
	if err != nil {
		return model.CostLineItem{}, err
	}
	netCost, err := normalize.Decimal(raw, "RealTotalCost", "RealCost", "CashPayAmount")
	if err != nil {
		return model.CostLineItem{}, err
	}
	if netCost.IsZero() && !listCost.IsZero() {
		netCost = listCost
	}
	amortized, err := normalize.Decimal(raw, "AmortizedCost", "RealTotalCost", "RealCost")
	if err != nil {
		return model.CostLineItem{}, err
	}
	if amortized.IsZero() && !netCost.IsZero() {
		amortized = netCost
	}
	usage, err := normalize.Decimal(raw, "UsedAmount", "Usage", "DosageValue")
	if err != nil {
		return model.CostLineItem{}, err
	}
	usageStart, err := normalize.Time(raw, "FeeBeginTime", "UsedTime", "StartTime")
	if err != nil {
		return model.CostLineItem{}, err
	}
	usageEnd, err := normalize.Time(raw, "FeeEndTime", "EndTime")
	if err != nil {
		return model.CostLineItem{}, err
	}
	service := firstNonEmpty(raw.String("BusinessCodeName"), raw.String("ProductCodeName"), raw.String("BusinessCode"), "Tencent Cloud")
	currency := normalize.Currency(raw.String("Currency"))
	item := model.CostLineItem{
		Provider:       model.ProviderTencent,
		PayerAccountID: firstNonEmpty(raw.String("PayerUin", "PayerUIN"), s.accountID),
		OwnerAccountID: raw.String("OwnerUin", "OwnerUIN"),
		BillingPeriod:  cursor.Period.Start.Format("2006-01"),
		InvoiceID:      raw.String("BillId", "BillID"),
		LineItemID:     raw.String("BillDetailId", "BillDetailID", "OrderId"),
		Service:        service,
		Product:        raw.String("ProductCodeName", "ProductCode"),
		SKU:            raw.String("SubProductCodeName", "ComponentCodeName", "ComponentCode"),
		Category:       normalize.Category(service, raw.String("ProductCodeName")),
		Region:         raw.String("RegionName", "RegionId", "RegionID"),
		Zone:           raw.String("ZoneName", "ZoneId", "ZoneID"),
		ResourceID:     raw.String("ResourceId", "ResourceID"),
		ResourceName:   raw.String("ResourceName"),
		ChargeModel:    normalize.ChargeModel(raw.String("PayModeName", "PayMode")),
		UsageStart:     usageStart,
		UsageEnd:       usageEnd,
		UsageQuantity:  usage,
		UsageUnit:      raw.String("UsedAmountUnit", "UsageUnit"),
		ListCost:       model.Money{Amount: listCost, Currency: currency},
		NetCost:        model.Money{Amount: netCost, Currency: currency},
		AmortizedCost:  model.Money{Amount: amortized, Currency: currency},
		Tags:           raw.StringMap("Tags"),
		Raw:            compactRaw(raw),
		SourceVersion:  "Billing/2018-07-09/DescribeBillDetail",
		ObservedAt:     s.now().UTC(),
	}
	if item.LineItemID == "" {
		item.LineItemID = item.InvoiceID
	}
	if err := item.Validate(); err != nil {
		return model.CostLineItem{}, err
	}
	return item, nil
}

func compactRaw(raw jsonx.Object) map[string]string {
	keys := []string{"ActionTypeName", "ProjectName", "PriceInfo", "Formula", "Discount"}
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
