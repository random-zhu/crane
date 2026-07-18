package identity

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/gocrane/crane/pkg/cost/model"
)

type Resolver struct {
	Provider     model.Provider
	Schemes      []string
	ResourceType string
	ClusterID    string
	AccountID    string
	IDPrefixes   []string
	DotRegionID  bool
	PathHasZone  bool
}

func (r Resolver) ResolveNode(_ context.Context, node model.KubernetesNode) (model.CloudResourceIdentity, error) {
	providerID := strings.TrimSpace(node.ProviderID)
	if providerID == "" {
		return model.CloudResourceIdentity{}, fmt.Errorf("node %s has no providerID", node.Name)
	}

	resourceID, region, zone, err := r.parse(providerID)
	if err != nil {
		return model.CloudResourceIdentity{}, err
	}
	if region == "" {
		region = firstNonEmpty(node.Region, node.Labels["topology.kubernetes.io/region"], node.Labels["failure-domain.beta.kubernetes.io/region"])
	}
	if zone == "" {
		zone = firstNonEmpty(node.Zone, node.Labels["topology.kubernetes.io/zone"], node.Labels["failure-domain.beta.kubernetes.io/zone"])
	}

	identity := model.CloudResourceIdentity{
		Provider:     r.Provider,
		AccountID:    r.AccountID,
		Region:       region,
		Zone:         zone,
		ResourceID:   resourceID,
		ResourceType: r.ResourceType,
		ClusterID:    r.ClusterID,
		Metadata: map[string]string{
			"node":          node.Name,
			"instance_type": node.InstanceType,
			"provider_id":   providerID,
		},
	}
	if err := identity.Validate(); err != nil {
		return model.CloudResourceIdentity{}, err
	}
	return identity, nil
}

func (r Resolver) parse(providerID string) (resourceID, region, zone string, err error) {
	if parsed, parseErr := url.Parse(providerID); parseErr == nil && parsed.Scheme != "" {
		if !containsFold(r.Schemes, parsed.Scheme) {
			return "", "", "", fmt.Errorf("providerID %q has scheme %q, expected one of %v", providerID, parsed.Scheme, r.Schemes)
		}
		segments := splitSegments(parsed.Host + "/" + parsed.Path)
		if len(segments) > 0 {
			resourceID = segments[len(segments)-1]
		}
		if r.PathHasZone && len(segments) > 1 {
			zone = segments[len(segments)-2]
		}
	} else {
		resourceID = providerID
	}

	if r.DotRegionID {
		parts := strings.Split(resourceID, ".")
		if len(parts) >= 2 && hasPrefix(parts[len(parts)-1], r.IDPrefixes) {
			region = strings.Join(parts[:len(parts)-1], ".")
			resourceID = parts[len(parts)-1]
		}
	}
	if resourceID == "" || !hasPrefix(resourceID, r.IDPrefixes) {
		return "", "", "", fmt.Errorf("providerID %q does not contain a supported instance id", providerID)
	}
	return resourceID, region, zone, nil
}

func splitSegments(value string) []string {
	raw := strings.FieldsFunc(value, func(r rune) bool { return r == '/' })
	result := make([]string, 0, len(raw))
	for _, segment := range raw {
		if segment != "" {
			result = append(result, segment)
		}
	}
	return result
}

func containsFold(values []string, value string) bool {
	for _, item := range values {
		if strings.EqualFold(item, value) {
			return true
		}
	}
	return false
}

func hasPrefix(value string, prefixes []string) bool {
	if len(prefixes) == 0 {
		return value != ""
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
