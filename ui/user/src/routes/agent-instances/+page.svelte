<script lang="ts">
	import { doDelete, doGet, doPost, doPut, doWithBody, handleResponse } from '$lib/services/http';
	import { listModels } from '$lib/services/user/operations';
	import type { Model } from '$lib/services/user/types';
	import { onMount } from 'svelte';

	type Instance = {
		metadata: { name: string; generation: number; deletionTimestamp?: string };
		spec: { displayName: string; model: string; suspended: boolean };
		status: { state?: string; error?: string; observedGeneration?: number };
	};
	type Message = { role: string; text: string; interrupted?: boolean };
	let enabled = $state<boolean>();
	let instances = $state<Instance[]>([]);
	let models = $state<Model[]>([]);
	let selectedID = $state('');
	let displayName = $state('');
	let model = $state('');
	let serverIDs = $state('');
	let prompt = $state('');
	let messages = $state<Message[]>([]);
	let busy = $state(false);
	let saving = $state(false);
	let error = $state('');
	let activity = $state('');
	let historyID = '';
	let refreshing = false;
	let stream: AbortController | undefined;
	const selected = $derived(instances.find((instance) => instance.metadata.name === selectedID));
	const ready = $derived(
		selected?.status.state === 'RUNNING' &&
			!selected.spec.suspended &&
			!selected.metadata.deletionTimestamp &&
			selected.status.observedGeneration === selected.metadata.generation
	);
	const path = (id: string) => `/agent-instances/${encodeURIComponent(id)}`;

	function report(e: unknown) {
		error = e instanceof Error ? e.message : String(e);
	}

	async function refresh() {
		if (refreshing) return;
		refreshing = true;
		try {
			instances = (
				(await doGet('/agent-instances', { dontLogErrors: true })) as { items: Instance[] }
			).items;
			if (!instances.some((instance) => instance.metadata.name === selectedID))
				selectedID = instances[0]?.metadata.name ?? '';
			const current = instances.find((instance) => instance.metadata.name === selectedID);
			if (
				!busy &&
				current?.status.state === 'RUNNING' &&
				!current.spec.suspended &&
				historyID !== selectedID
			) {
				const id = selectedID;
				const history = (await doGet(`${path(id)}/history`, { dontLogErrors: true })) as {
					messages: Message[];
				};
				if (id === selectedID && !busy) {
					messages = history.messages;
					historyID = id;
				}
			}
		} catch (e) {
			report(e);
		} finally {
			refreshing = false;
		}
	}

	onMount(() => {
		let stopped = false;
		void (async () => {
			try {
				enabled = ((await doGet('/agent-instances/config')) as { enabled: boolean }).enabled;
				if (enabled && !stopped) {
					models = (await listModels()).filter(
						(m) => m.active && (m.dialect === 'anthropic' || m.targetModel.startsWith('claude'))
					);
					await refresh();
				}
			} catch (e) {
				report(e);
			}
		})();
		const timer = setInterval(() => {
			if (enabled) void refresh();
		}, 5000);
		return () => {
			stopped = true;
			clearInterval(timer);
			stream?.abort();
		};
	});

	async function create() {
		saving = true;
		error = '';
		try {
			const created = (await doPost('/agent-instances', {
				displayName,
				model,
				mcpServerIDs: serverIDs.split(/[\s,]+/).filter(Boolean)
			})) as Instance;
			selectedID = created.metadata.name;
			messages = [];
			displayName = '';
			await refresh();
		} catch (e) {
			report(e);
		} finally {
			saving = false;
		}
	}

	async function changeState(suspended: boolean) {
		saving = true;
		error = '';
		try {
			await doPut(path(selectedID), { suspended });
			await refresh();
		} catch (e) {
			report(e);
		} finally {
			saving = false;
		}
	}

	async function remove() {
		if (!confirm('Delete this agent and its workspace? This cannot be undone.')) return;
		saving = true;
		try {
			await doDelete(path(selectedID));
			await refresh();
		} catch (e) {
			report(e);
		} finally {
			saving = false;
		}
	}

	async function send() {
		if (!ready || busy || !prompt.trim()) return;
		const text = prompt;
		const id = selectedID;
		prompt = '';
		error = '';
		activity = 'Thinking…';
		busy = true;
		stream = new AbortController();
		messages = [...messages, { role: 'user', text }, { role: 'assistant', text: '' }];
		const replyIndex = messages.length - 1;
		try {
			await doWithBody(
				'POST',
				`${path(id)}/chat`,
				{ prompt: text },
				{
					signal: stream.signal,
					dontLogErrors: true,
					responseHandler: async (response) => {
						if (!response.ok) return handleResponse(response, path(id), { dontLogErrors: true });
						if (!response.body) throw new Error('Agent returned an empty stream');
						const reader = response.body.getReader();
						const decoder = new TextDecoder();
						let pending = '';
						let finished = false;
						try {
							while (true) {
								const { value, done } = await reader.read();
								pending += decoder.decode(value, { stream: !done });
								const lines = pending.split('\n');
								pending = lines.pop() ?? '';
								for (const line of lines) {
									if (!line.trim()) continue;
									const event = JSON.parse(line);
									if (event.type === 'text') {
										messages[replyIndex].text += event.text;
										activity = '';
									}
									if (event.type === 'tool') activity = `Using ${event.name}…`;
									if (event.type === 'error') error = event.error;
									if (event.type === 'done') {
										finished = true;
										messages[replyIndex].interrupted = !event.completed;
									}
								}
								if (done) break;
							}
							if (!finished)
								throw new Error(
									'Connection ended before the turn completed. Refresh the conversation before retrying.'
								);
						} finally {
							reader.releaseLock();
						}
					}
				}
			);
		} catch (e) {
			report(e);
		} finally {
			busy = false;
			stream?.abort();
			stream = undefined;
			activity = '';
			historyID = '';
			await refresh();
		}
	}

	async function cancel() {
		try {
			await doPost(`${path(selectedID)}/cancel`, {});
		} catch (e) {
			report(e);
		}
	}
</script>

<svelte:head><title>Agents · Obot</title></svelte:head>

<div class="mx-auto flex h-full max-w-6xl flex-col gap-5 overflow-auto p-6">
	<h1 class="text-2xl font-semibold">
		Agents <span class="text-sm font-normal text-muted-foreground">POC</span>
	</h1>
	{#if error}<p role="alert" class="rounded border border-destructive p-3 text-destructive">
			{error}
		</p>{/if}
	{#if enabled === undefined}<p>Loading…</p>
	{:else if !enabled}<p>The agent POC is not enabled on this server.</p>
	{:else}
		<div class="grid gap-6 md:grid-cols-[18rem_1fr]">
			<aside class="space-y-5">
				<form
					class="space-y-3 rounded border p-4"
					onsubmit={(event) => {
						event.preventDefault();
						void create();
					}}
				>
					<h2 class="font-semibold">Create an agent</h2>
					<label class="block text-sm"
						>Name<input
							class="mt-1 w-full rounded border bg-background p-2"
							bind:value={displayName}
							required
							maxlength="128"
						/></label
					>
					<label class="block text-sm"
						>Model<select
							class="mt-1 w-full rounded border bg-background p-2"
							bind:value={model}
							required
							><option value="">Choose a Claude model</option
							>{#each models as option (option.id)}<option value={option.id}
									>{option.displayName || option.name || option.id}</option
								>{/each}</select
						></label
					>
					<label class="block text-sm"
						>MCP server IDs (optional)<input
							class="mt-1 w-full rounded border bg-background p-2"
							bind:value={serverIDs}
							placeholder="Comma-separated IDs"
						/></label
					>
					<button
						class="rounded border px-3 py-2 hover:bg-muted disabled:opacity-50"
						type="submit"
						disabled={saving || busy || !model}>Create</button
					>
				</form>
				<nav aria-label="Your agents" class="space-y-2">
					{#each instances as instance (instance.metadata.name)}
						<button
							class="block w-full rounded border p-3 text-left hover:bg-muted disabled:opacity-50"
							class:bg-muted={selectedID === instance.metadata.name}
							disabled={busy}
							onclick={() => {
								selectedID = instance.metadata.name;
								messages = [];
								historyID = '';
								error = '';
								void refresh();
							}}
						>
							<div class="font-medium">{instance.spec.displayName}</div>
							<div class="text-xs text-muted-foreground">
								{instance.metadata.deletionTimestamp
									? 'Deleting'
									: instance.status.state || 'Pending'}
							</div>
						</button>
					{:else}<p class="text-sm text-muted-foreground">No agents yet.</p>{/each}
				</nav>
			</aside>
			<section
				class="flex min-h-[30rem] flex-col gap-4 rounded border p-4"
				aria-label="Agent conversation"
			>
				{#if selected}
					<div class="flex flex-wrap items-center gap-2">
						<h2 class="mr-auto font-semibold">{selected.spec.displayName}</h2>
						<button
							class="rounded border px-3 py-2 hover:bg-muted disabled:opacity-50"
							disabled={saving || !!selected.metadata.deletionTimestamp}
							onclick={() => changeState(!selected.spec.suspended)}
							>{selected.spec.suspended ? 'Resume' : 'Suspend'}</button
						><button
							class="rounded border px-3 py-2 hover:bg-muted disabled:opacity-50"
							disabled={saving || busy || !!selected.metadata.deletionTimestamp}
							onclick={remove}>Delete</button
						>
					</div>
					{#if selected.status.error}<p role="status" class="text-sm text-muted-foreground">
							{selected.status.error}
						</p>{/if}
					{#if !ready}<p role="status" class="text-sm">
							{selected.metadata.deletionTimestamp
								? 'Deleting…'
								: selected.spec.suspended
									? 'Suspended or suspending. Resume to chat.'
									: `Waiting for agent: ${selected.status.state || 'Pending'}`}
						</p>{/if}
					<div class="flex-1 space-y-4" role="log" aria-label="Messages">
						{#each messages as message, index (index)}<div class="rounded bg-muted/50 p-3">
								<p class="mb-1 text-xs font-semibold">
									{message.role === 'user' ? 'You' : 'Claude'}
								</p>
								<p class="whitespace-pre-wrap break-words">{message.text}</p>
								{#if message.interrupted}<p class="text-xs text-muted-foreground">
										Turn interrupted
									</p>{/if}
							</div>{/each}
					</div>
					<p role="status" class="text-sm text-muted-foreground">{activity}</p>
					<form
						class="space-y-2"
						onsubmit={(event) => {
							event.preventDefault();
							void send();
						}}
					>
						<label class="block text-sm"
							>Message<textarea
								class="mt-1 w-full rounded border bg-background p-3"
								bind:value={prompt}
								rows="3"
								maxlength="32768"
								disabled={!ready || busy}
								required></textarea></label
						>
						<div class="flex gap-2">
							<button
								class="rounded border px-3 py-2 hover:bg-muted disabled:opacity-50"
								type="submit"
								disabled={!ready || busy || !prompt.trim()}>Send</button
							><button
								class="rounded border px-3 py-2 hover:bg-muted disabled:opacity-50"
								type="button"
								disabled={!selected || !!selected.metadata.deletionTimestamp}
								onclick={cancel}>Stop turn</button
							>
						</div>
					</form>
				{:else}<p class="text-muted-foreground">
						Create an agent to start chatting with Claude Code.
					</p>{/if}
			</section>
		</div>
	{/if}
</div>
