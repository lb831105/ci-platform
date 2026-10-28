package executor

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

const (
	junitDirName         = "test-reports"
	junitMaxFileBytes    = 32 << 20  // 单个 XML 上限，防止误配大文件把内存吃满
	junitMaxFiles        = 200       // 单次构建最多收集的报告数
	junitMaxTotalBytes   = 128 << 20 // 全部报告总大小上限
	junitMaxFailedCases  = 200       // 页面只展示最近/最关键的失败用例
	junitMaxMessageRunes = 500
)

// JUnitCollector 收集工作区里的 JUnit XML，解析汇总并复制原始报告到构建目录。
type JUnitCollector struct{}

func NewJUnitCollector() *JUnitCollector { return &JUnitCollector{} }

type junitProblem struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	ClassName string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitProblem `xml:"failure"`
	Error     *junitProblem `xml:"error"`
	Skipped   *struct{}     `xml:"skipped"`
}

type junitSuite struct {
	Name     string      `xml:"name,attr"`
	Tests    int         `xml:"tests,attr"`
	Failures int         `xml:"failures,attr"`
	Errors   int         `xml:"errors,attr"`
	Skipped  int         `xml:"skipped,attr"`
	Time     string      `xml:"time,attr"`
	Cases    []junitCase `xml:"testcase"`
}

type junitDoc struct {
	Suites []junitSuite `xml:"testsuite"`
}

type junitSingle struct {
	Tests    int         `xml:"tests,attr"`
	Failures int         `xml:"failures,attr"`
	Errors   int         `xml:"errors,attr"`
	Skipped  int         `xml:"skipped,attr"`
	Time     string      `xml:"time,attr"`
	Cases    []junitCase `xml:"testcase"`
}

// Collect 匹配 JUnit 报告、解析汇总并复制原始文件到 buildDir/test-reports/。
// 未启用时返回 (nil,nil)；未匹配到报告时按 AllowEmpty 决定告警或失败。
func (c *JUnitCollector) Collect(ctx context.Context, workspace, buildDir string, cfg model.TestReportConfig, out io.Writer) (*model.TestReport, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if len(cfg.Files) == 0 {
		return nil, errors.New("junit: test_report.files is empty")
	}
	files, notes, err := matchWorkspaceFilesDetailed(workspace, cfg.Files, "junit")
	if err != nil {
		return nil, err
	}
	for _, note := range notes {
		fmt.Fprintf(out, "[junit] %s\n", note)
	}
	if len(files) == 0 {
		fmt.Fprintln(out, "[junit] WARN: no JUnit XML matched")
		if cfg.AllowEmpty {
			fmt.Fprintln(out, "[junit] 已按「允许无报告」继续")
			return nil, nil
		}
		return nil, fmt.Errorf("junit: no test report matched %v", cfg.Files)
	}
	if len(files) > junitMaxFiles {
		return nil, fmt.Errorf("junit: matched %d reports, exceeding %d", len(files), junitMaxFiles)
	}
	var totalBytes int64
	for _, file := range files {
		info, statErr := os.Lstat(file)
		if statErr == nil {
			totalBytes += info.Size()
		}
	}
	if totalBytes > junitMaxTotalBytes {
		return nil, fmt.Errorf("junit: reports total %d bytes, exceeding %d", totalBytes, junitMaxTotalBytes)
	}

	destRoot := filepath.Join(buildDir, junitDirName)
	if err = os.RemoveAll(destRoot); err != nil {
		return nil, fmt.Errorf("junit: clean %s: %w", destRoot, err)
	}
	if err = pkg.EnsureDir(destRoot); err != nil {
		return nil, err
	}

	report := &model.TestReport{CreatedAt: time.Now().UTC()}
	parsed := 0
	for _, file := range files {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		info, statErr := os.Lstat(file)
		if statErr != nil || !info.Mode().IsRegular() {
			fmt.Fprintf(out, "[junit] WARN: skip %s (not a regular file)\n", file)
			continue
		}
		if info.Size() > junitMaxFileBytes {
			fmt.Fprintf(out, "[junit] WARN: skip %s (%d bytes > %d)\n", file, info.Size(), junitMaxFileBytes)
			continue
		}
		data, readErr := os.ReadFile(file)
		if readErr != nil {
			fmt.Fprintf(out, "[junit] WARN: read %s: %v\n", file, readErr)
			continue
		}
		rel := filepath.ToSlash(relativeTo(workspace, file))
		if rel == "" || rel == "." || strings.HasPrefix(rel, "../") {
			fmt.Fprintf(out, "[junit] WARN: skip %s (outside workspace)\n", file)
			continue
		}
		if err = pkg.WriteFileAtomic(filepath.Join(destRoot, filepath.FromSlash(rel)), data); err != nil {
			return nil, fmt.Errorf("junit: archive %s: %w", rel, err)
		}
		suites, parseErr := parseJUnit(data)
		if parseErr != nil {
			fmt.Fprintf(out, "[junit] WARN: parse %s: %v\n", rel, parseErr)
			continue
		}
		parsed++
		report.Files = append(report.Files, rel)
		mergeJUnitSuites(report, suites)
	}
	if parsed == 0 {
		return nil, errors.New("junit: matched reports but none could be parsed")
	}

	fmt.Fprintf(out, "[junit] %d suite(s), %d test(s): %d passed, %d failed, %d error, %d skipped (%dms)\n",
		report.Suites, report.Total, report.Passed, report.Failed, report.Errors, report.Skipped, report.DurationMS)
	return report, nil
}

func parseJUnit(data []byte) ([]junitSuite, error) {
	var doc junitDoc
	if err := xml.Unmarshal(data, &doc); err == nil && len(doc.Suites) > 0 {
		return doc.Suites, nil
	}
	// 有些工具直接输出根节点 <testsuite>，上面按 <testsuites> 解析会得到空列表。
	var single junitSingle
	if err := xml.Unmarshal(data, &single); err != nil {
		return nil, err
	}
	if len(single.Cases) == 0 && single.Tests == 0 {
		return nil, errors.New("no <testsuite>/<testcase> found")
	}
	return []junitSuite{{
		Tests: single.Tests, Failures: single.Failures, Errors: single.Errors,
		Skipped: single.Skipped, Time: single.Time, Cases: single.Cases,
	}}, nil
}

func mergeJUnitSuites(report *model.TestReport, suites []junitSuite) {
	report.Suites += len(suites)
	for _, suite := range suites {
		// 有些工具只写 testsuite 的汇总属性，不输出 testcase 明细；
		// 这种情况仍要计入总数，否则“测试全部失败”会被误判为 0 个用例。
		if len(suite.Cases) == 0 {
			report.Total += suite.Tests
			report.Failed += suite.Failures
			report.Errors += suite.Errors
			report.Skipped += suite.Skipped
			report.DurationMS += parseJUnitTimeMS(suite.Time)
			if passed := suite.Tests - suite.Failures - suite.Errors - suite.Skipped; passed > 0 {
				report.Passed += passed
			}
			continue
		}
		for _, c := range suite.Cases {
			report.Total++
			ms := parseJUnitTimeMS(c.Time)
			report.DurationMS += ms
			switch {
			case c.Error != nil:
				report.Errors++
				appendFailedCase(report, c, "error", ms)
			case c.Failure != nil:
				report.Failed++
				appendFailedCase(report, c, "failure", ms)
			case c.Skipped != nil:
				report.Skipped++
			default:
				report.Passed++
			}
		}
	}
}

func appendFailedCase(report *model.TestReport, c junitCase, status string, ms int64) {
	if len(report.FailedCases) >= junitMaxFailedCases {
		return
	}
	problem := c.Failure
	if status == "error" {
		problem = c.Error
	}
	message := ""
	if problem != nil {
		message = strings.TrimSpace(problem.Message)
		if text := strings.TrimSpace(problem.Text); text != "" {
			if message != "" {
				message += ": "
			}
			message += text
		}
	}
	if runes := []rune(message); len(runes) > junitMaxMessageRunes {
		message = string(runes[:junitMaxMessageRunes]) + "…"
	}
	report.FailedCases = append(report.FailedCases, model.TestCaseResult{
		Name: c.Name, ClassName: c.ClassName, Status: status, TimeMS: ms, Message: message,
	})
}

func parseJUnitTimeMS(raw string) int64 {
	seconds, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || seconds <= 0 {
		return 0
	}
	return int64(seconds * 1000)
}
