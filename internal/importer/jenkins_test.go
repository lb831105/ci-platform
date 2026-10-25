package importer

import (
	"strings"
	"testing"
)

// fullJobXML 是一份贴近真实导出的 config.xml：
// Git + Shell/Batch + 定时（含 H 与 TZ）+ 参数 + 产物 + 保留策略 + Publish Over SSH。
const fullJobXML = `<?xml version="1.0" encoding="UTF-8"?>
<project>
  <description>订单服务：打包并发布</description>
  <keepDependencies>false</keepDependencies>
  <properties>
    <hudson.model.ParametersDefinitionProperty>
      <parameterDefinitions>
        <hudson.model.StringParameterDefinition>
          <name>VERSION</name>
          <description>发布版本号</description>
          <defaultValue>1.0.0</defaultValue>
        </hudson.model.StringParameterDefinition>
        <hudson.model.BooleanParameterDefinition>
          <name>SKIP_TEST</name>
          <defaultValue>false</defaultValue>
        </hudson.model.BooleanParameterDefinition>
        <hudson.model.ChoiceParameterDefinition>
          <name>ENV</name>
          <choices class="java.util.Arrays$ArrayList">
            <string>dev</string>
            <string>prod</string>
          </choices>
        </hudson.model.ChoiceParameterDefinition>
        <hudson.model.TextParameterDefinition>
          <name>NOTE</name>
          <defaultValue>line1
line2</defaultValue>
        </hudson.model.TextParameterDefinition>
        <hudson.model.PasswordParameterDefinition>
          <name>TOKEN</name>
          <defaultValue>secret</defaultValue>
        </hudson.model.PasswordParameterDefinition>
        <hudson.model.StringParameterDefinition>
          <name>my.param</name>
          <defaultValue>x</defaultValue>
        </hudson.model.StringParameterDefinition>
      </parameterDefinitions>
    </hudson.model.ParametersDefinitionProperty>
    <jenkins.model.BuildDiscarderProperty>
      <strategy class="hudson.tasks.LogRotator">
        <daysToKeep>7</daysToKeep>
        <numToKeep>20</numToKeep>
      </strategy>
    </jenkins.model.BuildDiscarderProperty>
  </properties>
  <scm class="hudson.plugins.git.GitSCM">
    <userRemoteConfigs>
      <hudson.plugins.git.UserRemoteConfig>
        <url>git@gitlab.example.com:shop/order.git</url>
        <credentialsId>gitlab-deploy-key</credentialsId>
      </hudson.plugins.git.UserRemoteConfig>
    </userRemoteConfigs>
    <branches>
      <hudson.plugins.git.BranchSpec>
        <name>*/release</name>
      </hudson.plugins.git.BranchSpec>
    </branches>
  </scm>
  <triggers>
    <hudson.triggers.TimerTrigger>
      <spec>TZ=Asia/Shanghai H 2 * * *</spec>
    </hudson.triggers.TimerTrigger>
    <hudson.triggers.SCMTrigger>
      <spec>H/5 * * * *</spec>
    </hudson.triggers.SCMTrigger>
  </triggers>
  <builders>
    <hudson.tasks.Shell>
      <command>mvn -B -DskipTests clean package</command>
    </hudson.tasks.Shell>
    <hudson.tasks.Shell>
      <command>echo "package done"
ls -l target</command>
    </hudson.tasks.Shell>
    <hudson.tasks.BatchFile>
      <command>echo Windows</command>
    </hudson.tasks.BatchFile>
    <hudson.tasks.Maven>
      <targets>verify</targets>
      <mavenName>maven-3.9</mavenName>
    </hudson.tasks.Maven>
    <hudson.tasks.Gradle>
      <tasks>build</tasks>
      <useWrapper>true</useWrapper>
    </hudson.tasks.Gradle>
  </builders>
  <publishers>
    <hudson.tasks.ArtifactArchiver>
      <artifacts>target/*.jar, dist/**</artifacts>
      <allowEmptyArchive>false</allowEmptyArchive>
    </hudson.tasks.ArtifactArchiver>
    <jenkins.plugins.publish__over__ssh.BapSshPublisherPlugin>
      <consolePrefix>SSH:</consolePrefix>
      <delegate class="jenkins.plugins.publish__over__ssh.BapSshPublisherPlugin$DescriptorImpl">
        <publishers>
          <jenkins.plugins.publish__over__ssh.BapSshPublisher>
            <configName>prod-server</configName>
            <transfers>
              <jenkins.plugins.publish__over__ssh.BapSshTransfer>
                <remoteDirectory>app</remoteDirectory>
                <sourceFiles>target/*.jar</sourceFiles>
                <removePrefix>target</removePrefix>
                <execCommand>systemctl restart order</execCommand>
              </jenkins.plugins.publish__over__ssh.BapSshTransfer>
            </transfers>
          </jenkins.plugins.publish__over__ssh.BapSshPublisher>
          <jenkins.plugins.publish__over__ssh.BapSshPublisher>
            <configName>unknown-server</configName>
            <transfers>
              <jenkins.plugins.publish__over__ssh.BapSshTransfer>
                <remoteDirectory>backup</remoteDirectory>
                <sourceFiles>target/*.jar</sourceFiles>
              </jenkins.plugins.publish__over__ssh.BapSshTransfer>
            </transfers>
          </jenkins.plugins.publish__over__ssh.BapSshPublisher>
        </publishers>
      </delegate>
    </jenkins.plugins.publish__over__ssh.BapSshPublisherPlugin>
  </publishers>
  <disabled>false</disabled>
</project>`

func TestFromJenkinsXMLFullJob(t *testing.T) {
	servers := []SshServerRef{{ID: "s1", Name: "prod-server", Host: "10.0.0.9"}}
	p, warnings, err := FromJenkinsXML([]byte(fullJobXML), servers)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if p.Description != "订单服务：打包并发布" {
		t.Fatalf("description = %q", p.Description)
	}
	if p.GitURL != "git@gitlab.example.com:shop/order.git" || p.GitBranch != "release" {
		t.Fatalf("scm = %q/%q", p.GitURL, p.GitBranch)
	}
	// 5 个可识别 builder：2 shell + 1 batch + maven + gradle
	if len(p.Steps) != 5 {
		t.Fatalf("steps = %d (%+v)", len(p.Steps), p.Steps)
	}
	if p.Steps[0].Name != "shell-1" || !strings.Contains(p.Steps[0].Run, "mvn -B -DskipTests") {
		t.Fatalf("step0 = %+v", p.Steps[0])
	}
	if !strings.Contains(p.Steps[1].Run, "ls -l target") {
		t.Fatalf("多行命令未原样保留: %q", p.Steps[1].Run)
	}
	if p.Steps[2].Name != "batch-1" {
		t.Fatalf("batch step = %+v", p.Steps[2])
	}
	if p.Steps[3].Run != "mvn verify" {
		t.Fatalf("maven step = %q", p.Steps[3].Run)
	}
	if p.Steps[4].Run != "./gradlew build" {
		t.Fatalf("gradle step = %q", p.Steps[4].Run)
	}

	// 定时：TZ 前缀去掉，H 保留；SCM 轮询忽略。
	if p.CronSpec != "H 2 * * *" {
		t.Fatalf("cron = %q", p.CronSpec)
	}

	// 参数：类型/默认值/可选值；密码参数降级；非法名被重命名。
	byName := map[string]bool{}
	for _, def := range p.Params {
		byName[def.Name] = true
	}
	for _, want := range []string{"VERSION", "SKIP_TEST", "ENV", "NOTE", "TOKEN", "my_param"} {
		if !byName[want] {
			t.Fatalf("参数 %q 未导入: %+v", want, p.Params)
		}
	}
	if p.Params[0].Default != "1.0.0" || p.Params[0].Description != "发布版本号" {
		t.Fatalf("VERSION = %+v", p.Params[0])
	}
	if p.Params[1].Type != "bool" || p.Params[1].Default != "false" {
		t.Fatalf("SKIP_TEST = %+v", p.Params[1])
	}
	if p.Params[2].Type != "choice" || len(p.Params[2].Options) != 2 || p.Params[2].Default != "dev" {
		t.Fatalf("ENV = %+v", p.Params[2])
	}
	if p.Params[3].Type != "text" || !strings.Contains(p.Params[3].Default, "\n") {
		t.Fatalf("NOTE = %+v", p.Params[3])
	}

	// 产物与保留策略
	if !p.Artifacts.Enabled || len(p.Artifacts.Files) != 2 || p.Artifacts.Files[0] != "target/*.jar" {
		t.Fatalf("artifacts = %+v", p.Artifacts)
	}
	if p.BuildRetentionMaxPerJob != 20 || p.BuildRetentionMaxAgeDays != 7 {
		t.Fatalf("retention = %d/%d", p.BuildRetentionMaxPerJob, p.BuildRetentionMaxAgeDays)
	}

	// SSH 发布：命中服务器 -> 启用；未命中 -> 保存但停用。
	if len(p.SshPublishes) != 2 {
		t.Fatalf("ssh publishes = %+v", p.SshPublishes)
	}
	prod := p.SshPublishes[0]
	if !prod.Enabled || prod.ServerID != "s1" || prod.RemoteBaseDir != "app" ||
		prod.RemovePrefix != "target" || len(prod.SourceFiles) != 1 || prod.ExecCommand != "systemctl restart order" {
		t.Fatalf("prod target = %+v", prod)
	}
	unknown := p.SshPublishes[1]
	if unknown.Enabled || unknown.ServerID != "" || unknown.Name != "unknown-server" {
		t.Fatalf("unknown target = %+v", unknown)
	}

	// 关键告警必须出现：凭据、密码参数、未匹配的 SSH 服务器。
	assertWarning(t, warnings, "scm", LevelWarn)
	assertWarning(t, warnings, "params", LevelError)
	assertWarning(t, warnings, "ssh", LevelError)
}

func TestFromJenkinsXMLCronAliases(t *testing.T) {
	cases := map[string]string{
		"@daily":            "H H * * *",
		"@midnight":         "H H * * *",
		"@hourly":           "H * * * *",
		"@weekly":           "H H * * 0",
		"@monthly":          "H H 1 * *",
		"@yearly":           "H H 1 1 *",
		"TZ=UTC 30 3 * * 1": "30 3 * * 1",
	}
	for in, want := range cases {
		var w warnings
		got, _ := normalizeCronSpec(in, &w)
		if got != want {
			t.Fatalf("cron %q -> %q, want %q", in, got, want)
		}
	}
}

func TestFromJenkinsXMLUnsupportedJobTypes(t *testing.T) {
	cases := []string{
		`<flow-definition><definition class="org.jenkinsci.plugins.workflow.cps.CpsFlowDefinition"/></flow-definition>`,
		`<org.jenkinsci.plugins.workflow.multibranch.WorkflowMultiBranchProject/>`,
		`<com.cloudbees.hudson.plugins.folder.Folder/>`,
	}
	for _, xml := range cases {
		if _, _, err := FromJenkinsXML([]byte(xml), nil); err == nil {
			t.Fatalf("%s: 应返回明确错误", xml)
		}
	}
	if _, _, err := FromJenkinsXML([]byte("not xml"), nil); err == nil {
		t.Fatal("非法 XML 应报错")
	}
	if _, _, err := FromJenkinsXML(nil, nil); err == nil {
		t.Fatal("空内容应报错")
	}
}

// 没有任何可识别 builder 时必须给出占位步骤 + error 告警（任务仍可用）。
func TestFromJenkinsXMLPlaceholderStep(t *testing.T) {
	xml := `<project>
  <builders>
    <hudson.plugins.dockerbuild.DockerBuildWrapper/>
  </builders>
</project>`
	p, warnings, err := FromJenkinsXML([]byte(xml), nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(p.Steps) != 1 || !strings.Contains(p.Steps[0].Run, "TODO") {
		t.Fatalf("placeholder step = %+v", p.Steps)
	}
	assertWarning(t, warnings, "builders", LevelError)
}

// 参数名需要清洗：数字开头、CI_ 前缀、重名（大小写不敏感）。
func TestParamNameSanitizing(t *testing.T) {
	xml := `<project>
  <properties>
    <hudson.model.ParametersDefinitionProperty>
      <parameterDefinitions>
        <hudson.model.StringParameterDefinition><name>1st</name></hudson.model.StringParameterDefinition>
        <hudson.model.StringParameterDefinition><name>CI_TOKEN</name></hudson.model.StringParameterDefinition>
        <hudson.model.StringParameterDefinition><name>dup</name></hudson.model.StringParameterDefinition>
        <hudson.model.StringParameterDefinition><name>DUP</name></hudson.model.StringParameterDefinition>
        <hudson.model.StringParameterDefinition><name>中文参数</name></hudson.model.StringParameterDefinition>
      </parameterDefinitions>
    </hudson.model.ParametersDefinitionProperty>
  </properties>
  <builders><hudson.tasks.Shell><command>echo ok</command></hudson.tasks.Shell></builders>
</project>`
	p, warnings, err := FromJenkinsXML([]byte(xml), nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := make([]string, 0, len(p.Params))
	for _, def := range p.Params {
		got = append(got, def.Name)
	}
	want := []string{"P_1st", "J_CI_TOKEN", "dup", "DUP_2", "PARAM"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("params = %v, want %v", got, want)
	}
	assertWarning(t, warnings, "params", LevelWarn)
}

// 多申请一个参数定义上限由 service 校验；这里只验证 importer 不吞掉数量。
func TestFromJenkinsXMLManyParams(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<project><properties><hudson.model.ParametersDefinitionProperty><parameterDefinitions>`)
	for i := 0; i < 35; i++ {
		b.WriteString(`<hudson.model.StringParameterDefinition><name>P`)
		b.WriteString(string(rune('A' + i)))
		b.WriteString(`</name></hudson.model.StringParameterDefinition>`)
	}
	b.WriteString(`</parameterDefinitions></hudson.model.ParametersDefinitionProperty></properties>`)
	b.WriteString(`<builders><hudson.tasks.Shell><command>echo ok</command></hudson.tasks.Shell></builders></project>`)
	p, _, err := FromJenkinsXML([]byte(b.String()), nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(p.Params) != 35 {
		t.Fatalf("params = %d, want 35", len(p.Params))
	}
}

func assertWarning(t *testing.T, list []Warning, field, level string) {
	t.Helper()
	for _, w := range list {
		if w.Field == field && w.Level == level {
			return
		}
	}
	t.Fatalf("缺少告警 field=%s level=%s，实际: %+v", field, level, list)
}
