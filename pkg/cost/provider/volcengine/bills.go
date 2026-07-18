package volcengine

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
		return model.BillPage{}, fmt.Errorf("volcengine billing cursor must stay within one billing month")
	}
	credentials, err := s.credential.Retrieve(ctx)
	if err != nil {
		return model.BillPage{}, fmt.Errorf("retrieve volcengine credentials: %w", err)
	}
	accessKeyID := credentials["access_key_id"]
	secretAccessKey := credentials["secret_access_key"]
	if accessKeyID == "" || secretAccessKey == "" {
		return model.BillPage{}, fmt.Errorf("volcengine credentials require access_key_id and secret_access_key")
	}

	payload := map[string]interface{}{
		"Offset":         cursor.Offset,
		"Limit":          s.pageSize,
		"BillPeriod":     cursor.Period.Start.Format("2006-01"),
		"AmortizedMonth": cursor.Period.Start.Format("2006-01"),
		"NeedRecordNum":  1,
		"IgnoreZero":     0,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return model.BillPage{}, err
	}
	requestURL := *s.endpoint
	query := requestURL.Query()
	query.Set("Action", "ListAmortizedCostBillDetail")
	query.Set("Version", "2022-01-01")
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequest(http.MethodPost, requestURL.String(), bytes.NewReader(body))
	if err != nil {
		return model.BillPage{}, err
	}
	SignRequest(request, body, accessKeyID, secretAccessKey, credentials["session_token"], s.region, serviceName, s.now())

	var rawResponse jsonx.Object
	if err := httpx.DoJSON(ctx, s.client, request, &rawResponse); err != nil {
		return model.BillPage{}, err
	}
	return s.decode(rawResponse, cursor)
}

func (s *BillSource) decode(rawResponse jsonx.Object, cursor model.BillCursor) (model.BillPage, error) {
	if metadata, found, err := rawResponse.Object("ResponseMetadata"); err != nil {
		return model.BillPage{}, err
	} else if found {
		if apiError, exists, err := metadata.Object("Error"); err != nil {
			return model.BillPage{}, err
		} else if exists && apiError.String("Code") != "" {
			return model.BillPage{}, fmt.Errorf("volcengine billing API error %s: %s", apiError.String("Code"), apiError.String("Message"))
		}
	}
	result, ok, err := rawResponse.Object("Result")
	if err != nil || !ok {
		return model.BillPage{}, fmt.Errorf("decode volcengine billing response: missing Result object: %w", err)
	}
	rawItems, _, err := result.Array("List", "Items", "Records")
	if err != nil {
		return model.BillPage{}, fmt.Errorf("decode volcengine bill items: %w", err)
	}
	items := make([]model.CostLineItem, 0, len(rawItems))
	for index, raw := range rawItems {
		item, err := s.normalize(raw, cursor)
		if err != nil {
			return model.BillPage{}, fmt.Errorf("normalize volcengine bill item %d: %w", index, err)
		}
		items = append(items, item)
	}
	total, err := result.Int64("Total", "RecordNum")
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
	listCost, err := normalize.Decimal(raw, "OriginalCost", "OriginalBillAmount", "PretaxGrossAmount", "ListPrice")
	if err != nil {
		return model.CostLineItem{}, err
	}
	netCost, err := normalize.Decimal(raw, "DiscountedCost", "DiscountBillAmount", "PretaxAmount", "PayableAmount")
	if err != nil {
		return model.CostLineItem{}, err
	}
	if netCost.IsZero() && !listCost.IsZero() {
		netCost = listCost
	}
	amortized, err := normalize.Decimal(raw, "AmortizedCost", "AmortizedAmount", "DiscountedCost", "DiscountBillAmount")
	if err != nil {
		return model.CostLineItem{}, err
	}
	if amortized.IsZero() && !netCost.IsZero() {
		amortized = netCost
	}
	usage, err := normalize.Decimal(raw, "Usage", "UsageAmount", "DosageValue")
	if err != nil {
		return model.CostLineItem{}, err
	}
	usageStart, err := normalize.Time(raw, "ExpenseDate", "AmortizedDay", "UsageStartTime")
	if err != nil {
		return model.CostLineItem{}, err
	}
	usageEnd, err := normalize.Time(raw, "UsageEndTime")
	if err != nil {
		return model.CostLineItem{}, err
	}
	service := firstNonEmpty(raw.String("Product"), raw.String("ProductName"), raw.String("BusinessCodeName"), "Volcengine")
	currency := normalize.Currency(raw.String("Currency"))
	item := model.CostLineItem{
		Provider:       model.ProviderVolc,
		PayerAccountID: firstNonEmpty(raw.String("PayerID", "PayerId"), s.accountID),
		OwnerAccountID: raw.String("OwnerID", "OwnerId"),
		BillingPeriod:  cursor.Period.Start.Format("2006-01"),
		InvoiceID:      raw.String("BillID", "BillId", "BillNo"),
		LineItemID:     raw.String("BillDetailID", "BillDetailId", "OrderNo"),
		Service:        service,
		Product:        raw.String("Product", "ProductName"),
		SKU:            raw.String("BillingItem", "InstanceType", "Configuration"),
		Category:       normalize.Category(service, raw.String("Product")),
		Region:         raw.String("Region", "RegionName"),
		Zone:           raw.String("Zone", "ZoneName"),
		ResourceID:     raw.String("InstanceNo", "InstanceID", "InstanceId"),
		ResourceName:   raw.String("InstanceName"),
		ChargeModel:    normalize.ChargeModel(raw.String("BillingMode", "BillingModeName")),
		UsageStart:     usageStart,
		UsageEnd:       usageEnd,
		UsageQuantity:  usage,
		UsageUnit:      raw.String("UsageUnit", "Unit"),
		ListCost:       model.Money{Amount: listCost, Currency: currency},
		NetCost:        model.Money{Amount: netCost, Currency: currency},
		AmortizedCost:  model.Money{Amount: amortized, Currency: currency},
		Tags:           raw.StringMap("Tags", "Tag"),
		Raw:            compactRaw(raw),
		SourceVersion:  "Billing/2022-01-01/ListAmortizedCostBillDetail",
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
	keys := []string{"BillCategory", "Project", "Discount", "CouponAmount", "AmortizedType"}
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
