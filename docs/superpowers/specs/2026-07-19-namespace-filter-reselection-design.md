# Namespace Filter Reselection Design

## Problem

Several frontend Namespace selectors allow an initial searchable selection but
show only that selected Namespace when reopened. Users can recover the complete
option list only by resetting the form.

The affected selectors use `filterable` from `tdesign-react 0.37.1`. In that
version, opening a Select clears its search input with `onInputChange('')`, but
the input-value effect calls `handleFilter` only when the new value is truthy.
The internal `currentOptions` array therefore remains narrowed to the previous
search result instead of returning to the complete `options` array.

The behavior affects both Cost selectors and the shared recommendation search
form. The Cost pages also have initialization effects that can overwrite a
valid Redux selection when option data is refreshed.

## Scope

- Fix Namespace reselection in:
  - `Cost/WorkloadOverview/OverviewSearchPanel.tsx`.
  - `Cost/WorkloadInsight/InsightSearchPanel.tsx`.
  - `Recommend/ReplicaRecommend/components/SearchForm.tsx`, which is shared by
    Resource Recommendation and Replica Recommendation.
- Preserve searchable Namespace lists.
- Preserve the existing default selections and dependent Workload resets.
- Prevent default-value effects from replacing a valid user-selected Namespace.
- Add focused regression coverage and verify the running development frontend.

This change does not upgrade TDesign, change backend Namespace APIs, alter
Grafana queries, or refactor unrelated Select controls.

## Design

Add a small common Select compatibility component. It accepts the normal
TDesign Select props and forwards `value`, `onChange`, refs, and callbacks. When
the popup opens, the component advances a local refresh counter and supplies a
fresh copy of the original `options` array. TDesign's existing `options` effect
then restores `currentOptions` without remounting or closing the popup.

Only the affected Namespace controls will use the compatibility component.
Keeping the workaround local avoids a dependency upgrade and limits behavioral
change to the reported workflow. The shared recommendation form continues to
work with TDesign Form because the wrapper forwards the injected `value` and
`onChange` props to the underlying Select.

Cost initialization effects will set the first available value only when the
current value is empty or no longer exists in the current option list. A user
selection that is still valid remains unchanged. Changing Namespace continues
to clear Workload Type and Workload so dependent queries cannot retain values
from the previous Namespace.

The interaction becomes:

1. Open Namespace and optionally type a search term.
2. Select a Namespace.
3. Reopen Namespace; the complete Namespace list is restored.
4. Select another Namespace without resetting the page or form.
5. Cost pages refresh dependent Workload options for the new Namespace.

## Error Handling

An empty successful Namespace response leaves Workload Insight with no default
to dispatch. Workload Overview retains its existing synthetic `All` option and
may select that value. A failed response does not dispatch a new default. If a
cluster change removes the selected Namespace, the Cost page selects the first
newly available option; otherwise it preserves the current selection.

The compatibility component treats an omitted `options` prop as an empty input
and still forwards all visibility callbacks. It does not mutate the caller's
option array.

## Verification

Implementation follows a red-green sequence:

1. Add a focused regression test covering option restoration and the three
   affected Namespace integration points; confirm it fails before the wrapper
   is introduced.
2. Add the compatibility component and replace only the affected Namespace
   Select instances.
3. Add selection-validity guards to the Cost initialization effects.
4. Run the focused test, existing frontend tests, lint, and production build.
5. In the running frontend, verify these sequences without using Reset:
   - Workload Overview: `All -> crane-system -> default -> kube-system`.
   - Workload Insight: `crane-system -> default -> kube-system`.
   - Resource and Replica Recommendation: select one available Namespace,
     reopen the selector, and select a different Namespace.
6. Confirm each reopening shows the complete Namespace option set and that the
   table or Grafana panels update for the latest selection.

## Rollback

Rollback removes the compatibility component usage and restores the original
TDesign Select imports and initialization conditions. No backend, cluster, or
persisted data rollback is required.
