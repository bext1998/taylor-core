package safety

import "testing"

const testRoot = `C:\ws`

func wantConfirm(t *testing.T, cmds ...string) {
	t.Helper()
	for _, cmd := range cmds {
		if _, ok := classifyPowerShell(cmd, testRoot); !ok {
			t.Errorf("classifyPowerShell(%q) wants CONFIRM, got AUTO", cmd)
		}
	}
}

func wantAuto(t *testing.T, cmds ...string) {
	t.Helper()
	for _, cmd := range cmds {
		if reason, ok := classifyPowerShell(cmd, testRoot); ok {
			t.Errorf("classifyPowerShell(%q) wants AUTO, got CONFIRM (%s)", cmd, reason)
		}
	}
}

// A wildcard delete removes many files at once, exactly like the wildcard
// move/copy that is already confirmed (#31 item 1).
func TestClassifyConfirmsWildcardDelete(t *testing.T) {
	wantConfirm(t,
		`Remove-Item *.txt`,
		`del *.log`,
		`Remove-Item .\build\*`,
		`rm .\logs\2024-??.log`,
		`Get-ChildItem *.tmp | Remove-Item`,
	)
	// A single named file stays AUTO, as before.
	wantAuto(t,
		`Remove-Item .\build\output.txt`,
		`del .\a.log`,
		`Get-ChildItem .\build`,
	)
}

// A comma joins several paths into one argument; each one must be checked,
// not only the first (#31 item 2).
func TestClassifyChecksEveryPathInACommaList(t *testing.T) {
	wantConfirm(t,
		`Get-Content C:\ws\a.txt,C:\Windows\win.ini`,
		`Get-Content C:\ws\a.txt, C:\Windows\win.ini`,
		`Copy-Item C:\ws\a.txt;C:\Windows\x`,
		`Get-Content C:\ws\a.txt,\\server\share\f.txt`,
	)
	wantAuto(t,
		`Get-Content C:\ws\a.txt,C:\ws\b.txt`,
		`Get-Content C:\ws\a,b.txt`,
	)
}

// `;`, `&`, `,` and `|` are ordinary file-name characters inside quotes. A
// quoted path or destination must be read whole: splitting it at those
// characters turned an overwrite or an escape out of the workspace into AUTO.
func TestClassifyKeepsQuotedPathsWhole(t *testing.T) {
	wantConfirm(t,
		`Copy-Item a.txt ".\archive\;victim.txt"`,
		`Copy-Item a.txt ".\archive\&victim.txt"`,
		`Copy-Item a.txt '.\archive\;victim.txt'`,
		`Get-Content "C:\ws\safe;name\..\..\Windows\win.ini"`,
		`Get-Content "C:\ws\safe,name\..\..\Windows\win.ini"`,
		`Get-Content 'C:\ws\safe,name\..\..\Windows\win.ini'`,
		`Get-Content "C:\Program Files\x\y.txt"`,
	)
	wantAuto(t,
		`Get-Content "C:\ws\my dir\a.txt"`,
		`Get-Content "C:\ws\a;b.txt"`,
		`Move-Item a.txt ".\archive\"; Write-Output "done; really"`,
	)
}

// Quoting must never hide something: an interpolated expression inside a
// quoted string can read a path outside the workspace, and a backtick-escaped
// quote does not close the string (so a later overwrite is still a statement
// of its own).
func TestClassifyQuotingDoesNotHideRisk(t *testing.T) {
	wantConfirm(t,
		"Write-Output \"C:\\ws\\$(Get-Content C:\\Windows\\win.ini)\"",
		"Write-Output \"hello`\" there\"; Copy-Item a.txt b.txt; Write-Output .\\archive\\",
		"Write-Output 'it''s'; Copy-Item a.txt b.txt; Write-Output .\\archive\\",
		"Copy-Item a.txt b.txt; Write-Output \"unterminated",
	)
	wantAuto(t,
		"Write-Output \"say `\"hi`\"; ok\"; Move-Item a.txt .\\archive\\",
	)
}

// A harmless statement chained after a move must not be read as the move's
// destination, but a move that is itself the overwrite must still be caught
// wherever it sits in the chain (#31 item 3).
func TestClassifyMoveDestinationIsReadPerStatement(t *testing.T) {
	wantAuto(t,
		`Move-Item a.txt .\archive\; Write-Output done`,
		`Copy-Item a.txt .\backup\ | Out-Null`,
	)
	wantConfirm(t,
		`Write-Output hi; Move-Item a.txt b.txt`,
		`Move-Item a.txt b.txt; Write-Output done`,
		`Get-ChildItem | Move-Item -Destination b.txt`,
	)
}
