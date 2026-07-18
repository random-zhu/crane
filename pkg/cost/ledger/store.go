package ledger

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/gocrane/crane/pkg/cost/model"
)

type UpsertResult struct {
	Inserted  int `json:"inserted"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
}

type Query struct {
	Provider      model.Provider
	PayerAccount  string
	ClusterID     string
	BillingPeriod string
	ResourceID    string
	Category      model.CostCategory
	Offset        int
	Limit         int
}

func (q Query) Validate() error {
	if q.Provider != "" {
		if err := q.Provider.Validate(); err != nil {
			return err
		}
	}
	if q.Offset < 0 {
		return fmt.Errorf("query offset must not be negative")
	}
	if q.Limit < 0 {
		return fmt.Errorf("query limit must not be negative")
	}
	return nil
}

type Store interface {
	Upsert(ctx context.Context, items []model.CostLineItem) (UpsertResult, error)
	Query(ctx context.Context, query Query) ([]model.CostLineItem, error)
	LoadCursor(ctx context.Context, key string) (model.BillCursor, bool, error)
	SaveCursor(ctx context.Context, key string, cursor model.BillCursor) error
}

func CursorKey(provider model.Provider, accountID string, period model.BillingPeriod) string {
	return strings.Join([]string{
		string(provider), accountID,
		period.Start.UTC().Format("20060102T150405Z"),
		period.End.UTC().Format("20060102T150405Z"),
	}, ":")
}

type SummaryKey struct {
	Provider     model.Provider `json:"provider"`
	PayerAccount string         `json:"payerAccountId"`
	ClusterID    string         `json:"clusterId,omitempty"`
	Currency     model.Currency `json:"currency"`
}

type Summary struct {
	Key           SummaryKey    `json:"key"`
	LineItems     int           `json:"lineItems"`
	ListCost      model.Decimal `json:"listCost"`
	NetCost       model.Decimal `json:"netCost"`
	AmortizedCost model.Decimal `json:"amortizedCost"`
}

func Summarize(items []model.CostLineItem) ([]Summary, error) {
	values := make(map[SummaryKey]*Summary)
	for _, item := range items {
		key := SummaryKey{
			Provider: item.Provider, PayerAccount: item.PayerAccountID,
			ClusterID: item.ClusterID, Currency: item.AmortizedCost.Currency,
		}
		entry, ok := values[key]
		if !ok {
			entry = &Summary{Key: key, ListCost: model.Zero, NetCost: model.Zero, AmortizedCost: model.Zero}
			values[key] = entry
		}
		var err error
		entry.ListCost, err = entry.ListCost.Add(item.ListCost.Amount)
		if err != nil {
			return nil, fmt.Errorf("sum list cost: %w", err)
		}
		entry.NetCost, err = entry.NetCost.Add(item.NetCost.Amount)
		if err != nil {
			return nil, fmt.Errorf("sum net cost: %w", err)
		}
		entry.AmortizedCost, err = entry.AmortizedCost.Add(item.AmortizedCost.Amount)
		if err != nil {
			return nil, fmt.Errorf("sum amortized cost: %w", err)
		}
		entry.LineItems++
	}

	result := make([]Summary, 0, len(values))
	for _, value := range values {
		result = append(result, *value)
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := result[i].Key, result[j].Key
		if left.Provider != right.Provider {
			return left.Provider < right.Provider
		}
		if left.PayerAccount != right.PayerAccount {
			return left.PayerAccount < right.PayerAccount
		}
		if left.ClusterID != right.ClusterID {
			return left.ClusterID < right.ClusterID
		}
		return left.Currency < right.Currency
	})
	return result, nil
}
