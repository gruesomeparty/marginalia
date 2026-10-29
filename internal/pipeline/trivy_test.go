package pipeline_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type vuln struct{ id, pkg string }

// report writes a Trivy JSON report holding vulns and returns its path.
func report(t *testing.T, name string, vulns ...vuln) string {
	t.Helper()
	list := make([]map[string]string, len(vulns))
	for i, v := range vulns {
		list[i] = map[string]string{
			"VulnerabilityID": v.id, "PkgName": v.pkg,
			"InstalledVersion": "1.0.0", "FixedVersion": "1.0.1", "Severity": "HIGH",
		}
	}
	raw, err := json.Marshal(map[string]any{"Results": []any{
		map[string]any{"Target": "marginalia", "Vulnerabilities": list},
		map[string]any{"Target": "debian", "Vulnerabilities": nil},
	}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// compare runs scripts/trivy-new.sh, the gate that fails a PR only on the
// vulnerabilities it adds.
func compare(t *testing.T, base, head string) (string, int) {
	t.Helper()
	path := filepath.Join(filepath.Dir(script(t)), "trivy-new.sh")
	out, err := exec.Command("bash", path, base, head).CombinedOutput()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("run: %v (%s)", err, out)
		}
		code = exit.ExitCode()
	}
	return string(out), code
}

func TestTrivyWarnsOnWhatTheBaseAlreadyHas(t *testing.T) {
	cve := vuln{"CVE-2026-0001", "stdlib"}
	out, code := compare(t, report(t, "base.json", cve), report(t, "head.json", cve))
	if code != 0 || !strings.Contains(out, "::warning title=Existing vulnerability::HIGH CVE-2026-0001 in stdlib") {
		t.Fatalf("an existing CVE must warn, not fail: code %d\n%s", code, out)
	}
}

func TestTrivyFailsOnWhatTheChangeAdds(t *testing.T) {
	old := vuln{"CVE-2026-0001", "stdlib"}
	added := vuln{"CVE-2026-0002", "golang.org/x/net"}
	out, code := compare(t, report(t, "base.json", old), report(t, "head.json", old, added))
	if code != 1 || !strings.Contains(out, "::error title=New vulnerability::HIGH CVE-2026-0002 in golang.org/x/net") {
		t.Fatalf("an added CVE must fail: code %d\n%s", code, out)
	}
	if !strings.Contains(out, "1 new, 1 already on the base branch") {
		t.Fatalf("summary missing:\n%s", out)
	}
}

// The same CVE in a different package is a different finding: a dependency
// the change pulls in can carry an advisory the base already has elsewhere.
func TestTrivyKeysOnPackageToo(t *testing.T) {
	out, code := compare(t,
		report(t, "base.json", vuln{"CVE-2026-0001", "stdlib"}),
		report(t, "head.json", vuln{"CVE-2026-0001", "golang.org/x/net"}))
	if code != 1 {
		t.Fatalf("same id in a new package must fail: code %d\n%s", code, out)
	}
}

// A fix PR removes findings; what is left is the base's and only warns.
func TestTrivyPassesAFix(t *testing.T) {
	a, b := vuln{"CVE-2026-0001", "stdlib"}, vuln{"CVE-2026-0002", "golang.org/x/net"}
	if out, code := compare(t, report(t, "base.json", a, b), report(t, "head.json", a)); code != 0 {
		t.Fatalf("a fix must pass: code %d\n%s", code, out)
	}
}

func TestTrivyNeverFailsOnAMissingScan(t *testing.T) {
	cve := vuln{"CVE-2026-0001", "stdlib"}
	if out, code := compare(t, report(t, "base.json", cve), ""); code != 0 || !strings.Contains(out, "did not produce a report") {
		t.Fatalf("no head report must warn: code %d\n%s", code, out)
	}
	out, code := compare(t, "", report(t, "head.json", cve))
	if code != 0 || !strings.Contains(out, "::warning title=Existing vulnerability::") {
		t.Fatalf("no base report must warn on everything: code %d\n%s", code, out)
	}
	broken := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(broken, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, code := compare(t, report(t, "base.json"), broken); code != 0 {
		t.Fatalf("an unreadable report must warn: code %d\n%s", code, out)
	}
}
