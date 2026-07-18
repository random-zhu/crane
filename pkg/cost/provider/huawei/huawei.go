package huawei

import (
	"fmt"
	"net/url"
	"time"

	"github.com/gocrane/crane/pkg/cost/model"
	"github.com/gocrane/crane/pkg/cost/provider"
	"github.com/gocrane/crane/pkg/cost/provider/internal/httpx"
	"github.com/gocrane/crane/pkg/cost/provider/internal/identity"
	"github.com/gocrane/crane/pkg/cost/ratecard"
)

const defaultEndpoint = "https://bss.myhuaweicloud.com"

type Options struct {
	Endpoint   string
	AccountID  string
	ClusterID  string
	Credential provider.CredentialProvider
	HTTPClient httpx.Doer
	Now        func() time.Time
	PageSize   int
}

func New(config provider.Config) (provider.Adapter, error) {
	if config.Credential == nil {
		return provider.Adapter{}, fmt.Errorf("huawei credential provider is required")
	}
	source, err := NewBillSource(Options{
		Endpoint:   config.Endpoint,
		AccountID:  config.AccountID,
		ClusterID:  config.ClusterID,
		Credential: config.Credential,
	})
	if err != nil {
		return provider.Adapter{}, err
	}
	rates, hasRates, err := ratecard.FromOptions(model.ProviderHuawei, config.Options)
	if err != nil {
		return provider.Adapter{}, err
	}
	resolver := identity.Resolver{
		Provider:     model.ProviderHuawei,
		Schemes:      []string{"huawei", "huaweicloud", "cce"},
		ResourceType: "ecs",
		ClusterID:    config.ClusterID,
		AccountID:    config.AccountID,
	}
	return provider.Adapter{
		Name: model.ProviderHuawei,
		Capabilities: provider.Capabilities{
			IdentityResolution: true,
			RateCard:           hasRates,
			BillAPI:            true,
		},
		Identity: resolver,
		Rates:    rates,
		Bills:    source,
	}, nil
}

func Register(registry *provider.Registry) error {
	return registry.Register(model.ProviderHuawei, New)
}

func NewBillSource(options Options) (*BillSource, error) {
	if options.Credential == nil {
		return nil, fmt.Errorf("huawei credential provider is required")
	}
	if options.Endpoint == "" {
		options.Endpoint = defaultEndpoint
	}
	endpoint, err := url.Parse(options.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse huawei endpoint: %w", err)
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.PageSize <= 0 || options.PageSize > 1000 {
		options.PageSize = 1000
	}
	return &BillSource{
		endpoint:   endpoint,
		accountID:  options.AccountID,
		clusterID:  options.ClusterID,
		credential: options.Credential,
		client:     httpx.DefaultClient(options.HTTPClient),
		now:        options.Now,
		pageSize:   options.PageSize,
	}, nil
}
