package identity

import (
	"context"
	"testing"

	"github.com/gocrane/crane/pkg/cost/model"
)

func TestResolver(t *testing.T) {
	resolver := Resolver{
		Provider:     model.ProviderTencent,
		Schemes:      []string{"qcloud"},
		ResourceType: "cvm",
		ClusterID:    "cls-1",
		IDPrefixes:   []string{"ins-"},
		PathHasZone:  true,
	}
	identity, err := resolver.ResolveNode(context.Background(), model.KubernetesNode{
		Name:       "node-1",
		ProviderID: "qcloud:///ap-beijing-3/ins-abc",
		Labels: map[string]string{
			"topology.kubernetes.io/region": "ap-beijing",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if identity.ResourceID != "ins-abc" || identity.Zone != "ap-beijing-3" || identity.Region != "ap-beijing" {
		t.Fatalf("unexpected identity %#v", identity)
	}
}

func TestDotRegionResolver(t *testing.T) {
	resolver := Resolver{
		Provider:     model.ProviderAliyun,
		Schemes:      []string{"alicloud"},
		ResourceType: "ecs",
		IDPrefixes:   []string{"i-"},
		DotRegionID:  true,
	}
	identity, err := resolver.ResolveNode(context.Background(), model.KubernetesNode{
		Name:       "node-1",
		ProviderID: "alicloud://cn-hangzhou.i-bp123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if identity.ResourceID != "i-bp123" || identity.Region != "cn-hangzhou" {
		t.Fatalf("unexpected identity %#v", identity)
	}
}
