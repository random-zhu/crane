package options

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveCgroupDriver(t *testing.T) {
	tests := []struct {
		name     string
		config   string
		cgroupV2 bool
		want     string
		wantErr  bool
	}{
		{name: "kubelet systemd config", config: "cgroupDriver: systemd\n", want: "systemd"},
		{name: "kubelet cgroupfs config", config: "cgroupDriver: cgroupfs\n", cgroupV2: true, want: "cgroupfs"},
		{name: "cgroup v2 fallback", cgroupV2: true, want: "systemd"},
		{name: "cgroup v1 fallback", want: "cgroupfs"},
		{name: "invalid kubelet driver", config: "cgroupDriver: invalid\n", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			kubeletRoot := filepath.Join(root, "kubelet")
			sysRoot := filepath.Join(root, "sys")
			if err := os.MkdirAll(kubeletRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			if tt.config != "" {
				if err := os.WriteFile(filepath.Join(kubeletRoot, "config.yaml"), []byte(tt.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tt.cgroupV2 {
				cgroupRoot := filepath.Join(sysRoot, "fs", "cgroup")
				if err := os.MkdirAll(cgroupRoot, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(cgroupRoot, "cgroup.controllers"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}

			got, err := resolveCgroupDriver(kubeletRoot, sysRoot)
			if (err != nil) != tt.wantErr {
				t.Fatalf("resolveCgroupDriver() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("resolveCgroupDriver() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOptionsCompleteAndValidate(t *testing.T) {
	root := t.TempDir()
	o := &Options{CgroupDriver: "auto", KubeletRootPath: filepath.Join(root, "kubelet"), SysPath: filepath.Join(root, "sys")}
	if err := o.Complete(); err != nil {
		t.Fatal(err)
	}
	if o.CgroupDriver != "cgroupfs" {
		t.Fatalf("resolved driver = %q, want cgroupfs", o.CgroupDriver)
	}
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}

	o.CgroupDriver = "invalid"
	if err := o.Validate(); err == nil {
		t.Fatal("Validate() succeeded for an invalid cgroup driver")
	}
}
