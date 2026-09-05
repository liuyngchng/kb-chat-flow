package kb

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"kb-chat-flow/internal/model"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3FileStore S3 兼容对象存储实现（MinIO / AWS S3 / 阿里云 OSS）。
type S3FileStore struct {
	client *minio.Client
	bucket string
}

// NewS3FileStore 创建 S3 文件存储。
// cfg.OSS.Endpoint 为空时返回错误。
func NewS3FileStore(cfg *model.Config) (*S3FileStore, error) {
	if cfg.OSS.Endpoint == "" {
		return nil, fmt.Errorf("OSS endpoint is empty (required in cluster mode)")
	}

	opts := &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.OSS.AccessKey, cfg.OSS.SecretKey, ""),
		Secure: false,
	}

	// 阿里云 OSS 强制 HTTPS
	if cfg.OSS.Type == "aliyun" {
		opts.Secure = true
	}

	client, err := minio.New(cfg.OSS.Endpoint, opts)
	if err != nil {
		return nil, fmt.Errorf("create S3 client failed: %w", err)
	}

	// 确保 bucket 存在
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	exists, err := client.BucketExists(ctx, cfg.OSS.Bucket)
	if err != nil {
		return nil, fmt.Errorf("check bucket failed: %w", err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, cfg.OSS.Bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("create bucket failed: %w", err)
		}
		slog.Info("s3_bucket_created", "bucket", cfg.OSS.Bucket)
	}

	slog.Info("s3_file_store_ready", "endpoint", cfg.OSS.Endpoint, "bucket", cfg.OSS.Bucket, "type", cfg.OSS.Type)
	return &S3FileStore{client: client, bucket: cfg.OSS.Bucket}, nil
}

// Save 上传文件到 S3
func (s *S3FileStore) Save(path string, reader io.Reader) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	info, err := s.client.PutObject(ctx, s.bucket, path, reader, -1,
		minio.PutObjectOptions{ContentType: "application/octet-stream"})
	if err != nil {
		return 0, fmt.Errorf("S3 put object failed: %w", err)
	}
	return info.Size, nil
}

// ReadAll 从 S3 读取文件完整内容
func (s *S3FileStore) ReadAll(path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	obj, err := s.client.GetObject(ctx, s.bucket, path, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("S3 get object failed: %w", err)
	}
	defer obj.Close()

	return io.ReadAll(obj)
}

// Delete 从 S3 删除文件
func (s *S3FileStore) Delete(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return s.client.RemoveObject(ctx, s.bucket, path, minio.RemoveObjectOptions{})
}

// Exists 检查文件在 S3 中是否存在
func (s *S3FileStore) Exists(path string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := s.client.StatObject(ctx, s.bucket, path, minio.StatObjectOptions{})
	if err != nil {
		errResp := minio.ToErrorResponse(err)
		if errResp.Code == "NoSuchKey" {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// MkdirAll S3 不需要创建目录（key 自带路径），空操作
func (s *S3FileStore) MkdirAll(path string) error {
	return nil
}

// Open 从 S3 打开文件返回 io.ReadCloser
func (s *S3FileStore) Open(path string) (io.ReadCloser, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	obj, err := s.client.GetObject(ctx, s.bucket, path, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("S3 get object failed: %w", err)
	}
	return obj, nil
}

// DownloadToTemp 将 S3 文件下载到本地临时文件，返回路径和清理函数。
// 用于 PDF/DOCX/XLSX 解析时需要本地文件路径的场景。
func (s *S3FileStore) DownloadToTemp(path string) (string, func(), error) {
	data, err := s.ReadAll(path)
	if err != nil {
		return "", nil, err
	}

	// 在 upload_doc 目录创建临时文件
	if err := os.MkdirAll(UploadDir, 0755); err != nil {
		return "", nil, fmt.Errorf("mkdir for temp file: %w", err)
	}

	tmpFile, err := os.CreateTemp(UploadDir, "kb_s3_dl_*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp file: %w", err)
	}

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", nil, fmt.Errorf("write temp file: %w", err)
	}
	tmpFile.Close()

	cleanup := func() {
		if err := os.Remove(tmpFile.Name()); err != nil {
			slog.Warn("s3_cleanup_temp_file_failed", "path", tmpFile.Name(), "error", err)
		}
	}

	return tmpFile.Name(), cleanup, nil
}
