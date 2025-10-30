package txstore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"github.com/tencentyun/cos-go-sdk-v5"
	"github.com/tencentyun/cos-go-sdk-v5/debug"
)

type TxObjectParams struct {
	// Bucket specifies the Tx bucket that the object resides in.
	Bucket string

	// ID specifies the ID of the Tx object.
	ID string
}

type TxConfig struct {
	Endpoint        string
	AccessKeyId     string
	AccessKeySecret string
	BucketName      string
	RegionId        string
}

type TxComposeParams struct {
	// Bucket specifies the Tx bucket which the composed objects will be stored in.
	Bucket string

	// Sources is a list of the object IDs that are going to be composed.
	Sources []string

	// Destination specifies the desired ID of the composed object.
	Destination string
}

type TxFilterParams struct {
	// Bucket specifies the Tx bucket of which the objects you want to filter reside in.
	Bucket string

	// Prefix specifies the prefix of which you want to filter object names with.
	Prefix string
}

// TxReader implements cloud.google.com/go/storage.Reader.
// It is used to read Google Cloud storage objects.
// TODO: Remain, Size, ContentType seem to only be used in tests. Maybe we can replace this interface with an io.ReadCloser?
type TxReader interface {
	Close() error
	ContentType() string
	Read(p []byte) (int, error)
	Remain() int64
	Size() int64
}

// TxOSSReader 适配器
type TxOSSReader struct {
	reader      io.ReadCloser // 原始 Reader
	contentType string        // 内容类型
	size        int64         // 文件总大小
	remain      int64         // 剩余字节数
}

// Read 方法
func (a *TxOSSReader) Read(p []byte) (int, error) {
	n, err := a.reader.Read(p)
	a.remain -= int64(n) // 更新剩余字节数
	return n, err
}

// Close 方法
func (a *TxOSSReader) Close() error {
	return a.reader.Close()
}

// ContentType 方法
func (a *TxOSSReader) ContentType() string {
	return a.contentType
}

// Remain 方法
func (a *TxOSSReader) Remain() int64 {
	return a.remain
}

// Size 方法
func (a *TxOSSReader) Size() int64 {
	return a.size
}

var NextAppendPosHeader = "x-cos-next-append-position"

// TxAPI is an interface composed of all the necessary Tx
// operations that are required to enable the tus protocol
// to work with Google's cloud storage.
type TxAPI interface {
	ReadObject(ctx context.Context, params TxObjectParams) (TxReader, error)
	GetObjectSize(ctx context.Context, params TxObjectParams) (int64, error)
	SetObjectMetadata(ctx context.Context, params TxObjectParams, metadata map[string]string) error
	DeleteObject(ctx context.Context, params TxObjectParams) error
	WriteObject(ctx context.Context, params TxObjectParams, r io.Reader, pos int64) (int64, error)
	PutObject(ctx context.Context, params TxObjectParams, r io.Reader) error
	GetObjectPos(ctx context.Context, params TxObjectParams) (int64, error)
}

// TxService holds the cloud.google.com/go/storage client
// as well as its associated context.
// Closures are used as minimal wrappers around the Google Cloud Storage API, since the Storage API cannot be mocked.
// The usage of these closures allow them to be redefined in the testing package, allowing test to be run against this file.
type TxService struct {
	Client *cos.ObjectService
	// BucketName 存储桶名称
	Endpoint string
}

// NewTxService returns a TxService object given a GCloud service account file path.
func NewTxService(config *TxConfig) (*TxService, error) {
	// New client

	u, _ := url.Parse(config.Endpoint)
	b := &cos.BaseURL{BucketURL: u}

	// New client
	client := cos.NewClient(b, &http.Client{
		Transport: &cos.AuthorizationTransport{
			// 通过环境变量获取密钥
			// 环境变量 SECRETID 表示用户的 SecretId，登录访问管理控制台查看密钥，https://console.cloud.tencent.com/cam/capi
			SecretID: config.AccessKeyId,
			// 环境变量 SECRETKEY 表示用户的 SecretKey，登录访问管理控制台查看密钥，https://console.cloud.tencent.com/cam/capi
			SecretKey: config.AccessKeySecret,
			// Debug 模式，把对应 请求头部、请求内容、响应头部、响应内容 输出到标准输出
			Transport: &debug.DebugRequestTransport{
				RequestHeader:  false,
				RequestBody:    false,
				ResponseHeader: false,
				ResponseBody:   false,
			},
		},
	})

	// Get bucket
	ok, err := client.Bucket.IsExist(context.Background())
	if err == nil && ok {
		fmt.Printf("bucket exists\n")
	} else if err != nil {
		fmt.Printf("head bucket failed: %v\n", err)
		os.Exit(1)
	} else {
		fmt.Printf("bucket does not exist\n")
		os.Exit(1)
	}

	service := &TxService{
		Client:   client.Object,
		Endpoint: config.Endpoint,
	}

	return service, nil
}

// GetObjectSize returns the byte length of the specified Tx object.
func (service *TxService) GetObjectSize(ctx context.Context, params TxObjectParams) (int64, error) {
	// 获取对象的元信息
	headers, err := service.Client.Head(ctx, params.ID, nil)
	if err != nil {
		slog.Debug(err.Error())
		return 0, err
	}

	// 获取 Content-Length（文件大小）
	contentLengthStr := headers.Header.Get("Content-Length")

	contentLength, err := strconv.ParseInt(contentLengthStr, 10, 64)
	if err != nil {
		slog.Debug(err.Error())
		return 0, err
	}

	return contentLength, nil
}

// ReadObject reads a TxObjectParams, returning a TxReader object if successful, and an error otherwise
func (service *TxService) ReadObject(ctx context.Context, params TxObjectParams) (TxReader, error) {
	// 获取对象元数据
	objectMeta, err := service.Client.Head(ctx, params.ID, nil)
	if err != nil {
		slog.Debug(err.Error() + params.ID)
		return nil, err
	}

	// 获取文件大小
	sizeStr := objectMeta.Header.Get("Content-Length")
	totalSize, err := strconv.ParseInt(sizeStr, 10, 64) // 更安全的转换
	if err != nil {
		slog.Debug(sizeStr)
		slog.Debug(err.Error())
		return nil, err
	}

	// 获取内容类型
	contentType := objectMeta.Header.Get("Content-Type")

	// 创建 HTTP Header
	headers := http.Header{}
	headers.Set("Content-Type", contentType) // 可能不需要设置，因为 OSS 自动返回

	// 获取对象
	reader, err := service.Client.Get(ctx, params.ID, nil)
	if err != nil {
		slog.Debug(err.Error())
		return nil, err
	}

	return &TxOSSReader{
		reader:      reader.Body,
		contentType: contentType,
		size:        totalSize,
		remain:      totalSize,
	}, nil
}

// SetObjectMetadata reads a TxObjectParams and a map of metadata, returning a nil on success and an error otherwise
func (service *TxService) SetObjectMetadata(ctx context.Context, params TxObjectParams, metadata map[string]string) error {
	// 构造源对象 URL: bucket/object
	sourceURL := fmt.Sprintf("%s/%s", service.Endpoint, params.ID)

	// 设置元信息
	opt := &cos.ObjectCopyOptions{
		ObjectCopyHeaderOptions: &cos.ObjectCopyHeaderOptions{
			XCosMetadataDirective: "Replaced", // 关键:替换元信息
			XCosMetaXXX:           &http.Header{},
		},
	}

	// 添加自定义元信息
	for k, v := range metadata {
		opt.XCosMetaXXX.Add("x-cos-meta-"+k, v)
	}

	// 复制对象到自身,替换元信息
	_, _, err := service.Client.Copy(ctx, params.ID, sourceURL, opt)
	return err
}

// PutObject reads  to file
func (service *TxService) PutObject(ctx context.Context, params TxObjectParams, r io.Reader) error {
	_, err := service.Client.Put(ctx, params.ID, r, nil)
	return err
}

// DeleteObject deletes the object defined by TxObjectParams
func (service *TxService) DeleteObject(ctx context.Context, params TxObjectParams) error {
	_, err := service.Client.Delete(ctx, params.ID)
	return err
}

// Write object writes the file set out by the TxObjectParams
func (service *TxService) WriteObject(ctx context.Context, params TxObjectParams, r io.Reader, pos int64) (int64, error) {
	// props, err := service.Client.GetObjectDetailedMeta(params.ID)

	props, err := service.Client.Head(ctx, params.ID, nil)
	nextPos := int64(0)
	if err != nil {
		if !cos.IsNotFoundError(err) {
			slog.Debug(err.Error())
			return 0, err
		}
	} else {
		nextPos, err = strconv.ParseInt(props.Header.Get(NextAppendPosHeader), 10, 64)
		if err != nil {
			slog.Debug(props.Header.Get(NextAppendPosHeader))
			slog.Debug(err.Error())
			return 0, err
		}
	}

	if nextPos != pos {
		slog.Debug(fmt.Sprintf("id:%s , pos is not match, inputis %d ,  cloud is %d", params.ID, pos, nextPos))
	}

	dataSize, r, err := getLength(r)
	if err != nil {
		return 0, err
	}

	// 3. 执行追加操作
	opt := &cos.ObjectPutOptions{
		ObjectPutHeaderOptions: &cos.ObjectPutHeaderOptions{
			// ContentLength: totalBytes,
			XCosMetaXXX:   &http.Header{},
			ContentLength: dataSize, // 使用传入的数据大小 ,
		},
	}

	opt.XCosMetaXXX.Set(NextAppendPosHeader, strconv.FormatInt(pos, 10))

	newPos, _, err := service.Client.Append(ctx, params.ID, int(pos), r, opt)
	// 4. 检查追加结果
	if err != nil {
		slog.Debug(props.Header.Get(NextAppendPosHeader))
		fmt.Println(err.Error())
		slog.Debug(err.Error())
		return 0, err
	}

	return int64(newPos), err
}

func getLength(reader io.Reader) (int64, io.Reader, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return 0, nil, err
	}

	// 创建新的 Reader 来"重置"
	newReader := bytes.NewReader(data)

	return int64(len(data)), newReader, nil
}

// GetObjectPos : the next append position by GetObjectDetailedMeta
func (service *TxService) GetObjectPos(ctx context.Context, params TxObjectParams) (int64, error) {
	props, err := service.Client.Head(ctx, params.ID, nil)
	if err != nil {
		return 0, err
	}

	nextPos, err := strconv.ParseInt(props.Header.Get(NextAppendPosHeader), 10, 64)
	if err != nil {
		slog.Debug(props.Header.Get(NextAppendPosHeader))
		slog.Debug(err.Error())
		return 0, err
	}

	return nextPos, nil
}
