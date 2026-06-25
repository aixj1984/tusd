// Package baidustore provides a Baidu BOS based backend for tusd.
package baidustore

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

	"github.com/baidubce/bce-sdk-go/bce"
	"github.com/tus/tusd/v2/internal/uid"
	"github.com/tus/tusd/v2/pkg/handler"
)

type BaiduStore struct {
	Bucket       string
	Container    string
	ObjectPrefix string
	Service      BaiduAPI
}

func New(bucket string, service BaiduAPI) BaiduStore {
	return BaiduStore{
		Bucket:  bucket,
		Service: service,
	}
}

func (store BaiduStore) UseIn(composer *handler.StoreComposer) {
	composer.UseCore(store)
	composer.UseTerminater(store)
	composer.UseContentServer(store)
}

func (store BaiduStore) NewUpload(ctx context.Context, info handler.FileInfo) (handler.Upload, error) {
	if info.ID == "" {
		info.ID = uid.Uid()
	}

	info.Storage = map[string]string{
		"Type":      "baidustore",
		"Bucket":    store.Bucket,
		"Key":       store.binPath(info.ID),
		"Container": store.Container,
	}

	err := store.writeInfo(ctx, info.ID, info)
	if err != nil {
		return &baiduUpload{info.ID, &store, &info}, err
	}

	return &baiduUpload{info.ID, &store, &info}, nil
}

type baiduUpload struct {
	id    string
	store *BaiduStore
	info  *handler.FileInfo
}

func (store BaiduStore) AsTerminatableUpload(upload handler.Upload) handler.TerminatableUpload {
	return upload.(*baiduUpload)
}

func (store BaiduStore) AsServableUpload(upload handler.Upload) handler.ServableUpload {
	return upload.(*baiduUpload)
}

func (upload baiduUpload) Terminate(ctx context.Context) error {
	id := upload.id
	store := upload.store

	params := BaiduObjectParams{
		Bucket: store.Bucket,
		ID:     store.binPath(id),
	}

	err := store.Service.DeleteObject(ctx, params)
	if err != nil {
		return err
	}

	params.ID = store.infoPath(id)
	return store.Service.DeleteObject(ctx, params)
}

func (store BaiduStore) GetUpload(ctx context.Context, id string) (handler.Upload, error) {
	info := handler.FileInfo{}

	params := BaiduObjectParams{
		Bucket: store.Bucket,
		ID:     store.infoPath(id),
	}

	r, err := store.Service.ReadObject(ctx, params)
	if err != nil {
		if isNotFound(err) {
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
		if val, exists := info.Storage["Type"]; exists && val == "filestore" {
			params.ID = store.binPath(id)
			info.Offset, err = store.Service.GetObjectSize(ctx, params)
			if isNotFound(err) {
				info.Offset = 0
			} else if err != nil {
				slog.Debug(err.Error())
			}
		}
	}

	return &baiduUpload{id, &store, &info}, nil
}

func (upload baiduUpload) WriteChunk(ctx context.Context, offset int64, src io.Reader) (int64, error) {
	store := upload.store

	params := BaiduObjectParams{
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

func (upload baiduUpload) GetInfo(ctx context.Context) (handler.FileInfo, error) {
	return *upload.info, nil
}

func (store BaiduStore) writeInfo(ctx context.Context, id string, info handler.FileInfo) error {
	data, err := json.Marshal(info)
	if err != nil {
		slog.Debug(err.Error())
		return err
	}

	params := BaiduObjectParams{
		Bucket: store.Bucket,
		ID:     store.infoPath(id),
	}

	err = store.Service.PutObject(ctx, params, bytes.NewReader(data))
	if err != nil {
		slog.Debug(err.Error())
		return err
	}

	return nil
}

func (upload baiduUpload) FinishUpload(ctx context.Context) error {
	return nil
}

func (upload baiduUpload) GetReader(ctx context.Context) (io.ReadCloser, error) {
	params := BaiduObjectParams{
		Bucket: upload.store.Bucket,
		ID:     upload.store.binPath(upload.id),
	}
	return upload.store.Service.ReadObject(ctx, params)
}

func (store BaiduStore) binPath(id string) string {
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

func (store BaiduStore) infoPath(id string) string {
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

func (store BaiduStore) GetFileDirPath(id string) (path string) {
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

func setDownloadNoCacheHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Pragma", "no-cache")
	h.Del("ETag")
	h.Del("Expires")
	h.Del("Last-Modified")
}

func (upload *baiduUpload) ServeContent(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	headers, fileReader, err := upload.store.Service.GetObject(ctx, BaiduObjectParams{
		Bucket: upload.store.Bucket,
		ID:     upload.store.binPath(upload.id),
	}, &r.Header)
	if err != nil {
		w.Header().Del("Content-Type")
		w.Header().Del("Content-Disposition")

		if bosErr, ok := err.(*bce.BceServiceError); ok {
			if bosErr.StatusCode == http.StatusNotFound || bosErr.StatusCode == http.StatusForbidden {
				return errors.New("not found file")
			}

			if bosErr.StatusCode == http.StatusNotModified {
				w.Header().Set("Accept-Ranges", "bytes")
				setDownloadNoCacheHeaders(w)
				w.WriteHeader(http.StatusNotModified)
				return nil
			}

			if bosErr.StatusCode == http.StatusRequestedRangeNotSatisfiable {
				if val := headers.Get("Content-Range"); val != "" {
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

	statusCode := http.StatusOK
	if contentRange := headers.Get("Content-Range"); contentRange != "" {
		statusCode = http.StatusPartialContent
	} else if contentLength := headers.Get("Content-Length"); contentLength == "0" {
		statusCode = http.StatusNoContent
	}
	w.WriteHeader(statusCode)

	_, err = io.Copy(w, fileReader)
	return err
}
