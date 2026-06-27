// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package shellexec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wavetermdev/waveterm/pkg/util/shellutil"
)

func TestCheckCwdRequiresDirectory(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "file.txt")
	if err := os.WriteFile(filePath, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := checkCwd(tempDir); err != nil {
		t.Fatalf("checkCwd directory error = %v", err)
	}
	if err := checkCwd(filePath); err == nil {
		t.Fatal("checkCwd file error = nil")
	}
}

func TestRemoteCwdPrefixUsesShellSyntax(t *testing.T) {
	pwshPrefix := remoteCwdPrefix("C:\\Users\\me\\project", "C:\\Users\\me", shellutil.ShellType_pwsh)
	if !strings.Contains(pwshPrefix, "Set-Location") {
		t.Fatalf("PowerShell prefix = %q, expected Set-Location", pwshPrefix)
	}
	if strings.Contains(pwshPrefix, " || ") {
		t.Fatalf("PowerShell prefix = %q, should not use POSIX fallback syntax", pwshPrefix)
	}

	posixPrefix := remoteCwdPrefix("/tmp/project", "/home/me", shellutil.ShellType_unknown)
	if !strings.Contains(posixPrefix, " || ") {
		t.Fatalf("POSIX prefix = %q, expected POSIX fallback syntax", posixPrefix)
	}
}

func TestRemoteCwdInitCommandUsesConservativeUnknownSyntax(t *testing.T) {
	unknownCmd := remoteCwdInitCommand("/tmp/project", shellutil.ShellType_unknown)
	if !strings.HasPrefix(unknownCmd, "cd ") {
		t.Fatalf("unknown init command = %q, expected cd", unknownCmd)
	}
	if strings.Contains(unknownCmd, " || ") {
		t.Fatalf("unknown init command = %q, should not use POSIX fallback syntax", unknownCmd)
	}

	pwshCmd := remoteCwdInitCommand("C:\\Users\\me\\project", shellutil.ShellType_pwsh)
	if !strings.Contains(pwshCmd, "Set-Location") {
		t.Fatalf("PowerShell init command = %q, expected Set-Location", pwshCmd)
	}
}
