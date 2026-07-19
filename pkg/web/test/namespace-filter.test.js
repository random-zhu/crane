const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');
const ts = require('typescript');

const webRoot = path.resolve(__dirname, '..');
const componentPath = path.join(webRoot, 'src/components/common/FilterableSelect.tsx');

function findNode(node, predicate) {
  if (predicate(node)) return node;

  let match;
  ts.forEachChild(node, (child) => {
    if (!match) match = findNode(child, predicate);
  });
  return match;
}

function hasNode(node, predicate) {
  return Boolean(findNode(node, predicate));
}

function parseIntegration(relativePath) {
  const source = fs.readFileSync(path.join(webRoot, relativePath), 'utf8');
  const sourceFile = ts.createSourceFile(relativePath, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  assert.equal(sourceFile.parseDiagnostics.length, 0, relativePath);
  return sourceFile;
}

function getJsxAttribute(element, name) {
  return element.attributes.properties.find(
    (attribute) => ts.isJsxAttribute(attribute) && attribute.name.text === name,
  );
}

function findSelectByOptions(sourceFile, optionsName) {
  return findNode(sourceFile, (node) => {
    if (!ts.isJsxSelfClosingElement(node) && !ts.isJsxOpeningElement(node)) return false;

    const options = getJsxAttribute(node, 'options');
    return options
      && options.initializer
      && ts.isJsxExpression(options.initializer)
      && options.initializer.expression?.getText(sourceFile) === optionsName;
  });
}

function hasDirectDispatch(block, actionName, argumentText, sourceFile) {
  if (!ts.isBlock(block)) return false;

  return block.statements.some((statement) => {
    if (!ts.isExpressionStatement(statement) || !ts.isCallExpression(statement.expression)) return false;

    const dispatch = statement.expression;
    if (dispatch.expression.getText(sourceFile) !== 'dispatch') return false;

    const action = dispatch.arguments[0];
    return ts.isCallExpression(action)
      && action.expression.getText(sourceFile) === `insightAction.${actionName}`
      && action.arguments[0]?.getText(sourceFile) === argumentText;
  });
}

function loadFilterableSelect() {
  let refreshCount = 0;
  const React = {
    createElement: (type, props) => ({ type, props }),
    forwardRef: (render) => ({ render }),
    useCallback: (callback) => callback,
    useMemo: (factory) => factory(),
    useReducer: () => [0, () => { refreshCount += 1; }],
  };
  const Select = function Select() {};
  const source = fs.readFileSync(componentPath, 'utf8');
  const compiled = ts.transpileModule(source, {
    compilerOptions: {
      esModuleInterop: true,
      jsx: ts.JsxEmit.React,
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2020,
    },
  }).outputText;
  const componentModule = { exports: {} };
  const requireStub = (id) => {
    if (id === 'react') return { __esModule: true, default: React };
    if (id === 'tdesign-react') return { Select };
    throw new Error(`Unexpected module: ${id}`);
  };

  vm.runInNewContext(compiled, {
    exports: componentModule.exports,
    module: componentModule,
    require: requireStub,
  });

  return {
    FilterableSelect: componentModule.exports.FilterableSelect,
    hasSelectOption: componentModule.exports.hasSelectOption,
    getRefreshCount: () => refreshCount,
  };
}

test('FilterableSelect refreshes complete options when reopened', () => {
  const { FilterableSelect, getRefreshCount } = loadFilterableSelect();
  const options = [{ label: 'default', value: 'default' }, { label: 'kube-system', value: 'kube-system' }];
  const onChange = () => {};
  const visibility = [];
  const element = FilterableSelect.render({
    filterable: true,
    onChange,
    onVisibleChange: (visible) => visibility.push(visible),
    options,
    value: 'default',
  }, null);

  assert.notEqual(element.props.options, options);
  assert.deepEqual(Array.from(element.props.options, ({ label, value }) => ({ label, value })), options);
  assert.equal(element.props.value, 'default');
  assert.equal(element.props.onChange, onChange);

  element.props.onVisibleChange(true);
  element.props.onVisibleChange(false);

  assert.equal(getRefreshCount(), 1);
  assert.deepEqual(visibility, [true, false]);
});

test('hasSelectOption reports whether a selection is available', () => {
  const { hasSelectOption } = loadFilterableSelect();
  const options = [{ label: 'default', value: 'default' }, { label: 'kube-system', value: 'kube-system' }];

  assert.equal(typeof hasSelectOption, 'function');
  assert.equal(hasSelectOption(options, 'default'), true);
  assert.equal(hasSelectOption(options, 'missing'), false);
  assert.equal(hasSelectOption(options, undefined), false);
});

const integrations = [
  {
    optionsName: 'namespaceOptions',
    relativePath: 'src/pages/Cost/WorkloadOverview/OverviewSearchPanel.tsx',
    type: 'cost',
  },
  {
    optionsName: 'namespaceOptions',
    relativePath: 'src/pages/Cost/WorkloadInsight/InsightSearchPanel.tsx',
    type: 'cost',
  },
  {
    optionsName: 'nameSpaceOptions',
    relativePath: 'src/pages/Recommend/ReplicaRecommend/components/SearchForm.tsx',
    type: 'recommendation',
  },
];

test('all affected Namespace controls preserve their FilterableSelect behavior', () => {
  for (const { optionsName, relativePath, type } of integrations) {
    const sourceFile = parseIntegration(relativePath);
    const namespaceSelect = findSelectByOptions(sourceFile, optionsName);

    assert.ok(namespaceSelect, relativePath);
    assert.equal(namespaceSelect.tagName.getText(sourceFile), 'FilterableSelect', relativePath);
    const filterable = getJsxAttribute(namespaceSelect, 'filterable');
    assert.ok(filterable, relativePath);
    assert.ok(
      !filterable.initializer
      || (ts.isJsxExpression(filterable.initializer)
        && filterable.initializer.expression?.kind === ts.SyntaxKind.TrueKeyword),
      relativePath,
    );

    if (type === 'cost') {
      const value = getJsxAttribute(namespaceSelect, 'value');
      assert.ok(value?.initializer && ts.isJsxExpression(value.initializer), relativePath);
      assert.equal(value.initializer.expression?.getText(sourceFile), 'selectedNamespace ?? undefined', relativePath);
      const onChange = getJsxAttribute(namespaceSelect, 'onChange');
      assert.ok(onChange?.initializer && ts.isJsxExpression(onChange.initializer), relativePath);
      assert.ok(onChange.initializer.expression && ts.isArrowFunction(onChange.initializer.expression), relativePath);
      assert.ok(
        hasDirectDispatch(onChange.initializer.expression.body, 'selectedNamespace', 'value', sourceFile),
        relativePath,
      );
      assert.ok(
        hasDirectDispatch(onChange.initializer.expression.body, 'selectedWorkloadType', 'undefined', sourceFile),
        relativePath,
      );
      assert.ok(
        hasDirectDispatch(onChange.initializer.expression.body, 'selectedWorkload', 'undefined', sourceFile),
        relativePath,
      );
    } else {
      let formItem = namespaceSelect.parent;
      while (formItem && !ts.isJsxElement(formItem)) formItem = formItem.parent;

      assert.ok(formItem && ts.isJsxElement(formItem), relativePath);
      assert.equal(formItem.openingElement.tagName.getText(sourceFile), 'FormItem', relativePath);
      const name = getJsxAttribute(formItem.openingElement, 'name');
      assert.ok(name?.initializer && ts.isStringLiteral(name.initializer), relativePath);
      assert.equal(name.initializer.text, 'namespace', relativePath);
    }
  }
});

test('Cost Namespace defaults preserve valid selections and reset dependents when replaced', () => {
  for (const { relativePath } of integrations.filter(({ type }) => type === 'cost')) {
    const sourceFile = parseIntegration(relativePath);
    const availability = findNode(sourceFile, (node) =>
      ts.isVariableDeclaration(node)
      && node.name.getText(sourceFile) === 'isSelectedNamespaceAvailable');

    assert.ok(availability?.initializer && ts.isCallExpression(availability.initializer), relativePath);
    assert.equal(availability.initializer.expression.getText(sourceFile), 'hasSelectOption', relativePath);
    assert.deepEqual(availability.initializer.arguments.map((argument) => argument.getText(sourceFile)), [
      'namespaceOptions',
      'selectedNamespace',
    ], relativePath);

    const namespaceEffect = findNode(sourceFile, (node) =>
      ts.isCallExpression(node)
      && node.expression.getText(sourceFile) === 'React.useEffect'
      && node.arguments[0]
      && hasNode(node.arguments[0], (candidate) =>
        ts.isPrefixUnaryExpression(candidate)
        && candidate.operator === ts.SyntaxKind.ExclamationToken
        && candidate.operand.getText(sourceFile) === 'isSelectedNamespaceAvailable'));

    assert.ok(namespaceEffect, relativePath);
    const dependencies = namespaceEffect.arguments[1];
    assert.ok(dependencies && ts.isArrayLiteralExpression(dependencies), relativePath);
    assert.ok(
      dependencies.elements.some((dependency) => dependency.getText(sourceFile) === 'isSelectedNamespaceAvailable'),
      relativePath,
    );

    const guardedReplacement = findNode(namespaceEffect.arguments[0], (node) =>
      ts.isIfStatement(node)
      && hasNode(node.expression, (candidate) =>
        ts.isPrefixUnaryExpression(candidate)
        && candidate.operator === ts.SyntaxKind.ExclamationToken
        && candidate.operand.getText(sourceFile) === 'isSelectedNamespaceAvailable'));

    assert.ok(guardedReplacement, relativePath);
    assert.ok(
      hasDirectDispatch(
        guardedReplacement.thenStatement,
        'selectedNamespace',
        'namespaceOptions[0].value',
        sourceFile,
      ),
      relativePath,
    );
    assert.ok(
      hasDirectDispatch(guardedReplacement.thenStatement, 'selectedWorkloadType', 'undefined', sourceFile),
      relativePath,
    );
    assert.ok(
      hasDirectDispatch(guardedReplacement.thenStatement, 'selectedWorkload', 'undefined', sourceFile),
      relativePath,
    );
  }
});
