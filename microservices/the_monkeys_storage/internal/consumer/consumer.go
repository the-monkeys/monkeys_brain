package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/the-monkeys/the_monkeys/apis/serviceconn/gateway_file_service/pb"
	"github.com/the-monkeys/the_monkeys/config"
	"github.com/the-monkeys/the_monkeys/constants"
	"github.com/the-monkeys/the_monkeys/logger"
	"github.com/the-monkeys/the_monkeys/microservices/rabbitmq"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_storage/constant"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_storage/internal/database"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_storage/internal/models"
	"go.uber.org/zap"
)

// CAS owner-type constants must mirror the values used by the Gateway in
// microservices/the_monkeys_gateway/internal/storage_v2/routes.go so that
// reference lookups across services agree.
const (
	ownerTypeBlog    = "blog"
	ownerTypeProfile = "profile"
	ownerTypeEvent   = "event"
	ownerTypeGroup   = "group"
)

func ConsumeFromQueue(mgr *rabbitmq.ConnManager, conf config.RabbitMQ, db database.StorageDB, log *zap.SugaredLogger) {
	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		logger.ZapSugar().Debug("Storage consumer: received termination signal, exiting")
		os.Exit(0)
	}()

	// Load config and init a single MinIO client for all handlers
	cfg, err := config.GetConfig()
	if err != nil {
		log.Errorf("Failed to load storage config: %v", err)
	}
	var mc *minio.Client
	if cfg != nil {
		mc, err = minio.New(cfg.Minio.Endpoint, &minio.Options{
			Creds:  credentials.NewStaticV4(cfg.Minio.AccessKey, cfg.Minio.SecretKey, ""),
			Secure: cfg.Minio.UseSSL,
		})
		if err != nil {
			log.Errorf("Failed to initialize MinIO client: %v", err)
		}
	}

	go consumeQueue(mgr, conf.Queues[0], log, cfg, mc, db)
	go consumeQueue(mgr, conf.Queues[2], log, cfg, mc, db)

	select {}
}

func consumeQueue(mgr *rabbitmq.ConnManager, queueName string, log *zap.SugaredLogger, cfg *config.Config, mc *minio.Client, db database.StorageDB) {
	backoff := time.Second

	for {
		msgs, err := mgr.Channel().Consume(
			queueName,
			"",
			true,
			false,
			false,
			false,
			nil,
		)
		if err != nil {
			logger.ZapSugar().Errorf("Storage consumer: failed to register on queue '%s', reconnecting in %v: %v", queueName, backoff, err)
			time.Sleep(backoff)
			if backoff *= 2; backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			mgr.Reconnect()
			continue
		}

		backoff = time.Second
		logger.ZapSugar().Infof("Storage consumer: registered on queue '%s'", queueName)

		for d := range msgs {
			user := models.TheMonkeysMessage{}
			if err := json.Unmarshal(d.Body, &user); err != nil {
				logger.ZapSugar().Errorf("Failed to unmarshal user from RabbitMQ: %v", err)
				continue
			}
			log.Debugw("Storage consumer: message received",
				"queue", queueName,
				"action", user.Action,
				"username", user.Username,
				"account_id", user.AccountId,
				"blog_id", user.BlogId,
				"blog_ids_count", len(user.BlogIds),
			)
			handleUserAction(user, log, cfg, mc, db)
		}

		// Channel closed — reconnect
		logger.ZapSugar().Warn("Storage consumer: channel closed, reconnecting...")
		mgr.Reconnect()
	}
}

func minioBucketForObjectKey(publicBucket, verificationBucket, objectKey string) string {
	if strings.HasPrefix(objectKey, "verifications/") {
		return verificationBucket
	}
	return publicBucket
}

const defaultVerificationBucket = "the-monkeys-verification"

// softDeleteAssetRefs soft-deletes every active CAS asset reference for the
// given (ownerType, ownerId). It is a no-op if db is nil or ownerId is blank.
// Last-ref GC deletes MinIO objects listed in orphans.
func softDeleteAssetRefs(db database.StorageDB, log *zap.SugaredLogger, cfg *config.Config, mc *minio.Client, ownerType, ownerId string) {
	if db == nil || strings.TrimSpace(ownerId) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := db.DeleteAssetRef(ctx, &pb.DeleteAssetRefReq{
		OwnerType: ownerType,
		OwnerId:   ownerId,
	})
	if err != nil {
		log.Errorw("Failed to soft-delete asset refs", "owner_type", ownerType, "owner_id", ownerId, "err", err)
		return
	}
	log.Debugw("Soft-deleted asset refs", "owner_type", ownerType, "owner_id", ownerId, "deleted_count", res.GetDeletedCount())

	if mc == nil || cfg == nil {
		return
	}
	verBucket := strings.TrimSpace(cfg.Minio.VerificationBucket)
	if verBucket == "" {
		verBucket = defaultVerificationBucket
	}
	for _, o := range res.GetOrphans() {
		if o == nil {
			continue
		}
		key := o.GetObjectKey()
		if key == "" {
			continue
		}
		bucket := minioBucketForObjectKey(cfg.Minio.Bucket, verBucket, key)
		if rmErr := mc.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{}); rmErr != nil {
			log.Errorw("Failed to remove orphaned MinIO object", "bucket", bucket, "object_key", key, "err", rmErr)
		}
	}
}

func handleUserAction(user models.TheMonkeysMessage, log *zap.SugaredLogger, cfg *config.Config, mc *minio.Client, db database.StorageDB) {
	switch user.Action {
	case constants.USER_REGISTER:
		log.Debugw("Handling USER_REGISTER", "username", user.Username)

		// Fetch default profile image: try remote URL, fall back to bundled local copy
		imageBytes, err := readImageFromURL(constant.DefaultProfilePhoto)
		if err != nil {
			log.Warnw("Remote profile photo fetch failed, using bundled fallback",
				"username", user.Username, "remote_err", err)
			imageBytes, err = os.ReadFile(constant.DefaultProfilePhotoLocal)
			if err != nil {
				log.Errorw("Failed to read bundled fallback profile photo",
					"username", user.Username, "path", constant.DefaultProfilePhotoLocal, "err", err)
				break
			}
		}

		// Upload to MinIO
		if mc != nil && cfg != nil {
			objectKey := "profiles/" + user.Username + "/profile.svg"
			log.Debugw("Uploading default profile photo (MinIO)", "username", user.Username, "object_key", objectKey)
			reader := bytes.NewReader(imageBytes)
			_, putErr := mc.PutObject(context.Background(), cfg.Minio.Bucket, objectKey, reader, int64(len(imageBytes)), minio.PutObjectOptions{
				ContentType: "image/svg+xml",
			})
			if putErr != nil {
				log.Errorw("Failed to upload profile photo to MinIO", "username", user.Username, "err", putErr)
			} else {
				log.Debugw("Uploaded default profile photo (MinIO)", "username", user.Username, "object_key", objectKey)
			}
		}

		log.Debugw("USER_REGISTER done", "username", user.Username)

	case constants.USERNAME_UPDATE:
		log.Debugw("Handling USERNAME_UPDATE",
			"old_username", user.Username,
			"new_username", user.NewUsername,
		)

		if mc != nil && cfg != nil {
			log.Debugw("Renaming user profile folder (MinIO)",
				"from_prefix", "profiles/"+user.Username+"/",
				"to_prefix", "profiles/"+user.NewUsername+"/",
			)
			if err := UpdateMinioProfileFolder(context.Background(), mc, cfg.Minio.Bucket, user.Username, user.NewUsername); err != nil {
				log.Errorw("Failed to rename MinIO profile folder", "from", user.Username, "to", user.NewUsername, "err", err)
			} else {
				log.Debugw("Renamed user profile folder (MinIO)", "from", user.Username, "to", user.NewUsername)
			}
		}

		log.Debugw("USERNAME_UPDATE done", "old_username", user.Username, "new_username", user.NewUsername)

	case constants.USER_ACCOUNT_DELETE:
		log.Debugw("Handling USER_ACCOUNT_DELETE",
			"username", user.Username,
			"account_id", user.AccountId,
			"blog_ids_count", len(user.BlogIds),
			"blog_ids", user.BlogIds,
		)

		// 0. Soft-delete CAS asset references owned by this profile.
		softDeleteAssetRefs(db, log, cfg, mc, ownerTypeProfile, user.Username)

		// 1. Delete profile folder from MinIO (legacy path-based objects only).
		if mc != nil && cfg != nil {
			log.Debugw("Deleting user profile folder (MinIO)", "username", user.Username, "prefix", "profiles/"+user.Username+"/")
			if err := DeleteMinioProfileFolder(context.Background(), mc, cfg.Minio.Bucket, user.Username); err != nil {
				log.Errorw("Failed to delete MinIO profile folder", "username", user.Username, "err", err)
			} else {
				log.Debugw("Deleted user profile folder (MinIO)", "username", user.Username)
			}
		}

		// 2. Delete blog files for each owned blog from FS + MinIO
		for i, blogId := range user.BlogIds {
			log.Debugw("Deleting blog files", "blog_index", i+1, "total", len(user.BlogIds), "blog_id", blogId)

			// Soft-delete CAS asset references owned by this blog.
			softDeleteAssetRefs(db, log, cfg, mc, ownerTypeBlog, blogId)

			// FS: blogs/{blogId}/ (legacy)
			if err := DeleteBlogFolder(blogId); err != nil {
				log.Errorw("Failed to delete blog folder (FS)", "blog_id", blogId, "err", err)
			} else {
				log.Debugw("Deleted blog folder (FS)", "blog_id", blogId)
			}

			// MinIO: posts/{blogId}/ (legacy path-based objects only;
			// last-ref GC deletes MinIO objects listed in orphans).
			if mc != nil && cfg != nil {
				if err := DeleteMinioBlogFolder(context.Background(), mc, cfg.Minio.Bucket, blogId); err != nil {
					log.Errorw("Failed to delete blog folder (MinIO)", "blog_id", blogId, "err", err)
				} else {
					log.Debugw("Deleted blog folder (MinIO)", "blog_id", blogId, "prefix", "posts/"+blogId+"/")
				}
			}
		}

		log.Debugw("USER_ACCOUNT_DELETE done",
			"username", user.Username,
			"blogs_processed", len(user.BlogIds),
		)
	case constants.BLOG_DELETE:
		log.Debugw("Handling BLOG_DELETE", "blog_id", user.BlogId)

		// Soft-delete every active CAS asset reference owned by this blog.
		softDeleteAssetRefs(db, log, cfg, mc, ownerTypeBlog, user.BlogId)

		log.Debugw("Deleting blog folder (FS)", "blog_id", user.BlogId)
		if err := DeleteBlogFolder(user.BlogId); err != nil {
			log.Errorw("Failed to delete blog folder (FS)", "blog_id", user.BlogId, "err", err)
		} else {
			log.Debugw("Deleted blog folder (FS)", "blog_id", user.BlogId)
		}

		// Legacy posts/{blogId}/ prefix only. Last-ref GC deletes MinIO objects listed in orphans.
		if mc != nil && cfg != nil {
			log.Debugw("Deleting blog folder (MinIO)", "blog_id", user.BlogId, "prefix", "posts/"+user.BlogId+"/")
			if err := DeleteMinioBlogFolder(context.Background(), mc, cfg.Minio.Bucket, user.BlogId); err != nil {
				log.Errorw("Failed to delete MinIO blog folder", "blog_id", user.BlogId, "err", err)
			} else {
				log.Debugw("Deleted blog folder (MinIO)", "blog_id", user.BlogId)
			}
		}

		log.Debugw("BLOG_DELETE done", "blog_id", user.BlogId)
	case constants.EVENT_DELETE:
		slug := user.EventSlug
		log.Debugw("Handling EVENT_DELETE", "event_slug", slug)
		softDeleteAssetRefs(db, log, cfg, mc, ownerTypeEvent, slug)
		if prefix, ok := entityStoragePrefix("events", slug); ok && mc != nil && cfg != nil {
			if err := DeleteMinioPrefix(context.Background(), mc, cfg.Minio.Bucket, prefix); err != nil {
				log.Errorw("Failed to delete event folder (MinIO)", "event_slug", slug, "err", err)
			} else {
				log.Debugw("Deleted event folder (MinIO)", "event_slug", slug, "prefix", prefix)
			}
		} else if slug != "" && (mc == nil || cfg == nil) {
			log.Debugw("EVENT_DELETE skipped MinIO (client not configured)", "event_slug", slug)
		} else if slug != "" {
			log.Errorw("EVENT_DELETE rejected unsafe slug", "event_slug", slug)
		}
		log.Debugw("EVENT_DELETE done", "event_slug", slug)
	case constants.GROUP_DELETE:
		slug := user.GroupSlug
		log.Debugw("Handling GROUP_DELETE", "group_slug", slug)
		softDeleteAssetRefs(db, log, cfg, mc, ownerTypeGroup, slug)
		if prefix, ok := entityStoragePrefix("groups", slug); ok && mc != nil && cfg != nil {
			if err := DeleteMinioPrefix(context.Background(), mc, cfg.Minio.Bucket, prefix); err != nil {
				log.Errorw("Failed to delete group folder (MinIO)", "group_slug", slug, "err", err)
			} else {
				log.Debugw("Deleted group folder (MinIO)", "group_slug", slug, "prefix", prefix)
			}
		} else if slug != "" && (mc == nil || cfg == nil) {
			log.Debugw("GROUP_DELETE skipped MinIO (client not configured)", "group_slug", slug)
		} else if slug != "" {
			log.Errorw("GROUP_DELETE rejected unsafe slug", "group_slug", slug)
		}
		log.Debugw("GROUP_DELETE done", "group_slug", slug)
	default:
		log.Errorw("Unknown action", "action", user.Action)
	}
}

// func CreateUserFolder(userName string) error {
// 	dirPath, filePath := ConstructPath(constant.ProfileDir, userName, "profile.png")
// 	if err := os.MkdirAll(dirPath, 0755); err != nil {
// 		logger.ZapSugar().Errorf("Cannot create directory structure for user: %s, error: %v", userName, err)
// 		return err
// 	}
// 	imageByte, err := readImageFromURL(constant.DefaultProfilePhoto)
// 	if err != nil {
// 		logger.ZapSugar().Errorf("Error fetching image for user: %s, error: %v", userName, err)
// 		return fmt.Errorf("error fetching image: %v", err)
// 	}
// 	if err = os.WriteFile(filePath, imageByte, 0644); err != nil {
// 		logger.ZapSugar().Errorf("Cannot write profile image file for user: %s, error: %v", userName, err)
// 		return err
// 	}
// 	logger.ZapSugar().Debugf("Done uploading profile pic: %s", filePath)
// 	return nil
// }

func readImageFromURL(url string) ([]byte, error) {
	client := http.Client{}

	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("Error closing response body: %v", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP request failed with status code %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %v", err)
	}

	return data, nil
}

func ConstructPath(baseDir, userName, fileName string) (string, string) {
	dirPath := filepath.Join(baseDir, userName)
	filePath := filepath.Join(dirPath, fileName)
	return dirPath, filePath
}

// func UpdateUserFolder(currentName, newName string) error {
// 	currentPath := filepath.Join(constant.ProfileDir, currentName)
// 	newPath := filepath.Join(constant.ProfileDir, newName)

// 	log.Printf("updating user folder %s to %s", currentName, newName)

// 	from, err := os.Stat(currentPath)
// 	if err != nil {
// 		return errors.New("could not stat current directory: " + err.Error())
// 	}

// 	if !from.IsDir() {
// 		return errors.New(currentPath + " is not a directory")
// 	}

// 	to := currentPath + "_temp"

// 	err = os.Rename(currentPath, to)
// 	if err != nil {
// 		return errors.New("failed to rename directory: " + err.Error())
// 	}

// 	err = os.Rename(to, newPath)
// 	if err != nil {
// 		return errors.New("failed to rename directory to new name: " + err.Error())
// 	}

// 	return nil
// }

// UpdateMinioProfileFolder renames the object prefix in MinIO from
// profiles/{oldName}/ -> profiles/{newName}/ by copying each object then deleting the old one.
func UpdateMinioProfileFolder(ctx context.Context, mc *minio.Client, bucket, oldName, newName string) error {
	oldPrefix := "profiles/" + strings.Trim(oldName, "/") + "/"
	newPrefix := "profiles/" + strings.Trim(newName, "/") + "/"
	if oldPrefix == newPrefix {
		return nil
	}
	// List all objects under the old prefix
	for obj := range mc.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: oldPrefix, Recursive: true}) {
		if obj.Err != nil {
			return fmt.Errorf("list objects failed: %w", obj.Err)
		}
		srcKey := obj.Key
		dstKey := strings.Replace(srcKey, oldPrefix, newPrefix, 1)
		// Copy to destination key
		_, err := mc.CopyObject(ctx,
			minio.CopyDestOptions{Bucket: bucket, Object: dstKey},
			minio.CopySrcOptions{Bucket: bucket, Object: srcKey},
		)
		if err != nil {
			return fmt.Errorf("copy %s -> %s failed: %w", srcKey, dstKey, err)
		}
		// Remove the old object
		if err := mc.RemoveObject(ctx, bucket, srcKey, minio.RemoveObjectOptions{}); err != nil {
			return fmt.Errorf("remove old object %s failed: %w", srcKey, err)
		}
	}
	return nil
}

// DeleteMinioPrefix removes all objects under the given prefix.
func DeleteMinioPrefix(ctx context.Context, mc *minio.Client, bucket, prefix string) error {
	for obj := range mc.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return fmt.Errorf("list objects failed: %w", obj.Err)
		}
		if err := mc.RemoveObject(ctx, bucket, obj.Key, minio.RemoveObjectOptions{}); err != nil {
			return fmt.Errorf("remove object %s failed: %w", obj.Key, err)
		}
	}
	return nil
}

func DeleteMinioProfileFolder(ctx context.Context, mc *minio.Client, bucket, username string) error {
	prefix := "profiles/" + strings.Trim(username, "/") + "/"
	return DeleteMinioPrefix(ctx, mc, bucket, prefix)
}

func DeleteMinioBlogFolder(ctx context.Context, mc *minio.Client, bucket, blogId string) error {
	prefix := "posts/" + strings.Trim(blogId, "/") + "/"
	return DeleteMinioPrefix(ctx, mc, bucket, prefix)
}

func DeleteUserFolder(userName string) error {
	dirPath := filepath.Join(constant.ProfileDir, userName)

	err := os.RemoveAll(dirPath)
	if err != nil {
		return errors.New("failed to remove directory: " + err.Error())
	}

	return nil
}

func DeleteBlogFolder(blogId string) error {
	dirPath := filepath.Join(constant.BlogDir, blogId)

	err := os.RemoveAll(dirPath)
	if err != nil {
		return errors.New("failed to remove directory: " + err.Error())
	}

	return nil
}
