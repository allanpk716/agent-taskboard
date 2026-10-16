// publish-updatehub 把 GitHub Release 上指定 tag 的产物转发到内网
// UpdateHub：下载并按 manifest.json 校验 SHA256，自己生成 .nuspec 打成
// nupkg（不依赖 vpk），把 manifest.json 一起打进包里，连同
// releases.win.json 上传到 agent-taskboard-server / agent-taskboard-web
// 应用的 stable 通道，最后读 feed 确认已登记。
//
// 本仓库公开，UpdateHub 的地址和 token 只从环境变量读取：
//
//	UPDATEHUB_BASE_URL  UpdateHub 基地址，如 https://updatehub.example.com
//	UPDATEHUB_TOKEN     上传用的 Bearer token
//	GITHUB_TOKEN        可选，下载 Release 时附带
//	GITHUB_MIRROR       可选，GitHub 下载镜像前缀（拼在 github.com 地址前面）
//
// 用法：
//
//	publish-updatehub -tag server/v1.2.3 [-repo allanpk716/agent-taskboard]
//	                  [-channel stable] [-notes 备注] [-out 目录] [-dry-run]
//	                  [-download-base URL]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/allanpk716/agent-taskboard/internal/updatehub"
)

const defaultRepo = "allanpk716/agent-taskboard"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "publish-updatehub: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("publish-updatehub", flag.ContinueOnError)
	repo := fs.String("repo", defaultRepo, "GitHub 仓库（OWNER/REPO）")
	tag := fs.String("tag", "", "发布 tag，如 server/v1.2.3")
	channel := fs.String("channel", "stable", "UpdateHub 通道")
	notes := fs.String("notes", "", "版本备注")
	outDir := fs.String("out", "publish-out", "下载产物、nupkg 和 feed 的保存目录")
	dryRun := fs.Bool("dry-run", false, "只下载和打包，不上传（不需要 UpdateHub 配置）")
	downloadBase := fs.String("download-base", "", "覆盖默认下载基址（默认 GITHUB_MIRROR + https://github.com/{repo}/releases/download/{tag}/）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *tag == "" {
		return fmt.Errorf("需要 -tag，例如 -tag server/v1.2.3")
	}

	base := *downloadBase
	if base == "" {
		mirror := strings.TrimRight(os.Getenv("GITHUB_MIRROR"), "/")
		base = fmt.Sprintf("%shttps://github.com/%s/releases/download/%s/", mirror, *repo, *tag)
	}

	opt := updatehub.PublishOptions{
		DownloadBaseURL: base,
		Tag:             *tag,
		Channel:         *channel,
		Notes:           *notes,
		OutDir:          *outDir,
		HubBaseURL:      strings.TrimRight(os.Getenv("UPDATEHUB_BASE_URL"), "/"),
		HubToken:        os.Getenv("UPDATEHUB_TOKEN"),
		GitHubToken:     os.Getenv("GITHUB_TOKEN"),
		DryRun:          *dryRun,
		Log: func(format string, args ...any) {
			fmt.Printf(format+"\n", args...)
		},
	}
	if !opt.DryRun && opt.HubBaseURL == "" {
		return fmt.Errorf("缺少 UpdateHub 地址：请设置环境变量 UPDATEHUB_BASE_URL（token 用 UPDATEHUB_TOKEN）")
	}
	res, err := updatehub.Publish(context.Background(), opt)
	if err != nil {
		return err
	}
	fmt.Printf("完成：%s %s -> %s/%s（nupkg %s，%d 字节）\n",
		res.Component, res.Version, res.AppID, res.Channel, res.NupkgName, res.NupkgSize)
	return nil
}
