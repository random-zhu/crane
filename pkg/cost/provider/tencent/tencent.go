package tencent

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gocrane/crane/pkg/cost/model"
	"github.com/gocrane/crane/pkg/cost/provider"
	"github.com/gocrane/crane/pkg/cost/provider/internal/httpx"
	"github.com/gocrane/crane/pkg/cost/provider/internal/identity"
	"github.com/gocrane/crane/pkg/cost/ratecard"
)

const (
	defaultEndpoint = "https://billing.tencentcloudapi.com/"
	service         = "billing"
	apiVersion      = "2018-07-09"
)

type Options struct {
	Endpoint   string
	AccountID  string
	Region     string
	ClusterID  string
	Credential provider.CredentialProvider
	HTTPClient httpx.Doer
	Now        func() time.Time
	PageSize   int
}

func New(config provider.Config) (provider.Adapter, error) {
	if config.Credential == nil {
		return provider.Adapter{}, fmt.Errorf("tencent credential provider is required")
	}
	source, err := NewBillSource(Options{
		Endpoint:   config.Endpoint,
		AccountID:  config.AccountID,
		Region:     config.Region,
		ClusterID:  config.ClusterID,
		Credential: config.Credential,
	})
	if err != nil {
		return provider.Adapter{}, err
	}
	rates, hasRates, err := ratecard.FromOptions(model.ProviderTencent, config.Options)
	if err != nil {
		return provider.Adapter{}, err
	}
	resolver := identity.Resolver{
		Provider:     model.ProviderTencent,
		Schemes:      []string{"qcloud", "tencentcloud"},
		ResourceType: "cvm",
		ClusterID:    config.ClusterID,
		AccountID:    config.AccountID,
		IDPrefixes:   []string{"ins-"},
		PathHasZone:  true,
	}
	return provider.Adapter{
		Name: model.ProviderTencent,
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
	return registry.Register(model.ProviderTencent, New)
}

func NewBillSource(options Options) (*BillSource, error) {
	if options.Credential == nil {
		return nil, fmt.Errorf("tencent credential provider is required")
	}
	if options.Endpoint == "" {
		options.Endpoint = defaultEndpoint
	}
	endpoint, err := url.Parse(options.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse tencent endpoint: %w", err)
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.PageSize <= 0 || options.PageSize > 100 {
		options.PageSize = 100
	}
	return &BillSource{
		endpoint:   endpoint,
		accountID:  options.AccountID,
		region:     options.Region,
		clusterID:  options.ClusterID,
		credential: options.Credential,
		client:     httpx.DefaultClient(options.HTTPClient),
		now:        options.Now,
		pageSize:   options.PageSize,
	}, nil
}

// SignTC3 signs a Tencent Cloud API 3.0 request in place.
func SignTC3(request *http.Request, body []byte, secretID, secretKey, region, action string, timestamp time.Time) {
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("Host", request.URL.Host)
	request.Host = request.URL.Host
	request.Header.Set("X-TC-Action", action)
	request.Header.Set("X-TC-Version", apiVersion)
	request.Header.Set("X-TC-Timestamp", strconv.FormatInt(timestamp.Unix(), 10))
	if region != "" {
		request.Header.Set("X-TC-Region", region)
	}

	canonicalHeaders := "content-type:application/json; charset=utf-8\n" + "host:" + request.URL.Host + "\n"
	signedHeaders := "content-type;host"
	canonicalRequest := strings.Join([]string{
		request.Method,
		request.URL.EscapedPath(),
		request.URL.RawQuery,
		canonicalHeaders,
		signedHeaders,
		sha256Hex(body),
	}, "\n")
	date := timestamp.UTC().Format("2006-01-02")
	credentialScope := date + "/" + service + "/tc3_request"
	stringToSign := strings.Join([]string{
		"TC3-HMAC-SHA256",
		strconv.FormatInt(timestamp.Unix(), 10),
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	secretDate := hmacSHA256([]byte("TC3"+secretKey), date)
	secretService := hmacSHA256(secretDate, service)
	secretSigning := hmacSHA256(secretService, "tc3_request")
	signature := hex.EncodeToString(hmacSHA256(secretSigning, stringToSign))
	authorization := "TC3-HMAC-SHA256 Credential=" + secretID + "/" + credentialScope +
		", SignedHeaders=" + signedHeaders + ", Signature=" + signature
	request.Header.Set("Authorization", authorization)
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}
