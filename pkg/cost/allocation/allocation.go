package allocation

import (
	"fmt"
	"strings"

	"github.com/gocrane/crane/pkg/cost/model"
)

type Target struct {
	ClusterID  string            `json:"clusterId,omitempty"`
	Namespace  string            `json:"namespace,omitempty"`
	Workload   string            `json:"workload,omitempty"`
	Pod        string            `json:"pod,omitempty"`
	CostCenter string            `json:"costCenter,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	Weight     model.Decimal     `json:"weight"`
}

func (t Target) Validate() error {
	if strings.TrimSpace(t.ClusterID+t.Namespace+t.Workload+t.Pod+t.CostCenter) == "" && len(t.Labels) == 0 {
		return fmt.Errorf("allocation target identity is required")
	}
	if err := t.Weight.Validate(); err != nil {
		return fmt.Errorf("allocation weight: %w", err)
	}
	if comparison, _ := t.Weight.Cmp(model.Zero); comparison < 0 {
		return fmt.Errorf("allocation weight must not be negative")
	}
	return nil
}

type Share struct {
	SourceKey string      `json:"sourceKey"`
	Target    Target      `json:"target"`
	Amount    model.Money `json:"amount"`
}

type Result struct {
	Source      model.Money `json:"source"`
	Allocated   model.Money `json:"allocated"`
	Unallocated model.Money `json:"unallocated"`
	Shares      []Share     `json:"shares"`
	Balanced    bool        `json:"balanced"`
}

// Allocate splits amortized cost by non-negative weights. Each share is
// rounded to scale decimal places and the final share receives the residual,
// guaranteeing source = allocated + unallocated for charges and refunds.
func Allocate(item model.CostLineItem, targets []Target, scale int) (Result, error) {
	if err := item.Validate(); err != nil {
		return Result{}, err
	}
	if scale < 0 {
		return Result{}, fmt.Errorf("allocation scale must not be negative")
	}
	result := Result{
		Source: item.AmortizedCost,
		Allocated: model.Money{
			Amount: model.Zero, Currency: item.AmortizedCost.Currency,
		},
		Unallocated: item.AmortizedCost,
		Balanced:    true,
	}
	if len(targets) == 0 {
		return result, nil
	}

	totalWeight := model.Zero
	positive := make([]Target, 0, len(targets))
	for index, target := range targets {
		if err := target.Validate(); err != nil {
			return Result{}, fmt.Errorf("target %d: %w", index, err)
		}
		var err error
		totalWeight, err = totalWeight.Add(target.Weight)
		if err != nil {
			return Result{}, err
		}
		if !target.Weight.IsZero() {
			positive = append(positive, target)
		}
	}
	if totalWeight.IsZero() {
		return result, nil
	}

	remaining := item.AmortizedCost.Amount
	for index, target := range positive {
		amount := remaining
		if index < len(positive)-1 {
			weighted, err := item.AmortizedCost.Amount.Mul(target.Weight)
			if err != nil {
				return Result{}, err
			}
			amount, err = weighted.Quo(totalWeight, scale)
			if err != nil {
				return Result{}, err
			}
		}
		var err error
		remaining, err = remaining.Sub(amount)
		if err != nil {
			return Result{}, err
		}
		result.Allocated.Amount, err = result.Allocated.Amount.Add(amount)
		if err != nil {
			return Result{}, err
		}
		result.Shares = append(result.Shares, Share{
			SourceKey: item.IdempotencyKey(),
			Target:    cloneTarget(target),
			Amount:    model.Money{Amount: amount, Currency: item.AmortizedCost.Currency},
		})
	}
	result.Unallocated.Amount = remaining
	combined, err := result.Allocated.Amount.Add(result.Unallocated.Amount)
	if err != nil {
		return Result{}, err
	}
	comparison, err := combined.Cmp(result.Source.Amount)
	if err != nil {
		return Result{}, err
	}
	result.Balanced = comparison == 0
	if !result.Balanced {
		return Result{}, fmt.Errorf("allocation is not balanced: source=%s allocated=%s unallocated=%s",
			result.Source.Amount, result.Allocated.Amount, result.Unallocated.Amount)
	}
	return result, nil
}

func cloneTarget(target Target) Target {
	if target.Labels == nil {
		return target
	}
	source := target.Labels
	target.Labels = make(map[string]string, len(target.Labels))
	for key, value := range source {
		target.Labels[key] = value
	}
	return target
}
