package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/s3utils"
)

// DefaultRegion is the S3 region when the config names none (MinIO's own default).
const DefaultRegion = "us-east-1"

// S3Config is how to reach an S3-compatible service (spec/server.md, "Running it").
type S3Config struct {
	// Endpoint is host:port, such as minio:9000 or s3.eu-west-1.amazonaws.com. An http:// or https:// in front is
	// accepted and then decides Insecure.
	Endpoint  string
	Bucket    string
	AccessKey string
	SecretKey string
	Region    string // "" = DefaultRegion
	Insecure  bool   // plain HTTP instead of HTTPS (a MinIO on a private network)
}

// S3 keeps objects in one bucket of an S3-compatible service.
type S3 struct {
	client *minio.Client
	bucket string
	region string
}

var _ Store = (*S3)(nil)

// NewS3 connects to the service and creates the bucket if it doesn't exist.
func NewS3(ctx context.Context, c S3Config) (*S3, error) {
	s, err := newS3(c)
	if err != nil {
		return nil, err
	}
	if err := s.ensureBucket(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// newS3 checks the config and makes the client; it doesn't touch the network.
func newS3(c S3Config) (*S3, error) {
	endpoint, secure := strings.TrimSpace(c.Endpoint), !c.Insecure
	switch {
	case strings.HasPrefix(endpoint, "https://"):
		endpoint, secure = strings.TrimPrefix(endpoint, "https://"), true
	case strings.HasPrefix(endpoint, "http://"):
		endpoint, secure = strings.TrimPrefix(endpoint, "http://"), false
	}
	endpoint = strings.TrimSuffix(endpoint, "/")
	if endpoint == "" {
		return nil, errors.New("blob: S3 needs an endpoint (host:port)")
	}
	if strings.Contains(endpoint, "/") {
		return nil, fmt.Errorf("blob: S3 endpoint %q must be host:port, without a path", c.Endpoint)
	}
	if c.Bucket == "" {
		return nil, errors.New("blob: S3 needs a bucket")
	}
	if err := s3utils.CheckValidBucketNameStrict(c.Bucket); err != nil {
		return nil, fmt.Errorf("blob: S3 bucket %q: %w", c.Bucket, err)
	}
	region := c.Region
	if region == "" {
		region = DefaultRegion
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(c.AccessKey, c.SecretKey, ""),
		Secure: secure,
		Region: region,
	})
	if err != nil {
		return nil, fmt.Errorf("blob: S3 endpoint %q: %w", c.Endpoint, err)
	}
	return &S3{client: client, bucket: c.Bucket, region: region}, nil
}

// ensureBucket creates the bucket unless it exists. Another server creating it at the same moment is fine.
func (s *S3) ensureBucket(ctx context.Context) error {
	ok, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("blob: S3 bucket %s: %w", s.bucket, err)
	}
	if ok {
		return nil
	}
	if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{Region: s.region}); err != nil {
		switch errorCode(err) {
		case "BucketAlreadyOwnedByYou", "BucketAlreadyExists":
			if ok, err2 := s.client.BucketExists(ctx, s.bucket); err2 == nil && ok {
				return nil
			}
		}
		return fmt.Errorf("blob: creating the S3 bucket %s: %w", s.bucket, err)
	}
	return nil
}

// Name implements Store.
func (*S3) Name() string { return "s3" }

// Bucket returns the bucket's name.
func (s *S3) Bucket() string { return s.bucket }

// errorCode returns an S3 error's code ("NoSuchKey"), or "".
func errorCode(err error) string {
	var er minio.ErrorResponse
	if errors.As(err, &er) {
		return er.Code
	}
	return ""
}

// notFound reports whether err says the object isn't there.
func notFound(err error) bool {
	var er minio.ErrorResponse
	if !errors.As(err, &er) {
		return false
	}
	return er.Code == "NoSuchKey" || er.Code == "NotFound" ||
		(er.StatusCode == http.StatusNotFound && er.Code != "NoSuchBucket")
}

// Put implements Store. S3 shows an object only once all of it arrived, so a failed Put leaves the old one.
func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	if err := checkKey(key); err != nil {
		return err
	}
	if _, err := s.client.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType}); err != nil {
		return fmt.Errorf("blob: putting %s: %w", key, err)
	}
	return nil
}

// PutFile implements Store: the file is uploaded in parallel parts when it is large.
func (s *S3) PutFile(ctx context.Context, key, path, contentType string) error {
	if err := checkKey(key); err != nil {
		return err
	}
	if _, err := s.client.FPutObject(ctx, s.bucket, key, path, minio.PutObjectOptions{ContentType: contentType}); err != nil {
		return fmt.Errorf("blob: putting %s: %w", key, err)
	}
	return nil
}

// Get implements Store.
func (s *S3) Get(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}
	if offset < 0 {
		return nil, fmt.Errorf("blob: negative offset %d", offset)
	}
	if length == 0 {
		if _, err := s.Stat(ctx, key); err != nil {
			return nil, err
		}
		return emptyReader(), nil
	}
	var opts minio.GetObjectOptions
	switch {
	case length > 0:
		if err := opts.SetRange(offset, offset+length-1); err != nil {
			return nil, err
		}
	case offset > 0:
		if err := opts.SetRange(offset, 0); err != nil {
			return nil, err
		}
	}
	obj, err := s.client.GetObject(ctx, s.bucket, key, opts)
	if err != nil {
		return nil, s.getFailed(key, err)
	}
	// GetObject is lazy: Stat sends the request, so a missing key shows here rather than on the first read.
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		if errorCode(err) == "InvalidRange" {
			return emptyReader(), nil // the object is there; the range starts at or past its end
		}
		return nil, s.getFailed(key, err)
	}
	return obj, nil
}

func (s *S3) getFailed(key string, err error) error {
	if notFound(err) {
		return ErrNotFound
	}
	return fmt.Errorf("blob: getting %s: %w", key, err)
}

// Stat implements Store.
func (s *S3) Stat(ctx context.Context, key string) (Info, error) {
	if err := checkKey(key); err != nil {
		return Info{}, err
	}
	st, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if notFound(err) {
			return Info{}, ErrNotFound
		}
		return Info{}, fmt.Errorf("blob: stat %s: %w", key, err)
	}
	return Info{Key: key, Size: st.Size, Modified: st.LastModified}, nil
}

// Delete implements Store.
func (s *S3) Delete(ctx context.Context, key string) error {
	if err := checkKey(key); err != nil {
		return err
	}
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil && !notFound(err) {
		return fmt.Errorf("blob: deleting %s: %w", key, err)
	}
	return nil
}

// List implements Store.
func (s *S3) List(ctx context.Context, prefix string) ([]Info, error) {
	if !validPrefix(prefix) {
		return nil, badKey(prefix)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // stops the listing goroutine if we return early
	out := []Info{}
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return nil, fmt.Errorf("blob: listing %s: %w", prefix, obj.Err)
		}
		if strings.HasSuffix(obj.Key, "/") || !ValidKey(obj.Key) {
			continue // a folder marker, or a key we never write
		}
		out = append(out, Info{Key: obj.Key, Size: obj.Size, Modified: obj.LastModified})
	}
	slices.SortFunc(out, func(a, b Info) int { return strings.Compare(a.Key, b.Key) })
	return out, nil
}
