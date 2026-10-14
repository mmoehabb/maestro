package agent

import (
	"regexp"
	"strings"
	"unicode"
)

// Codex 0.162.1 approval_overlay and request_user_input rendering. These are
// terminal heuristics, not durable rollout events. Require dialog controls as
// well as a heading so ordinary assistant text does not request attention.
var codexQuestionProgress = regexp.MustCompile(`^question\d+/\d+(?:\D|$)`)

func (g Generic) InputPrompt(screen string) bool {
	if g.Name != "codex" || g.Config.InputHints != nil {
		return false
	}
	return codexScreenPrompt(screen)
}

type codexPromptKind uint8

const (
	codexNoPrompt codexPromptKind = iota
	codexApproval
	codexElicitation
	codexQuestion
	codexConfirmation
)

func codexScreenPrompt(screen string) bool {
	lines := strings.Split(strings.ToLower(screen), "\n")
	selected := make([]bool, len(lines))
	for i := range lines {
		// Drop whitespace to recognize soft wraps even in the middle of a
		// word. Border cells can occur between wrapped lines as well.
		lines[i] = strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) || strings.ContainsRune("│┃", r) {
				return -1
			}
			return r
		}, lines[i])
		selected[i] = strings.HasPrefix(lines[i], "›")
		lines[i] = strings.TrimPrefix(lines[i], "›")
	}
	fenced := false
	for i, line := range lines {
		if strings.HasPrefix(line, "```") {
			fenced = codexFence(line, fenced)
			continue
		}
		if fenced || selected[i] || codexQuoted(line) {
			continue
		}
		var heading strings.Builder
		for j := i; j < min(i+4, len(lines)); j++ {
			if selected[j] || codexQuoted(lines[j]) {
				break
			}
			heading.WriteString(lines[j])
			if kind := codexHeading(heading.String()); kind != codexNoPrompt {
				if codexDialogControls(lines, selected, j+1, kind) {
					return true
				}
				break
			}
		}
	}
	return false
}

func codexQuoted(line string) bool {
	return line != "" && strings.ContainsRune(">\"'`#", rune(line[0]))
}

func codexFence(line string, fenced bool) bool {
	// A same-row closing delimiter denotes inline code, not a fence that
	// consumes subsequent controls. Fences outside a dialog exclude examples.
	if fenced || !strings.Contains(strings.TrimLeft(line, "`"), "```") {
		return !fenced
	}
	return fenced
}

func codexHeading(text string) codexPromptKind {
	for _, prefix := range []string{
		"wouldyouliketorunthefollowingcommand?", "wouldyouliketomakethefollowingedits?",
		"wouldyouliketograntthesepermissions?", "wouldyouliketosendinputto",
		"doyouwanttoapprovenetworkaccessto",
	} {
		if strings.HasPrefix(text, prefix) {
			return codexApproval
		}
	}
	if strings.HasSuffix(text, "needsyourapproval.") {
		return codexElicitation
	}
	if codexQuestionProgress.MatchString(text) {
		return codexQuestion
	}
	if strings.HasPrefix(text, "submitwith") && strings.Contains(text, "unanswered") {
		return codexConfirmation
	}
	return codexNoPrompt
}

func codexDialogControls(lines []string, selected []bool, start int, kind codexPromptKind) bool {
	fenced, literal := false, false
	for i := start; i < len(lines); i++ {
		line := lines[i]
		// Only a selection or command inside an identified dialog starts
		// literal body content. Answer continuations include blank rows and
		// arbitrary Markdown; they end at the dialog's submission controls.
		if selected[i] && (!fenced || kind == codexQuestion || kind == codexElicitation) {
			// Questionnaire and MCP descriptions are arbitrary text. An
			// unmatched delimiter in that description cannot hide the real
			// answer or options after a heading established outside a fence.
			fenced, literal = false, true
		}
		if !fenced && strings.HasPrefix(line, "$") {
			literal = true
		}
		if !literal {
			// An inline span can soft-wrap and close in the middle of a row.
			if fenced && strings.HasSuffix(line, "```") {
				fenced = false
				continue
			}
			if strings.HasPrefix(line, "```") {
				fenced = codexFence(line, fenced)
				continue
			}
			if fenced || codexQuoted(line) {
				continue
			}
		}
		if line == "" || selected[i] {
			continue
		}
		// Require footer instructions at the beginning of a row, including
		// soft wraps. Body words such as enterprise are not controls.
		var footer strings.Builder
		for j := i; j < min(i+4, len(lines)); j++ {
			if selected[j] || (!literal && codexQuoted(lines[j])) {
				break
			}
			footer.WriteString(lines[j])
		}
		text := footer.String()
		switch kind {
		case codexApproval, codexElicitation:
			if strings.HasPrefix(text, "pressentertoconfirmoresctocancel") {
				return true
			}
		case codexQuestion:
			if strings.HasPrefix(text, "ctrl+stosubmitall") || strings.HasPrefix(text, "entertoselect") ||
				strings.HasPrefix(text, "entertoanswer") {
				return true
			}
		case codexConfirmation:
			if strings.HasPrefix(text, "entertoselect") {
				return true
			}
		case codexNoPrompt:
			return false
		}
	}
	return false
}

func (g Generic) InputNotification(message string) bool {
	if g.Name != "codex" || g.Config.InputHints != nil {
		return false
	}
	for _, line := range strings.Split(strings.ToLower(message), "\n") {
		for _, prefix := range []string{
			"approval requested:", "approval requested by ", "codex wants to edit ",
			"plan mode prompt:", "question:",
		} {
			if strings.HasPrefix(strings.TrimSpace(line), prefix) {
				return true
			}
		}
	}
	return false
}

// InputSubmission recognizes default decisions when only a terminal notification
// is available. Questions use printable keys and Enter to edit or select answers.
func (g Generic) InputSubmission(notification, key string) bool {
	if !g.InputNotification(notification) {
		return false
	}
	if key == "esc" || key == "ctrl+c" {
		return true
	}
	for _, line := range strings.Split(strings.ToLower(notification), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "question:"):
			return key == "ctrl+s"
		case strings.HasPrefix(line, "plan mode prompt:"):
			return key == "enter"
		case strings.HasPrefix(line, "approval requested:"):
			return key == "enter" || (len(key) == 1 && strings.ContainsAny(key, "yapdn"))
		case strings.HasPrefix(line, "approval requested by "):
			return key == "enter" || (len(key) == 1 && strings.ContainsAny(key, "ync"))
		case strings.HasPrefix(line, "codex wants to edit "):
			return key == "enter" || (len(key) == 1 && strings.ContainsAny(key, "yan"))
		}
	}
	return false
}
