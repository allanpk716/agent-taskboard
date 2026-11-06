package attachments

import (
	"fmt"
	"os"
	"strings"
)

// StorageConfig 是装配附件后端所需的配置（backend、数据目录和 OSS 连接
// 参数）。它不直接引用 config 包的类型，避免包循环。
type StorageConfig struct {
	// Backend 是 [attachments] backend；空串按 local 处理。
	Backend string
	// DataDir 是 local 后端的数据目录。
	DataDir string
	// OSS 是 backend = "oss" 时的连接参数；local 时也原样保留，供迁移
	// 命令把附件迁往 OSS 使用。
	OSS OSSConfig
}

// NewOSSStorageFromEnv 按连接参数构造 OSS 后端，AccessKey 从
// OSS_ACCESS_KEY_ID / OSS_ACCESS_KEY_SECRET 环境变量读取。
func NewOSSStorageFromEnv(config OSSConfig) (*OSSStorage, error) {
	return NewOSSStorage(config, OSSStorageOptions{
		AccessKeyID:     os.Getenv("OSS_ACCESS_KEY_ID"),
		AccessKeySecret: os.Getenv("OSS_ACCESS_KEY_SECRET"),
	})
}

// MarkerFromConfig 校验 backend 并归一化 OSS bucket/prefix，返回应写库的
// 后端标记。不构造存储、不读环境变量：测试注入假存储时也走这套校验。
func MarkerFromConfig(cfg StorageConfig) (BackendMarker, error) {
	none := BackendMarker{}
	backend := strings.TrimSpace(cfg.Backend)
	if backend == "" {
		backend = AttachmentBackendLocal
	}
	switch backend {
	case AttachmentBackendLocal:
		return BackendMarker{Backend: AttachmentBackendLocal}, nil
	case AttachmentBackendOSS:
		bucket := strings.TrimSpace(cfg.OSS.Bucket)
		prefix, err := NormalizeOSSPrefix(cfg.OSS.Prefix)
		if err != nil {
			return none, err
		}
		if bucket == "" {
			return none, fmt.Errorf("backend = \"oss\" 时必须配置 bucket")
		}
		return BackendMarker{Backend: AttachmentBackendOSS, Bucket: bucket, Prefix: prefix}, nil
	default:
		return none, fmt.Errorf("[attachments] backend = %q 无效（只能是 local 或 oss）", backend)
	}
}

// OpenStorageFromConfig 是按配置装配附件后端的唯一入口：校验 backend、
// 归一化 OSS bucket/prefix，并返回与配置一致的后端标记。这里不探测桶：
// 服务启动时探测，离线命令首次连接时暴露问题（backup-url 甚至可以只签
// 本地地址不连线）。
func OpenStorageFromConfig(cfg StorageConfig) (Storage, ObjectStorage, BackendMarker, error) {
	none := BackendMarker{}
	marker, err := MarkerFromConfig(cfg)
	if err != nil {
		return nil, nil, none, err
	}
	switch marker.Backend {
	case AttachmentBackendLocal:
		storage, err := NewLocalStorage(cfg.DataDir)
		if err != nil {
			return nil, nil, none, err
		}
		return storage, nil, marker, nil
	default:
		storage, err := NewOSSStorageFromEnv(OSSConfig{
			Region: cfg.OSS.Region, Bucket: marker.Bucket, Prefix: marker.Prefix,
			InternalEndpoint: cfg.OSS.InternalEndpoint, SigningHost: cfg.OSS.SigningHost,
		})
		if err != nil {
			return nil, nil, none, err
		}
		objects := ObjectStorageOf(storage)
		if objects == nil {
			return nil, nil, none, fmt.Errorf("OSS 附件后端不支持归档包与备份对象接口")
		}
		return storage, objects, marker, nil
	}
}
