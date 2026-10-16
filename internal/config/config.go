// Package config 读取 config.toml。
package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// Config 是 config.toml 的内容。后续工单会追加 [backup]、[update] 等小节。
type Config struct {
	// DataDir 是数据目录：SQLite 库和附件都放在这里。
	DataDir string `toml:"data_dir"`
	// Port 是监听端口，只监听 127.0.0.1。
	Port int `toml:"port"`
	// WebDir 是前端静态文件目录（构建产物）。
	WebDir string `toml:"web_dir"`
	// Attachments 是附件上限（#33；默认值见 ApplyDefaults，
	// 上限以 docs/deploy-and-release.md「附件上限与显示」为准）。
	Attachments Attachments `toml:"attachments"`
}

// Attachments 是 [attachments] 小节：服务端只检查上限，不压缩图片
// （压缩在前端做；spec「附件显示与安全」）。
type Attachments struct {
	// MaxFileMB 是单个文件的上限（MB），超过返回 413 attachment_too_large。
	MaxFileMB int `toml:"max_file_mb"`
	// MaxFilesPerPost 是每条说明或回复的附件个数上限（插图另计）。
	MaxFilesPerPost int `toml:"max_files_per_post"`
	// MaxImagesPerPost 是每条说明或回复的插图张数上限。
	MaxImagesPerPost int `toml:"max_images_per_post"`
	// MaxTicketMB 是整张工单附件合计的上限（MB），待用附件也计入。
	MaxTicketMB int `toml:"max_ticket_mb"`
}

// Load 从 path 读取配置并补默认值。
func Load(path string) (Config, error) {
	var cfg Config
	if _, err := os.Stat(path); err != nil {
		return cfg, fmt.Errorf("读取配置 %s 失败：%w（先用 config.example.toml 复制一份）", path, err)
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return cfg, fmt.Errorf("解析 %s 失败：%w", path, err)
	}
	ApplyDefaults(&cfg)
	return cfg, nil
}

// ApplyDefaults 补上缺省值。附件上限的默认值是 2026-10-16 调整后的新值
// （单文件 5MB、每帖 5 个附件 10 张插图、整单 50MB）。
func ApplyDefaults(cfg *Config) {
	if cfg.DataDir == "" {
		cfg.DataDir = "data"
	}
	if cfg.Port == 0 {
		cfg.Port = 8080
	}
	if cfg.WebDir == "" {
		cfg.WebDir = "web"
	}
	if cfg.Attachments.MaxFileMB == 0 {
		cfg.Attachments.MaxFileMB = 5
	}
	if cfg.Attachments.MaxFilesPerPost == 0 {
		cfg.Attachments.MaxFilesPerPost = 5
	}
	if cfg.Attachments.MaxImagesPerPost == 0 {
		cfg.Attachments.MaxImagesPerPost = 10
	}
	if cfg.Attachments.MaxTicketMB == 0 {
		cfg.Attachments.MaxTicketMB = 50
	}
}
