package attachments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	oss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	osssigner "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/signer"
)

const (
	AttachmentBackendLocal = "local"
	AttachmentBackendOSS   = "oss"
	ossAttachmentDirectory = "attachments"
)

// OSSConfig 是服务端访问 OSS 所需的连接参数。凭证刻意不在这里出现。
type OSSConfig struct {
	Region           string
	Bucket           string
	Prefix           string
	InternalEndpoint string
	SigningHost      string
}

// OSSStorageOptions 是创建 OSS 后端的装配参数；Now 和 HTTPClient 主要供
// 协议级测试注入，生产使用真实时间和默认 HTTP 客户端。
type OSSStorageOptions struct {
	AccessKeyID     string
	AccessKeySecret string
	Now             func() time.Time
	HTTPClient      *http.Client
}

// OSSStorage 是阿里云 OSS 附件后端。服务端 API 走 internal_endpoint，
// 签名地址按桶默认外网域名或 signing_host 生成。
type OSSStorage struct {
	config     OSSConfig
	prefix     string
	client     *oss.Client
	creds      credentials.Credentials
	now        func() time.Time
	endpoint   *url.URL
	httpClient *http.Client

	persistentHeaders bool
}

// NewOSSStorage 创建 OSS 后端。根前缀为空、连接参数缺失或 AK 不完整都会失败。
func NewOSSStorage(config OSSConfig, options OSSStorageOptions) (*OSSStorage, error) {
	prefix, err := NormalizeOSSPrefix(config.Prefix)
	if err != nil {
		return nil, err
	}
	config.Prefix = prefix
	if config.Region == "" || config.Bucket == "" || config.InternalEndpoint == "" {
		return nil, errors.New("backend = \"oss\" 时必须配置 region、bucket 和 internal_endpoint")
	}
	if options.AccessKeyID == "" || options.AccessKeySecret == "" {
		return nil, errors.New("缺少 OSS AccessKey：请设置 OSS_ACCESS_KEY_ID 和 OSS_ACCESS_KEY_SECRET 环境变量")
	}
	if _, err := signingHostURL(config.SigningHost, config.Region, config.Bucket); err != nil {
		return nil, err
	}
	endpoint, err := endpointURL(config.InternalEndpoint)
	if err != nil {
		return nil, err
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	creds := credentials.Credentials{
		AccessKeyID: options.AccessKeyID, AccessKeySecret: options.AccessKeySecret,
	}
	sdkConfig := oss.LoadDefaultConfig().
		WithRegion(config.Region).
		WithEndpoint(config.InternalEndpoint).
		WithSignatureVersion(oss.SignatureVersionV4).
		WithCredentialsProvider(credentials.NewStaticCredentialsProvider(creds.AccessKeyID, creds.AccessKeySecret))
	if options.HTTPClient != nil {
		sdkConfig = sdkConfig.WithHttpClient(options.HTTPClient)
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &OSSStorage{
		config: config, prefix: config.Prefix, client: oss.NewClient(sdkConfig),
		creds: creds, now: now, endpoint: endpoint, httpClient: httpClient,
		persistentHeaders: true,
	}, nil
}

func endpointURL(raw string) (*url.URL, error) {
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, fmt.Errorf("internal_endpoint %q 必须是 OSS 域名", raw)
	}
	parsed.Path = ""
	return parsed, nil
}

// NormalizeOSSPrefix 规范化系统根前缀，并拒绝空值、路径穿越和根路径。
func NormalizeOSSPrefix(raw string) (string, error) {
	prefix := strings.TrimSpace(raw)
	if prefix == "" {
		return "", errors.New("OSS 根前缀不能为空：所有对象都必须放在本系统的前缀下")
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	for _, part := range strings.Split(strings.Trim(prefix, "/"), "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("OSS 根前缀 %q 不是安全的对象键前缀", prefix)
		}
	}
	return prefix, nil
}

func signingHostURL(host, region, bucket string) (*url.URL, error) {
	if strings.Contains(host, "://") {
		parsed, err := url.Parse(host)
		if err != nil || parsed.Host == "" || parsed.Path != "" {
			return nil, fmt.Errorf("signing_host %q 必须是可访问的域名", host)
		}
		return parsed, nil
	}
	if host == "" {
		if strings.HasPrefix(region, "oss-") {
			host = bucket + "." + region + ".aliyuncs.com"
		} else {
			host = bucket + ".oss-" + region + ".aliyuncs.com"
		}
	}
	parsed, err := url.Parse("https://" + host)
	if err != nil || parsed.Host == "" || parsed.Path != "" {
		return nil, fmt.Errorf("signing_host %q 必须是可访问的域名", host)
	}
	return parsed, nil
}

func (s *OSSStorage) attachmentKey(id int64) string {
	return s.prefix + ossAttachmentDirectory + "/" + strconv.FormatInt(id, 10)
}

func (s *OSSStorage) objectKey(relKey string) (string, error) {
	if err := ValidateObjectRelKey(relKey); err != nil {
		return "", err
	}
	return s.prefix + relKey, nil
}

// Probe 启动时探测桶：确认桶可访问，然后在根前缀下写入并删除一个探测对象。
func (s *OSSStorage) Probe(ctx context.Context) error {
	if err := s.headBucket(ctx); err != nil {
		return fmt.Errorf("探测 OSS 桶 %s 失败：%w", s.config.Bucket, err)
	}
	key := s.prefix + ".taskboard-startup-probe"
	err := s.putKey(ctx, key, strings.NewReader("probe"), int64(len("probe")), "text/plain", s.persistentHeaders)
	if err != nil {
		var serviceErr *oss.ServiceError
		if errors.As(err, &serviceErr) && serviceErr.StatusCode == http.StatusBadRequest {
			err = s.putKey(ctx, key, strings.NewReader("probe"), int64(len("probe")), "text/plain", false)
			if err == nil {
				s.persistentHeaders = false
			}
		}
		if err != nil {
			return fmt.Errorf("探测 OSS 根前缀 %s 写入失败：%w", s.prefix, err)
		}
	}
	if err := s.DeleteKey(ctx, key); err != nil {
		return fmt.Errorf("探测 OSS 根前缀 %s 删除失败：%w", s.prefix, err)
	}
	return nil
}

func (s *OSSStorage) headBucket(ctx context.Context) error {
	target := *s.endpoint
	if net.ParseIP(target.Hostname()) == nil {
		target.Host = s.config.Bucket + "." + target.Host
		target.Path = "/"
	} else {
		target.Path = "/" + s.config.Bucket + "/"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, target.String(), nil)
	if err != nil {
		return err
	}
	product, region, bucket := "oss", s.config.Region, s.config.Bucket
	credentialsValue := s.creds
	signingContext := &osssigner.SigningContext{
		Product: &product, Region: &region, Bucket: &bucket,
		Request: request, Credentials: &credentialsValue,
	}
	signer := osssigner.SignerV4{}
	if err := signer.Sign(ctx, signingContext); err != nil {
		return err
	}
	response, err := s.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("HeadBucket 返回 HTTP %d", response.StatusCode)
	}
	return nil
}

type exactSizeReader struct {
	src    io.Reader
	remain int64
}

func (r *exactSizeReader) Read(p []byte) (int, error) {
	if r.remain == 0 {
		if len(p) == 0 {
			return 0, io.EOF
		}
		p = p[:1]
		n, err := r.src.Read(p)
		if n > 0 {
			return 0, errors.New("附件内容比声明的大小更长")
		}
		if err == nil {
			return 0, io.EOF
		}
		return 0, err
	}
	if int64(len(p)) > r.remain {
		p = p[:r.remain]
	}
	n, err := r.src.Read(p)
	r.remain -= int64(n)
	return n, err
}

func (s *OSSStorage) putKey(ctx context.Context, key string, src io.Reader, size int64,
	contentType string, persistentHeaders bool) error {
	request := &oss.PutObjectRequest{
		Bucket: oss.Ptr(s.config.Bucket), Key: oss.Ptr(key),
		ContentLength: oss.Ptr(size), Body: &exactSizeReader{src: src, remain: size},
	}
	if contentType != "" {
		request.ContentType = oss.Ptr(contentType)
	}
	if persistentHeaders {
		value := base64.StdEncoding.EncodeToString([]byte("nosniff"))
		request.Headers = map[string]string{
			"X-Oss-Persistent-Headers": "X-Content-Type-Options:" + value,
		}
	}
	_, err := s.client.PutObject(ctx, request)
	return err
}

func (s *OSSStorage) Put(ctx context.Context, id int64, src io.Reader, size int64, meta PutMeta) error {
	return s.putKey(ctx, s.attachmentKey(id), src, size, meta.ContentType, s.persistentHeaders)
}

func objectNotFound(err error) bool {
	var serviceErr *oss.ServiceError
	return errors.As(err, &serviceErr) && serviceErr.StatusCode == http.StatusNotFound
}

func (s *OSSStorage) Open(ctx context.Context, id int64) (io.ReadCloser, int64, error) {
	result, err := s.client.GetObject(ctx, &oss.GetObjectRequest{
		Bucket: oss.Ptr(s.config.Bucket), Key: oss.Ptr(s.attachmentKey(id)),
	})
	if objectNotFound(err) {
		return nil, 0, ErrObjectNotFound
	}
	if err != nil {
		return nil, 0, fmt.Errorf("读取 OSS 附件失败：%w", err)
	}
	size := result.ContentLength
	if size < 0 {
		size = 0
	}
	return result.Body, size, nil
}

func (s *OSSStorage) keyInsidePrefix(key string) bool {
	if !strings.HasPrefix(key, s.prefix) {
		return false
	}
	for _, part := range strings.Split(strings.TrimPrefix(key, s.prefix), "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}

// DeleteKey 删除任意系统对象键。所有非附件删除都经过这里的根前缀护栏。
func (s *OSSStorage) DeleteKey(ctx context.Context, key string) error {
	if !s.keyInsidePrefix(key) {
		log.Printf("拒绝删除 OSS 根前缀 %q 外的对象 %q", s.prefix, key)
		return fmt.Errorf("拒绝删除 OSS 根前缀 %q 外的对象 %q", s.prefix, key)
	}
	_, err := s.client.DeleteObject(ctx, &oss.DeleteObjectRequest{
		Bucket: oss.Ptr(s.config.Bucket), Key: oss.Ptr(key),
	})
	if objectNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("删除 OSS 对象 %s 失败：%w", key, err)
	}
	return nil
}

func (s *OSSStorage) Delete(ctx context.Context, id int64) error {
	return s.DeleteKey(ctx, s.attachmentKey(id))
}

func (s *OSSStorage) PutObject(ctx context.Context, relKey string, src io.Reader, size int64,
	meta PutMeta) error {
	key, err := s.objectKey(relKey)
	if err != nil {
		return err
	}
	return s.putKey(ctx, key, src, size, meta.ContentType, s.persistentHeaders)
}

func (s *OSSStorage) OpenObject(ctx context.Context, relKey string) (io.ReadCloser, int64, error) {
	key, err := s.objectKey(relKey)
	if err != nil {
		return nil, 0, err
	}
	result, err := s.client.GetObject(ctx, &oss.GetObjectRequest{
		Bucket: oss.Ptr(s.config.Bucket), Key: oss.Ptr(key),
	})
	if objectNotFound(err) {
		return nil, 0, ErrObjectNotFound
	}
	if err != nil {
		return nil, 0, fmt.Errorf("读取 OSS 对象 %s 失败：%w", key, err)
	}
	size := result.ContentLength
	if size < 0 {
		size = 0
	}
	return result.Body, size, nil
}

func (s *OSSStorage) DeleteObject(ctx context.Context, relKey string) error {
	key, err := s.objectKey(relKey)
	if err != nil {
		return err
	}
	return s.DeleteKey(ctx, key)
}

func (s *OSSStorage) Stat(ctx context.Context, id int64) (int64, bool) {
	result, err := s.client.HeadObject(ctx, &oss.HeadObjectRequest{
		Bucket: oss.Ptr(s.config.Bucket), Key: oss.Ptr(s.attachmentKey(id)),
	})
	if err != nil {
		return 0, false
	}
	size := result.ContentLength
	if size < 0 {
		size = 0
	}
	return size, true
}

func (s *OSSStorage) List(ctx context.Context, fn func(id int64, size int64, modTime time.Time) error) error {
	prefix := s.prefix + ossAttachmentDirectory + "/"
	var token *string
	for {
		request := &oss.ListObjectsV2Request{
			Bucket: oss.Ptr(s.config.Bucket), Prefix: oss.Ptr(prefix), MaxKeys: 1000,
		}
		if token != nil {
			request.ContinuationToken = token
		}
		result, err := s.client.ListObjectsV2(ctx, request)
		if err != nil {
			return fmt.Errorf("列举 OSS 附件失败：%w", err)
		}
		for _, object := range result.Contents {
			if object.Key == nil {
				continue
			}
			rawID := strings.TrimPrefix(*object.Key, prefix)
			id, err := strconv.ParseInt(rawID, 10, 64)
			if err != nil || strconv.FormatInt(id, 10) != rawID {
				continue
			}
			var modified time.Time
			if object.LastModified != nil {
				modified = *object.LastModified
			}
			if err := fn(id, object.Size, modified); err != nil {
				return err
			}
		}
		if !result.IsTruncated || result.NextContinuationToken == nil {
			return nil
		}
		token = result.NextContinuationToken
	}
}

func (s *OSSStorage) DownloadURL(ctx context.Context, id int64, opts DownloadURLOpts) (string, bool, error) {
	key, err := s.objectKey(ossAttachmentDirectory + "/" + strconv.FormatInt(id, 10))
	if err != nil {
		return "", false, err
	}
	return s.signedObjectURL(ctx, key, 15*time.Minute, opts.Disposition)
}

func (s *OSSStorage) ObjectURL(ctx context.Context, relKey string, ttl time.Duration,
	disposition string) (string, bool, error) {
	key, err := s.objectKey(relKey)
	if err != nil {
		return "", false, err
	}
	return s.signedObjectURL(ctx, key, ttl, disposition)
}

func (s *OSSStorage) signedObjectURL(ctx context.Context, key string, ttl time.Duration,
	disposition string) (string, bool, error) {
	if ttl <= 0 {
		return "", false, errors.New("对象签名地址有效期必须大于 0")
	}
	now := s.now().UTC().Truncate(10 * time.Minute)
	host, err := signingHostURL(s.config.SigningHost, s.config.Region, s.config.Bucket)
	if err != nil {
		return "", false, err
	}
	datetime := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	scope := date + "/" + s.config.Region + "/oss/aliyun_v4_request"
	query := url.Values{
		"x-oss-signature-version":      []string{"OSS4-HMAC-SHA256"},
		"x-oss-date":                   []string{datetime},
		"x-oss-expires":                []string{strconv.FormatInt(int64(ttl/time.Second), 10)},
		"x-oss-credential":             []string{s.creds.AccessKeyID + "/" + scope},
		"response-cache-control":       []string{"private, max-age=600"},
		"response-content-disposition": []string{disposition},
	}
	canonicalQuery := canonicalValues(query)
	canonicalURI := escapeObjectKey("/" + s.config.Bucket + "/" + key)
	canonicalRequest := strings.Join([]string{
		http.MethodGet, canonicalURI, canonicalQuery, "", "", "UNSIGNED-PAYLOAD",
	}, "\n")
	stringToSign := strings.Join([]string{
		"OSS4-HMAC-SHA256", datetime, scope, hex.EncodeToString(sha256Sum([]byte(canonicalRequest))),
	}, "\n")
	signingKey := "aliyun_v4" + s.creds.AccessKeySecret
	for _, part := range []string{date, s.config.Region, "oss", "aliyun_v4_request"} {
		mac := hmac.New(sha256.New, []byte(signingKey))
		mac.Write([]byte(part))
		signingKey = string(mac.Sum(nil))
	}
	mac := hmac.New(sha256.New, []byte(signingKey))
	mac.Write([]byte(stringToSign))
	query.Set("x-oss-signature", hex.EncodeToString(mac.Sum(nil)))

	target := *host
	target.Path = "/" + key
	target.RawQuery = strings.ReplaceAll(query.Encode(), "+", "%20")
	return target.String(), true, nil
}

func sha256Sum(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

func canonicalValues(values url.Values) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		part := ossQueryEscape(key)
		if value := ossQueryEscape(values.Get(key)); value != "" {
			part += "=" + value
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "&")
}

func ossQueryEscape(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

func escapeObjectKey(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		segments[i] = ossQueryEscape(segment)
	}
	return strings.Join(segments, "/")
}
