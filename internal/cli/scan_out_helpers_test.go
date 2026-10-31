package cli

import (
	"os"
	"testing"
)

// writeOutputsIn calls writeOutputs on a root opened on dir, as the scan does.
func writeOutputsIn(t *testing.T, dir string, files map[string][]byte) error {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	return writeOutputs(root, files)
}
