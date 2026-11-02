<script lang="ts">
  // Record's glyph: a microphone with the small red record dot at its top right (what people expect "record" to be,
  // with the dot that says it records).
  let { size = 20 }: { size?: number } = $props();
</script>

<span class="mic" aria-hidden="true" style:width="{size}px" style:height="{size}px"
  ><svg viewBox="0 0 24 24"
    ><rect x="8.6" y="3" width="6.8" height="11.4" rx="3.4" fill="none" stroke="currentColor" stroke-width="1.8" /><path
      d="M5.4 11.2a6.6 6.6 0 0 0 13.2 0M12 17.8V21"
      fill="none"
      stroke="currentColor"
      stroke-width="1.8"
      stroke-linecap="round"
    /></svg
  ><span class="recdot"></span></span
>
