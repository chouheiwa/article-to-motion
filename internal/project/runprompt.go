package project

import (
	"fmt"
	"os"
	"path/filepath"

	assets "github.com/chouheiwa/articale-to-motion"
	"github.com/chouheiwa/articale-to-motion/internal/song"
)

// songContract 是歌曲模式追加在提示词末尾的内置契约。
const songContract = "assets/shared/PROMPT-SONG.md"

// RunPrompt 读出整片制作发给编排工具的提示词，am run 与 am studio 共用。
//
// override 非空时直接读它；否则口播项目读项目根的 PROMPT.md，歌曲模式读
// PROMPT-SONG.md，并在末尾追加随二进制下发的歌曲契约——项目里那份可能被
// 用户改过，契约要以内置版本为准。返回实际读取的路径，供打印给人看。
func RunPrompt(root, override string) ([]byte, string, error) {
	path := filepath.Join(root, "PROMPT.md")
	songMode := song.Enabled(root)
	if songMode {
		if _, err := song.LoadConfig(root); err != nil {
			return nil, "", err
		}
		path = filepath.Join(root, "PROMPT-SONG.md")
	}
	if override != "" {
		absolute, err := filepath.Abs(override)
		if err != nil {
			return nil, "", err
		}
		path = absolute
	}
	prompt, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("找不到 prompt 文件：%s", path)
	}
	if songMode {
		contract, err := assets.Files.ReadFile(songContract)
		if err != nil {
			return nil, "", err
		}
		prompt = append(prompt, "\n\n歌曲模式必要契约（优先于口播规程）：\n"...)
		prompt = append(prompt, contract...)
	}
	return prompt, path, nil
}
