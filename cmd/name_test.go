package cmd

import (
	"strings"
	"testing"
)

func TestToolName(t *testing.T) {
	if ToolName == "" || strings.ContainsAny(ToolName, " \t/") {
		t.Fatalf("ToolName %q cannot be a binary name", ToolName)
	}
}
