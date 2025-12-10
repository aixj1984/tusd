package alistore

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

type AliObjectParams struct {
	// Bucket specifies the Ali bucket that the object resides in.
	Bucket string

	// ID specifies the ID of the Ali object.
	ID string
}

type AliConfig struct {
	Endpoint        string
	AccessKeyId     string
	AccessKeySecret string
	BucketName      string
	RegionId        string
}

type AliComposeParams struct {
	// Bucket specifies the Ali bucket which the composed objects will be stored in.
	Bucket string

	// Sources is a list of the object IDs that are going to be composed.
	Sources []string

	// Destination specifies the desired ID of the composed object.
	Destination string
}

type AliFilterParams struct {
	// Bucket specifies the Ali bucket of which the objects you want to filter reside in.
	Bucket string

	// Prefix specifies the prefix of which you want to filter object names with.
	Prefix string
}

// AliReader implements cloud.google.com/go/storage.Reader.
// It is used to read Google Cloud storage objects.
// TODO: Remain, Size, ContentType seem to only be used in tests. Maybe we can replace this interface with an io.ReadCloser?
type AliReader interface {
	Close() error
	ContentType() string
	Read(p []byte) (int, error)
	Remain() int64
	Size() int64
}

// AliOSSReader 适配器
type AliOSSReader struct {
	reader      io.ReadCloser // 原始 Reader
	contentType string        // 内容类型
	size        int64         // 文件总大小
	remain      int64         // 剩余字节数
}

// Read 方法
func (a *AliOSSReader) Read(p []byte) (int, error) {
	n, err := a.reader.Read(p)
	a.remain -= int64(n) // 更新剩余字节数
	return n, err
}

// Close 方法
func (a *AliOSSReader) Close() error {
	return a.reader.Close()
}

// ContentType 方法
func (a *AliOSSReader) ContentType() string {
	return a.contentType
}

// Remain 方法
func (a *AliOSSReader) Remain() int64 {
	return a.remain
}

// Size 方法
func (a *AliOSSReader) Size() int64 {
	return a.size
}

// AliAPI is an interface composed of all the necessary Ali
// operations that are required to enable the tus protocol
// to work with Google's cloud storage.
type AliAPI interface {
	ReadObject(ctx context.Context, params AliObjectParams) (AliReader, error)
	GetObject(ctx context.Context, params AliObjectParams, headers *http.Header) (http.Header, io.ReadCloser, error)
	GetObjectSize(ctx context.Context, params AliObjectParams) (int64, error)
	SetObjectMetadata(ctx context.Context, params AliObjectParams, metadata map[string]string) error
	DeleteObject(ctx context.Context, params AliObjectParams) error
	WriteObject(ctx context.Context, params AliObjectParams, r io.Reader, pos int64) (int64, error)
	PutObject(ctx context.Context, params AliObjectParams, r io.Reader) error
	GetObjectPos(ctx context.Context, params AliObjectParams) (int64, error)
}

// AliService holds the cloud.google.com/go/storage client
// as well as its associated context.
// Closures are used as minimal wrappers around the Google Cloud Storage API, since the Storage API cannot be mocked.
// The usage of these closures allow them to be redefined in the testing package, allowing test to be run against this file.
type AliService struct {
	Client *oss.Bucket
}

// NewAliService returns a AliService object given a GCloud service account file path.
func NewAliService(config *AliConfig) (*AliService, error) {
	// New client
	client, err := oss.New(config.Endpoint, config.AccessKeyId, config.AccessKeySecret)
	if err != nil {
		return nil, err
	}

	// Get bucket
	bucket, err := client.Bucket(config.BucketName)
	if err != nil {
		slog.Debug(err.Error())
		return nil, err
	}

	service := &AliService{
		Client: bucket,
	}

	return service, nil
}

// GetObjectSize returns the byte length of the specified Ali object.
func (service *AliService) GetObjectSize(ctx context.Context, params AliObjectParams) (int64, error) {
	// 获取对象的元信息
	headers, err := service.Client.GetObjectMeta(params.ID)
	if err != nil {
		slog.Debug(err.Error())
		return 0, err
	}

	// 获取 Content-Length（文件大小）
	contentLengthStr := headers.Get("Content-Length")

	contentLength, err := strconv.ParseInt(contentLengthStr, 10, 64)
	if err != nil {
		slog.Debug(err.Error())
		return 0, err
	}

	return contentLength, nil
}

// ReadObject reads a AliObjectParams, returning a AliReader object if successful, and an error otherwise
func (service *AliService) ReadObject(ctx context.Context, params AliObjectParams) (AliReader, error) {
	// 获取对象元数据
	objectMeta, err := service.Client.GetObjectDetailedMeta(params.ID)
	if err != nil {
		slog.Debug(err.Error() + params.ID)
		return nil, err
	}

	// 获取文件大小
	sizeStr := objectMeta.Get("Content-Length")
	totalSize, err := strconv.ParseInt(sizeStr, 10, 64) // 更安全的转换
	if err != nil {
		slog.Debug(sizeStr)
		slog.Debug(err.Error())
		return nil, err
	}

	// 获取内容类型
	contentType := objectMeta.Get("Content-Type")

	// 创建 HTTP Header
	headers := http.Header{}
	headers.Set("Content-Type", contentType) // 可能不需要设置，因为 OSS 自动返回

	// 获取对象
	reader, err := service.Client.GetObject(params.ID, oss.GetResponseHeader(&headers))
	if err != nil {
		slog.Debug(err.Error())
		return nil, err
	}

	return &AliOSSReader{
		reader:      reader,
		contentType: contentType,
		size:        totalSize,
		remain:      totalSize,
	}, nil
}

// ParseHTTPRangeHeader 解析HTTP Range头部，返回阿里云OSS NormalizedRange需要的格式
func ParseHTTPRangeHeader(rangeHeader string) (string, error) {
	if rangeHeader == "" {
		return "", nil
	}

	// 去除空格
	rangeHeader = strings.TrimSpace(rangeHeader)

	// 检查是否是bytes=开头
	if !strings.HasPrefix(rangeHeader, "bytes=") {
		// 如果不是标准格式，直接返回空
		return "", nil
	}

	// 去掉"bytes="前缀
	rangeValue := strings.TrimSpace(rangeHeader[6:])

	// 直接返回剩余的字符串，让SDK处理
	// 如: "bytes=0-100" -> "0-100"
	//     "bytes=100-"  -> "100-"
	//     "bytes=-500"  -> "-500"
	return rangeValue, nil
}

// ReadObject reads a AliObjectParams, returning a AliReader object if successful, and an error otherwise
func (service *AliService) GetObject(ctx context.Context, params AliObjectParams, reqHeaders *http.Header) (http.Header, io.ReadCloser, error) {
	// 获取对象

	// 执行请求
	// 注意：阿里云 OSS Go SDK 的常用接口是 bucket.GetObject，它接受多个 Option
	// 根据文档，范围下载使用 oss.Range(start, end) 作为参数[citation:5]
	// 条件参数如 oss.IfModifiedSince(t) 等
	// 因此，一个更贴近 SDK 风格的实现可能是：
	var options []oss.Option

	// 处理 Range
	if val := reqHeaders.Get("Range"); val != "" {
		// 调用一个解析函数，获取 start 和 end
		normalizedRange, err := ParseHTTPRangeHeader(val)
		if err == nil {
			options = append(options, oss.NormalizedRange(normalizedRange))
		}
	}

	// 处理其他条件头
	if val := reqHeaders.Get("If-Match"); val != "" {
		options = append(options, oss.IfMatch(val))
	}
	if val := reqHeaders.Get("If-None-Match"); val != "" {
		options = append(options, oss.IfNoneMatch(val))
	}
	if val := reqHeaders.Get("If-Modified-Since"); val != "" {
		t, err := http.ParseTime(val)
		if err == nil {
			options = append(options, oss.IfModifiedSince(t))
		}
	}
	if val := reqHeaders.Get("If-Unmodified-Since"); val != "" {
		t, err := http.ParseTime(val)
		if err == nil {
			options = append(options, oss.IfUnmodifiedSince(t))
		}
	}

	// **关键：设置标准范围行为**
	// 根据阿里云文档，要获得符合预期的范围请求行为（例如超出范围返回416），
	// 需要设置请求头 x-oss-range-behavior: standard[citation:1][citation:2][citation:3]
	options = append(options, oss.RangeBehavior("standard"))

	request := &oss.GetObjectRequest{ObjectKey: params.ID}

	result, err := service.Client.DoGetObject(request, options)
	if err != nil {
		slog.Debug(err.Error())
		return nil, nil, err
	}

	return result.Response.Headers, result.Response.Body, nil
}

// SetObjectMetadata reads a AliObjectParams and a map of metadata, returning a nil on success and an error otherwise
func (service *AliService) SetObjectMetadata(ctx context.Context, params AliObjectParams, metadata map[string]string) error {
	options := []oss.Option{}
	for k, v := range metadata {
		options = append(options, oss.Meta(k, v))
	}

	return service.Client.SetObjectMeta(params.ID, options...)
}

// PutObject reads  to file
func (service *AliService) PutObject(ctx context.Context, params AliObjectParams, r io.Reader) error {
	return service.Client.PutObject(params.ID, r)
}

// DeleteObject deletes the object defined by AliObjectParams
func (service *AliService) DeleteObject(ctx context.Context, params AliObjectParams) error {
	return service.Client.DeleteObject(params.ID)
}

// Write object writes the file set out by the AliObjectParams
func (service *AliService) WriteObject(ctx context.Context, params AliObjectParams, r io.Reader, pos int64) (int64, error) {
	props, err := service.Client.GetObjectDetailedMeta(params.ID)
	nextPos := int64(0)
	if err != nil {
		ossErr, ok := err.(oss.ServiceError)
		if !ok || ossErr.Code != "NoSuchKey" {
			slog.Debug(err.Error())
			return 0, err
		}
	} else {
		nextPos, err = strconv.ParseInt(props.Get(oss.HTTPHeaderOssNextAppendPosition), 10, 64)
		if err != nil {
			slog.Debug(props.Get(oss.HTTPHeaderOssNextAppendPosition))
			slog.Debug(err.Error())
			return 0, err
		}
	}

	if nextPos != pos {
		slog.Debug(fmt.Sprintf("id:%s , pos is not match, inputis %d ,  cloud is %d", params.ID, pos, nextPos))
	}

	return service.Client.AppendObject(params.ID, r, pos)
}

// GetObjectPos : the next append position by GetObjectDetailedMeta
func (service *AliService) GetObjectPos(ctx context.Context, params AliObjectParams) (int64, error) {
	props, err := service.Client.GetObjectDetailedMeta(params.ID)
	if err != nil {
		slog.Debug(err.Error())
		return 0, err
	}

	nextPos, err := strconv.ParseInt(props.Get(oss.HTTPHeaderOssNextAppendPosition), 10, 64)
	if err != nil {
		slog.Debug(err.Error())
		return 0, err
	}

	return nextPos, nil
}
