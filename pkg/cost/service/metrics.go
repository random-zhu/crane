package service

import (
	"fmt"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/gocrane/crane/pkg/cost/collector"
	"github.com/gocrane/crane/pkg/cost/ledger"
)

type metrics struct {
	registry        *prometheus.Registry
	attempts        *prometheus.CounterVec
	errors          *prometheus.CounterVec
	lastSuccess     *prometheus.GaugeVec
	lastDuration    *prometheus.GaugeVec
	lineItems       prometheus.Gauge
	billAmount      *prometheus.GaugeVec
	ready           prometheus.Gauge
	nodeTotalHourly *prometheus.GaugeVec
	nodeCPUHourly   *prometheus.GaugeVec
	nodeRAMHourly   *prometheus.GaugeVec
}

func newMetrics(nodeRates []NodeRate) (*metrics, error) {
	labels := []string{"provider", "account_id"}
	nodeLabels := []string{"node", "provider", "account_id", "cluster_id", "region", "instance_type", "currency"}
	result := &metrics{
		registry: prometheus.NewRegistry(),
		attempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "crane_cost_collection_attempts_total", Help: "Cloud bill collection attempts.",
		}, labels),
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "crane_cost_collection_errors_total", Help: "Cloud bill collection failures.",
		}, labels),
		lastSuccess: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "crane_cost_collection_last_success_timestamp_seconds", Help: "Last successful cloud bill collection Unix time.",
		}, labels),
		lastDuration: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "crane_cost_collection_duration_seconds", Help: "Duration of the latest cloud bill collection.",
		}, labels),
		lineItems: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "crane_cost_ledger_line_items", Help: "Normalized line items currently stored in the ledger.",
		}),
		billAmount: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "crane_cost_bill_amount", Help: "Normalized bill total; exact values remain available through the API.",
		}, []string{"provider", "account_id", "cluster_id", "currency", "cost_type"}),
		ready: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "crane_cost_collector_ready", Help: "Whether at least one configured provider completed a collection.",
		}),
		nodeTotalHourly: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "node_total_hourly_cost", Help: "Configured total node hourly rate for Crane dashboard compatibility.",
		}, nodeLabels),
		nodeCPUHourly: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "node_cpu_hourly_cost", Help: "Configured per-core hourly rate for Crane dashboard compatibility.",
		}, nodeLabels),
		nodeRAMHourly: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "node_ram_hourly_cost", Help: "Configured per-GiB hourly rate for Crane dashboard compatibility.",
		}, nodeLabels),
	}
	result.registry.MustRegister(result.attempts, result.errors, result.lastSuccess, result.lastDuration,
		result.lineItems, result.billAmount, result.ready, result.nodeTotalHourly, result.nodeCPUHourly, result.nodeRAMHourly)
	for _, rate := range nodeRates {
		labelValues := []string{rate.Node, string(rate.Provider), rate.AccountID, rate.ClusterID, rate.Region, rate.InstanceType, string(rate.Currency)}
		for metric, amount := range map[*prometheus.GaugeVec]string{
			result.nodeTotalHourly: rate.TotalHourlyCost.String(),
			result.nodeCPUHourly:   rate.CPUHourlyCost.String(),
			result.nodeRAMHourly:   rate.RAMHourlyCost.String(),
		} {
			value, err := strconv.ParseFloat(amount, 64)
			if err != nil {
				return nil, fmt.Errorf("convert node rate %s: %w", rate.Node, err)
			}
			metric.WithLabelValues(labelValues...).Set(value)
		}
	}
	return result, nil
}

func (m *metrics) observe(result collector.Result, err error, started time.Time) {
	labels := []string{string(result.Provider), result.AccountID}
	m.attempts.WithLabelValues(labels...).Inc()
	m.lastDuration.WithLabelValues(labels...).Set(time.Since(started).Seconds())
	if err != nil {
		m.errors.WithLabelValues(labels...).Inc()
		return
	}
	if result.Complete {
		m.lastSuccess.WithLabelValues(labels...).Set(float64(result.CompletedAt.Unix()))
	}
}

func (m *metrics) refresh(items int, summaries []ledger.Summary) error {
	m.lineItems.Set(float64(items))
	m.billAmount.Reset()
	for _, summary := range summaries {
		labels := []string{string(summary.Key.Provider), summary.Key.PayerAccount, summary.Key.ClusterID, string(summary.Key.Currency)}
		for costType, amount := range map[string]string{
			"list": summary.ListCost.String(), "net": summary.NetCost.String(), "amortized": summary.AmortizedCost.String(),
		} {
			value, err := strconv.ParseFloat(amount, 64)
			if err != nil {
				return fmt.Errorf("convert %s bill summary: %w", costType, err)
			}
			m.billAmount.WithLabelValues(append(labels, costType)...).Set(value)
		}
	}
	return nil
}
