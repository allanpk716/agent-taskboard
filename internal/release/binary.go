package release

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"fmt"
)

// CheckExecutable 确认 data 是能在 goos/goarch 上运行的可执行文件。更新器在
// 替换 exe 前调用：SHA256 只能证明“下载的就是发布的那个文件”，证明不了它能
// 在本机运行。2026-10-17 的 server/v0.1.4 在 Ubuntu 上没设 GOOS 就构建了
// taskboard.exe，哈希全对，换上去后服务起不来（#46 之后）。
// 不认识的 goos 不检查。
func CheckExecutable(data []byte, goos, goarch string) error {
	r := bytes.NewReader(data)
	switch goos {
	case "windows":
		f, err := pe.NewFile(r)
		if err != nil {
			return fmt.Errorf("不是 Windows 可执行文件（PE）：%w", err)
		}
		defer f.Close()
		if want, ok := map[string]uint16{
			"amd64": pe.IMAGE_FILE_MACHINE_AMD64,
			"386":   pe.IMAGE_FILE_MACHINE_I386,
			"arm64": pe.IMAGE_FILE_MACHINE_ARM64,
		}[goarch]; ok && f.Machine != want {
			return fmt.Errorf("Windows 可执行文件的架构（0x%x）与本机 %s 不符", f.Machine, goarch)
		}
	case "linux":
		f, err := elf.NewFile(r)
		if err != nil {
			return fmt.Errorf("不是 Linux 可执行文件（ELF）：%w", err)
		}
		defer f.Close()
		if want, ok := map[string]elf.Machine{
			"amd64": elf.EM_X86_64,
			"386":   elf.EM_386,
			"arm64": elf.EM_AARCH64,
		}[goarch]; ok && f.Machine != want {
			return fmt.Errorf("Linux 可执行文件的架构（%s）与本机 %s 不符", f.Machine, goarch)
		}
	case "darwin":
		f, err := macho.NewFile(r)
		if err != nil {
			return fmt.Errorf("不是 macOS 可执行文件（Mach-O）：%w", err)
		}
		defer f.Close()
		if want, ok := map[string]macho.Cpu{
			"amd64": macho.CpuAmd64,
			"arm64": macho.CpuArm64,
		}[goarch]; ok && f.Cpu != want {
			return fmt.Errorf("macOS 可执行文件的架构（%s）与本机 %s 不符", f.Cpu, goarch)
		}
	}
	return nil
}
