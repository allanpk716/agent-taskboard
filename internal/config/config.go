// Package config 读取 config.toml。
package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// Config 是 config.toml 的内容。后续工单会追加 [attachments]、[backup]、[update] 等小节。
type Config struct {
	// DataDir 是数据目录：SQLite 库和附件都放在这里。
	DataDir string `toml:"data_dir"`
	// Port 是监听端口，只监听 127.0.0.1。
	Port int `toml:"port"`
	// WebDir 是前端静态文件目录（构建产物）。
	WebDir string `toml:"web_dir"`
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

// ApplyDefaults 补上缺省值。
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
}
