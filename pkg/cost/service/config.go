package service

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gocrane/crane/pkg/cost/model"
	"github.com/gocrane/crane/pkg/cost/provider"
	"github.com/gocrane/crane/pkg/cost/reconcile"
)

type Config struct {
	ListenAddress string              `json:"listenAddress"`
	DataFile      string              `json:"dataFile"`
	APITokenFile  string              `json:"apiTokenFile,omitempty"`
	PullInterval  string              `json:"pullInterval"`
	LookbackDays  int                 `json:"lookbackDays"`
	MaxPages      int                 `json:"maxPages"`
	Providers     []ProviderConfig    `json:"providers"`
	Mappings      []reconcile.Mapping `json:"resourceMappings,omitempty"`
	NodeRates     []NodeRate          `json:"nodeRates,omitempty"`
}

type ProviderConfig struct {
	Name                    model.Provider    `json:"name"`
	AccountID               string            `json:"accountId"`
	Region                  string            `json:"region,omitempty"`
	ClusterID               string            `json:"clusterId,omitempty"`
	Endpoint                string            `json:"endpoint,omitempty"`
	Options                 map[string]string `json:"options,omitempty"`
	CredentialEnvPrefix     string            `json:"credentialEnvPrefix,omitempty"`
	CredentialFileDirectory string            `json:"credentialFileDirectory,omitempty"`
	CredentialKeys          []string          `json:"credentialKeys,omitempty"`
	OptionalCredentialKeys  []string          `json:"optionalCredentialKeys,omitempty"`
}

type NodeRate struct {
	Node            string         `json:"node"`
	Provider        model.Provider `json:"provider"`
	AccountID       string         `json:"accountId"`
	ClusterID       string         `json:"clusterId"`
	Region          string         `json:"region,omitempty"`
	InstanceType    string         `json:"instanceType,omitempty"`
	Currency        model.Currency `json:"currency"`
	TotalHourlyCost model.Decimal  `json:"totalHourlyCost"`
	CPUHourlyCost   model.Decimal  `json:"cpuCoreHourlyCost"`
	RAMHourlyCost   model.Decimal  `json:"ramGiBHourlyCost"`
}

func LoadConfig(path string) (Config, error) {
	if strings.TrimSpace(path) == "" {
		return Config{}, fmt.Errorf("config path is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open cost collector config: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode cost collector config: %w", err)
	}
	config.setDefaults()
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (c *Config) setDefaults() {
	if c.ListenAddress == "" {
		c.ListenAddress = ":8080"
	}
	if c.DataFile == "" {
		c.DataFile = "/var/lib/crane-cost/ledger.json"
	}
	if c.PullInterval == "" {
		c.PullInterval = "6h"
	}
	if c.LookbackDays <= 0 {
		c.LookbackDays = 7
	}
	if c.MaxPages <= 0 {
		c.MaxPages = 1000
	}
	for index := range c.Providers {
		entry := &c.Providers[index]
		if entry.CredentialEnvPrefix == "" {
			entry.CredentialEnvPrefix = "COST_" + strings.ToUpper(string(entry.Name)) + "_"
		}
		if len(entry.CredentialKeys) == 0 {
			entry.CredentialKeys, entry.OptionalCredentialKeys = defaultCredentialKeys(entry.Name)
		}
	}
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.ListenAddress) == "" {
		return fmt.Errorf("listenAddress is required")
	}
	if strings.TrimSpace(c.DataFile) == "" {
		return fmt.Errorf("dataFile is required")
	}
	if _, err := time.ParseDuration(c.PullInterval); err != nil {
		return fmt.Errorf("invalid pullInterval: %w", err)
	}
	if c.LookbackDays <= 0 {
		return fmt.Errorf("lookbackDays must be positive")
	}
	if c.MaxPages <= 0 {
		return fmt.Errorf("maxPages must be positive")
	}
	if len(c.Providers) == 0 {
		return fmt.Errorf("at least one cost provider is required")
	}
	seen := make(map[string]struct{})
	accounts := make(map[string]struct{})
	for index, entry := range c.Providers {
		if err := entry.Name.Validate(); err != nil || entry.Name == model.ProviderContract {
			return fmt.Errorf("provider %d: unsupported built-in provider %q", index, entry.Name)
		}
		if strings.TrimSpace(entry.AccountID) == "" {
			return fmt.Errorf("provider %d accountId is required", index)
		}
		key := string(entry.Name) + "\x00" + entry.AccountID
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate provider account %s/%s", entry.Name, entry.AccountID)
		}
		seen[key] = struct{}{}
		accounts[key] = struct{}{}
		if len(entry.CredentialKeys) == 0 {
			return fmt.Errorf("provider %s/%s requires credentialKeys", entry.Name, entry.AccountID)
		}
	}
	if _, err := reconcile.New(c.Mappings); err != nil {
		return err
	}
	nodes := make(map[string]struct{})
	for index, rate := range c.NodeRates {
		if err := rate.Validate(); err != nil {
			return fmt.Errorf("node rate %d: %w", index, err)
		}
		if _, exists := nodes[rate.Node]; exists {
			return fmt.Errorf("duplicate node rate %s", rate.Node)
		}
		nodes[rate.Node] = struct{}{}
		if _, exists := accounts[string(rate.Provider)+"\x00"+rate.AccountID]; !exists {
			return fmt.Errorf("node rate %s refers to unconfigured provider account %s/%s", rate.Node, rate.Provider, rate.AccountID)
		}
	}
	return nil
}

func (c Config) Interval() time.Duration {
	duration, _ := time.ParseDuration(c.PullInterval)
	return duration
}

func (p ProviderConfig) Credentials() provider.CredentialProvider {
	if strings.TrimSpace(p.CredentialFileDirectory) != "" {
		return provider.FileCredentials{
			Directory: p.CredentialFileDirectory, Keys: p.CredentialKeys, OptionalKeys: p.OptionalCredentialKeys,
		}
	}
	return provider.EnvironmentCredentials{
		Prefix: p.CredentialEnvPrefix, Keys: p.CredentialKeys, OptionalKeys: p.OptionalCredentialKeys,
	}
}

func (n NodeRate) Validate() error {
	if strings.TrimSpace(n.Node) == "" || strings.TrimSpace(n.AccountID) == "" || strings.TrimSpace(n.ClusterID) == "" {
		return fmt.Errorf("node, accountId and clusterId are required")
	}
	if err := n.Provider.Validate(); err != nil {
		return err
	}
	if err := n.Currency.Validate(); err != nil {
		return err
	}
	for name, amount := range map[string]model.Decimal{
		"totalHourlyCost": n.TotalHourlyCost, "cpuCoreHourlyCost": n.CPUHourlyCost, "ramGiBHourlyCost": n.RAMHourlyCost,
	} {
		if err := amount.Validate(); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if comparison, _ := amount.Cmp(model.Zero); comparison < 0 {
			return fmt.Errorf("%s must not be negative", name)
		}
	}
	return nil
}

func defaultCredentialKeys(name model.Provider) ([]string, []string) {
	switch name {
	case model.ProviderAliyun:
		return []string{"access_key_id", "access_key_secret"}, []string{"security_token"}
	case model.ProviderVolc:
		return []string{"access_key_id", "secret_access_key"}, []string{"session_token"}
	case model.ProviderHuawei:
		return []string{"auth_token"}, nil
	case model.ProviderTencent:
		return []string{"secret_id", "secret_key"}, []string{"token"}
	default:
		return nil, nil
	}
}
