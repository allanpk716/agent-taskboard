package server

import (
	"context"
	"fmt"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/store"
)

type attachmentBackendStartup struct {
	backend       string
	storage       attachments.Storage
	objectStorage attachments.ObjectStorage
	commitMarker  func() error
}

func prepareAttachmentBackend(ctx context.Context, cfg config.Attachments, dataDir string,
	st *store.Store, injected attachments.Storage) (attachmentBackendStartup, error) {
	// 后端校验和 OSS 参数归一化统一走 MarkerFromConfig；注入假存储的测试
	// 不构造真实后端，也不需要 AccessKey。
	want, err := attachments.MarkerFromConfig(cfg.StorageConfig(dataDir))
	if err != nil {
		return attachmentBackendStartup{}, err
	}
	backend := want.Backend

	marker, exists, err := attachments.ReadBackendMarker(ctx, st.DB)
	if err != nil {
		return attachmentBackendStartup{}, err
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
	} else {
		constructed, _, _, err := attachments.OpenStorageFromConfig(cfg.StorageConfig(dataDir))
		if err != nil {
			return attachmentBackendStartup{}, err
		}
		storage = constructed
		if backend == attachments.AttachmentBackendOSS {
			if err := constructed.(*attachments.OSSStorage).Probe(ctx); err != nil {
				return attachmentBackendStartup{}, err
			}
		}
	}
	// 只有 OSS 后端提供归档包与备份对象接口；本地后端即使注入了实现
	// ObjectStorage 的假存储也不启用。
	var objectStorage attachments.ObjectStorage
	if backend == attachments.AttachmentBackendOSS {
		objectStorage = attachments.ObjectStorageOf(storage)
		if objectStorage == nil {
			return attachmentBackendStartup{}, fmt.Errorf("OSS 附件后端不支持归档包与备份对象接口")
		}
	}
	return attachmentBackendStartup{
		backend: backend, storage: storage, objectStorage: objectStorage,
		commitMarker: func() error {
			if st.New || marker != want {
				return attachments.WriteBackendMarker(ctx, st.DB, want)
			}
			return nil
		},
	}, nil
}
