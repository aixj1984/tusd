package txstore_test

import (
	"bytes"
	"context"
	"testing"

	. "github.com/tus/tusd/v2/pkg/txstore"
	"gopkg.in/h2non/gock.v1"
)

type googleObjectResponse struct {
	Name string `json:"name"`
}

type googleBucketResponse struct {
	Items []googleObjectResponse `json:"items"`
}

var config = &TxConfig{
	Endpoint:        "https://test-xxxx.cos.ap-shanghai.myqcloud.com",
	AccessKeyId:     "xxxx",
	AccessKeySecret: "xxxx",
	BucketName:      "test-xxx",
	RegionId:        "ap-xxxx",
}

func TestGetObjectSize(t *testing.T) {
	defer gock.Off()

	ctx := context.Background()

	// New client
	service, err := NewTxService(config)
	if err != nil {
		t.Errorf("Error: %v", err)
		return
	}

	size, err := service.GetObjectSize(ctx, TxObjectParams{
		Bucket: config.BucketName,
		ID:     "calc.png",
	})
	if err != nil {
		t.Errorf("Error: %v", err)
		return
	}

	if size != 15403 {
		t.Errorf("Object size does not match expected value: %+v", size)
	}
}

func TestReadObject(t *testing.T) {
	ctx := context.Background()
	defer gock.Off()

	// New client
	service, err := NewTxService(config)
	if err != nil {
		t.Errorf("Error: %v", err)
		return
	}
	reader, err := service.ReadObject(ctx, TxObjectParams{
		Bucket: config.BucketName,
		ID:     "calc.png",
	})
	if err != nil {
		t.Fatal(err)
		return
	}

	if reader.Size() != 15403 {
		t.Errorf("Object size does not match expected value: %d", reader.Size())
	}
}

func TestDeleteObject(t *testing.T) {
	ctx := context.Background()
	defer gock.Off()

	// New client
	service, err := NewTxService(config)
	if err != nil {
		t.Errorf("Error: %v", err)
		return
	}
	err = service.DeleteObject(ctx, TxObjectParams{
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

	// New client
	service, err := NewTxService(config)
	if err != nil {
		t.Errorf("Error: %v", err)
		return
	}
	reader := bytes.NewReader([]byte{'1'})

	size, err := service.WriteObject(ctx, TxObjectParams{
		Bucket: config.BucketName,
		ID:     "test-name3",
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

	// New client
	service, err := NewTxService(config)
	if err != nil {
		t.Errorf("Error: %v", err)
		return
	}
	reader := bytes.NewReader([]byte{'1'})

	err = service.PutObject(ctx, TxObjectParams{
		Bucket: config.BucketName,
		ID:     "test-name-put",
	}, reader)
	if err != nil {
		t.Errorf("Error writing object: %+v", err)
	}
}
