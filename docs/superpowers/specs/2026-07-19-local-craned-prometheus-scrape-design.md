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
generic chart defaults are unchanged.

## Pods Insight Query Correction

The Workload Pods Insight recommendation targets currently count pods with a
hard-coded `crane-scheduler` name. Replace that pod matcher with `$Workload` so
the recommendation line follows the dashboard selection. Keep the change
limited to the two affected recommendation targets; do not alter the duplicate
panel layout or saved variable defaults.

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
- Run the dashboard PromQL regression test and confirm Pods Insight recommendation
  targets use `$Workload` rather than a specific workload name.
- Provision the updated dashboard and confirm the corrected query returns a
  recommendation value for a workload in the local kind cluster.
