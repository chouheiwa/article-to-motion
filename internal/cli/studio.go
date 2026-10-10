package cli

import (
	"fmt"
	"io"

	"github.com/chouheiwa/articale-to-motion/internal/studio"
	"github.com/spf13/cobra"
)

func newStudioCommand(stdout, stderr io.Writer) *cobra.Command {
	var listen, configPath string
	cmd := &cobra.Command{
		Use:   "studio [PROJECT_DIR]",
		Short: "启动本地 ArticleToMotion Studio 工作台",
		Long: `启动本地网页工作台，读取并管理一个或多个 ArticleToMotion 项目。

省略 PROJECT_DIR 时显示工作台并等待选择项目；传入目录会登记并直接打开。
默认只监听 127.0.0.1。需要从手机访问时，请先用 Tailscale、VPN 或 SSH 隧道，
再显式指定 --listen；Studio 本身不提供公网账户或 HTTPS。项目文件仍是状态真相源，
工作台不会读取或保存项目凭据。`,
		Args: maxArgs(1, "PROJECT_DIR"),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := ""
			if len(args) == 1 {
				path = args[0]
			}
			if listen == "" {
				listen = "127.0.0.1:4317"
			}
			if listen != "127.0.0.1:4317" && listen != "localhost:4317" && listen != "[::1]:4317" {
				fmt.Fprintf(stderr, "提示：Studio 将监听 %s。远程访问请使用 Tailscale、VPN 或 SSH 隧道。\n", listen)
			}
			s, err := studio.New(studio.Config{ListenAddr: listen, ProjectPath: path, ConfigPath: configPath})
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "ArticleToMotion Studio 已启动：http://%s\n", listen)
			return s.ListenAndServe(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "127.0.0.1:4317", "监听地址（默认只允许本机访问）")
	cmd.Flags().StringVar(&configPath, "config", "", "Studio 项目登记文件路径（默认使用用户配置目录）")
	cmd.Example = "  am studio\n  am studio /path/to/project\n  am studio --listen 127.0.0.1:4317"
	return cmd
}
