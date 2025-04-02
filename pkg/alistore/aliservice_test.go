package alistore_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	. "github.com/tus/tusd/v2/pkg/alistore"
	"gopkg.in/h2non/gock.v1"
)

type googleObjectResponse struct {
	Name string `json:"name"`
}

type googleBucketResponse struct {
	Items []googleObjectResponse `json:"items"`
}

func TestGetObjectSize(t *testing.T) {
	defer gock.Off()

	// gock.New("https://storage.googleapis.com").
	// 	Get("/storage/v1/b/test-bucket/o/test-name").
	// 	MatchParam("alt", "json").
	// 	MatchParam("projection", "full").
	// 	Reply(200).
	// 	JSON(map[string]string{"size": "54321"})

	// gock.New("https://accounts.google.com/").
	// 	Post("/o/oauth2/token").Reply(200).JSON(map[string]string{
	// 	"access_token":  "H3l5321N123sdI4HLY/RF39FjrCRF39FjrCRF39FjrCRF39FjrC_RF39FjrCRF39FjrC",
	// 	"token_type":    "Bearer",
	// 	"refresh_token": "1/smWJksmWJksmWJksmWJksmWJk_smWJksmWJksmWJksmWJksmWJk",
	// 	"expiry_date":   "1425333671141",
	// })

	ctx := context.Background()
	// We need to explicit configure the Ali client to use the default HTTP client
	// or otherwise gock cannot intercept the HTTP requests.
	// client, err := storage.NewClient(ctx, option.WithHTTPClient(http.DefaultClient), option.WithAPIKey("foo"))
	// if err != nil {
	// 	t.Fatal(err)
	// 	return
	// }

	config := &AliConfig{
		Endpoint:        "https://oss-cn-hangzhou.aliyuncs.com",
		AccessKeyId:     "xxxxx",
		AccessKeySecret: "xxx",
		BucketName:      "xxx",
		RegionId:        "hangzhou",
	}

	// New client
	client, err := oss.New(config.Endpoint, config.AccessKeyId, config.AccessKeySecret)
	if err != nil {
		t.Errorf("Error: %v", err)
		return
	}

	// Create bucket
	err = client.CreateBucket(config.BucketName)
	if err != nil {
		t.Errorf("Error: %v", err)
		return
	}

	// Get bucket
	bucket, err := client.Bucket(config.BucketName)
	if err != nil {
		t.Errorf("Error: %v", err)
		return
	}

	service := AliService{
		Client: bucket,
	}

	size, err := service.GetObjectSize(ctx, AliObjectParams{
		Bucket: config.BucketName,
		ID:     "test-name",
	})
	if err != nil {
		t.Errorf("Error: %v", err)
		return
	}

	if size != 54321 {
		t.Errorf("Error: Did not match given size")
		return
	}
}

func TestReadObject(t *testing.T) {
	ctx := context.Background()
	defer gock.Off()

	config := &AliConfig{
		Endpoint:        "https://oss-cn-hangzhou.aliyuncs.com",
		AccessKeyId:     "xxxxx",
		AccessKeySecret: "xxx",
		BucketName:      "xxx",
		RegionId:        "hangzhou",
	}

	// New client
	client, err := oss.New(config.Endpoint, config.AccessKeyId, config.AccessKeySecret)
	if err != nil {
		t.Errorf("Error: %v", err)
		return
	}

	// Create bucket
	// err = client.CreateBucket(config.BucketName)
	// if err != nil {
	// 	t.Errorf("Error: %v", err)
	// 	return
	// }

	// Get bucket
	bucket, err := client.Bucket(config.BucketName)
	if err != nil {
		t.Errorf("Error: %v", err)
		return
	}

	service := AliService{
		Client: bucket,
	}

	reader, err := service.ReadObject(ctx, AliObjectParams{
		Bucket: config.BucketName,
		ID:     "test-name",
	})
	if err != nil {
		t.Fatal(err)
		return
	}

	if reader.Size() != 30 {
		t.Errorf("Object size does not match expected value: %+v", reader)
	}
}

func TestDeleteObject(t *testing.T) {
	ctx := context.Background()
	defer gock.Off()

	config := &AliConfig{
		Endpoint:        "https://oss-cn-hangzhou.aliyuncs.com",
		AccessKeyId:     "xxxxx",
		AccessKeySecret: "xxx",
		BucketName:      "xxx",
		RegionId:        "hangzhou",
	}

	// New client
	client, err := oss.New(config.Endpoint, config.AccessKeyId, config.AccessKeySecret)
	if err != nil {
		t.Errorf("Error: %v", err)
		return
	}

	// Create bucket
	err = client.CreateBucket(config.BucketName)
	if err != nil {
		t.Errorf("Error: %v", err)
		return
	}

	// Get bucket
	bucket, err := client.Bucket(config.BucketName)
	if err != nil {
		t.Errorf("Error: %v", err)
		return
	}

	service := AliService{
		Client: bucket,
	}
	err = service.DeleteObject(ctx, AliObjectParams{
		Bucket: config.BucketName,
		ID:     "test-name",
	})
	if err != nil {
		t.Errorf("Error deleting object: %+v", err)
	}
}

func TestWriteObject(t *testing.T) {
	ctx := context.Background()
	defer gock.Off()

	config := &AliConfig{
		Endpoint:        "https://oss-cn-hangzhou.aliyuncs.com",
		AccessKeyId:     "xxxxx",
		AccessKeySecret: "xxx",
		BucketName:      "xxx",
		RegionId:        "hangzhou",
	}

	// New client
	client, err := oss.New(config.Endpoint, config.AccessKeyId, config.AccessKeySecret)
	if err != nil {
		t.Errorf("Error: %v", err)
		return
	}

	// Get bucket
	bucket, err := client.Bucket(config.BucketName)
	if err != nil {
		t.Errorf("Error: %v", err)
		return
	}

	service := AliService{
		Client: bucket,
	}
	reader := bytes.NewReader([]byte{'1'})

	size, err := service.WriteObject(ctx, AliObjectParams{
		Bucket: config.BucketName,
		ID:     "test-name2",
	}, reader, 0)
	if err != nil {
		t.Errorf("Error writing object: %+v", err)
	}

	if size != 1 {
		t.Errorf("Mismatch of object size: %v", size)
	}
}

func TestPutObject(t *testing.T) {
	ctx := context.Background()
	defer gock.Off()

	config := &AliConfig{
		Endpoint:        "https://oss-cn-hangzhou.aliyuncs.com",
		AccessKeyId:     "xxxxx",
		AccessKeySecret: "xxx",
		BucketName:      "xxx",
		RegionId:        "hangzhou",
	}

	// New client
	client, err := oss.New(config.Endpoint, config.AccessKeyId, config.AccessKeySecret)
	if err != nil {
		t.Errorf("Error: %v", err)
		return
	}

	// Get bucket
	bucket, err := client.Bucket(config.BucketName)
	if err != nil {
		t.Errorf("Error: %v", err)
		return
	}

	service := AliService{
		Client: bucket,
	}
	reader := bytes.NewReader([]byte{'1'})

	err = service.PutObject(ctx, AliObjectParams{
		Bucket: config.BucketName,
		ID:     "test-name-put",
	}, reader)
	if err != nil {
		t.Errorf("Error writing object: %+v", err)
	}
}
