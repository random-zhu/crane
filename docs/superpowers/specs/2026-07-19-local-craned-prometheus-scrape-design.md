# Local Craned Prometheus Scrape Design

## Problem

The local Craned process exports workload recommendation metrics on
`127.0.0.1:8080/metrics`, but Prometheus running in the kind cluster does not
scrape that endpoint. Grafana therefore receives no
`crane_analysis_resource_recommendation` series, leaving workload type and
workload pods insight empty even after Recommendation resources are ready.

## Design

Add a local-only Prometheus scrape job to
`examples/prometheus-local-values.yaml`. The job targets
`host.docker.internal:8080`, which is reachable from the Prometheus pod and
maps to the host where Craned runs.

The configuration remains in the existing local values file so production and
generic chart defaults are unchanged. Existing Grafana dashboards and queries
remain unchanged because they already consume the correct Crane metric.

## Data Flow

1. RecommendationRule creates workload Recommendation resources.
2. Craned computes recommendations and exports them on `/metrics`.
3. Prometheus scrapes the local Craned endpoint once per configured interval.
4. Grafana variable and panel queries read the stored recommendation series.

## Failure Handling

If Craned is stopped, the Prometheus `craned` target becomes `down`; other
Prometheus jobs continue operating. Restarting Craned restores collection on
the next scrape without changing the configuration.

## Verification

- Render the Helm chart and confirm the `craned` scrape job targets
  `host.docker.internal:8080`.
- Upgrade the local Prometheus release with the local values file.
- Confirm the `craned` target reports `up`.
- Confirm `crane_analysis_resource_recommendation` returns workload series.
- Confirm the dashboard variable query returns `Deployment` and workload names.
