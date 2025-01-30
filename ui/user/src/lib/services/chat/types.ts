export interface Progress {
	runID?: string;
	parentRunID?: string;
	time: string;
	content: string;
	contentID?: string;
	input?: string;
	inputIsStepTemplateInput?: boolean;
	stepTemplateInvoke?: StepTemplateInvoke;
	step?: Step;
	prompt?: Prompt;
	toolInput?: ToolInput;
	toolCall?: ToolCall;
	workflowCall?: WorkflowCall;
	waitingOnModel?: boolean;
	error?: string;
	runComplete?: boolean;
	replayComplete?: boolean;
}

export interface Step {
	id: string;
}

type StepTemplateInvoke = {
	name?: string;
	description?: string;
	args?: { [key: string]: string };
	result?: string;
};

type Prompt = {
	id?: string;
	name?: string;
	description?: string;
	time: string;
	message?: string;
	fields?: string[];
	sensitive?: boolean;
	metadata?: { [key: string]: string };
};

type ToolInput = {
	name?: string;
	description?: string;
	input?: string;
	metadata?: { [key: string]: string };
};

type ToolCall = {
	name?: string;
	description?: string;
	input?: string;
	output?: string;
	metadata?: { [key: string]: string };
};

type WorkflowCall = {
	name?: string;
	description?: string;
	threadID?: string;
	workflowID?: string;
	input?: string;
};

export interface Message {
	runID: string;
	parentRunID?: string;
	time?: Date;
	sent?: boolean;
	aborted?: boolean;
	icon?: string;
	tool?: boolean;
	toolCall?: ToolCall;
	toolInput?: boolean;
	sourceName: string;
	sourceDescription?: string;
	done?: boolean;
	ignore?: boolean;
	message: string[];
	explain?: Explain;
	file?: MessageFile;
	oauthURL?: string;
	contentID?: string;
}

export interface InvokeInput {
	prompt?: string;
	explain?: Explain;
	improve?: Explain;
	changedFiles?: Record<string, string>;
}

export interface Explain {
	filename: string;
	selection: string;
}

export interface MessageFile {
	filename: string;
	content: string;
}

export interface ToolInfo {
	name: string;
	description: string;
	metadata: { [key: string]: string };
}

export interface InputMessage {
	prompt: string;
	type: string;
}

export interface Messages {
	lastRunID?: string;
	messages: Message[];
	inProgress: boolean;
}

export interface Version {
	emailDomain?: string;
	dockerSupported?: boolean;
}

export interface Profile {
	email: string;
	iconURL: string;
	role: number;
	loaded?: boolean;
	isAdmin?: () => boolean;
	unauthorized?: boolean;
}

export interface Files {
	items: File[];
}

export interface File {
	name: string;
}

export interface KnowledgeFiles {
	items: KnowledgeFile[];
}

export interface KnowledgeFile {
	deleted?: string;
	fileName: string;
	state: string;
	error?: string;
}

export interface IngestionStatus {
	status: string;
}

export interface Assistants {
	items: Assistant[];
}

export interface AssistantIcons {
	icon?: string;
	iconDark?: string;
	collapsed?: string;
	collapsedDark?: string;
}

export interface Assistant {
	id: string;
	default?: boolean;
	name?: string;
	description?: string;
	current?: boolean;
	icons: AssistantIcons;
	starterMessages?: string[];
	introductionMessage?: string;
}

export interface AssistantTool {
	id: string;
	name?: string;
	description?: string;
	icon?: string;
	enabled?: boolean;
	builtin?: boolean;
	toolType?: string;
	image?: string;
	instructions?: string;
	context?: string;
	params?: Record<string, string>;
}

export interface AssistantToolList {
	readonly?: boolean;
	items: AssistantTool[];
}

export interface Credential {
	name: string;
}

export interface CredentialList {
	items: Credential[];
}

export interface TaskStep {
	id: string;
	step?: string;
}

export interface Task {
	id: string;
	name?: string;
	description?: string;
	steps: TaskStep[];
	schedule?: Schedule;
	email?: object;
	webhook?: object;
	onDemand?: OnDemand;
	alias?: string;
}

export interface OnDemand {
	params?: Record<string, string>;
}

export interface Schedule {
	interval: string;
	hour: number;
	minute: number;
	day: number;
	weekday: number;
}

export interface TaskList {
	items: Task[];
}

export interface TaskRun {
	id: string;
	created: string;
	taskID: string;
	task: Task;
	startTime?: string;
	endTime?: string;
	input?: string;
	error?: string;
}

export interface TaskRunList {
	items: TaskRun[];
}

export interface TableList {
	tables?: Table[];
}

export interface Table {
	name: string;
}

export interface Rows {
	columns: string[];
	rows: Record<string, unknown>[];
}

export interface Context {
	assistantID: string;
	projectID: string;
	valid?: boolean;
}
