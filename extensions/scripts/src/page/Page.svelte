<script lang="ts">
  // The page slot on the home (#ext/scripts/<route>): the ideas, or New idea.
  import { onMount, untrack } from 'svelte';
  import type { Ctx } from '../types';
  import Ideas from './Ideas.svelte';
  import NewIdea from './NewIdea.svelte';

  let { ctx }: { ctx: Ctx } = $props();

  const c = untrack(() => ctx);
  let route = $state(c.route);
  onMount(() => c.onRoute((r) => (route = r)));
  const page = $derived(route.replace(/^\/+|\/+$/g, '').split('/')[0]);
</script>

<div class="ig-page" data-part="scripts-page">
  {#if page === 'new'}
    <NewIdea ctx={c} />
  {:else}
    <Ideas ctx={c} />
  {/if}
</div>
