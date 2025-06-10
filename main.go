package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/fsnotify/fsnotify"
)

// Version can be set at build time
var Version = "dev"

// S3Uploader defines the interface for S3 operations.
// Using an interface allows us to mock S3 interactions for easy testing.
type S3Uploader interface {
	Upload(ctx context.Context, input *s3.PutObjectInput) (*s3.PutObjectOutput, error)
	DeleteObject(ctx context.Context, input *s3.DeleteObjectInput) (*s3.DeleteObjectOutput, error)
}

// S3Client is a wrapper for the official AWS S3 client that implements our S3Uploader interface.
type S3Client struct {
	client *s3.Client
}

// Upload uploads a file to an S3 bucket.
func (c *S3Client) Upload(ctx context.Context, input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
	return c.client.PutObject(ctx, input)
}

// DeleteObject deletes an object from an S3 bucket.
func (c *S3Client) DeleteObject(ctx context.Context, input *s3.DeleteObjectInput) (*s3.DeleteObjectOutput, error) {
	return c.client.DeleteObject(ctx, input)
}

// newS3Client creates a new S3 client wrapper.
func newS3Client(ctx context.Context) (*S3Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to load AWS SDK config: %w", err)
	}
	return &S3Client{client: s3.NewFromConfig(cfg)}, nil
}

// App holds the application's configuration and dependencies.
type App struct {
	uploader     S3Uploader
	localPath    string
	bucket       string
	keyPrefix    string
	delete       bool
	storageClass types.StorageClass
}

// main is the entry point of the application.
func main() {
	// Define and parse command-line flags
	deleteFlag := flag.Bool("delete", false, "Delete files in S3 when they are deleted locally.")
	storageClassFlag := flag.String("storage-class", string(types.StorageClassIntelligentTiering), "Specify the S3 storage class (e.g., STANDARD, GLACIER).")
	versionFlag := flag.Bool("version", false, "Print the echos3 version and exit.")
	flag.Parse()

	if *versionFlag {
		fmt.Println("echos3 version", Version)
		os.Exit(0)
	}

	// Validate command-line arguments
	args := flag.Args()
	if len(args) != 2 {
		log.Fatal("Usage: echos3 /path/to/watch s3://bucket/key [--delete] [--storage-class STORAGE_CLASS]")
	}

	localPath, err := filepath.Abs(args[0])
	if err != nil {
		log.Fatalf("Invalid local path: %v", err)
	}
	s3Path := args[1]

	bucket, keyPrefix, err := parseS3Path(s3Path)
	if err != nil {
		log.Fatalf("Invalid S3 path: %v", err)
	}

	// Create the S3 client
	ctx := context.Background()
	s3Client, err := newS3Client(ctx)
	if err != nil {
		log.Fatalf("Failed to create S3 client: %v", err)
	}

	// Create and run the application
	app := &App{
		uploader:     s3Client,
		localPath:    localPath,
		bucket:       bucket,
		keyPrefix:    keyPrefix,
		delete:       *deleteFlag,
		storageClass: types.StorageClass(*storageClassFlag),
	}

	if err := app.run(ctx); err != nil {
		log.Fatalf("Application error: %v", err)
	}
}

// run starts the file watcher and handles events.
func (a *App) run(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("could not create file watcher: %w", err)
	}
	defer watcher.Close()

	// Initial sync: walk the local path and add all directories to the watcher.
	log.Printf("Performing initial scan of %s...", a.localPath)
	err = filepath.Walk(a.localPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := watcher.Add(path); err != nil {
				return fmt.Errorf("failed to add path to watcher %s: %w", path, err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("error during initial directory scan: %w", err)
	}

	log.Printf("Watching %s for changes. Uploading to s3://%s/%s", a.localPath, a.bucket, a.keyPrefix)

	// Main event loop
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			a.handleEvent(ctx, event, watcher)
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			log.Printf("Watcher error: %v", err)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// handleEvent processes a single file system event.
func (a *App) handleEvent(ctx context.Context, event fsnotify.Event, watcher *fsnotify.Watcher) {
	relPath, err := filepath.Rel(a.localPath, event.Name)
	if err != nil {
		log.Printf("Could not determine relative path for %s: %v", event.Name, err)
		return
	}
	s3Key := filepath.ToSlash(filepath.Join(a.keyPrefix, relPath))

	op := event.Op
	// Handle writes, creates, and renames as upload events
	if op&fsnotify.Write == fsnotify.Write || op&fsnotify.Create == fsnotify.Create || op&fsnotify.Rename == fsnotify.Rename {
		// Wait a moment to handle rapid writes (e.g., from editors saving)
		time.Sleep(100 * time.Millisecond)
		info, err := os.Stat(event.Name)
		if err != nil {
			if os.IsNotExist(err) {
				// This can happen if a file is created and deleted quickly (e.g., temp files)
				// Or if it was a rename source, which we handle as a delete below.
				a.handleRemove(ctx, s3Key)
			} else {
				log.Printf("Error stating file %s: %v", event.Name, err)
			}
			return
		}

		if info.IsDir() {
			if err := watcher.Add(event.Name); err != nil {
				log.Printf("Failed to add new directory to watcher %s: %v", event.Name, err)
			}
			log.Printf("Now watching new directory: %s", event.Name)
		} else {
			a.handleUpload(ctx, event.Name, s3Key)
		}
	} else if op&fsnotify.Remove == fsnotify.Remove {
		// The file is gone, so we don't need to check if it's a directory
		a.handleRemove(ctx, s3Key)
		// We don't need to explicitly remove from watcher, fsnotify handles it.
	}
}

// handleUpload uploads a single file to S3.
func (a *App) handleUpload(ctx context.Context, localFile, s3Key string) {
	file, err := os.Open(localFile)
	if err != nil {
		log.Printf("Could not open file for upload %s: %v", localFile, err)
		return
	}
	defer file.Close()

	log.Printf("Uploading %s to s3://%s/%s", localFile, a.bucket, s3Key)
	input := &s3.PutObjectInput{
		Bucket:       aws.String(a.bucket),
		Key:          aws.String(s3Key),
		Body:         file,
		StorageClass: a.storageClass,
	}

	_, err = a.uploader.Upload(ctx, input)
	if err != nil {
		log.Printf("Failed to upload %s: %v", localFile, err)
	}
}

// handleRemove deletes a single object from S3 if the --delete flag is set.
func (a *App) handleRemove(ctx context.Context, s3Key string) {
	if !a.delete {
		log.Printf("File removed locally but --delete is not set. Ignoring: %s", s3Key)
		return
	}

	log.Printf("Deleting s3://%s/%s", a.bucket, s3Key)
	input := &s3.DeleteObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(s3Key),
	}
	_, err := a.uploader.DeleteObject(ctx, input)
	if err != nil {
		log.Printf("Failed to delete %s from S3: %v", s3Key, err)
	}
}

// parseS3Path parses an S3 path string (e.g., "s3://bucket/key/prefix")
// into a bucket and a key prefix.
func parseS3Path(s3Path string) (bucket, keyPrefix string, err error) {
	if !strings.HasPrefix(s3Path, "s3://") {
		return "", "", errors.New("S3 path must start with s3://")
	}
	trimmed := strings.TrimPrefix(s3Path, "s3://")
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return "", "", errors.New("invalid S3 path format: missing bucket name")
	}

	bucket = parts[0]
	if len(parts) > 1 {
		keyPrefix = parts[1]
	}

	return bucket, keyPrefix, nil
}
