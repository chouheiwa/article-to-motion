package studio

import (
	"fmt"
	"net/http"

	"github.com/chouheiwa/articale-to-motion/internal/agentrun"
	"github.com/chouheiwa/articale-to-motion/internal/config"
	"github.com/chouheiwa/articale-to-motion/internal/envutil"
	"github.com/chouheiwa/articale-to-motion/internal/tools"
)

// agentOrder 是网页上编排工具的展示顺序与名称。取值必须都在 tools.ValidTools 里，
// TestAgentOrderCoversValidTools 守着两边一致。
var agentOrder = []struct{ ID, Label string }{
	{"claude", "Claude Code"},
	{"codex", "Codex"},
	{"qoder", "Qoder"},
	{"codebuddy", "CodeBuddy"},
	{"opencode", "OpenCode"},
}

// agentChoice 是网页为一个会话选的编排工具与渲染工具。空值表示沿用项目配置。
type agentChoice struct {
	Tool     string `json:"tool"`
	Renderer string `json:"renderer"`
}

func (c agentChoice) validate() error {
	if c.Tool != "" && !tools.ValidTools[c.Tool] {
		return fmt.Errorf("未知的编排工具：%s", c.Tool)
	}
	if c.Renderer != "" && !tools.ValidTools[c.Renderer] {
		return fmt.Errorf("未知的渲染工具：%s", c.Renderer)
	}
	return nil
}

// withDefaults 用项目配置补齐没选的那一项。
func (c agentChoice) withDefaults(cfg config.Config) agentChoice {
	if c.Tool == "" {
		c.Tool = cfg.Orchestrator
	}
	if c.Renderer == "" {
		c.Renderer = cfg.Renderer
	}
	return c
}

// sessionConfig 读项目配置，再用会话选定的工具覆盖。Studio 不改 article-to-motion.conf：
// 会话里选的工具只对这个会话生效，经 ChildEnvironment 写进子进程的 ORCHESTRATOR / RENDERER，
// 编排工具在这一轮里调用的 am scene run 因此也用会话选的渲染工具。
func sessionConfig(projectPath string, meta SessionMeta) (config.Config, error) {
	cfg, err := config.Load(projectPath, nil)
	if err != nil {
		return config.Config{}, err
	}
	return overrideAgents(cfg, agentChoice{Tool: meta.Tool, Renderer: meta.Renderer})
}

func overrideAgents(cfg config.Config, choice agentChoice) (config.Config, error) {
	if err := choice.validate(); err != nil {
		return config.Config{}, err
	}
	if choice.Tool != "" {
		cfg.Orchestrator = choice.Tool
	}
	if choice.Renderer != "" {
		cfg.Renderer = choice.Renderer
	}
	return cfg, nil
}

type agentInfo struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Command   string `json:"command"`
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
}

// executable 从调用矩阵取工具的命令名，不在这里另写一份。
func executable(tool string) string {
	args, _, err := tools.OrchestratorInvocation(tool, tools.OrchestratorOptions{Workdir: ".", Prompt: "-", SessionID: agentrun.NewSessionID(tool)})
	if err != nil || len(args) == 0 {
		return tool
	}
	return args[0]
}

// agentsAPI 列出五家工具在本机是否装好，以及项目配置里的默认选择。
func (s *Server) agentsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.method(w, http.MethodGet)
		return
	}
	path := envutil.EnvMap()["PATH"]
	agents := make([]agentInfo, 0, len(agentOrder))
	for _, a := range agentOrder {
		info := agentInfo{ID: a.ID, Label: a.Label, Command: executable(a.ID)}
		if found, err := envutil.LookPath(info.Command, path); err == nil {
			info.Installed, info.Path = true, found
		}
		agents = append(agents, info)
	}
	result := map[string]any{"agents": agents, "orchestrator": "", "renderer": ""}
	if p, err := s.currentProject(); err == nil {
		if cfg, err := config.Load(p.Path, nil); err == nil {
			result["orchestrator"], result["renderer"] = cfg.Orchestrator, cfg.Renderer
		} else {
			result["config_error"] = err.Error()
		}
	}
	s.write(w, http.StatusOK, result)
}

// switchSessionAgents 改一个会话的编排工具或渲染工具，从下一轮起生效。换编排工具时原 CLI 会话
// 对新工具没有意义，清掉会话 id，下一轮按续接失败的路子带上对话记录开新会话。
func (s *Server) switchSessionAgents(w http.ResponseWriter, r *http.Request, sess *session) {
	if r.Method != http.MethodPost {
		s.method(w, http.MethodPost)
		return
	}
	var choice agentChoice
	if err := decodeJSON(r, &choice); err != nil {
		s.error(w, http.StatusBadRequest, "invalid_json", "请求不是合法 JSON")
		return
	}
	if err := choice.validate(); err != nil {
		s.error(w, http.StatusBadRequest, "invalid_agent", err.Error())
		return
	}
	s.sessions.mu.Lock()
	running := sess.meta.RunningTurn != ""
	before := sess.meta
	s.sessions.mu.Unlock()
	if running {
		s.error(w, http.StatusConflict, "turn_running", "这一轮还在进行，结束后再换工具")
		return
	}
	switched := choice.Tool != "" && choice.Tool != before.Tool
	s.updateMeta(sess, func(meta *SessionMeta) {
		if choice.Tool != "" {
			meta.Tool = choice.Tool
		}
		if choice.Renderer != "" {
			meta.Renderer = choice.Renderer
		}
		if switched {
			meta.CLISessionID = ""
		}
	})
	if switched {
		s.emit(sess, "", SessionEvent{Event: agentrun.Event{Kind: kindNotice, Text: "编排工具改为 " + choice.Tool + "，下一轮会带上之前的对话记录开新会话"}})
	}
	s.sessions.mu.Lock()
	meta := sess.meta
	s.sessions.mu.Unlock()
	s.write(w, http.StatusOK, meta)
}
