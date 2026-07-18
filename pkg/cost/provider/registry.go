package provider

import (
	"fmt"
	"sort"
	"sync"

	"github.com/gocrane/crane/pkg/cost/model"
)

type Registry struct {
	mu        sync.RWMutex
	factories map[model.Provider]Factory
}

func NewRegistry() *Registry {
	return &Registry{factories: make(map[model.Provider]Factory)}
}

func (r *Registry) Register(name model.Provider, factory Factory) error {
	if err := name.Validate(); err != nil {
		return err
	}
	if factory == nil {
		return fmt.Errorf("provider %s factory must not be nil", name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[name]; exists {
		return fmt.Errorf("provider %s is already registered", name)
	}
	r.factories[name] = factory
	return nil
}

func (r *Registry) New(name model.Provider, config Config) (Adapter, error) {
	r.mu.RLock()
	factory, exists := r.factories[name]
	r.mu.RUnlock()
	if !exists {
		return Adapter{}, fmt.Errorf("provider %s is not registered", name)
	}

	adapter, err := factory(config)
	if err != nil {
		return Adapter{}, fmt.Errorf("create provider %s: %w", name, err)
	}
	if adapter.Name != name {
		return Adapter{}, fmt.Errorf("provider factory %s returned adapter %s", name, adapter.Name)
	}
	if err := adapter.Validate(); err != nil {
		return Adapter{}, err
	}
	return adapter, nil
}

func (r *Registry) Providers() []model.Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	providers := make([]model.Provider, 0, len(r.factories))
	for name := range r.factories {
		providers = append(providers, name)
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i] < providers[j] })
	return providers
}
