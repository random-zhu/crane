package options

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/pflag"
	cliflag "k8s.io/component-base/cli/flag"
	"sigs.k8s.io/yaml"

	topologyapi "github.com/gocrane/api/topology/v1alpha1"
)

// Options hold the command-line options about crane manager
type Options struct {
	// HostnameOverride is the name of k8s node
	HostnameOverride string
	// RuntimeEndpoint is the endpoint of runtime
	RuntimeEndpoint string
	// driver that the kubelet uses to manipulate cgroups on the host (cgroupfs or systemd)
	CgroupDriver string
	// SysPath is the path to /sys dir.
	SysPath string
	// KubeletRootPath is the Path to kubelet root directory.
	KubeletRootPath string
	// Is debug/pprof endpoint enabled
	EnableProfiling bool
	// BindAddr is the address the endpoint binds to.
	BindAddr string
	// CollectInterval is the period for state collector to collect metrics
	CollectInterval time.Duration
	// MaxInactivity is the maximum time from last recorded activity before automatic restart
	MaxInactivity time.Duration
	// Ifaces is the network devices to collect metric
	Ifaces               []string
	NodeResourceReserved map[string]string
	// ExecuteExcess is the percentage of executions that exceed the gap between current usage and watermarks
	ExecuteExcess string
	// CPUManagerReconcilePeriod is a duration that cpu manager reconciles.
	CPUManagerReconcilePeriod time.Duration
	// DefaultCPUPolicy is the default cpu policy, default to exclusive.
	DefaultCPUPolicy string
}

// NewOptions builds an empty options.
func NewOptions() *Options {
	return &Options{}
}

// Complete completes all the required options.
func (o *Options) Complete() error {
	if o.CgroupDriver != "auto" {
		return nil
	}

	driver, err := resolveCgroupDriver(o.KubeletRootPath, o.SysPath)
	if err != nil {
		return err
	}
	o.CgroupDriver = driver
	return nil
}

// Validate all required options.
func (o *Options) Validate() error {
	switch o.CgroupDriver {
	case "cgroupfs", "systemd":
		return nil
	default:
		return fmt.Errorf("unsupported cgroup driver %q: expected auto, cgroupfs, or systemd", o.CgroupDriver)
	}
}

type kubeletConfig struct {
	CgroupDriver string `json:"cgroupDriver"`
}

func resolveCgroupDriver(kubeletRootPath, sysPath string) (string, error) {
	configPath := filepath.Join(kubeletRootPath, "config.yaml")
	if data, err := os.ReadFile(configPath); err == nil {
		var config kubeletConfig
		if err := yaml.Unmarshal(data, &config); err != nil {
			return "", fmt.Errorf("parse kubelet config %s: %w", configPath, err)
		}
		switch config.CgroupDriver {
		case "cgroupfs", "systemd":
			return config.CgroupDriver, nil
		case "":
			// Fall back to the host cgroup hierarchy when the field is omitted.
		default:
			return "", fmt.Errorf("unsupported cgroup driver %q in %s", config.CgroupDriver, configPath)
		}
	} else if !os.IsNotExist(err) && !os.IsPermission(err) {
		return "", fmt.Errorf("read kubelet config %s: %w", configPath, err)
	}

	if _, err := os.Stat(filepath.Join(sysPath, "fs", "cgroup", "cgroup.controllers")); err == nil {
		return "systemd", nil
	} else if !os.IsNotExist(err) && !os.IsPermission(err) {
		return "", fmt.Errorf("detect cgroup v2 hierarchy: %w", err)
	}

	return "cgroupfs", nil
}

// AddFlags adds flags to the specified FlagSet.
func (o *Options) AddFlags(flags *pflag.FlagSet) {
	flags.StringVar(&o.HostnameOverride, "hostname-override", "", "Which is the name of k8s node be used to filtered.")
	flags.StringVar(&o.RuntimeEndpoint, "runtime-endpoint", "", "CRI v1 runtime endpoint. Auto-detection checks containerd, CRI-O, and k3s sockets when empty.")
	flags.StringVar(&o.CgroupDriver, "cgroup-driver", "auto", "Driver that the kubelet uses to manipulate cgroups on the host. Possible values: 'auto', 'cgroupfs', 'systemd'. Auto reads the kubelet config and otherwise detects cgroup v2.")
	flags.StringVar(&o.SysPath, "sys-path", "/sys", "Path to /sys dir.")
	flags.StringVar(&o.KubeletRootPath, "kubelet-root-path", "/var/lib/kubelet", "Path to the kubelet root directory.")
	flags.Bool("enable-profiling", false, "Is debug/pprof endpoint enabled, default: false")
	flags.StringVar(&o.BindAddr, "bind-address", "0.0.0.0:8081", "The address the agent binds to for metrics, health-check and pprof, default: 0.0.0.0:8081.")
	flags.DurationVar(&o.CollectInterval, "collect-interval", 10*time.Second, "Period for the state collector to collect metrics, default: 10s")
	flags.StringArrayVar(&o.Ifaces, "ifaces", []string{"eth0"}, "The network devices to collect metric, use comma to separated, default: eth0")
	flags.Var(cliflag.NewMapStringString(&o.NodeResourceReserved), "node-resource-reserved", "A set of ResourceName=Percent (e.g. cpu=40%,memory=40%)")
	flags.DurationVar(&o.MaxInactivity, "max-inactivity", 5*time.Minute, "Maximum time from last recorded activity before automatic restart, default: 5min")
	flags.StringVar(&o.ExecuteExcess, "execute-excess", "10%", "The percentage of executions that exceed the gap between current usage and watermarks, default: 10%.")
	flags.DurationVar(&o.CPUManagerReconcilePeriod, "cpu-manager-reconcile-period", 5*time.Second, "Specifies how often cpu manager reconciles.")
	flags.StringVar(&o.DefaultCPUPolicy, "default-cpu-policy", topologyapi.AnnotationPodCPUPolicyExclusive, "The default cpu policy if pod does not specify, should be one of none, exclusive, numa or immovable, default to exclusive.")
}
