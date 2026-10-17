// release 是发布流水线的本地工具，GitHub Actions 与开发机演练共用。
//
// 用法：
//
//	release manifest -component server -version 1.2.3 [-min-server 1.2.3] -dir dist [-o dist/manifest.json]
//	release latest   -component web -version 1.4.0 -tag web/v1.4.0 -repo OWNER/REPO [-file latest.json] [-out latest.json]
//	release check-exe -goos windows -goarch amd64 dist/taskboard.exe
//
// latest 默认读改写同一个文件（-file），另一组件的条目原样保留。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/allanpk716/agent-taskboard/internal/release"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "release: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage(os.Stderr)
		return fmt.Errorf("缺少子命令")
	}
	switch args[0] {
	case "manifest":
		return cmdManifest(args[1:])
	case "latest":
		return cmdLatest(args[1:])
	case "check-exe":
		return cmdCheckExe(args[1:])
	case "help", "-h", "--help":
		usage(os.Stdout)
		return nil
	default:
		usage(os.Stderr)
		return fmt.Errorf("未知子命令 %q", args[0])
	}
}

func usage(w *os.File) {
	fmt.Fprintln(w, `用法：
  release manifest -component server|web -version X.Y.Z [-min-server X.Y.Z] -dir dist [-o 输出路径]
      为 dist 下的产物生成 manifest.json（SHA256、大小）；缺省记录全部文件并写入 dist/manifest.json
  release latest -component server|web -version X.Y.Z -tag server/vX.Y.Z -repo OWNER/REPO [-file latest.json] [-out latest.json]
      读改写 latest.json：更新本组件条目，保留另一组件；缺省写回 -file 指向的文件
  release check-exe -goos windows|linux -goarch amd64 文件...
      核对产物是能在该平台运行的可执行文件（构建后、生成 manifest 前调用）`)
}

func cmdManifest(args []string) error {
	fs := flag.NewFlagSet("manifest", flag.ContinueOnError)
	component := fs.String("component", "", "server 或 web")
	version := fs.String("version", "", "发布版本，如 1.2.3（可带 v 前缀）")
	minServer := fs.String("min-server", "", "需要的最低后端版本；缺省等于本版本")
	dir := fs.String("dir", "", "产物目录")
	output := fs.String("o", "", "输出路径，缺省 dir/manifest.json")
	fs.Usage = func() { usage(os.Stderr) }
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *component == "" || *version == "" || *dir == "" {
		return fmt.Errorf("manifest 需要 -component、-version 和 -dir")
	}
	if *minServer == "" {
		*minServer = *version
	}
	m, err := release.BuildManifest(*component, *version, *minServer, *dir, nil)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if *output == "" {
		*output = filepath.Join(*dir, "manifest.json")
	}
	if err := os.WriteFile(*output, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("已生成 %s（%d 个产物，min_server %s）\n", *output, len(m.Files), m.MinServer)
	return nil
}

func cmdLatest(args []string) error {
	fs := flag.NewFlagSet("latest", flag.ContinueOnError)
	component := fs.String("component", "", "server 或 web")
	version := fs.String("version", "", "发布版本，如 1.4.0")
	tag := fs.String("tag", "", "发布 tag，如 web/v1.4.0")
	repo := fs.String("repo", "", "GitHub 仓库，OWNER/REPO")
	file := fs.String("file", "", "现有 latest.json 路径（可以不存在）")
	output := fs.String("out", "", "输出路径，缺省写回 -file")
	fs.Usage = func() { usage(os.Stderr) }
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *component == "" || *version == "" || *tag == "" || *repo == "" {
		return fmt.Errorf("latest 需要 -component、-version、-tag 和 -repo")
	}
	if *output == "" {
		if *file == "" {
			return fmt.Errorf("latest 需要 -file 或 -out 之一")
		}
		*output = *file
	}
	var existing []byte
	if *file != "" {
		var err error
		existing, err = os.ReadFile(*file)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("读取 %s：%w", *file, err)
		}
	}
	v, err := release.NormalizeVersion(*version)
	if err != nil {
		return err
	}
	merged, err := release.MergeLatestJSON(existing, *component, release.LatestEntry{
		Version:     v,
		Tag:         *tag,
		ManifestURL: release.ManifestURL(*repo, *tag),
	})
	if err != nil {
		return err
	}
	merged = append(merged, '\n')
	if err := os.WriteFile(*output, merged, 0o644); err != nil {
		return err
	}
	fmt.Printf("已更新 %s（%s %s）\n", *output, *component, v)
	return nil
}

// cmdCheckExe 核对产物能在指定平台运行：构建机是 Ubuntu，漏设 GOOS 时
// taskboard.exe 会是 Linux 二进制（server/v0.1.4），哈希照样对得上。
func cmdCheckExe(args []string) error {
	fs := flag.NewFlagSet("check-exe", flag.ContinueOnError)
	goos := fs.String("goos", "", "目标系统，如 windows、linux")
	goarch := fs.String("goarch", "amd64", "目标架构")
	fs.Usage = func() { usage(os.Stderr) }
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *goos == "" || fs.NArg() == 0 {
		return fmt.Errorf("check-exe 需要 -goos 和至少一个文件")
	}
	for _, path := range fs.Args() {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := release.CheckExecutable(data, *goos, *goarch); err != nil {
			return fmt.Errorf("%s 不是 %s/%s 可执行文件：%w", path, *goos, *goarch, err)
		}
		fmt.Printf("%s：%s/%s 可执行文件\n", path, *goos, *goarch)
	}
	return nil
}
