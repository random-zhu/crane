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

function collectPrometheusDatasources(dashboard, dashboardName) {
  const references = [];
  const collect = (datasource, location) => {
    if (datasource?.type === 'prometheus') {
      references.push({ dashboard: dashboardName, location, uid: datasource.uid });
    }
  };
  const walkPanels = (panels) => {
    for (const panel of panels ?? []) {
      collect(panel.datasource, `panel ${panel.title ?? panel.id}`);
      for (const target of panel.targets ?? []) {
        collect(target.datasource, `panel ${panel.title ?? panel.id} target ${target.refId}`);
      }
      walkPanels(panel.panels);
    }
  };

  walkPanels(dashboard.panels);
  for (const variable of dashboard.templating?.list ?? []) {
    collect(variable.datasource, `variable ${variable.name}`);
  }
  return references;
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

test('dashboard Prometheus datasource references resolve to a provisioned UID', () => {
  const values = yaml.load(fs.readFileSync(valuesPath, 'utf8'));
  const provisionedUids = new Set(
    values.datasources['datasources.yaml'].datasources
      .filter(({ type }) => type === 'prometheus')
      .map(({ uid }) => uid)
      .filter(Boolean),
  );
  const unresolved = Object.entries(values.dashboards.default).flatMap(([name, config]) => {
    const dashboard = JSON.parse(config.json);
    return collectPrometheusDatasources(dashboard, name)
      .filter(({ uid }) => !provisionedUids.has(uid))
      .map(({ dashboard: dashboardName, location, uid }) => (
        `${dashboardName}: ${location} (${uid ?? 'missing UID'})`
      ));
  });

  assert.deepEqual(unresolved, []);
});

test('Workload Pods Insight recommendation follows the selected workload', () => {
  const values = yaml.load(fs.readFileSync(valuesPath, 'utf8'));
  const dashboard = JSON.parse(values.dashboards.default['workload-insight'].json);
  const recommendationExpressions = (dashboard.panels ?? [])
    .filter(({ title }) => title?.startsWith('Workload Pods Insight'))
    .flatMap(({ targets }) => targets ?? [])
    .filter(({ legendFormat }) => legendFormat === 'recommend')
    .map(({ expr }) => expr);
  const invalid = recommendationExpressions.filter((expression) => (
    !expression.includes('pod=~"^$Workload-.*$"')
    || expression.includes('crane-scheduler')
  ));

  assert.equal(recommendationExpressions.length, 2);
  assert.deepEqual(invalid, []);
});
