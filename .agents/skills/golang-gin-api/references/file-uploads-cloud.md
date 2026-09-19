# File Uploads — Cloud Storage (S3) & Security Checklist

See also: `file-uploads-local.md`

## S3 / Cloud Storage

Define a storage interface so handlers stay decoupled from the provider:

```go
// internal/storage/storage.go
type FileStorage interface {
    Upload(ctx context.Context, file *multipart.FileHeader) (url string, err error)
    PresignedURL(ctx context.Context, key string, ttl time.Duration) (string, error)
}
```

```go
// internal/storage/s3_storage.go
type S3Storage struct {
    client *s3.Client
    bucket string
    logger *slog.Logger
}

func (s *S3Storage) Upload(ctx context.Context, file *multipart.FileHeader) (string, error) {
    src, err := file.Open()
    if err != nil {
        return "", fmt.Errorf("open file: %w", err)
    }
    defer src.Close()

    key := uuid.NewString() + "_" + filepath.Base(file.Filename)

    _, err = s.client.PutObject(ctx, &s3.PutObjectInput{
        Bucket:      aws.String(s.bucket),
        Key:         aws.String(key),
        Body:        src,
        ContentType: aws.String(file.Header.Get("Content-Type")),
    })
    if err != nil {
        return "", fmt.Errorf("s3 put object: %w", err)
    }

    return fmt.Sprintf("https://%s.s3.amazonaws.com/%s", s.bucket, key), nil
}

func (s *S3Storage) PresignedURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
    presignClient := s3.NewPresignClient(s.client)
    req, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
        Bucket: aws.String(s.bucket),
        Key:    aws.String(key),
    }, s3.WithPresignExpires(ttl))
    if err != nil {
        return "", fmt.Errorf("presign: %w", err)
    }
    return req.URL, nil
}
```

Local filesystem fallback for development — implement `FileStorage` writing to disk.

## Security Checklist

| Risk | Mitigation |
|------|-----------|
| Directory traversal | `filepath.Base(file.Filename)` strips all path components |
| MIME spoofing | Detect MIME from first 512 bytes with `http.DetectContentType`; never trust `Content-Type` header |
| Oversized files | `router.MaxMultipartMemory` + explicit `file.Size` check in handler |
| Filename collision | Prefix with `uuid.NewString()` |
| Serving uploaded files | Store outside webroot; serve through signed URLs or a dedicated file handler |
| Malware | Integrate ClamAV or cloud AV scan before persisting in production |
| Path exposure | Never return filesystem paths; return opaque keys or signed URLs |
