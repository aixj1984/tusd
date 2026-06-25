package baidustore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/baidubce/bce-sdk-go/bce"
	"github.com/baidubce/bce-sdk-go/services/bos"
	"github.com/baidubce/bce-sdk-go/services/bos/api"
)

type BaiduObjectParams struct {
	Bucket string
	ID     string
}

type BaiduConfig struct {
	Endpoint        string
	AccessKeyId     string
	AccessKeySecret string
	BucketName      string
	RegionId        string
}

type BaiduReader interface {
	Close() error
	ContentType() string
	Read(p []byte) (int, error)
	Remain() int64
	Size() int64
}

type BaiduBOSReader struct {
	reader      io.ReadCloser
	contentType string
	size        int64
	remain      int64
}

func (r *BaiduBOSReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.remain -= int64(n)
	return n, err
}

func (r *BaiduBOSReader) Close() error {
	return r.reader.Close()
}

func (r *BaiduBOSReader) ContentType() string {
	return r.contentType
}

func (r *BaiduBOSReader) Remain() int64 {
	return r.remain
}

func (r *BaiduBOSReader) Size() int64 {
	return r.size
}

type BaiduAPI interface {
	ReadObject(ctx context.Context, params BaiduObjectParams) (BaiduReader, error)
	GetObject(ctx context.Context, params BaiduObjectParams, reqHeaders *http.Header) (http.Header, io.ReadCloser, error)
	GetObjectSize(ctx context.Context, params BaiduObjectParams) (int64, error)
	SetObjectMetadata(ctx context.Context, params BaiduObjectParams, metadata map[string]string) error
	DeleteObject(ctx context.Context, params BaiduObjectParams) error
	WriteObject(ctx context.Context, params BaiduObjectParams, r io.Reader, pos int64) (int64, error)
	PutObject(ctx context.Context, params BaiduObjectParams, r io.Reader) error
	GetObjectPos(ctx context.Context, params BaiduObjectParams) (int64, error)
}

type BaiduService struct {
	Client     *bos.Client
	BucketName string
}

func NewBaiduService(config *BaiduConfig) (*BaiduService, error) {
	client, err := bos.NewClient(config.AccessKeyId, config.AccessKeySecret, config.Endpoint)
	if err != nil {
		return nil, err
	}

	exists, err := client.DoesBucketExist(config.BucketName)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("bucket %s does not exist", config.BucketName)
	}

	return &BaiduService{
		Client:     client,
		BucketName: config.BucketName,
	}, nil
}

func isNotFound(err error) bool {
	bosErr, ok := err.(*bce.BceServiceError)
	if !ok {
		return false
	}
	return bosErr.StatusCode == http.StatusNotFound || bosErr.Code == "NoSuchKey"
}

func (service *BaiduService) GetObjectSize(ctx context.Context, params BaiduObjectParams) (int64, error) {
	meta, err := service.Client.GetObjectMetaWithContext(ctx, service.BucketName, params.ID)
	if err != nil {
		slog.Debug(err.Error())
		return 0, err
	}
	return meta.ContentLength, nil
}

func (service *BaiduService) ReadObject(ctx context.Context, params BaiduObjectParams) (BaiduReader, error) {
	meta, err := service.Client.GetObjectMetaWithContext(ctx, service.BucketName, params.ID)
	if err != nil {
		slog.Debug(err.Error() + params.ID)
		return nil, err
	}

	result, err := service.Client.GetObjectWithContext(ctx, service.BucketName, params.ID, nil)
	if err != nil {
		slog.Debug(err.Error())
		return nil, err
	}

	return &BaiduBOSReader{
		reader:      result.Body,
		contentType: meta.ContentType,
		size:        meta.ContentLength,
		remain:      meta.ContentLength,
	}, nil
}

func parseHTTPRangeForBOS(rangeHeader string, totalSize int64) ([]int64, error) {
	if rangeHeader == "" {
		return nil, nil
	}

	rangeHeader = strings.TrimSpace(rangeHeader)
	if !strings.HasPrefix(rangeHeader, "bytes=") {
		return nil, fmt.Errorf("unsupported range unit")
	}

	spec := strings.TrimSpace(rangeHeader[6:])
	if spec == "" {
		return nil, fmt.Errorf("empty range")
	}

	if strings.HasPrefix(spec, "-") {
		suffix, err := strconv.ParseInt(strings.TrimSpace(spec[1:]), 10, 64)
		if err != nil {
			return nil, err
		}
		if totalSize <= 0 {
			return nil, fmt.Errorf("suffix range requires object size")
		}
		start := totalSize - suffix
		if start < 0 {
			start = 0
		}
		return []int64{start, totalSize - 1}, nil
	}

	parts := strings.SplitN(spec, "-", 2)
	start, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil {
		return nil, err
	}
	if len(parts) == 1 || strings.TrimSpace(parts[1]) == "" {
		return []int64{start}, nil
	}

	end, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if err != nil {
		return nil, err
	}
	return []int64{start, end}, nil
}

func objectMetaToHTTPHeader(meta api.ObjectMeta) http.Header {
	h := http.Header{}
	setIfNotEmpty := func(key, val string) {
		if val != "" {
			h.Set(key, val)
		}
	}

	setIfNotEmpty("Content-Disposition", meta.ContentDisposition)
	setIfNotEmpty("Content-Encoding", meta.ContentEncoding)
	setIfNotEmpty("Content-Language", meta.ContentLanguage)
	if meta.ContentLength > 0 {
		h.Set("Content-Length", strconv.FormatInt(meta.ContentLength, 10))
	}
	setIfNotEmpty("Content-Range", meta.ContentRange)
	setIfNotEmpty("Content-Type", meta.ContentType)
	h.Set("Accept-Ranges", "bytes")
	return h
}

func (service *BaiduService) GetObject(ctx context.Context, params BaiduObjectParams, reqHeaders *http.Header) (http.Header, io.ReadCloser, error) {
	var ranges []int64

	if val := reqHeaders.Get("Range"); val != "" {
		var totalSize int64
		meta, err := service.Client.GetObjectMetaWithContext(ctx, service.BucketName, params.ID)
		if err != nil {
			slog.Debug(err.Error())
			return nil, nil, err
		}
		totalSize = meta.ContentLength

		ranges, err = parseHTTPRangeForBOS(val, totalSize)
		if err != nil {
			slog.Debug(err.Error())
			return nil, nil, err
		}
	}

	// 不转发 If-None-Match 等条件头，避免 Range 请求被缓存校验干扰。
	result, err := service.Client.GetObjectWithContext(ctx, service.BucketName, params.ID, nil, ranges...)
	if err != nil {
		slog.Debug(err.Error())
		return nil, nil, err
	}

	return objectMetaToHTTPHeader(result.ObjectMeta), result.Body, nil
}

func (service *BaiduService) SetObjectMetadata(ctx context.Context, params BaiduObjectParams, metadata map[string]string) error {
	args := &api.CopyObjectArgs{
		MetadataDirective: "replace",
		ObjectMeta: api.ObjectMeta{
			UserMeta: metadata,
		},
	}
	_, err := service.Client.CopyObjectWithContext(ctx, service.BucketName, params.ID, service.BucketName, params.ID, args)
	return err
}

func (service *BaiduService) PutObject(ctx context.Context, params BaiduObjectParams, r io.Reader) error {
	_, err := service.Client.PutObjectFromStreamWithContext(ctx, service.BucketName, params.ID, r, nil)
	return err
}

func (service *BaiduService) DeleteObject(ctx context.Context, params BaiduObjectParams) error {
	return service.Client.DeleteObject(service.BucketName, params.ID)
}

func (service *BaiduService) WriteObject(ctx context.Context, params BaiduObjectParams, r io.Reader, pos int64) (int64, error) {
	nextPos := int64(0)

	meta, err := service.Client.GetObjectMetaWithContext(ctx, service.BucketName, params.ID)
	if err != nil {
		if !isNotFound(err) {
			slog.Debug(err.Error())
			return 0, err
		}
	} else {
		nextPos, err = strconv.ParseInt(meta.NextAppendOffset, 10, 64)
		if err != nil {
			slog.Debug(meta.NextAppendOffset)
			slog.Debug(err.Error())
			return 0, err
		}
	}

	if nextPos != pos {
		slog.Debug(fmt.Sprintf("id:%s , pos is not match, inputis %d ,  cloud is %d", params.ID, pos, nextPos))
	}

	dataSize, reader, err := getLength(r)
	if err != nil {
		return 0, err
	}

	body, err := bce.NewBodyFromSizedReaderV2(reader, dataSize, false)
	if err != nil {
		return 0, err
	}

	args := &api.AppendObjectArgs{Offset: pos}
	res, err := service.Client.AppendObjectWithContext(ctx, service.BucketName, params.ID, body, args)
	if err != nil {
		slog.Debug(err.Error())
		return 0, err
	}

	return res.NextAppendOffset, nil
}

func getLength(reader io.Reader) (int64, io.Reader, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return 0, nil, err
	}
	return int64(len(data)), bytes.NewReader(data), nil
}

func (service *BaiduService) GetObjectPos(ctx context.Context, params BaiduObjectParams) (int64, error) {
	meta, err := service.Client.GetObjectMetaWithContext(ctx, service.BucketName, params.ID)
	if err != nil {
		slog.Debug(err.Error())
		return 0, err
	}

	nextPos, err := strconv.ParseInt(meta.NextAppendOffset, 10, 64)
	if err != nil {
		slog.Debug(err.Error())
		return 0, err
	}

	return nextPos, nil
}
