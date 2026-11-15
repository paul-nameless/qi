package llm

import "strings"

const (
	QueuedLead       = "The user sent this while you were working:"
	hookContextOpen  = "<hook-context>"
	hookContextClose = "</hook-context>"
	imageNoteOpen    = "<attached-image>"
	imageNoteClose   = "</attached-image>"
)

func HookContext(extra string) string {
	return hookContextOpen + "\n" + extra + "\n" + hookContextClose
}

func AttachedImage(note string) string {
	return imageNoteOpen + note + imageNoteClose
}

func IsAttachment(text string) bool {
	return strings.HasPrefix(text, hookContextOpen) || strings.HasPrefix(text, imageNoteOpen)
}

func IsInBand(text string) bool {
	return strings.HasPrefix(text, "<") || strings.HasPrefix(text, QueuedLead)
}
