package ratecard

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gocrane/crane/pkg/cost/model"
)

const OptionJSON = "rate_card_json"

type Catalog struct {
	provider model.Provider
	rates    []model.Rate
}

func New(provider model.Provider, rates []model.Rate) (*Catalog, error) {
	if err := provider.Validate(); err != nil {
		return nil, err
	}
	copyRates := make([]model.Rate, len(rates))
	copy(copyRates, rates)
	for index := range copyRates {
		if copyRates[index].Provider == "" {
			copyRates[index].Provider = provider
		}
		if copyRates[index].Provider != provider {
			return nil, fmt.Errorf("rate %d belongs to provider %s, expected %s", index, copyRates[index].Provider, provider)
		}
		if err := copyRates[index].Validate(); err != nil {
			return nil, fmt.Errorf("rate %d: %w", index, err)
		}
	}
	return &Catalog{provider: provider, rates: copyRates}, nil
}

func FromJSON(provider model.Provider, data []byte) (*Catalog, error) {
	var rates []model.Rate
	if err := json.Unmarshal(data, &rates); err != nil {
		return nil, fmt.Errorf("decode %s rate card: %w", provider, err)
	}
	return New(provider, rates)
}

func FromOptions(provider model.Provider, options map[string]string) (*Catalog, bool, error) {
	data := strings.TrimSpace(options[OptionJSON])
	if data == "" {
		return nil, false, nil
	}
	catalog, err := FromJSON(provider, []byte(data))
	return catalog, true, err
}

func (c *Catalog) GetRates(_ context.Context, query model.RateQuery) ([]model.Rate, error) {
	if query.Provider == "" {
		query.Provider = c.provider
	}
	if query.Provider != c.provider {
		return nil, fmt.Errorf("rate query provider %s does not match catalog %s", query.Provider, c.provider)
	}
	effectiveAt := query.EffectiveAt
	if effectiveAt.IsZero() {
		effectiveAt = time.Now()
	}

	type match struct {
		rate  model.Rate
		score int
	}
	matches := make([]match, 0)
	for _, rate := range c.rates {
		score, ok := matchRate(rate, query, effectiveAt)
		if ok {
			matches = append(matches, match{rate: rate, score: score})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		if matches[i].rate.ValidFrom.Equal(matches[j].rate.ValidFrom) {
			return matches[i].rate.SKU < matches[j].rate.SKU
		}
		return matches[i].rate.ValidFrom.After(matches[j].rate.ValidFrom)
	})
	result := make([]model.Rate, len(matches))
	for index := range matches {
		result[index] = matches[index].rate
	}
	return result, nil
}

func matchRate(rate model.Rate, query model.RateQuery, at time.Time) (int, bool) {
	if !rate.ValidFrom.IsZero() && at.Before(rate.ValidFrom) {
		return 0, false
	}
	if !rate.ValidUntil.IsZero() && !at.Before(rate.ValidUntil) {
		return 0, false
	}
	fields := []struct {
		rate   string
		query  string
		weight int
	}{
		{rate.AccountID, query.AccountID, 32},
		{rate.Region, query.Region, 16},
		{rate.Zone, query.Zone, 8},
		{rate.ResourceType, query.ResourceType, 4},
		{rate.InstanceType, query.InstanceType, 2},
		{string(rate.ChargeModel), string(query.ChargeModel), 1},
	}
	score := 0
	for _, field := range fields {
		if field.rate == "" || field.query == "" {
			continue
		}
		if !strings.EqualFold(field.rate, field.query) {
			return 0, false
		}
		score += field.weight
	}
	return score, true
}
