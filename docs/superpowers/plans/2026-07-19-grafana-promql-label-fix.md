# Grafana PromQL Label Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Repair all provisioned Grafana PromQL expressions that use an invalid raw Kubernetes label key and deploy the corrected dashboards to the current cluster.

**Architecture:** Keep `pkg/web/values.yaml` as the Helm-owned dashboard source. Add a Node regression test that parses every embedded dashboard JSON document, make the smallest textual matcher removal, validate all expressions with the running Prometheus parser, then upgrade the existing Grafana release at its current chart version.

**Tech Stack:** Helm 3, Grafana chart 10.5.15, Grafana 12.3.1, Prometheus HTTP API, Node.js built-in test runner, `js-yaml`.

## Global Constraints

- Preserve chart `grafana-10.5.15` and Grafana `12.3.1`.
- Preserve the normalized matcher `label_node_kubernetes_io_instance_type!~"eklet"`.
- Do not alter query calculations, dashboard layout, authentication, data sources, persistence, Prometheus, cost collector, Craned, or frontend processes.
- Do not directly patch the generated Dashboard ConfigMap; Helm remains its owner.
- Preserve all pre-existing worktree changes. In particular, do not automatically commit `pkg/web/values.yaml` because it already contains user edits.

---

### Task 1: Add Dashboard PromQL Regression Coverage

**Files:**
- Create: `pkg/web/test/dashboard-promql.test.js`
- Modify: `pkg/web/package.json`
- Test: `pkg/web/test/dashboard-promql.test.js`

**Interfaces:**
- Consumes: `pkg/web/values.yaml` at `dashboards.default.<name>.json`.
- Produces: an `npm test` check that reports each dashboard, panel, and target containing `node.kubernetes.io/instance-type`.

- [ ] **Step 1: Write the failing test**

Create a CommonJS Node test that loads `values.yaml`, parses every dashboard JSON string, recursively visits nested panels, collects target `expr` fields, asserts that 77 expressions were inspected, and asserts that none contains `node.kubernetes.io/instance-type`.

```js
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const yaml = require('js-yaml');

const valuesPath = path.resolve(__dirname, '..', 'values.yaml');

function collectExpressions(panels, dashboardName, expressions = []) {
  for (const panel of panels ?? []) {
    for (const target of panel.targets ?? []) {
      if (typeof target.expr === 'string') {
        expressions.push({
          dashboard: dashboardName,
          panel: panel.title,
          refId: target.refId,
          expression: target.expr,
        });
      }
    }
    collectExpressions(panel.panels, dashboardName, expressions);
  }
  return expressions;
}

test('dashboard PromQL uses Prometheus-normalized Kubernetes label names', () => {
  const values = yaml.load(fs.readFileSync(valuesPath, 'utf8'));
  const expressions = Object.entries(values.dashboards.default).flatMap(([name, config]) => {
    const dashboard = JSON.parse(config.json);
    return collectExpressions(dashboard.panels, name);
  });
  const invalid = expressions
    .filter(({ expression }) => expression.includes('node.kubernetes.io/instance-type'))
    .map(({ dashboard, panel, refId }) => `${dashboard}: ${panel} (${refId})`);

  assert.equal(expressions.length, 77);
  assert.deepEqual(invalid, []);
});
```

Change the package test script to:

```json
"test": "node --test test/*.test.js"
```

- [ ] **Step 2: Run the test and verify RED**

Run: `npm test`

Expected: FAIL because the assertion reports 34 targets containing the raw Kubernetes label key.

---

### Task 2: Repair And Validate All Dashboard Queries

**Files:**
- Modify: `pkg/web/values.yaml`
- Test: `pkg/web/test/dashboard-promql.test.js`

**Interfaces:**
- Consumes: the failing regression test from Task 1.
- Produces: 77 embedded PromQL expressions accepted by the current Prometheus parser.

- [ ] **Step 1: Apply the minimal matcher removal**

Mechanically remove both raw matcher forms, including the adjacent comma and optional space:

```text
node.kubernetes.io/instance-type!~\"eklet\",
node.kubernetes.io/instance-type!=\"eklet\",
```

Do not change the adjacent normalized matcher.

- [ ] **Step 2: Run the regression test and verify GREEN**

Run: `npm test`

Expected: PASS with one passing test and no failures.

- [ ] **Step 3: Confirm the source inventory**

Run a structured Node scan of `values.yaml`.

Expected: 77 expressions inspected and zero expressions containing `node.kubernetes.io/instance-type`.

- [ ] **Step 4: Validate every expression with Prometheus**

Load all 77 expressions, replace these Grafana variables with representative parse-safe values, and send each expression to `http://127.0.0.1:9090/api/v1/parse_query`:

```text
$Discount=100
$Namespace=crane-system
$namespace=crane-system
$WorkloadType=Deployment
$Workload=.*
$cluster=.*
$node=.*
$__range=1h
```

Expected JSON summary: `{"checked":77,"errorCount":0}`.

- [ ] **Step 5: Check the diff**

Run: `git diff --check -- pkg/web/values.yaml pkg/web/package.json pkg/web/test/dashboard-promql.test.js`

Expected: no whitespace errors. Confirm the `values.yaml` diff contains only the 34 matcher removals in addition to the user's pre-existing data source change.

---

### Task 3: Upgrade And Verify The Current Grafana Release

**Files:**
- Runtime source: `pkg/web/values.yaml`
- Generated resource: `configmap/grafana-dashboards-default` in namespace `crane-system`

**Interfaces:**
- Consumes: the parser-clean values from Task 2 and Helm release `grafana` revision 2.
- Produces: Helm-managed live dashboards with no invalid raw Kubernetes label matchers.

- [ ] **Step 1: Render a server-side dry run**

Run:

```bash
helm upgrade grafana grafana/grafana \
  --namespace crane-system \
  --version 10.5.15 \
  --reuse-values \
  --values pkg/web/values.yaml \
  --dry-run=server
```

Expected: successful render with no Helm errors and no release mutation.

- [ ] **Step 2: Upgrade the release**

Run the same command without `--dry-run=server`.

Expected: release `grafana` becomes revision 3 with status `deployed`.

- [ ] **Step 3: Wait for the rollout**

Run: `kubectl rollout status deployment/grafana -n crane-system --timeout=180s`

Expected: `deployment "grafana" successfully rolled out`.

- [ ] **Step 4: Verify the live Dashboard ConfigMap**

Read `configmap/grafana-dashboards-default` and scan all six JSON entries.

Expected: 77 expressions and zero occurrences of `node.kubernetes.io/instance-type`.

- [ ] **Step 5: Verify runtime parsing and logs**

Repeat the 77-expression Prometheus parse validation against the live ConfigMap, then inspect Grafana logs since the upgrade.

Expected: 77 parser successes, zero parser errors, a healthy Grafana Pod, and no new `bad_data` messages caused by `unexpected character inside braces: '.'`.

- [ ] **Step 6: Report rollback point**

Record release revision 3 as the deployed result and revision 2 as the rollback point. If verification fails, run:

```bash
helm rollback grafana 2 --namespace crane-system --wait
```

Do not rollback when all verification checks pass.
