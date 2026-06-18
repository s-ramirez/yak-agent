# yak-go

## Features

- Interactive CLI loop with an agentic tool-use cycle
- OpenAI-compatible `/v1/chat/completions` client
- ChatGPT Plus/Pro subscription access through OpenAI Codex OAuth
- Optional bearer-token auth for OpenAI-compatible endpoints
- Dynamic system prompt with environment info and tool-selection rules
- Built-in tools: `read`, `write`, `edit`, `bash`, `grep`, `ls`, `find`, `web_fetch`, `web_search`
- Plugin system with startup-registered plugins; `tilldone` is currently disabled
- Multi-provider sub-agents and customizable main agent via `.yak/agent.md`

## Prerequisites

- Go 1.26.1 or later
- An OpenAI API key, another OpenAI-compatible chat completions API, or a ChatGPT subscription with Codex access

## Running

```sh
go run ./cmd/yak
```

By default the CLI connects to OpenAI using `gpt-5.4`. Configure with
environment variables. The CLI also loads a local `.env` file automatically,
which is useful for development defaults:

```env
YAK_WEBUI_PORT=8420
YAK_LOG_DIR=.yak/logs
```

Available settings:

| Variable            | Default                     | Description                                           |
|---------------------|-----------------------------|-------------------------------------------------------|
| `YAK_PROVIDER`      | `openai`                     | `openai`/OpenAI-compatible or `openai-codex`          |
| `YAK_BASE_URL`      | `https://api.openai.com`    | Provider base URL                                     |
| `YAK_MODEL`         | `gpt-5.4`                   | Model name to use in API requests                     |
| `YAK_API_KEY`       | unset                       | Optional bearer token for authenticated APIs          |
| `YAK_CODEX_AUTH_FILE` | `~/.yak/auth/openai-codex.json` | Override the Codex OAuth credential path         |
| `YAK_LLM_TIMEOUT`   | `5m`                        | Timeout for primary and heartbeat LLM requests        |
| `YAK_SUBAGENT_LLM_TIMEOUT` | `YAK_LLM_TIMEOUT` | Timeout for sub-agent LLM requests                    |
| `YAK_DISABLE_TOOLS` | `false`                     | Do not send `tools` in chat completion requests       |
| `YAK_WEBUI_PORT`    | `8420`                      | Enables the web UI plugin on the given port           |
| `YAK_LOG_DIR`       | unset                       | Writes session logs under a timestamped subdirectory  |
| `YAK_BRAVE_API_KEY` | unset                       | Brave Search API key for the `web_search` tool        |
| `BRAVE_API_KEY`     | unset                       | Alternate Brave Search API key env var                |
| `YAK_TELEGRAM_TOPIC_AGENTS` | unset               | Routing-only topic→subagent map like `exercise=rocky,nutrition=scout,general=main` |

Example with a custom endpoint:

```sh
YAK_BASE_URL=http://localhost:8080 YAK_MODEL=my-model go run ./cmd/yak
```

If your local OpenAI-compatible server resets the connection on function-calling
payloads, disable tool advertisement:

```sh
YAK_BASE_URL=http://localhost:8000 \
YAK_MODEL=my-model \
YAK_DISABLE_TOOLS=true \
go run ./cmd/yak
```

Telegram topic routing example:

```sh
YAK_TELEGRAM_TOPIC_AGENTS=exercise=rocky,nutrition=scout,general=main go run ./cmd/yak
```

This currently enables only topic→subagent routing inside Yak's dispatcher. It does **not** yet connect to Telegram's network API by itself; it is intended to pair with inbound messages that populate channel=`telegram`, thread=<chat>, and topic=<topic>.

Example with OpenAI:

```sh
YAK_BASE_URL=https://api.openai.com \
YAK_MODEL=gpt-4o-mini \
YAK_API_KEY=your-openai-api-key \
go run ./cmd/yak
```

### ChatGPT subscription authentication

Authenticate once with your ChatGPT account:

```sh
go run ./cmd/yak auth login
```

The command uses OAuth Authorization Code with PKCE, opens the ChatGPT sign-in
page, and stores refreshable credentials at
`~/.yak/auth/openai-codex.json` with user-only permissions. On a headless
machine, use `go run ./cmd/yak auth login --manual` and paste the final redirect
URL.

Then configure the provider:

```sh
YAK_PROVIDER=openai-codex \
YAK_MODEL=gpt-5.3-codex \
go run ./cmd/yak
```

Or put the same values in `.yak/AGENTS.md`:

```yaml
---
provider: openai-codex
model: gpt-5.3-codex
base_url: https://chatgpt.com/backend-api
tools: ["*"]
---
```

The `openai-codex` provider uses subscription capacity and is separate from
the `openai` provider, which still requires an API key for billed API access.

## Multi-provider sub-agents

Each sub-agent under `.yak/subagents/` or `~/.yak/subagents/` can target its
own provider through these frontmatter fields:

| Field         | Description                                                              |
|---------------|--------------------------------------------------------------------------|
| `provider`    | `openai`, `openai-compatible`, or `openai-codex`                          |
| `model`       | Model name passed to the provider                                         |
| `base_url`    | API base URL (the client appends `/v1/chat/completions`)                 |
| `api_key_env` | Name of an env var holding the API key (resolved at spawn time)          |

If `provider` is omitted, definitions with their own `base_url` or
`api_key_env` use the OpenAI-compatible client; otherwise they inherit the
global provider. If `base_url` is omitted, the global `YAK_BASE_URL` is used. If `api_key_env`
is set but the env var is empty, no `Authorization` header is sent. Put
secrets in `.env` and reference them by env var name —
never commit keys.

Example sub-agents shipped in `.yak/subagents/`: `gpt.md` (OpenAI),
`fireworks.md` (Fireworks), `local.md` (OMLX).

`memory-reviewer` is a reserved background sub-agent used for automatic
bounded-memory review every 10 user turns. By default it connects to OMLX at
`http://localhost:8000` with model `gemma-4-e4b-it-4bit` and receives only
the memory tool. Override its model, endpoint, prompt, or context size in
`.yak/subagents/memory-reviewer.md`; its tool access is always forced to
`memory` and it is excluded from normal `sessions_spawn` delegation.

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
plugin tools; `plugins` (optional) restricts which plugins load. When
`api_key_env` is omitted, the main agent sends no `Authorization` header.

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
