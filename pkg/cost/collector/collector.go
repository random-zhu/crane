package collector

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/gocrane/crane/pkg/cost/ledger"
	"github.com/gocrane/crane/pkg/cost/model"
	"github.com/gocrane/crane/pkg/cost/provider"
	"github.com/gocrane/crane/pkg/cost/reconcile"
)

const completeMetadataKey = "complete"

type Target struct {
	Provider   model.Provider
	AccountID  string
	Bills      provider.BillSource
	Reconciler *reconcile.Reconciler
}

func (t Target) Validate() error {
	if err := t.Provider.Validate(); err != nil {
		return err
	}
	if t.AccountID == "" {
		return fmt.Errorf("collector account id is required")
	}
	if t.Bills == nil {
		return fmt.Errorf("collector bill source is required")
	}
	return nil
}

type Result struct {
	Provider    model.Provider      `json:"provider"`
	AccountID   string              `json:"accountId"`
	Period      model.BillingPeriod `json:"period"`
	Pages       int                 `json:"pages"`
	RawItems    int                 `json:"rawItems"`
	Reconciled  int                 `json:"reconciled"`
	Inserted    int                 `json:"inserted"`
	Updated     int                 `json:"updated"`
	Unchanged   int                 `json:"unchanged"`
	Complete    bool                `json:"complete"`
	CompletedAt time.Time           `json:"completedAt,omitempty"`
}

type Collector struct {
	store    ledger.Store
	now      func() time.Time
	maxPages int
}

func New(store ledger.Store, maxPages int) (*Collector, error) {
	if store == nil {
		return nil, fmt.Errorf("collector ledger store is required")
	}
	if maxPages <= 0 {
		maxPages = 1000
	}
	return &Collector{store: store, now: time.Now, maxPages: maxPages}, nil
}

// PullMonth is an at-least-once ingestion loop. A cursor is persisted only
// after its page has been committed. Completed months start from page one on
// the next run so delayed cloud-bill corrections replace prior records by
// idempotency key.
func (c *Collector) PullMonth(ctx context.Context, target Target, period model.BillingPeriod) (Result, error) {
	if err := target.Validate(); err != nil {
		return Result{}, err
	}
	if err := period.Validate(); err != nil {
		return Result{}, err
	}
	monthStart := time.Date(period.Start.Year(), period.Start.Month(), 1, 0, 0, 0, 0, period.Start.Location())
	if !period.Start.Equal(monthStart) || !period.End.Equal(monthStart.AddDate(0, 1, 0)) {
		return Result{}, fmt.Errorf("collector period must be exactly one billing month")
	}
	result := Result{Provider: target.Provider, AccountID: target.AccountID, Period: period}
	cursorKey := ledger.CursorKey(target.Provider, target.AccountID, period)
	cursor := model.BillCursor{Period: period}
	if saved, ok, err := c.store.LoadCursor(ctx, cursorKey); err != nil {
		return result, err
	} else if ok && saved.Metadata[completeMetadataKey] != "true" {
		cursor = saved
	}

	seen := make(map[string]struct{})
	for result.Pages < c.maxPages {
		fingerprint := cursor.Token + ":" + strconv.FormatInt(cursor.Offset, 10)
		if _, duplicate := seen[fingerprint]; duplicate {
			return result, fmt.Errorf("provider %s returned a non-progressing bill cursor %s", target.Provider, fingerprint)
		}
		seen[fingerprint] = struct{}{}

		page, err := target.Bills.Pull(ctx, cursor)
		if err != nil {
			return result, fmt.Errorf("pull %s bills for %s: %w", target.Provider, period.Start.Format("2006-01"), err)
		}
		result.Pages++
		result.RawItems += page.RawCount
		items := make([]model.CostLineItem, 0, len(page.Items))
		for index, item := range page.Items {
			if item.Provider != target.Provider {
				return result, fmt.Errorf("bill item %d provider %s does not match target %s", index, item.Provider, target.Provider)
			}
			if target.Reconciler != nil {
				var matched bool
				item, matched, err = target.Reconciler.Apply(item)
				if err != nil {
					return result, fmt.Errorf("reconcile bill item %d: %w", index, err)
				}
				if matched {
					result.Reconciled++
				}
			}
			items = append(items, item)
		}
		upserted, err := c.store.Upsert(ctx, items)
		if err != nil {
			return result, fmt.Errorf("commit %s bill page: %w", target.Provider, err)
		}
		result.Inserted += upserted.Inserted
		result.Updated += upserted.Updated
		result.Unchanged += upserted.Unchanged

		if page.Complete {
			completed := cursor
			completed.Token = ""
			completed.Offset = 0
			completed.UpdatedAt = c.now().UTC()
			completed.Metadata = map[string]string{completeMetadataKey: "true"}
			if err := c.store.SaveCursor(ctx, cursorKey, completed); err != nil {
				return result, fmt.Errorf("save completed bill cursor: %w", err)
			}
			result.Complete = true
			result.CompletedAt = completed.UpdatedAt
			return result, nil
		}
		if page.NextCursor == nil {
			return result, fmt.Errorf("provider %s returned an incomplete page without a next cursor", target.Provider)
		}
		if page.NextCursor.Period != period {
			return result, fmt.Errorf("provider %s changed the billing period in its next cursor", target.Provider)
		}
		if err := c.store.SaveCursor(ctx, cursorKey, *page.NextCursor); err != nil {
			return result, fmt.Errorf("save bill cursor: %w", err)
		}
		cursor = *page.NextCursor
	}
	return result, fmt.Errorf("provider %s exceeded the %d-page safety limit for %s", target.Provider, c.maxPages, period.Start.Format("2006-01"))
}

// BillingMonths returns complete calendar-month cursors covering the lookback
// window. Cloud billing APIs used by the built-in adapters are month-based.
func BillingMonths(now time.Time, lookbackDays int) []model.BillingPeriod {
	if lookbackDays <= 0 {
		lookbackDays = 7
	}
	now = now.UTC()
	from := now.AddDate(0, 0, -lookbackDays)
	month := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC)
	last := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	periods := make([]model.BillingPeriod, 0, 2)
	for !month.After(last) {
		periods = append(periods, model.BillingPeriod{Start: month, End: month.AddDate(0, 1, 0)})
		month = month.AddDate(0, 1, 0)
	}
	return periods
}
