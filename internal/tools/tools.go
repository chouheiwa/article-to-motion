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

func OrchestratorInvocation(tool, workdir, prompt string, unsafe bool) ([]string, string, error) {
	switch tool {
	case "codex":
		if unsafe {
			return []string{"codex", "exec", "--skip-git-repo-check", "--cd", workdir, "--dangerously-bypass-approvals-and-sandbox", "-"}, prompt, nil
		}
		return []string{"codex", "--ask-for-approval", "never", "exec", "--skip-git-repo-check", "--cd", workdir, "--sandbox", "workspace-write", "-"}, prompt, nil
	case "claude":
		if unsafe {
			return []string{"claude", "-p", prompt, "--dangerously-skip-permissions"}, "", nil
		}
		return []string{"claude", "-p", prompt, "--permission-mode", "acceptEdits"}, "", nil
	case "qoder":
		if unsafe {
			return []string{"qoderclicn", "-p", prompt, "--dangerously-skip-permissions", "--output-format", "stream-json"}, "", nil
		}
		return []string{"qoderclicn", "-p", prompt, "--permission-mode", "dont_ask", "--output-format", "stream-json"}, "", nil
	case "codebuddy":
		if unsafe {
			return []string{"codebuddy", "-p", prompt, "-y"}, "", nil
		}
		return []string{"codebuddy", "-p", prompt, "--permission-mode", "acceptEdits"}, "", nil
	case "opencode":
		args := []string{"opencode", "run", "--dir", workdir}
		if unsafe {
			args = append(args, "--auto")
		}
		return append(args, prompt), "", nil
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
