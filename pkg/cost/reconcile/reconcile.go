package reconcile

import (
	"fmt"
	"strings"

	"github.com/gocrane/crane/pkg/cost/model"
)

type Mapping struct {
	Provider   model.Provider    `json:"provider"`
	AccountID  string            `json:"accountId,omitempty"`
	ResourceID string            `json:"resourceId"`
	ClusterID  string            `json:"clusterId"`
	Tags       map[string]string `json:"tags,omitempty"`
}

func (m Mapping) Validate() error {
	if err := m.Provider.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(m.ResourceID) == "" {
		return fmt.Errorf("mapping resource id is required")
	}
	if strings.TrimSpace(m.ClusterID) == "" {
		return fmt.Errorf("mapping cluster id is required")
	}
	return nil
}

type Reconciler struct {
	mappings map[string]Mapping
}

func New(mappings []Mapping) (*Reconciler, error) {
	result := &Reconciler{mappings: make(map[string]Mapping, len(mappings))}
	for index, mapping := range mappings {
		if err := mapping.Validate(); err != nil {
			return nil, fmt.Errorf("mapping %d: %w", index, err)
		}
		key := mappingKey(mapping.Provider, mapping.AccountID, mapping.ResourceID)
		if _, exists := result.mappings[key]; exists {
			return nil, fmt.Errorf("duplicate resource mapping %s", key)
		}
		mapping.Tags = cloneMap(mapping.Tags)
		result.mappings[key] = mapping
	}
	return result, nil
}

// Apply enriches a normalized line item with its Kubernetes cluster and cost
// allocation tags. Account-specific mappings take precedence over provider-wide
// mappings. An existing ClusterID is never silently overwritten.
func (r *Reconciler) Apply(item model.CostLineItem) (model.CostLineItem, bool, error) {
	if err := item.Validate(); err != nil {
		return model.CostLineItem{}, false, err
	}
	if item.ResourceID == "" {
		return item, false, nil
	}
	mapping, found := r.mappings[mappingKey(item.Provider, item.OwnerAccountID, item.ResourceID)]
	if !found {
		mapping, found = r.mappings[mappingKey(item.Provider, item.PayerAccountID, item.ResourceID)]
	}
	if !found {
		mapping, found = r.mappings[mappingKey(item.Provider, "", item.ResourceID)]
	}
	if !found {
		return item, false, nil
	}
	if item.ClusterID != "" && item.ClusterID != mapping.ClusterID {
		return model.CostLineItem{}, false, fmt.Errorf("resource %s already belongs to cluster %s, mapping says %s",
			item.ResourceID, item.ClusterID, mapping.ClusterID)
	}
	item.ClusterID = mapping.ClusterID
	item.Tags = cloneMap(item.Tags)
	if item.Tags == nil {
		item.Tags = make(map[string]string)
	}
	for key, value := range mapping.Tags {
		if _, exists := item.Tags[key]; !exists {
			item.Tags[key] = value
		}
	}
	return item, true, nil
}

func mappingKey(provider model.Provider, accountID, resourceID string) string {
	return strings.Join([]string{string(provider), accountID, resourceID}, "\x00")
}

func cloneMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
