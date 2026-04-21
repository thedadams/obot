<script lang="ts">
	import { page } from '$app/state';
	import Layout from '$lib/components/Layout.svelte';
	import FilterForm from '$lib/components/admin/FilterForm.svelte';
	import { PAGE_TRANSITION_DURATION } from '$lib/constants.js';
	import type { MCPFilter } from '$lib/services/admin/types';
	import { profile } from '$lib/stores';
	import { goto } from '$lib/url';
	import { fly } from 'svelte/transition';
	import { twMerge } from 'tailwind-merge';

	let { data }: { data: { filter: MCPFilter } } = $props();
	let { filter } = $derived(data);
	let title = $derived(filter?.name ?? 'Filter');
	let selected = $derived<string>((page.url.searchParams.get('view') as string) || 'configuration');

	const tabs = [
		{ label: 'Configuration', view: 'configuration' },
		{ label: 'Server Details', view: 'server-details' },
		{ label: 'Audit Logs', view: 'audit-logs' },
		{ label: 'Usage', view: 'usage' }
	];

	function handleSelectionChange(newSelection: string) {
		if (newSelection !== selected) {
			const url = new URL(window.location.href);
			url.searchParams.set('view', newSelection);
			goto(url, { replaceState: true });
		}
	}

	$effect(() => {
		console.log({ filter });
	});

	const duration = PAGE_TRANSITION_DURATION;
</script>

<Layout {title} showBackButton>
	<div
		class="h-full w-full flex flex-col gap-4"
		in:fly={{ x: 100, duration }}
		out:fly={{ x: -100, duration }}
	>
		<div class="flex flex-1 gap-2 py-1 text-sm font-light">
			{#each tabs as tab (tab.view)}
				<button
					onclick={() => {
						handleSelectionChange(tab.view);
					}}
					class={twMerge(
						'min-w-fit flex-1 rounded-md border border-transparent px-3 py-2 text-center whitespace-nowrap transition-colors duration-300',
						selected === tab.view &&
							'dark:bg-surface1 dark:border-surface3 bg-background shadow-sm',
						selected !== tab.view && 'hover:bg-surface3'
					)}
				>
					{tab.label}
				</button>
			{/each}
		</div>

		{#if selected === 'configuration'}
			<FilterForm
				{filter}
				onUpdate={() => {
					goto('/admin/filters');
				}}
				readonly={profile.current.isAdminReadonly?.()}
			/>
		{:else if selected === 'server-details'}
			<!-- server details view -->
		{:else if selected === 'audit-logs'}
			<!-- <AuditLogs /> -->
		{:else if selected === 'usage'}
			<!-- <Usage /> -->
		{/if}
	</div>
</Layout>

<svelte:head>
	<title>Obot | {title}</title>
</svelte:head>
