package provider

import (
	"context"
	"fmt"
	"io"

	"github.com/gocrane/crane/pkg/cost/model"
)

type Credentials map[string]string

// CredentialProvider lets adapters use workload identity, STS or a rotating
// secret without keeping long-lived keys in provider configuration.
type CredentialProvider interface {
	Retrieve(ctx context.Context) (Credentials, error)
}

type IdentityResolver interface {
	ResolveNode(ctx context.Context, node model.KubernetesNode) (model.CloudResourceIdentity, error)
}

type RateCardProvider interface {
	GetRates(ctx context.Context, query model.RateQuery) ([]model.Rate, error)
}

type BillSource interface {
	Pull(ctx context.Context, cursor model.BillCursor) (model.BillPage, error)
}

type BillFile struct {
	Name         string
	ETag         string
	LastModified string
	Open         func(ctx context.Context) (io.ReadCloser, error)
}

type BillFileSource interface {
	List(ctx context.Context, period model.BillingPeriod) ([]BillFile, error)
}

type Capabilities struct {
	IdentityResolution bool `json:"identityResolution"`
	RateCard           bool `json:"rateCard"`
	BillAPI            bool `json:"billApi"`
	BillFiles          bool `json:"billFiles"`
}

type Adapter struct {
	Name         model.Provider
	Capabilities Capabilities
	Identity     IdentityResolver
	Rates        RateCardProvider
	Bills        BillSource
	BillFiles    BillFileSource
}

func (a Adapter) Validate() error {
	if err := a.Name.Validate(); err != nil {
		return err
	}
	checks := []struct {
		name    string
		enabled bool
		value   interface{}
	}{
		{"identity resolver", a.Capabilities.IdentityResolution, a.Identity},
		{"rate card provider", a.Capabilities.RateCard, a.Rates},
		{"bill source", a.Capabilities.BillAPI, a.Bills},
		{"bill file source", a.Capabilities.BillFiles, a.BillFiles},
	}
	for _, check := range checks {
		if check.enabled && check.value == nil {
			return fmt.Errorf("provider %s enables %s without an implementation", a.Name, check.name)
		}
	}
	return nil
}

type Config struct {
	AccountID  string
	Region     string
	ClusterID  string
	Endpoint   string
	Options    map[string]string
	Credential CredentialProvider
}

type Factory func(config Config) (Adapter, error)
