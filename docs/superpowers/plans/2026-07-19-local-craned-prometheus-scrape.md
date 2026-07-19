# Local Craned Prometheus Scrape Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the kind-hosted Prometheus scrape recommendation metrics from the local Craned process so Grafana workload dashboards receive workload type and insight data.

**Architecture:** Extend the existing local-only Prometheus values file with one static `craned` scrape job targeting the host gateway. Upgrade the existing pinned Helm release while reusing its current values, then verify the complete Craned-to-Prometheus-to-Grafana query path.

**Tech Stack:** Helm 3, prometheus-community/prometheus 29.18.0, Prometheus HTTP API, kind, Crane Recommendation API

## Global Constraints

- Keep the host-specific target only in `examples/prometheus-local-values.yaml`.
- Do not change the bundled Grafana dashboards or generic deployment defaults.
- Preserve the current Prometheus release values with `--reuse-values`.
- Treat live Helm rendering and Prometheus API checks as the configuration acceptance test.

---

### Task 1: Add and Deploy the Local Craned Scrape Job

**Files:**
- Modify: `examples/prometheus-local-values.yaml`

**Interfaces:**
- Consumes: Craned metrics endpoint `http://host.docker.internal:8080/metrics`
- Produces: Prometheus job `craned` and stored `crane_analysis_resource_recommendation` series

- [ ] **Step 1: Run the pre-change rendering check**

Run:

```bash
helm template prometheus prometheus-community/prometheus \
  --namespace crane-system \
  --version 29.18.0 \
  -f examples/prometheus-local-values.yaml \
  | rg -n 'job_name: craned|host\.docker\.internal:8080'
```

Expected: exit status 1 with no matches because the local Craned scrape job is absent.

- [ ] **Step 2: Add the minimal scrape configuration**

Append this exact configuration to `examples/prometheus-local-values.yaml`:

```yaml

# Scrape the Craned process running on the host from the local kind cluster.
scrapeConfigs:
  craned:
    static_configs:
      - targets:
          - host.docker.internal:8080
```

- [ ] **Step 3: Render the chart and verify the generated Prometheus config**

Run:

```bash
helm template prometheus prometheus-community/prometheus \
  --namespace crane-system \
  --version 29.18.0 \
  -f examples/prometheus-local-values.yaml \
  | rg -n 'job_name: craned|host\.docker\.internal:8080'
```

Expected: both `job_name: craned` and `host.docker.internal:8080` appear in the rendered ConfigMap.

- [ ] **Step 4: Upgrade the existing Prometheus release**

Run:

```bash
helm upgrade prometheus prometheus-community/prometheus \
  --namespace crane-system \
  --version 29.18.0 \
  --reuse-values \
  -f examples/prometheus-local-values.yaml \
  --wait \
  --timeout 5m
```

Expected: release `prometheus` reports `STATUS: deployed`.

- [ ] **Step 5: Verify the scrape target and recommendation series**

Run:

```bash
curl -sS http://127.0.0.1:9090/api/v1/targets \
  | jq -r '.data.activeTargets[] | select(.labels.job == "craned") | [.labels.job,.health,.lastError] | @tsv'

curl -sS --get \
  --data-urlencode 'query=crane_analysis_resource_recommendation' \
  http://127.0.0.1:9090/api/v1/query \
  | jq -r '.data.result[] | [.metric.namespace,.metric.owner_kind,.metric.owner_name,.metric.resource] | @tsv'
```

Expected: target output contains `craned\tup`, and the query returns Deployment workload rows.

- [ ] **Step 6: Verify Grafana variable inputs**

Run:

```bash
curl -sS --get \
  --data-urlencode 'query=count by (owner_kind) (crane_analysis_resource_recommendation)' \
  http://127.0.0.1:9090/api/v1/query \
  | jq -r '.data.result[] | [.metric.owner_kind,.value[1]] | @tsv'

curl -sS --get \
  --data-urlencode 'query=count by (owner_name) (crane_analysis_resource_recommendation{owner_kind="Deployment"})' \
  http://127.0.0.1:9090/api/v1/query \
  | jq -r '.data.result[] | [.metric.owner_name,.value[1]] | @tsv'
```

Expected: the first query includes `Deployment`, and the second lists workload names such as `grafana`, `coredns`, and `prometheus-server`.

- [ ] **Step 7: Commit the local configuration**

```bash
git add examples/prometheus-local-values.yaml
git commit -m "chore: scrape local craned metrics"
```
