package alistore

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"

	"github.com/tus/tusd/v2/pkg/handler"
)

//go:generate mockgen -destination=./gcsstore_mock_test.go -package=gcsstore_test github.com/tus/tusd/v2/pkg/alistore AliReader,AliAPI

const (
	mockID         = "123456789abcdefghijklmnopqrstuvwxyz"
	mockBucket     = "meloota"
	mockSize       = 1337
	mockReaderData = "helloworld"
)

var mockConfig = &AliConfig{
	Endpoint:        "https://oss-cn-hangzhou.aliyuncs.com",
	AccessKeyId:     "xxxxx",
	AccessKeySecret: "xxx",
	BucketName:      "xxx",
	RegionId:        "hangzhou",
}

var (
	mockTusdInfoJson = fmt.Sprintf(`{"ID":"%s","Size":%d,"MetaData":{"foo":"bar"},"Storage":{"Bucket":"bucket","Key":"%s","Type":"alistore"}}`, mockID, mockSize, mockID)
	mockTusdInfo     = handler.FileInfo{
		ID:   mockID,
		Size: mockSize,
		MetaData: map[string]string{
			"foo": "bar",
		},
		Storage: map[string]string{
			"Type":   "alistore",
			"Bucket": mockBucket,
			"Key":    mockID,
		},
	}
)

var (
	mockPartial0 = fmt.Sprintf("%s_0", mockID)
	mockPartial1 = fmt.Sprintf("%s_1", mockID)
	mockPartial2 = fmt.Sprintf("%s_2", mockID)
	mockPartials = []string{mockPartial0, mockPartial1, mockPartial2}
)

func TestNewUpload(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	assert := assert.New(t)

	service, err := NewAliService(mockConfig)
	assert.Nil(err)
	store := New(mockBucket, service)

	assert.Equal(store.Bucket, mockBucket)

	// data, err := json.Marshal(mockTusdInfo)
	// assert.Nil(err)

	// r := bytes.NewReader(data)

	// params := AliObjectParams{
	// 	Bucket: store.Bucket,
	// 	ID:     fmt.Sprintf("%s.info", mockID),
	// }

	// ctx := context.Background()
	// service.WriteObject(ctx, params, r, 0)

	upload, err := store.NewUpload(context.Background(), mockTusdInfo)
	fmt.Printf("%+v\n", upload)
}

// MockReader is an implementation of AliReader.
type MockReader struct {
	reader *bytes.Reader
}

func (r MockReader) Close() error {
	return nil
}

func (r MockReader) ContentType() string {
	return "text/plain; charset=utf-8"
}

func (r MockReader) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

func (r MockReader) Remain() int64 {
	return int64(r.reader.Len())
}

func (r MockReader) Size() int64 {
	return r.reader.Size()
}

func TestGetInfo(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	assert := assert.New(t)
	service, err := NewAliService(mockConfig)
	assert.Nil(err)
	store := New(mockBucket, service)
	params := AliObjectParams{
		Bucket: store.Bucket,
		ID:     mockID,
	}

	upload, err := store.GetUpload(context.Background(), params.ID)
	assert.Nil(err)

	info, err := upload.GetInfo(context.Background())
	assert.Nil(err)
	assert.Equal(mockTusdInfo, info)
}

func TestGetInfoNotFound(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	assert := assert.New(t)

	service, err := NewAliService(mockConfig)
	assert.Nil(err)
	store := New(mockBucket, service)
	params := AliObjectParams{
		Bucket: store.Bucket,
		ID:     fmt.Sprintf("%s.info", mockID),
	}

	upload, err := store.GetUpload(context.Background(), params.ID)
	assert.Nil(err)

	_, err = upload.GetInfo(context.Background())
	assert.Equal(handler.ErrNotFound, err)
}

func TestGetReader(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	assert := assert.New(t)
	service, err := NewAliService(mockConfig)
	assert.Nil(err)
	store := New(mockBucket, service)
	params := AliObjectParams{
		Bucket: store.Bucket,
		ID:     fmt.Sprintf("%s.info", mockID),
	}

	ctx := context.Background()
	service.ReadObject(ctx, params)

	upload, err := store.GetUpload(context.Background(), mockID)
	assert.Nil(err)

	reader, err := upload.GetReader(context.Background())
	assert.Nil(err)

	buf := make([]byte, len(mockReaderData))
	_, err = reader.Read(buf)

	assert.Nil(err)
	assert.Equal(mockReaderData, string(buf[:]))
}

func TestWriteChunk(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	assert := assert.New(t)

	service, err := NewAliService(mockConfig)
	assert.Nil(err)
	store := New(mockBucket, service)
	params := AliObjectParams{
		Bucket: store.Bucket,
		ID:     fmt.Sprintf("%s.info", mockID),
	}

	// write object
	writeObjectParams := AliObjectParams{
		Bucket: store.Bucket,
		ID:     mockPartial1,
	}

	rGet := bytes.NewReader([]byte(mockReaderData))

	ctx := context.Background()
	service.WriteObject(ctx, writeObjectParams, rGet, 0)
	upload, err := store.GetUpload(context.Background(), params.ID)
	assert.Nil(err)

	reader := bytes.NewReader([]byte(mockReaderData))
	var offset int64 = mockSize / 3

	_, err = upload.WriteChunk(context.Background(), offset, reader)
	assert.Nil(err)
}
