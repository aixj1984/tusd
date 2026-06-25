package baidustore

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"testing"

	"github.com/baidubce/bce-sdk-go/bce"
	my_http "github.com/baidubce/bce-sdk-go/http"
	"github.com/baidubce/bce-sdk-go/services/bos"
	"github.com/baidubce/bce-sdk-go/services/bos/api"
	"github.com/baidubce/bce-sdk-go/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testBucket = "test-bucket"

func newMockBaiduService(t *testing.T, opts ...util.MockRoundTripperOption) *BaiduService {
	t.Helper()

	config := bos.NewBosClientConfig("ak", "sk", "http://bos.example.com")
	mockHTTP := util.NewMockHTTPClient(opts...)
	config = config.WithHttpClient(*mockHTTP)

	client, err := bos.NewClientWithConfig(config)
	require.NoError(t, err)

	return &BaiduService{
		Client:     client,
		BucketName: testBucket,
	}
}

func TestParseHTTPRangeForBOS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		header    string
		totalSize int64
		want      []int64
		wantErr   bool
	}{
		{name: "empty", header: "", totalSize: 100, want: nil},
		{name: "closed range", header: "bytes=0-4", totalSize: 100, want: []int64{0, 4}},
		{name: "open end", header: "bytes=10-", totalSize: 100, want: []int64{10}},
		{name: "suffix", header: "bytes=-5", totalSize: 100, want: []int64{95, 99}},
		{name: "suffix longer than file", header: "bytes=-200", totalSize: 100, want: []int64{0, 99}},
		{name: "unsupported unit", header: "items=0-1", totalSize: 100, wantErr: true},
		{name: "suffix without size", header: "bytes=-5", totalSize: 0, wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseHTTPRangeForBOS(tt.header, tt.totalSize)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestObjectMetaToHTTPHeader(t *testing.T) {
	meta := api.ObjectMeta{
		ContentDisposition: `attachment; filename="video.mp4"`,
		ContentLength:      42,
		ContentRange:       "bytes 0-41/42",
		ContentType:        "video/mp4",
		CacheControl:       "max-age=3600",
		ETag:               `"etag"`,
	}

	h := objectMetaToHTTPHeader(meta)

	assert.Equal(t, `attachment; filename="video.mp4"`, h.Get("Content-Disposition"))
	assert.Equal(t, "42", h.Get("Content-Length"))
	assert.Equal(t, "bytes 0-41/42", h.Get("Content-Range"))
	assert.Equal(t, "video/mp4", h.Get("Content-Type"))
	assert.Equal(t, "bytes", h.Get("Accept-Ranges"))
	assert.Empty(t, h.Get("Cache-Control"))
	assert.Empty(t, h.Get("ETag"))
}

func TestIsNotFound(t *testing.T) {
	assert.True(t, isNotFound(bce.NewBceServiceError("NoSuchKey", "not found", "req", http.StatusNotFound)))
	assert.True(t, isNotFound(bce.NewBceServiceError("NOTFound", "404", "req", http.StatusNotFound)))
	assert.False(t, isNotFound(bce.NewBceServiceError("AccessDenied", "denied", "req", http.StatusForbidden)))
	assert.False(t, isNotFound(io.EOF))
}

func TestNewBaiduServiceBucketExists(t *testing.T) {
	config := bos.NewBosClientConfig("ak", "sk", "http://bos.example.com")
	mockHTTP := util.NewMockHTTPClient(util.SetStatusCode(http.StatusOK))
	config = config.WithHttpClient(*mockHTTP)
	client, err := bos.NewClientWithConfig(config)
	require.NoError(t, err)

	exists, err := client.DoesBucketExist(testBucket)
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestNewBaiduServiceBucketMissing(t *testing.T) {
	config := bos.NewBosClientConfig("ak", "sk", "http://bos.example.com")
	mockHTTP := util.NewMockHTTPClient(util.RoundTripperOpts404...)
	config = config.WithHttpClient(*mockHTTP)
	client, err := bos.NewClientWithConfig(config)
	require.NoError(t, err)

	exists, err := client.DoesBucketExist(testBucket)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestBaiduServiceGetObjectSize(t *testing.T) {
	service := newMockBaiduService(t,
		util.SetStatusCode(http.StatusOK),
		util.AddHeaders(map[string]string{
			http.CanonicalHeaderKey(my_http.CONTENT_LENGTH): "12345",
		}),
	)

	size, err := service.GetObjectSize(context.Background(), BaiduObjectParams{ID: "video.bin"})
	require.NoError(t, err)
	assert.Equal(t, int64(12345), size)
}

func TestBaiduServiceReadObject(t *testing.T) {
	const respBody = "hello baidu bos"
	service := newMockBaiduService(t,
		util.SetStatusCode(http.StatusOK),
		util.AppendRespBody([]string{"", respBody}),
		util.AddHeaders(map[string]string{
			http.CanonicalHeaderKey(my_http.CONTENT_LENGTH): strconv.Itoa(len(respBody)),
			http.CanonicalHeaderKey(my_http.CONTENT_TYPE):   "video/mp4",
		}),
	)

	reader, err := service.ReadObject(context.Background(), BaiduObjectParams{ID: "video.bin"})
	require.NoError(t, err)
	defer reader.Close()

	assert.Equal(t, int64(len(respBody)), reader.Size())
	assert.Equal(t, "video/mp4", reader.ContentType())

	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, respBody, string(data))
}

func TestBaiduServiceGetObjectWithRange(t *testing.T) {
	const fullSize = 11
	const partialBody = "hello"
	service := newMockBaiduService(t,
		util.SetStatusCode(http.StatusOK),
		util.AppendRespBody([]string{"", partialBody}),
		util.AddHeaders(map[string]string{
			http.CanonicalHeaderKey(my_http.CONTENT_LENGTH):    strconv.Itoa(fullSize),
			http.CanonicalHeaderKey(my_http.CONTENT_RANGE):   "bytes 0-4/11",
			http.CanonicalHeaderKey(my_http.CONTENT_TYPE):      "text/plain",
			http.CanonicalHeaderKey(my_http.CACHE_CONTROL):     "max-age=3600",
			http.CanonicalHeaderKey(my_http.ETAG):              `"etag-from-bos"`,
		}),
	)

	reqHeaders := http.Header{}
	reqHeaders.Set("Range", "bytes=0-4")

	headers, body, err := service.GetObject(context.Background(), BaiduObjectParams{ID: "video.bin"}, &reqHeaders)
	require.NoError(t, err)
	defer body.Close()

	assert.Equal(t, "bytes 0-4/11", headers.Get("Content-Range"))
	assert.Equal(t, "text/plain", headers.Get("Content-Type"))
	assert.Equal(t, "bytes", headers.Get("Accept-Ranges"))
	assert.Empty(t, headers.Get("Cache-Control"))
	assert.Empty(t, headers.Get("ETag"))

	got, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, partialBody, string(got))
}

func TestBaiduServiceWriteObjectAppend(t *testing.T) {
	service := newMockBaiduService(t,
		util.AppendStatusCode([]int{http.StatusNotFound, http.StatusOK}),
		util.AppendRespBody([]string{
			`{"code":"NoSuchKey","message":"not found","requestId":"req-1"}`,
			"",
		}),
		util.AddHeaders(map[string]string{
			http.CanonicalHeaderKey(my_http.BCE_NEXT_APPEND_OFFSET): "3",
		}),
	)

	nextPos, err := service.WriteObject(context.Background(), BaiduObjectParams{ID: "upload.bin"}, bytes.NewReader([]byte("abc")), 0)
	require.NoError(t, err)
	assert.Equal(t, int64(3), nextPos)
}

func TestBaiduServicePutAndDeleteObject(t *testing.T) {
	service := newMockBaiduService(t, util.SetStatusCode(http.StatusOK))

	err := service.PutObject(context.Background(), BaiduObjectParams{ID: "meta.info"}, bytes.NewReader([]byte(`{"ID":"x"}`)))
	require.NoError(t, err)

	err = service.DeleteObject(context.Background(), BaiduObjectParams{ID: "meta.info"})
	require.NoError(t, err)
}

func TestBaiduServiceGetObjectPos(t *testing.T) {
	service := newMockBaiduService(t,
		util.SetStatusCode(http.StatusOK),
		util.AddHeaders(map[string]string{
			http.CanonicalHeaderKey(my_http.BCE_NEXT_APPEND_OFFSET): "8192",
		}),
	)

	pos, err := service.GetObjectPos(context.Background(), BaiduObjectParams{ID: "upload.bin"})
	require.NoError(t, err)
	assert.Equal(t, int64(8192), pos)
}
