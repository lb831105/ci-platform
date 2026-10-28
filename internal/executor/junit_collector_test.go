package executor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"ci-platform/internal/model"
)

func TestJUnitCollectorParsesAndArchives(t *testing.T) {
	workspace := t.TempDir()
	buildDir := t.TempDir()
	xml := `<?xml version="1.0"?>
<testsuites>
  <testsuite name="demo" tests="3" failures="1" errors="1" skipped="1">
    <testcase name="ok" classname="Demo" time="0.012"/>
    <testcase name="bad" classname="Demo" time="0.034"><failure message="expected true">assert failed</failure></testcase>
    <testcase name="boom" classname="Demo" time="0.056"><error message="panic"/></testcase>
    <testcase name="skip" classname="Demo"><skipped/></testcase>
  </testsuite>
</testsuites>`
	if err := os.WriteFile(filepath.Join(workspace, "TEST-demo.xml"), []byte(xml), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	report, err := NewJUnitCollector().Collect(context.Background(), workspace, buildDir,
		model.TestReportConfig{Enabled: true, Files: []string{"TEST-*.xml"}}, &out)
	if err != nil {
		t.Fatalf("Collect: %v\n%s", err, out.String())
	}
	if report.Total != 4 || report.Passed != 1 || report.Failed != 1 || report.Errors != 1 || report.Skipped != 1 {
		t.Fatalf("report = %+v", report)
	}
	if report.DurationMS != 102 {
		t.Fatalf("duration = %d", report.DurationMS)
	}
	if len(report.FailedCases) != 2 || report.FailedCases[0].Status != "failure" {
		t.Fatalf("failed cases = %+v", report.FailedCases)
	}
	if _, err := os.Stat(filepath.Join(buildDir, junitDirName, "TEST-demo.xml")); err != nil {
		t.Fatalf("raw report not archived: %v", err)
	}
}

func TestJUnitCollectorSingleTestsuiteRoot(t *testing.T) {
	workspace := t.TempDir()
	xml := `<testsuite name="single"><testcase name="ok" classname="A"/></testsuite>`
	if err := os.WriteFile(filepath.Join(workspace, "junit.xml"), []byte(xml), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := NewJUnitCollector().Collect(context.Background(), workspace, t.TempDir(),
		model.TestReportConfig{Enabled: true, Files: []string{"junit.xml"}}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 1 || report.Passed != 1 || report.Suites != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestJUnitCollectorUsesSuiteAttributesWithoutCases(t *testing.T) {
	workspace := t.TempDir()
	xml := `<testsuites><testsuite name="summary" tests="5" failures="2" errors="1" skipped="1" time="1.5"/></testsuites>`
	if err := os.WriteFile(filepath.Join(workspace, "summary.xml"), []byte(xml), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := NewJUnitCollector().Collect(context.Background(), workspace, t.TempDir(),
		model.TestReportConfig{Enabled: true, Files: []string{"summary.xml"}}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 5 || report.Failed != 2 || report.Errors != 1 || report.Skipped != 1 || report.Passed != 1 {
		t.Fatalf("report = %+v", report)
	}
	if report.DurationMS != 1500 {
		t.Fatalf("duration = %d", report.DurationMS)
	}
}

func TestJUnitCollectorSingleSuiteAttributesOnly(t *testing.T) {
	workspace := t.TempDir()
	xml := `<testsuite name="summary" tests="2" failures="1"/>`
	if err := os.WriteFile(filepath.Join(workspace, "single.xml"), []byte(xml), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := NewJUnitCollector().Collect(context.Background(), workspace, t.TempDir(),
		model.TestReportConfig{Enabled: true, Files: []string{"single.xml"}}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 2 || report.Failed != 1 || report.Passed != 1 {
		t.Fatalf("report = %+v", report)
	}
}
