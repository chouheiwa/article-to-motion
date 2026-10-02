package preset

import "testing"

// AllFonts 是自定义风格可选字体的全集，每个内置风格用到的字体都必须在里面，
// 否则 am style add-font 拿不到内置风格明明在用的字体。
func TestAllFontsCoversEveryStyleFont(t *testing.T) {
	files := map[string]bool{}
	for _, font := range AllFonts() {
		if files[font.File] {
			t.Errorf("AllFonts 重复列出 %s", font.File)
		}
		files[font.File] = true
	}
	for _, style := range AllStyles() {
		for _, font := range style.Fonts {
			if !files[font.File] {
				t.Errorf("风格 %s 的字体 %s 不在 AllFonts 里", style.ID, font.File)
			}
		}
	}
}

func TestAllFontsReturnsCopy(t *testing.T) {
	all := AllFonts()
	all[0].File = "mutated"
	if AllFonts()[0].File == "mutated" {
		t.Fatal("AllFonts 必须返回副本")
	}
}
