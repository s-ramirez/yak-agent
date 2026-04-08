# yak-go

## Features

- Interactive CLI loop with an agentic tool-use cycle
- LM Studio-compatible `/v1/chat/completions` client
- Optional bearer-token auth for OpenAI-compatible endpoints
- Dynamic system prompt with environment info and tool-selection rules
- Built-in tools: `read`, `write`, `edit`, `bash`, `grep`, `ls`, `find`

## Prerequisites

- Go 1.26.1 or later
- An OpenAI-compatible chat completions API (e.g. LM Studio running locally)

## Running

```sh
go run ./cmd/yak
```

By default the CLI connects to a local LM Studio instance. Configure with
environment variables:

| Variable        | Default                 | Description                                 |
|-----------------|-------------------------|---------------------------------------------|
| `YAK_BASE_URL`  | `http://localhost:1234` | Base URL of the chat completions API        |
| `YAK_MODEL`     | `default`               | Model name to use in API requests           |
| `YAK_API_KEY`   | unset                   | Optional bearer token for authenticated APIs |

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
  tools/             Tool implementations (read, write, edit, bash, grep, ls, find)
  types/             Shared request/response types
```

## Notes

The implementation intentionally stays close to the current TypeScript behavior.
Future passes can add bounded history, transports, scheduling, and persistent memory.
