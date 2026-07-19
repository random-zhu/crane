# Grafana PromQL Label Fix Design

## Problem

The six provisioned Grafana dashboards contain 77 PromQL expressions. The
current Prometheus parser rejects 34 expressions with `unexpected character
inside braces: '.'`.

Every rejected expression uses the Kubernetes label key
`node.kubernetes.io/instance-type` directly inside a PromQL selector. Prometheus
label names cannot contain `.`, `/`, or `-`. Kube-state-metrics exposes this key
as `label_node_kubernetes_io_instance_type`, which is already present beside the
invalid matcher in each affected expression.

The remaining 43 expressions pass Prometheus `/api/v1/parse_query` after Grafana
variables are replaced with representative values.

## Scope

- Fix all 34 invalid expressions in `pkg/web/values.yaml`.
- Preserve the existing normalized matcher
  `label_node_kubernetes_io_instance_type!~"eklet"`.
- Add an automated regression test that parses every embedded dashboard JSON
  document and rejects raw Kubernetes label keys in PromQL selectors.
- Upgrade the existing `grafana` Helm release in `crane-system` so the corrected
  dashboards take effect immediately.
- Keep the release on chart `grafana-10.5.15` and Grafana `12.3.1`.

This change does not alter query calculations, dashboard layout, Grafana
authentication, data sources, persistence, or unrelated local deployment
configuration.

## Implementation

For each affected selector, remove only the invalid matcher and its adjacent
comma. For example:

```promql
kube_node_labels{node.kubernetes.io/instance-type!~"eklet", label_node_kubernetes_io_instance_type!~"eklet"}
```

becomes:

```promql
kube_node_labels{label_node_kubernetes_io_instance_type!~"eklet"}
```

The test will use Node's built-in test runner and the existing `js-yaml`
dependency to load `pkg/web/values.yaml`, parse each `dashboards.default.*.json`
value, recursively visit panel targets, and assert that no PromQL expression
contains the raw key `node.kubernetes.io/instance-type`.

## Verification

The implementation follows a red-green sequence:

1. Add the regression test and confirm it fails on the 34 current expressions.
2. Apply the minimal expression edits and confirm the test passes.
3. Parse all 77 expressions through the current Prometheus
   `/api/v1/parse_query` endpoint after substituting representative Grafana
   variables. The required result is 77 successes and zero errors.
4. Render and lint chart `grafana-10.5.15` with the updated values.
5. Upgrade release `grafana` in namespace `crane-system` using the same chart
   version and the updated values.
6. Confirm the dashboard ConfigMap contains no raw instance-type label, the
   Grafana rollout is healthy, and recent Grafana logs contain no new PromQL
   `bad_data` parse errors after dashboard access.

## Deployment And Rollback

The runtime update will use Helm rather than directly patching the generated
ConfigMap, so the live state remains owned by the existing release.

Before the upgrade, record the current release revision and render the proposed
manifests. If the rollout fails, use Helm rollback to revision 2. Source rollback
consists only of reverting the dashboard expression change and its regression
test.

The upgrade must not prune Docker volumes or modify the existing Prometheus,
cost collector, Craned, or frontend processes.
