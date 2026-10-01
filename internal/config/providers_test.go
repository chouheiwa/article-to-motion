package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEveryTTSProviderLoads(t *testing.T) {
	for _, provider := range TTSProviders {
		root := t.TempDir()
		os.WriteFile(filepath.Join(root, "article-to-motion.conf"), []byte("ORCHESTRATOR=codex\nRENDERER=claude\nTTS_PROVIDER="+provider.ID+"\n"), 0o644)
		cfg, err := Load(root, map[string]string{})
		if err != nil || cfg.TTSProvider != provider.ID {
			t.Errorf("%s 应被接受：%v", provider.ID, err)
		}
	}
}

func TestAlignProviderResolution(t *testing.T) {
	cases := []struct{ value, goos, goarch, want string }{
		{"auto", "darwin", "arm64", "mlx"},
		{"auto", "darwin", "amd64", "funasr"},
		{"auto", "linux", "arm64", "funasr"},
		{"funasr", "darwin", "arm64", "funasr"},
		{"mlx", "linux", "amd64", "mlx"},
	}
	for _, tc := range cases {
		if got := resolveAligner(tc.value, tc.goos, tc.goarch); got != tc.want {
			t.Errorf("resolveAligner(%s, %s/%s) = %s，期望 %s", tc.value, tc.goos, tc.goarch, got, tc.want)
		}
	}
}

// TestAlignProviderDefaultsToAutoAndIsExported：缺省按平台解析，编排工具从环境
// 变量拿到的是具体工具名而不是 auto。
func TestAlignProviderDefaultsToAutoAndIsExported(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "article-to-motion.conf"), []byte("ORCHESTRATOR=codex\nRENDERER=claude\n"), 0o644)
	cfg, err := Load(root, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	want := resolveAligner(AlignAuto, runtime.GOOS, runtime.GOARCH)
	if cfg.AlignProvider != want {
		t.Fatalf("缺省对齐工具 = %s，期望 %s", cfg.AlignProvider, want)
	}
	if env := cfg.ChildEnvironment(map[string]string{"PATH": "/bin"}, false, nil); env["ALIGN_PROVIDER"] != want {
		t.Fatalf("子进程环境应带解析后的 ALIGN_PROVIDER：%v", env)
	}
}

func TestProviderIDLists(t *testing.T) {
	if !strings.HasPrefix(TTSProviderIDs(), "minimax bailian ") || !strings.Contains(TTSProviderIDs(), "elevenlabs") {
		t.Errorf("TTS 取值列表不符：%s", TTSProviderIDs())
	}
	if AlignerIDs() != "auto mlx funasr" {
		t.Errorf("对齐取值列表不符：%s", AlignerIDs())
	}
}
