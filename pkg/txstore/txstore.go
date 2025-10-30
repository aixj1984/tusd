// Package txstore provides a Google cloud storage based backend.
//
// TxStore is a storage backend that uses the TxAPI interface in order to store uploads
// on Tx. Uploads will be represented by two files in Tx; the data file will be stored
// as an extensionless object [uid] and the JSON info file will stored as [uid].info.
// In order to store uploads on Tx, make sure to specify the appropriate Google service
// account file path in the Tx_SERVICE_ACCOUNT_FILE environment variable. Also make sure that
// this service account file has the "https://www.googleapis.com/auth/devstorage.read_write"
// scope enabled so you can read and write data to the storage buckets associated with the
// service account file.
package txstore

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/tencentyun/cos-go-sdk-v5"
	"github.com/tus/tusd/v2/internal/uid"
	"github.com/tus/tusd/v2/pkg/handler"
)

// See the handler.DataStore interface for documentation about the different
// methods.
type TxStore struct {
	// Specifies the Tx bucket that uploads will be stored in
	Bucket string

	Container string

	// ObjectPrefix is prepended to the name of each Tx object that is created.
	// It can be used to create a pseudo-directory structure in the bucket,
	// e.g. "path/to/my/uploads".
	ObjectPrefix string

	// Service specifies an interface used to communicate with the Google
	// cloud storage backend. Implementation can be seen in gcsservice file.
	Service TxAPI
}

// New constructs a new Tx storage backend using the supplied Tx bucket name
// and service object.
func New(bucket string, service TxAPI) TxStore {
	return TxStore{
		Bucket:  bucket,
		Service: service,
	}
}

func (store TxStore) UseIn(composer *handler.StoreComposer) {
	composer.UseCore(store)
	composer.UseTerminater(store)
}

func (store TxStore) NewUpload(ctx context.Context, info handler.FileInfo) (handler.Upload, error) {
	if info.ID == "" {
		info.ID = uid.Uid()
	}

	info.Storage = map[string]string{
		"Type":      "txstore",
		"Bucket":    store.Bucket,
		"Key":       store.binPath(info.ID),
		"Container": store.Container,
	}

	err := store.writeInfo(ctx, info.ID, info)
	if err != nil {
		return &txUpload{info.ID, &store, &info}, err
	}

	return &txUpload{info.ID, &store, &info}, nil
}

type txUpload struct {
	id    string
	store *TxStore
	info  *handler.FileInfo
}

func (store TxStore) AsTerminatableUpload(upload handler.Upload) handler.TerminatableUpload {
	return upload.(*txUpload)
}

func (upload txUpload) Terminate(ctx context.Context) error {
	id := upload.id
	store := upload.store

	params := TxObjectParams{
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

func (store TxStore) GetUpload(ctx context.Context, id string) (handler.Upload, error) {
	info := handler.FileInfo{}

	params := TxObjectParams{
		Bucket: store.Bucket,
		ID:     store.infoPath(id),
	}

	r, err := store.Service.ReadObject(ctx, params)
	if err != nil {
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
			if cos.IsNotFoundError(err) {
				info.Offset = 0
			} else if err != nil {
				slog.Debug(err.Error())
			}
		}
	}

	return &txUpload{id, &store, &info}, nil
}

func (upload txUpload) WriteChunk(ctx context.Context, offset int64, src io.Reader) (int64, error) {
	store := upload.store

	params := TxObjectParams{
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

func (upload txUpload) GetInfo(ctx context.Context) (handler.FileInfo, error) {
	return *upload.info, nil
}

func (store TxStore) writeInfo(ctx context.Context, id string, info handler.FileInfo) error {
	data, err := json.Marshal(info)
	if err != nil {
		slog.Debug(err.Error())
		return err
	}

	r := bytes.NewReader(data)

	params := TxObjectParams{
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

func (upload txUpload) FinishUpload(ctx context.Context) error {
	return nil
}

func (upload txUpload) GetReader(ctx context.Context) (io.ReadCloser, error) {
	id := upload.id
	store := upload.store

	params := TxObjectParams{
		Bucket: store.Bucket,
		ID:     store.binPath(id),
	}

	return store.Service.ReadObject(ctx, params)
}

func (store TxStore) keyWithPrefix(key string) string {
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

func (store TxStore) binPath(id string) string {
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
func (store TxStore) infoPath(id string) string {
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

func (store TxStore) GetFileDirPath(id string) (path string) {
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
