package daemon

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// BuildPrompt constructs the task prompt for an agent CLI.
// Keep this minimal — detailed instructions live in CLAUDE.md / AGENTS.md
// injected by execenv.InjectRuntimeConfig.
func BuildPrompt(task Task) string {
	if task.ChatSessionID != "" {
		return buildChatPrompt(task)
	}
	if task.TriggerCommentID != "" {
		return buildCommentPrompt(task)
	}
	if task.AutopilotRunID != "" {
		return buildAutopilotPrompt(task)
	}
	var b strings.Builder
	b.WriteString("You are running as a local coding agent for a Multica workspace.\n\n")
	fmt.Fprintf(&b, "Your assigned issue ID is: %s\n\n", task.IssueID)
	writeCollaborationPromptContext(&b, task)
	fmt.Fprintf(&b, "Start by running `multica issue get %s --output json` to understand your task, then complete it.\n", task.IssueID)
	return b.String()
}

// buildCommentPrompt constructs a prompt for comment-triggered tasks.
// The triggering comment content is embedded directly so the agent cannot
// miss it, even when stale output files exist in a reused workdir.
// The reply instructions (including the current TriggerCommentID as --parent)
// are re-emitted on every turn so resumed sessions cannot carry forward a
// previous turn's --parent UUID.
func buildCommentPrompt(task Task) string {
	var b strings.Builder
	b.WriteString("You are running as a local coding agent for a Multica workspace.\n\n")
	fmt.Fprintf(&b, "Your assigned issue ID is: %s\n\n", task.IssueID)
	writeCollaborationPromptContext(&b, task)
	if task.TriggerCommentContent != "" {
		authorLabel := "A user"
		if task.TriggerAuthorType == "agent" {
			name := task.TriggerAuthorName
			if name == "" {
				name = "another agent"
			}
			authorLabel = fmt.Sprintf("Another agent (%s)", name)
		}
		fmt.Fprintf(&b, "[NEW COMMENT] %s just left a new comment. Focus on THIS comment — do not confuse it with previous ones:\n\n", authorLabel)
		fmt.Fprintf(&b, "> %s\n\n", task.TriggerCommentContent)
		if task.TriggerAuthorType == "agent" {
			b.WriteString("⚠️ The triggering comment was posted by another agent. Before replying, decide whether a reply is warranted at all. If that comment was an acknowledgment, thanks, or sign-off and no concrete question or task is being asked of you, do NOT reply — silence is the preferred way to end agent-to-agent threads. If you do reply, do not @mention the other agent as a sign-off (that re-triggers them and starts a loop).\n\n")
		}
	}
	fmt.Fprintf(&b, "Start by running `multica issue get %s --output json` to understand your task, then decide how to proceed.\n\n", task.IssueID)
	b.WriteString(execenv.BuildCommentReplyInstructions(task.IssueID, task.TriggerCommentID))
	return b.String()
}

func writeCollaborationPromptContext(b *strings.Builder, task Task) {
	if task.Collaboration == nil {
		return
	}
	b.WriteString("Shared collaboration context is available for this task. Use it as the team workroom memory before deciding what to do next.\n")
	if task.Collaboration.Role != "" {
		fmt.Fprintf(b, "Your collaboration role is: %s\n", task.Collaboration.Role)
	}
	if task.Collaboration.AssignmentID != "" {
		fmt.Fprintf(b, "Current assignment ID: %s\n", task.Collaboration.AssignmentID)
	}
	if len(task.Collaboration.TaskBrief) > 0 {
		writePromptJSON(b, "Task brief", task.Collaboration.TaskBrief)
	}
	if len(task.Collaboration.Assignment) > 0 {
		writePromptJSON(b, "Assignment", task.Collaboration.Assignment)
	}
	if len(task.Collaboration.TicketMemory) > 0 {
		writePromptJSON(b, "Ticket memory snapshot", json.RawMessage(task.Collaboration.TicketMemory))
	}
	if len(task.Collaboration.RepoMemory) > 0 {
		writePromptJSON(b, "Repo memory snapshot", json.RawMessage(task.Collaboration.RepoMemory))
	}
	if len(task.Collaboration.RepoMemoryWiki) > 0 {
		writePromptJSON(b, "Repo memory wiki", json.RawMessage(task.Collaboration.RepoMemoryWiki))
	}
	if len(task.Collaboration.Metrics) > 0 {
		writePromptJSON(b, "Collaboration reliability metrics", task.Collaboration.Metrics)
	}
	if len(task.Collaboration.RecentHandoffs) > 0 {
		writePromptJSON(b, "Prior handoffs", task.Collaboration.RecentHandoffs)
	}
	if task.Collaboration.Role == "orchestrator" {
		b.WriteString("When finished, make your final output a JSON synthesis with keys: brief, assignments, dependencies, shared_notes, next_steps.\n")
	}
	if task.Collaboration.Role == "worker" {
		b.WriteString("When finished, make your final output a JSON handoff with keys: summary, worked_on, evidence, validation, remaining_work, handoff_notes.\n")
	}
	b.WriteString("\n")
}

func writePromptJSON(b *strings.Builder, title string, value any) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return
	}
	fmt.Fprintf(b, "%s:\n```json\n%s\n```\n", title, string(data))
}

// buildChatPrompt constructs a prompt for interactive chat tasks.
func buildChatPrompt(task Task) string {
	var b strings.Builder
	b.WriteString("You are running as a chat assistant for a Multica workspace.\n")
	b.WriteString("A user is chatting with you directly. Respond to their message.\n\n")
	fmt.Fprintf(&b, "User message:\n%s\n", task.ChatMessage)
	return b.String()
}

// buildAutopilotPrompt constructs a prompt for run_only autopilot tasks.
func buildAutopilotPrompt(task Task) string {
	var b strings.Builder
	b.WriteString("You are running as a local coding agent for a Multica workspace.\n\n")
	b.WriteString("This task was triggered by an Autopilot in run-only mode. There is no assigned Multica issue for this run.\n\n")
	fmt.Fprintf(&b, "Autopilot run ID: %s\n", task.AutopilotRunID)
	if task.AutopilotID != "" {
		fmt.Fprintf(&b, "Autopilot ID: %s\n", task.AutopilotID)
	}
	if task.AutopilotTitle != "" {
		fmt.Fprintf(&b, "Autopilot title: %s\n", task.AutopilotTitle)
	}
	if task.AutopilotSource != "" {
		fmt.Fprintf(&b, "Trigger source: %s\n", task.AutopilotSource)
	}
	if strings.TrimSpace(string(task.AutopilotTriggerPayload)) != "" {
		fmt.Fprintf(&b, "Trigger payload:\n%s\n", strings.TrimSpace(string(task.AutopilotTriggerPayload)))
	}
	b.WriteString("\nAutopilot instructions:\n")
	if strings.TrimSpace(task.AutopilotDescription) != "" {
		b.WriteString(task.AutopilotDescription)
		b.WriteString("\n\n")
	} else if task.AutopilotTitle != "" {
		fmt.Fprintf(&b, "%s\n\n", task.AutopilotTitle)
	} else {
		b.WriteString("No additional autopilot instructions were provided. Inspect the autopilot configuration before proceeding.\n\n")
	}
	if task.AutopilotID != "" {
		fmt.Fprintf(&b, "Start by running `multica autopilot get %s --output json` if you need the full autopilot configuration, then complete the instructions above.\n", task.AutopilotID)
	} else {
		b.WriteString("Complete the instructions above.\n")
	}
	b.WriteString("Do not run `multica issue get`; this run does not have an issue ID.\n")
	return b.String()
}
