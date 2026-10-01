package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/chouheiwa/articale-to-motion/internal/envutil"
	"github.com/chouheiwa/articale-to-motion/internal/tools"
)

const (
	DefaultSceneJobs = 3
	MaxSceneJobs     = 16
)

var secretKey = regexp.MustCompile(`(?i)(API_KEY|TOKEN|SECRET|PASSWORD)$`)

type Config struct {
	Orchestrator string
	Renderer     string
	TTSProvider  string
	// AlignProvider 是已解析的对齐工具，auto 已落成具体取值。
	AlignProvider string
	SceneJobs     int
	Overlay       map[string]string
}

func envMap() map[string]string {
	return envutil.EnvMap()
}

func parseFile(path string) (map[string]string, error) {
	out := make(map[string]string)
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"')) {
			value = value[1 : len(value)-1]
		}
		out[key] = value
	}
	return out, scanner.Err()
}

func Load(root string, environ map[string]string) (Config, error) {
	if environ == nil {
		environ = envMap()
	}
	dotenv, err := parseFile(filepath.Join(root, ".env"))
	if err != nil {
		return Config{}, fmt.Errorf("读取 .env: %w", err)
	}
	for key := range dotenv {
		if secretKey.MatchString(key) {
			return Config{}, fmt.Errorf(".env 不得保存密钥 %s，请使用对应工具的登录命令", key)
		}
	}
	conf, err := parseFile(filepath.Join(root, "article-to-motion.conf"))
	if err != nil {
		return Config{}, fmt.Errorf("读取 article-to-motion.conf: %w", err)
	}
	resolve := func(key, fallback string) string {
		for _, source := range []map[string]string{environ, dotenv, conf} {
			if value := source[key]; value != "" {
				return value
			}
		}
		return fallback
	}
	cfg := Config{Orchestrator: resolve("ORCHESTRATOR", ""), Renderer: resolve("RENDERER", ""), TTSProvider: resolve("TTS_PROVIDER", "minimax"), Overlay: dotenv}
	if !tools.ValidTools[cfg.Orchestrator] {
		return Config{}, fmt.Errorf("无效的 ORCHESTRATOR: %s（可选：codex claude qoder codebuddy opencode）", cfg.Orchestrator)
	}
	if !tools.ValidTools[cfg.Renderer] {
		return Config{}, fmt.Errorf("无效的 RENDERER: %s（可选：codex claude qoder codebuddy opencode）", cfg.Renderer)
	}
	if !validTTSProvider(cfg.TTSProvider) {
		return Config{}, fmt.Errorf("无效的 TTS_PROVIDER: %s（可选：%s）", cfg.TTSProvider, TTSProviderIDs())
	}
	align := resolve("ALIGN_PROVIDER", AlignAuto)
	if align != AlignAuto && !validAligner(align) {
		return Config{}, fmt.Errorf("无效的 ALIGN_PROVIDER: %s（可选：%s）", align, AlignerIDs())
	}
	cfg.AlignProvider = hostAligner(align)
	cfg.SceneJobs, err = strconv.Atoi(resolve("SCENE_JOBS", strconv.Itoa(DefaultSceneJobs)))
	if err != nil || cfg.SceneJobs < 1 || cfg.SceneJobs > MaxSceneJobs {
		return Config{}, fmt.Errorf("SCENE_JOBS 必须在 1..%d 之间", MaxSceneJobs)
	}
	return cfg, nil
}

func (c Config) ChildEnvironment(base map[string]string, unsafe bool, passthrough []string) map[string]string {
	if base == nil {
		base = envMap()
	}
	out := make(map[string]string)
	if unsafe {
		for key, value := range base {
			out[key] = value
		}
	} else {
		// USER / LOGNAME / SHELL 不是凭据，但工具链要靠它们确定身份：
		// macOS 上 claude CLI 缺 USER 就读不到 Keychain 里的登录态，
		// 报的却是「Not logged in」，与权限隔离毫无关联，极难排查。
		allowed := []string{"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "LANG", "LC_ALL", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "SSL_CERT_FILE", "SSL_CERT_DIR"}
		allowed = append(allowed, passthrough...)
		for _, key := range allowed {
			if value, ok := base[key]; ok {
				out[key] = value
			}
		}
	}
	for key, value := range c.Overlay {
		out[key] = value
	}
	out["ORCHESTRATOR"] = c.Orchestrator
	out["RENDERER"] = c.Renderer
	out["TTS_PROVIDER"] = c.TTSProvider
	if c.AlignProvider != "" {
		out["ALIGN_PROVIDER"] = c.AlignProvider
	}
	if unsafe {
		out["AM_UNSAFE"] = "1"
	}
	return out
}
