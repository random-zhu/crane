# Namespace Filter Reselection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore the complete searchable Namespace option list whenever an affected Select is reopened, while preserving valid user selections.

**Architecture:** Add a focused TDesign compatibility wrapper that refreshes the `options` reference when its popup opens. Use it only for the three affected Namespace controls, and make Cost-page default effects validate rather than overwrite the current selection.

**Tech Stack:** React 17, TypeScript 4.5, TDesign React 0.37.1, Redux Toolkit, Node built-in test runner, Vite 2.

## Global Constraints

- Do not upgrade `tdesign-react` from `0.37.1`.
- Preserve searchable Namespace lists and existing dependent Workload resets.
- Do not change backend Namespace APIs, Grafana queries, or unrelated Select controls.
- Preserve all unrelated dirty-worktree changes.

---

### Task 1: Filterable Select Compatibility Component

**Files:**
- Create: `pkg/web/src/components/common/FilterableSelect.tsx`
- Create: `pkg/web/test/namespace-filter.test.js`

**Interfaces:**
- Consumes: all props accepted by `tdesign-react`'s `Select`.
- Produces: `FilterableSelect`, a ref-forwarding Select that refreshes its option-array identity on popup open.

- [ ] **Step 1: Write the failing component behavior test**

Create `pkg/web/test/namespace-filter.test.js`. Compile the TSX with the existing `typescript` package and execute it with stubbed React/TDesign modules so the test can inspect the visibility behavior without adding a DOM dependency:

```js
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
```

- [ ] **Step 2: Run the test and verify the red state**

Run: `cd pkg/web && node --test test/namespace-filter.test.js`

Expected: FAIL with `ENOENT` for `src/components/common/FilterableSelect.tsx`.

- [ ] **Step 3: Add the minimal compatibility component**

Create `pkg/web/src/components/common/FilterableSelect.tsx`:

```tsx
import React from 'react';
import { Select } from 'tdesign-react';

type FilterableSelectProps = React.ComponentProps<typeof Select>;

export const FilterableSelect = React.forwardRef<HTMLDivElement, FilterableSelectProps>(
  ({ options, onVisibleChange, ...props }, ref) => {
    const [optionsRevision, refreshOptions] = React.useReducer((revision) => revision + 1, 0);
    const refreshedOptions = React.useMemo(() => (options ? [...options] : options), [options, optionsRevision]);
    const handleVisibleChange = React.useCallback(
      (visible: boolean) => {
        if (visible) refreshOptions();
        onVisibleChange?.(visible);
      },
      [onVisibleChange],
    );

    return <Select {...props} ref={ref} options={refreshedOptions} onVisibleChange={handleVisibleChange} />;
  },
);

FilterableSelect.displayName = 'FilterableSelect';
```

- [ ] **Step 4: Run the focused test and TypeScript build**

Run: `cd pkg/web && node --test test/namespace-filter.test.js`

Expected: PASS, 1 test and 0 failures.

Run: `cd pkg/web && npm run build:test`

Expected: Vite exits 0 with generated assets in `dist/`.

- [ ] **Step 5: Commit the compatibility component**

```bash
git add pkg/web/src/components/common/FilterableSelect.tsx pkg/web/test/namespace-filter.test.js
git commit -m "fix: restore filterable select options on reopen"
```

### Task 2: Namespace Integrations And Selection Guards

**Files:**
- Modify: `pkg/web/test/namespace-filter.test.js`
- Modify: `pkg/web/src/pages/Cost/WorkloadOverview/OverviewSearchPanel.tsx:1-191`
- Modify: `pkg/web/src/pages/Cost/WorkloadInsight/InsightSearchPanel.tsx:1-214`
- Modify: `pkg/web/src/pages/Recommend/ReplicaRecommend/components/SearchForm.tsx:1-58`

**Interfaces:**
- Consumes: `FilterableSelect` from Task 1.
- Produces: three Namespace controls that restore all options on reopen and Cost effects that preserve valid selections.

- [ ] **Step 1: Add failing integration assertions**

Append to `pkg/web/test/namespace-filter.test.js`:

```js
const integrations = [
  'src/pages/Cost/WorkloadOverview/OverviewSearchPanel.tsx',
  'src/pages/Cost/WorkloadInsight/InsightSearchPanel.tsx',
  'src/pages/Recommend/ReplicaRecommend/components/SearchForm.tsx',
];

test('all affected Namespace controls use FilterableSelect', () => {
  for (const relativePath of integrations) {
    const source = fs.readFileSync(path.join(webRoot, relativePath), 'utf8');
    assert.match(source, /import \{ FilterableSelect \} from ['"]components\/common\/FilterableSelect['"]/);
    assert.equal((source.match(/<FilterableSelect/g) ?? []).length, 1, relativePath);
  }
});

test('Cost Namespace defaults preserve a valid current selection', () => {
  for (const relativePath of integrations.slice(0, 2)) {
    const source = fs.readFileSync(path.join(webRoot, relativePath), 'utf8');
    assert.match(source, /isSelectedNamespaceAvailable/);
    assert.match(source, /!isSelectedNamespaceAvailable/);
  }
});
```

- [ ] **Step 2: Run the integration tests and verify the red state**

Run: `cd pkg/web && node --test test/namespace-filter.test.js`

Expected: the component test passes; both new integration tests fail because the affected files still use raw `Select` and lack validity guards.

- [ ] **Step 3: Replace the three affected Namespace controls**

In each affected file, add:

```tsx
import { FilterableSelect } from 'components/common/FilterableSelect';
```

Replace only the Namespace control's opening and closing tags:

```tsx
<FilterableSelect
  options={namespaceOptions}
  placeholder={t('命名空间')}
  filterable
  value={selectedNamespace ?? undefined}
  onChange={(value: any) => {
    dispatch(insightAction.selectedNamespace(value));
    dispatch(insightAction.selectedWorkloadType(undefined));
    dispatch(insightAction.selectedWorkload(undefined));
  }}
/>
```

For the shared recommendation form, preserve its Form-controlled value and existing styling:

```tsx
<FilterableSelect
  options={nameSpaceOptions}
  placeholder={t('请选择Namespace')}
  filterable
  style={{ margin: '0px 20px' }}
/>
```

Keep the raw TDesign `Select` import because Workload Type and Workload controls continue using it.

- [ ] **Step 4: Guard Cost default initialization**

In both Cost search panels, derive selection validity after `namespaceOptions`:

```tsx
const isSelectedNamespaceAvailable = namespaceOptions.some(({ value }) => value === selectedNamespace);
```

Change the Namespace initialization condition to:

```tsx
if (
  namespaceList.isSuccess
  && isNeedSelectNamespace
  && namespaceOptions?.[0]?.value
  && !isSelectedNamespaceAvailable
) {
  dispatch(insightAction.selectedNamespace(namespaceOptions[0].value));
}
```

Include `isSelectedNamespaceAvailable` in the effect dependency array. Preserve the Namespace `onChange` handler that clears `selectedWorkloadType` and `selectedWorkload`.

- [ ] **Step 5: Run focused and existing checks**

Run: `cd pkg/web && node --test test/namespace-filter.test.js`

Expected: PASS, 3 tests and 0 failures.

Run: `cd pkg/web && npm run lint`

Expected: exit 0 with no new ESLint errors in the four changed TSX files.

Run: `cd pkg/web && npm run build:test`

Expected: Vite exits 0.

Run: `cd pkg/web && npm test`

Expected: the Namespace tests pass. If the pre-existing uncommitted Workload recommendation test still fails on hard-coded `crane-scheduler`, record it separately and do not modify that concurrent work.

- [ ] **Step 6: Commit the integrations**

```bash
git add pkg/web/src/pages/Cost/WorkloadOverview/OverviewSearchPanel.tsx pkg/web/src/pages/Cost/WorkloadInsight/InsightSearchPanel.tsx pkg/web/src/pages/Recommend/ReplicaRecommend/components/SearchForm.tsx pkg/web/test/namespace-filter.test.js
git commit -m "fix: allow namespace filters to be reselected"
```

### Task 3: Running Frontend Verification

**Files:**
- No source files.

**Interfaces:**
- Consumes: the running Vite server on `127.0.0.1:3003`, namespace API on `127.0.0.1:8082`, and Tasks 1-2.
- Produces: runtime evidence that all six Namespace API values remain available and the frontend build is serving the updated modules.

- [ ] **Step 1: Confirm services and Namespace data**

Run: `lsof -nP -iTCP:3003 -sTCP:LISTEN`

Expected: one Node process listening on port `3003`.

Run: `curl -sS -G http://127.0.0.1:3003/api/v1/namespaces/cls-gdrmnjc5`

Expected: `totalCount: 6` with `crane-system`, `default`, `kube-node-lease`, `kube-public`, `kube-system`, and `local-path-storage`.

- [ ] **Step 2: Verify interactive reselection**

Open `http://127.0.0.1:3003` in the available local browser and verify without pressing Reset:

```text
Workload Overview: All -> crane-system -> default -> kube-system
Workload Insight: crane-system -> default -> kube-system
Resource Recommendation: first available Namespace -> second available Namespace
Replica Recommendation: first available Namespace -> second available Namespace
```

After each selection, reopen the control. Expected: all six API-provided Namespace values are available, not only the current selection.

- [ ] **Step 3: Check final diff and status**

Run: `git diff --check && git status --short`

Expected: no whitespace errors; unrelated pre-existing changes remain untouched.
