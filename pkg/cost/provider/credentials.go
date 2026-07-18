package provider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type StaticCredentials struct {
	Values Credentials
}

func (s StaticCredentials) Retrieve(context.Context) (Credentials, error) {
	if len(s.Values) == 0 {
		return nil, fmt.Errorf("static credentials are empty")
	}
	result := make(Credentials, len(s.Values))
	for key, value := range s.Values {
		result[key] = value
	}
	return result, nil
}

// EnvironmentCredentials is useful for local development and CI. Production
// deployments should prefer a workload-identity or STS implementation.
type EnvironmentCredentials struct {
	Prefix       string
	Keys         []string
	OptionalKeys []string
}

func (e EnvironmentCredentials) Retrieve(context.Context) (Credentials, error) {
	result := make(Credentials, len(e.Keys))
	missing := make([]string, 0)
	for _, key := range e.Keys {
		envName := credentialEnvironmentName(e.Prefix, key)
		value, ok := os.LookupEnv(envName)
		if !ok || strings.TrimSpace(value) == "" {
			missing = append(missing, envName)
			continue
		}
		result[key] = value
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing credential environment variables: %s", strings.Join(missing, ", "))
	}
	for _, key := range e.OptionalKeys {
		if value := strings.TrimSpace(os.Getenv(credentialEnvironmentName(e.Prefix, key))); value != "" {
			result[key] = value
		}
	}
	return result, nil
}

func credentialEnvironmentName(prefix, key string) string {
	return prefix + strings.ToUpper(strings.ReplaceAll(key, "-", "_"))
}

// FileCredentials rereads one file per key on every retrieval. Kubernetes
// projected Secret volumes and token agents update files in place, allowing
// credential rotation without restarting the collector.
type FileCredentials struct {
	Directory    string
	Keys         []string
	OptionalKeys []string
}

func (f FileCredentials) Retrieve(context.Context) (Credentials, error) {
	if strings.TrimSpace(f.Directory) == "" {
		return nil, fmt.Errorf("credential file directory is required")
	}
	result := make(Credentials, len(f.Keys)+len(f.OptionalKeys))
	missing := make([]string, 0)
	for _, key := range f.Keys {
		value, err := readCredentialFile(f.Directory, key)
		if err != nil || value == "" {
			missing = append(missing, filepath.Join(f.Directory, key))
			continue
		}
		result[key] = value
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing credential files: %s", strings.Join(missing, ", "))
	}
	for _, key := range f.OptionalKeys {
		if value, err := readCredentialFile(f.Directory, key); err == nil && value != "" {
			result[key] = value
		}
	}
	return result, nil
}

func readCredentialFile(directory, key string) (string, error) {
	if key == "" || filepath.Base(key) != key || key == "." || key == ".." {
		return "", fmt.Errorf("invalid credential key %q", key)
	}
	data, err := os.ReadFile(filepath.Join(directory, key))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
