package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	assets "github.com/chouheiwa/articale-to-motion"
	"github.com/chouheiwa/articale-to-motion/internal/preset"
	"github.com/chouheiwa/articale-to-motion/internal/project"
)

// newStyleCommand 保留旧版字体管理命令，供存量项目使用。
//
// 字体只能来自字体池或用户自带文件：渲染机没有系统字体，临时下载的字体既不可复现，
// 许可也说不清。
func newStyleCommand(stdout io.Writer) *cobra.Command {
	styleCmd := &cobra.Command{
		Use:   "style",
		Short: "旧版项目的字体工具",
		Long: `旧版项目或需要本地字体的镜头可以使用。

fonts 列出可用字体：随每个项目下发的正文字体，以及字体池里可按需拷入的字体。
add-font 把字体池里的字体连同许可证拷进项目的 assets/fonts/；拷入后还要在
镜头代码通过相对路径引用；不要依赖系统字体。`,
	}
	styleCmd.AddCommand(&cobra.Command{
		Use:   "fonts",
		Args:  noArgs,
		Short: "列出可用字体",
		Long: `列出自定义风格可以使用的全部字体：族名、字重与项目内路径。

第一组随每个项目下发；第二组在字体池里，要先用 am style add-font 拷进项目。
不在清单里的字体只能由用户自带 woff2 文件，
不得引用本机系统字体——渲染机上没有。`,
		Example: `  am style fonts`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(stdout, "随项目下发（无需拷入，可直接声明）：")
			for _, font := range preset.SharedFonts {
				fmt.Fprintf(stdout, "  %-32s %4d  %s%s\n", font.Family, font.Weight, assets.FontsDir, font.File)
			}
			fmt.Fprintln(stdout, "字体池（用 am style add-font FAMILY [--weight N] 拷入）：")
			for _, font := range preset.AllFonts() {
				fmt.Fprintf(stdout, "  %-32s %4d  %s%s\n", font.Family, font.Weight, assets.FontsDir, font.File)
			}
			return nil
		},
	})
	var projectRoot string
	var weight int
	addCmd := &cobra.Command{
		Use:   "add-font FAMILY",
		Args:  exactArgs(1, "FAMILY"),
		Short: "把字体池里的字体拷进项目",
		Long: `把字体池里 FAMILY 的字体文件与许可证拷进 assets/fonts/。族名不区分大小写；
不传 --weight 时拷入该族全部字重。

已存在且内容相同的文件跳过，内容不同时整体失败、不覆盖。`,
		Example: `  am style add-font "LXGW WenKai"
  am style add-font Fredoka --weight 700 --project-root .`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fonts, err := poolFonts(args[0], weight)
			if err != nil {
				return err
			}
			files, err := assets.FontFiles(fonts)
			if err != nil {
				return err
			}
			rootDir := projectRoot
			if rootDir == "" {
				rootDir = "."
			}
			// 写错目录时不能凭空建出 assets/fonts/ 还报成功。
			if info, statErr := os.Stat(filepath.Join(rootDir, "frame.md")); statErr != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("%s 不是 am 项目（缺少 frame.md），用 --project-root 指向项目根目录", rootDir)
			}
			result, err := project.InitializeWithFiles(rootDir, files)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "已拷入 %s（新增 %d，未变 %d）。请在镜头代码中用相对路径引用：\n", fonts[0].Family, result.Created, result.Unchanged)
			for _, font := range fonts {
				fmt.Fprintf(stdout, "  - {family: %q, weight: %d, file: %q}\n", font.Family, font.Weight, assets.FontsDir+font.File)
			}
			return nil
		},
	}
	addCmd.Flags().StringVar(&projectRoot, "project-root", "", "项目根目录，缺省为当前目录")
	addCmd.Flags().IntVar(&weight, "weight", 0, "只拷入指定字重，缺省为该族全部字重")
	styleCmd.AddCommand(addCmd)
	return styleCmd
}

// poolFonts 按族名（不区分大小写）与可选字重在字体池里查字体。
func poolFonts(family string, weight int) ([]preset.Font, error) {
	var matched []preset.Font
	var weights []string
	for _, font := range preset.AllFonts() {
		if !strings.EqualFold(font.Family, strings.TrimSpace(family)) {
			continue
		}
		weights = append(weights, fmt.Sprint(font.Weight))
		if weight == 0 || font.Weight == weight {
			matched = append(matched, font)
		}
	}
	if len(weights) == 0 {
		return nil, fmt.Errorf("字体池里没有字体族 %q，用 am style fonts 查看可选字体", family)
	}
	if len(matched) == 0 {
		return nil, fmt.Errorf("字体族 %q 没有字重 %d，可选：%s", family, weight, strings.Join(weights, " "))
	}
	return matched, nil
}
