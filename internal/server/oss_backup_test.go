package server_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/guestlinkkey"
	"github.com/allanpk716/agent-taskboard/internal/ops"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// OSS 后端下备份只把库副本和访客链接密钥上传到 backups/，附件镜像仍留在
// 备份目标的 attachments/ 目录；本地后端即使注入实现 ObjectStorage 的假存储
// 也不上传任何对象。
func TestOSS后端备份上传库副本和密钥但不上传附件镜像(t *testing.T) {
	for _, backend := range []string{config.AttachmentBackendOSS, config.AttachmentBackendLocal} {
		t.Run(backend, func(t *testing.T) {
			mem := attachments.NewMemoryStorage()
			s := tbtest.Start(t,
				tbtest.WithAttachmentConfig(config.Attachments{Backend: backend, OSS: config.OSS{
					Bucket: "files", Prefix: "agent-taskboard/test/",
				}}),
				tbtest.WithAttachmentStorage(mem),
				tbtest.WithBackup(config.Backup{
					Target: t.TempDir(), DailyAt: "03:00",
					KeepDaily: 30, KeepMonthly: 12, KeepPreupdate: 5,
				}))
			s.CreateAdmin("陈工", "secret-pass")
			admin := s.Login("陈工", "secret-pass")
			number := mustCreateTicket(t, s, admin, "OSS 备份工单")
			uploaded := mustUploadAttachment(t, s, admin, number, "备份.txt", []byte("oss backup"))

			record, err := s.Backups().BackupNow(context.Background(), ops.KindDaily)
			if err != nil {
				t.Fatalf("备份失败：%v", err)
			}
			id, err := attachments.ParseRef(uploaded.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(s.Backups().TargetDir(), "attachments", strconv.FormatInt(id, 10))); err != nil {
				t.Fatalf("附件镜像应留在备份目标：%v", err)
			}

			dbKey := attachments.BackupRelKey(record.Filename)
			keyKey := attachments.BackupRelKey(guestlinkkey.FileName)
			dbObject, dbExists := mem.ObjectByKey(dbKey)
			_, keyExists := mem.ObjectByKey(keyKey)
			if backend != config.AttachmentBackendOSS {
				if dbExists || keyExists {
					t.Fatalf("本地后端不应上传备份对象：%q/%q", dbKey, keyKey)
				}
				return
			}
			if !dbExists || dbObject.Size != record.Size {
				t.Fatalf("OSS 备份对象 = exists:%v size:%d/%d，想上传完整库副本", dbExists, dbObject.Size, record.Size)
			}
			if !keyExists {
				t.Fatal("OSS 备份对象里缺少访客链接密钥")
			}
			if _, exists := mem.ObjectByKey("backups/attachments/" + strconv.FormatInt(id, 10)); exists {
				t.Fatal("附件镜像不应上传到 OSS")
			}
		})
	}
}

// 镜像同步以数据库附件行为准：镜像缺了当前后端仍有的对象时补回；数据库没有
// 记录的孤儿对象不进入镜像。
func Test镜像缺失的附件按库记录从当前后端补齐(t *testing.T) {
	mem := attachments.NewMemoryStorage()
	target := t.TempDir()
	s := tbtest.Start(t,
		tbtest.WithAttachmentConfig(ossTestAttachmentConfig()),
		tbtest.WithAttachmentStorage(mem),
		tbtest.WithBackup(config.Backup{
			Target: target, DailyAt: "03:00",
			KeepDaily: 30, KeepMonthly: 12, KeepPreupdate: 5,
		}))
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, admin, "补齐镜像工单")
	uploaded := mustUploadAttachment(t, s, admin, number, "镜像.txt", []byte("mirror me"))
	id, err := attachments.ParseRef(uploaded.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store().DB.Exec(`DELETE FROM attachments WHERE id != ?`, id); err != nil {
		t.Fatal(err)
	}
	if err := mem.Put(context.Background(), 999, strings.NewReader("orphan"), 6,
		attachments.PutMeta{ContentType: "text/plain"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Backups().BackupNow(context.Background(), ops.KindDaily); err != nil {
		t.Fatalf("第一次备份失败：%v", err)
	}
	if err := os.Remove(filepath.Join(target, "attachments", strconv.FormatInt(id, 10))); err != nil {
		t.Fatal(err)
	}
	s.Clock().Set(s.Clock().Now().Add(time.Second))

	record, err := s.Backups().BackupNow(context.Background(), ops.KindDaily)
	if err != nil {
		t.Fatalf("第二次备份失败：%v", err)
	}
	if record.FilesAdded != 1 {
		t.Fatalf("第二次备份应补 1 个镜像文件，record=%+v", record)
	}
	got, err := os.ReadFile(filepath.Join(target, "attachments", strconv.FormatInt(id, 10)))
	if err != nil || string(got) != "mirror me" {
		t.Fatalf("镜像未按库记录补回：content=%q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(target, "attachments", "999")); !os.IsNotExist(err) {
		t.Fatalf("数据库无记录的对象不应进入镜像：err=%v", err)
	}
}

// 本地轮换删除副本时同步删除 OSS 同名对象；OSS 删除失败只写日志，不影响备份
// 成功，下一轮会再删。
func Test备份轮换同步删除OSS对象且删除失败只记录日志(t *testing.T) {
	mem := attachments.NewMemoryStorage()
	s := tbtest.Start(t,
		tbtest.WithAttachmentConfig(ossTestAttachmentConfig()),
		tbtest.WithAttachmentStorage(mem),
		tbtest.WithBackup(config.Backup{
			Target: t.TempDir(), DailyAt: "03:00", KeepDaily: 1,
		}))

	first, err := s.Backups().BackupNow(context.Background(), ops.KindDaily)
	if err != nil {
		t.Fatal(err)
	}
	s.Clock().Set(s.Clock().Now().Add(time.Second))
	mem.DeleteErr = errors.New("oss temporarily down")
	second, err := s.Backups().BackupNow(context.Background(), ops.KindDaily)
	if err != nil {
		t.Fatalf("OSS 轮换删除失败不应让备份失败：%v（record=%+v）", err, second)
	}
	if _, exists := mem.ObjectByKey(attachments.BackupRelKey(first.Filename)); !exists {
		t.Fatal("删除失败时旧对象应留给下一轮")
	}

	s.Clock().Set(s.Clock().Now().Add(2 * time.Second))
	mem.DeleteErr = nil
	if _, err := s.Backups().BackupNow(context.Background(), ops.KindDaily); err != nil {
		t.Fatal(err)
	}
	if _, exists := mem.ObjectByKey(attachments.BackupRelKey(first.Filename)); exists {
		t.Fatal("下一轮轮换应删除旧 OSS 对象")
	}
	if _, exists := mem.ObjectByKey(attachments.BackupRelKey(second.Filename)); exists {
		t.Fatal("第二份也应按保留策略一起删除")
	}
}

func ossTestAttachmentConfig() config.Attachments {
	return config.Attachments{Backend: config.AttachmentBackendOSS, OSS: config.OSS{
		Bucket: "files", Prefix: "agent-taskboard/test/",
	}}
}
