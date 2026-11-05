package attachments

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrObjectNotFound 表示存储后端里没有这个附件对象。
var ErrObjectNotFound = errors.New("附件对象不存在")

// PutMeta 是写入对象时附带的元数据。本地文件没有地方存 Content-Type，
// 内存假实现和以后的对象存储实现会把它带给对象元数据。
type PutMeta struct {
	ContentType string
}

// DownloadURLOpts 是生成下载外链时需要的响应头信息。
type DownloadURLOpts struct {
	// Disposition 是完整的 Content-Disposition 值（含文件名）。
	Disposition string
}

// Storage 是附件本体的窄接口：对象按附件 id 标识，任一时刻全部附件只在
// 一个后端上（ADR 0018）。Put 信任调用方传入的大小并用它校验读完的内容；
// Open 流式读取并返回对象大小；Delete 幂等，删不存在的对象不算错误。
type Storage interface {
	Put(ctx context.Context, id int64, src io.Reader, size int64, meta PutMeta) error
	Open(ctx context.Context, id int64) (io.ReadCloser, int64, error)
	Delete(ctx context.Context, id int64) error
	// Stat 返回对象大小与是否存在；err 非空表示后端故障（网络、鉴权等），
	// 此时 exists 没有意义，调用方必须把错误往上交，不能当成对象缺失。
	Stat(ctx context.Context, id int64) (size int64, exists bool, err error)
	List(ctx context.Context, fn func(id int64, size int64, modTime time.Time) error) error
	// DownloadURL 返回可直达对象的外链。返回 ok=false 表示这个后端不提供
	// 外链，调用方照旧从 Open 流式返回文件。
	DownloadURL(ctx context.Context, id int64, opts DownloadURLOpts) (url string, ok bool, err error)
}
