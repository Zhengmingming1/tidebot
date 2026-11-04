package webui

// CapabilityRecovery opens a conversation without submitting the editable draft.
// Only POSTing a subsequent turn is allowed to start a model run.
type CapabilityRecovery struct {
	Session     ChatSession `json:"session"`
	DraftPrompt string      `json:"draft_prompt"`
}
