<script lang="ts">
	import { closeSidebarConfig, getLayout } from '$lib/context/layout.svelte';
	import { ChatService, type Project, type ProjectMCP } from '$lib/services';
	import { isValidMcpConfig, type MCPServerInfo } from '$lib/services/chat/mcp';
	import { ChevronsRight, PencilLine, Server, X } from 'lucide-svelte';
	import { twMerge } from 'tailwind-merge';
	import { onMount } from 'svelte';
	import { errors } from '$lib/stores';
	import HostedMcpForm from '$lib/components/mcp/HostedMcpForm.svelte';
	import RemoteMcpForm from '$lib/components/mcp/RemoteMcpForm.svelte';

	interface Props {
		projectMcp?: ProjectMCP;
		project?: Project;
		onCreate?: (customMcpServerInfo: MCPServerInfo) => void;
		onUpdate?: (customMcpServerInfo: MCPServerInfo) => void;
	}
	let { projectMcp, project, onCreate, onUpdate }: Props = $props();

	function isObotHosted(projectMcp: ProjectMCP) {
		return (
			(projectMcp.env?.length ?? 0) > 0 ||
			(projectMcp.args?.length ?? 0) > 0 ||
			!!projectMcp.command
		);
	}

	const initConfig: MCPServerInfo = projectMcp
		? {
				...projectMcp,
				env:
					projectMcp.env?.map((env) => ({
						...env,
						value: ''
					})) ?? [],
				headers:
					projectMcp.headers?.map((header) => ({
						...header,
						value: ''
					})) ?? []
			}
		: {
				description: '',
				icon: '',
				name: '',
				env: [],
				args: [],
				command: '',
				url: '',
				headers: []
			};

	let config = $state<MCPServerInfo>({ ...initConfig });
	let showObotHosted = $state(projectMcp ? isObotHosted(projectMcp) : true);
	let showSubmitError = $state(false);
	const layout = getLayout();

	onMount(() => {
		if (projectMcp && project) {
			ChatService.revealProjectMCPEnvHeaders(project.assistantID, project.id, projectMcp.id)
				.then((response) => {
					if (config.env) {
						config.env = config.env.map((env) => ({
							...env,
							value: response?.[env.key] ?? env.value
						}));
					}
					if (config.headers) {
						config.headers = config.headers.map((header) => ({
							...header,
							value: response?.[header.key] ?? header.value
						}));
					}
				})
				.catch((err) => {
					// if 404, that's expected for reveal -- means no credentials were set
					if (
						(err instanceof Error && err.message.includes('404')) ||
						(typeof err === 'string' && err.includes('404'))
					) {
						return;
					}
					errors.append(err);
				});
		}
	});

	function init(isRemote?: boolean) {
		config = {
			...initConfig
		};
		showObotHosted = !isRemote;
	}

	async function handleSubmit() {
		if (!isValidMcpConfig(config)) {
			showSubmitError = true;
			return;
		}
		if (projectMcp) {
			onUpdate?.(config);
		} else {
			onCreate?.(config);
		}
	}
</script>

<div class="flex h-full w-full flex-col">
	<div class="mt-8 flex w-full flex-col gap-2 px-4 md:px-8">
		<div class="flex w-full flex-col gap-2 self-center md:max-w-[900px]">
			<div class="flex items-center justify-between gap-8">
				{#if projectMcp}
					<div class="flex flex-col gap-4">
						<div class="flex max-w-sm items-center gap-2">
							<div
								class="h-fit flex-shrink-0 self-start rounded-md bg-gray-50 p-1 dark:bg-gray-600"
							>
								{#if projectMcp.icon}
									<img src={projectMcp.icon} alt={projectMcp.name} class="size-6" />
								{:else}
									<Server class="size-6" />
								{/if}
							</div>
							<div class="flex flex-col gap-1">
								<h3 class="text-lg leading-4.5 font-semibold">
									{projectMcp.name || 'My Custom Server'}
								</h3>
							</div>
						</div>
						{#if projectMcp.description}
							<p class="mb-4 text-sm font-light text-gray-500">
								{projectMcp.description}
							</p>
						{/if}
					</div>
				{:else}
					<h3 class="flex items-center gap-2 text-xl font-semibold">
						<PencilLine class="size-5" /> Create MCP Config
					</h3>
				{/if}
				<button
					class="icon-button h-fit w-fit flex-shrink-0 self-start"
					onclick={() => closeSidebarConfig(layout)}
				>
					<X class="size-6" />
				</button>
			</div>
		</div>
	</div>

	{#if !projectMcp?.catalogID}
		<div
			class="dark:bg-gray-980 mt-4 flex w-full flex-col gap-2 bg-gray-50 px-4 pt-4 pb-2 shadow-inner md:px-8"
		>
			<div class="flex w-full self-center md:max-w-[900px]">
				<div class="flex w-full gap-1">
					<button
						class={twMerge(
							'dark:bg-gray-980 flex-1 bg-gray-50 py-3',
							showObotHosted &&
								'dark:bg-surface2 dark:border-surface3 rounded-md bg-white shadow-sm dark:border'
						)}
						onclick={() => init()}
					>
						Obot Hosted
					</button>
					<button
						class={twMerge(
							'dark:bg-gray-980 flex-1 bg-gray-50 py-3',
							!showObotHosted &&
								'dark:bg-surface2 dark:border-surface3 rounded-md bg-white shadow-sm dark:border'
						)}
						onclick={() => init(true)}
					>
						Remote
					</button>
				</div>
			</div>
		</div>
	{/if}

	<div
		class="dark:bg-gray-980 relative flex grow flex-col gap-4 bg-gray-50 px-4 pb-4 md:px-8"
		class:pt-4={projectMcp?.catalogID}
	>
		<div
			class="dark:bg-surface2 dark:border-surface3 flex w-full grow flex-col gap-4 self-center rounded-lg bg-white px-4 pt-12 pb-8 shadow-sm md:max-w-[900px] md:px-8 dark:border"
		>
			{#if showObotHosted}
				<HostedMcpForm bind:config {showSubmitError} custom={!projectMcp?.catalogID} />
			{:else}
				<RemoteMcpForm bind:config {showSubmitError} custom={!projectMcp?.catalogID} />
			{/if}
		</div>

		<div class="flex w-full flex-col gap-2 self-center md:max-w-[900px]">
			<button class="button-primary flex items-center gap-1 self-end" onclick={handleSubmit}>
				{projectMcp ? 'Update' : 'Configure'} server <ChevronsRight class="size-4" />
			</button>
		</div>
	</div>
</div>
