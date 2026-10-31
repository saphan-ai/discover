package cli

// Exit codes of the discover command. Later work adds its own codes here.
const (
	// ExitOK means the command did what was asked.
	ExitOK = 0
	// ExitUsage means a usage error or a refusal.
	ExitUsage = 2
	// ExitNotEqual means the read-only proof failed: the target's digest after the scan differs from the one before.
	ExitNotEqual = 3
)
