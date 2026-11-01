<script lang="ts">
  // The phone layout's Z: undo the last stroke of this take. Icon only in landscape (`compact`).
  let { onclick, compact = false }: { onclick: () => void; compact?: boolean } = $props();
</script>

<button id="undo" data-part="undo" class:compact aria-label="Undo" title="Undo the last stroke (Z)" {onclick}
  ><svg viewBox="0 0 24 24" aria-hidden="true"
    ><path
      d="M9 14 4 9l5-5M4 9h10.5a5.5 5.5 0 0 1 0 11H11"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
    /></svg
  >{#if !compact}<span>Undo</span>{/if}</button
>
