import http from "node:http";
import { timingSafeEqual } from "node:crypto";
import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import { query } from "@anthropic-ai/claude-agent-sdk";

const token = process.env.OBOT_AGENT_TOKEN;
const model = process.env.OBOT_AGENT_MODEL;
if (!token || !model || !process.env.ANTHROPIC_AUTH_TOKEN) {
  throw new Error("Missing agent credentials or model configuration");
}

const workspace = "/workspace";
const statePath = `${workspace}/conversation.json`;
await mkdir(`${workspace}/home/.claude`, { recursive: true });
await mkdir(`${workspace}/project`, { recursive: true });
/** @type {{sessionID?: string, quiesced?: boolean, messages: {role: string, text: string, interrupted?: boolean}[]}} */
let state = { messages: [] };
try {
  state = JSON.parse(await readFile(statePath, "utf8"));
} catch (error) {
  if (
    !(error instanceof Error) ||
    !("code" in error) ||
    error.code !== "ENOENT"
  )
    throw error;
}

const mcpServers = JSON.parse(process.env.OBOT_AGENT_MCPS || "{}");
const tools = ["Read", "Write", "Edit", "Glob", "Grep", "Bash"];
/** @type {AbortController | undefined} */
let active;
let accepting = !state.quiesced;
let storageFailed = false;
let pendingSave = Promise.resolve();

function save() {
  // ponytail: one conversation per actor; a database is unnecessary until we add multiple chats.
  const content = JSON.stringify(state);
  pendingSave = pendingSave
    .catch(() => {})
    .then(async () => {
      try {
        await writeFile(`${statePath}.tmp`, content, { mode: 0o600 });
        await rename(`${statePath}.tmp`, statePath);
        storageFailed = false;
      } catch (error) {
        storageFailed = true;
        throw error;
      }
    });
  return pendingSave;
}

/** @param {http.ServerResponse} response @param {number} code @param {unknown} body */
function json(response, code, body) {
  response.writeHead(code, {
    "Content-Type": "application/json",
    "Cache-Control": "no-store",
  });
  response.end(JSON.stringify(body));
}

/** @param {http.IncomingMessage} request */
async function readPrompt(request) {
  const chunks = [];
  let size = 0;
  for await (const chunk of request) {
    size += chunk.length;
    if (size > 64 * 1024) throw new Error("Request too large");
    chunks.push(chunk);
  }
  const { prompt } = JSON.parse(Buffer.concat(chunks).toString("utf8"));
  if (
    typeof prompt !== "string" ||
    !prompt.trim() ||
    prompt.length > 32 * 1024
  ) {
    throw new Error("A prompt of at most 32 KiB is required");
  }
  return prompt;
}

/** @param {http.IncomingMessage} request @param {http.ServerResponse} response */
async function chat(request, response) {
  let prompt;
  try {
    prompt = await readPrompt(request);
  } catch {
    return json(response, 400, { error: "Invalid or oversized prompt" });
  }
  // Check after reading the body, before yielding, so concurrent requests cannot both enter.
  if (active || !accepting)
    return json(response, 409, { error: "Agent is busy or suspending" });
  const abortController = new AbortController();
  active = abortController;
  response.on("close", () => abortController.abort());
  response.writeHead(200, {
    "Content-Type": "application/x-ndjson",
    "Cache-Control": "no-store",
  });
  response.flushHeaders();
  /** @param {object} event */
  const emit = (event) => {
    if (!response.destroyed) response.write(`${JSON.stringify(event)}\n`);
  };

  let reply = "";
  let completed = false;
  state.messages.push({ role: "user", text: prompt });
  try {
    await save();
    for await (const message of query({
      prompt,
      options: {
        cwd: `${workspace}/project`,
        model,
        resume: state.sessionID,
        abortController,
        includePartialMessages: true,
        persistSession: true,
        settingSources: [],
        tools,
        mcpServers,
        permissionMode: "default",
        // The sandbox is the execution boundary; no interactive permission UI in the POC.
        canUseTool: async (name, input) => {
          const allowed =
            tools.includes(name) ||
            Object.keys(mcpServers).some((id) =>
              name.startsWith(`mcp__${id}__`),
            );
          return allowed
            ? { behavior: "allow", updatedInput: input }
            : { behavior: "deny", message: "Tool unavailable in this POC" };
        },
        maxTurns: 50,
        env: {
          ...process.env,
          HOME: `${workspace}/home`,
          CLAUDE_CONFIG_DIR: `${workspace}/home/.claude`,
          CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: "1",
          ANTHROPIC_DEFAULT_HAIKU_MODEL: model,
          ANTHROPIC_DEFAULT_SONNET_MODEL: model,
          ANTHROPIC_DEFAULT_OPUS_MODEL: model,
        },
      },
    })) {
      if (message.type === "system" && message.subtype === "init") {
        state.sessionID = message.session_id;
        await save();
      }
      if (message.type === "stream_event") {
        const event = message.event;
        if (
          event.type === "content_block_delta" &&
          event.delta.type === "text_delta"
        ) {
          reply += event.delta.text;
          emit({ type: "text", text: event.delta.text });
        }
        if (
          event.type === "content_block_start" &&
          event.content_block.type === "tool_use"
        ) {
          emit({ type: "tool", name: event.content_block.name });
        }
      }
      if (message.type === "result") {
        state.sessionID = message.session_id;
        if (message.is_error) throw new Error("Agent turn failed");
        completed = true;
      }
    }
    if (!completed) throw new Error("Agent stream ended before completion");
  } catch {
    emit({
      type: "error",
      error: abortController.signal.aborted
        ? "Turn cancelled"
        : "Agent turn failed. Check the model and MCP access for the instance key.",
    });
  } finally {
    state.messages.push({
      role: "assistant",
      text: reply || "(No response)",
      interrupted: !completed,
    });
    // Bound the UI transcript. Claude's complete session stays in its own durable session files.
    state.messages = state.messages.slice(-200);
    try {
      await save();
      emit({ type: "done", completed });
    } catch {
      accepting = false;
      emit({
        type: "error",
        error:
          "Unable to persist the conversation; restart the agent after checking storage.",
      });
    }
    active = undefined;
    response.end();
  }
}

const server = http.createServer(async (request, response) => {
  try {
    if (request.method === "GET" && request.url === "/healthz")
      return json(response, 200, { ready: true });
    const supplied = Buffer.from(request.headers.authorization || "");
    const expected = Buffer.from(`Bearer ${token}`);
    if (
      supplied.length !== expected.length ||
      !timingSafeEqual(supplied, expected)
    ) {
      return json(response, 401, { error: "Unauthorized" });
    }

    if (request.method === "GET" && request.url === "/history")
      return json(response, 200, { messages: state.messages, busy: !!active });
    if (request.method === "POST" && request.url === "/chat")
      return await chat(request, response);
    if (request.method === "POST" && request.url === "/cancel") {
      active?.abort();
      response.writeHead(204).end();
      return;
    }
    if (request.method === "POST" && request.url === "/quiesce") {
      accepting = false;
      if (active) {
        response.writeHead(409).end();
        return;
      }
      // Persist the admission gate so a stale routed request cannot start a turn after restore.
      state.quiesced = true;
      await save();
      response.writeHead(204).end();
      return;
    }
    if (request.method === "POST" && request.url === "/activate") {
      if (storageFailed)
        return json(response, 503, {
          error: "Conversation storage is unavailable",
        });
      accepting = true;
      state.quiesced = false;
      response.writeHead(204).end();
      return;
    }
    json(response, 404, { error: "Not found" });
  } catch {
    if (!response.headersSent)
      json(response, 500, { error: "Runtime request failed" });
    else response.end();
  }
});
server.requestTimeout = 30_000;
server.listen(80, "0.0.0.0");
