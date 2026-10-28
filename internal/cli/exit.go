package cli

// Exit codes of the discover command. Later work adds its own codes here.
const (
	// ExitOK means the command did what was asked.
	ExitOK = 0
	// ExitUsage means a usage error or a refusal.
	ExitUsage = 2
)
