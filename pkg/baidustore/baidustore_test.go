package baidustore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/baidubce/bce-sdk-go/bce"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tus/tusd/v2/pkg/handler"
)

var (
	_ handler.DataStore           = BaiduStore{}
	_ handler.TerminaterDataStore = BaiduStore{}
	_ handler.ContentServerDataStore = BaiduStore{}
)

type mockBaiduAPI struct {
	putObjectCalls []BaiduObjectParams
	putObjectData  [][]byte

	readObjectFn func(ctx context.Context, params BaiduObjectParams) (BaiduReader, error)
	getObjectFn  func(ctx context.Context, params BaiduObjectParams, reqHeaders *http.Header) (http.Header, io.ReadCloser, error)
	deleteFn     func(ctx context.Context, params BaiduObjectParams) error
	writeObjectFn func(ctx context.Context, params BaiduObjectParams, r io.Reader, pos int64) (int64, error)
}

func (m *mockBaiduAPI) ReadObject(ctx context.Context, params BaiduObjectParams) (BaiduReader, error) {
	if m.readObjectFn != nil {
		return m.readObjectFn(ctx, params)
	}
	return nil, errors.New("read object not implemented")
}

func (m *mockBaiduAPI) GetObject(ctx context.Context, params BaiduObjectParams, reqHeaders *http.Header) (http.Header, io.ReadCloser, error) {
	if m.getObjectFn != nil {
		return m.getObjectFn(ctx, params, reqHeaders)
	}
	return nil, nil, errors.New("get object not implemented")
}

func (m *mockBaiduAPI) GetObjectSize(ctx context.Context, params BaiduObjectParams) (int64, error) {
	return 0, nil
}

func (m *mockBaiduAPI) SetObjectMetadata(ctx context.Context, params BaiduObjectParams, metadata map[string]string) error {
	return nil
}

func (m *mockBaiduAPI) DeleteObject(ctx context.Context, params BaiduObjectParams) error {
	if m.deleteFn != nil {
		return m.deleteFn(ctx, params)
	}
	return nil
}

func (m *mockBaiduAPI) WriteObject(ctx context.Context, params BaiduObjectParams, r io.Reader, pos int64) (int64, error) {
	if m.writeObjectFn != nil {
		return m.writeObjectFn(ctx, params, r, pos)
	}
	return 0, nil
}

func (m *mockBaiduAPI) PutObject(ctx context.Context, params BaiduObjectParams, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.putObjectCalls = append(m.putObjectCalls, params)
	m.putObjectData = append(m.putObjectData, data)
	return nil
}

func (m *mockBaiduAPI) GetObjectPos(ctx context.Context, params BaiduObjectParams) (int64, error) {
	return 0, nil
}

func buildDateUploadID(date string) string {
	id := make([]byte, 32)
	for i := range id {
		id[i] = 'a'
	}
	for i := 0; i < len(date); i++ {
		id[i*4+1] = date[i]
	}
	return string(id)
}

func buildDateHourUploadID(dateHour string) string {
	id := make([]byte, 40)
	for i := range id {
		id[i] = 'a'
	}
	for i := 0; i < len(dateHour); i++ {
		id[i*4+1] = dateHour[i]
	}
	return string(id)
}

func TestGetFileDirPath(t *testing.T) {
	store := BaiduStore{}

	assert.Empty(t, store.GetFileDirPath("short-id"))
	assert.Equal(t, "20250625", store.GetFileDirPath(buildDateUploadID("20250625")))
	assert.Equal(t, "20250625/2025062512", store.GetFileDirPath(buildDateHourUploadID("2025062512")))
}

func TestBinAndInfoPath(t *testing.T) {
	store := BaiduStore{ObjectPrefix: "uploads"}
	id := buildDateUploadID("20250625")

	assert.Equal(t, "uploads/20250625/"+id+".bin", store.binPath(id))
	assert.Equal(t, "uploads/20250625/"+id+".info", store.infoPath(id))
}

func TestNewUploadStorageMetadata(t *testing.T) {
	mock := &mockBaiduAPI{}
	store := BaiduStore{
		Bucket:    "bos-bucket",
		Container: "prod-videos",
		Service:   mock,
	}

	upload, err := store.NewUpload(context.Background(), handler.FileInfo{
		ID:   "upload-id",
		Size: 100,
	})
	require.NoError(t, err)

	info, err := upload.GetInfo(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "baidustore", info.Storage["Type"])
	assert.Equal(t, "bos-bucket", info.Storage["Bucket"])
	assert.Equal(t, "prod-videos", info.Storage["Container"])
	assert.Equal(t, "upload-id.bin", info.Storage["Key"])

	require.Len(t, mock.putObjectCalls, 1)
	assert.Equal(t, "upload-id.info", mock.putObjectCalls[0].ID)
}

func TestGetUploadNotFound(t *testing.T) {
	mock := &mockBaiduAPI{
		readObjectFn: func(ctx context.Context, params BaiduObjectParams) (BaiduReader, error) {
			return nil, bce.NewBceServiceError("NoSuchKey", "not found", "req", http.StatusNotFound)
		},
	}
	store := BaiduStore{Bucket: "bos-bucket", Service: mock}

	_, err := store.GetUpload(context.Background(), "missing-id")
	assert.ErrorIs(t, err, handler.ErrNotFound)
}

func TestGetUploadReadsInfo(t *testing.T) {
	want := handler.FileInfo{
		ID:     "upload-id",
		Size:   42,
		Offset: 10,
		Storage: map[string]string{
			"Type": "baidustore",
		},
	}
	payload, err := json.Marshal(want)
	require.NoError(t, err)

	mock := &mockBaiduAPI{
		readObjectFn: func(ctx context.Context, params BaiduObjectParams) (BaiduReader, error) {
			return &BaiduBOSReader{
				reader:      io.NopCloser(bytes.NewReader(payload)),
				contentType: "application/json",
				size:        int64(len(payload)),
				remain:      int64(len(payload)),
			}, nil
		},
	}
	store := BaiduStore{Bucket: "bos-bucket", Service: mock}

	upload, err := store.GetUpload(context.Background(), "upload-id")
	require.NoError(t, err)

	info, err := upload.GetInfo(context.Background())
	require.NoError(t, err)
	assert.Equal(t, want.ID, info.ID)
	assert.Equal(t, want.Size, info.Size)
	assert.Equal(t, want.Offset, info.Offset)
}

func TestServeContentPartialNoCache(t *testing.T) {
	const body = "hello"
	mock := &mockBaiduAPI{
		getObjectFn: func(ctx context.Context, params BaiduObjectParams, reqHeaders *http.Header) (http.Header, io.ReadCloser, error) {
			h := http.Header{}
			h.Set("Content-Range", "bytes 0-4/5")
			h.Set("Content-Length", "5")
			// BOS default for *.bin; must not override upload metadata headers.
			h.Set("Content-Type", "application/octet-stream")
			h.Set("Content-Disposition", "attachment")
			h.Set("Accept-Ranges", "bytes")
			return h, io.NopCloser(bytes.NewReader([]byte(body))), nil
		},
	}
	store := BaiduStore{Bucket: "bos-bucket", Service: mock}
	upload := &baiduUpload{
		id:    "upload-id",
		store: &store,
		info: &handler.FileInfo{
			ID: "upload-id",
			MetaData: map[string]string{
				"filetype": "video/mp4",
				"filename": "demo.mp4",
			},
		},
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Range", "bytes=0-4")

	err := upload.ServeContent(context.Background(), w, r)
	require.NoError(t, err)

	assert.Equal(t, http.StatusPartialContent, w.Code)
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	assert.Equal(t, "no-cache", w.Header().Get("Pragma"))
	assert.Empty(t, w.Header().Get("ETag"))
	assert.Equal(t, "bytes 0-4/5", w.Header().Get("Content-Range"))
	assert.Equal(t, "video/mp4", w.Header().Get("Content-Type"))
	assert.Equal(t, `inline;filename="demo.mp4"`, w.Header().Get("Content-Disposition"))
	assert.Equal(t, body, w.Body.String())
}

func TestServeContentUsesMetadataForImageAndZip(t *testing.T) {
	tests := []struct {
		name               string
		filetype           string
		filename           string
		wantType           string
		wantDisposition    string
	}{
		{
			name:            "image inline",
			filetype:        "image/jpeg",
			filename:        "photo.jpg",
			wantType:        "image/jpeg",
			wantDisposition: `inline;filename="photo.jpg"`,
		},
		{
			name:            "zip attachment",
			filetype:        "application/zip",
			filename:        "archive.zip",
			wantType:        "application/zip",
			wantDisposition: `attachment;filename="archive.zip"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const body = "data"
			mock := &mockBaiduAPI{
				getObjectFn: func(ctx context.Context, params BaiduObjectParams, reqHeaders *http.Header) (http.Header, io.ReadCloser, error) {
					h := http.Header{}
					h.Set("Content-Length", strconv.Itoa(len(body)))
					h.Set("Content-Type", "application/octet-stream")
					h.Set("Accept-Ranges", "bytes")
					return h, io.NopCloser(bytes.NewReader([]byte(body))), nil
				},
			}
			upload := &baiduUpload{
				id:    "upload-id",
				store: &BaiduStore{Bucket: "bos-bucket", Service: mock},
				info: &handler.FileInfo{
					ID: "upload-id",
					MetaData: map[string]string{
						"filetype": tt.filetype,
						"filename": tt.filename,
					},
				},
			}

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			err := upload.ServeContent(context.Background(), w, r)
			require.NoError(t, err)

			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, tt.wantType, w.Header().Get("Content-Type"))
			assert.Equal(t, tt.wantDisposition, w.Header().Get("Content-Disposition"))
			assert.Equal(t, body, w.Body.String())
		})
	}
}

func TestServeContentNotFound(t *testing.T) {
	mock := &mockBaiduAPI{
		getObjectFn: func(ctx context.Context, params BaiduObjectParams, reqHeaders *http.Header) (http.Header, io.ReadCloser, error) {
			return nil, nil, bce.NewBceServiceError("NoSuchKey", "not found", "req", http.StatusNotFound)
		},
	}
	store := BaiduStore{Bucket: "bos-bucket", Service: mock}
	upload := &baiduUpload{
		id: "upload-id",
		store: &store,
		info: &handler.FileInfo{ID: "upload-id"},
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	err := upload.ServeContent(context.Background(), w, r)
	require.Error(t, err)
	assert.Equal(t, "not found file", err.Error())
}

func TestServeContentRangeNotSatisfiable(t *testing.T) {
	mock := &mockBaiduAPI{
		getObjectFn: func(ctx context.Context, params BaiduObjectParams, reqHeaders *http.Header) (http.Header, io.ReadCloser, error) {
			return nil, nil, bce.NewBceServiceError("InvalidRange", "range not satisfiable", "req", http.StatusRequestedRangeNotSatisfiable)
		},
	}
	store := BaiduStore{Bucket: "bos-bucket", Service: mock}
	upload := &baiduUpload{
		id: "upload-id",
		store: &store,
		info: &handler.FileInfo{ID: "upload-id"},
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Range", "bytes=999-")

	err := upload.ServeContent(context.Background(), w, r)
	require.NoError(t, err)
	assert.Equal(t, http.StatusRequestedRangeNotSatisfiable, w.Code)
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	assert.Equal(t, "bytes", w.Header().Get("Accept-Ranges"))
}
