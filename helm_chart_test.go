package main

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// deploy/helm/marginalia rots the same ways deploy/cloudflare can, and these
// pin it: its version drifting from the release it ships in, the
// single-writer pin loosening into a replica count someone turns up, and a
// setting it passes that the binary never reads. scripts/helm-check.sh and
// scripts/helm-smoke.sh test the rendered chart; these need no helm.

const chartDir = "deploy/helm/marginalia"

func TestChartIsTheRelease(t *testing.T) {
	var manifest map[string]string
	readJSON(t, ".release-please-manifest.json", &manifest)
	version := manifest["."]

	raw := readFile(t, filepath.Join(chartDir, "Chart.yaml"))
	block := regexp.MustCompile(`(?s)# x-release-please-start-version\n(.*?)# x-release-please-end`).FindStringSubmatch(raw)
	if block == nil {
		t.Fatal("Chart.yaml has no x-release-please version block, so a release would not move it")
	}
	for _, want := range []string{"version: " + version + "\n", `appVersion: "` + version + `"` + "\n"} {
		if !strings.Contains(block[1], want) {
			t.Errorf("Chart.yaml's release block lacks %q — chart and image must be the release this chart ships in", strings.TrimSpace(want))
		}
	}

	var config struct {
		Packages map[string]struct {
			ExtraFiles []string `json:"extra-files"`
		} `json:"packages"`
	}
	readJSON(t, "release-please-config.json", &config)
	listed := false
	for _, f := range config.Packages["."].ExtraFiles {
		listed = listed || f == chartDir+"/Chart.yaml"
	}
	if !listed {
		t.Errorf("release-please-config.json does not list %s/Chart.yaml in extra-files", chartDir)
	}
}

func TestChartRunsOneWriter(t *testing.T) {
	deploy := readFile(t, filepath.Join(chartDir, "templates", "deployment.yaml"))
	for _, want := range []string{"\n  replicas: 1\n", "\n    type: Recreate\n"} {
		if !strings.Contains(deploy, want) {
			t.Errorf("deployment.yaml lacks %q: the host is its store's single writer", strings.TrimSpace(want))
		}
	}
	values := readFile(t, filepath.Join(chartDir, "values.yaml"))
	for _, knob := range []string{"replicaCount:", "autoscaling:", "podDisruptionBudget:"} {
		if strings.Contains(values, knob) {
			t.Errorf("values.yaml offers %q: a second replica would be a second writer", knob)
		}
	}
}

func TestChartSettingsAreReadByTheBinary(t *testing.T) {
	names := regexp.MustCompile(`MARGINALIA_[A-Z0-9_]*[A-Z0-9]`).FindAllString(readFile(t, filepath.Join(chartDir, "templates", "deployment.yaml")), -1)
	if len(names) == 0 {
		t.Fatal("deployment.yaml passes no setting")
	}
	source := binarySource(t)
	for _, name := range names {
		if !strings.Contains(source, `"`+name+`"`) {
			t.Errorf("deployment.yaml passes %s, which no non-test Go file reads", name)
		}
	}
}
