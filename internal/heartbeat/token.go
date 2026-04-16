// Package heartbeat provides helpers for the heartbeat system: the
// HEARTBEAT_OK suppression token and active-hours gating.
package heartbeat

import (
	"regexp"
	"strings"
	"unicode"
)

// Token is the sentinel the model should return during a heartbeat turn
// when there is nothing actionable to report. Returning it causes the
// dispatcher to prune the turn from conversation history and suppress
// delivery to any outbound channel.
const Token = "HEARTBEAT_OK"

// tokenRE matches the token case-insensitively.
var tokenRE = regexp.MustCompile(`(?i)HEARTBEAT_OK`)

// IsOnlyToken reports whether text contains nothing meaningful beyond
// the Token. Whitespace, punctuation, and mixed case are all tolerated.
func IsOnlyToken(text string) bool {
	s := strings.TrimSpace(text)
	if s == "" {
		return true
	}
	// Remove every occurrence of the token, then check whether anything
	// with semantic content (a letter or digit) remains.
	stripped := tokenRE.ReplaceAllString(s, "")
	return strings.TrimFunc(stripped, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) == ""
}

// SystemPromptInstruction is injected into the system prompt when the
// heartbeat is configured. It teaches the model how to suppress no-op turns.
const SystemPromptInstruction = `# Heartbeat
When a <scheduled_event name="heartbeat"> fires and you have nothing actionable to report, respond with exactly:

  HEARTBEAT_OK

This suppresses delivery and removes the exchange from conversation history, keeping context clean. Only use it when there is genuinely nothing to act on or report.`
