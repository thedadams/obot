<script lang="ts">
	import type { Project } from '$lib/services';
	import CopyButton from '$lib/components/CopyButton.svelte';
	import {
		configureProjectSlack,
		disableProjectSlack,
		getProjectSlack
	} from '$lib/services/chat/operations';
	import { ChatService, type AssistantTool } from '$lib/services';
	import { Settings, X } from 'lucide-svelte';
	import { closeSidebarConfig, getLayout } from '$lib/context/layout.svelte';
	import { responsive } from '$lib/stores';
	import { getProjectTools } from '$lib/context/projectTools.svelte';
	import { clickOutside } from '$lib/actions/clickoutside';
	import { tooltip } from '$lib/actions/tooltip.svelte';
	import CollapsePane from '$lib/components/edit/CollapsePane.svelte';
	import Toggle from '$lib/components/Toggle.svelte';
	import { fade } from 'svelte/transition';
	import { openTask } from '$lib/context/layout.svelte';
	import type { Task } from '$lib/services';
	import type { ProjectCredential } from '$lib/services/chat/types';
	import CredentialAuth from '$lib/components/edit/CredentialAuth.svelte';

	interface Props {
		project: Project;
		inline?: boolean;
	}

	const projectTools = getProjectTools();
	function getSelectionMap() {
		return projectTools.tools
			.filter((t) => !t.builtin)
			.reduce<Record<string, AssistantTool>>((acc, tool) => {
				acc[tool.id] = { ...tool };
				return acc;
			}, {});
	}
	const layout = getLayout();

	let toolSelection = $state<Record<string, AssistantTool>>({});

	toolSelection = getSelectionMap();

	let { project = $bindable(), inline }: Props = $props();
	let dialog: HTMLDialogElement;
	let confirmDisable: HTMLDialogElement;
	let addSlackBotTool: HTMLDialogElement;
	let redirectUrl = $state('');
	let eventUrl = $state('');
	let config = $state({
		appId: '',
		clientId: '',
		clientSecret: '',
		signingSecret: ''
	});
	let slackEnabled = $derived(project.capabilities?.onSlackMessage);
	let showSteps = $state(!project.capabilities?.onSlackMessage);
	let taskDialog: HTMLDialogElement | undefined = $state();
	let task = $state<Task | undefined>();
	let authDialog: ReturnType<typeof CredentialAuth> | undefined = $state();
	let credToAuth = $state<ProjectCredential | undefined>();
	let credentials = $state<ProjectCredential[]>([]);

	$effect(() => {
		ChatService.listProjectLocalCredentials(project.assistantID, project.id).then((creds) => {
			credentials = creds.items;
			credToAuth = credentials.find((c) => c.toolID === 'slack-bot-bundle');
		});
	});

	$effect(() => {
		redirectUrl = `${window.location.protocol}//${window.location.host}/api/app-oauth/callback/oa1t1${project.id.slice(2, 8)}`;
		eventUrl = `${window.location.protocol}//${window.location.host}/api/slack/events`;
	});

	async function getSlackConfig() {
		if (slackEnabled) {
			try {
				const response = await getProjectSlack(project.assistantID, project.id);
				config.appId = response.appId;
				config.clientId = response.clientId;
			} catch (error) {
				console.error('Failed to get Slack config:', error);
			}
		}
	}

	$effect(() => {
		if (project) {
			getSlackConfig();
		}
	});

	async function handleSubmit() {
		let slackReceiver = await configureProjectSlack(
			project.assistantID,
			project.id,
			config,
			slackEnabled ? 'PUT' : 'POST'
		);
		project = await ChatService.getProject(project.id);
		dialog.close();
		addSlackBotTool.showModal();
		config.appId = slackReceiver.appId;
		config.clientId = slackReceiver.clientId;
		config.clientSecret = '';
		config.signingSecret = '';
	}

	async function disableSlack() {
		await disableProjectSlack(project.assistantID, project.id);
		project = await ChatService.getProject(project.id);
		project.capabilities = { onSlackMessage: false };
		confirmDisable.close();
		config.appId = '';
		config.clientId = '';
		config.clientSecret = '';
		config.signingSecret = '';
	}

	async function configureSlackTool() {
		addSlackBotTool?.close();
		if (toolSelection['slack-bot-bundle'] && !toolSelection['slack-bot-bundle'].enabled) {
			toolSelection['slack-bot-bundle'].enabled = true;
			projectTools.tools = Object.values(toolSelection);
			await ChatService.updateProjectTools(project.assistantID, project.id, {
				items: Object.values(toolSelection)
			});
		}

		// Wait for the Slack workflow to be created
		let maxAttempts = 30;
		let attempts = 0;

		while (attempts < maxAttempts) {
			attempts++;
			project = await ChatService.getProject(project.id);

			if (project.workflowNameFromIntegration) {
				layout.tasks = (await ChatService.listTasks(project.assistantID, project.id)).items;
				task = layout.tasks.find((t) => t.id === project.workflowNameFromIntegration);
				if (task) {
					authDialog?.show();
				}

				if (!project.sharedTasks) {
					project.sharedTasks = [];
				}
				if (!project.sharedTasks.includes(project.workflowNameFromIntegration)) {
					project.sharedTasks.push(project.workflowNameFromIntegration);
					project = await ChatService.updateProject(project);
				}
				break;
			}

			await new Promise((resolve) => setTimeout(resolve, 1000));
		}
	}
</script>

{#if !inline}
	<div class="flex flex-col gap-2">
		<div class="mb-1 flex items-center justify-between">
			<div class="flex h-7 items-center gap-1">
				<p class="grow text-sm font-semibold">Slack Integration</p>
				{#if slackEnabled}
					<button
						class="icon-button min-h-auto min-w-auto p-1"
						onclick={() => dialog.showModal()}
						use:tooltip={'Modify Slack Integration'}
						transition:fade
					>
						<Settings class="size-5" />
					</button>
				{/if}
			</div>
			<Toggle
				label="Toggle Slack Integration"
				checked={!!slackEnabled}
				onChange={() => {
					if (!slackEnabled) {
						dialog.showModal();
					} else {
						disableSlack();
					}
				}}
			/>
		</div>

		<p class="text-xs text-gray-500">
			Enable this to trigger tasks from Slack messages that mention the slack bot you configured
			with your agent.
		</p>
	</div>
{:else}
	<div class="flex w-full flex-col">
		<div class="flex w-full justify-center px-4 py-4 md:px-8">
			<div class="flex w-full flex-col gap-4 md:max-w-[1200px]">
				<div class="flex w-full items-center justify-between">
					<h4 class="text-xl font-semibold">Slack</h4>
					<button
						onclick={() => closeSidebarConfig(layout)}
						class="icon-button"
						use:tooltip={'Exit Slack Configuration'}
					>
						<X class="size-6" />
					</button>
				</div>

				<div class="pr-2.5">
					<CollapsePane
						header="Configure Slack Integration"
						open={showSteps}
						classes={{
							header: 'font-semibold px-0',
							content: 'bg-transparent px-0 shadow-none',
							headerText: 'text-base font-normal'
						}}
					>
						{@render steps()}
					</CollapsePane>
					<div class="w-full">
						{@render form()}
					</div>
				</div>
			</div>
		</div>
	</div>
{/if}

<dialog
	bind:this={dialog}
	use:clickOutside={() => dialog?.close()}
	class="default-dialog md:w-1/2"
	class:mobile-screen-dialog={responsive.isMobile}
>
	<div class="p-6">
		<div class="flex flex-col gap-2">
			<button class="absolute top-0 right-0 p-3" onclick={() => dialog?.close()}>
				<X class="icon-default" />
			</button>
			<h3 class="mb-4 text-lg font-semibold">Configure Slack OAuth App</h3>
			{@render steps()}

			<div>
				{@render form()}
			</div>
		</div>
	</div>
</dialog>

<dialog bind:this={confirmDisable} class="modal" use:clickOutside={() => confirmDisable?.close()}>
	<div class="modal-box">
		<div class="p-4">
			<h3 class="text-lg font-medium">Disable Slack Integration</h3>
			<p class="mt-2 text-sm text-gray-500">
				Are you sure you want to disable Slack integration? This will remove the Slack trigger from
				this project.
			</p>

			<div class="mt-6 flex justify-end gap-3">
				<button
					class="button"
					onclick={() => {
						confirmDisable.close();
					}}
				>
					Cancel
				</button>
				<button
					class="button bg-red-500 text-white hover:bg-red-600"
					onclick={async () => {
						await disableSlack();
					}}
				>
					Disable
				</button>
			</div>
		</div>
	</div>
</dialog>

<dialog bind:this={addSlackBotTool} class="default-dialog">
	<div class="p-6">
		<h3 class="mb-4 text-lg font-semibold">Next Steps</h3>
		<div class="space-y-4">
			<p class="text-sm text-gray-600">
				We'll add the Slack Bot tool to your project and automatically create a task that can be
				triggered from Slack messages. This will allow your agent to:
			</p>
			<ul class="list-disc pl-6 text-sm text-gray-600">
				<li>Automatically respond when mentioned in Slack</li>
				<li>Process messages and take actions</li>
				<li>Send responses back to the Slack conversation</li>
			</ul>

			<div class="mt-6 flex justify-end gap-3">
				<button class="button-primary" onclick={configureSlackTool}> Continue </button>
			</div>
		</div>
	</div>
</dialog>

<dialog bind:this={taskDialog}>
	<div class="modal-box">
		<div class="p-4">
			<h3 class="text-lg font-medium">Task Created</h3>
			<p class="mt-2 text-sm text-gray-500">
				Task "{task?.name}" has been created from the Slack integration.
			</p>

			<div class="mt-6 flex justify-end gap-3">
				<button
					class="button"
					onclick={() => {
						taskDialog?.close();
						openTask(layout, task?.id);
					}}
				>
					Go to Task
				</button>
			</div>
		</div>
	</div>
</dialog>

<CredentialAuth
	bind:this={authDialog}
	credential={credToAuth}
	{project}
	local={true}
	toolID="slack-bot-bundle"
	onClose={() => {
		credToAuth = undefined;
		taskDialog?.showModal();
	}}
/>

{#snippet steps()}
	<div class="space-y-6">
		<p class="text-sm text-gray-600">All steps will be performed on the Slack API Dashboard.</p>

		<div class="space-y-4">
			<div>
				<h4 class="font-medium">Step 1: Create a Slack App</h4>
				<p class="text-sm text-gray-600">
					If you've already created a Slack app, you can skip this step.
				</p>
			</div>

			<div>
				<h4 class="font-medium">Step 2: Add the Redirect URL</h4>
				<p class="text-sm text-gray-600">
					From the Slack API Dashboard, click on your app and select "OAuth & Permissions"
				</p>
				<p class="text-sm text-gray-600">
					In the "Redirect URLs" section, click "Add New Redirect URL"
				</p>
				<div
					class="mt-2 flex max-w-fit items-center gap-2 rounded bg-gray-100 p-2 dark:bg-gray-800"
				>
					<CopyButton text={redirectUrl} />
					{redirectUrl}
				</div>
			</div>

			<div>
				<h4 class="font-medium">Step 3: Enable Events</h4>
				<p class="text-sm text-gray-600">
					Navigate to the "Event Subscriptions" tab from the sidebar
				</p>
				<p class="text-sm text-gray-600">Enable events and add the Request URL below:</p>
				<div
					class="mt-2 flex max-w-fit items-center gap-2 rounded bg-gray-100 p-2 dark:bg-gray-800"
				>
					<CopyButton text={eventUrl} />
					{eventUrl}
				</div>
				<p class="mt-2 text-sm text-gray-600">
					Under "Subscribe to bot events", add the following events:
				</p>
				<div
					class="mt-2 flex max-w-fit items-center gap-2 rounded bg-gray-100 p-2 dark:bg-gray-800"
				>
					<CopyButton text={'app_mention'} />
					app_mention
				</div>
			</div>

			<div>
				<h4 class="font-medium">Step 4: Add Bot Scopes</h4>
				<p class="text-sm text-gray-600">
					Navigate to the "OAuth & Permissions" tab from the sidebar
				</p>
				<p class="text-sm text-gray-600">
					Locate the "Bot Token Scopes" section and add the following scopes:
				</p>
				<div class="mt-2 flex flex-wrap gap-1">
					<div
						class="flex max-w-fit items-center gap-2 rounded-full bg-gray-100 px-3 py-1 dark:bg-gray-800"
					>
						<CopyButton text="channels:history" />
						channels:history
					</div>
					<div
						class="flex max-w-fit items-center gap-2 rounded-full bg-gray-100 px-3 py-1 dark:bg-gray-800"
					>
						<CopyButton text="groups:history" />
						groups:history
					</div>
					<div
						class="flex max-w-fit items-center gap-2 rounded-full bg-gray-100 px-3 py-1 dark:bg-gray-800"
					>
						<CopyButton text="im:history" />
						im:history
					</div>
					<div
						class="flex max-w-fit items-center gap-2 rounded-full bg-gray-100 px-3 py-1 dark:bg-gray-800"
					>
						<CopyButton text="mpim:history" />
						mpim:history
					</div>
					<div
						class="flex max-w-fit items-center gap-2 rounded-full bg-gray-100 px-3 py-1 dark:bg-gray-800"
					>
						<CopyButton text="channels:read" />
						channels:read
					</div>
					<div
						class="flex max-w-fit items-center gap-2 rounded-full bg-gray-100 px-3 py-1 dark:bg-gray-800"
					>
						<CopyButton text="files:read" />
						files:read
					</div>
					<div
						class="flex max-w-fit items-center gap-2 rounded-full bg-gray-100 px-3 py-1 dark:bg-gray-800"
					>
						<CopyButton text="im:read" />
						im:read
					</div>
					<div
						class="flex max-w-fit items-center gap-2 rounded-full bg-gray-100 px-3 py-1 dark:bg-gray-800"
					>
						<CopyButton text="team:read" />
						team:read
					</div>
					<div
						class="flex max-w-fit items-center gap-2 rounded-full bg-gray-100 px-3 py-1 dark:bg-gray-800"
					>
						<CopyButton text="users:read" />
						users:read
					</div>
					<div
						class="flex max-w-fit items-center gap-2 rounded-full bg-gray-100 px-3 py-1 dark:bg-gray-800"
					>
						<CopyButton text="groups:read" />
						groups:read
					</div>
					<div
						class="flex max-w-fit items-center gap-2 rounded-full bg-gray-100 px-3 py-1 dark:bg-gray-800"
					>
						<CopyButton text="chat:write" />
						chat:write
					</div>
					<div
						class="flex max-w-fit items-center gap-2 rounded-full bg-gray-100 px-3 py-1 dark:bg-gray-800"
					>
						<CopyButton text="groups:write" />
						groups:write
					</div>
					<div
						class="flex max-w-fit items-center gap-2 rounded-full bg-gray-100 px-3 py-1 dark:bg-gray-800"
					>
						<CopyButton text="mpim:write" />
						mpim:write
					</div>
					<div
						class="flex max-w-fit items-center gap-2 rounded-full bg-gray-100 px-3 py-1 dark:bg-gray-800"
					>
						<CopyButton text="im:write" />
						im:write
					</div>
					<div
						class="flex max-w-fit items-center gap-2 rounded-full bg-gray-100 px-3 py-1 dark:bg-gray-800"
					>
						<CopyButton text="assistant:write" />
						assistant:write
					</div>
				</div>
			</div>
		</div>

		<div>
			<h4 class="font-medium">Step 5: Register OAuth App in your agent</h4>
			<p class="text-sm text-gray-600">
				Click the Basic Information section in the side nav, locate the Client ID and Client Secret
				fields, copy/paste them into the form below, and click Submit.
			</p>
		</div>
	</div>
{/snippet}

{#snippet form()}
	<div class="space-y-3">
		<div>
			<label for="appIdLabel" class="text-sm font-medium">App ID</label>
			<input
				type="text"
				id="appId"
				class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
				placeholder="Enter App ID"
				bind:value={config.appId}
				oninput={(e) => (config.appId = e.currentTarget.value)}
			/>
		</div>

		<div>
			<label for="clientIdLabel" class="text-sm font-medium">Client ID</label>
			<input
				type="text"
				id="clientId"
				class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
				placeholder="Enter Client ID"
				bind:value={config.clientId}
				oninput={(e) => (config.clientId = e.currentTarget.value)}
			/>
		</div>

		<form>
			<label for="clientSecretLabel" class="text-sm font-medium">Client Secret</label>
			<input
				type="password"
				id="clientSecret"
				class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
				placeholder={slackEnabled ? '***********' : 'Enter Client Secret'}
				autocomplete="off"
				bind:value={config.clientSecret}
				oninput={(e) => (config.clientSecret = e.currentTarget.value)}
			/>
		</form>

		<form>
			<label for="signingSecretLabel" class="text-sm font-medium">Signing Secret</label>
			<input
				type="password"
				id="signingSecret"
				class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
				placeholder={slackEnabled ? '***********' : 'Enter Signing Secret'}
				autocomplete="off"
				bind:value={config.signingSecret}
				oninput={(e) => (config.signingSecret = e.currentTarget.value)}
			/>
		</form>

		<div class="mt-6 flex justify-end gap-3">
			{#if project.capabilities?.onSlackMessage}
				<button
					class="button bg-red-500 text-white hover:bg-red-600"
					onclick={() => {
						dialog.close();
						confirmDisable?.showModal();
					}}
				>
					Remove Configuration
				</button>
			{/if}
			<button class="button" onclick={handleSubmit}> Configure </button>
		</div>
	</div>
{/snippet}
