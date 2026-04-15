# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Test

```sh
go build ./...              # Build all packages
go test ./...               # Run full test suite
go test ./internal/tools/   # Run tests for a single package
go test ./internal/tools/ -run TestGrepToolFindsPattern  # Run a single test
go test -v ./...            # Verbose output
```

Run the CLI: `go run ./cmd/yak` (requires an OpenAI-compatible API, defaults to `http://localhost:1234`).

Environment variables: `YAK_BASE_URL` (API endpoint), `YAK_MODEL` (model name, default `"default"`).

iMessage channel environment variables (all optional; channel is disabled unless both URL and password are set):
`YAK_IMESSAGE_SERVER_URL`, `YAK_IMESSAGE_PASSWORD`, `YAK_IMESSAGE_WEBHOOK_PORT` (default `8421`), `YAK_IMESSAGE_WEBHOOK_PATH` (default `/bluebubbles`), `YAK_IMESSAGE_OWNER_HANDLES` (comma-separated), `YAK_IMESSAGE_GROUP_TAG` (e.g. `@yak`).

Discord channel environment variables (channel is disabled unless `YAK_DISCORD_TOKEN` is set):
`YAK_DISCORD_TOKEN` (bot token), `YAK_DISCORD_OWNER_IDS` (comma-separated Discord user snowflakes), `YAK_DISCORD_GUILD_TAG` (e.g. `@yak` — required in guild channels unless the bot is @mentioned; DMs bypass the check).

## Architecture

**Agent loop** (`internal/cli/runner.go`): The core cycle is `Runner.agentLoop()`. It calls the LLM, dispatches any tool calls through the `Registry`, appends results as `"tool"` role messages, and loops until the model responds with text. If the model returns empty content after tool calls, the runner retries up to 2 times with a follow-up nudge message.

**Tool interface** (`internal/tools/types.go`): Every tool implements `Tool` — `Definition()` returns metadata and JSON schema, `Execute(ctx, json.RawMessage)` returns `(ToolResult, error)`. Tool-level errors (bad args, file not found) are returned as `ToolResult{IsError: true}` via the `errorResult`/`errorResultf` helpers, not as Go errors.

**Tool hooks** (`internal/tools/types.go`): `ToolHook` interface with `BeforeToolCall` (can block execution by returning a reason string) and `AfterToolCall`. Hooks are registered on the `Registry` and invoked by the runner around each tool execution.

**Filesystem abstraction** (`internal/tools/fs.go`): Tools that touch files (read, write, edit, ls) take an `FS` interface. Production uses `OSFS{}`. Tools that walk directories (grep, find, bash) use `os` directly.

**Skills** (`internal/skills/`): Skills are markdown files (`SKILL.md`) with YAML frontmatter that provide specialized instructions to the LLM. `LoadSkills(dirs...)` scans directories for `SKILL.md` files, parses frontmatter (name, description, disable-model-invocation), and returns validated `Skill` structs. Skills are discovered from `~/.yak/skills/` (user-level) and `.yak/skills/` (project-level). First skill with a given name wins on collision. Users invoke skills with `/skill:<name> [args]`, which the runner expands to the skill file content before sending to the LLM. Skills with `disable-model-invocation: true` are excluded from the system prompt but remain invocable via `/skill:`.

**System prompt** (`internal/prompt/system.go`): `BuildSystemPrompt(tools, skills, env)` assembles the prompt from sections: environment info (OS, arch, workspace, time), per-tool guidelines, conditional tool-selection rules, and an `<available_skills>` XML block listing visible skills with their name, description, and file location.

**LLM client** (`internal/llm/client.go`): `ChatClient` interface with a single `Chat()` method. The concrete `Client` posts to `/v1/chat/completions` (OpenAI-compatible format).

**Channel interface** (`internal/channel/channel.go`): `Channel` has `Name() string`, `Listen(ctx, out chan<- Inbound) error`, and `Send(ctx, Outbound) error`. The `Dispatcher` fans out to all registered channels, routes each inbound message to a conversation keyed by `(Channel, Thread)`, and calls back `Send` on the originating channel with the reply.

**iMessage channel** (`internal/channel/imessage/`): Receives messages via an HTTP webhook that imessage-rs POSTs to (compatible with BlueBubbles API). Sends replies via `POST /api/v1/message/text`. Only forwards messages from `OwnerHandles`; group messages additionally require `GroupTag`. Outbound text has Markdown stripped (`stripMarkdown`) and runner meta-lines like `[tokens: ...]` filtered (`dropMetaLines`) before sending. Handle normalization (`normalizeHandle`) strips service prefixes and normalises phone formatting. When imessage-rs sends an empty `chats` array, the chatGuid is constructed as `iMessage;-;<handle>`. The `imessage_send` tool (`internal/tools/imessage_send.go`) lets the agent proactively send messages.

**Discord channel** (`internal/channel/discord/`): Uses `github.com/bwmarrin/discordgo` to receive messages via the Discord gateway (websocket) and send via REST. Only forwards messages from `OwnerIDs`; guild channels additionally require either an @mention of the bot or the literal `GuildTag` in the message body, which is stripped before dispatch. DMs bypass the tag check. The `Thread` is the Discord channel ID. Outbound meta-lines (`[tokens: ...]`) are filtered and messages over 2000 chars are chunked. The `discord_send` tool (`internal/tools/discord_send.go`) lets the agent proactively post to any channel or DM a user (it opens a DM channel automatically via `UserChannelCreate` when `kind=user`).

## Conventions

- **Minimal external dependencies.** Prefer the standard library. External dependencies are allowed when they earn their keep — pick focused, small, well-maintained packages over reimplementing non-trivial logic. Avoid dependencies that pull large transitive trees, force CGo on the build, or reimplement something the stdlib already does well.
- **Tool parameter structs** are named `<ToolName>Params` with JSON tags matching the schema field names exactly. Unmarshal with `json.Unmarshal(raw, &params)`.
- **Tool definitions** are module-level vars (`var bashDefinition = ToolDefinition{...}`), not generated at runtime.
- **Directory skipping**: grep and find skip `.git`, `node_modules`, `__pycache__`, `.cache` via `filepath.SkipDir`.
- **Output truncation**: Tools enforce default limits (read: 2000 lines, grep: 100 matches, ls: 500 entries, find: 1000 results) and append a `[... limit reached]` marker when truncated.
- **Skill files** are `SKILL.md` with YAML frontmatter (`name`, `description`, `disable-model-invocation`). Name must be lowercase alphanumeric with hyphens, max 64 chars. Description is required.
- **Tests** use `t.TempDir()` for filesystem tests, `httptest.NewServer` for HTTP tests, and `json.Marshal(Params{...})` to build tool input. A shared `writeFile` helper exists in `grep_test.go` and `loader_test.go` for creating temp files.
