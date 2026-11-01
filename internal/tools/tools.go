package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

const UserMessageMarker = "[[USER_MESSAGE]]"

// ValidTools 是所有合法的 AI CLI 工具名集合。
// config、scene 等包共用此集合，新增工具时只需改这一处（加上
// RendererInvocation、OrchestratorInvocation、ProjectRulesFilename 的 switch）。
var ValidTools = map[string]bool{"codex": true, "claude": true, "qoder": true, "codebuddy": true, "opencode": true}

// RendererAccess 是安全模式下渲染器需要、而默认权限模式不给的最小权限。
//
// 只有需要显式放行的工具才用得上：claude 的 acceptEdits 只自动批准文件编辑，
// Bash 一律拦下，而 -p 模式下没有人来批准——实测渲染器因此跑不了
// `npx hyperframes render` / `snapshot`，镜头直接渲染失败。
type RendererAccess struct {
	// Commands 是放行的命令前缀，例如 "npx --yes hyperframes@0.8.225"。
	Commands []string
	// ReadDirs 是镜头目录之外需要读取的目录（技能树），绝对路径。
	ReadDirs []string
}

// claudeSettings 把 RendererAccess 译成 claude --settings 的 JSON。
// .env 的读取禁令与执行契约一致：渲染器不得读取或打印凭据。
func (a RendererAccess) claudeSettings() (string, error) {
	allow := make([]string, 0, len(a.Commands)+len(a.ReadDirs))
	for _, command := range a.Commands {
		allow = append(allow, "Bash("+command+":*)")
	}
	for _, dir := range a.ReadDirs {
		// claude 的路径规则里，绝对路径要写成 // 开头。
		allow = append(allow, "Read(/"+dir+"/**)")
	}
	dirs := append([]string{}, a.ReadDirs...)
	settings := map[string]any{"permissions": map[string]any{
		"allow":                 allow,
		"deny":                  []string{"Read(**/.env)", "Read(**/.env.*)"},
		"additionalDirectories": dirs,
	}}
	body, err := json.Marshal(settings)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func RendererInvocation(tool, prompt string, unsafe bool, access RendererAccess) ([]string, error) {
	switch tool {
	case "codex":
		if unsafe {
			return []string{"codex", "exec", "--skip-git-repo-check", "--dangerously-bypass-approvals-and-sandbox", "--json", prompt}, nil
		}
		return []string{"codex", "--ask-for-approval", "never", "exec", "--skip-git-repo-check", "--sandbox", "workspace-write", "--json", prompt}, nil
	case "claude":
		mode := []string{"--dangerously-skip-permissions"}
		if !unsafe {
			settings, err := access.claudeSettings()
			if err != nil {
				return nil, err
			}
			mode = []string{"--permission-mode", "acceptEdits", "--settings", settings}
		}
		return append([]string{"claude", "-p"}, append(mode, "--verbose", "--output-format", "stream-json", "--prompt-suggestions", "false", prompt)...), nil
	case "qoder":
		mode := []string{"--permission-mode", "dont_ask"}
		if unsafe {
			mode = []string{"--dangerously-skip-permissions"}
		}
		return append([]string{"qoderclicn", "-p", prompt}, append(mode, "--output-format", "stream-json")...), nil
	case "codebuddy":
		mode := []string{"--permission-mode", "acceptEdits"}
		if unsafe {
			mode = []string{"-y"}
		}
		return append([]string{"codebuddy", "-p", "--verbose", "--output-format", "stream-json"}, append(mode, prompt)...), nil
	case "opencode":
		args := []string{"opencode", "run"}
		if unsafe {
			args = append(args, "--auto")
		}
		return append(args, "--format", "json", prompt), nil
	default:
		return nil, fmt.Errorf("未知工具：%s", tool)
	}
}

// OrchestratorAccess 是安全模式下编排工具要放行的 Bash 命令前缀。
//
// 编排工具要跑 am 子命令、ffmpeg、TTS 与对齐 CLI，而各家「只批准文件编辑」的权限模式
// 一律拦下 Bash（claude 的 acceptEdits、qoder 的 accept_edits），dont_ask 更是连写文件
// 都拒——实测安全模式下整片制作因此一步都走不动。清单由调用方按项目配置算出，
// 见 project.OrchestratorAccess。
type OrchestratorAccess struct {
	Commands []string
}

// OrchestratorOptions 是一次编排调用的全部输入。
type OrchestratorOptions struct {
	Workdir string
	Prompt  string
	Unsafe  bool
	// SessionID 非空且 Resume 为 false 时，表示让工具用这个 id 开新会话（只有
	// PresetSessionID 为 true 的工具支持）；Resume 为 true 时表示续接这个会话。
	SessionID string
	Resume    bool
	Access    OrchestratorAccess
}

// PresetSessionID 报告该工具能否由调用方预先指定新会话的 id。
// codex 与 opencode 不能，它们的 id 要从输出流的第一条事件里读。
func PresetSessionID(tool string) bool {
	return tool == "claude" || tool == "qoder" || tool == "codebuddy"
}

// OrchestratorInvocation 返回编排调用的命令行与要写进 stdin 的内容。
//
// 所有工具都输出结构化流（stream-json / JSONL），由 internal/agentrun 解析。
// 调用方负责把进程的工作目录设为 o.Workdir：opencode 2.x 的 run 已经没有 --dir。
func OrchestratorInvocation(tool string, o OrchestratorOptions) ([]string, string, error) {
	if o.Resume && o.SessionID == "" {
		return nil, "", fmt.Errorf("续接会话需要 session id")
	}
	if o.SessionID != "" && !o.Resume && !PresetSessionID(tool) {
		return nil, "", fmt.Errorf("%s 不支持预先指定会话 id", tool)
	}
	claudeSession := func() []string {
		switch {
		case o.Resume:
			return []string{"--resume", o.SessionID}
		case o.SessionID != "":
			return []string{"--session-id", o.SessionID}
		}
		return nil
	}
	switch tool {
	case "codex":
		args := []string{"codex"}
		if o.Unsafe {
			args = append(args, "exec", "--skip-git-repo-check", "--cd", o.Workdir, "--dangerously-bypass-approvals-and-sandbox")
		} else {
			// workspace-write 沙箱默认断网，TTS 要连外网；codex 没有命令白名单，靠沙箱限制写入范围。
			args = append(args, "--ask-for-approval", "never", "-c", "sandbox_workspace_write.network_access=true",
				"exec", "--skip-git-repo-check", "--cd", o.Workdir, "--sandbox", "workspace-write")
		}
		args = append(args, "--json")
		if o.Resume {
			args = append(args, "resume", o.SessionID)
		}
		return append(args, "-"), o.Prompt, nil
	case "claude", "codebuddy":
		args := []string{tool, "-p", "--verbose", "--output-format", "stream-json"}
		if tool == "claude" {
			args = append(args, "--prompt-suggestions", "false")
		}
		switch {
		case o.Unsafe && tool == "claude":
			args = append(args, "--dangerously-skip-permissions")
		case o.Unsafe:
			args = append(args, "-y")
		default:
			settings, err := RendererAccess{Commands: o.Access.Commands}.claudeSettings()
			if err != nil {
				return nil, "", err
			}
			args = append(args, "--permission-mode", "acceptEdits", "--settings", settings)
		}
		args = append(args, claudeSession()...)
		return append(args, o.Prompt), "", nil
	case "qoder":
		args := []string{"qoderclicn", "-p", o.Prompt, "--output-format", "stream-json"}
		if o.Unsafe {
			args = append(args, "--dangerously-skip-permissions")
		} else {
			args = append(args, "--permission-mode", "accept_edits")
			for _, command := range o.Access.Commands {
				args = append(args, "--allowed-tools", "Bash("+command+":*)")
			}
		}
		switch {
		case o.Resume:
			args = append(args, "-r", o.SessionID)
		case o.SessionID != "":
			args = append(args, "--session-id", o.SessionID)
		}
		return args, "", nil
	case "opencode":
		// opencode 只认项目目录里的 opencode.json，规则集写法实测拦不住命令，
		// 全拒时无头运行直接挂起；安全模式下因此不加限制，见 README。
		args := []string{"opencode", "run", "--format", "json"}
		if o.Unsafe {
			args = append(args, "--auto")
		}
		if o.Resume {
			args = append(args, "-s", o.SessionID)
		}
		return append(args, o.Prompt), "", nil
	default:
		return nil, "", fmt.Errorf("未知工具：%s", tool)
	}
}

// ProjectRulesFilename 返回该工具会自动从工作目录向上递归读取的项目级指令文件名。
//
// 名字必须逐个工具核对，不能统一成 AGENTS.md：Claude Code 官方文档明写
// "Claude Code reads CLAUDE.md, not AGENTS.md"，CodeBuddy 读的是产品名派生的
// CODEBUDDY.md。写错文件名不会报错，只会让规则静默失效。
func ProjectRulesFilename(tool string) (string, error) {
	switch tool {
	case "codex", "qoder", "opencode":
		return "AGENTS.md", nil
	case "claude":
		return "CLAUDE.md", nil
	case "codebuddy":
		return "CODEBUDDY.md", nil
	default:
		return "", fmt.Errorf("未知工具：%s", tool)
	}
}

func ExtractUserMessages(tool, line string) []string {
	var event map[string]any
	if json.Unmarshal([]byte(line), &event) != nil {
		return nil
	}
	var texts []string
	switch tool {
	case "codex":
		item, _ := event["item"].(map[string]any)
		if event["type"] == "item.completed" && item["type"] == "agent_message" {
			if text, ok := item["text"].(string); ok {
				texts = append(texts, text)
			}
		}
	case "claude", "qoder", "codebuddy":
		message, _ := event["message"].(map[string]any)
		content, _ := message["content"].([]any)
		if event["type"] == "assistant" {
			for _, raw := range content {
				block, _ := raw.(map[string]any)
				if block["type"] == "text" {
					if text, ok := block["text"].(string); ok {
						texts = append(texts, text)
					}
				}
			}
		}
	case "opencode":
		part, _ := event["part"].(map[string]any)
		if event["type"] == "text" {
			if text, ok := part["text"].(string); ok {
				texts = append(texts, text)
			}
		}
	}
	var messages []string
	for _, text := range texts {
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, UserMessageMarker) {
				messages = append(messages, strings.TrimPrefix(line, UserMessageMarker))
			}
		}
	}
	return messages
}
