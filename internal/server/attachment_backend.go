package server

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/store"
)

type attachmentBackendStartup struct {
	storage      attachments.Storage
	commitMarker func() error
}

func prepareAttachmentBackend(ctx context.Context, cfg config.Attachments, dataDir string,
	st *store.Store, injected attachments.Storage) (attachmentBackendStartup, error) {
	backend := strings.TrimSpace(cfg.Backend)
	if backend == "" {
		backend = config.AttachmentBackendLocal
	}
	if backend != config.AttachmentBackendLocal && backend != config.AttachmentBackendOSS {
		return attachmentBackendStartup{}, fmt.Errorf("[attachments] backend = %q 无效（只能是 local 或 oss）", backend)
	}

	marker, exists, err := attachments.ReadBackendMarker(ctx, st.DB)
	if err != nil {
		return attachmentBackendStartup{}, err
	}
	want := attachments.BackendMarker{Backend: backend}
	if backend == config.AttachmentBackendOSS {
		want.Bucket = strings.TrimSpace(cfg.OSS.Bucket)
		want.Prefix, err = attachments.NormalizeOSSPrefix(cfg.OSS.Prefix)
		if err != nil {
			return attachmentBackendStartup{}, err
		}
	}
	if !exists {
		if !st.New {
			return attachmentBackendStartup{}, fmt.Errorf("数据库缺少 attachments_backend 标记：先用当前版本完成数据库迁移，或恢复完整备份后再启动")
		}
	} else if marker != want && !st.New {
		if marker.Backend != want.Backend {
			command := "taskboard migrate-attachments -to " + want.Backend
			return attachmentBackendStartup{}, fmt.Errorf(
				"数据库记录的附件后端是 %s，配置是 %s：先停服运行 %s，完成后再改配置并启动",
				marker.Backend, want.Backend, command)
		}
		return attachmentBackendStartup{}, fmt.Errorf(
			"数据库记录的 OSS 附件后端是 bucket=%s prefix=%s，配置是 bucket=%s prefix=%s：先停服运行 taskboard migrate-attachments -to local 迁回后再调整",
			marker.Bucket, marker.Prefix, want.Bucket, want.Prefix)
	}

	var storage attachments.Storage
	if injected != nil {
		storage = injected
	} else if backend == config.AttachmentBackendOSS {
		accessKeyID := os.Getenv("OSS_ACCESS_KEY_ID")
		accessKeySecret := os.Getenv("OSS_ACCESS_KEY_SECRET")
		ossStorage, err := attachments.NewOSSStorage(attachments.OSSConfig{
			Region: cfg.OSS.Region, Bucket: cfg.OSS.Bucket, Prefix: want.Prefix,
			InternalEndpoint: cfg.OSS.InternalEndpoint, SigningHost: cfg.OSS.SigningHost,
		}, attachments.OSSStorageOptions{AccessKeyID: accessKeyID, AccessKeySecret: accessKeySecret})
		if err != nil {
			return attachmentBackendStartup{}, err
		}
		if err := ossStorage.Probe(ctx); err != nil {
			return attachmentBackendStartup{}, err
		}
		storage = ossStorage
	} else {
		local, err := attachments.NewLocalStorage(dataDir)
		if err != nil {
			return attachmentBackendStartup{}, err
		}
		storage = local
	}
	return attachmentBackendStartup{
		storage: storage,
		commitMarker: func() error {
			if st.New || marker != want {
				return attachments.WriteBackendMarker(ctx, st.DB, want)
			}
			return nil
		},
	}, nil
}
