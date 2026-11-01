<script lang="ts">
  import { Select } from 'bits-ui';

  interface Option {
    value: string;
    label: string;
    disabled?: boolean;
  }

  let {
    label,
    value,
    options,
    onValueChange,
    disabled = false,
    compact = false,
    fullWidth = false,
    placeholder = 'Choose…',
  }: {
    label: string;
    value: string;
    options: Option[];
    onValueChange: (value: string) => void | Promise<void>;
    disabled?: boolean;
    compact?: boolean;
    fullWidth?: boolean;
    placeholder?: string;
  } = $props();

  const selected = $derived(options.find((option) => option.value === value)?.label ?? placeholder);
</script>

<Select.Root
  type="single"
  bind:value={
    () => value,
    (next) => {
      if (next !== value) void onValueChange(next);
    }
  }
  {disabled}
  items={options}
>
  <Select.Trigger
    aria-label={label}
    class={`retake-select-trigger ${compact ? 'is-compact' : ''} ${fullWidth ? 'is-fluid' : ''}`}
    title={selected}
  >
    <span class="selected-label">{selected}</span>
    <svg class="chevron" viewBox="0 0 16 16" aria-hidden="true"><path d="m4.5 6.5 3.5 3.5 3.5-3.5" /></svg>
  </Select.Trigger>
  <Select.Portal>
    <Select.Content class="retake-select-menu" align="start" sideOffset={6} collisionPadding={12} aria-label={label}>
      <Select.Viewport class="retake-select-viewport">
        {#each options as option (option.value)}
          <Select.Item
            value={option.value}
            label={option.label}
            disabled={option.disabled}
            class="retake-select-option"
          >
            {#snippet children({ selected })}
              <span class="option-label">{option.label}</span>
              <span class="check" aria-hidden="true"
                >{#if selected}<svg viewBox="0 0 16 16"><path d="m3.5 8 3 3 6-6" /></svg>{/if}</span
              >
            {/snippet}
          </Select.Item>
        {/each}
        {#if !options.length}<div class="no-options">No options available</div>{/if}
      </Select.Viewport>
    </Select.Content>
  </Select.Portal>
</Select.Root>

<style>
  :global(.retake-select-trigger),
  :global(.retake-select-option) {
    -webkit-tap-highlight-color: transparent;
  }
  :global(.retake-select-trigger) {
    box-sizing: border-box;
    display: inline-flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    min-width: 0;
    max-width: 100%;
    min-height: 40px;
    padding: 9px 12px;
    border: 1px solid #ffffff20;
    border-radius: 8px;
    background: #1a1a1e;
    color: #dddde5;
    font:
      13px/1.4 Inter,
      'Adwaita Sans',
      system-ui,
      sans-serif;
    text-align: left;
    cursor: pointer;
    transition:
      background 120ms ease,
      border-color 120ms ease;
  }
  :global(.retake-select-trigger:hover:not(:disabled)) {
    background: #242429;
    border-color: #ffffff38;
  }
  :global(.retake-select-trigger:focus-visible),
  :global(.retake-select-trigger[data-state='open']) {
    border-color: #a99ae7;
    outline: 2px solid #a99ae733;
    outline-offset: 2px;
  }
  :global(.retake-select-trigger:disabled) {
    opacity: 0.45;
    cursor: not-allowed;
  }
  :global(.retake-select-trigger.is-compact) {
    min-height: 32px;
    padding: 6px 8px;
    font-size: 12px;
    gap: 8px;
  }
  :global(.retake-select-trigger.is-fluid) {
    width: 100%;
  }
  .selected-label {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .chevron {
    width: 14px;
    height: 14px;
    flex-shrink: 0;
    color: #96969f;
    transition: transform 120ms ease;
  }
  :global(.retake-select-trigger[data-state='open']) .chevron {
    transform: rotate(180deg);
  }
  svg path {
    fill: none;
    stroke: currentColor;
    stroke-width: 1.6;
    stroke-linecap: round;
    stroke-linejoin: round;
  }
  :global(.retake-select-menu) {
    z-index: 200;
    box-sizing: border-box;
    min-width: var(--bits-floating-anchor-width);
    width: max-content;
    max-width: min(420px, calc(100vw - 24px));
    max-height: min(320px, var(--bits-floating-available-height));
    padding: 5px;
    border: 1px solid #ffffff24;
    border-radius: 10px;
    background: #242428;
    color: #e8e8ef;
    box-shadow:
      0 12px 40px #0008,
      0 2px 8px #0004;
    font:
      13px/1.4 Inter,
      'Adwaita Sans',
      system-ui,
      sans-serif;
    overflow: hidden;
  }
  :global(.retake-select-viewport) {
    min-height: 0;
    overflow-y: auto;
    scrollbar-width: thin;
    scrollbar-color: #55555f transparent;
    overscroll-behavior: contain;
  }
  :global(.retake-select-option) {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    min-height: 36px;
    padding: 8px 10px;
    border-radius: 6px;
    cursor: pointer;
    user-select: none;
    outline: none;
  }
  :global(.retake-select-option[data-highlighted]) {
    background: #ffffff10;
  }
  :global(.retake-select-option[data-selected]) {
    color: #d3c7ff;
    background: #a99ae710;
  }
  :global(.retake-select-option[data-selected][data-highlighted]) {
    background: #a99ae724;
  }
  :global(.retake-select-option[data-disabled]) {
    opacity: 0.4;
    cursor: not-allowed;
  }
  .option-label {
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .check {
    display: flex;
    width: 16px;
    height: 16px;
    flex-shrink: 0;
  }
  .check svg {
    width: 16px;
    height: 16px;
  }
  .no-options {
    padding: 10px;
    color: #9898a3;
  }
  @media (pointer: coarse) {
    :global(.retake-select-trigger),
    :global(.retake-select-trigger.is-compact),
    :global(.retake-select-option) {
      min-height: 44px;
    }
  }
</style>
