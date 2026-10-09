package plugin

// AgentTurnResponse is an immutable operator resolution payload. Interrupt is
// explicit user intent; absence means false. A consumer still checks the actual
// target capability before dispatch. This field confers no reply authority.
type AgentTurnResponse struct {
	Action         string `json:"action"`
	ResponseText   string `json:"response_text,omitempty"`
	SelectedOption string `json:"selected_option,omitempty"`
	Note           string `json:"note,omitempty"`
	Interrupt      bool   `json:"interrupt,omitempty"`
}
