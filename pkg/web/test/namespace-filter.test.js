const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');
const ts = require('typescript');

const webRoot = path.resolve(__dirname, '..');
const componentPath = path.join(webRoot, 'src/components/common/FilterableSelect.tsx');

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
    getRefreshCount: () => refreshCount,
  };
}

test('FilterableSelect refreshes complete options when reopened', () => {
  const { FilterableSelect, getRefreshCount } = loadFilterableSelect();
  const options = [{ label: 'default', value: 'default' }, { label: 'kube-system', value: 'kube-system' }];
  const visibility = [];
  const element = FilterableSelect.render({
    filterable: true,
    onVisibleChange: (visible) => visibility.push(visible),
    options,
  }, null);

  assert.notEqual(element.props.options, options);
  assert.deepEqual(Array.from(element.props.options, ({ label, value }) => ({ label, value })), options);

  element.props.onVisibleChange(true);
  element.props.onVisibleChange(false);

  assert.equal(getRefreshCount(), 1);
  assert.deepEqual(visibility, [true, false]);
});
