package service

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/gocrane/crane/pkg/cost/allocation"
	"github.com/gocrane/crane/pkg/cost/collector"
	"github.com/gocrane/crane/pkg/cost/ledger"
	"github.com/gocrane/crane/pkg/cost/model"
	"github.com/gocrane/crane/pkg/cost/provider"
	"github.com/gocrane/crane/pkg/cost/providers"
	"github.com/gocrane/crane/pkg/cost/reconcile"
)

type targetRuntime struct {
	config  ProviderConfig
	adapter provider.Adapter
	target  collector.Target
}

type providerStatus struct {
	Provider     model.Provider        `json:"provider"`
	AccountID    string                `json:"accountId"`
	Capabilities provider.Capabilities `json:"capabilities"`
	LastSuccess  time.Time             `json:"lastSuccess,omitempty"`
	LastError    string                `json:"lastError,omitempty"`
}

type Service struct {
	config    Config
	store     ledger.Store
	collector *collector.Collector
	targets   []targetRuntime
	metrics   *metrics
	logger    *log.Logger
	server    *http.Server
	now       func() time.Time

	mu       sync.RWMutex
	ready    bool
	statuses map[string]providerStatus
}

func New(config Config, logger *log.Logger) (*Service, error) {
	config.setDefaults()
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = log.Default()
	}
	store, err := ledger.OpenFile(config.DataFile)
	if err != nil {
		return nil, err
	}
	reconciler, err := reconcile.New(config.Mappings)
	if err != nil {
		return nil, err
	}
	registry := provider.NewRegistry()
	if err := providers.RegisterAll(registry); err != nil {
		return nil, err
	}
	targets := make([]targetRuntime, 0, len(config.Providers))
	statuses := make(map[string]providerStatus, len(config.Providers))
	for _, entry := range config.Providers {
		adapter, err := registry.New(entry.Name, provider.Config{
			AccountID: entry.AccountID, Region: entry.Region, ClusterID: entry.ClusterID,
			Endpoint: entry.Endpoint, Options: entry.Options, Credential: entry.Credentials(),
		})
		if err != nil {
			return nil, err
		}
		targets = append(targets, targetRuntime{
			config: entry, adapter: adapter,
			target: collector.Target{Provider: entry.Name, AccountID: entry.AccountID, Bills: adapter.Bills, Reconciler: reconciler},
		})
		statuses[statusKey(entry.Name, entry.AccountID)] = providerStatus{
			Provider: entry.Name, AccountID: entry.AccountID, Capabilities: adapter.Capabilities,
		}
	}
	ingestor, err := collector.New(store, config.MaxPages)
	if err != nil {
		return nil, err
	}
	metricSet, err := newMetrics(config.NodeRates)
	if err != nil {
		return nil, err
	}
	service := &Service{
		config: config, store: store, collector: ingestor, targets: targets,
		metrics: metricSet, logger: logger, now: time.Now, statuses: statuses,
	}
	service.server = &http.Server{Addr: config.ListenAddress, Handler: service.routes(), ReadHeaderTimeout: 10 * time.Second}
	if err := service.refreshMetrics(context.Background()); err != nil {
		return nil, err
	}
	return service, nil
}

func (s *Service) Run(ctx context.Context) error {
	serverErrors := make(chan error, 1)
	go func() {
		s.logger.Printf("cost collector listening on %s", s.config.ListenAddress)
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErrors <- err
		}
		close(serverErrors)
	}()
	go s.collectionLoop(ctx)

	select {
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return s.server.Shutdown(shutdownContext)
	case err := <-serverErrors:
		return err
	}
}

func (s *Service) collectionLoop(ctx context.Context) {
	ticker := time.NewTicker(s.config.Interval())
	defer ticker.Stop()
	for {
		if _, err := s.CollectOnce(ctx); err != nil {
			s.logger.Printf("cost collection cycle completed with errors: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// CollectOnce isolates provider and billing-month failures. One unavailable
// cloud never prevents the remaining accounts from being refreshed.
func (s *Service) CollectOnce(ctx context.Context) ([]collector.Result, error) {
	periods := collector.BillingMonths(s.now(), s.config.LookbackDays)
	results := make([]collector.Result, 0, len(s.targets)*len(periods))
	errors := make([]string, 0)
	anySuccess := false
	for _, runtime := range s.targets {
		for _, period := range periods {
			started := time.Now()
			result, err := s.collector.PullMonth(ctx, runtime.target, period)
			s.metrics.observe(result, err, started)
			results = append(results, result)
			s.recordStatus(runtime.config, result, err)
			if err != nil {
				errors = append(errors, fmt.Sprintf("%s/%s/%s: %v", runtime.config.Name, runtime.config.AccountID, period.Start.Format("2006-01"), err))
				continue
			}
			anySuccess = anySuccess || result.Complete
		}
	}
	if err := s.refreshMetrics(ctx); err != nil {
		errors = append(errors, err.Error())
	}
	if anySuccess {
		s.mu.Lock()
		s.ready = true
		s.mu.Unlock()
		s.metrics.ready.Set(1)
	}
	if len(errors) > 0 {
		return results, fmt.Errorf("%s", strings.Join(errors, "; "))
	}
	return results, nil
}

func (s *Service) recordStatus(config ProviderConfig, result collector.Result, err error) {
	key := statusKey(config.Name, config.AccountID)
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.statuses[key]
	if err != nil {
		status.LastError = err.Error()
	} else if result.Complete {
		status.LastSuccess = result.CompletedAt
		status.LastError = ""
	}
	s.statuses[key] = status
}

func (s *Service) refreshMetrics(ctx context.Context) error {
	items, err := s.store.Query(ctx, ledger.Query{})
	if err != nil {
		return err
	}
	summaries, err := ledger.Summarize(items)
	if err != nil {
		return err
	}
	return s.metrics.refresh(len(items), summaries)
}

func (s *Service) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(s.metrics.registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/readyz", s.handleReady)
	mux.HandleFunc("/api/v1/providers", s.authenticate(s.handleProviders))
	mux.HandleFunc("/api/v1/costs", s.authenticate(s.handleCosts))
	mux.HandleFunc("/api/v1/costs/summary", s.authenticate(s.handleSummary))
	mux.HandleFunc("/api/v1/rates", s.authenticate(s.handleRates))
	mux.HandleFunc("/api/v1/allocate", s.authenticate(s.handleAllocate))
	return mux
}

func (s *Service) authenticate(next http.HandlerFunc) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if strings.TrimSpace(s.config.APITokenFile) == "" {
			next(response, request)
			return
		}
		data, err := os.ReadFile(s.config.APITokenFile)
		if err != nil || strings.TrimSpace(string(data)) == "" {
			http.Error(response, "API authentication is unavailable", http.StatusServiceUnavailable)
			return
		}
		expected := "Bearer " + strings.TrimSpace(string(data))
		provided := request.Header.Get("Authorization")
		if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			response.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(response, request)
	}
}

func (s *Service) handleReady(response http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	ready := s.ready
	s.mu.RUnlock()
	if !ready {
		http.Error(response, "no provider has completed collection", http.StatusServiceUnavailable)
		return
	}
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write([]byte("ok\n"))
}

func (s *Service) handleProviders(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(response, http.MethodGet)
		return
	}
	s.mu.RLock()
	statuses := make([]providerStatus, 0, len(s.statuses))
	for _, status := range s.statuses {
		statuses = append(statuses, status)
	}
	s.mu.RUnlock()
	writeJSON(response, http.StatusOK, map[string]interface{}{"providers": statuses})
}

func (s *Service) handleCosts(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(response, http.MethodGet)
		return
	}
	query, err := parseQuery(request)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	items, err := s.store.Query(request.Context(), query)
	if err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(response, http.StatusOK, map[string]interface{}{
		"costType": "actual_bill", "items": items, "offset": query.Offset, "limit": query.Limit,
	})
}

func (s *Service) handleSummary(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(response, http.MethodGet)
		return
	}
	query, err := parseQuery(request)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	query.Offset, query.Limit = 0, 0
	items, err := s.store.Query(request.Context(), query)
	if err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	summaries, err := ledger.Summarize(items)
	if err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(response, http.StatusOK, map[string]interface{}{
		"costType": "actual_bill", "summaries": summaries, "lineItems": len(items),
	})
}

func (s *Service) handleRates(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(response, http.MethodGet)
		return
	}
	values := request.URL.Query()
	query := model.RateQuery{
		Provider: model.Provider(values.Get("provider")), AccountID: values.Get("account_id"),
		Region: values.Get("region"), Zone: values.Get("zone"), ResourceType: values.Get("resource_type"),
		InstanceType: values.Get("instance_type"), ChargeModel: model.ChargeModel(values.Get("charge_model")),
	}
	if err := query.Provider.Validate(); err != nil || strings.TrimSpace(query.AccountID) == "" || strings.TrimSpace(query.ResourceType) == "" {
		http.Error(response, "provider, account_id and resource_type are required", http.StatusBadRequest)
		return
	}
	if raw := values.Get("effective_at"); raw != "" {
		effectiveAt, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			http.Error(response, "effective_at must be RFC3339", http.StatusBadRequest)
			return
		}
		query.EffectiveAt = effectiveAt
	}
	for _, runtime := range s.targets {
		if runtime.config.Name != query.Provider || runtime.config.AccountID != query.AccountID {
			continue
		}
		if runtime.adapter.Rates == nil {
			http.Error(response, "the configured provider account has no rate card", http.StatusNotFound)
			return
		}
		rates, err := runtime.adapter.Rates.GetRates(request.Context(), query)
		if err != nil {
			http.Error(response, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(response, http.StatusOK, map[string]interface{}{"costType": "estimated_rate", "rates": rates})
		return
	}
	http.Error(response, "provider account not found", http.StatusNotFound)
}

func (s *Service) handleAllocate(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(response, http.MethodPost)
		return
	}
	var input struct {
		Item    model.CostLineItem  `json:"item"`
		Targets []allocation.Target `json:"targets"`
		Scale   int                 `json:"scale"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	result, err := allocation.Allocate(input.Item, input.Targets, input.Scale)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func parseQuery(request *http.Request) (ledger.Query, error) {
	values := request.URL.Query()
	query := ledger.Query{
		Provider: model.Provider(values.Get("provider")), PayerAccount: values.Get("account_id"),
		ClusterID: values.Get("cluster_id"), BillingPeriod: values.Get("billing_period"),
		ResourceID: values.Get("resource_id"), Category: model.CostCategory(values.Get("category")), Limit: 100,
	}
	for name, destination := range map[string]*int{"offset": &query.Offset, "limit": &query.Limit} {
		if raw := values.Get(name); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil {
				return ledger.Query{}, fmt.Errorf("invalid %s", name)
			}
			*destination = value
		}
	}
	if query.Limit > 1000 {
		return ledger.Query{}, fmt.Errorf("limit must not exceed 1000")
	}
	return query, query.Validate()
}

func writeJSON(response http.ResponseWriter, status int, value interface{}) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func methodNotAllowed(response http.ResponseWriter, allowed string) {
	response.Header().Set("Allow", allowed)
	http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
}

func statusKey(provider model.Provider, account string) string {
	return string(provider) + "\x00" + account
}
