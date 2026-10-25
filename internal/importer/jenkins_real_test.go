package importer

import (
	"strings"
	"testing"
)

// 真实 Jenkins 2.x 导出的 config.xml 结构（敏感信息已替换为占位值）：
//   - 声明是 version='1.1'（Go 的 encoding/xml 只认 1.0，必须预处理）
//   - GitLab 连接属性 + GitLabPushTrigger（带 secretToken，不能迁移）
//   - Publish Over SSH 出现在 **builders** 里（BapSshBuilderPlugin），不是 publishers
//   - NodeJSBuildWrapper 选择 node 版本
//   - <concurrentBuild>false</concurrentBuild>
const realJenkinsXML = `<?xml version='1.1' encoding='UTF-8'?>
<project>
  <actions/>
  <description>南亚东南亚语言实践平台-积分商城管理后台</description>
  <keepDependencies>false</keepDependencies>
  <properties>
    <com.dabsquared.gitlabjenkins.connection.GitLabConnectionProperty plugin="gitlab-plugin@1.9.9">
      <gitLabConnection>gitlab</gitLabConnection>
      <jobCredentialId></jobCredentialId>
      <useAlternativeCredential>false</useAlternativeCredential>
    </com.dabsquared.gitlabjenkins.connection.GitLabConnectionProperty>
  </properties>
  <scm class="hudson.plugins.git.GitSCM" plugin="git@5.8.0">
    <configVersion>2</configVersion>
    <userRemoteConfigs>
      <hudson.plugins.git.UserRemoteConfig>
        <url>https://git.example.com/team/admin_web.git</url>
        <credentialsId>00000000-0000-0000-0000-000000000000</credentialsId>
      </hudson.plugins.git.UserRemoteConfig>
    </userRemoteConfigs>
    <branches>
      <hudson.plugins.git.BranchSpec>
        <name>*/dev</name>
      </hudson.plugins.git.BranchSpec>
    </branches>
    <doGenerateSubmoduleConfigurations>false</doGenerateSubmoduleConfigurations>
    <submoduleCfg class="empty-list"/>
    <extensions/>
  </scm>
  <canRoam>true</canRoam>
  <disabled>false</disabled>
  <blockBuildWhenDownstreamBuilding>false</blockBuildWhenDownstreamBuilding>
  <blockBuildWhenUpstreamBuilding>false</blockBuildWhenUpstreamBuilding>
  <jdk>(System)</jdk>
  <triggers>
    <com.dabsquared.gitlabjenkins.GitLabPushTrigger plugin="gitlab-plugin@1.9.9">
      <spec></spec>
      <triggerOnPush>true</triggerOnPush>
      <triggerOnMergeRequest>true</triggerOnMergeRequest>
      <noteRegex>Jenkins please retry a build</noteRegex>
      <ciSkip>true</ciSkip>
      <branchFilterType>All</branchFilterType>
      <secretToken>{PLACEHOLDER-ENCRYPTED-TOKEN=}</secretToken>
    </com.dabsquared.gitlabjenkins.GitLabPushTrigger>
  </triggers>
  <concurrentBuild>false</concurrentBuild>
  <builders>
    <hudson.tasks.Shell>
      <command>rm -rf dist dist.tar.gz
node --version
npm --version
npm --registry https://registry.npmjs.org install
npm run build
tar zvcf dist.tar.gz dist
</command>
      <configuredLocalRules/>
    </hudson.tasks.Shell>
    <jenkins.plugins.publish__over__ssh.BapSshBuilderPlugin plugin="publish-over-ssh@1.25">
      <delegate>
        <consolePrefix>SSH: </consolePrefix>
        <delegate plugin="publish-over@0.22">
          <publishers>
            <jenkins.plugins.publish__over__ssh.BapSshPublisher plugin="publish-over-ssh@1.25">
              <configName>测试服务器-10.0.0.1</configName>
              <verbose>false</verbose>
              <transfers>
                <jenkins.plugins.publish__over__ssh.BapSshTransfer>
                  <remoteDirectory>admin_web</remoteDirectory>
                  <sourceFiles>dist.tar.gz</sourceFiles>
                  <excludes></excludes>
                  <removePrefix></removePrefix>
                  <remoteDirectorySDF>false</remoteDirectorySDF>
                  <flatten>true</flatten>
                  <cleanRemote>true</cleanRemote>
                  <usePty>true</usePty>
                  <execCommand>rm -rf /data/admin_web/dist
tar zxf /data/admin_web/dist.tar.gz -C /data/admin_web/
chown -R nginx.nginx /data/admin_web/dist</execCommand>
                  <execTimeout>120000</execTimeout>
                  <usePty>false</usePty>
                </jenkins.plugins.publish__over__ssh.BapSshTransfer>
              </transfers>
              <useWorkspaceInPromotion>false</useWorkspaceInPromotion>
            </jenkins.plugins.publish__over__ssh.BapSshPublisher>
          </publishers>
          <continueOnError>false</continueOnError>
          <failOnError>false</failOnError>
          <alwaysPublishFromMaster>false</alwaysPublishFromMaster>
        </delegate>
      </delegate>
    </jenkins.plugins.publish__over__ssh.BapSshBuilderPlugin>
  </builders>
  <publishers/>
  <buildWrappers>
    <jenkins.plugins.nodejs.NodeJSBuildWrapper plugin="nodejs@1.6.5">
      <nodeJSInstallationName>node22.14.0</nodeJSInstallationName>
      <cacheLocationStrategy class="jenkins.plugins.nodejs.cache.DefaultCacheLocationLocator"/>
    </jenkins.plugins.nodejs.NodeJSBuildWrapper>
  </buildWrappers>
</project>`

// 真实导出文件必须能直接导入：XML 1.1 声明、builders 里的 SSH 发布、环境变量式构建步骤。
func TestFromJenkinsXMLRealWorldSample(t *testing.T) {
	servers := []SshServerRef{{ID: "srv-test", Name: "测试服务器-10.0.0.1", Host: "10.0.0.1"}}
	p, warnings, err := FromJenkinsXML([]byte(realJenkinsXML), servers)
	if err != nil {
		t.Fatalf("真实 config.xml 解析失败: %v", err)
	}

	if p.Description != "南亚东南亚语言实践平台-积分商城管理后台" {
		t.Fatalf("description = %q", p.Description)
	}
	if p.GitURL != "https://git.example.com/team/admin_web.git" || p.GitBranch != "dev" {
		t.Fatalf("scm = %q/%q", p.GitURL, p.GitBranch)
	}
	// SSH 发布在 builders 里：不能被当成「不支持的构建步骤」，也不能丢。
	if len(p.Steps) != 1 || p.Steps[0].Name != "shell-1" {
		t.Fatalf("steps = %+v", p.Steps)
	}
	if !strings.Contains(p.Steps[0].Run, "npm run build") ||
		!strings.Contains(p.Steps[0].Run, "tar zvcf dist.tar.gz") {
		t.Fatalf("多行命令未原样保留: %q", p.Steps[0].Run)
	}
	if len(p.SshPublishes) != 1 {
		t.Fatalf("ssh publishes = %+v", p.SshPublishes)
	}
	pub := p.SshPublishes[0]
	if pub.Name != "测试服务器-10.0.0.1" || pub.ServerID != "srv-test" || !pub.Enabled {
		t.Fatalf("ssh publish = %+v", pub)
	}
	if pub.RemoteBaseDir != "admin_web" || len(pub.SourceFiles) != 1 || pub.SourceFiles[0] != "dist.tar.gz" {
		t.Fatalf("ssh publish 字段 = %+v", pub)
	}
	if !strings.Contains(pub.ExecCommand, "tar zxf") || !strings.Contains(pub.ExecCommand, "chown -R") {
		t.Fatalf("execCommand = %q", pub.ExecCommand)
	}
	// transfer 选项：cleanRemote / flatten 直接映射；execTimeout 毫秒 → 秒。
	if !pub.CleanRemote || !pub.Flatten {
		t.Fatalf("clean/flatten = %v/%v, want true/true", pub.CleanRemote, pub.Flatten)
	}
	if pub.ExecTimeoutSec != 120 {
		t.Fatalf("execTimeoutSec = %d, want 120", pub.ExecTimeoutSec)
	}
	// Jenkins 的 secretToken 绝不能进入导出/任务配置。
	if strings.Contains(pub.ExecCommand, "PLACEHOLDER") || p.WebhookSecret != "" {
		t.Fatal("密钥不应被导入")
	}
	// 定时：这里没有 TimerTrigger，GitLab 触发器不能变成 cron。
	if p.CronSpec != "" {
		t.Fatalf("cron = %q, want 空（GitLab 推送触发不是定时）", p.CronSpec)
	}

	// 告警：凭据、GitLab 触发器、NodeJS 版本、SSH 顺序语义都要提示。
	assertWarning(t, warnings, "scm", LevelWarn)      // 凭据无法迁移
	assertWarning(t, warnings, "triggers", LevelWarn) // GitLabPushTrigger 不受支持
	assertWarning(t, warnings, "wrappers", LevelInfo) // NodeJS 安装项
	assertWarning(t, warnings, "ssh", LevelInfo)      // 发布位于构建步骤中间
	if hasLevel(t, warnings, LevelError) {
		t.Fatalf("这份任务不应有 error 级告警: %+v", warnings)
	}
}

// 未匹配服务器时同样要停用并给 error（真实场景下用户还没建服务器）。
func TestFromJenkinsXMLRealWorldWithoutServers(t *testing.T) {
	p, warnings, err := FromJenkinsXML([]byte(realJenkinsXML), nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.SshPublishes[0].Enabled || p.SshPublishes[0].ServerID != "" {
		t.Fatalf("未匹配服务器时应停用: %+v", p.SshPublishes[0])
	}
	assertWarning(t, warnings, "ssh", LevelError)
}

// XML 1.1 声明（单/双引号）都能被改写为 1.0；其它内容原样保留。
func TestNormalizeXMLDeclaration(t *testing.T) {
	cases := map[string]string{
		`<?xml version='1.1' encoding='UTF-8'?><project/>`: `<?xml version='1.0' encoding='UTF-8'?><project/>`,
		`<?xml version="1.1" encoding="UTF-8"?><project/>`: `<?xml version="1.0" encoding="UTF-8"?><project/>`,
		`<?xml version='1.0'?><project/>`:                  `<?xml version='1.0'?><project/>`,
		`<project/>`:                                       `<project/>`,
		"\n<?xml version='1.1'?><project/>":                "<?xml version='1.0'?><project/>",
	}
	for in, want := range cases {
		if got := string(normalizeXMLDeclaration([]byte(in))); got != want {
			t.Fatalf("normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

// usePty 等不支持的 transfer 选项必须逐条告警（不能静默忽略）。
func TestFromJenkinsXMLUnsupportedTransferOptions(t *testing.T) {
	_, warnings, err := FromJenkinsXML([]byte(realJenkinsXML), nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	found := false
	for _, w := range warnings {
		if w.Field == "ssh" && strings.Contains(w.Message, "Exec PTY") {
			found = true
		}
	}
	if !found {
		t.Fatalf("usePty 应产生告警，实际: %+v", warnings)
	}
}

func hasField(list []Warning, field string) bool {
	for _, w := range list {
		if w.Field == field {
			return true
		}
	}
	return false
}

func hasLevel(t *testing.T, list []Warning, level string) bool {
	t.Helper()
	for _, w := range list {
		if w.Level == level {
			return true
		}
	}
	return false
}
