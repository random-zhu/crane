package providers

import (
	"reflect"
	"testing"

	"github.com/gocrane/crane/pkg/cost/model"
	"github.com/gocrane/crane/pkg/cost/provider"
)

func TestRegisterAll(t *testing.T) {
	registry := provider.NewRegistry()
	if err := RegisterAll(registry); err != nil {
		t.Fatal(err)
	}
	want := []model.Provider{
		model.ProviderAliyun,
		model.ProviderHuawei,
		model.ProviderTencent,
		model.ProviderVolc,
	}
	if got := registry.Providers(); !reflect.DeepEqual(got, want) {
		t.Fatalf("providers = %#v, want %#v", got, want)
	}
}
