// Package alistore provides a Google cloud storage based backend.
//
// AliStore is a storage backend that uses the AliAPI interface in order to store uploads
// on Ali. Uploads will be represented by two files in Ali; the data file will be stored
// as an extensionless object [uid] and the JSON info file will stored as [uid].info.
// In order to store uploads on Ali, make sure to specify the appropriate Google service
// account file path in the Ali_SERVICE_ACCOUNT_FILE environment variable. Also make sure that
// this service account file has the "https://www.googleapis.com/auth/devstorage.read_write"
// scope enabled so you can read and write data to the storage buckets associated with the
// service account file.
package alistore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"github.com/tus/tusd/v2/internal/uid"
	"github.com/tus/tusd/v2/pkg/handler"
)

// See the handler.DataStore interface for documentation about the different
// methods.
type AliStore struct {
	// Specifies the Ali bucket that uploads will be stored in
	Bucket string

	Container string

	// ObjectPrefix is prepended to the name of each Ali object that is created.
	// It can be used to create a pseudo-directory structure in the bucket,
	// e.g. "path/to/my/uploads".
	ObjectPrefix string

	// Service specifies an interface used to communicate with the Google
	// cloud storage backend. Implementation can be seen in gcsservice file.
	Service AliAPI
}

// New constructs a new Ali storage backend using the supplied Ali bucket name
// and service object.
func New(bucket string, service AliAPI) AliStore {
	return AliStore{
		Bucket:  bucket,
		Service: service,
	}
}

func (store AliStore) UseIn(composer *handler.StoreComposer) {
	composer.UseCore(store)
	composer.UseTerminater(store)
	composer.UseContentServer(store)
}

func (store AliStore) NewUpload(ctx context.Context, info handler.FileInfo) (handler.Upload, error) {
	if info.ID == "" {
		info.ID = uid.Uid()
	}

	info.Storage = map[string]string{
		"Type":      "alistore",
		"Bucket":    store.Bucket,
		"Key":       store.binPath(info.ID),
		"Container": store.Container,
	}

	err := store.writeInfo(ctx, info.ID, info)
	if err != nil {
		return &aliUpload{info.ID, &store, &info}, err
	}

	return &aliUpload{info.ID, &store, &info}, nil
}

type aliUpload struct {
	id    string
	store *AliStore
	info  *handler.FileInfo
}

func (store AliStore) AsTerminatableUpload(upload handler.Upload) handler.TerminatableUpload {
	return upload.(*aliUpload)
}

func (store AliStore) AsServableUpload(upload handler.Upload) handler.ServableUpload {
	return upload.(*aliUpload)
}

func (upload aliUpload) Terminate(ctx context.Context) error {
	id := upload.id
	store := upload.store

	params := AliObjectParams{
		Bucket: store.Bucket,
		ID:     store.binPath(id),
	}

	err := store.Service.DeleteObject(ctx, params)
	if err != nil {
		return err
	}

	params.ID = store.infoPath(id)

	err = store.Service.DeleteObject(ctx, params)
	if err != nil {
		return err
	}

	return nil
}

func (store AliStore) GetUpload(ctx context.Context, id string) (handler.Upload, error) {
	info := handler.FileInfo{}

	params := AliObjectParams{
		Bucket: store.Bucket,
		ID:     store.infoPath(id),
	}

	r, err := store.Service.ReadObject(ctx, params)
	if err != nil {
		ossErr, ok := err.(oss.ServiceError)
		if ok && ossErr.Code == "NoSuchKey" {
			return nil, handler.ErrNotFound
		}
		return nil, err
	}
	defer r.Close()

	buf, err := io.ReadAll(r)
	if err != nil {
		slog.Debug(err.Error())
		return nil, err
	}

	if err := json.Unmarshal(buf, &info); err != nil {
		slog.Debug(err.Error())
		return nil, err
	}

	if info.Offset == 0 {
		// 弥补文件存储没有写offset的问题
		if val, exists := info.Storage["Type"]; exists && val == "filestore" {
			params.ID = store.binPath(id)
			info.Offset, err = store.Service.GetObjectSize(ctx, params)
			if err != nil {
				slog.Debug(err.Error())
			}
		}
	}

	return &aliUpload{id, &store, &info}, nil
}

func (upload aliUpload) WriteChunk(ctx context.Context, offset int64, src io.Reader) (int64, error) {
	store := upload.store

	params := AliObjectParams{
		Bucket: store.Bucket,
		ID:     upload.store.binPath(upload.id),
	}

	nextPos, err := store.Service.WriteObject(ctx, params, src, offset)
	if err != nil {
		slog.Debug(err.Error())
		return 0, err
	}

	upload.info.Offset = nextPos

	store.writeInfo(ctx, upload.info.ID, *upload.info)

	return nextPos - offset, err
}

func (upload aliUpload) GetInfo(ctx context.Context) (handler.FileInfo, error) {
	return *upload.info, nil
}

func (store AliStore) writeInfo(ctx context.Context, id string, info handler.FileInfo) error {
	data, err := json.Marshal(info)
	if err != nil {
		slog.Debug(err.Error())
		return err
	}

	r := bytes.NewReader(data)

	params := AliObjectParams{
		Bucket: store.Bucket,
		ID:     store.infoPath(id),
	}

	err = store.Service.PutObject(ctx, params, r)
	if err != nil {
		slog.Debug(err.Error())
		return err
	}

	return nil
}

func (upload aliUpload) FinishUpload(ctx context.Context) error {
	return nil
}

func (upload aliUpload) GetReader(ctx context.Context) (io.ReadCloser, error) {
	id := upload.id
	store := upload.store

	params := AliObjectParams{
		Bucket: store.Bucket,
		ID:     store.binPath(id),
	}

	return store.Service.ReadObject(ctx, params)
}

func (store AliStore) keyWithPrefix(key string) string {
	prefix := store.ObjectPrefix
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	dirPath := store.GetFileDirPath(key)
	if len(dirPath) > 0 {
		key = dirPath + "/" + key
	}

	return prefix + key
}

func (store AliStore) binPath(id string) string {
	prefix := store.ObjectPrefix
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	dirPath := store.GetFileDirPath(id)
	if len(dirPath) == 0 {
		return prefix + id + ".bin"
	}
	return prefix + dirPath + "/" + id + ".bin"
}

// infoPath returns the path to the .info file storing the file's info.
func (store AliStore) infoPath(id string) string {
	// return filepath.Join(store.Path, id+".info")
	prefix := store.ObjectPrefix
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	dirPath := store.GetFileDirPath(id)
	if len(dirPath) == 0 {
		return prefix + id + ".info"
	}
	return prefix + dirPath + "/" + id + ".info"
}

func (store AliStore) GetFileDirPath(id string) (path string) {
	srcId := []byte(id)
	if len(srcId) >= 32 && len(srcId) < 36 {
		currentDate := [8]byte{0}
		for i := 0; i < 8; i++ {
			currentDate[i] = srcId[(i*4)+1]
		}
		_, err := time.Parse("20060102", string(currentDate[0:8]))
		if err != nil {
			return ""
		}
		return string(currentDate[0:8])
	} else if len(srcId) >= 36 {
		currentDate := [10]byte{0}
		for i := 0; i < 10; i++ {
			currentDate[i] = srcId[(i*4)+1]
		}
		_, err := time.Parse("2006010215", string(currentDate[0:10]))
		if err != nil {
			return ""
		}
		return string(currentDate[0:8]) + "/" + string(currentDate[0:10])
	}
	return ""
}

// setDownloadNoCacheHeaders forces download responses to not be cached by clients or shared caches.
func setDownloadNoCacheHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Pragma", "no-cache")
	h.Del("ETag")
	h.Del("Expires")
	h.Del("Last-Modified")
}

func (store *aliUpload) ServeContent(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	// 阿里云OSS获取对象
	headers, fileReader, err := store.store.Service.GetObject(ctx, AliObjectParams{
		Bucket: store.store.Bucket,
		ID:     store.store.binPath(store.id),
	}, &r.Header)
	if err != nil {
		// 删除由tusd处理器设置的header。对于错误响应我们不需要它们。
		w.Header().Del("Content-Type")
		w.Header().Del("Content-Disposition")

		// 处理阿里云OSS的错误响应
		if ossErr, ok := err.(*oss.ServiceError); ok {
			if ossErr.StatusCode == http.StatusNotFound || ossErr.StatusCode == http.StatusForbidden {
				// 如果找不到对象，表示上传尚未完成，无法提供。
				// 在这个阶段上传本身不可能不存在，因为处理器已经检查过这种情况。
				// 因此，我们可以安全地假定上传仍在进行中。
				return errors.New("not found file")
			}

			if ossErr.StatusCode == http.StatusNotModified {
				w.Header().Set("Accept-Ranges", "bytes")
				setDownloadNoCacheHeaders(w)
				w.WriteHeader(http.StatusNotModified)
				return nil
			}

			if ossErr.StatusCode == http.StatusRequestedRangeNotSatisfiable {
				// 对于416 Request Range Not Satisfiable响应，应设置Content-Range头部
				if val := r.Header.Get("Content-Range"); val != "" {
					w.Header().Set("Content-Range", val)
				}
				w.Header().Set("Accept-Ranges", "bytes")
				setDownloadNoCacheHeaders(w)

				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return nil
			}
		}
		return err
	}
	defer fileReader.Close()

	// 从响应头部复制相关字段到HTTP响应（不透传 OSS 的缓存/校验头，避免 Range 与二次播放被错误缓存）
	headersToCopy := []string{
		"Accept-Ranges",
		"Content-Disposition",
		"Content-Encoding",
		"Content-Language",
		"Content-Length",
		"Content-Range",
		"Content-Type",
	}

	for _, header := range headersToCopy {
		if val := headers.Get(header); val != "" {
			w.Header().Set(header, val)
		}
	}
	setDownloadNoCacheHeaders(w)

	// 确定HTTP状态码
	statusCode := http.StatusOK
	if contentRange := headers.Get("Content-Range"); contentRange != "" {
		// 对于范围请求使用206 Partial Content
		statusCode = http.StatusPartialContent
	} else if contentLength := headers.Get("Content-Length"); contentLength == "0" {
		statusCode = http.StatusNoContent
	}
	w.WriteHeader(statusCode)

	_, err = io.Copy(w, fileReader)
	return err
}
