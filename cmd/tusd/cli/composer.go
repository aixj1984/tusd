package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/exp/slog"

	"github.com/tus/tusd/v2/internal/s3log"
	"github.com/tus/tusd/v2/pkg/alistore"
	"github.com/tus/tusd/v2/pkg/azurestore"
	"github.com/tus/tusd/v2/pkg/baidustore"
	"github.com/tus/tusd/v2/pkg/filelocker"
	"github.com/tus/tusd/v2/pkg/filestore"
	"github.com/tus/tusd/v2/pkg/gcsstore"
	"github.com/tus/tusd/v2/pkg/handler"
	"github.com/tus/tusd/v2/pkg/memorylocker"
	"github.com/tus/tusd/v2/pkg/s3store"
	"github.com/tus/tusd/v2/pkg/txstore"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/prometheus/client_golang/prometheus"
)

var Composer *handler.StoreComposer

func CreateComposer() {
	// Attempt to use S3 as a backend if the -s3-bucket option has been supplied.
	// If not, we default to storing them locally on disk.
	Composer = handler.NewStoreComposer()
	if Flags.S3Bucket != "" {
		// Derive credentials from default credential chain (env, shared, ec2 instance role)
		// as per https://github.com/aws/aws-sdk-go#configuring-credentials
		s3Config, err := config.LoadDefaultConfig(context.Background())
		if err != nil {
			stderr.Fatalf("Unable to load S3 configuration: %s", err)
		}

		if Flags.S3Endpoint == "" {
			if Flags.S3TransferAcceleration {
				printStartupLog("Using 's3://%s' as S3 bucket for storage with AWS S3 Transfer Acceleration enabled.\n", Flags.S3Bucket)
			} else {
				printStartupLog("Using 's3://%s' as S3 bucket for storage.\n", Flags.S3Bucket)
			}
		} else {
			printStartupLog("Using '%s/%s' as S3 endpoint and bucket for storage.\n", Flags.S3Endpoint, Flags.S3Bucket)
		}

		var s3Client s3store.S3API
		s3Client = s3.NewFromConfig(s3Config, func(o *s3.Options) {
			o.UseAccelerate = Flags.S3TransferAcceleration

			// Disable HTTPS and only use HTTP (helpful for debugging requests).
			o.EndpointOptions.DisableHTTPS = Flags.S3DisableSSL

			if Flags.S3Endpoint != "" {
				o.BaseEndpoint = &Flags.S3Endpoint
				o.UsePathStyle = true
			}
		})

		if Flags.S3LogAPICalls {
			if !Flags.VerboseOutput {
				stderr.Fatalf("The -s3-log-api-calls flag requires verbose mode (-verbose) to be enabled")
			}
			s3Client = s3log.New(s3Client, slog.Default())
		}

		store := s3store.New(Flags.S3Bucket, s3Client)
		store.ObjectPrefix = Flags.S3ObjectPrefix
		store.PreferredPartSize = Flags.S3PartSize
		store.MinPartSize = Flags.S3MinPartSize
		store.MaxBufferedParts = Flags.S3MaxBufferedParts
		store.DisableContentHashes = Flags.S3DisableContentHashes
		store.SetConcurrentPartUploads(Flags.S3ConcurrentPartUploads)
		store.UseIn(Composer)

		locker := memorylocker.New()
		locker.UseIn(Composer)

		// Attach the metrics from S3 store to the global Prometheus registry
		store.RegisterMetrics(prometheus.DefaultRegisterer)
	} else if Flags.GCSBucket != "" {
		if Flags.GCSObjectPrefix != "" && strings.Contains(Flags.GCSObjectPrefix, "_") {
			stderr.Fatalf("gcs-object-prefix value (%s) can't contain underscore. "+
				"Please remove underscore from the value", Flags.GCSObjectPrefix)
		}

		// Application Default Credentials discovery mechanism is attempted to fetch credentials,
		// but an account file can be provided through the GCS_SERVICE_ACCOUNT_FILE environment variable.
		gcsSAF := os.Getenv("GCS_SERVICE_ACCOUNT_FILE")

		service, err := gcsstore.NewGCSService(gcsSAF)
		if err != nil {
			stderr.Fatalf("Unable to create Google Cloud Storage service: %s\n", err)
		}

		printStartupLog("Using 'gcs://%s' as GCS bucket for storage.\n", Flags.GCSBucket)

		store := gcsstore.New(Flags.GCSBucket, service)
		store.ObjectPrefix = Flags.GCSObjectPrefix
		store.UseIn(Composer)

		locker := memorylocker.New()
		locker.UseIn(Composer)
	} else if Flags.AzStorage != "" {

		accountName := os.Getenv("AZURE_STORAGE_ACCOUNT")
		if accountName == "" {
			stderr.Fatalf("No service account name for Azure BlockBlob Storage using the AZURE_STORAGE_ACCOUNT environment variable.\n")
		}

		accountKey := os.Getenv("AZURE_STORAGE_KEY")
		if accountKey == "" {
			printStartupLog("Azure BlockBlob Storage authentication using identity")
		} else {
			printStartupLog("Azure BlockBlob Storage authentication using account key")
		}

		azureEndpoint := Flags.AzEndpoint
		// Enables support for using Azurite as a storage emulator without messing with proxies and stuff
		// e.g. http://127.0.0.1:10000/devstoreaccount1
		if azureEndpoint == "" {
			azureEndpoint = fmt.Sprintf("https://%s.blob.core.windows.net", accountName)
		}
		printStartupLog("Using Azure endpoint %s.\n", azureEndpoint)

		azConfig := &azurestore.AzConfig{
			AccountName:         accountName,
			AccountKey:          accountKey,
			ContainerName:       Flags.AzStorage,
			ContainerAccessType: Flags.AzContainerAccessType,
			BlobAccessTier:      Flags.AzBlobAccessTier,
			Endpoint:            azureEndpoint,
		}

		azService, err := azurestore.NewAzureService(azConfig)
		if err != nil {
			stderr.Fatalf("Unable to create Azure BlockBlob Storage service: %s", err)
		}

		store := azurestore.New(azService)
		store.ObjectPrefix = Flags.AzObjectPrefix
		store.Container = Flags.AzStorage
		store.UseIn(Composer)

		locker := memorylocker.New()
		locker.UseIn(Composer)

	} else if Flags.AliStorage != "" {
		bucket := os.Getenv("ALI_BUCKET")
		if Flags.AliBucket == "" && bucket == "" {
			stderr.Fatalf("No service bucket name for Ali Oss Storage in param or  ALI_BUCKET environment variable.\n")
		}
		if Flags.AliBucket == "" {
			Flags.AliBucket = bucket
		}

		accessID := os.Getenv("ALI_ACCESS_ID")
		if Flags.AliAccessId == "" && accessID == "" {
			stderr.Fatalf("No service access name for Ali Oss Storage in param or  ALI_ACCESS_ID environment variable.\n")
		}
		if Flags.AliAccessId == "" {
			Flags.AliAccessId = accessID
		}

		accessKey := os.Getenv("ALI_ACCESS_KEY")
		if Flags.AliAccessSecret == "" && accessKey == "" {
			stderr.Fatalf("No service account key for Ali Oss Storage in param or  ALI_ACCESS_KEY environment variable.\n")
		}
		if Flags.AliAccessSecret == "" {
			Flags.AliAccessSecret = accessKey
		}

		endpoint := os.Getenv("ALI_ENDPOINT")
		// // Enables support for using ali oss as a storage emulator without messing with proxies and stuff
		// // e.g. http://oss-cn-xxxxx.aliyuncs.com
		if Flags.AliEndpoint == "" && endpoint == "" {
			stderr.Fatalf("No service endpoint  for Ali Oss Storage in param or ALI_ENDPOINT environment .\n")
		}
		if Flags.AliEndpoint == "" {
			Flags.AliEndpoint = endpoint
		}

		aliConfig := &alistore.AliConfig{
			Endpoint:        Flags.AliEndpoint,
			AccessKeyId:     Flags.AliAccessId,
			AccessKeySecret: Flags.AliAccessSecret,
			BucketName:      Flags.AliBucket,
			RegionId:        Flags.AliRegionId,
		}

		printStartupLog("Using Ali Oss endpoint %s.\n", aliConfig.Endpoint)

		aliService, err := alistore.NewAliService(aliConfig)
		if err != nil {
			stderr.Fatalf(err.Error())
		}

		store := alistore.New(aliConfig.BucketName, aliService)
		store.ObjectPrefix = Flags.AliObjectPrefix
		store.Container = Flags.AliStorage
		store.UseIn(Composer)

		locker := memorylocker.New()
		locker.UseIn(Composer)

	} else if Flags.TxStorage != "" {
		bucket := os.Getenv("TX_BUCKET")
		if Flags.TxBucket == "" && bucket == "" {
			stderr.Fatalf("No service bucket name for Tencent Oss Storage in param or  TX_BUCKET environment variable.\n")
		}
		if Flags.TxBucket == "" {
			Flags.TxBucket = bucket
		}

		accessID := os.Getenv("TX_ACCESS_ID")
		if Flags.TxAccessId == "" && accessID == "" {
			stderr.Fatalf("No service access name for Tencent Oss Storage in param or  TX_ACCESS_ID environment variable.\n")
		}
		if Flags.TxAccessId == "" {
			Flags.TxAccessId = accessID
		}

		accessKey := os.Getenv("TX_ACCESS_KEY")
		if Flags.TxAccessSecret == "" && accessKey == "" {
			stderr.Fatalf("No service account key for Tencent Oss Storage in param or  TX_ACCESS_KEY environment variable.\n")
		}
		if Flags.TxAccessSecret == "" {
			Flags.TxAccessSecret = accessKey
		}

		endpoint := os.Getenv("TX_ENDPOINT")
		// // Enables support for using tx oss as a storage emulator without messing with proxies and stuff
		// // e.g. http://oss-cn-xxxxx.aliyuncs.com
		if Flags.TxEndpoint == "" && endpoint == "" {
			stderr.Fatalf("No service endpoint  for Tencent Oss Storage in param or TX_ENDPOINT environment .\n")
		}
		if Flags.TxEndpoint == "" {
			Flags.TxEndpoint = endpoint
		}

		txConfig := &txstore.TxConfig{
			Endpoint:        Flags.TxEndpoint,
			AccessKeyId:     Flags.TxAccessId,
			AccessKeySecret: Flags.TxAccessSecret,
			BucketName:      Flags.TxBucket,
			RegionId:        Flags.TxRegionId,
		}

		printStartupLog("Using Tencent Oss endpoint %s.\n", txConfig.Endpoint)

		txService, err := txstore.NewTxService(txConfig)
		if err != nil {
			stderr.Fatalf(err.Error())
		}

		store := txstore.New(txConfig.BucketName, txService)
		store.ObjectPrefix = Flags.TxObjectPrefix
		store.Container = Flags.TxStorage
		store.UseIn(Composer)

		locker := memorylocker.New()
		locker.UseIn(Composer)
	} else if Flags.BaiduStorage != "" {
		bucket := os.Getenv("BAIDU_BUCKET")
		if Flags.BaiduBucket == "" && bucket == "" {
			stderr.Fatalf("No service bucket name for Baidu BOS in param or BAIDU_BUCKET environment variable.\n")
		}
		if Flags.BaiduBucket == "" {
			Flags.BaiduBucket = bucket
		}

		accessID := os.Getenv("BAIDU_ACCESS_ID")
		if Flags.BaiduAccessId == "" && accessID == "" {
			stderr.Fatalf("No service access id for Baidu BOS in param or BAIDU_ACCESS_ID environment variable.\n")
		}
		if Flags.BaiduAccessId == "" {
			Flags.BaiduAccessId = accessID
		}

		accessKey := os.Getenv("BAIDU_ACCESS_SECRET")
		if Flags.BaiduAccessSecret == "" && accessKey == "" {
			stderr.Fatalf("No service access secret for Baidu BOS in param or BAIDU_ACCESS_SECRET environment variable.\n")
		}
		if Flags.BaiduAccessSecret == "" {
			Flags.BaiduAccessSecret = accessKey
		}

		endpoint := os.Getenv("BAIDU_ENDPOINT")
		if Flags.BaiduEndpoint == "" && endpoint == "" {
			stderr.Fatalf("No service endpoint for Baidu BOS in param or BAIDU_ENDPOINT environment variable.\n")
		}
		if Flags.BaiduEndpoint == "" {
			Flags.BaiduEndpoint = endpoint
		}

		baiduConfig := &baidustore.BaiduConfig{
			Endpoint:        Flags.BaiduEndpoint,
			AccessKeyId:     Flags.BaiduAccessId,
			AccessKeySecret: Flags.BaiduAccessSecret,
			BucketName:      Flags.BaiduBucket,
			RegionId:        Flags.BaiduRegionId,
		}

		printStartupLog("Using Baidu BOS endpoint %s.\n", baiduConfig.Endpoint)

		baiduService, err := baidustore.NewBaiduService(baiduConfig)
		if err != nil {
			stderr.Fatalf(err.Error())
		}

		store := baidustore.New(baiduConfig.BucketName, baiduService)
		store.ObjectPrefix = Flags.BaiduObjectPrefix
		store.Container = Flags.BaiduStorage
		store.UseIn(Composer)

		locker := memorylocker.New()
		locker.UseIn(Composer)
	} else {
		dir, err := filepath.Abs(Flags.UploadDir)
		if err != nil {
			stderr.Fatalf("Unable to make absolute path: %s", err)
		}

		printStartupLog("Using '%s' as directory storage.\n", dir)

		if err := os.MkdirAll(dir, os.FileMode(0o774)); err != nil {
			stderr.Fatalf("Unable to ensure directory exists: %s", err)
		}

		store := filestore.New(dir)
		store.UseIn(Composer)

		if Flags.MemoryLocker {
			printStartupLog("Using memory locker for storage.\n")
			locker := memorylocker.New()
			locker.UseIn(Composer)
		} else {
			locker := filelocker.New(dir)
			locker.AcquirerPollInterval = Flags.FilelockAcquirerPollInterval
			locker.HolderPollInterval = Flags.FilelockHolderPollInterval
			locker.UseIn(Composer)
		}
	}

	printStartupLog("Using %.2fMB as maximum size.\n", float64(Flags.MaxSize)/1024/1024)
}
