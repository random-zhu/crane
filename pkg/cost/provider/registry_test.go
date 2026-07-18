package provider

import (
	"context"
	"reflect"
	"testing"

	"github.com/gocrane/crane/pkg/cost/model"
)

type testResolver struct{}

func (testResolver) ResolveNode(context.Context, model.KubernetesNode) (model.CloudResourceIdentity, error) {
	return model.CloudResourceIdentity{}, nil
}

func TestRegistry(t *testing.T) {
	registry := NewRegistry()
	factory := func(Config) (Adapter, error) {
		return Adapter{
			Name:         model.ProviderAliyun,
			Capabilities: Capabilities{IdentityResolution: true},
			Identity:     testResolver{},
		}, nil
	}
	if err := registry.Register(model.ProviderAliyun, factory); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(model.ProviderAliyun, factory); err == nil {
		t.Fatal("expected duplicate registration to fail")
	}
	if _, err := registry.New(model.ProviderAliyun, Config{}); err != nil {
		t.Fatal(err)
	}
	if got := registry.Providers(); !reflect.DeepEqual(got, []model.Provider{model.ProviderAliyun}) {
		t.Fatalf("providers = %#v", got)
	}
}

func TestAdapterCapabilityValidation(t *testing.T) {
	adapter := Adapter{Name: model.ProviderTencent, Capabilities: Capabilities{BillAPI: true}}
	if err := adapter.Validate(); err == nil {
		t.Fatal("expected missing bill implementation to fail")
	}
}
