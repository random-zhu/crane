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
