package config

import (
	"reflect"
	"testing"
)

func TestEveryProviderNamesItsCommand(t *testing.T) {
	for _, provider := range TTSProviders {
		if provider.Command == "" {
			t.Errorf("TTS %s 没有登记命令，安全模式下编排工具调用不了它", provider.ID)
		}
	}
	for _, aligner := range Aligners {
		if aligner.Command == "" {
			t.Errorf("对齐工具 %s 没有登记命令", aligner.ID)
		}
	}
}

func TestOrchestratorAccessFollowsConfig(t *testing.T) {
	cfg := Config{TTSProvider: "edge", AlignProvider: "mlx"}
	got := cfg.OrchestratorAccess("npx --yes hyperframes@1.0.0", "am").Commands
	want := []string{"am", "ffmpeg", "ffprobe", "ls", "mkdir", "edge-tts", "mlx_audio.stt.generate", "npx --yes hyperframes@1.0.0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	if again := cfg.OrchestratorAccess().Commands; len(again) != 7 || again[0] != "am" {
		t.Fatalf("基础清单被上一次调用改写了：%v", again)
	}
}
