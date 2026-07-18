package aliyun

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gocrane/crane/pkg/cost/model"
	"github.com/gocrane/crane/pkg/cost/provider"
	"github.com/gocrane/crane/pkg/cost/provider/internal/httpx"
	"github.com/gocrane/crane/pkg/cost/provider/internal/identity"
	"github.com/gocrane/crane/pkg/cost/ratecard"
)

const defaultEndpoint = "https://business.aliyuncs.com/"

type Options struct {
	Endpoint   string
	AccountID  string
	ClusterID  string
	Credential provider.CredentialProvider
	HTTPClient httpx.Doer
	Now        func() time.Time
	Nonce      func() string
	PageSize   int
}

func New(config provider.Config) (provider.Adapter, error) {
	if config.Credential == nil {
		return provider.Adapter{}, fmt.Errorf("aliyun credential provider is required")
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
	rates, hasRates, err := ratecard.FromOptions(model.ProviderAliyun, config.Options)
	if err != nil {
		return provider.Adapter{}, err
	}
	resolver := identity.Resolver{
		Provider:     model.ProviderAliyun,
		Schemes:      []string{"alicloud", "aliyun", "alibabacloud"},
		ResourceType: "ecs",
		ClusterID:    config.ClusterID,
		AccountID:    config.AccountID,
		IDPrefixes:   []string{"i-"},
		DotRegionID:  true,
	}
	return provider.Adapter{
		Name: model.ProviderAliyun,
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
	return registry.Register(model.ProviderAliyun, New)
}

func NewBillSource(options Options) (*BillSource, error) {
	if options.Credential == nil {
		return nil, fmt.Errorf("aliyun credential provider is required")
	}
	if options.Endpoint == "" {
		options.Endpoint = defaultEndpoint
	}
	endpoint, err := url.Parse(options.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse aliyun endpoint: %w", err)
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Nonce == nil {
		options.Nonce = func() string { return strconv.FormatInt(options.Now().UnixNano(), 10) }
	}
	if options.PageSize <= 0 || options.PageSize > 300 {
		options.PageSize = 300
	}
	return &BillSource{
		endpoint:   endpoint,
		accountID:  options.AccountID,
		clusterID:  options.ClusterID,
		credential: options.Credential,
		client:     httpx.DefaultClient(options.HTTPClient),
		now:        options.Now,
		nonce:      options.Nonce,
		pageSize:   options.PageSize,
	}, nil
}

// SignRPCParams signs an Alibaba Cloud RPC request using SignatureVersion 1.0.
// It is exported so deterministic conformance vectors can be tested without
// sending credentials over the network.
func SignRPCParams(parameters map[string]string, accessKeySecret string) string {
	keys := make([]string, 0, len(parameters))
	for key := range parameters {
		if key != "Signature" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, percentEncode(key)+"="+percentEncode(parameters[key]))
	}
	canonical := strings.Join(pairs, "&")
	stringToSign := "GET&%2F&" + percentEncode(canonical)
	mac := hmac.New(sha1.New, []byte(accessKeySecret+"&"))
	_, _ = mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func percentEncode(value string) string {
	encoded := url.QueryEscape(value)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	encoded = strings.ReplaceAll(encoded, "*", "%2A")
	encoded = strings.ReplaceAll(encoded, "%7E", "~")
	return encoded
}

func requestWithParams(endpoint *url.URL, parameters map[string]string) (*http.Request, error) {
	requestURL := *endpoint
	query := requestURL.Query()
	for key, value := range parameters {
		query.Set(key, value)
	}
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequest(http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	return request, nil
}
