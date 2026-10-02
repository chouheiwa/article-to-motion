package assets

import (
	"io/fs"
	"testing"

	"github.com/chouheiwa/articale-to-motion/internal/preset"
)

func TestPresetAndStyleTreesResolveForEveryCombination(t *testing.T) {
	for _, canvas := range preset.All() {
		tree, err := Preset(canvas.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fs.Stat(tree, "PROMPT-PRODUCTION.md"); err != nil {
			t.Errorf("画幅 %s 缺少 PROMPT-PRODUCTION.md", canvas.ID)
		}
		for _, style := range preset.AllStyles() {
			tree, err := Style(style.ID, canvas.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fs.Stat(tree, style.GuideDoc()); err != nil {
				t.Errorf("%s/%s 缺少说明书 %s", style.ID, canvas.ID, style.GuideDoc())
			}
		}
	}
}

func TestPresetAndStyleRejectUnknownIDs(t *testing.T) {
	if _, err := Preset("no-such-canvas"); err == nil {
		t.Error("未知画幅应报错")
	}
	if _, err := Style("no-such-style", preset.Default().ID); err == nil {
		t.Error("未知风格应报错")
	}
	if _, err := Style(preset.DefaultStyle().ID, "no-such-canvas"); err == nil {
		t.Error("未知画幅下的风格应报错")
	}
}

// 自定义风格的草稿骨架要随每个画幅下发：am init --style custom 直接取这棵树。
func TestCustomDraftTreeResolvesForEveryCanvas(t *testing.T) {
	for _, canvas := range preset.All() {
		tree, err := Style(preset.CustomStyle.ID, canvas.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fs.Stat(tree, preset.CustomStyle.GuideDoc()); err != nil {
			t.Errorf("%s 缺少草稿说明书", canvas.ID)
		}
	}
}
