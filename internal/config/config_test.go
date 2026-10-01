package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadPrecedence(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "article-to-motion.conf"), []byte("ORCHESTRATOR=codex\nRENDERER=claude\nSCENE_JOBS=2\n"), 0o644)
	os.WriteFile(filepath.Join(root, ".env"), []byte("RENDERER=qoder\nSCENE_JOBS=4\n"), 0o600)

	cfg, err := Load(root, map[string]string{"ORCHESTRATOR": "opencode"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Orchestrator != "opencode" || cfg.Renderer != "qoder" || cfg.SceneJobs != 4 || cfg.TTSProvider != "minimax" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestLoadAllowsSameTools(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "article-to-motion.conf"), []byte("ORCHESTRATOR=codex\nRENDERER=codex\n"), 0o644)
	cfg, err := Load(root, map[string]string{})
	if err != nil {
		t.Fatalf("same tool should be allowed: %v", err)
	}
	if cfg.Orchestrator != "codex" || cfg.Renderer != "codex" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestSafeChildEnvironment(t *testing.T) {
	cfg := Config{Orchestrator: "codex", Renderer: "claude", TTSProvider: "minimax", Overlay: map[string]string{"MINIMAX_VOICE_ID": "voice"}}
	base := map[string]string{"PATH": "/bin", "HOME": "/home/test", "AWS_SECRET_ACCESS_KEY": "secret", "CUSTOM": "ok"}
	env := cfg.ChildEnvironment(base, false, []string{"CUSTOM"})
	want := map[string]string{"PATH": "/bin", "HOME": "/home/test", "CUSTOM": "ok", "MINIMAX_VOICE_ID": "voice", "ORCHESTRATOR": "codex", "RENDERER": "claude", "TTS_PROVIDER": "minimax"}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("env mismatch\n got: %#v\nwant: %#v", env, want)
	}
}

// USER 不是密钥，但 macOS 上 claude CLI 靠它定位 Keychain 凭据。
// 白名单漏掉它时，渲染器会以「Not logged in」失败，且失败信息和权限隔离毫无关联。
func TestSafeChildEnvironmentKeepsIdentityVariables(t *testing.T) {
	cfg := Config{Orchestrator: "codex", Renderer: "claude", TTSProvider: "minimax"}
	base := map[string]string{"PATH": "/bin", "HOME": "/home/test", "USER": "test", "LOGNAME": "test", "SHELL": "/bin/zsh"}
	env := cfg.ChildEnvironment(base, false, nil)
	for _, key := range []string{"USER", "LOGNAME", "SHELL"} {
		if env[key] != base[key] {
			t.Errorf("%s must survive the allowlist, got %q", key, env[key])
		}
	}
	if _, leaked := env["AWS_SECRET_ACCESS_KEY"]; leaked {
		t.Fatal("allowlist must still drop unrelated variables")
	}
}

func TestDotEnvRejectsSecrets(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "article-to-motion.conf"), []byte("ORCHESTRATOR=codex\nRENDERER=claude\n"), 0o644)
	os.WriteFile(filepath.Join(root, ".env"), []byte("OPENAI_API_KEY=secret\n"), 0o600)
	if _, err := Load(root, nil); err == nil {
		t.Fatal("expected secret rejection")
	}
}

func TestLoadRejectsInvalidChoicesAndJobs(t *testing.T) {
	for name, body := range map[string]string{
		"missing": "RENDERER=claude\n",
		"tts":     "ORCHESTRATOR=codex\nRENDERER=claude\nTTS_PROVIDER=bad\n",
		"align":   "ORCHESTRATOR=codex\nRENDERER=claude\nALIGN_PROVIDER=whisper\n",
		"jobs":    "ORCHESTRATOR=codex\nRENDERER=claude\nSCENE_JOBS=99\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			os.WriteFile(filepath.Join(root, "article-to-motion.conf"), []byte(body), 0o644)
			if _, err := Load(root, map[string]string{}); err == nil {
				t.Fatal("expected invalid config")
			}
		})
	}
}
