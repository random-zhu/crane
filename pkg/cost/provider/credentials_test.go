package provider

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestEnvironmentCredentialsOptionalKeys(t *testing.T) {
	t.Setenv("TEST_SECRET_ID", "required")
	t.Setenv("TEST_TOKEN", "optional")
	credentials, err := (EnvironmentCredentials{
		Prefix: "TEST_", Keys: []string{"secret_id"}, OptionalKeys: []string{"token", "missing"},
	}).Retrieve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if credentials["secret_id"] != "required" || credentials["token"] != "optional" {
		t.Fatalf("unexpected credentials: %+v", credentials)
	}
	if _, exists := credentials["missing"]; exists {
		t.Fatal("missing optional credential should not be returned")
	}
}

func TestFileCredentialsAreRereadForRotation(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "auth_token")
	if err := os.WriteFile(path, []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := FileCredentials{Directory: directory, Keys: []string{"auth_token"}}
	credentials, err := provider.Retrieve(context.Background())
	if err != nil || credentials["auth_token"] != "first" {
		t.Fatalf("unexpected initial credentials: %+v %v", credentials, err)
	}
	if err := os.WriteFile(path, []byte("second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	credentials, err = provider.Retrieve(context.Background())
	if err != nil || credentials["auth_token"] != "second" {
		t.Fatalf("credentials were not rotated: %+v %v", credentials, err)
	}
}
