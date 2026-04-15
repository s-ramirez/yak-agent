package main

import (
	"bufio"
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed templates/AGENTS.md
var tplAgents string

//go:embed templates/IDENTITY.md
var tplIdentity string

//go:embed templates/USER.md
var tplUser string

//go:embed templates/MEMORY.md
var tplMemory string

// runInit bootstraps a `.yak` workspace in the current directory: copies
// workspace templates (skipping existing files with a warning) and walks the
// user through per-channel env var prompts, appending any new keys to .env.
// Keys already present in .env are never re-prompted or overwritten.
func runInit(args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	out := os.Stdout
	fmt.Fprintf(out, "Bootstrapping yak workspace in %s\n\n", cwd)

	yakDir := filepath.Join(cwd, ".yak")
	if err := os.MkdirAll(yakDir, 0o755); err != nil {
		return fmt.Errorf("create .yak: %w", err)
	}
	memoryDir := filepath.Join(yakDir, "memory")
	if err := os.MkdirAll(memoryDir, 0o755); err != nil {
		return fmt.Errorf("create .yak/memory: %w", err)
	}

	fmt.Fprintln(out, "Templates:")
	writeTemplateFile(out, filepath.Join(yakDir, "AGENTS.md"), tplAgents)
	writeTemplateFile(out, filepath.Join(yakDir, "IDENTITY.md"), tplIdentity)
	writeTemplateFile(out, filepath.Join(yakDir, "USER.md"), tplUser)
	writeTemplateFile(out, filepath.Join(memoryDir, "MEMORY.md"), tplMemory)

	existing, err := readEnvKeys(".env")
	if err != nil {
		return fmt.Errorf("read .env: %w", err)
	}
	if len(existing) > 0 {
		fmt.Fprintf(out, "\nFound existing .env (%d keys) — keys already set will be skipped.\n", len(existing))
	}

	reader := bufio.NewReader(os.Stdin)
	newEnv := map[string]string{}
	ask := func(key, prompt, def string) {
		if _, ok := existing[key]; ok {
			fmt.Fprintf(out, "  %s: (already set, skipping)\n", key)
			return
		}
		val := promptLine(reader, out, prompt, def)
		if val != "" {
			newEnv[key] = val
		}
	}

	fmt.Fprintln(out, "\n== LLM ==")
	fmt.Fprintln(out, "The default agent uses OPENAI_API_KEY. Leave blank to configure manually later.")
	ask("OPENAI_API_KEY", "OpenAI API key", "")

	fmt.Fprintln(out, "\n== Web UI ==")
	if promptYesNo(reader, out, "Enable embedded web UI?", false) {
		ask("YAK_WEBUI_PORT", "Port", "8420")
	}

	fmt.Fprintln(out, "\n== Session logging ==")
	if promptYesNo(reader, out, "Enable per-session logging?", true) {
		ask("YAK_LOG_DIR", "Log directory", ".yak/logs")
	}

	fmt.Fprintln(out, "\n== Heartbeat ==")
	fmt.Fprintln(out, "Periodic tick that wakes the agent with no user input (Go duration syntax).")
	if promptYesNo(reader, out, "Enable heartbeat?", false) {
		ask("YAK_HEARTBEAT_INTERVAL", "Interval (e.g. 10m, 1h)", "15m")
	}

	fmt.Fprintln(out, "\n== iMessage channel (BlueBubbles) ==")
	if promptYesNo(reader, out, "Enable iMessage channel?", false) {
		ask("YAK_IMESSAGE_SERVER_URL", "BlueBubbles server URL", "")
		ask("YAK_IMESSAGE_PASSWORD", "BlueBubbles password", "")
		ask("YAK_IMESSAGE_WEBHOOK_PORT", "Webhook port", "8421")
		ask("YAK_IMESSAGE_WEBHOOK_PATH", "Webhook path", "/bluebubbles")
		ask("YAK_IMESSAGE_OWNER_HANDLES", "Owner handles (comma-separated)", "")
		ask("YAK_IMESSAGE_GROUP_TAG", "Group tag (e.g. @yak)", "")
	}

	fmt.Fprintln(out, "\n== Discord channel ==")
	if promptYesNo(reader, out, "Enable Discord channel?", false) {
		ask("YAK_DISCORD_TOKEN", "Discord bot token", "")
		ask("YAK_DISCORD_OWNER_IDS", "Owner user IDs (comma-separated)", "")
		ask("YAK_DISCORD_GUILD_TAG", "Guild tag (e.g. @yak)", "")
	}

	if len(newEnv) == 0 {
		fmt.Fprintln(out, "\nNo new env keys to write.")
	} else {
		if err := appendEnvFile(".env", newEnv); err != nil {
			return fmt.Errorf("write .env: %w", err)
		}
		fmt.Fprintf(out, "\nAppended %d key(s) to .env\n", len(newEnv))
	}

	fmt.Fprintln(out, "\nDone. Run `yak` to start.")
	return nil
}

func writeTemplateFile(out io.Writer, path, content string) {
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintf(out, "  skip  %s (exists)\n", path)
		return
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		fmt.Fprintf(out, "  error %s: %v\n", path, err)
		return
	}
	fmt.Fprintf(out, "  wrote %s\n", path)
}

// readEnvKeys returns the set of keys defined in a dotenv file. Missing file
// is not an error. Uses the same lenient parsing as loadDotenv.
func readEnvKeys(path string) (map[string]struct{}, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]struct{}{}, nil
		}
		return nil, err
	}
	defer f.Close()
	keys := map[string]struct{}{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(line[len("export "):])
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		keys[strings.TrimSpace(line[:eq])] = struct{}{}
	}
	return keys, scanner.Err()
}

func appendEnvFile(path string, kv map[string]string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return err
	}
	if stat.Size() > 0 {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		line := fmt.Sprintf("%s=%s\n", k, quoteEnvValue(kv[k]))
		if _, err := f.WriteString(line); err != nil {
			return err
		}
	}
	return nil
}

func quoteEnvValue(v string) string {
	if v == "" {
		return ""
	}
	// Quote if value contains spaces, quotes, or shell metacharacters.
	if strings.ContainsAny(v, " \t\"'#\\$`") {
		escaped := strings.ReplaceAll(v, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		return `"` + escaped + `"`
	}
	return v
}

// promptLine prints `label` (with optional default) and returns the user's
// trimmed response, or `def` if they hit enter.
func promptLine(r *bufio.Reader, out io.Writer, label, def string) string {
	if def != "" {
		fmt.Fprintf(out, "  %s [%s]: ", label, def)
	} else {
		fmt.Fprintf(out, "  %s: ", label)
	}
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return def
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func promptYesNo(r *bufio.Reader, out io.Writer, label string, defaultYes bool) bool {
	hint := "y/N"
	if defaultYes {
		hint = "Y/n"
	}
	fmt.Fprintf(out, "  %s [%s]: ", label, hint)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return defaultYes
	}
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" {
		return defaultYes
	}
	return line == "y" || line == "yes"
}
