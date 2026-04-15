# yak-go

## Features

- Interactive CLI loop with an agentic tool-use cycle
- LM Studio-compatible `/v1/chat/completions` client
- Optional bearer-token auth for OpenAI-compatible endpoints
- Dynamic system prompt with environment info and tool-selection rules
- Built-in tools: `read`, `write`, `edit`, `bash`, `grep`, `ls`, `find`, `web_fetch`, `web_search`
- Plugin system with startup-registered plugins; `tilldone` is currently disabled
- Multi-provider sub-agents and customizable main agent via `.yak/agent.md`

## Prerequisites

- Go 1.26.1 or later
- An OpenAI-compatible chat completions API (e.g. LM Studio running locally)

## Running

```sh
go run ./cmd/yak
```

By default the CLI connects to a local LM Studio instance. Configure with
environment variables. The CLI also loads a local `.env` file automatically,
which is useful for development defaults:

```env
YAK_WEBUI_PORT=8420
YAK_LOG_DIR=.yak/logs
```

Available settings:

| Variable        | Default                 | Description                                 |
|-----------------|-------------------------|---------------------------------------------|
| `YAK_BASE_URL`  | `http://localhost:1234` | Base URL of the chat completions API        |
| `YAK_MODEL`     | `default`               | Model name to use in API requests           |
| `YAK_API_KEY`   | unset                   | Optional bearer token for authenticated APIs |
| `YAK_WEBUI_PORT`| `8420`                  | Enables the web UI plugin on the given port |
| `YAK_LOG_DIR`   | unset                   | Writes session logs under a timestamped subdirectory |
| `YAK_BRAVE_API_KEY` | unset               | Brave Search API key for the `web_search` tool        |
| `BRAVE_API_KEY` | unset                   | Alternate Brave Search API key env var                |

Example with a custom endpoint:

```sh
YAK_BASE_URL=http://localhost:8080 YAK_MODEL=my-model go run ./cmd/yak
```

Example with OpenAI:

```sh
YAK_BASE_URL=https://api.openai.com \
YAK_MODEL=gpt-4o-mini \
YAK_API_KEY=your-openai-api-key \
go run ./cmd/yak
```

## Multi-provider sub-agents

Each sub-agent under `.yak/subagents/` or `~/.yak/subagents/` can target its
own provider via three frontmatter fields:

| Field         | Description                                                              |
|---------------|--------------------------------------------------------------------------|
| `model`       | Model name passed to the API verbatim                                    |
| `base_url`    | API base URL (the client appends `/v1/chat/completions`)                 |
| `api_key_env` | Name of an env var holding the API key (resolved at spawn time)          |

If `base_url` is omitted, the global `YAK_BASE_URL` is used. If `api_key_env`
is set but the env var is empty, no `Authorization` header is sent (LM Studio
works this way). Put secrets in `.env` and reference them by env var name —
never commit keys.

Example sub-agents shipped in `.yak/subagents/`: `gpt.md` (OpenAI),
`fireworks.md` (Fireworks), `local.md` (LM Studio).

`.env`:

```env
OPENAI_API_KEY=sk-...
FIREWORKS_API_KEY=fw-...
```

## Customizing the main agent

Drop a `.yak/agent.md` (project) or `~/.yak/agent.md` (user) to customize the
orchestrator. Same frontmatter as sub-agents, plus the body becomes a
`# Personality` section appended to the auto-built system prompt.

`tools` and `model` are required. `tools` filters the available builtin and
plugin tools; `plugins` (optional) restricts which plugins load.

```yaml
---
model: gpt-4.1
base_url: https://api.openai.com
api_key_env: OPENAI_API_KEY
tools: [read, write, edit, bash, grep, ls, find]
plugins: [webui]
---

You are a senior Go engineer. Prefer minimal diffs and be terse.
```

## Testing

Run the full test suite:

```sh
go test ./...
```

Run tests for a specific package:

```sh
go test ./internal/tools/...
go test ./internal/prompt/...
go test ./internal/cli/...
```

Run a single test by name:

```sh
go test ./internal/tools/ -run TestGrepToolFindsPattern
```

Run with verbose output:

```sh
go test -v ./...
```

## Project structure

```
cmd/yak/             CLI entrypoint
internal/
  cli/               Interactive runner and agent loop
  llm/               Chat completions client
  prompt/            System prompt generation
  tools/             Tool implementations (read, write, edit, bash, grep, ls, find, web_fetch, web_search)
  types/             Shared request/response types
```

## Notes

The implementation intentionally stays close to the current TypeScript behavior.
Future passes can add bounded history, transports, scheduling, and persistent memory.
