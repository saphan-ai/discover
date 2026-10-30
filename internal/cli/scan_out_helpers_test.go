package cli

import "testing"

// writeOutputsIn calls writeOutputs on dir as the scan does.
func writeOutputsIn(t *testing.T, dir string, files map[string][]byte) error {
	t.Helper()
	return writeOutputs(dir, files)
}
