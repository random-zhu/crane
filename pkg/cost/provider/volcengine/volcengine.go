package volcengine

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gocrane/crane/pkg/cost/model"
	"github.com/gocrane/crane/pkg/cost/provider"
	"github.com/gocrane/crane/pkg/cost/provider/internal/httpx"
	"github.com/gocrane/crane/pkg/cost/provider/internal/identity"
	"github.com/gocrane/crane/pkg/cost/ratecard"
)

const (
	defaultEndpoint = "https://open.volcengineapi.com/"
	serviceName     = "billing"
	defaultRegion   = "cn-north-1"
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
		return provider.Adapter{}, fmt.Errorf("volcengine credential provider is required")
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
	rates, hasRates, err := ratecard.FromOptions(model.ProviderVolc, config.Options)
	if err != nil {
		return provider.Adapter{}, err
	}
	resolver := identity.Resolver{
		Provider:     model.ProviderVolc,
		Schemes:      []string{"volcengine", "vke"},
		ResourceType: "ecs",
		ClusterID:    config.ClusterID,
		AccountID:    config.AccountID,
		IDPrefixes:   []string{"i-"},
		PathHasZone:  true,
	}
	return provider.Adapter{
		Name: model.ProviderVolc,
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
	return registry.Register(model.ProviderVolc, New)
}

func NewBillSource(options Options) (*BillSource, error) {
	if options.Credential == nil {
		return nil, fmt.Errorf("volcengine credential provider is required")
	}
	if options.Endpoint == "" {
		options.Endpoint = defaultEndpoint
	}
	endpoint, err := url.Parse(options.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse volcengine endpoint: %w", err)
	}
	if options.Region == "" {
		options.Region = defaultRegion
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.PageSize <= 0 || options.PageSize > 300 {
		options.PageSize = 300
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

// SignRequest applies Volcengine OpenAPI HMAC-SHA256 authentication.
func SignRequest(request *http.Request, body []byte, accessKeyID, secretAccessKey, sessionToken, region, service string, timestamp time.Time) {
	payloadHash := sha256Hex(body)
	xDate := timestamp.UTC().Format("20060102T150405Z")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Host", request.URL.Host)
	request.Host = request.URL.Host
	request.Header.Set("X-Date", xDate)
	request.Header.Set("X-Content-Sha256", payloadHash)
	if sessionToken != "" {
		request.Header.Set("X-Security-Token", sessionToken)
	}

	headerNames := []string{"content-type", "host", "x-content-sha256", "x-date"}
	if sessionToken != "" {
		headerNames = append(headerNames, "x-security-token")
	}
	sort.Strings(headerNames)
	canonicalHeaders := make([]string, 0, len(headerNames))
	for _, name := range headerNames {
		canonicalHeaders = append(canonicalHeaders, name+":"+strings.TrimSpace(headerValue(request, name)))
	}
	signedHeaders := strings.Join(headerNames, ";")
	canonicalURI := request.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	canonicalRequest := strings.Join([]string{
		request.Method,
		canonicalURI,
		request.URL.Query().Encode(),
		strings.Join(canonicalHeaders, "\n") + "\n",
		signedHeaders,
		payloadHash,
	}, "\n")
	shortDate := timestamp.UTC().Format("20060102")
	credentialScope := shortDate + "/" + region + "/" + service + "/request"
	stringToSign := strings.Join([]string{
		"HMAC-SHA256",
		xDate,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	kDate := hmacSHA256([]byte(secretAccessKey), shortDate)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))
	request.Header.Set("Authorization", "HMAC-SHA256 Credential="+accessKeyID+"/"+credentialScope+
		", SignedHeaders="+signedHeaders+", Signature="+signature)
}

func headerValue(request *http.Request, lowerName string) string {
	if lowerName == "host" {
		return request.URL.Host
	}
	return request.Header.Get(http.CanonicalHeaderKey(lowerName))
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
