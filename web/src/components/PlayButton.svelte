<script lang="ts">
  let { paused, onclick }: { paused: boolean; onclick: () => void } = $props();
</script>

<button id="play" aria-label={paused ? 'Play' : 'Pause'} {onclick}>
  {#if paused}
    <svg viewBox="0 0 24 24"
      ><path
        d="M8 5.6v12.8c0 .8.9 1.3 1.6.9l10.2-6.4c.6-.4.6-1.3 0-1.7L9.6 4.7C8.9 4.3 8 4.8 8 5.6z"
        fill="currentColor"
      /></svg
    >
  {:else}
    <svg viewBox="0 0 24 24"
      ><rect x="6.5" y="5" width="3.6" height="14" rx="1.2" fill="currentColor" /><rect
        x="13.9"
        y="5"
        width="3.6"
        height="14"
        rx="1.2"
        fill="currentColor"
      /></svg
    >
  {/if}
</button>
