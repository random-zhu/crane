package providers

import (
	"fmt"

	"github.com/gocrane/crane/pkg/cost/provider"
	"github.com/gocrane/crane/pkg/cost/provider/aliyun"
	"github.com/gocrane/crane/pkg/cost/provider/huawei"
	"github.com/gocrane/crane/pkg/cost/provider/tencent"
	"github.com/gocrane/crane/pkg/cost/provider/volcengine"
)

// RegisterAll registers every built-in domestic cloud adapter. Keeping this
// explicit avoids package init side effects and lets embedders select a subset.
func RegisterAll(registry *provider.Registry) error {
	registrations := []struct {
		name string
		fn   func(*provider.Registry) error
	}{
		{"aliyun", aliyun.Register},
		{"volcengine", volcengine.Register},
		{"huawei", huawei.Register},
		{"tencent", tencent.Register},
	}
	for _, registration := range registrations {
		if err := registration.fn(registry); err != nil {
			return fmt.Errorf("register %s provider: %w", registration.name, err)
		}
	}
	return nil
}
