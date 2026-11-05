package attachments

import (
	"bytes"
	"context"
	"fmt"
	"hash/crc64"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MemoryStorage 是测试用的内存假实现：对象与元数据放在进程里，供黑盒测试
// 注入并从外部查看存储里实际有什么。
type MemoryStorage struct {
	mu      sync.Mutex
	objects map[int64]MemoryObject
	named   map[string]MemoryObject

	// PutErr 非空时 Put 直接返回它（测试注入存储故障）。
	PutErr error
	// PutObjectErr 非空时 PutObject 直接返回它（测试注入非附件对象故障）。
	PutObjectErr error
	// DeleteErr 非空时 Delete / DeleteObject 直接返回它（测试注入清理故障）。
	DeleteErr error
	// AfterPut 在对象写入后、Put 返回前调用，用来在「Put 已成功、事务
	// 尚未提交」的时机注入故障。
	AfterPut func(id int64)
	// DownloadURLPrefix 非空时 DownloadURL 返回这个前缀下的可识别地址，
	// 供黑盒测试模拟 OSS 等外链后端。
	DownloadURLPrefix string
}

// MemoryObject 是内存存储里一个对象的元数据和内容。
type MemoryObject struct {
	Size        int64
	ContentType string
	Content     []byte
	ModTime     time.Time
}

// NewMemoryStorage 返回空的内存存储。
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		objects: map[int64]MemoryObject{},
		named:   map[string]MemoryObject{},
	}
}

func (s *MemoryStorage) Put(ctx context.Context, id int64, src io.Reader, size int64, meta PutMeta) error {
	s.mu.Lock()
	fault := s.PutErr
	s.mu.Unlock()
	if fault != nil {
		return fault
	}
	content, err := io.ReadAll(src)
	if err != nil {
		return err
	}
	if int64(len(content)) != size {
		return fmt.Errorf("附件对象大小不符：应为 %d 字节，实际读到 %d", size, len(content))
	}
	s.mu.Lock()
	s.objects[id] = MemoryObject{
		Size:        size,
		ContentType: meta.ContentType,
		Content:     content,
		ModTime:     time.Now(),
	}
	after := s.AfterPut
	s.mu.Unlock()
	if after != nil {
		after(id)
	}
	return nil
}

func (s *MemoryStorage) Open(ctx context.Context, id int64) (io.ReadCloser, int64, error) {
	s.mu.Lock()
	obj, ok := s.objects[id]
	s.mu.Unlock()
	if !ok {
		return nil, 0, ErrObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(obj.Content)), obj.Size, nil
}

func (s *MemoryStorage) Delete(ctx context.Context, id int64) error {
	s.mu.Lock()
	fault := s.DeleteErr
	s.mu.Unlock()
	if fault != nil {
		return fault
	}
	s.mu.Lock()
	delete(s.objects, id)
	s.mu.Unlock()
	return nil
}

func (s *MemoryStorage) PutObject(ctx context.Context, relKey string, src io.Reader, size int64, meta PutMeta) error {
	if err := ValidateObjectRelKey(relKey); err != nil {
		return err
	}
	s.mu.Lock()
	fault := s.PutObjectErr
	s.mu.Unlock()
	if fault != nil {
		return fault
	}
	content, err := io.ReadAll(src)
	if err != nil {
		return err
	}
	if int64(len(content)) != size {
		return fmt.Errorf("对象大小不符：应为 %d 字节，实际读到 %d", size, len(content))
	}
	s.mu.Lock()
	s.named[relKey] = MemoryObject{
		Size: size, ContentType: meta.ContentType, Content: content, ModTime: time.Now(),
	}
	s.mu.Unlock()
	return nil
}

func (s *MemoryStorage) OpenObject(ctx context.Context, relKey string) (io.ReadCloser, int64, error) {
	if err := ValidateObjectRelKey(relKey); err != nil {
		return nil, 0, err
	}
	s.mu.Lock()
	obj, ok := s.named[relKey]
	s.mu.Unlock()
	if !ok {
		return nil, 0, ErrObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(obj.Content)), obj.Size, nil
}

func (s *MemoryStorage) DeleteObject(ctx context.Context, relKey string) error {
	if err := ValidateObjectRelKey(relKey); err != nil {
		return err
	}
	s.mu.Lock()
	fault := s.DeleteErr
	s.mu.Unlock()
	if fault != nil {
		return fault
	}
	s.mu.Lock()
	delete(s.named, relKey)
	s.mu.Unlock()
	return nil
}

func (s *MemoryStorage) ObjectURL(ctx context.Context, relKey string, ttl time.Duration,
	disposition string) (string, bool, error) {
	if err := ValidateObjectRelKey(relKey); err != nil {
		return "", false, err
	}
	if s.DownloadURLPrefix == "" {
		return "", false, nil
	}
	values := url.Values{
		"disposition": []string{disposition},
		"ttl":         []string{strconv.FormatInt(int64(ttl/time.Second), 10)},
	}
	prefix := strings.TrimSuffix(s.DownloadURLPrefix, "/") + "/"
	return prefix + relKey + "?" + values.Encode(), true, nil
}

func (s *MemoryStorage) ListObjects(ctx context.Context, relPrefix string) ([]ObjectInfo, error) {
	if err := ValidateObjectRelPrefix(relPrefix); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.named))
	for key := range s.named {
		if strings.HasPrefix(key, relPrefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	objects := make([]ObjectInfo, 0, len(keys))
	for _, key := range keys {
		obj := s.named[key]
		objects = append(objects, ObjectInfo{RelKey: key, Size: obj.Size, ModTime: obj.ModTime})
	}
	return objects, nil
}

func (s *MemoryStorage) Stat(ctx context.Context, id int64) (int64, bool) {
	s.mu.Lock()
	obj, ok := s.objects[id]
	s.mu.Unlock()
	return obj.Size, ok
}

// SetModTime 供测试把对象调整为指定的存在时间（如 24 小时边界），返回对象
// 是否存在。生产行为不受影响。
func (s *MemoryStorage) SetModTime(id int64, at time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[id]
	if !ok {
		return false
	}
	obj.ModTime = at
	s.objects[id] = obj
	return true
}

func (s *MemoryStorage) List(ctx context.Context, fn func(id int64, size int64, modTime time.Time) error) error {
	s.mu.Lock()
	ids := make([]int64, 0, len(s.objects))
	snapshot := make(map[int64]MemoryObject, len(s.objects))
	for id, obj := range s.objects {
		ids = append(ids, id)
		snapshot[id] = obj
	}
	s.mu.Unlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		obj := snapshot[id]
		if err := fn(id, obj.Size, obj.ModTime); err != nil {
			return err
		}
	}
	return nil
}

func (s *MemoryStorage) DownloadURL(ctx context.Context, id int64, opts DownloadURLOpts) (string, bool, error) {
	if s.DownloadURLPrefix == "" {
		return "", false, nil
	}
	values := url.Values{"disposition": []string{opts.Disposition}}
	return fmt.Sprintf("%sattachments/%d?%s", s.DownloadURLPrefix, id, values.Encode()), true, nil
}

// ObjectChecksum 返回对象的 OSS CRC64（ECMA-182），供迁移测试模拟 OSS 元数据。
func (s *MemoryStorage) ObjectChecksum(ctx context.Context, id int64) (uint64, bool, error) {
	s.mu.Lock()
	obj, ok := s.objects[id]
	s.mu.Unlock()
	if !ok {
		return 0, false, nil
	}
	hash := crc64.New(crc64.MakeTable(crc64.ECMA))
	if _, err := hash.Write(obj.Content); err != nil {
		return 0, true, err
	}
	return hash.Sum64(), true, nil
}

// Object 返回一个对象的副本，供测试查看对象及其元数据。
func (s *MemoryStorage) Object(id int64) (MemoryObject, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[id]
	return obj, ok
}

// ObjectByKey 返回一个非附件对象的副本，供测试查看归档包等对象。
func (s *MemoryStorage) ObjectByKey(relKey string) (MemoryObject, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.named[relKey]
	return obj, ok
}

// IDs 返回全部对象 id（升序），供测试断言存储里留下了什么。
func (s *MemoryStorage) IDs() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]int64, 0, len(s.objects))
	for id := range s.objects {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
