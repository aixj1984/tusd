// Package filestore provide a storage backend based on the local file system.
//
// FileStore is a storage backend used as a handler.DataStore in handler.NewHandler.
// It stores the uploads in a directory specified in two different files: The
// `[id].info` files are used to store the fileinfo in JSON format. The
// `[id]` files without an extension contain the raw binary data uploaded.
// No cleanup is performed so you may want to run a cronjob to ensure your disk
// is not filled up with old and finished uploads.
//
// Related to the filestore is the package filelocker, which provides a file-based
// locking mechanism. The use of some locking method is recommended and further
// explained in https://tus.github.io/tusd/advanced-topics/locks/.
package filestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/tus/tusd/v2/internal/uid"
	"github.com/tus/tusd/v2/pkg/handler"
)

var (
	defaultFilePerm      = os.FileMode(0o664)
	defaultDirectoryPerm = os.FileMode(0o754)
)

// See the handler.DataStore interface for documentation about the different
// methods.
type FileStore struct {
	// Relative or absolute path to store files in. FileStore does not check
	// whether the path exists, use os.MkdirAll in this case on your own.
	Path string
}

// New creates a new file based storage backend. The directory specified will
// be used as the only storage entry. This method does not check
// whether the path exists, use os.MkdirAll to ensure.
func New(path string) FileStore {
	return FileStore{path}
}

// UseIn sets this store as the core data store in the passed composer and adds
// all possible extension to it.
func (store FileStore) UseIn(composer *handler.StoreComposer) {
	composer.UseCore(store)
	composer.UseTerminater(store)
	composer.UseConcater(store)
	composer.UseLengthDeferrer(store)
	composer.UseContentServer(store)
}

func isDirExists(path string) bool {
	fi, err := os.Stat(path)

	if err != nil {
		return os.IsExist(err)
	} else {
		return fi.IsDir()
	}
}

func (store FileStore) GetFileDirPath(id string) (path string) {
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

func (store FileStore) NewUpload(ctx context.Context, info handler.FileInfo) (handler.Upload, error) {
	if info.ID == "" {
		info.ID = uid.Uid()
	}
	// create dir
	dirPath := store.GetFileDirPath(info.ID)
	if len(dirPath) > 0 {
		if !isDirExists(store.Path + "/" + dirPath) {
			err := os.MkdirAll(store.Path+"/"+dirPath, defaultFilePerm)
			if err != nil {
				return nil, err
			}
		}
	}

	binPath := store.binPath(info.ID)
	info.Storage = map[string]string{
		"Type": "filestore",
		"Path": binPath,
	}

	// Create binary file with no content
	file, err := os.OpenFile(binPath, os.O_CREATE|os.O_WRONLY, defaultFilePerm)
	if err != nil {
		if os.IsNotExist(err) {
			err = fmt.Errorf("upload directory does not exist: %s", store.Path)
		}
		return nil, err
	}
	err = file.Close()
	if err != nil {
		return nil, err
	}

	upload := &fileUpload{
		info:     info,
		infoPath: store.infoPath(info.ID),
		binPath:  binPath,
	}

	// writeInfo creates the file by itself if necessary
	err = upload.writeInfo()
	if err != nil {
		return nil, err
	}

	return upload, nil
}

func (store FileStore) GetUpload(ctx context.Context, id string) (handler.Upload, error) {
	info := handler.FileInfo{}
	data, err := os.ReadFile(store.infoPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			// Interpret os.ErrNotExist as 404 Not Found
			err = handler.ErrNotFound
		}
		return nil, err
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}

	binPath := store.binPath(id)
	infoPath := store.infoPath(id)
	stat, err := os.Stat(binPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Interpret os.ErrNotExist as 404 Not Found
			err = handler.ErrNotFound
		}
		return nil, err
	}

	info.Offset = stat.Size()

	return &fileUpload{
		info:     info,
		binPath:  binPath,
		infoPath: infoPath,
	}, nil
}

func (store FileStore) AsTerminatableUpload(upload handler.Upload) handler.TerminatableUpload {
	return upload.(*fileUpload)
}

func (store FileStore) AsLengthDeclarableUpload(upload handler.Upload) handler.LengthDeclarableUpload {
	return upload.(*fileUpload)
}

func (store FileStore) AsConcatableUpload(upload handler.Upload) handler.ConcatableUpload {
	return upload.(*fileUpload)
}

func (store FileStore) AsServableUpload(upload handler.Upload) handler.ServableUpload {
	return upload.(*fileUpload)
}

// defaultBinPath returns the path to the file storing the binary data, if it is
// not customized using the pre-create hook.
func (store FileStore) defaultBinPath(id string) string {
	return filepath.Join(store.Path, id)
}

// binPath returns the path to the file storing the binary data.
func (store FileStore) binPath(id string) string {
	// return filepath.Join(store.Path, id)
	dirPath := store.GetFileDirPath(id)
	if len(dirPath) == 0 {
		return filepath.Join(store.Path, id+".bin")
	}
	return filepath.Join(store.Path, dirPath, id+".bin")
}

// infoPath returns the path to the .info file storing the file's info.
func (store FileStore) infoPath(id string) string {
	// return filepath.Join(store.Path, id+".info")
	dirPath := store.GetFileDirPath(id)
	if len(dirPath) == 0 {
		return filepath.Join(store.Path, id+".info")
	}
	return filepath.Join(store.Path, dirPath, id+".info")
}

type fileUpload struct {
	// info stores the current information about the upload
	info handler.FileInfo
	// infoPath is the path to the .info file
	infoPath string
	// binPath is the path to the binary file (which has no extension)
	binPath string
}

func (upload *fileUpload) GetInfo(ctx context.Context) (handler.FileInfo, error) {
	return upload.info, nil
}

func (upload *fileUpload) WriteChunk(ctx context.Context, offset int64, src io.Reader) (int64, error) {
	file, err := os.OpenFile(upload.binPath, os.O_WRONLY|os.O_APPEND, defaultFilePerm)
	if err != nil {
		return 0, err
	}
	// Avoid the use of defer file.Close() here to ensure no errors are lost
	// See https://github.com/tus/tusd/issues/698.

	n, err := io.Copy(file, src)
	upload.info.Offset += n
	if err != nil {
		file.Close()
		return n, err
	}

	return n, file.Close()
}

func (upload *fileUpload) GetReader(ctx context.Context) (io.ReadCloser, error) {
	return os.Open(upload.binPath)
}

func (upload *fileUpload) Terminate(ctx context.Context) error {
	// We ignore errors indicating that the files cannot be found because we want
	// to delete them anyways. The files might be removed by a cron job for cleaning up
	// or some file might have been removed when tusd crashed during the termination.
	err := os.Remove(upload.binPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	err = os.Remove(upload.infoPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	return nil
}

func (upload *fileUpload) ConcatUploads(ctx context.Context, uploads []handler.Upload) (err error) {
	file, err := os.OpenFile(upload.binPath, os.O_WRONLY|os.O_APPEND, defaultFilePerm)
	if err != nil {
		return err
	}
	defer func() {
		// Ensure that close error is propagated, if it occurs.
		// See https://github.com/tus/tusd/issues/698.
		cerr := file.Close()
		if err == nil {
			err = cerr
		}
	}()

	for _, partialUpload := range uploads {
		fileUpload := partialUpload.(*fileUpload)

		src, err := os.Open(fileUpload.binPath)
		if err != nil {
			return err
		}

		if _, err := io.Copy(file, src); err != nil {
			return err
		}
	}

	return
}

func (upload *fileUpload) DeclareLength(ctx context.Context, length int64) error {
	upload.info.Size = length
	upload.info.SizeIsDeferred = false
	return upload.writeInfo()
}

// writeInfo updates the entire information. Everything will be overwritten.
func (upload *fileUpload) writeInfo() error {
	data, err := json.Marshal(upload.info)
	if err != nil {
		return err
	}
	return createFile(upload.infoPath, data)
}

func (upload *fileUpload) FinishUpload(ctx context.Context) error {
	return nil
}

func (upload *fileUpload) ServeContent(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	http.ServeFile(w, r, upload.binPath)

	return nil
}

// createFile creates the file with the content. If the corresponding directory does not exist,
// it is created. If the file already exists, its content is removed.
func createFile(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, defaultFilePerm)
	if err != nil {
		if os.IsNotExist(err) {
			// An upload ID containing slashes is mapped onto different directories on disk,
			// for example, `myproject/uploadA` should be put into a folder called `myproject`.
			// If we get an error indicating that a directory is missing, we try to create it.
			if err := os.MkdirAll(filepath.Dir(path), defaultDirectoryPerm); err != nil {
				return fmt.Errorf("failed to create directory for %s: %s", path, err)
			}

			// Try creating the file again.
			file, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, defaultFilePerm)
			if err != nil {
				// If that still doesn't work, error out.
				return err
			}
		} else {
			return err
		}
	}

	if content != nil {
		if _, err := file.Write(content); err != nil {
			return err
		}
	}

	return file.Close()
}
