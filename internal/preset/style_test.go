package preset

import (
	"strings"
	"testing"
)

func TestDefaultStyleIsFirstAndLookupsAgree(t *testing.T) {
	all := AllStyles()
	if len(all) == 0 || DefaultStyle().ID != all[0].ID {
		t.Fatalf("默认风格应是风格表第一项")
	}
	ids := StyleIDs()
	if len(ids) != len(all) {
		t.Fatalf("StyleIDs 数量 %d 与风格表 %d 不一致", len(ids), len(all))
	}
	seen := map[string]bool{}
	for i, style := range all {
		if ids[i] != style.ID || seen[style.ID] {
			t.Errorf("风格 id 重复或顺序不一致：%s", style.ID)
		}
		seen[style.ID] = true
		got, ok := StyleByID(style.ID)
		if !ok || got.Name != style.Name {
			t.Errorf("StyleByID(%s) 未找到", style.ID)
		}
		if doc := style.GuideDoc(); !strings.HasPrefix(doc, "docs/"+style.Name) || !strings.HasSuffix(doc, "-视频风格说明书.md") {
			t.Errorf("%s 的说明书路径不符合约定：%s", style.ID, doc)
		}
	}
}

func TestAllStylesReturnsCopy(t *testing.T) {
	all := AllStyles()
	all[0].ID = "mutated"
	if DefaultStyle().ID == "mutated" {
		t.Fatal("AllStyles 必须返回副本，调用方改动不能写回风格表")
	}
}

func TestStyleByIDOrErrorListsChoices(t *testing.T) {
	if _, err := StyleByIDOrError(DefaultStyle().ID); err != nil {
		t.Fatal(err)
	}
	_, err := StyleByIDOrError("no-such-style")
	if err == nil || !strings.Contains(err.Error(), DefaultStyle().ID) {
		t.Fatalf("未知风格应报错并列出可选项，实际 %v", err)
	}
}

// 自定义风格的草稿身份不能混进内置风格表：生成器、init 选择框与一串枚举
// 内置风格的测试都遍历那张表，混进去会让它们把草稿当成一套正式风格。
func TestCustomStyleStaysOutOfBuiltinTable(t *testing.T) {
	if _, ok := StyleByID(CustomStyle.ID); ok {
		t.Fatalf("草稿风格 %s 不应出现在内置风格表里", CustomStyle.ID)
	}
	if CustomStyle.ID != DraftStyleID {
		t.Fatalf("草稿风格的 id 应是 %s，实际 %s", DraftStyleID, CustomStyle.ID)
	}
	for _, id := range StyleIDs() {
		if IsCustomStyleID(id) {
			t.Errorf("内置风格 %s 落进了自定义风格的命名空间", id)
		}
	}
}

func TestIsCustomStyleID(t *testing.T) {
	for id, want := range map[string]bool{
		DraftStyleID:         true,
		"custom-warm-film":   true,
		"custom-a1":          true,
		"custom-":            false,
		"custom":             false,
		"custom-Warm":        false,
		"custom-warm--film":  false,
		"custom-warm/../x":   false,
		"pixel-arcade-v1":    false,
		"x-custom-warm-film": false,
	} {
		if got := IsCustomStyleID(id); got != want {
			t.Errorf("IsCustomStyleID(%q) = %v，期望 %v", id, got, want)
		}
	}
}
