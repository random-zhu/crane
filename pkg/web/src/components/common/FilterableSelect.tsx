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
