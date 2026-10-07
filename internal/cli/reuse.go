package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chouheiwa/articale-to-motion/internal/config"
	"github.com/chouheiwa/articale-to-motion/internal/scene"
	"github.com/chouheiwa/articale-to-motion/internal/schedule"
)

// reuseDecision 判定一个已有合格产物的镜头是跳过还是标 Stale。
//
// 有渲染记录时按输入指纹判定：修改时间会被 touch、git checkout、复制目录
// 随手改掉，也看不见 frame.md、字幕、技能版本这些不在镜头两份文件里的输入。
// 记录缺失（老产物）或指纹算不出来时退回修改时间判定，行为与引入指纹前一致。
func reuseDecision(item scene.Scene, cfg config.Config, env map[string]string) (schedule.Status, string) {
	if record, ok := scene.LoadRecord(item); ok {
		renderer := item.Renderer
		if renderer == "" {
			renderer = cfg.Renderer
		}
		resolved, err := scene.ResolveAllSkills(renderer, item.Directory, scene.SkillsEnvironment(env, cfg.Overlay))
		if err == nil {
			current, err := scene.Fingerprint(item, renderer, resolved)
			if err == nil {
				if current.Fingerprint == record.Fingerprint {
					return schedule.Skipped, "已有合格产物（输入指纹一致）"
				}
				return schedule.Stale, fmt.Sprintf("输入已变化（%s），需要显式 --force 重渲染",
					strings.Join(current.Changed(record.Inputs), "、"))
			}
		}
	}
	outputInfo, err := os.Stat(item.OutputPath())
	if err != nil {
		return schedule.Stale, "无法读取产物，需要显式 --force 重渲染"
	}
	for _, input := range []string{filepath.Join(item.Directory, "scene.json"), filepath.Join(item.Directory, "prompt.md")} {
		if info, statErr := os.Stat(input); statErr == nil && info.ModTime().After(outputInfo.ModTime()) {
			return schedule.Stale, "输入比产物新，需要显式 --force 重渲染"
		}
	}
	return schedule.Skipped, "已有合格产物"
}
