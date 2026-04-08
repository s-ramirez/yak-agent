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

## Architecture

**Agent loop** (`internal/cli/runner.go`): The core cycle is `Runner.agentLoop()`. It calls the LLM, dispatches any tool calls through the `Registry`, appends results as `"tool"` role messages, and loops until the model responds with text. If the model returns empty content after tool calls, the runner retries up to 2 times with a follow-up nudge message.

**Tool interface** (`internal/tools/types.go`): Every tool implements `Tool` — `Definition()` returns metadata and JSON schema, `Execute(ctx, json.RawMessage)` returns `(ToolResult, error)`. Tool-level errors (bad args, file not found) are returned as `ToolResult{IsError: true}` via the `errorResult`/`errorResultf` helpers, not as Go errors.

**Tool hooks** (`internal/tools/types.go`): `ToolHook` interface with `BeforeToolCall` (can block execution by returning a reason string) and `AfterToolCall`. Hooks are registered on the `Registry` and invoked by the runner around each tool execution.

**Filesystem abstraction** (`internal/tools/fs.go`): Tools that touch files (read, write, edit, ls) take an `FS` interface. Production uses `OSFS{}`. Tools that walk directories (grep, find, bash) use `os` directly.

**Skills** (`internal/skills/`): Skills are markdown files (`SKILL.md`) with YAML frontmatter that provide specialized instructions to the LLM. `LoadSkills(dirs...)` scans directories for `SKILL.md` files, parses frontmatter (name, description, disable-model-invocation), and returns validated `Skill` structs. Skills are discovered from `~/.yak/skills/` (user-level) and `.yak/skills/` (project-level). First skill with a given name wins on collision. Users invoke skills with `/skill:<name> [args]`, which the runner expands to the skill file content before sending to the LLM. Skills with `disable-model-invocation: true` are excluded from the system prompt but remain invocable via `/skill:`.

**System prompt** (`internal/prompt/system.go`): `BuildSystemPrompt(tools, skills, env)` assembles the prompt from sections: environment info (OS, arch, workspace, time), per-tool guidelines, conditional tool-selection rules, and an `<available_skills>` XML block listing visible skills with their name, description, and file location.

**LLM client** (`internal/llm/client.go`): `ChatClient` interface with a single `Chat()` method. The concrete `Client` posts to `/v1/chat/completions` (OpenAI-compatible format).

## Conventions

- **No external dependencies.** The entire project uses only the Go standard library.
- **Tool parameter structs** are named `<ToolName>Params` with JSON tags matching the schema field names exactly. Unmarshal with `json.Unmarshal(raw, &params)`.
- **Tool definitions** are module-level vars (`var bashDefinition = ToolDefinition{...}`), not generated at runtime.
- **Directory skipping**: grep and find skip `.git`, `node_modules`, `__pycache__`, `.cache` via `filepath.SkipDir`.
- **Output truncation**: Tools enforce default limits (read: 2000 lines, grep: 100 matches, ls: 500 entries, find: 1000 results) and append a `[... limit reached]` marker when truncated.
- **Skill files** are `SKILL.md` with YAML frontmatter (`name`, `description`, `disable-model-invocation`). Name must be lowercase alphanumeric with hyphens, max 64 chars. Description is required.
- **Tests** use `t.TempDir()` for filesystem tests, `httptest.NewServer` for HTTP tests, and `json.Marshal(Params{...})` to build tool input. A shared `writeFile` helper exists in `grep_test.go` and `loader_test.go` for creating temp files.
